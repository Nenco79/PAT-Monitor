package rtc

import (
	"math"
	"time"
)

// The bitrate chases a quality, it does not just fill up to one.
//
// In CBR the encoder spends everything it is given whatever there is to film,
// and a still room does not need it. The rule fits on one line: **the bitrate
// comes down only if the quality exceeds**, that is only when the picture comes
// out better than necessary. On a hard scene the condition never holds, so it
// does not come down: the protection is in the rule, not in a tuning.
//
// Three constraints, and they are the ones that killed the previous loop
// (CLAUDE.md, "constant quality is done inside the encoder, not around it"):
//
//   - **it closes on the bytes that come out**, never on the request. How much
//     the encoder lies then stops mattering;
//   - **the step is computed**, not chosen: six points of quantiser are a
//     doubling of the bits, by the specification;
//   - **the target is not learnt**, we tell it. Learning it requires sitting at
//     the cap, and a loop that comes down prevents ever getting back there.

const (
	// qpPerDoubling: how many points of quantiser are worth a doubling of the
	// bits. Six, by the H.264 specification, on any encoder and any decoder. It
	// is the only constant in here that is not a choice.
	qpPerDoubling = 6.0

	// qualityDeadZone: how far the quantiser has to depart from the target
	// before anything moves.
	//
	// One point is a sixth of a doubling, that is 12% of the bits: below that
	// threshold it would be chasing the noise of a measurement taken over one
	// second.
	qualityDeadZone = 1

	// qualityOvershootNoise: by how much the throughput has to exceed the cap,
	// as a percentage, before correcting it is worth while.
	//
	// **It is the same quantity as the dead zone, applied to the bytes instead of
	// the quantiser**, and it sits between two measured phenomena: on Quick Sync
	// stuck at the cap the throughput of one second swings between 2078 and 2906
	// against a cap of 2500 — mean 2557, that is a real overshoot of 2% but
	// **peaks of 16%** — while the real overshoot measured on AMD was 66% (4156
	// against 2500).
	//
	// Twenty and not fifteen, and the difference is instructive: at fifteen the
	// highest peak of the real sequence still got through. **A single sample does
	// not separate the two phenomena any more finely than that**, and it is the
	// same reason that elsewhere in this file a reading is judged over a window.
	// Below that line the surplus is accepted: correcting it would mean chasing
	// the noise, and with the quality already below the target it would mean
	// taking bits from an encoder that needs them.
	qualityOvershootNoise = 20

	// qualityThroughputWindow: how many samples an overshoot is judged over.
	//
	// **Four seconds, that is two GOPs, and they are needed because the dead zone
	// above is tuned on the noise of the full bitrate and at the bottom of the
	// scale it is no longer enough.** Measured on AMD, asking for 600 kbit/s with
	// a GOP of 2 s: the throughput of one second alternates 876, 508, 858, 534,
	// 964, 588, 884, 442 — and **every high second is a second with the
	// keyframe**, without a single exception. The mean sits at the cap, the odd
	// seconds exceed it by 50-60%.
	//
	// At full bitrate the phenomenon is invisible: there the alternation is 5%
	// and the dead zone absorbs it, which is why it never emerged. But the cost
	// of a keyframe does not fall with the budget, so its share grows on the way
	// down — at the bottom of the scale, that is exactly where the monitor
	// spends its worst nights, the high sample is worth nearly twice the cap.
	// Reacting to that cut the request by 40% one time in two, for a keyframe the
	// encoder had to send anyway.
	//
	// Two GOPs and not one: with one, a badly aligned window contains two or
	// zero. With two the keyframe share is the same in every window, which is the
	// property that is wanted — not averaging for longer, but averaging **always
	// the same number of keyframes**.
	//
	// It holds only for the cap on the bytes. The loop's step stays anchored to
	// the sample: there an error is corrected on the next turn, while the cap is
	// a one-way cut.
	qualityThroughputWindow = 4

	// qualityQPWindow: how many samples the quantiser is judged over.
	//
	// **Two GOPs, exactly like the throughput, and for the same phenomenon seen
	// from another side.** The quantiser arriving here is the p90 of **one
	// second**, the GOP is two: one second in two contains a keyframe, and the
	// frames around it are encoded differently by construction. The result is an
	// alternation, and it is not a hypothesis — from the log, Quick Sync, target
	// 30:
	//
	//	qp  28  28  32  28  32  27  28  28  33  27  28  28  32  28  28  33
	//
	// **None of those values sits in the dead zone**, which is one point: on
	// every turn the loop commands, alternating up and down. Measured, **74
	// commands in 9 minutes**, one every 7.5 seconds — and on a machine where the
	// bitrate is changed by rebuilding the encoder, every command drains the
	// transform, that is throws away the frames in flight. Those are the
	// micro-stutters visible while watching.
	//
	// The target, what is more, falls **in the middle** of the alternation: the
	// mean of that sequence is 29.5. The loop was chasing a value it could not
	// reach, because the quantity it read never passed through it.
	//
	// **The average lives here and not in the pipeline**, and that is the
	// decision that matters: two readers take that number, and they want two
	// different things. The scale needs **the instant** — one bad second has to
	// be caught straight away, and its threshold is 38 — while this loop needs
	// **the stretch**, because it decides how many bits to buy for the scene and
	// not whether the picture has just broken. Averaging in the pipeline would
	// have slowed the scale down in order to cure the quality: it is damped where
	// it is decided, not where it is measured.
	//
	// The delay that follows — four seconds — does not touch the urgent case:
	// movement in the room skips the quantiser entirely and goes to the cap in
	// one tick. And the loop waits five seconds between commands anyway, so the
	// window is shorter than its own wait.
	qualityQPWindow = 4

	// qualityDescentDamping: how much of the correction is applied when coming
	// down.
	//
	// **It goes up whole and comes down by half**, and it is this project's usual
	// asymmetry: going up repairs a defect that is visible, coming down only
	// saves. Being wrong upwards costs a few bits, being wrong downwards costs a
	// picture worse than the one the user asked for. And halving the step on the
	// way down also damps the oscillation, because the sensor has a second of
	// delay and so does the encoder.
	qualityDescentDamping = 0.5

	// qualitySettle: how long to stay still after moving the bitrate.
	//
	// The encoder answers a change within a second — measured, 824 → 2433 kbit/s
	// with the quantiser from 32 to 26 in the same sample — but the next sample
	// still contains frames encoded with the old value: **an encoder is not
	// judged while it is answering.**
	//
	// **Five seconds, not two, and the number is not chosen here: it is what the
	// encoder watch takes to form an opinion.** That one weighs the bytes over a
	// four-second window; commanding faster puts a change inside every window, so
	// the throughput it measures still describes the previous request.
	//
	// Observed live, and the fault was anything but theoretical: coming down 631
	// → 372 → 300 the watch compared 300 with the 553 produced while 631 was
	// being asked for, concluded "the encoder does not obey" and went over to
	// reconfiguration — which on AMD produces `ProcessOutput: 0x8000FFFF` and
	// **interrupts the capture**. Four restarts in eighty seconds.
	//
	// Half of that was the comparison itself, and `bitrateAsked` has since been
	// corrected to weigh against the **highest** request of the window rather
	// than the lowest. The wait is the other half, and it survives that fix: two
	// commands inside one four-second window make the measurement describe
	// neither of them.
	//
	// Whoever touches this value should look first at `bitrateVerifyAfter` in
	// internal/pipeline: they have to stay one longer than the other.
	qualitySettle = 5 * time.Second
)

