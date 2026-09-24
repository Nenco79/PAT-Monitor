package rtc

import (
	"testing"
	"time"
)

// The scale is tested here and not over a network: it would otherwise take hours
// of bandwidth getting worse and better on command, and that is exactly what
// cannot be reproduced twice alike.

// testLimits are the thresholds with the values this machine really produces:
// reference 30, break at 38, climb below 33. The tests pass them explicitly,
// because what is being checked is the **rule**, not the value.
var testLimits = qpLimits{known: true, breakAt: 30 + qpBreakSpan, climbAt: 30 + qpClimbSpan}

// withoutQP calls the scale as it is called when the encoder does not declare
// the quantiser. It is the fallback, and it is the case these tests are about:
// the bandwidth. The tests for the real criterion, the QP, are further down.
func withoutQP(g *scaleGovernor, kbps int, now time.Time) (int, int, bool) {
	w, h, _, ch := g.target(kbps, true, 0, qpLimits{}, true, now)
	return w, h, ch
}

// echoOfUs calls the scale with an estimate that is **not credible**: it is what
// happens under the quality saving, where the encoder produces far less than the
// cap and gcc settles on that little.
func echoOfUs(g *scaleGovernor, kbps int, now time.Time) (int, int, bool) {
	w, h, _, ch := g.target(kbps, false, 0, qpLimits{}, true, now)
	return w, h, ch
}

func TestTheScaleComesDownAndClimbsBack(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// With the preset's bandwidth it stays at the full size.
	if w, h, changed := withoutQP(g, 2500, now); changed || w != 1280 || h != 720 {
		t.Fatalf("with plenty of bandwidth it changed: %dx%d changed=%v", w, h, changed)
	}

	// Bandwidth that no longer holds 720p but does hold the step below: it comes
	// down by one only. The value is chosen **between** the two thresholds,
	// otherwise the right descent would be longer than one step.
	//
	// A small shortfall wants a few confirmation samples, so it insists: that is
	// the wanted behaviour, not a delay to be worked around.
	under := g.steps[1].MinKbps + 10
	var h int
	for range scaleConfirmSamples + 2 {
		now = now.Add(time.Second)
		_, h, _ = withoutQP(g, under, now)
	}
	if h != g.steps[1].Height {
		t.Fatalf("first step: height %d, want %d", h, g.steps[1].Height)
	}

	// Below the last step it goes no further: the scale ends, and what to do
	// below is another decision, not one more step.
	now = now.Add(scaleDwell + time.Second)
	if _, _, changed := withoutQP(g, 10, now); !changed {
		t.Error("it did not come down to the last step")
	}
	now = now.Add(scaleDwell + time.Second)
	if _, _, changed := withoutQP(g, 1, now); changed {
		t.Error("it invented a step below the last")
	}
}

// The defect this rule exists to avoid: on a real collapse it goes **straight**
// to the right step, without passing through the ones in between.
//
// Coming down in stages leaves the picture blocky for the whole length of the
// stages still to come — that is, it produces with a rule of ours exactly the
// fault the scale exists to remove.
func TestTheScaleComesDownInOneGoOnACollapse(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// Good bandwidth first, otherwise there is no fall: the figure of a bandwidth
	// that has yet to grow and that of one that has just collapsed are identical.
	withoutQP(g, 2500, now)
	now = now.Add(time.Second)

	// From 2500 to 300: the bandwidth does not hold even the smallest step.
	_, h, changed := withoutQP(g, 300, now)
	if !changed {
		t.Fatal("it did not come down")
	}
	if last := g.steps[len(g.steps)-1].Height; h != last {
		t.Errorf("it came down to %d instead of the last step (%d): "+
			"on a collapse the intermediate stages are guaranteed blockiness", h, last)
	}
}

