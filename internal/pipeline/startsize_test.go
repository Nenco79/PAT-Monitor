package pipeline

import (
	"log/slog"
	"strings"
	"testing"
)

// **The size the capture starts from is published, and announced only when it
// changes.**
//
// setStartSize runs at every camera open, that is, at every capture restart and
// every step of the backoff. A line written each time would be hundreds a night
// on a machine whose camera keeps failing, and it would hide the one occasion it
// meant something — the size really moving, which is what happens when the
// camera is changed.
func TestTheStartSizeIsAnnouncedOnlyWhenItChanges(t *testing.T) {
	var lines strings.Builder
	p := New(Config{
		Width: 1280, Height: 720, FPS: 30,
		Log: slog.New(slog.NewTextHandler(&lines, nil)),
	})
	smaller := func() int { return strings.Count(lines.String(), "fewer pixels") }

	// **Before the first open the answer is zeros and not the preset**: nobody
	// has asked the camera yet, and "I do not know" is not a size. Whoever
	// builds the resolution scale on it guards zero for exactly this moment.
	if w, h, f := p.StartSize(); w != 0 || h != 0 || f != 0 {
		t.Errorf("before the first open the start size is %dx%d@%d, and nobody "+
			"has asked the camera anything yet", w, h, f)
	}

	// A camera with fewer pixels than the preset: published, and said once.
	p.setStartSize(640, 480, 30)
	if w, h, f := p.StartSize(); w != 640 || h != 480 || f != 30 {
		t.Errorf("published %dx%d@%d instead of the camera's own size", w, h, f)
	}
	if got := smaller(); got != 1 {
		t.Errorf("the camera being smaller than the preset was written %d times, once expected", got)
	}

	// The same size again, which is a capture restart: nothing to say.
	p.setStartSize(640, 480, 30)
	if got := smaller(); got != 1 {
		t.Errorf("a restart at the same size wrote the line again: %d in all", got)
	}

	// And a camera that does have the preset's pixels is not reported as small.
	p.setStartSize(1280, 720, 30)
	if w, h, f := p.StartSize(); w != 1280 || h != 720 || f != 30 {
		t.Errorf("published %dx%d@%d after a camera with the preset's pixels", w, h, f)
	}
	if got := smaller(); got != 1 {
		t.Errorf("a camera at the preset's size was reported as smaller: %d lines", got)
	}
}
