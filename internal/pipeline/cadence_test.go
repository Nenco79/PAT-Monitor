package pipeline

import (
	"testing"
	"time"
)

// passed counts how many frames the gate lets through, simulating a camera that
// delivers at `in` frames per second for `window`.
// Frames are counted instead of watching the clock: `time.Second/30` is not
// exact, and a loop stopped on time lets one extra in — which would measure the
// truncation of the division instead of the gate.
func passed(g *cadenceGate, in int, window time.Duration) int {
	t := time.Unix(0, 0)
	step := time.Second / time.Duration(in)
	count := in * int(window/time.Second)
	n := 0
	for range count {
		if g.due(t) {
			n++
		}
		t = t.Add(step)
	}
	return n
}

// TestAClosedGateLetsEverythingThrough: as long as nobody has lowered the
// cadence the gate must cost nothing and take nothing away. That is the ordinary
// case, the one the monitor sits in all night.
func TestAClosedGateLetsEverythingThrough(t *testing.T) {
	g := &cadenceGate{}
	if n := passed(g, 30, time.Second); n != 30 {
		t.Errorf("%d frames of 30 got through; the gate was meant to be transparent", n)
	}
}

// TestAskingForTheFullCadenceDropsNothing: a cadence equal to or above the
// incoming one is not a request to drop. It holds when the scale climbs back
// too: on returning to the top no gate must be left ajar.
func TestAskingForTheFullCadenceDropsNothing(t *testing.T) {
	for _, want := range []int{30, 60} {
		g := &cadenceGate{}
		g.setFPS(want, 30)
		if n := passed(g, 30, time.Second); n != 30 {
			t.Errorf("with a wanted cadence of %d, %d frames of 30 got through", want, n)
		}
	}
}

// TestTheWantedCadenceIsTheOneThatComesOut is the property that matters: the
// gate exists because the real cadence is ours to decide by counting frames,
// instead of asking the camera for it and hoping it applies it.
func TestTheWantedCadenceIsTheOneThatComesOut(t *testing.T) {
	cases := []struct{ in, want int }{
		{30, 15},
		{30, 6},
		{30, 1},
		{25, 5},
		{25, 1},
	}
	for _, c := range cases {
		g := &cadenceGate{}
		g.setFPS(c.want, c.in)
		n := passed(g, c.in, 4*time.Second)
		want := c.want * 4
		// One frame of tolerance: the window can fall across an interval, and
		// the gate is not a clock.
		if n < want-1 || n > want+1 {
			t.Errorf("from %d fps wanting %d, %d got through in 4s, want ~%d",
				c.in, c.want, n, want)
		}
	}
}

// TestAHalvedCadenceDoesNotBecomeAQuarter is the defect the margin prevents.
//
// With the camera at 30 and the gate at 15, frames arrive every 33.3 ms and the
// wanted interval is 66.6: without a margin the second usable frame arrives **a
// hair before** the deadline, is dropped, and the next one falls a whole interval
// later — that is, 10 frames per second come out of a request for 15. The defect
// is worst in the commonest case, the one where the wanted cadence divides the
// incoming one.
func TestAHalvedCadenceDoesNotBecomeAQuarter(t *testing.T) {
	g := &cadenceGate{}
	g.setFPS(15, 30)
	if n := passed(g, 30, 2*time.Second); n < 29 {
		t.Errorf("%d frames got through in 2s out of a request for 30: the gate eats more than it should", n)
	}
}

// TestIfTheCameraSlowsDownNothingIsCaughtUp: in the dark the automatic exposure
// pulls the incoming cadence down a long way. The gate must confine itself to
// letting through what there is, without keeping a debt to settle when the
// cadence comes back — otherwise the first time the room brightens two frames
// would come out stuck together.
func TestIfTheCameraSlowsDownNothingIsCaughtUp(t *testing.T) {
	g := &cadenceGate{}
	g.setFPS(15, 30)

	t0 := time.Unix(0, 0)
	// One frame, then a long darkness, then two frames close together.
	if !g.due(t0) {
		t.Fatal("the first frame was meant to get through")
	}
	if !g.due(t0.Add(2 * time.Second)) {
		t.Fatal("after two seconds of waiting the frame was meant to get through")
	}
	if g.due(t0.Add(2*time.Second + 10*time.Millisecond)) {
		t.Error("a frame 10 ms after the previous one got through: the gate is catching up on a debt")
	}
}
