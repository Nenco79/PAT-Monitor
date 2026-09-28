package rtc

import (
	"testing"
	"time"

	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/webrtc/v4"
)

// **The last viewer leaving takes the encoder back to the cap, whoever made the
// discount.** A discount from the quality loop sits inside a cap that stays at
// the preset, so the network's governor finds nothing to release: the encoder
// stayed at 300 with nobody watching, and the next viewer's governors all
// believed it was at the cap, which is the one position from which none of them
// raises anything.
//
// **The defect was put back and this test fails with it**: with the command
// judged on the network's governor alone, the release answers "unchanged".
func TestTheLastViewerLeavingTakesTheEncoderBackToTheCap(t *testing.T) {
	g := newBitrateGovernor(2500)
	quality := newQualityGovernor(30, 2500)
	quality.current = 300 // the still room's discount

	kbps, changed := releaseAll(g, quality, nil, 300)
	if !changed || kbps != 2500 {
		t.Errorf("released to %d kbit/s, changed=%v: the encoder stays at the discount", kbps, changed)
	}
	if quality.current != 2500 {
		t.Errorf("the quality loop still believes %d", quality.current)
	}

	// With the encoder already at the cap there is nothing to command.
	if _, changed := releaseAll(g, quality, nil, 2500); changed {
		t.Error("a release with the encoder at the cap commanded it again")
	}
}

// **One loss report is one cut.** The window holds a loss for three seconds
// against missed reports, and handing it to the governor on every turn made
// each tick a fresh multiplicative cut: one 20% burst followed by zeros took
// 2500 to 1600.
//
// **The defect was put back and this test fails with it**: with recentLoss
// handed to the governor, four ticks cut four times.
func TestOneLossReportIsOneCut(t *testing.T) {
	h := New(Config{BitrateKbps: 2500})
	g := newBitrateGovernor(2500)

	h.recordLoss(0.20, t0)
	cuts := 0
	kbps := 2500
	for i := range 4 {
		now := t0.Add(time.Duration(i) * time.Second)
		if i > 0 {
			h.recordLoss(0, now) // every later report says nothing was lost
		}
		var changed bool
		if kbps, changed = g.target(2500, h.lossToCut(now), now); changed {
			cuts++
		}
	}
	if cuts != 1 || kbps != 2250 {
		t.Errorf("one report of 20%% gave %d cuts, ending at %d kbit/s; want one, at 2250", cuts, kbps)
	}

	// A report that renews the loss is a new cut.
	now := t0.Add(4 * time.Second)
	h.recordLoss(0.20, now)
	if loss := h.lossToCut(now); loss != 0.20 {
		t.Errorf("a renewed loss was not handed over: %v", loss)
	}
}

// **Severe loss that goes on is cut on every report.** Tied to the window's
// worst being replaced, reports of 0.90, 0.89, 0.88 were cut once in three
// seconds. Put back and watched failing.
func TestSevereLossThatGoesOnIsCutOnEveryReport(t *testing.T) {
	h := New(Config{BitrateKbps: 2500})
	cuts := 0
	for i, f := range []float64{0.90, 0.89, 0.88, 0.89} {
		now := t0.Add(time.Duration(i) * time.Second)
		h.recordLoss(f, now)
		if h.lossToCut(now) >= bitrateLossSevere {
			cuts++
		}
	}
	if cuts != 4 {
		t.Errorf("four severe reports gave %d cuts, wanted four", cuts)
	}
}

// **A gap in the quantiser ages the scale's veto window.** The readings from
// before it used to stay and be averaged with the first ones after it, so a
// hard scene read as the healthy one before the gap.
//
// **The defect was put back and this test fails with it**: with a missing
// reading leaving the window alone, the first reading after the gap averages
// to 29.
func TestAGapInTheQuantiserAgesTheScalesWindow(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)
	lim := qpThresholds()
	now := t0
	tick := func(qp int) {
		now = now.Add(time.Second)
		g.target(2500, true, qp, lim, false, now)
	}
	for range scaleQPWindow {
		tick(27)
	}
	for range scaleQPWindow {
		tick(-1) // the camera stalls: no reading
	}
	tick(36)
	if veto := g.noteQP(0); veto != 36 {
		t.Errorf("after the gap the window reads %d, wanted the new scene's 36", veto)
	}
}

// onlyVideo answers for the video stream and not for the audio one, which is
// the shape of a teardown that unbinds the audio first.
type onlyVideo struct{ ssrc uint32 }

func (o onlyVideo) Get(ssrc uint32) *stats.Stats {
	if ssrc != o.ssrc {
		return nil
	}
	s := &stats.Stats{}
	s.OutboundRTPStreamStats.BytesSent = 5 << 20
	s.OutboundRTPStreamStats.PacketsSent = 31000
	return s
}

// **The audio counter is subtracted only across two readings of it.** Validity
// was decided by the video's, so a turn that read the video and not the audio
// subtracted a large audio total from zero, in uint64.
//
// **The defect was put back and this test fails with it**, with an audio
// bitrate of about 9.8e15 kbit/s.
func TestAMissingAudioReadingIsNotAReadingOfZero(t *testing.T) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	v := &Viewer{pc: pc, hub: New(Config{FPS: 30}), closed: make(chan struct{}),
		stats: onlyVideo{ssrc: 1}, videoSSRC: 1, audioSSRC: 2}
	now := time.Now()

	v.statsMu.Lock()
	v.lastSnap = sessionSnapshot{
		at: now.Add(-15 * time.Second), videoBytes: 4 << 20, audioBytes: 128 << 10,
		packetsSent: 30000, valid: true, audioValid: true,
	}
	v.statsMu.Unlock()

	rep := v.Report()
	if rep.AudioKbps < 0 || rep.AudioKbps > 10000 {
		t.Errorf("audio_kbps came back as %d: subtracted from a reading that was never made", rep.AudioKbps)
	}
	if rep.VideoKbps <= 0 {
		t.Errorf("the video, which was read, gave %d", rep.VideoKbps)
	}
}
