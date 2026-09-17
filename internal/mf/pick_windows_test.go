//go:build windows

package mf

import "testing"

// fmts builds a list like the one a camera declares: every size in several
// subtypes and several cadences, which is the real shape and not a simplified
// one.
func fmts(stream int, sizes [][2]int, rates []int) []CameraFormat {
	var out []CameraFormat
	for _, sub := range []string{"NV12", "MJPG", "I420"} {
		for _, s := range sizes {
			for _, r := range rates {
				out = append(out, CameraFormat{
					Stream: stream, Subtype: sub, Video: true,
					Width: s[0], Height: s[1], FPSNum: r, FPSDen: 1,
				})
			}
		}
	}
	return out
}

// The list is the real one from a Logitech C210, taken with pat-diag on the
// machine where the monitor never delivered a frame. The maximum it declares is
// 640x480, and the monitor was asking it for 1280x720.
func c210() []CameraFormat {
	sizes := [][2]int{{640, 480}, {160, 120}, {176, 144}, {320, 176},
		{320, 240}, {352, 288}, {432, 240}, {544, 288}, {640, 360}}
	f := fmts(0, sizes, []int{30, 5, 10, 15, 20, 25})
	// Stream 1 is the still-image pin, at one frame per second.
	return append(f, fmts(1, sizes, []int{1})...)
}

func TestTheCameraIsNeverAskedForMorePixelsThanItHas(t *testing.T) {
	w, h, f, changed := chooseCameraSize(c210(), 1280, 720, 30)
	if !changed {
		t.Fatal("the size was not lowered")
	}
	if w != 640 || h != 480 || f != 30 {
		t.Errorf("chose %dx%d@%d, want 640x480@30", w, h, f)
	}
}

// The resolution scale needs to be able to **shrink**, so a camera that has the
// pixels keeps them: the preset stays the cap and the reader scales downwards,
// which is the direction that works.
func TestACameraThatHasThePixelsKeepsThePreset(t *testing.T) {
	full := fmts(0, [][2]int{{1920, 1080}, {1280, 720}, {640, 480}}, []int{30})
	w, h, f, changed := chooseCameraSize(full, 1280, 720, 30)
	if changed || w != 1280 || h != 720 || f != 30 {
		t.Errorf("the preset was touched: %dx%d@%d changed=%v", w, h, f, changed)
	}
}

// The criterion is the number of pixels, not the two dimensions separately:
// 1280x1024 is larger than 1280x720 even though it does not contain it, and
// shrinking it is what the reader knows how to do. Looking at the height would
// fall back on a far smaller size for a format that has pixels to spare.
func TestATallerFormatStillCountsAsEnoughPixels(t *testing.T) {
	odd := fmts(0, [][2]int{{1280, 1024}, {320, 240}}, []int{30})
	w, h, _, changed := chooseCameraSize(odd, 1280, 720, 30)
	if changed || w != 1280 || h != 720 {
		t.Errorf("chose %dx%d changed=%v, want the whole preset", w, h, changed)
	}
}

// The stream to look at is the one the reader would read — the **first** video
// one, which is not always zero. A webcam with Windows Hello also exposes an
// infrared sensor, and choosing the size from one list only to open another
// would be worse than not choosing.
func TestTheSizeComesFromTheStreamTheReaderWillOpen(t *testing.T) {
	var l []CameraFormat
	// Stream 0 is not video: a metadata stream, which does exist.
	l = append(l, CameraFormat{Stream: 0, Video: false, Width: 9999, Height: 9999})
	l = append(l, fmts(2, [][2]int{{640, 480}}, []int{30})...)
	l = append(l, fmts(3, [][2]int{{1920, 1080}}, []int{30})...)
	w, h, _, changed := chooseCameraSize(l, 1280, 720, 30)
	if !changed || w != 640 || h != 480 {
		t.Errorf("chose %dx%d changed=%v, want 640x480 from stream 2", w, h, changed)
	}
}

// The cadence is chosen below the one wanted: declaring less than the real one
// to the encoder overshoots the cap, which is the asymmetry already written
// down.
func TestTheFrameRateStaysUnderTheOneAskedFor(t *testing.T) {
	slow := fmts(0, [][2]int{{640, 480}}, []int{5, 10, 15, 20})
	_, _, f, _ := chooseCameraSize(slow, 1280, 720, 30)
	if f != 20 {
		t.Errorf("cadence %d, want 20", f)
	}

	// And if it offers none below, the lowest it has is taken: there is no
	// alternative, and knowing it is better than not opening.
	fast := fmts(0, [][2]int{{640, 480}}, []int{50, 60})
	_, _, f, _ = chooseCameraSize(fast, 1280, 720, 15)
	if f != 50 {
		t.Errorf("cadence %d, want 50", f)
	}
}

// An empty enumeration, or one with no video in it, must not be able to stop
// the camera opening: what was wanted is asked for.
func TestWithNothingDeclaredThePresetSurvives(t *testing.T) {
	for _, l := range [][]CameraFormat{nil, {{Stream: 0, Video: false}}} {
		w, h, f, changed := chooseCameraSize(l, 1280, 720, 30)
		if changed || w != 1280 || h != 720 || f != 30 {
			t.Errorf("%dx%d@%d changed=%v on an unusable list", w, h, f, changed)
		}
	}
}
