package rtc

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/pion/interceptor/pkg/cc"
)

// The encoder's bitrate chases the bandwidth the link really holds.
//
// The estimate comes from Pion's congestion control (pkg/gcc), which reads the
// TWCC feedback the browser sends anyway. Here only *whether* and *by how much*
// to move the encoder is decided: the estimate is noisy and changes constantly,
// while every change of bitrate is a visible jump in quality.
//
// The rule is asymmetric: it comes down at once and climbs back slowly. Staying
// too high means losing packets, that is a stuttering picture; staying too low
// means only a less sharp one. The two errors do not cost the same.

const (
	// bitrateFloorKbps is the minimum below which it does not go.
	//
	// At 720p that is already a poor picture, but a recognisable one: below it,
	// instead of going on taking bits from the same resolution, the right answer
	// is to send fewer pixels, and that is what the resolution scale does.
	bitrateFloorKbps = 300

	// bitrateRiseInterval is how long the estimate has to stay high before it is
	// taken seriously. An estimate that is high for an instant is not available
	// bandwidth: it is a lucky instant.
	//
	// **It is the only caution left on the way up**, because once the wait has
	// passed it goes where the estimate says instead of approaching it by
	// degrees: see the comment in target. The timer also restarts on every
	// descent, so after a drop the network is left to settle before pushing
	// again.
	bitrateRiseInterval = 5 * time.Second

	// bitrateCollapseRatio separates a dip from a collapse, and it sits
	// **between two measured phenomena** rather than at a value chosen for
	// symmetry:
	//
	//   - the estimate's swing on a mobile network, with losses at zero and a
	//     perfect picture, reached **50%** of the current value (647 against
	//     1290 in the same minute);
	//   - the real collapse, the one that produced 33% of the packets lost, was
	//     at **12%** (300 estimated while we were sending 2500).
	//
	// 40% sits below the observed noise and well above the observed collapse. If
	// one day a deeper swing were seen, this is the number to revisit — not the
	// rule.
	bitrateCollapseRatio = 0.40

	// bitrateDeadband is the variation below which nothing is touched: a change
	// of 5% is invisible, and every command to the encoder is an opportunity for
	// disturbance.
	bitrateDeadband = 0.05

	// bitrateStepKbps rounds the commands, so as not to chase every swing of the
	// estimate with a different value.
	bitrateStepKbps = 50

	// bitrateHeadroom is the share of the bandwidth left to the video after the
	// headers.
	//
	// Every packet carries 12 bytes of RTP, the extensions, the SRTP
	// authentication tag and then UDP and IP; on top of that come the
	// retransmissions asked for with NACKs. On packets of ~1200 bytes that makes
	// a few percentage points, and 5% is a prudent estimate worth keeping:
	// getting it wrong on the high side costs sharpness, on the low side it
	// costs lost packets.
	bitrateHeadroom = 0.95

	// bitrateWarmup is the time granted to a new viewer before its estimate is
	// listened to.
	//
	// In the first seconds gcc has little feedback and its estimate swings: taken
	// seriously, it would bring the quality down right at the opening, that is in
	// the seconds whoever is watching is really watching, and then climb back
	// slowly. Measured: starting from 700 kbit/s declared, the estimate took some
	// ten seconds to reach the real 2500.
	bitrateWarmup = 8 * time.Second
)

// viewerEstimate is one viewer's estimate, with the moment they connected:
// before the warm-up ends it is not consulted.
type viewerEstimate struct {
	bwe   cc.BandwidthEstimator
	since time.Time
}

// bitrateGovernor translates the estimates into commands for the encoder.
//
// It knows neither the encoder nor the viewers: it takes a number and an instant
// and says what bitrate to sit at. It is written that way so it can be checked
// without a network.
type bitrateGovernor struct {
	// ceiling is the bitrate of the preset the user chose. It never goes above:
	// the quality is decided by whoever is watching, congestion can only force
	// it down.
	ceiling int
	current int
	// lastRise marks from when a climb may be attempted. A descent moves it too:
	// after a drop the network has to be left to settle before pushing again,
	// otherwise it oscillates between the two values that caused it.
	lastRise time.Time
}

func newBitrateGovernor(ceilingKbps int) *bitrateGovernor {
	if ceilingKbps < bitrateFloorKbps {
		ceilingKbps = bitrateFloorKbps
	}
	return &bitrateGovernor{ceiling: ceilingKbps, current: ceilingKbps}
}

