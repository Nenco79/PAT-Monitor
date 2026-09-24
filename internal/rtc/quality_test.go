package rtc

import (
	"math"
	"testing"
	"time"
)

// The loop is tested here and not over a network: the fault it guards against is
// an oscillation, and an oscillation never reproduces twice alike from life.

const targetQP = 30

// ready gives a governor whose quantiser window is already full.
//
// **The window is two GOPs** (see qualityQPWindow): the quantiser reaching the
// loop is the p90 of one second and the GOP lasts two, so on its own it
// alternates with the keyframes. The tests below measure the **decision**, not
// the wait, and so start from where the decision is taken. Filling it with the
// same value leaves the average equal to the sample: no test changes meaning,
// only that it no longer has to wait four seconds to exist.
//
// Whoever needs a **non**-uniform window writes it by hand on `g.qp`, like
// TestAnAlternatingQuantiserCommandsNothing: that is where the defect this
// window exists for lives.
func ready(target, start, qp int) *qualityGovernor {
	g := newQualityGovernor(target, start)
	for i := 1; i < qualityQPWindow; i++ {
		g.qp = append(g.qp, qp)
	}
	return g
}

// after takes the clock past the wait, so the governor decides again.
func after(t time.Time) time.Time { return t.Add(qualitySettle + time.Second) }

// TestQualityTooGoodBringsItDown: it is the rule, and the whole saving is here.
// If the picture is better than it needs to be, those bits are not visible.
func TestQualityTooGoodBringsItDown(t *testing.T) {
	g := ready(targetQP, 2500, 22)
	now := after(time.Now())

	kbps, changed := g.target(22, 2500, 2500, true, false, now)
	if !changed || kbps >= 2500 {
		t.Errorf("with quality 22 against a target of 30 it did not come down: %d, changed=%v", kbps, changed)
	}
}

// And if the picture is worse than wanted it goes up, because there is a defect
// there that is visible.
//
// **The window has to be full, or the test proves nothing.** With an empty one
// the quantiser is "I do not know", the function leaves through `atCap` and
// answers the cap whatever it was given: the assertion then passes for qp 22 as
// well, which is the case that ought to bring the bitrate *down*. The negative
// control below is what says the shortfall branch was really reached.
func TestPoorQualityBringsItUp(t *testing.T) {
	g := ready(targetQP, 1000, 36)
	now := after(time.Now())

	kbps, changed := g.target(36, 1000, 2500, true, false, now)
	if !changed || kbps <= 1000 {
		t.Errorf("with quality 36 against a target of 30 it did not go up: %d, changed=%v", kbps, changed)
	}
	if kbps >= 2500 {
		t.Errorf("it asked for the cap (%d): that is the answer with no quantiser, "+
			"not a step computed from a shortfall of six points", kbps)
	}

	// The same call with the quality *better* than the target has to move the
	// other way. Without this, any answer at all satisfies the assertion above.
	down := ready(targetQP, 1000, 24)
	if kbps, _ := down.target(24, 1000, 2500, true, false, now); kbps >= 1000 {
		t.Errorf("with quality 24 it asked for %d: the branch under test is not being reached", kbps)
	}
}

// It goes up whole and comes down by half: going up repairs a defect that is
// visible, coming down only saves. It is this project's usual asymmetry.
func TestItGoesUpFasterThanItComesDown(t *testing.T) {
	up := ready(targetQP, 1000, targetQP+6)
	down := ready(targetQP, 1000, targetQP-6)
	now := after(time.Now())

	rose, _ := up.target(targetQP+6, 1000, 4000, true, false, now)
	fell, _ := down.target(targetQP-6, 1000, 4000, true, false, now)

	// Six points are a doubling: going up it reaches 2000, coming down by half a
	// step it reaches ~707 and not 500.
	if rose < 1900 || rose > 2100 {
		t.Errorf("climb with six points of shortfall = %d, want ~2000 (a doubling)", rose)
	}
	if fell < 650 || fell > 780 {
		t.Errorf("descent with six points of shortfall = %d, want ~707 (half a step)", fell)
	}
}

// **The anchor is what comes out, not what we asked for.** It is how the previous
// loop came down into the void: with an encoder producing more than it is asked
// for, cutting the request means cutting a number that has no relation to
// reality.
//
// Measured on AMD: 2500 asked, 2800 produced.
func TestItAnchorsToTheBytesOutNotToTheRequest(t *testing.T) {
	g := ready(targetQP, 2500, targetQP-6)
	now := after(time.Now())

	// The encoder produces more than it was asked for, and the quality is better
	// than the target: it has to come down **starting from 2800**.
	kbps, changed := g.target(targetQP-6, 2800, 4000, true, false, now)
	if !changed {
		t.Fatal("it moved nothing")
	}
	want := int(2800 * math.Pow(2, -0.5)) // half a step from 2800
	if kbps < want-100 || kbps > want+100 {
		t.Errorf("kbps = %d, want ~%d: it anchored to the request instead of the throughput", kbps, want)
	}
}

// A shortfall of one point is 12% of the bits: below that threshold it would be
// chasing the noise of a measurement taken over one second.
func TestASinglePointMovesNothing(t *testing.T) {
	g := ready(targetQP, 1500, targetQP)
	now := after(time.Now())

	for _, qp := range []int{targetQP - 1, targetQP, targetQP + 1} {
		if _, changed := g.target(qp, 1500, 2500, true, false, now); changed {
			t.Errorf("it moved the bitrate over a shortfall of %d points", qp-targetQP)
		}
	}
}