// **The quantiser is not judged here.** A safety net saying "if the bits come
// down and the quantiser does not move, that reading does not measure this
// encoder" produced a false positive on AMD in under a minute: bits down 55% and
// the quantiser stuck at 23 is the **definition of this loop succeeding**, not a
// symptom. The protection lives where the question has a clean answer —
// `pipeline.qpMeasured`, which does not accept a reading until it has seen it
// change at least once.

// qualityGovernor chooses the bitrate to ask for, inside the cap, to hold the
// quantiser at the target.
//
// Like the other governors it knows neither the encoder nor the network: it
// takes numbers and an instant and says what bitrate to sit at. It is written
// that way so it can be checked without hardware, which counts double here — the
// fault it guards against is an oscillation, and an oscillation never reproduces
// twice alike from life.
type qualityGovernor struct {
	targetQP int
	current  int
	moved    time.Time

	// throughput are the latest samples of kbit/s produced, so an overshoot can
	// be judged over a window instead of over a sample. See
	// qualityThroughputWindow.
	throughput []int

	// qp are the latest quantisers read, for the same reason and with the same
	// window. See qualityQPWindow.
	qp []int

	// wasFullSize is the size of the previous turn, and it serves to keep out the
	// quantiser of the tick that **enlarges**: see `target`.
	wasFullSize bool
}