// target returns the bitrate to impose and whether it changed.
//
// estimateKbps <= 0 means the congestion control does not have enough feedback
// yet: in that case nothing is touched, because the absence of an estimate is
// not an estimate of zero.
func (g *bitrateGovernor) target(estimateKbps int, loss float64, now time.Time) (int, bool) {
	// The losses command **before** the estimate, and in its absence too.
	//
	// A receiver declaring it did not receive nine packets in ten is not
	// expressing an opinion about the network: it is saying what happened to our
	// packets. It is the most direct datum this program can have, and it goes
	// before everything — including an estimate that has not come down, which is
	// exactly the case where the monitor stayed at 2500 while a phone was
	// drowning.
	if loss >= bitrateLossSevere {
		want := max(roundToStep(int(float64(g.current)*(1-bitrateLossReduction*loss))), bitrateFloorKbps)
		if want >= g.current {
			return g.current, false
		}
		g.current = want
		// After a descent the network has to be left to settle before pushing
		// again: it is the same wait as every other drop.
		g.lastRise = now
		return want, true
	}

	if estimateKbps <= 0 {
		return g.current, false
	}
	if estimateKbps < bitrateFloorKbps {
		estimateKbps = bitrateFloorKbps
	}
	if estimateKbps > g.ceiling {
		estimateKbps = g.ceiling
	}

	want := g.current
	switch {
	case estimateKbps < g.current:
		// Coming down it goes where the estimate says, **in one go**: coming
		// down in stages means going on losing packets at every stage.
		//
		// Whether the estimate **is to be believed** is another question, and it
		// is not decided here: see believableDrop. It arrives here already
		// filtered, because this piece has to stay the one that can be checked
		// without a network.
		want = estimateKbps
		g.lastRise = now

	case float64(estimateKbps) > float64(g.current)*(1+bitrateDeadband):
		// The first observation authorises nothing: it is the beginning of the
		// wait, not its end.
		if g.lastRise.IsZero() {
			g.lastRise = now
			return g.current, false
		}
		if now.Sub(g.lastRise) < bitrateRiseInterval {
			return g.current, false
		}
		g.lastRise = now

		// It goes **where the estimate says**, not a little at a time.
		//
		// Recovering a quarter of the gap each time took two minutes and forty
		// to reach the 2.4 Mbit/s the network held from the first second, that is
		// more than a whole glance. And it fed itself: gcc measures only the
		// packets that pass in front of it, so by sending little **the estimate
		// comes down to chase us**.
		//
		// The caution on the way up is all in the wait above. The estimate
		// already takes losses and delays into account: slowing it further is a
		// second ramp on top of its own. What protects is the other half of the
		// asymmetry — at the first lower estimate it comes down in one go.
		want = estimateKbps
	}

	want = min(max(roundToStep(want), bitrateFloorKbps), g.ceiling)

	// The dead band applies to the rounded result: it is the only way not to
	// emit commands that change nothing perceptible.
	//
	// Returning to the cap is exempt: it is the last command before standing
	// still, while letting it be absorbed would keep the monitor below the
	// chosen quality for the rest of its life.
	if want == g.current {
		return g.current, false
	}
	delta := want - g.current
	if delta < 0 {
		delta = -delta
	}
	if float64(delta) < float64(g.current)*bitrateDeadband && want != g.ceiling {
		return g.current, false
	}

	g.current = want
	return want, true
}

// release takes the bitrate back to the preset when there is nobody left
// watching.
//
// With no viewers there is no congestion to measure, and the estimate of the
// last one to leave says nothing about the next one's network: letting it be
// inherited would mean greeting whoever arrives with a quality reduced because
// of somebody else.
func (g *bitrateGovernor) release() (int, bool) {
	if g.current == g.ceiling {
		return g.current, false
	}
	g.current = g.ceiling
	g.lastRise = time.Time{}
	return g.current, true
}

func roundToStep(kbps int) int {
	return (kbps + bitrateStepKbps/2) / bitrateStepKbps * bitrateStepKbps
}

// The bandwidth estimate is the transport's, not the video's: the congestion
// interceptor counts every local stream (see BindLocalStream), so it includes
// the Opus packets too, and on the wire there are the headers and the
// retransmissions on top. Handing it whole to the encoder means putting more onto
// the network than it declared it holds, and that happens exactly when the
// bandwidth is short: it would answer congestion by feeding it.
//
// The two functions are each other's inverse. availableForVideo says what the
// encoder may ask for given an estimate; transportBudget says how much bandwidth
// is needed to sustain a given video, and serves to tell gcc where the limits
// are.
//
// The audio is subtracted whole and never compressed: a monitor that can be heard
// but not seen still gives warning that the child is crying, the other way round
// does not. It is a product decision, not an allocation detail.
func availableForVideo(estimateKbps, audioKbps int) int {
	if estimateKbps <= 0 {
		return 0
	}
	if v := int(float64(estimateKbps-audioKbps) * bitrateHeadroom); v > 0 {
		return v
	}
	return 0
}

// The budget rounds up: truncating, the round trip loses a kbit and the cap
// would be unreachable by a hair. Here the rounding to 50 would save it, which is
// a way of being correct by luck.
func transportBudget(videoKbps, audioKbps int) int {
	return int(math.Ceil(float64(videoKbps)/bitrateHeadroom)) + audioKbps
}

// worstEstimate is the lowest estimate among the connected viewers, together
// with their number.
//
// There is only one encoder, so it adapts to the worst: whoever has a good
// network sees less than they could because of whoever has a bad one. The
// alternative, while there is a single output, would be to make the picture break
// for somebody.
func (h *Hub) worstEstimate(now time.Time) (kbps, viewers int) {
	h.bweMu.Lock()
	defer h.bweMu.Unlock()

	for _, e := range h.bwe {
		viewers++
		if now.Sub(e.since) < bitrateWarmup {
			continue
		}
		bps := e.bwe.GetTargetBitrate()
		if bps <= 0 {
			continue
		}
		if k := bps / 1000; kbps == 0 || k < kbps {
			kbps = k
		}
	}
	return kbps, viewers
}