// gcc's estimate starts cautious and climbs for some ten seconds. Passing through
// the 720p threshold it brought the scale down halfway up the climb, only to
// climb back a few seconds later: whoever was watching saw the picture change
// shape twice every time they opened the page.
//
// A bandwidth that is climbing is not a low bandwidth, it is a bandwidth not yet
// discovered.
func TestTheScaleDoesNotComeDownWhileTheEstimateClimbs(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// A typical climb on wifi: it starts low and reaches the true value. The
	// first step's threshold is in the middle, and gets crossed.
	for _, kbps := range []int{300, 600, 900, 1200, 1364, 1600, 2000, 2400} {
		if _, h, changed := withoutQP(g, kbps, now); changed {
			t.Fatalf("it came down to %d during the climb, at %d kbit/s", h, kbps)
		}
		now = now.Add(time.Second)
	}
	if g.current != 0 {
		t.Errorf("after the climb it is at step %d instead of the full size", g.current)
	}
}

// Zero means "I do not know", not "zero bandwidth", and the difference cost a
// resolution descent while the network was perfectly fine.
//
// When the estimate is not credible — because it is only echoing our own
// throughput on a still scene — the caller passes zero. If the scale read that as
// no bandwidth it would come down to the last step on every quiet night.
func TestForTheScaleZeroMeansNotKnown(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// It starts from good bandwidth.
	withoutQP(g, 2500, now)

	// Then a long sequence with no information: nothing must happen.
	for i := range 30 {
		now = now.Add(time.Second)
		if _, _, changed := withoutQP(g, 0, now); changed {
			t.Fatalf("it changed step at sample %d with no estimate at all", i)
		}
	}
	if g.current != 0 {
		t.Errorf("it ended at step %d with nobody having measured anything", g.current)
	}
}

// A small shortfall is confirmed before acting, because it is almost always
// noise. A collapse is not: there waiting means keeping the picture broken on
// purpose.
func TestTheScaleOnlyConfirmsSmallShortfalls(t *testing.T) {
	now := time.Now()

	// A small shortfall: just below the threshold, and steady. It must not move
	// at once, but it must move within a few samples.
	g := newScaleGovernor(1280, 720, 30)
	nearly := g.steps[0].MinKbps - 20
	if _, _, changed := withoutQP(g, nearly, now); changed {
		t.Error("it reacted on the first sample to a shortfall of 20 kbit/s")
	}
	var came bool
	for i := 0; i < scaleConfirmSamples+1 && !came; i++ {
		now = now.Add(time.Second)
		_, _, came = withoutQP(g, nearly, now)
	}
	if !came {
		t.Error("it never came down despite staying below the threshold")
	}

	// A collapse: at once, on the first sample where the fall is visible. Good
	// bandwidth is needed first, though, otherwise there is no fall to see.
	g2 := newScaleGovernor(1280, 720, 30)
	start := time.Now()
	withoutQP(g2, 2500, start)
	if _, _, changed := withoutQP(g2, g2.steps[0].MinKbps/4, start.Add(time.Second)); !changed {
		t.Error("it waited for a confirmation in the face of a collapse")
	}
}

// A descent is never held back by the dwell: holding it back would mean keeping
// the picture broken on purpose. It is the bitrate's asymmetry, applied to the
// pixels.
func TestTheScaleDoesNotHoldBackDescents(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// A first descent to an intermediate step, so that one is left below. The
	// shortfall is small, so it wants its confirmation samples.
	came := false
	for i := 0; i < scaleConfirmSamples+2 && !came; i++ {
		now = now.Add(time.Second)
		_, _, came = withoutQP(g, g.steps[1].MinKbps+10, now)
	}
	if !came {
		t.Fatal("first descent missed")
	}
	// A second later the bandwidth collapses: it has to come down at once,
	// waiting for neither the dwell nor the confirmation.
	if _, _, changed := withoutQP(g, 10, now.Add(time.Second)); !changed {
		t.Error("the second descent was held back")
	}
}

