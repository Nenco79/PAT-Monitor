package pipeline

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"patmonitor/internal/mf"
)

// fakeFramer is a camera whose driver starts following at a given frame,
// as measured, and stops when it is told to.
type fakeFramer struct {
	frame      int // the frame the check is at, advanced by the test
	followFrom int // the driver says it follows from this frame on
	stopped    bool
	stops      int
}

func (f *fakeFramer) Framing() (mf.Framing, error) {
	return mf.Framing{CanFollow: true, Following: !f.stopped && f.frame >= f.followFrom}, nil
}

func (f *fakeFramer) StopFollowing() error {
	f.stopped = true
	f.stops++
	return nil
}

// **The first frame is not the answer.** Measured over five opens of an ACER
// HD User Facing: the first open after a rest said "not following" at frame 1
// and "following" from frame 5, the other four said "following" at frame 1. A
// check made once at the first frame left the framing on for the whole
// session, with nothing in the log — which is what a second start of the
// monitor showed. The check keeps looking, and turns it off when it comes on.
func TestTheFramingIsTurnedOffWhenItComesOnAfterTheFirstFrame(t *testing.T) {
	for _, from := range []int{1, 5} {
		var log bytes.Buffer
		p := New(Config{Log: slog.New(slog.NewTextHandler(&log, nil))})
		cam := &fakeFramer{followFrom: from}
		var c framingCheck
		for cam.frame = 1; cam.frame <= 120; cam.frame++ {
			c.step(p, cam)
		}
		if cam.stops != 1 {
			t.Errorf("following from frame %d: turned off %d times, want once", from, cam.stops)
		}
		if !strings.Contains(log.String(), "automatic framing is off for the monitor") {
			t.Errorf("following from frame %d: the log does not say it was turned off:\n%s", from, log.String())
		}
	}
}

// **A driver that turns it back on is turned off again, and says so.**
func TestTheFramingIsTurnedOffAgainIfTheCameraTurnsItBackOn(t *testing.T) {
	var log bytes.Buffer
	p := New(Config{Log: slog.New(slog.NewTextHandler(&log, nil))})
	cam := &fakeFramer{followFrom: 1}
	var c framingCheck
	for cam.frame = 1; cam.frame <= 2000; cam.frame++ {
		if cam.frame == 1000 {
			cam.stopped = false // the driver turns it back on
		}
		c.step(p, cam)
	}
	if cam.stops != 2 {
		t.Errorf("turned off %d times, want twice", cam.stops)
	}
	if !strings.Contains(log.String(), "turned its automatic framing back on") {
		t.Errorf("the log does not say the camera turned it back on:\n%s", log.String())
	}
}