// estimateCredibleRatio: below what fraction of what we really sent an estimate
// becomes news instead of an echo.
//
// A 15% gap is enough not to mistake the measurement's noise for congestion, and
// little enough not to lose a real drop, which when it happens is far larger —
// measured on a cellular network, the estimate came down to 300 while we were
// sending 2500.
const estimateCredibleRatio = 0.85

// estimateIsCredible says whether the bandwidth estimate describes the network
// or us.
//
// gcc can observe only the packets that pass in front of it: if we do not push,
// it has no way of knowing how much would get through, and its estimate settles
// on what we are sending. Taking it for a limit creates a vicious circle, and on
// a baby monitor it creates it in the ordinary condition of the night: **a still,
// dark room compresses beautifully**, so the encoder produces far less than it is
// asked for — measured, 1600 kbit/s against 2500 requested — the estimate comes
// down towards that value, the target follows it, production falls further, and
// so on. Observed live: estimate 1537 with ~1600 produced, that is, the estimate
// was an echo of our own throughput.
//
// A real drop is recognised because it sits **below** what we managed to send:
// there the network really did refuse something. Above, it refused nothing — it
// simply was not asked.
//
// Both quantities are in video bandwidth, otherwise a transport total and one
// stream would be being compared.
func estimateIsCredible(videoKbps, measuredKbps int) bool {
	if measuredKbps <= 0 {
		return true
	}
	return float64(videoKbps) < float64(measuredKbps)*estimateCredibleRatio
}

// atLeastWhatWeDelivered raises an estimate up to what we delivered.
//
// **If those packets arrived, the network carried them**: that is not an
// estimate, it is a fact, and no estimate can contradict it. The rule has no
// threshold to tune, and it is the only way out of the trap where an estimate
// echoing our own reduced traffic pulls the cap down, the cap reduces the
// traffic, and the reduced traffic confirms the estimate.
//
// **Losses take it out of the way**, and they are the case where the network
// really did refuse something: from a cellular network, 300 kbit/s estimated
// while we were sending 2500, with 33% of the packets lost. There what we
// "delivered" was not delivered at all.
//
// **And it holds in both directions: "I do not know" is not "zero".** With no
// known throughput nothing is raised, because there is no proof; but above all
// **an absent estimate is not raised at all**, because zero is not a low
// estimate, it is the vocabulary the two governors use to say "the congestion
// control does not have enough feedback yet". Raising it fabricated a real
// bandwidth equal to our own throughput exactly where nothing is known about it —
// the eight seconds of warm-up of **every** new viewer — and the cap went from
// 2500 to whatever the encoder was producing on an easy scene. It is the rule
// already written twice in this file, missed on the third.
func atLeastWhatWeDelivered(estimateKbps, deliveredKbps int, lossFraction float64) int {
	if estimateKbps <= 0 || deliveredKbps <= 0 || lossFraction >= bitrateLossQuiet {
		return estimateKbps
	}
	if estimateKbps < deliveredKbps {
		return deliveredKbps
	}
	return estimateKbps
}

// believableDrop says whether a drop in the estimate is to be believed.
//
// On radio gcc's estimate swings without anything having happened — its delay
// branch anticipates a queue on a fixed network, but on cellular the same signal
// is produced by schedulers, retransmissions and cell handovers. Following it
// into every dip produces a ratchet. So two ways out are needed, and both are
// needed because a deep buffer absorbs the excess without losing anything:
//
//   - **the losses** declared by the receiver;
//   - **the collapse**, that is an estimate reduced to less than half.
//
// **The collapse is measured against what we are sending, not against the cap
// granted.** With the saving switched on the bitrate sits well below the cap —
// 300 produced against 2500 — and there any estimate below 1250 would look like a
// collapse while it is only the echo of our 300. Cost of getting it wrong,
// measured: a resolution descent on localhost.
func believableDrop(estimateKbps, sentKbps int, lossFraction float64) bool {
	if sentKbps > 0 && float64(estimateKbps) < float64(sentKbps)*bitrateCollapseRatio {
		return true
	}
	return lossFraction >= bitrateLossQuiet
}

// sentWindowSamples: how many samples what we managed to send is measured over,
// when that number serves to **judge an estimate**.
//
// **Four seconds, that is two GOPs, and it is the same correction already made
// for the cap on the bytes.** The throughput of one second does not measure the
// encoder: it measures the GOP. One second in two contains the keyframe, and its
// share grows as the bitrate falls — from 5% at full bitrate to 90% at the
// bottom. Measured here, with the quality discount in force and a still room:
//
//	produced  789  75  560  113  558  116  543  116  864  70  651
//	          ^        ^         ^         ^         ^        ^
//	          the high seconds are the seconds with the keyframe
//
// Between one sample and the next there is a factor of **seven**, and
// `believableDrop` was comparing them with the estimate one by one: on the low
// seconds the estimate was an echo and was rightly refused, on the keyframe's
// second it became "a collapse". 338 against a threshold of 346 — two per cent —
// and the scale came down from 720p to 640x352 with the quantiser at 29, that is
// a perfect picture, zero packets lost and the real bandwidth at 2696. Twice in
// five minutes, for a minute each.
//
// Two GOPs and not one, for the usual reason: with one, depending on how it
// aligns, the window contains two keyframes or none; with two the share is the
// same in every window, which is the property that is wanted.
const sentWindowSamples = 4

