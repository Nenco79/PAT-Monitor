package rtc

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// **A camera that has stopped is not a camera delivering what it delivered a
// minute ago.**
//
// The window shown to the user is recomputed only when a frame arrives, so with
// the frames stopped it went on answering the last closed window for ever. A
// capture restart lasts up to half a minute, and for all of it the status page
// declared the cadence of before — and the cadence governor was handed the same
// stale number as its measurement.
//
// It is the defect the comment above `MeasuredFPS` already names, in the
// direction nobody had looked at: that one was a cumulative average that would
// not go **up** after the light returned, this is a recent window that will not
// come **down** when the camera stops. The second is the worse of the two — it
// hides a fault instead of inventing one.
//
// **The defect was put back and this test fails with it**, reporting 30.0 fps
// for a camera that had delivered nothing for a minute.
func TestTheCadenceDoesNotSurviveTheCameraStopping(t *testing.T) {
	h := New(Config{FPS: 30})
	start := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
	now := start

	// Twenty seconds at thirty frames: enough to close several windows.
	step := time.Second / 30
	for now.Sub(start) < 20*time.Second {
		now = now.Add(step)
		h.nextVideoDurationAt(now)
	}
	if fps := h.measuredFPSAt(now); fps < 28 || fps > 32 {
		t.Fatalf("with the camera running it declares %.1f fps instead of ~30", fps)
	}

	// The camera stops. Nothing arrives, so nothing closes a window — which is
	// the whole of the defect.
	for _, after := range []struct {
		silence time.Duration
		most    float64
	}{
		{10 * time.Second, 15},
		{30 * time.Second, 5},
		{2 * time.Minute, 1.5},
	} {
		at := now.Add(after.silence)
		if fps := h.measuredFPSAt(at); fps > after.most {
			t.Errorf("%s after the last frame it still declares %.1f fps, and a "+
				"number that ages is worse than an absent one", after.silence, fps)
		}
	}

	// And when the frames come back the number climbs again: the decay must not
	// be a way of switching the measurement off.
	back := now.Add(3 * time.Second)
	for i := 0; i < 300; i++ {
		back = back.Add(step)
		h.nextVideoDurationAt(back)
	}
	if fps := h.measuredFPSAt(back); fps < 25 {
		t.Errorf("ten seconds after the camera came back it declares %.1f fps", fps)
	}
}

// **A missing statistics sample is not a sample of zero.**
//
// The snapshot was marked valid before anything had been asked of the
// interceptor, so a turn where it answers nothing — it drops a stream when it is
// unbound, which is what happens while the session is being torn down — stored
// zeros that looked like a measurement. The next difference subtracted a large
// total from zero in `uint64`, which does not go negative: it wraps.
//
// And the number does not stay in a debug line. The watcher's loop keeps the
// last report and hands it to the session's closing line, which this package
// calls the only report there is for the test that counts.
//
// **The defect was put back and this test fails with it**, with a video bitrate
// of about 9.8e15 kbit/s.
func TestAMissingStatsSampleIsNotASampleOfZero(t *testing.T) {
	// A real PeerConnection, closed: that is the shape of the moment this is
	// about — the session is being torn down, the statistics interceptor has
	// unbound the stream, and the watcher's ticker fires once more.
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	v := &Viewer{pc: pc, hub: New(Config{FPS: 30}), closed: make(chan struct{})}
	now := time.Now()

	// A real reading, as the interceptor would have given it.
	v.statsMu.Lock()
	v.lastSnap = sessionSnapshot{
		at: now.Add(-15 * time.Second), videoBytes: 4 << 20, audioBytes: 128 << 10,
		packetsSent: 30000, valid: true,
	}
	v.statsMu.Unlock()

	// v.stats is nil, which is the shape of "the statistics answered nothing".
	rep := v.Report()
	if rep.VideoKbps < 0 || rep.VideoKbps > 100000 {
		t.Errorf("video_kbps came back as %d: the counters were subtracted from a "+
			"snapshot that was never read", rep.VideoKbps)
	}
	if rep.AudioKbps < 0 || rep.AudioKbps > 10000 {
		t.Errorf("audio_kbps came back as %d", rep.AudioKbps)
	}

	// And the snapshot it left behind must not be usable as a baseline either,
	// or the wrap simply moves to the next report.
	v.statsMu.Lock()
	stored := v.lastSnap
	v.statsMu.Unlock()
	if stored.valid {
		t.Error("a snapshot nobody could read was stored as valid: the next " +
			"report subtracts from it")
	}
}