func newQualityGovernor(targetQP, start int) *qualityGovernor {
	if targetQP <= 0 || targetQP > 51 {
		targetQP = 0
	}
	// **A new governor is born at full size**, and that is not a detail of the
	// field: the capture starts from the preset, so the first turn is not a
	// transition and its reading is good. Born at `false` it would throw away
	// the first sample at every start-up and every capture restart, that is, pay
	// the remedy where the defect is not.
	return &qualityGovernor{targetQP: targetQP, current: start, wasFullSize: true}
}

// note records a throughput sample and returns the window's average, or **zero
// until the window is full**.
//
// Zero here means "I do not know", and whoever reads it corrects nothing.
// Returning the average of a partial window would be worse than keeping quiet:
// at the first sample the "average" is one sample, and one time in two that
// sample is the second with the keyframe — that is, exactly the value this window
// exists to defend against. It is the same mistake already paid for with the
// quantiser thresholds, where the reference was published after ten samples while
// the average needed thirty-two to forget the first.
//
// Zero and negatives on the way in do not enter the window: they are "I do not
// know" too, and averaging them would say the encoder produced little instead of
// saying we do not know.
func (g *qualityGovernor) note(produced int) int {
	if produced > 0 {
		g.throughput = append(g.throughput, produced)
		if len(g.throughput) > qualityThroughputWindow {
			g.throughput = g.throughput[len(g.throughput)-qualityThroughputWindow:]
		}
	}
	if len(g.throughput) < qualityThroughputWindow {
		return 0
	}
	s := 0
	for _, v := range g.throughput {
		s += v
	}
	return s / len(g.throughput)
}

// atCap is what is asked for when there is nothing to decide about quality: the
// cap, less the correction on the bytes if the encoder overshoots it.
//
// **The correction stays here too, and that is not a detail.** The two cases
// that lead to this fallback — a shrunken picture and a quantiser not yet known
// — are the ones where the network is suffering most, and that is where an
// encoder with a wide calibration does the worst damage: the surplus goes onto
// the network anyway and feeds the congestion being responded to. Asking for the
// bare cap here means, on AMD, sending 66% more.
//
// With the throughput still unknown `byteCeiling` touches nothing, so the
// fallback's fallback stays the cap.
//
// **The question is asked of the cap, not of the current request**, and it is
// easy to conclude that `byteCeiling` cannot be reused here. That is true of a
// guard asking "does what comes out exceed the cap?" — because on this road that
// answer is the consequence of the correction: as soon as the correction works
// the guard judges it useless and takes the request back to the cap, the encoder
// overshoots again, and it starts over. Measured with an encoder producing 1.66
// times what it is asked for: **19 commands over 30 turns**, cycle 2500 → 1672 →
// 1098 → 851 → 2500 for ever.
//
// The wrong conclusion is to write a copy of it here instead of **fixing the
// guard**, and the two copies then diverge in the usual way: the motion jump
// called `byteCeiling`, that is the version that never fires on a discounted
// request, and with a generous encoder it put 4150 kbit/s on a cap of 2500. Now
// there is one question and `byteCeiling` asks it for everybody: **how much would
// come out if I asked for this?**
//
// **And the wait holds here too.** The throughput is an average over four
// seconds: straight after a command it still describes the previous request, and
// deciding on that is the definition of a loop that oscillates. It is the usual
// five seconds, for the usual reason — an encoder is not judged while it is
// answering.
func (g *qualityGovernor) atCap(capKbps, meanThroughput int, now time.Time) (int, bool) {
	want := max(
		// **The floor holds on every road.** Below `bitrateFloorKbps` the right
		// answer is not to take more bits away but to send fewer pixels, and there
		// the quantiser read is no longer a measurement: without this line the
		// correction came down to twenty-five kbit/s.
		g.byteCeiling(capKbps, capKbps, meanThroughput), bitrateFloorKbps)
	if want == g.current {
		return g.current, false
	}
	if !g.moved.IsZero() && now.Sub(g.moved) < qualitySettle {
		return g.current, false
	}
	g.current = want
	g.moved = now
	return g.current, true
}