// sentWindowMinSamples: from how many samples the window starts answering.
//
// **Two, and not four like its length.** With a GOP of two seconds, two
// consecutive samples contain exactly one keyframe, so the average of two is
// already **without bias** — four damp the rest, but the bias disappears at two.
// The difference matters because while the window keeps quiet no drop is
// credible, and that silence follows every reset: demanding four samples meant
// three turns of blindness to a collapse without losses after every size change
// and every bitrate command, that is **exactly while the network is getting
// worse**. With two the gap is one turn only.
const sentWindowMinSamples = 2

// sentWindow keeps the latest throughput samples for whoever has to judge an
// estimate instead of reacting to the instant.
//
// Whoever counts the encoder's overshoots does not use it: that one already has
// its own defence — three seconds in a row above the threshold — and there an
// isolated high sample decides nothing. Here, on the other hand, a single sample
// decided a resolution descent, which is a one-way cut.
type sentWindow struct{ samples []int }

// add records a sample and returns the average, or **zero until the window is
// full**.
//
// Zero means "I do not know", and for whoever judges an estimate that is the
// harmless direction: with an unknown throughput no drop is credible, so it does
// not come down. The saving is lost for a few seconds, never the picture.
//
// **A missing reading ages the window**, it does not leave it intact. Zero does
// not mean "it produced little", it means the capture is restarting — and
// averaging it would say the first thing. But keeping the previous value is
// worse: during a restart, which lasts up to half a minute, the window would go
// on answering with the throughput from **before** the restart, and an estimate
// that has decayed in the meantime would look like a collapse to it. So the
// oldest sample is removed: an isolated gap costs one tick, four in a row empty
// the window and take it back to "I do not know". It is the same rule already
// written for the quantiser window.
func (w *sentWindow) add(kbps int) int {
	if kbps > 0 {
		w.samples = append(w.samples, kbps)
		if len(w.samples) > sentWindowSamples {
			w.samples = w.samples[len(w.samples)-sentWindowSamples:]
		}
	} else if len(w.samples) > 0 {
		w.samples = w.samples[1:]
	}
	if len(w.samples) < sentWindowMinSamples {
		return 0
	}
	s := 0
	for _, v := range w.samples {
		s += v
	}
	return s / len(w.samples)
}

// reset empties the window, because **it does not survive a size change**:
// crossing one it would carry the samples of a larger picture with it, and the
// question "is the estimate well below what we are sending?" would get the
// previous size's answer. It is the same reason as the quality loop's window, and
// here it counts for more: a wrong answer after a descent authorises another one,
// that is a cascade.
func (w *sentWindow) reset() { w.samples = w.samples[:0] }

// bitrateLossQuiet is the fraction of lost packets below which the network is
// taken not to be refusing anything.
//
// It is not zero: an isolated loss happens on any link without meaning anything,
// and treating it as a refusal would bring the ratchet back. 1% is enough to
// cover the noise and well below the point where the picture suffers for it — by
// then the NACKs repair almost everything.
const bitrateLossQuiet = 0.01

// bitrateLossSevere is the loss beyond which it comes down **without asking the
// estimate's permission**.
//
// It is the correction of a defect that left a phone to die with 90% of its
// packets lost. The losses served only to **confirm** a drop gcc had already
// proposed, which leaves uncovered the case where the estimate does not drop at
// all — and that case is real:
//
//	estimate=2696  cap=2500  lost=89.8%  →  bitrate stuck at 2500
//
// There was no road by which a loss, however total, could bring the bitrate down.
//
// The 10% and the reduction factor are libwebrtc's loss-based controller's, not
// numbers invented here, and they sit above the noise bitrateLossQuiet already
// absorbs.
const bitrateLossSevere = 0.10

// bitrateLossReduction is how much of the loss translates into a reduction.
//
// At 90% loss it takes the bitrate to a little over half on every turn, that is
// from 2500 to the minimum in four seconds: as fast as it has to be, because
// while one insists the link goes on not getting through, and progressive instead
// of a single step, so a moderate loss does not empty the quality in one go.
const bitrateLossReduction = 0.5

// overshootTolerance is how much the encoder may overshoot before it is said.
//
// A little overshoot is normal: the bitrate control works over a window, and a
// scene that changes all at once costs more than the average. Beyond a quarter it
// is no longer settling.
const overshootTolerance = 1.25

// bitrateSettle is the time granted to the encoder to answer a command before its
// effect is measured.
//
// Three seconds is more than a GOP, which here is two: the frames already in
// flight have come out and the internal control has filled its window. Shorter
// produces false alarms on every descent; longer would leave too much of the
// encoder's life uncovered exactly when the bandwidth is swinging.
const bitrateSettle = 3 * time.Second

