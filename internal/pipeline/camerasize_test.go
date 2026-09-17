package pipeline

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// answers builds a pickSize that returns a fixed answer and counts the calls:
// **whether it is asked at all is half of what these tests assert.**
func answers(w, h, fps int, clamped bool, err error, calls *int) pickSize {
	return func(int, int, int) (int, int, int, bool, error) {
		*calls++
		return w, h, fps, clamped, err
	}
}

func quietLog(into *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(into, nil))
}

// The monitor's direction: a camera with fewer pixels than the preset gets asked
// for its own size, because with the advanced video processing on a larger
// request does not fail — it is upscaled in software, in silence.
func TestTheRequestIsLoweredToWhatTheCameraDeclares(t *testing.T) {
	var lines strings.Builder
	calls := 0

	w, h, fps := cameraSize(1280, 720, 30, false,
		answers(640, 480, 30, true, nil, &calls), quietLog(&lines))

	if w != 640 || h != 480 || fps != 30 {
		t.Errorf("asked the camera for %dx%d@%d instead of what it declares", w, h, fps)
	}
	if calls != 1 {
		t.Errorf("the camera was interrogated %d times, once expected", calls)
	}
}

// **The instrument's direction, and it is the one that was broken.**
// pat-capture prints the requested size, declares that what follows measures an
// upscale, and then measures it: lowered underneath, the report would say it is
// measuring an enlargement while measuring the camera.
//
// The camera must not even be interrogated — not for cost, but because a
// question whose answer is thrown away is a question somebody will one day wire
// to something.
func TestAMeasuringInstrumentKeepsItsOwnNumbers(t *testing.T) {
	var lines strings.Builder
	calls := 0

	w, h, fps := cameraSize(1280, 720, 30, true,
		answers(640, 480, 30, true, nil, &calls), quietLog(&lines))

	if w != 1280 || h != 720 || fps != 30 {
		t.Errorf("the instrument was given %dx%d@%d instead of the numbers it asked for", w, h, fps)
	}
	if calls != 0 {
		t.Errorf("the camera was interrogated %d times for an answer that is discarded", calls)
	}
}

// A camera that does have the preset's pixels keeps the preset: there the reader
// **shrinks**, which is the good direction and is what the resolution scale
// needs anyway.
func TestACameraWithEnoughPixelsKeepsThePreset(t *testing.T) {
	var lines strings.Builder
	calls := 0

	w, h, fps := cameraSize(1280, 720, 30, false,
		answers(1280, 720, 30, false, nil, &calls), quietLog(&lines))

	if w != 1280 || h != 720 || fps != 30 {
		t.Errorf("lowered to %dx%d@%d a camera that has the pixels", w, h, fps)
	}
}

// **A failure to enumerate stops nothing, and says so.** Worse than opening a
// camera sub-optimally is only not opening it — and the line is the whole
// remedy, because the fallback is silent by construction: the size simply comes
// back equal to the one asked for, which is how this function once did nothing
// at all for months.
func TestAFailureToEnumerateAsksForThePresetAndDeclaresIt(t *testing.T) {
	var lines strings.Builder
	calls := 0

	w, h, fps := cameraSize(1280, 720, 30, false,
		answers(0, 0, 0, false, errors.New("no"), &calls), quietLog(&lines))

	if w != 1280 || h != 720 || fps != 30 {
		t.Errorf("an enumeration failure changed the size to %dx%d@%d", w, h, fps)
	}
	if !strings.Contains(lines.String(), "cannot read the formats") {
		t.Errorf("the fallback was silent:\n%s", lines.String())
	}
}