// The cap always commands, and at once: it can come down for congestion from one
// instant to the next, and there is nothing to think about there — not even the
// wait.
func TestTheCapCommandsAtOnce(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()

	kbps, changed := g.target(targetQP, 2500, 800, true, false, now)
	if !changed || kbps != 800 {
		t.Errorf("kbps = %d, changed = %v; the cap was 800 and has to be respected at once", kbps, changed)
	}
}

// And it never goes above the cap, however bad the picture: that is the network's
// limit, and exceeding it is paid for in lost packets.
func TestItNeverExceedsTheCap(t *testing.T) {
	g := newQualityGovernor(targetQP, 500)
	now := after(time.Now())

	kbps, _ := g.target(50, 500, 900, true, false, now)
	if kbps > 900 {
		t.Errorf("kbps = %d, above the cap of 900", kbps)
	}
}

// With no quantiser nothing is invented: it sits at the cap, which is the earlier
// behaviour. The saving is lost, not the correctness.
func TestWithNoQuantiserItSitsAtTheCap(t *testing.T) {
	g := newQualityGovernor(targetQP, 800)
	now := after(time.Now())

	kbps, changed := g.target(0, 800, 2500, true, false, now)
	if !changed || kbps != 2500 {
		t.Errorf("kbps = %d, changed = %v; with no measurement it sits at the cap", kbps, changed)
	}
}

// And with no target configured the loop does not exist at all.
func TestWithNoTargetTheLoopDoesNotExist(t *testing.T) {
	g := newQualityGovernor(0, 1000)
	now := after(time.Now())

	kbps, _ := g.target(20, 1000, 2500, true, false, now)
	if kbps != 2500 {
		t.Errorf("kbps = %d; with no target it sits at the cap", kbps)
	}
}

// An encoder is not judged while it is answering: after a command the frames
// already in flight come out with the old value.
func TestNoJudgementWhileTheEncoderAnswers(t *testing.T) {
	g := ready(targetQP, 2500, 20)
	now := after(time.Now())

	if _, changed := g.target(20, 2500, 2500, true, false, now); !changed {
		t.Fatal("the first decision was not taken")
	}
	if _, changed := g.target(20, 2500, 2500, true, false, now.Add(qualitySettle/2)); changed {
		t.Error("it decided again before the encoder had answered")
	}
}

// The proof that matters: the loop **converges** instead of oscillating.
//
// It simulates an encoder that respects the specification and nothing more: it
// produces what it is asked for and its quantiser follows the six-points-per-
// doubling rule. If the loop is unstable, that shows here as an amplitude that
// does not fall.
func TestTheLoopConverges(t *testing.T) {
	// At 2000 kbit/s this scene would sit at quantiser 24: to take it to 30, six
	// points are enough, that is half the bits.
	const bitrateAt24 = 2000
	qpAt := func(kbps int) int {
		return int(24 - qpPerDoubling*math.Log2(float64(kbps)/bitrateAt24) + 0.5)
	}

	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()
	produced := 2500

	var recent []int
	for i := range 40 {
		now = after(now)
		kbps, _ := g.target(qpAt(produced), produced, 2500, true, false, now)
		produced = kbps // the encoder obeys
		if i >= 30 {
			recent = append(recent, produced)
		}
	}

	// Over the last ten samples the quality has to sit at the target, inside the
	// dead zone, and the bitrate must no longer swing.
	lo, hi := recent[0], recent[0]
	for _, v := range recent {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
		if qp := qpAt(v); qp < targetQP-qualityDeadZone || qp > targetQP+qualityDeadZone {
			t.Errorf("in the steady state the quantiser is %d, outside the dead zone around %d", qp, targetQP)
			break
		}
	}
	if hi-lo > hi/10 {
		t.Errorf("in the steady state the bitrate swings between %d and %d: the loop did not settle", lo, hi)
	}
}

// And it converges from below too, which is the case where the picture is bad and
// whoever is watching is waiting.
func TestItConvergesFromBelowToo(t *testing.T) {
	const bitrateAt24 = 2000
	qpAt := func(kbps int) int {
		return int(24 - qpPerDoubling*math.Log2(float64(kbps)/bitrateAt24) + 0.5)
	}

	g := newQualityGovernor(targetQP, 200)
	now := time.Now()
	produced := 200

	for range 40 {
		now = after(now)
		kbps, _ := g.target(qpAt(produced), produced, 2500, true, false, now)
		produced = kbps
	}
	if qp := qpAt(produced); qp < targetQP-qualityDeadZone || qp > targetQP+qualityDeadZone {
		t.Errorf("starting from 200 kbit/s it reached quantiser %d instead of %d", qp, targetQP)
	}
}

// TestItDoesNotCutWhileTheQualityWorsens reproduces the spiral observed live on
// Quick Sync in dim light, with a target of 30:
//
//	asked 2237  qp=33  produced 1582
//	asked 1273  qp=33  produced  900
//	asked  934  qp=32  produced  741
//	asked  796  qp=33  produced  563
//	asked 2500  qp=51  produced  541
//
// The encoder was under-producing — with little light the camera drops to 9-20
// fps while it believes it is working 30 — and anchoring the step to the
// throughput alone made even "going up" by 41% land below the request in force.
// Every turn cut while the quantiser rose, up to the maximum it can be.
func TestItDoesNotCutWhileTheQualityWorsens(t *testing.T) {
	g := newQualityGovernor(targetQP, 2237)
	now := after(time.Now())

	kbps, _ := g.target(33, 900, 2500, true, false, now)
	if kbps < 2237 {
		t.Errorf("with quality 33 against a target of 30 it asked for %d, less than the 2237 in force: "+
			"it cuts while the picture worsens", kbps)
	}

	// And the spiral must not restart even after a few turns.
	for i := range 4 {
		now = after(now)
		kbps, _ = g.target(33, kbps*2/5, 2500, true, false, now)
		if kbps < 2000 {
			t.Fatalf("at turn %d it came down to %d with the quality still worse than the target", i, kbps)
		}
	}
}