// noteQP averages the quantiser over its window, with the same rules as `note`:
// it is fed on every turn, it does not answer until it is full, and non-positive
// values do not enter because they are "I do not know".
//
// **It rounds instead of truncating**, and that is not fussiness: truncating
// always shifts downwards, that is towards "the quality exceeds", that is towards
// the cut. A systematic error of half a point on a dead zone of one point is half
// a dead zone thrown away, and always on the same side.
func (g *qualityGovernor) noteQP(qp int) int {
	if qp > 0 {
		g.qp = append(g.qp, qp)
		if len(g.qp) > qualityQPWindow {
			g.qp = g.qp[len(g.qp)-qualityQPWindow:]
		}
	} else if len(g.qp) > 0 {
		// **A missing reading ages the window**, it does not leave it intact.
		// Discarding it and no more is enough while the window is not full; once
		// full, a `TakeRecentQP` answering `!ok` — capture restart, an encoder
		// emitting nothing — would leave **the previous average answering for
		// ever**, that is deciding on a scene that is no longer there. Measured
		// with an obedient encoder and an average stuck below the target: 1512 →
		// 1200 → 952 → 756 → 600, down to the floor, without a single reading.
		//
		// An isolated gap therefore costs one tick at the cap — the window is no
		// longer full, and not full means "I do not know" — and four in a row
		// empty it. It is the rule written at the top of this file: **the saving
		// is lost, never the picture.**
		g.qp = g.qp[1:]
	}
	if len(g.qp) < qualityQPWindow {
		return 0
	}
	s := 0
	for _, v := range g.qp {
		s += v
	}
	return (s + len(g.qp)/2) / len(g.qp)
}