// The climb asks for more bandwidth than it takes to stay where it is: without
// that margin it would oscillate between two sizes at every breath of the
// estimate.
func TestTheScaleDoesNotOscillate(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// It comes down to the last step, starting from good bandwidth so the fall
	// is visible as such.
	withoutQP(g, 2500, now)
	now = now.Add(time.Second)
	if _, _, changed := withoutQP(g, 1, now); !changed {
		t.Fatal("it did not come down")
	}
	last := len(g.steps) - 1
	if g.current != last {
		t.Fatalf("want the last step, it is at %d", g.current)
	}
	// The margin is measured on the step **above**, which is where it would want
	// to go, not on the highest of the scale.
	above := g.steps[last-1].MinKbps

	// With exactly the bandwidth that step requires it does not climb yet.
	now = now.Add(scaleDwell + time.Second)
	if _, _, changed := withoutQP(g, above, now); changed {
		t.Errorf("it climbed with the exact bandwidth (%d kbit/s), with no margin", above)
	}

	// With the margin, it does. And by **one** step only, not all the way to the
	// top.
	now = now.Add(scaleDwell + time.Second)
	if _, h, changed := withoutQP(g, int(float64(above)*scaleRiseMargin)+1, now); !changed ||
		h != g.steps[last-1].Height {
		t.Errorf("wrong climb: height %d changed=%v (want %d)",
			h, changed, g.steps[last-1].Height)
	}
}

// The absence of an estimate is not an estimate of zero: without feedback nothing
// is touched, or the picture would shrink right at the opening.
func TestTheScaleTouchesNothingWithNoEstimate(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now().Add(time.Hour)
	for _, kbps := range []int{0, -1} {
		if w, h, changed := withoutQP(g, kbps, now); changed || w != 1280 || h != 720 {
			t.Errorf("with estimate %d it answered %dx%d changed=%v", kbps, w, h, changed)
		}
	}
}

// The steps go in multiples of 16, which is the size of the macroblock, and each
// one has to take something away from the one before: pixels first, then — when
// the pixels are gone — frames. The bandwidth threshold always falls, which is
// the property everything else is regulated by.
func TestTheScaleStepsAreConsistent(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	if len(g.steps) < 2 {
		t.Fatalf("a scale with %d steps", len(g.steps))
	}
	for i, s := range g.steps {
		if s.Width%16 != 0 || s.Height%16 != 0 {
			t.Errorf("step %d is not a multiple of 16: %dx%d", i, s.Width, s.Height)
		}
		if s.FPS <= 0 {
			t.Errorf("step %d with no cadence: %+v", i, s)
		}
		if i == 0 {
			continue
		}
		prev := g.steps[i-1]
		fewerPixels := s.Width < prev.Width
		lowerCadence := s.FPS < prev.FPS
		if !fewerPixels && !lowerCadence {
			t.Errorf("step %d takes nothing away: %dx%d@%d after %dx%d@%d",
				i, s.Width, s.Height, s.FPS, prev.Width, prev.Height, prev.FPS)
		}
		// Pixels and cadence never fall together: that would be a double jump,
		// and a step has to be one thing so its effect can be judged.
		if fewerPixels && lowerCadence {
			t.Errorf("step %d takes away pixels and cadence together: %dx%d@%d after %dx%d@%d",
				i, s.Width, s.Height, s.FPS, prev.Width, prev.Height, prev.FPS)
		}
		if s.MinKbps >= prev.MinKbps {
			t.Errorf("step %d does not cost less: %d kbit/s after %d", i, s.MinKbps, prev.MinKbps)
		}
	}
}

