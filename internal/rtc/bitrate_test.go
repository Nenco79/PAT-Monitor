package rtc

import (
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
)

var t0 = time.Date(2026, 8, 10, 22, 0, 0, 0, time.UTC)

// fakeBWE is a constant estimate, so the choice between viewers can be checked
// without bringing a network into it.
type fakeBWE struct{ bps int }

func (f fakeBWE) AddStream(*interceptor.StreamInfo, interceptor.RTPWriter) interceptor.RTPWriter {
	return nil
}
func (f fakeBWE) WriteRTCP([]rtcp.Packet, interceptor.Attributes) error { return nil }
func (f fakeBWE) GetTargetBitrate() int                                 { return f.bps }
func (f fakeBWE) OnTargetBitrateChange(func(int))                       {}
func (f fakeBWE) GetStats() map[string]any                              { return nil }
func (f fakeBWE) Close() error                                          { return nil }

// TestAllocation checks that the video is not promised the bandwidth that goes
// on audio and headers on the wire.
func TestAllocation(t *testing.T) {
	const audio = 64

	// An estimate of 1000 is not 1000 of video: with the audio and 5% of headers
	// taken away, 889 are left.
	if got := availableForVideo(1000, audio); got != 889 {
		t.Errorf("availableForVideo(1000) = %d, want 889", got)
	}

	// Below the cost of the audio alone nothing is left for the video, and not a
	// negative number.
	if got := availableForVideo(40, audio); got != 0 {
		t.Errorf("availableForVideo(40) = %d, want 0", got)
	}

	// The two functions have to close: the bandwidth needed for a given video,
	// converted back, has to give that video again. If they did not, on a healthy
	// network the monitor would stop just below the preset for ever.
	for _, video := range []int{300, 500, 1200, 2500} {
		budget := transportBudget(video, audio)
		if got := availableForVideo(budget, audio); got != video {
			t.Errorf("transportBudget(%d) = %d, then availableForVideo = %d; want %d",
				video, budget, got, video)
		}
	}
}

// TestAFullNetworkStaysAtThePreset is the test for the regression the allocation
// could introduce: with the network holding the preset, the governor must not
// come down by a single step.
func TestAFullNetworkStaysAtThePreset(t *testing.T) {
	const audio = 64
	g := newBitrateGovernor(2500)
	estimate := transportBudget(2500, audio)

	for i := range 10 {
		video := availableForVideo(estimate, audio)
		if kbps, changed := g.target(video, 0, t0.Add(time.Duration(i)*bitrateRiseInterval)); changed {
			t.Fatalf("turn %d: came down to %d with the network holding the preset", i, kbps)
		}
	}
}

// The defect that left a phone drowning: very high losses and an immobile
// bitrate.
//
// From the log, for over a minute in a row: estimate 2696, cap 2500, lost 89.8%.
// The phone's estimator declared full bandwidth while its link was losing nine
// packets in ten, so the minimum among the viewers stayed the PC's and the
// descent branch — which fires only when the estimate sits **below** the current
// bitrate — was never taken.
func TestLossesBringItDownEvenIfTheEstimateDoesNot(t *testing.T) {
	g := newBitrateGovernor(2500)
	now := t0

	// The estimate says full bandwidth, as it really did.
	if _, changed := g.target(2500, 0, now); changed {
		t.Fatal("it changed something with no losses and the estimate at the cap")
	}

	// Now the receiver declares it is losing almost everything. The estimate does
	// not change.
	now = now.Add(time.Second)
	kbps, changed := g.target(2500, 0.898, now)
	if !changed {
		t.Fatal("with 89.8% of the packets lost it lowered nothing")
	}
	if kbps >= 2500 {
		t.Errorf("bitrate %d: it did not come down", kbps)
	}

	// And insisting it reaches the minimum quickly: while one pushes, the link
	// goes on not getting through.
	for range 10 {
		now = now.Add(time.Second)
		kbps, _ = g.target(2500, 0.898, now)
	}
	if kbps != bitrateFloorKbps {
		t.Errorf("after ten seconds of total loss the bitrate is %d, want the minimum %d",
			kbps, bitrateFloorKbps)
	}
}