// TestTheCapHoldsOnTheBytesOut is the real case: the AMD encoder produced 4156
// kbit/s with 2500 granted. Stopping the request at the cap is not enough — that
// surplus goes onto the network anyway and is paid for in lost packets — so
// little enough is asked for that the cap comes out.
func TestTheCapHoldsOnTheBytesOut(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := after(time.Now())

	// **The cap is judged over a window, so the window has to be filled.** It is
	// not a weakening of the test: that quantity is now an average over two GOPs
	// and not a sample, for the reason written next to
	// `qualityThroughputWindow`. The price is that the constraint bites after
	// four seconds instead of one, and it is the right price — on the other side
	// there was a 40% cut on every keyframe at the bottom of the scale.
	for range qualityThroughputWindow - 1 {
		now = after(now)
		g.target(36, 4156, 2500, true, false, now)
	}

	// Quality worse than the target: without the constraint it would ask for the
	// cap.
	now = after(now)
	kbps, changed := g.target(36, 4156, 2500, true, false, now)
	if !changed {
		t.Fatal("with 4156 produced against a cap of 2500 it corrected nothing")
	}
	// 2500 * 2500 / 4156 = 1503, and with that gain the cap comes out.
	if kbps > 1600 {
		t.Errorf("asked %d: with a gain of 1.66 that would emit %.0f, above the cap",
			kbps, float64(kbps)*4156/2500)
	}
}

// And the constraint loosens on its own, because it is recomputed on what comes
// out: as soon as the encoder comes back inside the cap it no longer bites. A
// one-off correction would have no way back.
func TestTheConstraintLoosensWhenTheThroughputComesBack(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := after(time.Now())
	g.target(36, 4156, 2500, true, false, now) // the constraint bites

	now = after(now)
	kbps, _ := g.target(36, 1400, 2500, true, false, now)
	if kbps <= 1600 {
		t.Errorf("with 1400 produced the constraint still bites: asked %d", kbps)
	}
}

// An encoder that stays inside the cap must not be touched: there is nothing to
// contain, and taking bandwidth away from it would worsen the picture over a
// suspicion.
func TestInsideTheCapThereIsNoConstraint(t *testing.T) {
	g := newQualityGovernor(targetQP, 2000)
	now := after(time.Now())

	kbps, _ := g.target(36, 1900, 2500, true, false, now)
	if kbps < 2000 {
		t.Errorf("asked %d with 1900 produced against a cap of 2500: there was nothing to limit", kbps)
	}
}

// TestThroughputNoiseDoesNotMoveTheCap is the real sequence read on Quick Sync
// with the encoder stuck at the cap: twenty-five samples between 2078 and 2906
// against a cap of 2500, mean 2557, that is a real overshoot of 2%.
//
// Reacting to the single sample brought the request down to ~2100 on the high
// ones and back to the cap on the low ones, every five seconds, for ever: an
// oscillation of 15% commanded by noise instead of by the encoder. And on an
// encoder that obeys, that oscillation would end up on the network.
func TestThroughputNoiseDoesNotMoveTheCap(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()

	// The quality is worse than the target, so the cap is wanted: it is the
	// condition where the oscillation showed.
	produced := []int{2345, 2620, 2435, 2672, 2364, 2701, 2402, 2672, 2188,
		2529, 2437, 2521, 2530, 2906, 2785, 2426, 2788, 2634, 2513}

	for _, p := range produced {
		now = after(now)
		kbps, _ := g.target(32, p, 2500, true, false, now)
		if kbps != 2500 {
			t.Fatalf("with %d produced against a cap of 2500 it asked for %d: "+
				"that is noise, not an overshoot", p, kbps)
		}
	}
}

// But a real overshoot has to get through anyway: the threshold sits between the
// two measured phenomena, not above both.
func TestARealOvershootPassesTheThreshold(t *testing.T) {
	now := after(time.Now())

	// 25% sits just above the peak of the noise; 66% is AMD.
	// A new governor for each case: the two have to be tested from a standstill,
	// otherwise the second inherits the first's move and it is no longer clear
	// what decided what.
	for _, produced := range []int{3125, 4156} {
		g := newQualityGovernor(targetQP, 2500)
		// Sustained, not one sample: the cap is judged over two GOPs. A real
		// overshoot lasts, a keyframe does not — and that is exactly what the
		// window serves to distinguish.
		var kbps int
		for range qualityThroughputWindow {
			now = after(now)
			kbps, _ = g.target(32, produced, 2500, true, false, now)
		}
		if kbps >= 2500 {
			t.Errorf("with %d produced against 2500 it corrected nothing (asked %d)",
				produced, kbps)
		}
	}
}