// The cadence steps sit **at the bottom** and all on the last size: the
// resolution scale is tuned and measured, and interleaving new steps into it
// would reopen that tuning.
// The whole ladder, written out, for the one preset that ships.
//
// Every other test here asks a property — the sizes fall, the cadence only falls
// at the bottom, the bottom is `scaleMinFPS` — and each was passing while two
// documents described a ladder that did not exist: *5 and then 2 a second* in
// `CLAUDE.md` and *5 and then 2 fps* in the README, against the 15, 6 and 2 the
// code then built. **A property test
// cannot be read as a list**, and the question somebody actually asks of this
// file is what the steps are.
//
// So they are here, as numbers. Changing them fails this test, which is the
// intent: the numbers are quoted in four places outside the code, and whoever
// moves a step has to go and move them too.
func TestTheLadderIsWrittenOut(t *testing.T) {
	want := []scaleStep{
		{Width: 1280, Height: 720, FPS: 30},
		{Width: 960, Height: 528, FPS: 30},
		{Width: 640, Height: 352, FPS: 30},
		{Width: 640, Height: 352, FPS: 5}, // detail over smoothness
		{Width: 640, Height: 352, FPS: 2}, // the floor, a frame every half second
	}
	g := newScaleGovernor(1280, 720, 30)
	if len(g.steps) != len(want) {
		t.Fatalf("the ladder has %d steps, want %d: %v", len(g.steps), len(want), g.steps)
	}
	for i, w := range want {
		if s := g.steps[i]; s.Width != w.Width || s.Height != w.Height || s.FPS != w.FPS {
			t.Errorf("step %d is %dx%d@%d, want %dx%d@%d",
				i, s.Width, s.Height, s.FPS, w.Width, w.Height, w.FPS)
		}
	}
}

func TestTheCadenceOnlyComesDownAtTheBottom(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)

	// While the pixels fall, the cadence stays full.
	i := 0
	for ; i < len(g.steps) && g.steps[i].FPS == 30; i++ {
		if i > 0 && g.steps[i].Width >= g.steps[i-1].Width {
			t.Fatalf("step %d at full cadence but with no fewer pixels", i)
		}
	}
	if i < 2 {
		t.Fatalf("only %d resolution steps", i)
	}
	if i == len(g.steps) {
		t.Fatal("no cadence step at the bottom of the scale")
	}

	// From there on the size no longer changes and only the cadence falls.
	last := g.steps[i-1]
	for ; i < len(g.steps); i++ {
		s := g.steps[i]
		if s.Width != last.Width || s.Height != last.Height {
			t.Errorf("cadence step %d changes the size too: %dx%d instead of %dx%d",
				i, s.Width, s.Height, last.Width, last.Height)
		}
		if s.FPS >= g.steps[i-1].FPS {
			t.Errorf("step %d does not lower the cadence: %d after %d", i, s.FPS, g.steps[i-1].FPS)
		}
	}

	// The bottom is two frames per second: below that it does not go, because
	// below that there is no longer a video but a still picture, and that is
	// another thing.
	if bottom := g.steps[len(g.steps)-1]; bottom.FPS != scaleMinFPS {
		t.Errorf("the bottom of the scale is at %d fps, want %d", bottom.FPS, scaleMinFPS)
	}
}

// The descent does not stop at the last size: when the quantiser says the picture
// is broken and the pixels are gone, it carries on by taking frames away.
//
// It is the case the cadence steps exist for — without them the scale ends there,
// and a ruined 360p is left at full cadence for the whole time the bandwidth is
// short.
func TestBelowTheLastSizeFramesAreTakenAway(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// Plenty of bandwidth, so only the quantiser decides.
	const bandwidth = 100000
	var lastW, lastFPS int
	for i := 0; i < len(g.steps)*2; i++ {
		w, _, fps, changed := g.target(bandwidth, true, 45, testLimits, true, now)
		if changed {
			lastW, lastFPS = w, fps
		}
		// Beyond the settling time, otherwise the quantiser is not read.
		now = now.Add(scaleSettle + time.Second)
	}

	if lastFPS != scaleMinFPS {
		t.Errorf("with the quantiser always past the threshold it stopped at %d fps, want %d", lastFPS, scaleMinFPS)
	}
	if bottom := g.steps[len(g.steps)-1]; lastW != bottom.Width {
		t.Errorf("final size %d, want %d", lastW, bottom.Width)
	}
}

