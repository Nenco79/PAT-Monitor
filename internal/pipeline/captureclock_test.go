package pipeline

import (
	"testing"
	"time"
)

// **A frame is dated by its own timestamp, and only when that can be the
// capture instant.** The ages are the ones measured on the AMD machine: a
// frame leaves the encoder 41-121 ms after the camera stamped it.
func TestAFrameIsDatedWhenTheCameraSawIt(t *testing.T) {
	now := time.Unix(5000, 0)
	clock := 3 * time.Hour // the machine's uptime, the clock MF reads

	cases := []struct {
		name      string
		pts       time.Duration
		ok        bool
		want      time.Time
		fromFrame bool
	}{
		{"an ordinary frame", clock - 80*time.Millisecond, true, now.Add(-80 * time.Millisecond), true},
		{"the slowest measured", clock - 121*time.Millisecond, true, now.Add(-121 * time.Millisecond), true},
		{"just captured", clock, true, now, true},
		{"no timestamp", 0, false, now, false},
		// A camera counting from its own start: the age is the uptime.
		{"a clock of its own", 2 * time.Second, true, now, false},
		{"from the future", clock + time.Millisecond, true, now, false},
		{"just past the limit", clock - maxCaptureAge - time.Millisecond, true, now, false},
	}
	for _, c := range cases {
		got, fromFrame := capturedAt(c.pts, c.ok, clock, now)
		if !got.Equal(c.want) || fromFrame != c.fromFrame {
			t.Errorf("%s: dated %v before now (from the frame: %v), want %v (%v)",
				c.name, now.Sub(got), fromFrame, now.Sub(c.want), c.fromFrame)
		}
	}
}

// **A packet begins as many samples before the block's arrival as are still
// queued, its own included.** Two packets out of one block are 20 ms apart on
// the timeline even though both are encoded in the same instant, and a
// remainder carried from the block before makes the first one older still.
func TestAPacketIsDatedByItsFirstSample(t *testing.T) {
	arrived := time.Unix(5000, 0)
	const rate = 48000

	// 50 ms queued: a 10 ms remainder and a 40 ms block, two packets and 10 ms
	// left for the next.
	first := packetStart(arrived, 2400, rate)
	second := packetStart(arrived, 2400-960, rate)
	if got := arrived.Sub(first); got != 50*time.Millisecond {
		t.Errorf("first packet dated %v before arrival, want 50ms", got)
	}
	if got := second.Sub(first); got != 20*time.Millisecond {
		t.Errorf("the two packets are %v apart, want one Opus frame", got)
	}
}