// runLoop runs the loop with a fake encoder whose throughput **responds to the
// request**, as a real one does.
//
// It has to be closed-loop and not open: feeding a fixed sequence of throughputs
// while the request comes down makes the cap on the bytes spiral into itself on
// every turn. That would be a defect of the test, not of the code, and it is
// exactly the sort of thing an open-loop test makes one believe.
//
// `qp` sits **above** the target, that is the loop wants to buy bits and pushes
// towards the cap: it is the only condition in which the cap on the bytes is
// really consulted, because with the quantiser on the target the function exits
// earlier.
func runLoop(target, qp, capKbps, turns int, throughput func(asked int, s int) int) (lowest int) {
	g := newQualityGovernor(target, capKbps)
	now := time.Now()
	asked, lowest := capKbps, capKbps
	for s := range turns {
		produced := throughput(asked, s)
		now = now.Add(time.Second)
		asked, _ = g.target(qp, produced, capKbps, true, false, now)
		if asked < lowest {
			lowest = asked
		}
	}
	return lowest
}

// TestAKeyframeIsNotAnOvershoot comes from the real sequence measured on AMD,
// asking for 600 kbit/s with a GOP of two seconds:
//
//	876  508  858  534  964  588  884  442
//
// **Every high value is a second with the keyframe**, without a single exception.
// Against the cap: 1.49x on the seconds with the key, 0.86x on the others, and
// **1.18x on average**, that is inside the dead zone. There is nothing to
// correct: the encoder is at the cap, it is the cost of the keyframe concentrated
// in one second out of two.
//
// Reacting to the sample, the cap on the bytes cut the request to 600/1.49, that
// is to two thirds, one time in two — for a frame that has to be sent anyway. And
// it happened **at the bottom of the scale**, where the monitor spends its worst
// nights: at full bitrate the same alternation is worth 5% and the dead zone
// absorbs it, which is why it never emerged.
func TestAKeyframeIsNotAnOvershoot(t *testing.T) {
	const capKbps = 600
	lowest := runLoop(30, 36, capKbps, 24, func(asked, s int) int {
		f := 0.86
		if s%2 == 0 {
			f = 1.49 // the second with the keyframe
		}
		return int(float64(asked) * f)
	})
	if lowest < capKbps {
		t.Errorf("the request came down to %d against a cap of %d: it is the keyframe, not "+
			"an overshoot — the mean throughput is 1.18x, inside the dead zone", lowest, capKbps)
	}
}

// But a **real** overshoot has to go on getting through: the window averages the
// keyframe away, it does not hide an encoder producing more than it is asked for.
// 66% is the value measured on AMD when the overshoot really was there.
func TestASustainedOvershootStillPassesWithTheWindow(t *testing.T) {
	const capKbps = 600
	lowest := runLoop(30, 36, capKbps, 24, func(asked, s int) int {
		return int(float64(asked) * 1.66)
	})
	if lowest >= capKbps {
		t.Errorf("with a throughput always at 166%% of the cap the request never came down "+
			"below %d: the window is hiding a real overshoot", capKbps)
	}
}

// --- movement gives the discount back ----------------------------------------
//
// The relaxation lives in one branch, but that branch has to do **four** things
// and not do a fifth: jump in one go, not exceed the cap, not move when there is
// no discount to give back, and not switch the saving off afterwards.

// The reason this branch exists: without it, from the bottom of the discount to
// the cap takes three or four steps of five seconds, that is a quarter of a
// minute of blocky picture in the one moment somebody is watching.
func TestMotionReleasesTheDiscountInOneStep(t *testing.T) {
	g := ready(targetQP, 2500, 20)
	now := after(time.Now())

	// The loop has taken its discount: still room, quality better than the
	// target, it comes down.
	fell, changed := g.target(20, 400, 2500, true, false, now)
	if !changed || fell >= 2500 {
		t.Fatalf("the loop did not take the discount: %d, changed=%v", fell, changed)
	}

	// **One second later**, that is with the wait still far from expiring, the
	// room moves.
	kbps, changed := g.target(20, 400, 2500, true, true, now.Add(time.Second))
	if !changed || kbps != 2500 {
		t.Errorf("the movement did not give the discount back in one go: %d, changed=%v "+
			"(want 2500 at once, not one step at a time)", kbps, changed)
	}
}

// The movement does not buy bandwidth that is not there: the cap stays the
// network's.
func TestMotionNeverGoesAboveTheCeiling(t *testing.T) {
	g := ready(targetQP, 2500, 20)
	now := after(time.Now())

	if _, changed := g.target(20, 400, 700, true, false, now); !changed {
		t.Fatal("the loop did not take the network's cap")
	}
	kbps, _ := g.target(20, 400, 700, true, true, after(now))
	if kbps > 700 {
		t.Errorf("with the network at 700 the movement asked for %d: the cap is the network's, "+
			"and no sensor of ours can step over it", kbps)
	}
}

// With no discount there is nothing to give back, and the wait goes on holding
// for everything else: the branch is not a way of commanding the encoder more
// often.
func TestMotionDoesNothingWithoutADiscount(t *testing.T) {
	g := ready(targetQP, 2500, targetQP)
	now := after(time.Now())

	if _, changed := g.target(targetQP, 2500, 2500, true, true, now); changed {
		t.Error("it commanded the encoder while already at the cap")
	}

	// And if a movement arrives while the loop is waiting, the rest of the
	// function stays out of reach: it goes back to the cap and no more, it
	// decides nothing about the quality.
	if _, changed := g.target(20, 2500, 2500, true, false, now); !changed {
		t.Fatal("the first decision was not taken")
	}
	before, _ := g.target(20, 2500, 2500, true, true, now.Add(time.Second))
	afterwards, changed := g.target(20, 2500, 2500, true, true, now.Add(2*time.Second))
	if changed || afterwards != before {
		t.Errorf("with the wait in progress and no discount the movement moved the bitrate: "+
			"%d -> %d", before, afterwards)
	}
}

