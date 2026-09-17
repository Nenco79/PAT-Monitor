package rtc

import (
	"testing"
	"time"
)

// The cadence shown to the user has to **follow** the camera, not average the
// whole session.
//
// The defect, as reported by whoever was watching: "if it starts at 20 fps it
// never goes up again; if it starts at 24 or 27 then it varies". It was a
// cumulative average from the start of the capture, so dominated by what had
// happened earlier: half an hour in the dark buried the return of the light.
func TestTheMeasuredFPSFollowsTheCamera(t *testing.T) {
	h := New(Config{FPS: 30})
	start := time.Date(2026, 8, 1, 22, 0, 0, 0, time.UTC)
	now := start

	// Half an hour in the dark, 20 fps.
	slow := time.Second / 20
	for now.Sub(start) < 30*time.Minute {
		now = now.Add(slow)
		h.nextVideoDurationAt(now)
	}
	if fps := h.measuredFPSAt(now); fps < 19 || fps > 21 {
		t.Fatalf("in the dark it declares %.1f fps instead of ~20", fps)
	}

	// The light comes on: the camera goes back to 30. After a few seconds the
	// number shown has to say so, not in an hour's time.
	fast := time.Second / 30
	lit := now
	for now.Sub(lit) < 15*time.Second {
		now = now.Add(fast)
		h.nextVideoDurationAt(now)
	}
	if fps := h.measuredFPSAt(now); fps < 28 {
		t.Errorf("fifteen seconds after the light it still declares %.1f fps: "+
			"that is an average ageing, not a measurement", fps)
	}
}

// TestVideoClockTracksRealTime checks that the time declared in the RTP
// timestamps keeps pace with real time at various webcam cadences.
//
// It is the property that decides whether the delay stays constant or grows. If
// the media clock advances more slowly than real time, the browser sees the
// frames arriving ever earlier than declared and lengthens its playback buffer;
// if it advances faster, it discards them.
//
// The low cadences are not hypothetical: in the dark the automatic exposure
// lengthens the exposure time and the webcam drops well below the requested
// framerate.
func TestVideoClockTracksRealTime(t *testing.T) {
	const nominalFPS = 30

	cases := []struct {
		name   string
		actual time.Duration // the real interval between frames
	}{
		{"nominal cadence, 30 fps", time.Second / 30},
		{"dim light, 20 fps", time.Second / 20},
		{"dark, 10 fps", time.Second / 10},
		{"pitch dark, 5 fps", time.Second / 5},
		{"very slow webcam, 3 fps", time.Second / 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Config{FPS: nominalFPS})

			// A fixed instant: the test must depend neither on the system clock
			// nor on how fast the machine is.
			start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			now := start

			// Thirty seconds of filming at the cadence under test.
			frames := int(30 * time.Second / tc.actual)
			var media time.Duration
			for i := 0; i < frames; i++ {
				media += h.nextVideoDurationAt(now)
				now = now.Add(tc.actual)
			}

			real := now.Sub(start)
			drift := media - real
			if drift < 0 {
				drift = -drift
			}

			// A generous tolerance, but well below the threshold of perception:
			// what matters is that the drift does not grow with the duration.
			const tolerance = 250 * time.Millisecond
			if drift > tolerance {
				t.Errorf("after %v of filming at %.1f fps the media time is %v: a drift of %v (max %v)",
					real, float64(time.Second)/float64(tc.actual), media, media-real, tolerance)
			}
		})
	}
}

// TestVideoClockFollowsSlowdown checks the case that really matters for a baby
// monitor: not a webcam already slow at start-up, but one that slows down while
// somebody is watching, when the light fades and the automatic exposure
// lengthens the exposure time.
//
// The drift has to be measured after the transition, not during: a few frames of
// adjustment are inevitable and harmless, while a drift that stays is permanent
// delay.
func TestVideoClockFollowsSlowdown(t *testing.T) {
	h := New(Config{FPS: 30})

	start := time.Date(2026, 8, 1, 21, 0, 0, 0, time.UTC)
	now := start
	var media time.Duration

	advance := func(interval time.Duration, d time.Duration) {
		for end := now.Add(d); now.Before(end); now = now.Add(interval) {
			media += h.nextVideoDurationAt(now)
		}
	}

	advance(time.Second/30, 20*time.Second) // full light
	advance(time.Second/5, 10*time.Second)  // evening falls: the webcam adjusts

	// From here on the cadence is stable: the count is reset and the clock's
	// departure in the new regime is measured.
	mediaAtSteady, realAtSteady := media, now.Sub(start)
	advance(time.Second/5, 30*time.Second)

	drift := (media - mediaAtSteady) - (now.Sub(start) - realAtSteady)
	if drift < 0 {
		drift = -drift
	}
	const tolerance = 150 * time.Millisecond
	if drift > tolerance {
		t.Errorf("in the steady state after the slowdown the drift is %v over 30s (max %v)", drift, tolerance)
	}
}

// TestVideoClockResetsAfterGap checks that a long pause — the PC suspending, the
// webcam unplugged, the capture restarting — restarts the clock instead of
// leaving it chasing the lost time for minutes.
func TestVideoClockResetsAfterGap(t *testing.T) {
	h := New(Config{FPS: 30})

	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	now := start
	for i := 0; i < 60; i++ {
		h.nextVideoDurationAt(now)
		now = now.Add(time.Second / 30)
	}

	// A pause well beyond videoClockGapReset, then filming resumes.
	now = now.Add(30 * time.Second)
	beforeGap := h.videoMedia
	d := h.nextVideoDurationAt(now)

	if d != h.frameDuration {
		t.Errorf("after the pause the first frame declares %v, want the nominal %v", d, h.frameDuration)
	}
	// The clock restarts from zero instead of inheriting the 30 seconds of void.
	if h.videoMedia >= beforeGap {
		t.Errorf("the media clock did not restart: it was %v, now %v", beforeGap, h.videoMedia)
	}
}
