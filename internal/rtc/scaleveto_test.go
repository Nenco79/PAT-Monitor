package rtc

import (
	"testing"
	"time"
)

// The real sequence, on AMD with two viewers on the home network and a still
// room. The estimate is gcc's, already reduced to video bandwidth; the quantiser
// is the one read from the stream at the same instant.
//
//	06:41:51  video format changed  640x352  qp=27  break_threshold=38  estimate 382
//	          bitrate matched to quality  kbps=300  cap_kbps=2500  produced=654
//
// Eleven points below the break threshold, losses at 0.0%, and twenty seconds
// later the bandwidth re-measured at 2696 kbit/s. Four descents like that in
// twenty minutes, and each one costs a minute of small picture because it climbs
// back one step at a time with twenty seconds of dwell.
func TestAHealthyQuantiserVetoesTheBandwidthDescent(t *testing.T) {
	lim := qpThresholds()
	now := time.Now()

	// **First it is shown that the defect was there.** With the veto removed —
	// that is, with the quantiser declared unknown, which is how the bandwidth
	// decided before — the same identical sequence brings the scale down.
	// The estimate had been above a thousand for a minute: a collapse is a fall,
	// and it does not exist while the estimate is climbing.
	deaf := newScaleGovernor(1280, 720, 30)
	deaf.target(1063, true, 0, qpLimits{}, true, now)
	if _, _, _, came := deaf.target(302, true, 0, qpLimits{}, true, now.Add(time.Second)); !came {
		t.Fatal("without the veto the real sequence no longer comes down: " +
			"the test is no longer looking at the defect it exists to catch")
	}

	// With the quantiser in hand, the same bandwidth touches nothing.
	g := newScaleGovernor(1280, 720, 30)
	g.target(1063, true, 25, lim, true, now)
	for i, qp := range []int{25, 27, 24, 25, 29, 27} {
		now = now.Add(time.Second)
		w, h, _, came := g.target(302, true, qp, lim, true, now)
		if came {
			t.Fatalf("sample %d (qp %d): the bandwidth shrank to %dx%d "+
				"a picture the quantiser declares healthy", i, qp, w, h)
		}
	}
	if !g.atFullSize() {
		t.Error("the scale did not stay at the full size")
	}
}

// The half not to lose, and the reason the threshold is the climb one and not
// the break one: between 33 and 38 the picture really does start to suffer, and
// there the fast road of the collapse — which skips steps in one go — has to go
// on working.
func TestTheBandStillCommandsWhenTheImageIsSuffering(t *testing.T) {
	lim := qpThresholds()
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// A quantiser just above the climb threshold is not yet a break — the QP
	// alone would do nothing — but it removes the veto.
	g.target(2500, true, lim.climbAt+1, lim, true, now)
	w, h, _, came := g.target(10, true, lim.climbAt+1, lim, true, now.Add(time.Second))
	if !came {
		t.Fatal("a bandwidth collapse brought nothing down")
	}
	if w == 1280 && h == 720 {
		t.Error("the scale did not move")
	}
}

// The veto holds on the boundary, and the equals sign is on the veto's side: if
// the picture is good enough to authorise one more step, it is too good to lose
// one.
func TestTheVetoHoldsExactlyAtTheRiseThreshold(t *testing.T) {
	lim := qpThresholds()
	now := time.Now()

	g := newScaleGovernor(1280, 720, 30)
	g.target(2500, true, lim.climbAt, lim, true, now)
	if _, _, _, came := g.target(10, true, lim.climbAt, lim, true, now.Add(time.Second)); came {
		t.Error("on the exact climb threshold the bandwidth brought it down")
	}

	g2 := newScaleGovernor(1280, 720, 30)
	g2.target(2500, true, lim.climbAt+1, lim, true, now)
	if _, _, _, came := g2.target(10, true, lim.climbAt+1, lim, true, now.Add(time.Second)); !came {
		t.Error("one point above the threshold the veto did not lift")
	}
}

// The shortfalls do not accumulate while the veto is in force. Counting them
// anyway would mean keeping the counter full for a whole still room, and making
// the scale fall in the instant the quantiser grazes the threshold — a descent
// decided by minutes in which nobody had measured the network.
func TestVetoedSamplesDoNotAccumulate(t *testing.T) {
	lim := qpThresholds()
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// Bandwidth below the step's threshold but not a collapse, for many more
	// samples than confirmation needs, with the picture healthy.
	under := g.steps[0].MinKbps - 10
	for i := 0; i < scaleConfirmSamples*3; i++ {
		now = now.Add(time.Second)
		if _, _, _, came := g.target(under, true, 25, lim, true, now); came {
			t.Fatalf("sample %d: came down with the veto in force", i)
		}
	}

	// Now the picture worsens by one point past the threshold: the counter has to
	// restart from zero, not bring it down on the first sample.
	now = now.Add(time.Second)
	if _, _, _, came := g.target(under, true, lim.climbAt+1, lim, true, now); came {
		t.Error("the shortfalls had accumulated under the veto")
	}
}