// checkOvershoot compares what the encoder produces with the ceiling it was
// given, and returns the measured throughput.
//
// **The ceiling, not the request.** Its one caller passes the bandwidth
// governor's current value — what the network granted — and that is the whole
// point: under a quality discount the request is driven to `cap / gain` so that
// the output lands on the cap, so comparing against the request would report the
// encoder's constant gain error as deafness on every tick. The doc used to say
// "what it was asked for", which described a quantity this function is never
// given and sent a reader to change the argument.
//
// It is needed because `SetBitrate` always answers yes. An encoder that accepts
// the command and goes on producing what it likes — on hardware other than ours
// that is a concrete possibility, not a theoretical one — would make the
// congestion control a fiction: the measured bandwidth would come down, we would
// blame the network, and the only way to tell the two cases apart is to weigh the
// frames.
//
// It is the same principle as the Invoke counter: an API's answer says the
// request was accepted, not that it was executed.
func (h *Hub) checkOvershoot(elapsed time.Duration, targetKbps int) (measuredKbps int, over bool) {
	if elapsed <= 0 {
		return 0, false
	}
	bytes := h.videoBytes.Swap(0)
	measuredKbps = int(float64(bytes) * 8 / 1000 / elapsed.Seconds())
	h.measuredKbps.Store(int64(measuredKbps))

	// The bytes are counted even with no viewers: the track accepts the samples
	// and lets them fall, so the encoder watch works on an unused monitor too.
	// Zero means something else — the capture is restarting — and there is
	// nothing to compare there.
	if measuredKbps == 0 || targetKbps <= 0 {
		return measuredKbps, false
	}
	return measuredKbps, float64(measuredKbps) > float64(targetKbps)*overshootTolerance
}

// lossWindow is how long a declared loss stays valid.
//
// The receiver's reports arrive about once a second; three seconds cover a few
// missed reports without keeping a finished fault alive for ever.
const lossWindow = 3 * time.Second

// recordLoss notes the fraction of packets lost declared by one viewer.
func (h *Hub) recordLoss(fraction float64, now time.Time) {
	h.lossMu.Lock()
	defer h.lossMu.Unlock()
	// The worst of the window is kept: there is only one encoder and it adapts to
	// whoever is worst off, as with the bandwidth estimate.
	//
	// The instant noted is that of the **value kept**, not that of the last
	// report received. Updating it on every report made one viewer's loss window
	// be refreshed by another's reports: with a phone losing packets and a healthy
	// PC, the PC kept the phone's 89.8% alive for as long as it went on sending
	// its zeros, and the loss never expired.
	if now.Sub(h.lossWorstAt) > lossWindow || fraction >= h.lossWorst {
		h.lossWorst = fraction
		h.lossWorstAt = now
	}
}

// recentLoss is the worst fraction lost declared recently, zero if nobody has
// complained.
func (h *Hub) recentLoss(now time.Time) float64 {
	h.lossMu.Lock()
	defer h.lossMu.Unlock()
	if h.lossWorstAt.IsZero() || now.Sub(h.lossWorstAt) > lossWindow {
		return 0
	}
	return h.lossWorst
}

// MeasuredVideoKbps is the video throughput measured over the last second.
func (h *Hub) MeasuredVideoKbps() int { return int(h.measuredKbps.Load()) }

// TargetBitrateKbps is the bitrate the encoder is receiving now.
//
// The status page shows this and not the preset's: they are the same thing only
// while the network holds, and the difference between the two is exactly the
// useful information when somebody says the picture looks bad.
func (h *Hub) TargetBitrateKbps() int {
	if v := h.targetKbps.Load(); v > 0 {
		return int(v)
	}
	return h.cfg.BitrateKbps
}