// The relaxation does not switch the saving off: after the jump the loop comes
// back down as always. If it did not, a movement at midnight would keep the
// monitor at the cap until morning.
func TestTheDiscountIsEarnedAgainAfterTheJump(t *testing.T) {
	g := ready(targetQP, 2500, 20)
	now := after(time.Now())

	if _, changed := g.target(20, 400, 2500, true, false, now); !changed {
		t.Fatal("the loop did not take the discount")
	}
	if kbps, _ := g.target(20, 400, 2500, true, true, now.Add(time.Second)); kbps != 2500 {
		t.Fatalf("the jump did not reach the cap: %d", kbps)
	}
	now = after(now)
	kbps, changed := g.target(20, 2500, 2500, true, false, now)
	if !changed || kbps >= 2500 {
		t.Errorf("after the jump the loop did not go back to saving: %d, changed=%v",
			kbps, changed)
	}
}

// With the saving switched off there is no discount, so the branch is not even
// reachable: it sits at the cap, as it always has.
func TestAStirWithoutATargetChangesNothing(t *testing.T) {
	g := newQualityGovernor(0, 2500)
	now := after(time.Now())

	if _, changed := g.target(45, 400, 2500, true, true, now); changed {
		t.Error("with target_qp at zero the movement moved something")
	}
}

// climb counts the commands and the seconds it takes to get from the bottom of
// the discount back to the cap, with an encoder whose quantiser reacts to the
// bitrate by `pointsPerDoubling` points.
//
// Six points is the textbook encoder. **One is the measured one**: in dim light
// on Quick Sync the quantiser stayed at 32-33 while the bitrate changed by a
// factor of three (the sequence is in the comment in `quality.go`, "asked 2237
// qp=33 produced 1582 … asked 796 qp=33 produced 563"), because the camera drops
// to 9-20 fps and the encoder allocates the per-frame budget accordingly.
func climb(qpAtFloor, pointsPerDoubling float64, stirred bool) (commands, seconds int) {
	g := ready(targetQP, 2500, int(qpAtFloor))
	now := time.Now()
	// The loop has already taken its discount and has just moved: it is the worst
	// case, that is the whole wait still to serve.
	g.current, g.moved = bitrateFloorKbps, now
	produced := bitrateFloorKbps

	for seconds = 1; seconds <= 120; seconds++ {
		now = now.Add(time.Second)
		qp := min(int(qpAtFloor-pointsPerDoubling*math.Log2(float64(produced)/float64(bitrateFloorKbps))+0.5), 51)
		kbps, moved := g.target(qp, produced, 2500, true, stirred, now)
		stirred = false // the episode begins once only
		if moved {
			commands++
			produced = kbps
			if kbps >= 2500 {
				return commands, seconds
			}
		}
	}
	return commands, -1
}

// TestWithAStuckQuantiserTheClimbTakesSeveralCommands is the reason the motion
// branch exists, and the number that says what it is worth.
//
// **With a quantiser that follows the bits there would be nothing to do**: the
// loop computes the exact step and arrives where it needs to in one command. But
// that calculation only holds if the quantiser reacts, and **at night it does
// not** — it is the condition measured in dim light, where it stays nailed down
// while the bitrate changes by a factor of three. There every step buys little,
// and the climb becomes a ramp.
//
// That is, the worst case is not a rare case: it is the night, which is when
// this program works.
func TestWithAStuckQuantiserTheClimbTakesSeveralCommands(t *testing.T) {
	commands, seconds := climb(36, 1, false)
	if commands < 3 {
		t.Fatalf("with the quantiser nearly stuck the climb took %d commands: "+
			"the test is no longer measuring the ramp", commands)
	}
	t.Logf("quantiser nearly stuck: %d commands, cap reached at %d s", commands, seconds)

	withMotion, motionSeconds := climb(36, 1, true)
	if withMotion != 1 {
		t.Errorf("the movement took %d commands instead of one", withMotion)
	}
	if motionSeconds >= seconds {
		t.Errorf("the movement gained nothing: %d s against %d", motionSeconds, seconds)
	}
	t.Logf("with the movement: %d command, cap reached at %d s", withMotion, motionSeconds)
}

// The motion jump respects the cap on the **bytes**, not the one on the number
// asked for.
//
// On an encoder with a wide calibration — measured on AMD, 4156 kbit/s with 2500
// granted — going straight to the requested cap would put two thirds too much
// onto the network, and the wait of five seconds would keep it there. The good
// picture would be bought at the price of the stuttering picture, which is the
// worse of the two faults. TestMotionNeverCutsTheBitrate nails down this
// branch's direction.
//
// The cap on the bytes can **never** produce a jump: it fires only when the
// throughput exceeds the cap by more than the noise, and in that case the value
// it proposes sits below what is already being asked for. Executing it here
// would make a movement in the room a cut in the bitrate — and `moved` would
// keep it cut for five seconds. The cut is made by the ordinary branch, one tick
// later.
//
// The numbers are the ones measured on AMD: 4156 kbit/s produced with 2500
// granted.
func TestMotionNeverCutsTheBitrate(t *testing.T) {
	g := ready(targetQP, 2500, 20)
	now := after(time.Now())
	g.current = 1000
	g.throughput = []int{4156, 4156, 4156, 4156}

	kbps, changed := g.target(20, 4156, 2500, true, true, now)
	if changed {
		t.Errorf("the movement commanded %d starting from 1000: this branch raises, it does not cut", kbps)
	}
	if kbps != 1000 {
		t.Errorf("the bitrate became %d instead of staying 1000", kbps)
	}
}