// And it climbs back: the full cadence returns when the bandwidth and the quality
// allow it. Without this half, the first moment of bad network would leave the
// monitor at two frames per second all night.
func TestTheCadenceClimbsBack(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// It comes all the way down on the quantiser.
	for i := 0; i < len(g.steps)*2; i++ {
		g.target(100000, true, 45, testLimits, true, now)
		now = now.Add(scaleSettle + time.Second)
	}
	if g.steps[g.current].FPS != scaleMinFPS {
		t.Fatalf("it did not reach the bottom: %+v", g.steps[g.current])
	}

	// Then plenty of bandwidth and an excellent quantiser, with the dwell time.
	var fps int
	for i := 0; i < len(g.steps)*3; i++ {
		now = now.Add(scaleDwell + time.Second)
		if _, _, f, changed := g.target(100000, true, 20, testLimits, true, now); changed {
			fps = f
		}
	}
	if fps != 30 {
		t.Errorf("the cadence climbed back to %d, want 30", fps)
	}
	if g.current != 0 {
		t.Errorf("it did not return to the full size: step %d", g.current)
	}
}

// The real criterion: the quantiser, measured with somebody really moving in
// front of the camera.
//
// At 1091 kbit/s the moving room went to QP 38.5 while the same room, still, at
// the same bitrate, sat at 31. Seven points at the same bitrate: it is the case
// no constant of bits per pixel can express, because the bitrate does not know
// what is happening in the room.
func TestTheScaleComesDownOnTheQuantiserEvenWithEnoughBandwidth(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// Plenty of bandwidth: on its own it would bring nothing down.
	bandwidth := g.steps[0].MinKbps * 3
	if _, _, _, changed := g.target(bandwidth, true, 30, testLimits, true, now); changed {
		t.Fatal("it changed size with plenty of bandwidth and a clean picture")
	}

	// The movement arrives: the quantiser rises past the break threshold, while
	// the bandwidth has not changed by one kbit.
	now = now.Add(time.Second)
	_, h, _, changed := g.target(bandwidth, true, 39, testLimits, true, now)
	if !changed {
		t.Fatal("with QP 39 it did not come down: the bandwidth does not know how hard the scene is")
	}
	if h != g.steps[1].Height {
		t.Errorf("came down to %d, want one step only (%d)", h, g.steps[1].Height)
	}
}

// After a size change the encoder is rebuilt: its first frames carry a keyframe
// and a transient, not a hard scene. Judging them would make the scale fall all
// the way down over a fault that is not there.
func TestTheScaleIgnoresTheQuantiserRightAfterAChange(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()
	bandwidth := g.steps[0].MinKbps * 3

	g.target(bandwidth, true, 39, testLimits, true, now) // first descent
	start := g.current

	// Straight afterwards, the transient: a very high QP for a few frames.
	for i := 1; i <= 3; i++ {
		if _, _, _, changed := g.target(bandwidth, true, 45, testLimits, true, now.Add(time.Duration(i)*time.Second)); changed {
			t.Fatalf("it came down again after %ds, reading the transient", i)
		}
	}
	if g.current != start {
		t.Errorf("step %d instead of %d: the cascade started", g.current, start)
	}
}

// Climbing takes more than bandwidth: it takes **quality margin**. Climbing with
// the encoder already at its limit means arriving above the break threshold and
// coming straight back down, that is making the picture change shape twice for
// nothing.
func TestTheScaleDoesNotClimbWithoutQualityMargin(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()
	bandwidth := g.steps[0].MinKbps * 3

	g.target(bandwidth, true, 39, testLimits, true, now) // it comes down
	now = now.Add(scaleDwell + scaleSettle + time.Second)

	// Plenty of bandwidth, but the encoder is already working at its limit.
	if _, _, _, changed := g.target(bandwidth, true, 36, testLimits, true, now); changed {
		t.Error("it climbed with QP 36: at the step above it would have ended in the break")
	}

	// With margin, on the other hand, it climbs.
	now = now.Add(scaleDwell + time.Second)
	if _, _, _, changed := g.target(bandwidth, true, testLimits.climbAt, testLimits, true, now); !changed {
		t.Error("it did not climb despite having bandwidth and quality margin")
	}
}

