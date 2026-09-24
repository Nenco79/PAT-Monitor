package rtc

import "testing"

// It rounds **up**, which is the safe direction: declaring more than the truth
// spends slightly less than allowed, declaring less overshoots the cap.
// The scale's cadences are values this list knows.
//
// Nothing depends on it today — when the scale imposes a cadence it is the cap
// that commands, and `cadenceToDeclare` is not consulted — so this guard is
// about the vocabulary and says so. The two lists describe the same idea, they
// diverged once while one held divisors and the other values, and what it took
// to notice was printing the ladder. A cap the scale can impose is a number the
// declaration can also say.
func TestTheScaleOnlyImposesCadencesThisListKnows(t *testing.T) {
	known := make(map[int]bool, len(cadenceSteps))
	for _, s := range cadenceSteps {
		known[s] = true
	}
	for _, f := range scaleCadences {
		if !known[f] {
			t.Errorf("the scale can impose %d fps, which is not a declarable step: %v", f, cadenceSteps)
		}
	}
	if !known[scaleMinFPS] {
		t.Errorf("the floor of %d fps is not a declarable step: %v", scaleMinFPS, cadenceSteps)
	}
}

func TestItRoundsUp(t *testing.T) {
	cases := []struct {
		measured float64
		want     int
	}{
		{18.4, 20}, // the case measured live in dim light
		{13.7, 15},
		{9.1, 10},
		{29.8, 30},
		{30.0, 30},
		{31.0, 30}, // never above the cap
	}
	for _, c := range cases {
		if got := cadenceToDeclare(c.measured, 30); got != c.want {
			t.Errorf("with %.1f fps measured, %d is declared instead of %d", c.measured, got, c.want)
		}
	}
}

// With no measurement nothing is invented: the maximum is declared, which is the
// waste we come from — never the risk of overshooting.
func TestWithNoMeasurementTheMaximumIsDeclared(t *testing.T) {
	if got := cadenceToDeclare(0, 30); got != 30 {
		t.Errorf("with no measured cadence %d is declared instead of the maximum", got)
	}
}

// Coming down is the operation that saves, and it is done calmly: the light
// flickers, and without the wait a passing cloud would rebuild the encoder.
func TestComingDownTakesTime(t *testing.T) {
	g := newCadenceGovernor(30)
	for i := range cadenceConfirmDown - 1 {
		if fps, changed := g.target(18.4, 30); changed || fps != 30 {
			t.Fatalf("came down after only %d samples, to %d", i+1, fps)
		}
	}
	fps, changed := g.target(18.4, 30)
	if !changed || fps != 20 {
		t.Errorf("after %d samples at 18.4 fps it should have declared 20, instead %d (changed=%v)",
			cadenceConfirmDown, fps, changed)
	}
}

// Going up, on the other hand, is the operation that protects: if the camera
// picks up speed and the encoder believes it is going slower, every frame
// receives too much and the cap is overshot. Few confirmations.
func TestGoingUpIsQuicker(t *testing.T) {
	// It is reached by the real road rather than from the constructor: the
	// governor is born with declared and cap equal, and to have one below the cap
	// the camera has to have gone slower. Building it at 15 with the cap at 30
	// would mean testing a state that does not exist in the program, and on this
	// side of the file the difference matters: a cap that rises now has a precise
	// meaning.
	g := newCadenceGovernor(30)
	for range cadenceConfirmDown {
		g.target(14.0, 30)
	}
	if g.declared != 15 {
		t.Fatalf("setup: it should have declared 15, instead %d", g.declared)
	}
	for i := range cadenceConfirmUp - 1 {
		if _, changed := g.target(28.0, 30); changed {
			t.Fatalf("went up on the first sample: %d", i+1)
		}
	}
	fps, changed := g.target(28.0, 30)
	if !changed || fps != 30 {
		t.Errorf("with 28 fps measured it should have declared 30, instead %d (changed=%v)", fps, changed)
	}
	if cadenceConfirmUp >= cadenceConfirmDown {
		t.Error("going up must be quicker than coming down: it is the half that protects")
	}
}

// A small deviation moves nothing: rebuilding the encoder costs, and below a
// tenth the gain does not pay for it.
func TestASmallDeviationDoesNotRebuildTheEncoder(t *testing.T) {
	g := newCadenceGovernor(30)
	for i := range 30 {
		if _, changed := g.target(28.0, 30); changed {
			t.Fatalf("rebuilt the encoder for 28 fps against 30 declared, at sample %d", i+1)
		}
	}
}