// target says how much to ask the encoder for.
//
//	qp        the measured quantiser, zero if not known
//	produced  the kbit/s the encoder really produced, zero if not known
//	capKbps   the maximum allowed: the lesser of the preset and what the network holds
//	fullSize  the scale is at the first step, that is the picture is whole
//	stirred   an episode of movement in the room has just begun
//
// **Without a quantiser nothing is done and it sits at the cap.** It is the
// fallback for encoders we cannot read it from, and it is the earlier behaviour:
// the saving is lost, not the correctness.
func (g *qualityGovernor) target(qp, produced, capKbps int, fullSize, stirred bool, now time.Time) (kbps int, changed bool) {
	if g == nil || capKbps <= 0 {
		return capKbps, false
	}
	// The window is fed **on every turn**, before any early exit: sampling it
	// only when the loop reaches the bottom would take one sample every five
	// seconds, that is always in the same position relative to the GOP — which is
	// the way to turn the keyframe alternation into a constant bias instead of
	// averaging it away.
	meanThroughput := g.note(produced)
	// **The quantiser decided on is the window's, not the instant's.** See
	// qualityQPWindow: the instant alternates with the GOP, and chasing it made
	// the encoder be commanded every five seconds for ever. It is fed here, next
	// to the throughput, and for the same reason — before any early exit,
	// otherwise it would always be sampled in the same position relative to the
	// GOP.
	//
	// **And it is fed only at full size**, which is the other half of the same
	// rule. The quantisers read on a smaller picture describe an easier scene,
	// and keeping them across the size change makes the first turn of the whole
	// picture be decided on one that is no longer there: measured, a cut of 2500
	// → 1512 **in the instant the scale had just climbed back**, that is taking
	// bits away from exactly whoever had just asked for more. It is the same
	// reason as `scaleSettle`, on the other side: the encoder has just been
	// rebuilt and its first frames say nothing about the scene.
	//
	// Emptying it, the four seconds it takes to fill are spent at the cap —
	// which after an enlargement is exactly where one wants to be.
	//
	// **And the tick that enlarges does not yet carry the right quantiser.** The
	// scale writes the new size inside `scale.target()`, which the loop calls
	// **before** this governor: `fullSize` is therefore already true while this
	// turn's quantiser was produced by the small picture. Admitting it, the
	// just-emptied window restarts with a sample of an easier scene — measured
	// `[20 30 30 30]`, mean 28, and a cut of 2500 → 2138 four seconds after the
	// climb: the same fault as the 2500 → 1512 above, diluted to a quarter and
	// not removed.
	//
	// The first good sample is the next turn's, and the extra four seconds are
	// spent at the cap, which after an enlargement is where one wants to be. The
	// same holds for the very first turn, where the encoder has just been built.
	meanQP := 0
	if fullSize && g.wasFullSize {
		meanQP = g.noteQP(qp)
	} else {
		g.qp = nil
	}
	g.wasFullSize = fullSize

	// The cap always commands: it can come down for congestion from one instant
	// to the next, and there is nothing to think about there.
	if g.current > capKbps {
		g.current = capKbps
		g.moved = now
		return g.current, true
	}
	// **The saving only exists at full size.**
	//
	// Its premise is one thing: "the picture is already as we want it, so these
	// bits buy nothing". When the scale has **shrunk** the picture the premise is
	// false — there the extra bits buy **pixels**, which is precisely what the
	// scale is waiting for in order to climb.
	//
	// And there is worse: by sending little, one prevents discovering that more
	// could be sent. gcc measures only the traffic that passes in front of it, so
	// the estimate chases our throughput, and the scale climbs only if the
	// estimate covers the step above. **The saving starved the measurement that
	// was meant to end it.** From the log, nine minutes in a single session:
	//
	//	commands 74   asked median 576   qp median 28 (better than the target)
	//	estimate median 758, maximum 1487    climbing back to 720p needs 1547
	//
	// Four minutes stuck **sixty kbit/s below the threshold**, that is 4%. And
	// the proof that the network was not to blame is in the same log: as soon as
	// the viewer reopened the page, the estimate cleared, we sent 2237 kbit/s and
	// gcc measured **2253 on the same radio**. The traffic comes first, the
	// estimate follows.
	//
	// It is the same shape of fault already paid for once — "the loop starved the
	// tuning that fed it", the outer loop that was deleted — one floor up: then
	// it starved the quantiser reference, now gcc's estimate.
	//
	// **Until the window is full the quantiser is "I do not know"**, not zero:
	// the first four seconds are spent at the cap, which is the usual fallback.
	if !fullSize || g.targetQP == 0 || meanQP <= 0 {
		return g.atCap(capKbps, meanThroughput, now)
	}
	// **The room that moves revokes the discount, and in one go.**
	//
	// The saving is a loan granted on a premise: the room is still, so those bits
	// are not visible. When the room stops being still the premise is false, and
	// the right answer is not to rediscover one step at a time that bandwidth is
	// needed — it is to give the loan back and let the loop earn it again as it
	// always does.
	//
	// **Measured live, and the cost was not the ramp it looked like: it was a
	// command that did not arrive at all.** Quick Sync, a real viewer, a still
	// room until the loop had come down to 772 kbit/s, then somebody moves:
	//
	//	14:34:22.813  motion in the room  fraction=0.0078
	//	14:34:23.199  bitrate matched to quality  kbps=2500  qp=30  target_qp=30  produced=667  motion=true
	//	14:34:24.199  produced_kbps=2353  qp=26
	//
	// 386 milliseconds, and above all **`qp=30` with a target of 30**: the
	// shortfall was zero, that is squarely in the dead zone. Without this branch
	// the loop would not have made a slower climb, it **would have done nothing**
	// — in the instant the room moved the quantiser was still the previous
	// second's, and that second had the room still.
	//
	// It is the answer to a doubt that had been written down: "the movement comes
	// from the same source as the video, so the quantiser rises in the same
	// instant and the loop can climb on its own". No: `TakeRecentQP` is the p90
	// of a one-second window taken at the tick, so it arrives **afterwards**.
	//
	// **And it is not one episode: the same session had fourteen**, median 377 ms
	// from the movement to the command. In **thirteen of fourteen** the quantiser
	// was still against the target — ten times in the dead zone, and **three
	// times at 28, that is better than the target: the loop would have brought it
	// down.** The number describing the hard scene arrives after the hard scene
	// has begun, and in the meantime it goes on asking to save.
	//
	// **And when the quantiser does have time to rise, the ramp is there.** With
	// an encoder that reacts to bits the step is computed exactly and one command
	// is enough; but at night the quantiser does not react — it is the sequence
	// measured further down in this file, "asked 2237 qp=33 produced 1582 …
	// asked 796 qp=33 produced 563", with the camera at 9-20 fps. With a
	// shortfall of three points each step buys 41%: four commands and twenty
	// seconds from the bottom to the cap, against one and one second
	// (`TestWithAStuckQuantiserTheClimbTakesSeveralCommands`).
	//
	// That is, the worst case is not a rare case: it is the night, which is when
	// this program works.
	//
	// **It goes to the cap and not by a computed step.** A step would be what the
	// loop will do on the next turn anyway: it would gain a few seconds without
	// removing the ramp, which is the real cost. The cap removes it entirely, has
	// no constant to tune, and is limited by construction by the bandwidth the
	// network has already granted. The asymmetry is the usual one: **going up
	// repairs a defect that is visible, coming down only saves** — a false
	// positive costs a few seconds of bits the loop takes back on its own, a
	// missed alarm costs the picture in the moment that matters.
	//
	// **`g.current < capKbps` is the exact condition**, not an approximation:
	// with a target set, the only way to sit below the cap is for this loop to
	// have brought it down, because the clamp above always takes `current` back
	// to the cap. It is "only if it was the loop that lowered it because the
	// quality exceeded", written without a threshold.
	//
	// **And skipping the wait here is harmless, which is not obvious.** The five
	// seconds exist so as not to judge an encoder while it is answering, and so
	// as not to put two commands inside one `bitrateSeen` window. Here nothing is
	// judged — the decision comes from a sensor with no relation to the encoder —
	// and above all a request that **rises** cannot trip that watch: its first
	// branch clears the reference as soon as more is asked for than before
	// (`internal/pipeline`, `bitrateSeen`), and a verification is counted only
	// after asking for appreciably less. Covered by
	// `TestARiseNeverCountsAsAVerification`, which is the proof this whole branch
	// rests on.
	//
	// The jump rearms `moved`, so it **replaces** a command instead of adding
	// one: straight afterwards the loop carries on identically, descents
	// included.
	if stirred && g.current < capKbps {
		// **The cap is the one on the bytes, not the one on the number asked
		// for.** An encoder with a wide calibration produces more than it is
		// asked for even in CBR — measured on AMD, 4156 kbit/s with 2500 granted
		// — and that surplus goes onto the network anyway, where it is paid for
		// in lost packets. Jumping straight to the cap without this correction
		// would buy the good picture at the price of the stuttering picture,
		// which is the worse of the two faults; and the wait below would keep it
		// that way for five seconds.
		//
		// **And the question is the projected one**, that is "how much would
		// come out if I asked for the cap?". With a guard asking "does what comes
		// out exceed the cap?" this line would **never** have limited anything:
		// one only arrives here with a discount in force, so the throughput now
		// sits well below the cap and the guard does not fire. Measured, a 1.66x
		// encoder discounted to 576: a jump to 2500 and **4150 kbit/s on the
		// wire**, which is exactly what the comment claimed to prevent. And in
		// the one case where that guard did fire, `limit` always fell below
		// `current`, so it could only **cancel** the jump: there was no input for
		// which it really limited.
		//
		// **But this branch can only raise**, and that is the condition that
		// keeps it consistent with the rest of the chapter. When the encoder
		// produces so much more than it is asked for that even what comes out now
		// exceeds the cap, the limit falls **below** what is already being asked:
		// executing it here would mean that movement in the room **cuts** the
		// bitrate, and that `moved` keeps it cut for five seconds. A cut like
		// that is legitimate — the ordinary branch makes it one tick later, which
		// is its place — but this is not the branch that cuts, and above all it
		// is not true that "a rising request cannot trip the watch" if the
		// request falls.
		want := g.byteCeiling(capKbps, capKbps, meanThroughput)
		if want <= g.current {
			return g.current, false
		}
		g.current = want
		g.moved = now
		return g.current, true
	}

	if !g.moved.IsZero() && now.Sub(g.moved) < qualitySettle {
		return g.current, false
	}

	shortfall := meanQP - g.targetQP
	if shortfall <= qualityDeadZone && shortfall >= -qualityDeadZone {
		return g.current, false
	}

	// **The anchor is what comes out, not what we asked for.** Anchoring to the
	// request is how the previous loop came down into the void: with an encoder
	// producing more than it is asked for, it went on cutting a number that had
	// no relation to reality.
	base := produced
	if base <= 0 {
		base = g.current
	}
	// **But that holds only for coming down, and going up it is a spiral.** If
	// the encoder produces **less** than it was asked for, multiplying the
	// throughput by the factor lands below the request in force: one ends up
	// **cutting while the quality is getting worse**, which is the exact opposite
	// of this loop's job. Observed live on Quick Sync in dim light, target 30:
	//
	//	asked 2237  qp=33  produced 1582
	//	asked 1273  qp=33  produced  900
	//	asked  934  qp=32  produced  741
	//	asked  796  qp=33  produced  563
	//	asked 2500  qp=51  produced  541   ← the maximum the QP can be
	//
	// The encoder was under-producing because with little light the camera drops
	// to 9-20 fps while it believes it is working 30, and allocates the per-frame
	// budget accordingly. But the cause does not matter: **if the quality is
	// worse than the target, the encoder is starving, and one does not take the
	// plate away from the hungry.** Going up therefore starts from the greater of
	// the throughput and the request in force.
	if shortfall > 0 && g.current > base {
		base = g.current
	}

	// Six points of quantiser are a doubling of the bits, by the specification:
	// the step is computed, not chosen.
	exponent := float64(shortfall) / qpPerDoubling
	if shortfall < 0 {
		exponent *= qualityDescentDamping
	}
	want := min(int(float64(base)*math.Pow(2, exponent)+0.5), capKbps)

	// **The cap holds on the bytes that come out, not on the number we ask for.**
	//
	// An encoder with a wide calibration produces more than it is asked for even
	// in CBR: measured on AMD, **4156 kbit/s with 2500 granted**. That surplus
	// goes onto the network anyway and feeds the congestion being responded to,
	// which on WebRTC is paid for in lost packets — that is, in a stuttering
	// picture, the fault that is visible. Stopping the request at the cap is not
	// enough: one has to ask for little enough that **the cap comes out**.
	//
	// The correction is the inversion of the measured ratio and has no tuning: if
	// asking for X yields 1.66 X, asking for X/1.66 yields X. And it loosens on
	// its own, because it is recomputed on what comes out every turn: as soon as
	// the encoder comes back inside, the constraint stops biting. A one-off
	// correction would have no way back, and that is the asymmetry that has
	// already locked the small picture in for a whole session.
	//
	// It lives inside this loop and not in a governor of its own: it is the same
	// idea that holds the loop up — look at what comes out, not at what was asked
	// for — and a fourth controller would be a fourth way of quarrelling with the
	// other three.
	// **But an overshoot has to be confirmed by its measurement, not by a
	// sample.** The throughput of one second swings by 15% around its mean even
	// with the encoder stuck at the cap: measured on Quick Sync, twenty-five
	// samples between 2078 and 2906 with a mean of 2557 against a cap of 2500,
	// that is a real overshoot of 2%. Reacting to the single sample brought the
	// request down to ~2100 on the high samples and back to the cap on the low
	// ones, every five seconds, for ever — an oscillation of 15% commanded by
	// noise.
	//
	// The threshold sits between the two measured phenomena, like the bandwidth
	// collapse's: the noise reaches 15%, the real overshoot measured on AMD was
	// 66% (4156 against 2500). Below that line there is nothing to correct.
	//
	// **And the measurement is the window, not the sample**, for the reason
	// written next to `qualityThroughputWindow`: at the bottom of the scale the
	// second with the keyframe is worth nearly twice the cap, and correcting on
	// that cuts the request by 40% one time in two for a frame that has to be
	// sent anyway. The correction stays the inversion of the ratio, only measured
	// over two GOPs instead of over one second.
	want = min(
		// **The floor is the one the project has already decided on**, not a new one:
		// below `bitrateFloorKbps` the right answer is not to take more bits from the
		// same picture, it is to send fewer pixels — and the resolution scale sees to
		// that.
		//
		// It is not fussiness about constants: without this limit the loop came down
		// to 125 kbit/s and **oscillated** there, because at that bitrate a keyframe
		// every two seconds dominates the measurement window and the quantiser read
		// jumps between 23 and 40 from one sample to the next. Observed live: 125 →
		// 213 → 152 → 125 → 435. It is not the loop that is unstable, it is the
		// measurement that down there is no longer a measurement.
		max(

			g.byteCeiling(want, capKbps, meanThroughput), bitrateFloorKbps),
		// A cap below the floor commands anyway: it is the network speaking.
		capKbps)
	if want == g.current {
		return g.current, false
	}
	g.current = want
	g.moved = now
	return g.current, true
}