// Taking pixels away is the last resort. While there is unused bandwidth, a high
// quantiser is cured by buying bits: a size change is visible, a bitrate increase
// is not.
func TestTheScaleDoesNotTakePixelsWhileThereAreBitsToBuy(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()
	bandwidth := g.steps[0].MinKbps * 3

	// A broken picture, but the bitrate is not at the cap yet: the size is not
	// touched, the bitrate is left to climb.
	for i := range 5 {
		now = now.Add(time.Second)
		if _, _, _, changed := g.target(bandwidth, true, 45, testLimits, false, now); changed {
			t.Fatalf("it shrank the picture at sample %d "+
				"while there was still bandwidth to spend", i)
		}
	}

	// When the bits are gone, then it does: it is the only resort left.
	now = now.Add(time.Second)
	if _, _, _, changed := g.target(bandwidth, true, 45, testLimits, true, now); !changed {
		t.Error("with the bitrate already at the cap and the picture broken it did nothing")
	}
}

// The real defect, observed live: the picture stayed at 640x352 while the network
// was granting 2.5 Mbit/s, and there was no way of getting it back up.
//
// Under the quality saving the throughput sits **always** well below the cap —
// measured 35 kbit/s against 2500 on a still scene — so the estimate is never
// credible. Passing it zero, the scale exited at once on every turn: it could
// come down and never climb back.
//
// An echo, though, is a **lower bound**: if the network declares 2696 while we
// produce 35, that there is room for more pixels is not in doubt.
func TestItClimbsBackEvenWithAnEstimateThatEchoesUs(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// It comes all the way down with a credible estimate, as a network that
	// really is tightening would do.
	for range scaleConfirmSamples + 2 {
		now = now.Add(time.Second)
		withoutQP(g, 10, now)
	}
	if g.current != len(g.steps)-1 {
		t.Fatalf("it did not come down to the bottom: step %d", g.current)
	}

	// Then the network opens up again, but the monitor is saving and produces
	// very little: the estimate is high and not credible. It has to climb all the
	// way back to the top.
	for turn := 0; turn < 200 && g.current > 0; turn++ {
		now = now.Add(time.Second)
		echoOfUs(g, 2696, now)
	}
	if g.current != 0 {
		t.Errorf("with 2696 kbit/s declared it stayed at step %d (%dx%d)",
			g.current, g.steps[g.current].Width, g.steps[g.current].Height)
	}
}

// The other half, which is the defect the zero was introduced for: an estimate
// echoing our own throughput must not **bring anything down**.
//
// Observed then: the scale read the 1200 kbit/s we had just imposed on ourselves,
// compared them with its threshold and came down to 960 without anybody having
// measured the network.
func TestAnEchoDoesNotBringItDown(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// A low throughput for a long time, declared not credible: it must move
	// nothing, however much it insists.
	for i := range 60 {
		now = now.Add(time.Second)
		if _, _, changed := echoOfUs(g, 35, now); changed {
			t.Fatalf("it came down at sample %d over a number that describes us", i)
		}
	}
	if g.current != 0 {
		t.Errorf("it ended at step %d with nobody having measured the network", g.current)
	}
}

// And the shortfalls accumulated while the estimate was not credible are not
// carried along: if they were counted, the return of credibility would make the
// picture fall over a decision taken on minutes in which nobody had measured.
func TestNonCredibleShortfallsDoNotAccumulate(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	for range 30 {
		now = now.Add(time.Second)
		echoOfUs(g, 35, now)
	}
	// The first credible and low sample: on its own it is not enough, the
	// confirmation is needed.
	now = now.Add(time.Second)
	if _, _, changed := withoutQP(g, g.steps[0].MinKbps-20, now); changed {
		t.Error("it came down on the first credible sample, counting the earlier shortfalls")
	}
}