// The climb does not go through the veto, and must not: an echo is a **lower**
// bound, and this is the branch that brings the picture back to the full size.
func TestTheVetoDoesNotBlockTheRise(t *testing.T) {
	lim := qpThresholds()
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// It comes down for the quantiser, which is the road that remains.
	now = now.Add(time.Second)
	if _, _, _, came := g.target(2500, true, lim.breakAt, lim, true, now); !came {
		t.Fatal("the quantiser did not bring it down")
	}
	if g.atFullSize() {
		t.Fatal("the scale did not move")
	}

	// Then the bandwidth comes back, the picture is healthy — that is, the veto
	// is in force — and it has to climb anyway, once settling and dwell are past.
	now = now.Add(scaleDwell + time.Second)
	if _, _, _, moved := g.target(2500, true, 20, lim, true, now); !moved {
		t.Error("the veto blocked the climb too")
	}
}

// **The veto must not make the confirmation unreachable.** The quantiser
// arriving here is the p90 of one second and the GOP lasts two: one sample in two
// contains the keyframe and reads high. Reading the instant, the veto switched on
// and off on alternate seconds, and since it also clears the shortfall counter
// that counter never reached three — the confirmed descent became impossible and
// only the collapse got through, that is, the veto broke the half it claimed to
// leave intact.
func TestTheVetoDoesNotStarveTheConfirmation(t *testing.T) {
	lim := qpThresholds()
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()

	// A picture that really is suffering: an average of 34, that is above the
	// climb threshold, but the instant crosses that threshold at every keyframe.
	alternating := []int{31, 37, 31, 37, 31, 37, 31, 37, 31, 37}
	under := g.steps[0].MinKbps - 10
	g.target(2500, true, alternating[0], lim, true, now)

	came := false
	for i, qp := range alternating {
		now = now.Add(time.Second)
		if _, _, _, ok := g.target(under, true, qp, lim, true, now); ok {
			came = true
			if i+1 < scaleConfirmSamples {
				t.Errorf("came down at sample %d, before the %d confirmations", i, scaleConfirmSamples)
			}
			break
		}
	}
	if !came {
		t.Error("with the quantiser alternating around the threshold the confirmation " +
			"never arrived: the veto cleared the counter on alternate seconds")
	}
}

// And the opposite half: an alternation whose **average** stays healthy brings
// nothing down. 29 is the scene, 35 its second with the keyframe.
func TestAnAlternatingQuantiserThatAveragesHealthyStillVetoes(t *testing.T) {
	lim := qpThresholds()
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()
	under := g.steps[0].MinKbps - 10
	g.target(2500, true, 29, lim, true, now)

	for i, qp := range []int{29, 35, 29, 35, 29, 35, 29, 35} {
		now = now.Add(time.Second)
		if _, _, _, came := g.target(under, true, qp, lim, true, now); came {
			t.Fatalf("sample %d (qp %d): came down on a picture averaging 32", i, qp)
		}
	}
}

// **The scale does not inherit the judgement of whoever has left.** `target` is
// called only with somebody watching, so without a release the quantiser window
// stays that of the last session: whoever arrives ten minutes later gets a veto
// decided on a scene that no longer exists. It is the same family as
// `qualityGovernor.release` and the window over the throughput.
func TestTheScaleDoesNotInheritTheLastSession(t *testing.T) {
	lim := qpThresholds()
	under := 0

	// How many ticks it takes to come down, starting from a window full of
	// healthy readings and then with the picture breaking.
	ticks := func(release bool) int {
		g := newScaleGovernor(1280, 720, 30)
		under = g.steps[0].MinKbps - 10
		now := time.Now()

		// The previous session: healthy picture, bandwidth below the threshold.
		for i := 0; i < scaleQPWindow; i++ {
			now = now.Add(time.Second)
			g.target(under, true, 25, lim, true, now)
		}
		if !g.atFullSize() {
			t.Fatal("it came down with a healthy picture")
		}

		if release {
			g.release()
		}

		// The new session, ten minutes later, on a scene that is suffering.
		now = now.Add(10 * time.Minute)
		for i := 1; i <= 12; i++ {
			now = now.Add(time.Second)
			// Above the climb threshold but **below the break one**: that way
			// what decides is the bandwidth branch, not the descent for
			// quantiser, which here would fire on the first tick and measure
			// nothing.
			if _, _, _, came := g.target(under, true, lim.climbAt+2, lim, true, now); came {
				return i
			}
		}
		return 0
	}

	with, without := ticks(true), ticks(false)
	if with == 0 {
		t.Fatal("with the release it did not come down at all")
	}
	if with > scaleConfirmSamples+1 {
		t.Errorf("with the release it took %d ticks: the old window still weighs", with)
	}
	if without <= with {
		t.Errorf("the release changes nothing (%d ticks against %d): "+
			"the test is not looking at the defect it exists to catch", without, with)
	}

	// And the **step** is not touched: that is not a judgement, it is what the
	// pipeline is sending now.
	g := newScaleGovernor(1280, 720, 30)
	now := time.Now()
	g.target(2500, true, lim.breakAt, lim, true, now.Add(time.Second))
	if g.atFullSize() {
		t.Fatal("it did not come down")
	}
	before := g.current
	g.release()
	if g.current != before {
		t.Errorf("the release moved the step from %d to %d", before, g.current)
	}
}