// byteCeiling brings the request down when what **would come out** exceeds the
// cap.
//
// It is the inversion of the measured ratio and has no tuning: if asking for X
// yields 1.66 X, asking for X/1.66 yields X. It loosens on its own, because it is
// recomputed on what comes out every turn.
//
// **The guard projects, and it is easy to make it measure instead.** Asking
// "does what comes out now exceed the cap?" is the right question only where the
// request in force is already the cap. Elsewhere it never fires — under a
// discount the throughput sits below the cap by definition — so a climb back
// towards the cap passed uncorrected and a generous encoder put 66% more onto
// the network. What is scaled instead is the measured ratio applied to the
// request about to be made: `throughput x want / current`.
//
// On the road where the request is the cap the two forms give the same number,
// because `want == capKbps`; the other two callers — the loop's climb and the
// motion jump — are the ones nobody was correcting.
//
// **It lives in a function because three callers use it**, and the reason is the
// usual one: written more than once it diverges, and here diverging means three
// different ideas of what a cap is. It has happened — `atCap` had its own
// projecting copy while the motion jump called this one, which then did not
// project.
func (g *qualityGovernor) byteCeiling(want, capKbps, meanThroughput int) int {
	if meanThroughput <= 0 || g.current <= 0 || want <= 0 {
		return want
	}
	wouldEmit := meanThroughput * want / g.current
	if wouldEmit <= capKbps*(100+qualityOvershootNoise)/100 {
		return want
	}
	if limit := g.current * capKbps / meanThroughput; limit < want {
		return limit
	}
	return want
}

// release realigns the governor when nobody is watching any more.
//
// **With no viewers the encoder goes back to the cap** — `bitrateGovernor.release`
// does that — and this governor knew nothing about it: it is not called at all at
// zero viewers, so `current` and the two windows stayed those of the last
// session. Measured: A leaves with a discount at 1191 and a window of 26; B
// arrives ten minutes later on a scene exactly at the target — where the right
// answer is **no command** — and the first tick asks for 2102, decided by A's
// quantisers. And until it realigns, `current` says 1191 while the encoder sits
// at the cap: the projection above reads that gap as an encoder producing 65%
// more, and cuts.
//
// It is the same reason the last viewer's estimate is not inherited: **a state
// that outlives the conditions it described is not memory, it is a number that
// ages.**
func (g *qualityGovernor) release(capKbps int) {
	if g == nil {
		return
	}
	g.current = capKbps
	g.qp = nil
	g.throughput = nil
	g.moved = time.Time{}
	g.wasFullSize = false
}