// And the cadence the scale imposes commands at once: when it comes down to the
// bottom steps it is already dropping frames, and declaring more would mean
// dividing the budget by a cadence that does not exist.
func TestTheScalesCapCommandsAtOnce(t *testing.T) {
	g := newCadenceGovernor(30)
	fps, changed := g.target(20.0, 5)
	if !changed || fps != 5 {
		t.Errorf("the scale imposes 5 fps and %d is declared (changed=%v)", fps, changed)
	}
}

// Light that comes and goes must not make the declaration oscillate: it is the
// fault this governor exists to avoid, and from life it does not reproduce twice
// alike.
func TestFlickeringLightDoesNotMakeItOscillate(t *testing.T) {
	g := newCadenceGovernor(30)
	changes := 0
	for i := range 60 {
		measured := 21.0
		if i%2 == 0 {
			measured = 19.0 // either side of the border between 20 and 24
		}
		if _, changed := g.target(measured, 30); changed {
			changes++
		}
	}
	if changes > 1 {
		t.Errorf("%d encoder rebuilds for a cadence swinging by two frames", changes)
	}
}

// TestTheBorderDoesNotBounce is the real sequence read in the dark: the camera
// delivered 9.8 fps and the next sample a round 10.0. Without the tolerance,
// "the first step not below" went from 10 to 12 and back — three encoder
// rebuilds in a minute over two tenths of a frame.
func TestTheBorderDoesNotBounce(t *testing.T) {
	g := newCadenceGovernor(30)
	// First it comes down to 10, which is the ordinary case in the dark.
	for range cadenceConfirmDown + 1 {
		g.target(9.8, 30)
	}
	if g.declared != 10 {
		t.Fatalf("with 9.8 fps measured it should have declared 10, instead %d", g.declared)
	}

	// And now the cadence swings around the border, as it really does.
	changes := 0
	for i := range 60 {
		m := 9.8
		if i%3 == 0 {
			m = 10.0
		}
		if _, changed := g.target(m, 30); changed {
			changes++
		}
	}
	if changes > 0 {
		t.Errorf("%d encoder rebuilds over two tenths of a frame on the border", changes)
	}
}

// TestARisingCapFreesTheCadence is the fault seen on wifi.
//
// The scale had come down to the floor of 2 fps. The gate delivered 2, so the
// **measured** cadence was 2.1 — ours, not the camera's. When the bandwidth came
// back and the scale raised the cap, the declaration stayed at 2 because it is
// computed from the measurement, and since it was the declaration that commanded
// the gate, the gate stayed at 2: six minutes at two frames per second with the
// estimate at 2696 kbit/s, and the resolution climbing back to 1280x720 in the
// meantime.
//
// **A loop closes when the measurement describes one's own previous decision.**
// The remedy is in two parts, and one is needed on each side: the gate is
// commanded by the scale and not by the declaration (in internal/pipeline), and a
// cap that rises frees the declaration instead of inheriting a measurement that
// by now speaks about us.
func TestARisingCapFreesTheCadence(t *testing.T) {
	g := newCadenceGovernor(30)

	// The scale comes down to the bottom steps and the gate starts dropping.
	if fps, _ := g.target(30.0, 2); fps != 2 {
		t.Fatalf("with the scale at 2 fps, %d is declared", fps)
	}
	// From here on the measured cadence is the one we impose.
	for range 20 {
		g.target(2.1, 2)
	}

	// The bandwidth comes back and the scale climbs a step.
	fps, changed := g.target(2.1, 6)
	if !changed || fps != 6 {
		t.Fatalf("the cap rose to 6 and %d is still declared (changed=%v): "+
			"the measurement of 2.1 is ours, not the camera's", fps, changed)
	}

	// And all the way to the top, one step at a time as the scale does.
	if fps, changed := g.target(6.2, 30); !changed || fps != 30 {
		t.Fatalf("back at the preset, %d is declared (changed=%v)", fps, changed)
	}
}

// But a cap that **comes down** frees nothing: there the measurement still
// holds, and declaring more than the gate delivers would divide the budget by
// frames that will never arrive.
func TestACapComingDownFreesNothing(t *testing.T) {
	g := newCadenceGovernor(30)
	for range cadenceConfirmDown {
		g.target(9.8, 30)
	}
	if g.declared != 10 {
		t.Fatalf("setup: it should have declared 10, instead %d", g.declared)
	}
	// The scale comes down to 15: the declared one already sits below, so it is
	// not touched.
	if fps, changed := g.target(9.8, 15); changed || fps != 10 {
		t.Errorf("the cap at 15 should not have moved the declared one from 10, instead %d (changed=%v)",
			fps, changed)
	}
	// Below the declared one, on the other hand, it commands at once.
	if fps, changed := g.target(9.8, 5); !changed || fps != 5 {
		t.Errorf("the cap at 5 should have commanded at once, instead %d (changed=%v)", fps, changed)
	}
}