// A modest loss must not bring anything down on its own: it is the noise any
// link produces, and it is already the reason bitrateLossQuiet exists. Below the
// severe threshold it keeps its role of **confirming** a drop the estimate has
// already proposed.
func TestAModestLossIsNotEnoughToBringItDown(t *testing.T) {
	g := newBitrateGovernor(2500)
	if _, changed := g.target(2500, bitrateLossSevere-0.01, t0); changed {
		t.Error("it came down over a loss below the severe threshold")
	}
}

// The worst loss has to **expire**, and not be kept alive by the reports of
// whoever is fine.
//
// With two viewers the reports arrive mixed together: if the instant noted were
// that of the last report of any kind instead of that of the value kept, the PC's
// zeros would refresh the phone's loss window for ever. Observed: 89.8% nailed
// down for over a minute, even after whoever was losing had left.
func TestTheLossExpiresEvenIfAnotherViewerIsTalking(t *testing.T) {
	h := New(Config{BitrateKbps: 2500})
	now := t0

	h.recordLoss(0.898, now)
	if got := h.recentLoss(now); got != 0.898 {
		t.Fatalf("loss not recorded: %v", got)
	}

	// The PC goes on declaring zero, once a second, for far longer than the
	// window.
	for range 10 {
		now = now.Add(time.Second)
		h.recordLoss(0, now)
	}
	if got := h.recentLoss(now); got != 0 {
		t.Errorf("after %v the loss is still %v: the reports of whoever is fine keep it alive",
			lossWindow, got)
	}
}

// TestWorstEstimate checks the two rules that decide which estimate counts: the
// worst is followed, and whoever has just connected is not listened to.
func TestWorstEstimate(t *testing.T) {
	h := New(Config{BitrateKbps: 2500})
	fresh, old := &Viewer{}, &Viewer{}
	h.bwe = map[*Viewer]viewerEstimate{
		fresh: {bwe: fakeBWE{bps: 600_000}, since: t0},
		old:   {bwe: fakeBWE{bps: 1_500_000}, since: t0.Add(-time.Minute)},
	}

	// One second after connecting, the newcomer's low estimate must not drag down
	// the quality of somebody already watching perfectly well.
	kbps, viewers := h.worstEstimate(t0.Add(time.Second))
	if kbps != 1500 || viewers != 2 {
		t.Errorf("in warm-up = %d kbps over %d viewers; want 1500 over 2", kbps, viewers)
	}

	// Once the warm-up is over its estimate counts, and it commands because it is
	// the worst: there is only one encoder.
	kbps, viewers = h.worstEstimate(t0.Add(bitrateWarmup + time.Second))
	if kbps != 600 || viewers != 2 {
		t.Errorf("in the steady state = %d kbps over %d viewers; want 600 over 2", kbps, viewers)
	}
}

// TestTheGovernorWithNoEstimate checks that the absence of an estimate is not
// mistaken for an estimate of zero: at the start gcc has no feedback yet, and
// coming down to the minimum at that moment would mean greeting every viewer
// with the worst picture.
func TestTheGovernorWithNoEstimate(t *testing.T) {
	g := newBitrateGovernor(2500)
	if kbps, changed := g.target(0, 0, t0); changed || kbps != 2500 {
		t.Errorf("target(0) = %d, %v; want 2500, false", kbps, changed)
	}
}

// TestTheGovernorComesDownAtOnce: the descent is the case that costs, and it is
// not done in stages.
func TestTheGovernorComesDownAtOnce(t *testing.T) {
	g := newBitrateGovernor(2500)
	kbps, changed := g.target(800, 0, t0)
	if !changed || kbps != 800 {
		t.Fatalf("target(800) = %d, %v; want 800, true", kbps, changed)
	}
	// A second drop, still immediate.
	if kbps, changed = g.target(400, 0, t0.Add(time.Second)); !changed || kbps != 400 {
		t.Errorf("target(400) = %d, %v; want 400, true", kbps, changed)
	}
}