// RunBitrateControl matches the encoder's bitrate to the measured bandwidth and
// watches that the encoder obeys, until the context is cancelled.
//
// The sampling is one second: gcc updates far more often, but what matters here
// is the trend, not the last value.
//
// The loop runs even without OnBitrate: the encoder watch and the measured
// throughput are needed anyway, and they are the only things that say whether
// bandwidth control really takes on this machine.
func (h *Hub) RunBitrateControl(ctx context.Context) error {
	g := newBitrateGovernor(h.cfg.BitrateKbps)
	h.targetKbps.Store(int64(g.current))
	// **The scale is born on the size the capture really starts from**, and where
	// nobody can say it falls back to the configured one, which is what a Hub
	// built without VideoStartSize gets. It is left nil here on purpose when that
	// answer exists: the capture opens a moment after this loop starts, so the
	// first tick that knows builds it — and builds it once, instead of building
	// it on the preset and announcing a rebuild at every start-up.
	var scale *scaleGovernor
	if h.cfg.VideoStartSize == nil {
		scale = newScaleGovernor(h.cfg.Width, h.cfg.Height, h.cfg.FPS)
	}
	// The cadence declared to the encoder starts from the preset's and chases the
	// real one: see cadence.go.
	cadence := newCadenceGovernor(h.cfg.FPS)
	// The loop that holds the quality at the target while spending as little as
	// possible. Nil if nobody asked for a target: then it sits at the cap, as it
	// always has.
	var quality *qualityGovernor
	if h.cfg.TargetQP > 0 {
		quality = newQualityGovernor(h.cfg.TargetQP, h.cfg.BitrateKbps)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	last := time.Now()
	// The overshoot is reported after several turns in a row, not on the first: a
	// single second above the threshold is a keyframe, not an encoder ignoring
	// commands.
	var over int
	const overStreak = 3
	// settle is until when the encoder is still answering the last command, and
	// so is not judged.
	var settle time.Time
	// sent is what we managed to send, over two GOPs: the throughput of one
	// second alternates with the keyframe, and judging an estimate on a single
	// sample is what brought the resolution down on a perfect picture.
	var sent sentWindow

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			// **The scale is rebuilt when the size the capture starts from
			// changes**, which is what happens when the camera is changed while
			// the monitor runs. Its steps are fractions of that size, so on
			// another camera nothing that arrives matches a step, and resync —
			// which deliberately does not invent a size that is not a step —
			// would stay out of alignment for the rest of the session, with a
			// false atFullSize switching the saving off and nailing the bitrate to
			// the cap.
			//
			// Rebuilding throws away the judgements of the scene before, and that
			// is right rather than a cost: it is another camera, in another place,
			// pointed somewhere else.
			if sw, sh, sf := h.startSize(); sw > 0 && !scale.builtOn(sw, sh, sf) {
				if scale != nil {
					h.log.Info("resolution scale rebuilt: the capture size changed",
						"from", fmt.Sprintf("%dx%d@%d", scale.baseW, scale.baseH, scale.baseFPS),
						"to", fmt.Sprintf("%dx%d@%d", sw, sh, sf))
				}
				scale = newScaleGovernor(sw, sh, sf)
				// **A window is an assertion about a stretch, and the stretch
				// has just changed.** The throughput samples were produced by
				// the previous camera, and they feed estimateIsCredible,
				// believableDrop and atLeastWhatWeDelivered: a camera swap takes
				// about a second, so only one or two of the four samples age out
				// on their own and the mean goes on describing a size that is no
				// longer being sent. It is the same reset the other three
				// commands do.
				sent.reset()
			}

			// **The cap, deliberately, and not what the quality loop asked
			// for.** The two part company only under a discount, and there the
			// request is the wrong denominator: `atCap` drives it to
			// `cap / gain` precisely so the **output** lands on the cap, so
			// `produced / requested` is the encoder's constant gain error, for
			// ever. On a chip that overproduces by a third — which this file
			// records as obedience and not deafness — that is above the 1.25
			// tolerance every second, and the fixed point is exactly the state
			// where commands stop, so `settle` expires and the streak completes:
			// the log would say "congestion control has no effect on this
			// encoder" all night about an encoder that is obeying. It was tried
			// and reverted.
			//
			// What this measures is therefore the surplus over **what the network
			// granted**, which is the quantity worth a line in the log; whether
			// the encoder follows a command is asked elsewhere, by weighing the
			// throughput against the request over a window — `bitrateSeen` in
			// internal/pipeline, whose signature is "does not follow" rather than
			// "sits above".
			measured, overshoots := h.checkOvershoot(now.Sub(last), g.current)
			last = now
			// The window is fed on every turn, before any decision: it is the
			// only way for it to always contain the same number of keyframes.
			// Zero until it is full, which means "I do not know".
			sentAvg := sent.add(measured)
			// An encoder is not judged while it is answering. After a command the
			// frames already in flight come out with the old bitrate, and the
			// internal control works over a window that has to be filled:
			// measured, straight after a descent from 2500 to 1900 the throughput
			// was still 2628, and the watch cried that the command had not taken
			// when it very much had. A false alarm here is expensive — it would
			// send somebody looking for a fault in the encoder while it is
			// elsewhere.
			//
			// Whoever really does ignore commands is above the threshold
			// afterwards too, so nothing is granted to them: only the transient
			// is skipped.
			if now.Before(settle) {
				overshoots = false
				over = 0
			}
			if overshoots {
				if over++; over == overStreak {
					h.log.Warn("the encoder produces more than it is asked for",
						"granted_kbps", g.current, "measured_kbps", measured,
						"note", "congestion control has no effect on this encoder")
				}
			} else {
				over = 0
			}

			estimate, viewers := h.worstEstimate(now)

			video := availableForVideo(estimate, h.cfg.AudioKbps)

			// An estimate that does not sit below what we managed to send is not
			// a limit of the network: it is an echo of our own throughput, and it
			// must not bring anything down.
			//
			// The two governors have to be told in two different ways: the
			// **bitrate** is passed the current value, which for it is a
			// do-nothing; the **scale** is told zero, which in its vocabulary is
			// "I do not know" and not "zero bandwidth". Giving it the current
			// bitrate means passing off a number of ours as a measurement of the
			// network, and from life that brought it down to 960 without anybody
			// having measured anything.
			//
			// Two different reasons not to believe a low estimate, and they have
			// to be kept apart because they describe two different faults:
			//
			//   - the estimate **echoes our own throughput**, because on a still
			//     scene we produce less than we ask for and gcc can measure only
			//     what passes in front of it;
			//   - the estimate **has come down without anything being lost**,
			//     which on radio is the ordinary case: the delay swings on its
			//     own.
			//
			// **Both questions are asked of the window, not of the sample.**
			// Between two consecutive seconds the throughput changes by a factor
			// of seven, and what decides it is whether that second contained the
			// keyframe: on the high second any echo looks like a collapse. See
			// sentWindowSamples.
			loss := h.recentLoss(now)
			//
			// **With the throughput unknown no drop is believed.** The window
			// answers "I do not know" for its first samples and every time the
			// capture stops, and there `estimateIsCredible` on its own would say
			// yes: that function reads the zero as "there is nothing to compare
			// with, the estimate is all we have", which is right for a missing
			// sample and wrong for an empty window. The losses stay the
			// exception, because they are not an opinion about the network: they
			// say what happened to our packets.
			credible := estimateIsCredible(video, sentAvg) &&
				(video >= g.current || believableDrop(video, sentAvg, loss)) &&
				(sentAvg > 0 || loss >= bitrateLossQuiet)

			// The scale is **always** passed the real estimate, with the judgement
			// of how credible it is alongside: it needs it in order to climb even
			// when it echoes our own throughput, because in that case it is a
			// lower bound. The bitrate is told to stay where it is, which for it
			// is a do-nothing.
			// **What we delivered without losing anything is a demonstrated lower
			// bound on the capacity, and no estimate can take us below it.**
			//
			// It is the remedy for the trap that broke the picture in the moment
			// that matters. Observed on AMD: the estimate echoed our reduced
			// traffic, the cap came down to 350 kbit/s with losses at **0.0%**,
			// and from there there was nothing left to give back — at the first
			// real movement the quantiser went to 38, 49, 43 and the loop could
			// raise nothing, because `current` was already the cap. The room
			// moved and the picture broke **because of a rule of ours**, on a link
			// that a few seconds earlier measured 2696.
			//
			// The rule has no threshold to tune, because it is not an estimate: if
			// those packets arrived, the network carried them. The losses take it
			// out of the way — there the network really did refuse something, and
			// it is the cellular case already measured, 300 asked against 2500
			// sent with 33% of the packets lost.
			video = atLeastWhatWeDelivered(video, sentAvg, loss)

			videoForScale := video
			if !credible && video < g.current {
				video = g.current
			}

			// The estimate is written on every turn, even when it moves nothing:
			// when somebody says the picture looks bad, the first question will
			// be what bandwidth the monitor was measuring at that moment, and
			// without this line the answer would be a deduction.
			qp := -1
			if h.cfg.QP != nil {
				if v, ok := h.cfg.QP(); ok {
					qp = v
				}
			}
			// The quantiser is now read from the stream and not asked of the
			// encoder, so it is a real measurement on any machine and no longer
			// has to be discarded in any case. See internal/media/qp.go.

			// **It is consumed on every turn, even with no viewers.** An episode
			// that began while nobody was watching is not news for whoever
			// arrives afterwards: it is the same reason the last viewer's
			// estimate is not inherited. Consuming it only inside the quality
			// branch, a movement at midnight would send the six o'clock session
			// to the cap.
			stirred := false
			if h.cfg.MotionStarted != nil {
				stirred = h.cfg.MotionStarted()
			}

			// One read of what is in force for this turn: the debug line below and
			// the ceiling test further down have to describe the same value, and
			// nothing guarantees that of two separate loads.
			inForce := h.targetKbps.Load()
			lim := qpThresholds()
			// **The cap and the bitrate in force are two numbers, and this line
			// used to print the first under the second's name.** `g.current` is
			// what the network allows; what the encoder is really being asked for
			// is in `targetKbps`, because the quality loop lowers it inside that
			// ceiling afterwards. On a session where the saving worked — asked
			// 1940 and 2028, produced 1940 and 2032 — all 706 of these lines
			// carried `bitrate_kbps=2500`, so whoever grepped the log for the
			// bitrate found the preset seven hundred times and concluded the
			// saving had never come on. Both numbers are useful and each keeps its
			// own name.
			//
			// It is read before this turn's decision, so it is the value in force
			// as the line is written — which is what "the bitrate the monitor is
			// at" means.
			h.log.Debug("bandwidth estimate", "estimate_kbps", estimate,
				"for_video_kbps", video, "cap_kbps", g.current,
				"bitrate_kbps", inForce,
				"produced_kbps", measured, "qp", qp,
				"loss_pct", fmt.Sprintf("%.1f", loss*100), "viewers", viewers)

			// The scale is consulted before the bitrate and with the same
			// bandwidth: changing size, the bitrate the new step deserves is a
			// different one, and deciding it afterwards avoids commanding the
			// encoder twice in the same second.
			//
			// Taking pixels away is the last resort: while there is bandwidth to
			// spend, a high quantiser is answered by buying bits — a size change
			// is visible, a bitrate increase is not. The two sequence themselves,
			// because the need for bits reaches the cap in a second or two and
			// only then is the scale authorised.
			//
			// **It is read from `targetKbps`, the only place what we really
			// commanded ends up.** A local initialised to the preset and never
			// assigned again makes the guard always true, and with the saving
			// switched on the scale then takes pixels away from a picture that was
			// perfectly fine — 720p → 960 → 720p → 640 in forty seconds, with the
			// bandwidth having nothing to do with it.
			maxBits := int(inForce) >= g.current

			// **Before deciding, look at what we are really sending.** A format
			// request may have been refused by the pipeline or lost in a capture
			// restart, and from then on this governor would be reasoning about a
			// step that does not exist — never noticing, because it commands only
			// on changes. See `scaleGovernor.resync`.
			//
			// It is done with no viewers too: the capture restart does not wait
			// for somebody to watch, and whoever connects afterwards has to find
			// an aligned governor.
			if scale != nil && h.cfg.VideoFormat != nil {
				if aw, ah, af := h.cfg.VideoFormat(); aw > 0 {
					if step, moved := scale.resync(aw, ah, af, now); moved {
						sent.reset()
						h.log.Warn("the scale was not aligned with what is being sent",
							"sending", fmt.Sprintf("%dx%d@%d", aw, ah, af),
							"step", step,
							"why", "a format request was refused or lost in a capture restart")
					}
				}
			}

			if scale != nil && h.cfg.OnVideoFormat != nil && viewers > 0 {
				w, ht, fps, moved := scale.target(videoForScale, credible, qp, lim, maxBits, now)

				// **The cadence is declared, not assumed.** The encoder divides
				// the budget by the frames it believes it is receiving: telling
				// it 30 while the camera delivers 18 — which with little light is
				// normal — every frame gets a third less than its due, and the
				// bitrate produced collapses to 64% of what was asked for with
				// three and a half points of quantiser given away. See cadence.go.
				//
				// The cap is the cadence the scale wants: when it is the scale
				// coming down to the bottom steps it is already dropping frames,
				// and declaring more would be the same fault in miniature.
				// **`fps` is not overwritten.** What comes back from here is the
				// cadence to **declare**; the one to **deliver** stays the
				// scale's. Confusing them closed the loop: the declaration
				// commanded the cadence gate, the gate lowered the measured
				// cadence, and the measurement lowered the declaration again.
				// Measured on wifi, six minutes at 2 fps with the bandwidth back
				// at 2696 kbit/s.
				declaredFPS, cadenceMoved := cadence.target(h.measuredFPSAt(now), fps)
				if declaredFPS <= 0 {
					declaredFPS = fps
				}

				if moved || cadenceMoved {
					// The cadence is always written, even when it is not the one
					// that changed: whoever reads the log after a night has to be
					// able to say at a glance whether the monitor had come down to
					// the bottom steps, and "960x528" on its own does not say so.
					h.log.Info("video format changed",
						"video", fmt.Sprintf("%dx%d@%d", w, ht, fps),
						"declared", declaredFPS,
						"measured_fps", fmt.Sprintf("%.1f", h.measuredFPSAt(now)),
						"qp", qp, "break_threshold", lim.breakAt,
						"estimate_kbps", estimate, "for_video_kbps", video)
					// **A change of cadence alone rebuilds the encoder too**, and
					// with it the throughput changes: declaring 15 instead of 30
					// moves it by 45% on the same request. Tying the reset to
					// `moved` alone left the window full of the samples from
					// before the rebuild, which is exactly the false reading it
					// exists for.
					sent.reset()
					h.cfg.OnVideoFormat(w, ht, fps, declaredFPS)
				}
			}

			if h.cfg.OnBitrate == nil {
				continue
			}

			// The network's cap: how much **may** be sent.
			var kbps int
			var changed bool
			if viewers == 0 {
				kbps, changed = g.release()
				// **And the quality realigns with it.** This governor is not
				// called at all with no viewers, so without this line `current`
				// and its two windows stay those of the last session: whoever
				// arrives afterwards gets somebody else's discount, decided on a
				// scene from ten minutes ago. See `qualityGovernor.release`.
				quality.release(kbps)
				// **And the scale is not called either.** `target` is called only
				// with somebody watching, so its windows stay those of the last
				// session: whoever arrives afterwards gets a veto decided on a
				// scene from ten minutes ago, and a last estimate that is nobody's
				// any more. The **step** stays, and that is the difference: that
				// describes what the pipeline is really sending, not a judgement.
				scale.release()
			} else {
				kbps, changed = g.target(video, loss, now)
			}
			// The quality decides how much **needs** to be spent, inside that cap.
			//
			// It is an outer loop, and it is the only one: the encoder is given a
			// bitrate and no more, in CBR, which is the one thing every encoder
			// knows how to do. One has already failed, and what is different is
			// written in quality.go — in short: that one learnt its own target
			// while chasing it, this one was given the target.
			//
			// Only with somebody watching. With no viewers it sits at the cap, and
			// not out of laziness: saving bandwidth nobody is using would cost
			// nothing and gain nothing, and the loop's windows would fill with a
			// scene nobody is looking at.
			if quality != nil && viewers > 0 {
				if q, moved := quality.target(qp, measured, kbps, scale.atFullSize(), stirred, now); moved {
					// `motion` separates the loop's climbs from the room's.
					// Without that field, after a night the two are
					// indistinguishable, and the first question about a climb is
					// always who asked for it.
					h.log.Info("bitrate matched to quality",
						"kbps", q, "cap_kbps", kbps, "qp", qp,
						"target_qp", h.cfg.TargetQP, "produced_kbps", measured,
						// **The saving only exists at full size**, so whoever
						// reads the log has to be able to tell "it did not save
						// because the scene is hard" from "it could not save".
						// Without this field the two read the same.
						"full_size", scale.atFullSize(), "motion", stirred)
					kbps, changed = q, true
				} else if q != kbps {
					kbps = q
				}
			}

			if !changed {
				continue
			}

			h.targetKbps.Store(int64(kbps))
			settle = now.Add(bitrateSettle)
			h.log.Info("video bitrate matched to bandwidth",
				"kbps", kbps, "estimate_kbps", estimate, "viewers", viewers)

			// **And it does not survive a bitrate command.** During a ramp —
			// 2500, 1655, 1106, 737, 456, 300 — the average would lag four
			// seconds behind, that is, it would describe the previous request:
			// the echo threshold would stay twice what it should be and an echo
			// of the new traffic would read as a real limit. It is one sample's
			// staleness, amplified to four.
			sent.reset()
			h.cfg.OnBitrate(kbps)
		}
	}
}