// --- the keyframe alternation ------------------------------------------------

// **The quantiser arriving here alternates for a structural reason, and chasing
// it commanded the encoder for ever.**
//
// It is the p90 of **one second** and the GOP lasts **two**: one second in two
// contains a keyframe, and the frames around it are encoded differently by
// construction. The sequence below is the real one, read from the log on Quick
// Sync with a target of 30 — and its mean is 29.5, that is, the target falls **in
// the middle** of the alternation.
//
// None of those sixteen values sits in the dead zone, which is one point:
// reading the sample, the loop commanded on every turn the wait allowed it.
// Measured, **74 commands in 9 minutes**, and on a machine where the bitrate is
// changed by rebuilding the encoder every command drains the transform — that is,
// throws away the frames in flight. Those are the micro-stutters visible while
// watching.
func TestAnAlternatingQuantiserCommandsNothing(t *testing.T) {
	alternating := []int{28, 28, 32, 28, 32, 27, 28, 28, 33, 27, 28, 28, 32, 28, 28, 33}

	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()
	commands := 0
	for _, qp := range alternating {
		now = now.Add(time.Second)
		if _, moved := g.target(qp, 2400, 2500, true, false, now); moved {
			commands++
		}
	}
	if commands != 0 {
		t.Errorf("the loop commanded %d times over %d turns for an alternation whose "+
			"mean is the target: it is chasing the keyframes", commands, len(alternating))
	}
}

// **But the window must not make the loop deaf.** If the scene really does get
// worse, the quantiser stays high turn after turn and there bandwidth is bought:
// it is the half of the job the remedy could not take away.
func TestASustainedRiseStillCommands(t *testing.T) {
	g := newQualityGovernor(targetQP, 1000)
	now := time.Now()
	var last int
	commands := 0
	for range 12 {
		now = now.Add(time.Second)
		kbps, moved := g.target(targetQP+6, 900, 2500, true, false, now)
		if moved {
			commands++
			last = kbps
		}
	}
	if commands == 0 {
		t.Fatal("with the quality steadily worse than the target it bought nothing")
	}
	if last <= 1000 {
		t.Errorf("it commanded %d, which is not a climb: %d commands", last, commands)
	}
}

// --- the saving only exists at full size -------------------------------------

// **Below the full size the extra bits buy pixels, so nothing is saved.**
//
// It is the fault where the scale comes down, the scene becomes easy, the
// quantiser falls below the target, the loop concludes "these bits buy nothing"
// and cuts — and gcc, which measures only the traffic passing in front of it, can
// no longer estimate enough for the scale to climb. The saving starved the
// measurement that was meant to end it.
func TestTheDiscountOnlyExistsAtFullSize(t *testing.T) {
	// The same excellent quality, the same cap, the same everything: only the
	// step the scale sits on changes.
	full := ready(targetQP, 2500, 22)
	reduced := ready(targetQP, 2500, 22)
	now := after(time.Now())

	if kbps, changed := full.target(22, 2400, 2500, true, false, now); !changed || kbps >= 2500 {
		t.Errorf("at full size the saving has to hold: %d, changed=%v", kbps, changed)
	}
	if kbps, changed := reduced.target(22, 2400, 2500, false, false, now); changed || kbps != 2500 {
		t.Errorf("with the picture shrunk it saved anyway: %d, changed=%v — "+
			"those bits buy pixels, and it is the only way to make the estimate climb", kbps, changed)
	}
}

// **But the cap on the bytes stays**, and below the full size it counts double:
// that is where the network is suffering, and an encoder with a wide calibration
// sends the surplus onto the network exactly while congestion is being answered.
//
// The numbers are the ones measured on AMD: 4156 kbit/s produced with 2500
// granted.
func TestTheByteCeilingStillHoldsBelowFullSize(t *testing.T) {
	g := ready(targetQP, 2500, 22)
	g.throughput = []int{4156, 4156, 4156, 4156}
	now := after(time.Now())

	kbps, changed := g.target(22, 4156, 2500, false, false, now)
	if !changed || kbps >= 2500 {
		t.Errorf("it asked for %d with the encoder producing 4156 against 2500 granted: "+
			"the cap holds on the bytes out, not on the number asked for", kbps)
	}
}

// --- the fallback to the cap is a loop too -----------------------------------