// --- the governor is not the authority on what is coming out ----------------

// **A format request may not reach its destination, and the scale would never
// notice.**
//
// `target` commands only when the step **changes**, so there is no second
// attempt: if the pipeline gives up — a camera or an encoder refusing that size —
// or loses it in a **capture restart**, which starts again from the preset, from
// that moment this governor reasons about a step we are not sending. And the
// consequence is not theoretical: `atFullSize()` false switches the saving off
// and nails the bitrate to the cap while the full pixels go onto the network.
func TestTheScaleRealignsWithWhatIsBeingSent(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// It really does come down one step, so the starting point is the real one.
	under := g.steps[1].MinKbps + 10
	for range scaleConfirmSamples + 2 {
		now = now.Add(time.Second)
		withoutQP(g, under, now)
	}
	if g.current != 1 {
		t.Fatalf("the scale did not come down: step %d", g.current)
	}
	if g.atFullSize() {
		t.Fatal("down one step and it declares itself at full size")
	}

	// Inside the settling window it does not realign: there the old size is a
	// request not yet applied, not a misalignment.
	if _, moved := g.resync(1280, 720, 30, now); moved {
		t.Error("it realigned while the request was still in flight")
	}

	// Once the window has passed, the capture has restarted from the preset and
	// nobody said so: the scale aligns with what is coming out.
	now = now.Add(scaleSettle + time.Second)
	step, moved := g.resync(1280, 720, 30, now)
	if !moved || step != 0 {
		t.Fatalf("it did not realign: step %d, moved=%v", step, moved)
	}
	if !g.atFullSize() {
		t.Error("after the realignment it does not declare itself at full size, " +
			"that is the saving would stay switched off")
	}

	// And once aligned it moves no more.
	now = now.Add(scaleSettle + time.Second)
	if _, moved := g.resync(1280, 720, 30, now); moved {
		t.Error("it realigned twice to the same size")
	}
}

// **A size that is not a step is not invented.** It can arrive from an out-of-step
// reading — size and cadence live in two different atomics — and then the answer
// is to do nothing: on the next turn the reading is coherent. Moving the governor
// onto a "nearby" step would mean choosing on the pipeline's behalf, which is
// exactly the job this realignment gives back to it.
func TestTheScaleDoesNotInventAStep(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now().Add(scaleSettle + time.Second)

	for _, c := range []struct{ w, h, fps int }{
		{848, 480, 30}, // a size the scale does not have among its steps
		{0, 0, 0},      // the pipeline is sending nothing
		{1280, 720, 0}, // the cadence is not known yet
	} {
		if step, moved := g.resync(c.w, c.h, c.fps, now); moved {
			t.Errorf("%dx%d@%d moved the scale to step %d", c.w, c.h, c.fps, step)
		}
	}
}

// **The cadence compared is the delivered one, not the declared one.**
//
// The bottom steps change the cadence at a fixed size, so the comparison has to
// take all three numbers: looking only at the size, the scale would believe
// itself aligned while sending twice the frames.
func TestTheRealignmentLooksAtTheCadenceToo(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	last := len(g.steps) - 1
	if g.steps[last].Width != g.steps[last-1].Width {
		t.Skip("this preset has no cadence steps")
	}
	g.current = last
	now := time.Now().Add(scaleSettle + time.Second)

	// Same size, full cadence: it is the step with the reduced size alone.
	want := -1
	for i, s := range g.steps {
		if s.Width == g.steps[last].Width && s.FPS == 30 {
			want = i
			break
		}
	}
	if want < 0 {
		t.Skip("no step at full cadence with this size")
	}
	step, moved := g.resync(g.steps[last].Width, g.steps[last].Height, 30, now)
	if !moved || step != want {
		t.Errorf("with the cadence back to full: step %d (moved=%v), want %d",
			step, moved, want)
	}
}