// TestTheGovernorDoesNotGoBelowTheMinimum: below the minimum the right answer is
// to send fewer pixels, not fewer bits at the same resolution.
func TestTheGovernorDoesNotGoBelowTheMinimum(t *testing.T) {
	g := newBitrateGovernor(2500)
	kbps, _ := g.target(50, 0, t0)
	if kbps != bitrateFloorKbps {
		t.Errorf("target(50) = %d, want the minimum %d", kbps, bitrateFloorKbps)
	}
}

// TestTheGovernorsDeadBand: a variation nobody can see is not worth a command to
// the encoder.
func TestTheGovernorsDeadBand(t *testing.T) {
	g := newBitrateGovernor(2500)
	if kbps, changed := g.target(2400, 0, t0); changed {
		t.Errorf("an estimate 4%% lower moved the bitrate to %d", kbps)
	}
}

// The ratchet: following every dip of the estimate takes the bitrate to the
// minimum on a network that has nothing wrong with it.
//
// The numbers are the real ones, read from the log of a session on a mobile
// network with **losses at zero**, an RTT that did not follow the load and a
// quantiser between 19 and 23, that is with a perfect picture. The same link then
// held 2.4 Mbit/s. Coming down at every dip gives 950 → 700 → 600 in twelve
// seconds.
func TestADropIsNotBelievedWithoutLosses(t *testing.T) {
	const current = 1290
	for _, estimate := range []int{1108, 1077, 907, 875, 849, 812, 797} {
		if believableDrop(estimate, current, 0) {
			t.Errorf("with %d kbit/s estimated against %d, and zero losses, "+
				"the dip was believed", estimate, current)
		}
	}
}

// Losses bring immediate obedience back: they are the direct proof that we are
// sending more than gets through.
func TestADropIsBelievedIfPacketsAreLost(t *testing.T) {
	if !believableDrop(900, 1290, 0.03) {
		t.Error("with 3% of the packets lost the drop should have been believed")
	}
	// An isolated loss is not enough, though: it happens on any link without
	// meaning anything, and treating it as a refusal would bring the ratchet back.
	if believableDrop(900, 1290, 0.002) {
		t.Error("a 0.2% loss was treated as a refusal by the network")
	}
}

// A collapse is always believed, even without losses: it is the way out that
// guards against deep buffers, where the excess is absorbed without anything
// being lost and given back as half a second of delay. Measured on a cellular
// network: 300 kbit/s of estimate while we were sending 2500.
func TestACollapseIsBelievedEvenWithoutLosses(t *testing.T) {
	if !believableDrop(300, 2500, 0) {
		t.Error("a collapse to an eighth was not believed")
	}
	// And the threshold has to sit **between the two measured phenomena**: the
	// radio's swing reached half the current value, the real collapse an eighth.
	// Half is inside the observed noise and is not a collapse.
	if believableDrop(647, 1290, 0) {
		t.Error("half the current value is inside the observed swing, " +
			"it is not a collapse")
	}
}

// The climb waits, but when it starts it arrives.
//
// Recovering a quarter of the gap at a time, on a mobile network that caution
// cost **two minutes and forty** to reach the 2.4 Mbit/s the link held from the
// first second. On a baby monitor one watches for half a minute: the whole glance
// fell inside the ramp.
func TestTheGovernorClimbsAfterWaiting(t *testing.T) {
	g := newBitrateGovernor(2500)
	g.target(800, 0, t0) // descent

	// Straight after the drop it does not climb, however high the estimate has
	// come back: that is precisely the instant the network has to be left alone.
	if kbps, changed := g.target(2500, 0, t0.Add(time.Second)); changed {
		t.Errorf("immediate climb after the drop, to %d", kbps)
	}

	// Once the wait has passed it goes **where the estimate says**, not a little
	// at a time: gcc has already granted that number, and it has losses and
	// delays built into it.
	kbps, changed := g.target(2500, 0, t0.Add(bitrateRiseInterval+time.Second))
	if !changed || kbps != 2500 {
		t.Fatalf("first climb = %d, %v; want 2500, true", kbps, changed)
	}
}