// **The road that "sits at the cap" commanded on every turn, and oscillated.**
//
// Correcting the overshoot with `byteCeiling` reuses a guard asking "does what
// comes out exceed the cap?" — and there that answer is the **consequence** of
// the correction: as soon as it works, the guard judges it useless and takes the
// request back to the cap, the encoder overshoots again, and it starts over.
// Measured with an encoder producing 1.66 times what it is asked for, like the
// real AMD: 19 commands over 30 turns, cycle 2500 → 1672 → 1098 → 851 → 2500 for
// ever.
//
// It would be the same defect this round of changes removed from the quality
// loop, put back in through the side door — and it holds with `target_qp` at zero
// too, that is with the saving switched off, which is a supported configuration.
func TestSittingAtTheCapDoesNotOscillate(t *testing.T) {
	const excess = 1.66 // the wide calibration measured on AMD

	for name, c := range map[string]struct {
		fullSize bool
		target   int
	}{
		"reduced size":      {false, targetQP},
		"target_qp at zero": {true, 0},
	} {
		g := newQualityGovernor(c.target, 2500)
		now := time.Now()
		produced, commands := 2500, 0
		for range 30 {
			now = now.Add(time.Second)
			kbps, moved := g.target(22, produced, 2500, c.fullSize, false, now)
			if moved {
				commands++
			}
			produced = int(float64(kbps) * excess)
		}
		// The fixed point exists: asking for cap/1.66 makes the cap come out.
		if commands > 4 {
			t.Errorf("%s: %d commands over 30 turns — it is oscillating, and every command "+
				"on this machine drains the transform", name, commands)
		}
		if want := 2500 * 100 / 166; g.current < want-100 || g.current > want+100 {
			t.Errorf("%s: it stopped at %d instead of ~%d, that is it did not find "+
				"the request that makes the cap come out", name, g.current, want)
		}
	}
}

// And an honest encoder must make it command nothing: it sits at the cap and
// keeps quiet.
func TestSittingAtTheCapIsSilentWithAnHonestEncoder(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()
	commands := 0
	for range 30 {
		now = now.Add(time.Second)
		if _, moved := g.target(22, 2450, 2500, false, false, now); moved {
			commands++
		}
	}
	if commands != 0 || g.current != 2500 {
		t.Errorf("with the encoder respecting the cap it commanded %d times and stopped "+
			"at %d: there was nothing to correct", commands, g.current)
	}
}

// **The floor holds on every road.** Below `bitrateFloorKbps` the right answer is
// not to take more bits away but to send fewer pixels, and there the quantiser
// read is no longer a measurement. Without the line that holds it, the overshoot
// correction came down to twenty-five kbit/s.
func TestSittingAtTheCapRespectsTheFloor(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	// A throughput that does not follow the request is the worst case: the
	// measured ratio worsens on every turn and the correction finds no fixed
	// point.
	g.throughput = []int{4156, 4156, 4156, 4156}
	now := time.Now()
	for range 12 {
		now = now.Add(qualitySettle + time.Second)
		g.target(22, 4156, 2500, false, false, now)
	}
	if g.current < bitrateFloorKbps {
		t.Errorf("the correction came down to %d, below the floor of %d",
			g.current, bitrateFloorKbps)
	}
}

// **The small picture's quantisers do not decide for the whole one.**
//
// The window is fed on every turn, and crossing a size change it carried four
// samples of an easier scene with it: the first turn at full size cut **2500 →
// 1512**, that is it took bits away exactly in the instant the scale had just
// asked for them. It is the same reason as `scaleSettle` on the other side — the
// encoder has just been rebuilt and its first frames say nothing about the scene.
func TestTheQuantiserWindowDoesNotSurviveASizeChange(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()

	// At the reduced size the scene is easy: a low quantiser, turn after turn.
	for range 5 {
		now = now.Add(time.Second)
		g.target(22, 700, 2500, false, false, now)
	}
	if len(g.qp) != 0 {
		t.Errorf("the window collected %v below the full size: those values "+
			"describe a picture that is about to be gone", g.qp)
	}

	// The scale climbs back. This turn's quantiser is not known yet — the encoder
	// has just been rebuilt — so there is nothing to decide on and it sits at the
	// cap.
	now = now.Add(qualitySettle + time.Second)
	kbps, moved := g.target(0, 2400, 2500, true, false, now)
	if moved || kbps != 2500 {
		t.Errorf("first turn at full size: it asked for %d (moved=%v) on a "+
			"quantiser that is not known yet", kbps, moved)
	}
}

// --- state that outlives the conditions it described --------------------------
//
// The four tests below have one root: a number kept in memory went on answering
// after what it described had ended. It is the family already paid for twice
// elsewhere — "a loop closes when the measurement describes one's own previous
// decision" — seen from inside a single governor.

// **A missing reading does not let the previous one go on answering.**
//
// `TakeRecentQP` says `!ok` more often than it seems — a capture restart, an
// encoder emitting nothing for a second — and the loop passes `-1`. With the
// window already full, `noteQP` went on returning the previous average **for
// ever**: the loop decided on a scene that was no longer there, and with an
// encoder that obeys it came down 1512 → 1200 → 952 → 756 → 600 to the floor,
// without a single reading.
func TestAMissingQuantiserAgesTheWindow(t *testing.T) {
	g := ready(targetQP, 2500, 26)
	now := after(time.Now())

	// A window of 26 against a target of 30: the loop takes its discount.
	discount, moved := g.target(26, 2400, 2500, true, false, now)
	if !moved || discount >= 2500 {
		t.Fatalf("there is no discount: %d (moved=%v)", discount, moved)
	}

	// Then the darkness. The encoder obeys, that is it produces what it is asked
	// for: it is the condition in which the defect is a spiral instead of a fixed
	// wrong value.
	produced := discount
	lowest := discount
	kbps := discount
	for range 25 {
		now = now.Add(time.Second)
		kbps, _ = g.target(-1, produced, 2500, true, false, now)
		if kbps < lowest {
			lowest = kbps
		}
		produced = kbps
	}

	if lowest < discount {
		t.Errorf("twenty-five turns without a reading took the request to %d, "+
			"starting from %d: it is deciding on a window that no longer describes anything",
			lowest, discount)
	}
	if kbps != 2500 {
		t.Errorf("with no quantiser it sits at the cap, and instead it asks for %d", kbps)
	}
	if len(g.qp) != 0 {
		t.Errorf("after twenty-five missed readings the window still holds %v", g.qp)
	}
}

// **The motion jump goes to the cap on the bytes, not to the one asked for.**
//
// One only arrives here **with a discount in force**, so the throughput now sits
// well below the cap: a guard asking "does what comes out exceed the cap?" never
// fires, and with an encoder producing 1.66 times what it is asked for the jump
// put 4150 kbit/s on a cap of 2500 — that is, it bought the good picture at the
// price of lost packets, which is the worse of the two faults.
func TestMotionJumpsToWhatTheCapCanHold(t *testing.T) {
	g := ready(targetQP, 2500, 28)
	now := after(time.Now())
	// The loop has come down to 576 and the encoder produces 956: 1.66 times.
	g.current = 576
	g.throughput = []int{956, 956, 956, 956}

	kbps, moved := g.target(28, 956, 2500, true, true, now)
	if !moved || kbps <= 576 {
		t.Fatalf("the movement did not give the discount back: %d (moved=%v)", kbps, moved)
	}
	wouldEmit := kbps * 166 / 100
	if wouldEmit > 2500*(100+qualityOvershootNoise)/100 {
		t.Errorf("asking for %d emits %d against a cap of 2500: the jump went "+
			"to the requested cap instead of the produced one", kbps, wouldEmit)
	}
}

// **The same correction holds for any climb**, and it is easy for it not to: a
// guard looking at what comes out **now** sees, under a discount, something that
// sits below the cap by definition. With a generous encoder, every climb towards
// the cap then passes through uncorrected.
func TestARiseTowardsTheCapIsByteCorrected(t *testing.T) {
	g := ready(targetQP, 1000, 36)
	now := after(time.Now())
	g.current = 1000
	g.throughput = []int{1660, 1660, 1660, 1660}

	kbps, moved := g.target(36, 1660, 2500, true, false, now)
	if !moved || kbps <= 1000 {
		t.Fatalf("with the quality worse than the target it did not climb: %d (moved=%v)", kbps, moved)
	}
	wouldEmit := kbps * 166 / 100
	if wouldEmit > 2500*(100+qualityOvershootNoise)/100 {
		t.Errorf("asking for %d emits %d against a cap of 2500", kbps, wouldEmit)
	}
}

// **The tick that enlarges still carries the small picture's quantiser.**
//
// The scale writes the new size inside `scale.target()`, which the loop calls
// **before** the quality governor: `fullSize` is already true while this turn's
// number was produced by the previous picture. Admitting it, the just-emptied
// window restarted with a sample of an easier scene — `[20 30 30 30]`, mean 28, a
// cut of 2500 → 2138 four seconds after the climb. It is the fault
// `TestTheQuantiserWindowDoesNotSurviveASizeChange` exists to remove, diluted to
// a quarter.
func TestTheEnlargingTickCarriesTheOldQuantiser(t *testing.T) {
	g := newQualityGovernor(targetQP, 2500)
	now := time.Now()

	// Reduced size, easy scene.
	for range 3 {
		now = now.Add(time.Second)
		g.target(20, 1200, 2500, false, false, now)
	}

	// The enlarging turn: the size is already the full one, the quantiser is not.
	now = now.Add(time.Second)
	g.target(20, 1200, 2500, true, false, now)
	if len(g.qp) != 0 {
		t.Errorf("the window took %v on the turn that enlarges: that number "+
			"describes the previous picture", g.qp)
	}

	// Four good turns, and the window is all the new size's.
	for range 4 {
		now = now.Add(time.Second)
		g.target(30, 2400, 2500, true, false, now)
	}
	for _, v := range g.qp {
		if v != 30 {
			t.Fatalf("window %v: there is a sample of the small picture in it", g.qp)
		}
	}
	if g.current != 2500 {
		t.Errorf("with the quantiser at the target the request came down to %d: "+
			"what cut was the sample from the previous size", g.current)
	}
}

// **The discount does not outlive whoever had it.**
//
// With no viewers `bitrateGovernor.release` puts the encoder back at the cap, and
// this governor is not called at all: `current` and the two windows stayed those
// of the last session. Whoever arrived afterwards got somebody else's discount,
// decided on a scene from ten minutes earlier — and in the meantime `current`
// said 1191 while the encoder sat at the cap, that is the correction on the bytes
// read that gap as an encoder producing 65% more.
func TestTheDiscountDoesNotOutliveTheViewer(t *testing.T) {
	g := ready(targetQP, 2500, 26)
	now := after(time.Now())

	discount, moved := g.target(26, 1500, 2500, true, false, now)
	if !moved || discount >= 2500 {
		t.Fatalf("there is no discount: %d (moved=%v)", discount, moved)
	}

	// The last viewer leaves: the loop releases the cap, and the quality
	// realigns with it.
	g.release(2500)

	// Ten minutes later another one arrives, on a scene exactly at the target:
	// the right answer is **no command**.
	now = now.Add(10 * time.Minute)
	kbps, moved := g.target(targetQP, 2500, 2500, true, false, now)
	if moved {
		t.Errorf("the new viewer's first turn commanded %d on a scene "+
			"at the target: it is using the quantisers of whoever left", kbps)
	}
	if kbps != 2500 {
		t.Errorf("the new viewer starts from %d instead of from the cap", kbps)
	}
}