// The wait is the only caution left on the way up, so it has to hold: an estimate
// high for an instant is not available bandwidth.
func TestTheGovernorDoesNotClimbOnALuckyInstant(t *testing.T) {
	g := newBitrateGovernor(2500)
	g.target(800, 0, t0)

	now := t0
	for i := range 4 {
		now = now.Add(time.Second)
		if kbps, changed := g.target(2500, 0, now); changed {
			t.Fatalf("climb after %d seconds of waiting, to %d", i+1, kbps)
		}
	}
}

// The cap stays the cap: the estimate may say what it likes.
func TestTheGovernorDoesNotExceedTheCap(t *testing.T) {
	g := newBitrateGovernor(2500)
	g.target(800, 0, t0)
	now := t0
	for range 20 {
		now = now.Add(bitrateRiseInterval + time.Second)
		g.target(9000, 0, now)
	}
	if g.current != 2500 {
		t.Errorf("after many climbs = %d, want the cap 2500", g.current)
	}
}

// TestTheGovernorStaysQuietAtTheCap: with the network holding, the governor has
// to keep quiet. A "change" on every turn would fill the log and call the encoder
// constantly to leave it where it is.
func TestTheGovernorStaysQuietAtTheCap(t *testing.T) {
	g := newBitrateGovernor(2500)
	for i := range 10 {
		if kbps, changed := g.target(9000, 0, t0.Add(time.Duration(i)*bitrateRiseInterval)); changed {
			t.Fatalf("turn %d: a change declared at %d with the network holding", i, kbps)
		}
	}
}

// TestTheGovernorsReleaseReturnsToThePreset: whoever arrives must not inherit
// the network of whoever has left.
func TestTheGovernorsReleaseReturnsToThePreset(t *testing.T) {
	g := newBitrateGovernor(2500)
	g.target(600, 0, t0)

	kbps, changed := g.release()
	if !changed || kbps != 2500 {
		t.Fatalf("release() = %d, %v; want 2500, true", kbps, changed)
	}
	if _, changed := g.release(); changed {
		t.Error("a repeated release() declared a change that is not there")
	}
}

// TestACapBelowTheMinimum: an absurd preset must not produce a bitrate below the
// technical minimum.
func TestACapBelowTheMinimum(t *testing.T) {
	g := newBitrateGovernor(100)
	if g.ceiling != bitrateFloorKbps || g.current != bitrateFloorKbps {
		t.Errorf("ceiling = %d, current = %d; want both %d",
			g.ceiling, g.current, bitrateFloorKbps)
	}
}

// TestAnEchoIsNotACollapse is the real sequence read on AMD with the saving
// switched on, on **localhost**: the quality loop was holding the bitrate at 300
// because the scene was easy, the estimate chased that number, and compared with
// the cap of 2500 it looked like "less than half", that is a collapse.
//
// The result was a resolution descent — 640x352 with the quantiser at 27, that is
// with an excellent picture — on a network that had nothing wrong with it. A
// collapse is measured against what we are sending: 268 estimated while we
// produce 315 is not a collapse, it is an estimate that resembles us.
func TestAnEchoIsNotACollapse(t *testing.T) {
	if believableDrop(268, 315, 0) {
		t.Error("an estimate of 268 with 315 produced was taken for a collapse: it is the echo of our own throughput")
	}
	// The same number, with the cap instead of the throughput, looked like a
	// collapse: it is the wrong comparison.
	if !believableDrop(268, 2500, 0) {
		t.Error("the test case no longer reproduces the defect")
	}
}

// And a real collapse is still caught: on a cellular network the estimate came
// down to 300 while we really were sending 2400, with a third of the packets
// lost.
func TestARealCollapseIsStillCaught(t *testing.T) {
	if !believableDrop(300, 2400, 0) {
		t.Error("300 estimated while we were sending 2400 was not believed")
	}
}

// Without knowing how much we are sending nothing is concluded: a zero means "I
// do not know", never "zero", and in that case the losses are left to speak.
func TestWithNoThroughputTheLossesDecide(t *testing.T) {
	if believableDrop(300, 0, 0) {
		t.Error("a collapse was believed without knowing how much we are sending")
	}
	if !believableDrop(300, 0, 0.05) {
		t.Error("with 5%% lost the drop has to be believed anyway")
	}
}
