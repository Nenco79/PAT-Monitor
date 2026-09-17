//go:build windows

package mf

import (
	"patmonitor/internal/wincom"
)

// Choosing the capture size: **the camera is never asked for more pixels than
// it declares.**
//
// The preset stays a cap, and this is the only thing that can lower it before
// the monitor starts. It is not a negotiation: it is removing the one direction
// in which asking can only cost.
//
// With MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING switched on the reader
// **does not pick the nearest** format: it takes the one it is asked for and
// puts a scaler in between, so a 640x480 camera can be asked for 720p and will
// deliver it, enlarged. And rereading the format is no witness: it repeats the
// size it was handed.
//
// The result is a software enlargement of 2.25 times the pixels, encoded at
// full bandwidth, adding not one detail. Measured on a Logitech C210, which
// declares **640x480 as its maximum** and no format beyond it, with the monitor
// opening 1280x720 and never delivering a frame.

// PickCameraSize reports the size to ask the camera for.
//
// It returns the wanted one if the camera has at least one format as large —
// there the reader **shrinks**, which is the good direction and is needed by
// the resolution scale anyway — and otherwise the largest format it declares.
//
// A failure in the enumeration is not fatal: it falls back on the wanted size.
// Worse than opening a camera suboptimally there is only not opening it.
func PickCameraSize(link string, width, height, fps int) (w, h, f int, changed bool, err error) {
	var formats []CameraFormat
	if err = onMFThread(func() error {
		var e error
		formats, e = CameraFormats(link)
		return e
	}); err != nil {
		return width, height, fps, false, err
	}
	w, h, f, changed = chooseCameraSize(formats, width, height, fps)
	return w, h, f, changed, nil
}

// onMFThread runs fn on a thread of its own, with COM and Media Foundation
// started.
//
// **It is needed because the diagnostic tools ask from a thread with no COM.**
// pat-diag and pat-capture ask in main, at start-up: without a thread of its
// own here, ActivateObject would answer CO_E_NOTINITIALIZED (0x800401F0) and
// the choice would fall back on the preset every time — and the fallback holds,
// so nothing would fail. That is exactly how a new function can end up doing
// nothing in silence, and it is how this one did until the thread was added.
//
// **The monitor no longer asks from there.** It used to, in main and once, and
// this comment said so; it now asks at every camera open, from inside the
// capture goroutine, where COM is already up — see runVideo. A thread of its
// own costs nothing there, and it is what lets one function serve both.
//
// The apartment is MTA because that is the one Media Foundation runs in — the
// choice belongs here, and reading the outcome of CoInitializeEx lives in
// wincom.
//
// Accepted and not Required because what happens here is **enumeration**: no
// asynchronous transform, so no event queue that in STA would be left without a
// message loop. It is the same policy as before, and what separates this from
// runVideo is not the apartment but what runs on top of it.
func onMFThread(fn func() error) error {
	return wincom.Thread(wincom.MTA, wincom.Accepted, func() error {
		// MFStartup and MFShutdown are counted, so starting it here does not
		// disturb a pipeline that had already started it.
		if err := Startup(); err != nil {
			return err
		}
		defer Shutdown()

		return fn()
	})
}

// chooseCameraSize is the rule, kept apart from the enumeration because it is
// the half that can be tested without a camera plugged in.
func chooseCameraSize(formats []CameraFormat, width, height, fps int) (w, h, f int, changed bool) {
	stream, ok := firstVideoStream(formats)
	if !ok {
		return width, height, fps, false
	}

	// The criterion is the number of pixels and not the two dimensions
	// separately: a camera declaring 1280x1024 against a 1280x720 preset has
	// them all, and shrinking it is exactly what the reader knows how to do.
	// Looking at the dimensions one at a time would rule it out on height and
	// send us back to something a great deal smaller.
	wanted := width * height
	var max CameraFormat
	for _, c := range formats {
		if c.Stream != stream || !c.Video || c.Width <= 0 || c.Height <= 0 {
			continue
		}
		if c.Width*c.Height >= wanted {
			return width, height, fps, false
		}
		if c.Width*c.Height > max.Width*max.Height {
			max = c
		}
	}
	if max.Width == 0 {
		return width, height, fps, false
	}
	return max.Width, max.Height, cameraFPS(formats, stream, max.Width, max.Height, fps), true
}

// firstVideoStream finds the stream the reader would read.
//
// It is MF_SOURCE_READER_FIRST_VIDEO_STREAM, that is, the **first** video
// stream, and not necessarily stream 0: a webcam with Windows Hello also
// exposes an infrared sensor, and cameras that do have more than one stream.
// Applying a different rule here from the reader's would mean choosing the size
// from one list and opening another.
func firstVideoStream(formats []CameraFormat) (int, bool) {
	first, found := 0, false
	for _, c := range formats {
		if !c.Video {
			continue
		}
		if !found || c.Stream < first {
			first, found = c.Stream, true
		}
	}
	return first, found
}

// cameraFPS picks the cadence at that size.
//
// The highest of those declared that **does not exceed** the wanted one: the
// cadence declared to the encoder is a governor of its own, and receiving more
// than expected is the direction that overshoots the cap. If the camera offers
// none below, the lowest it has is taken — there is no alternative, and knowing
// it is higher is better than not opening.
func cameraFPS(formats []CameraFormat, stream, w, h, want int) int {
	best, lowest := 0, 0
	for _, c := range formats {
		if c.Stream != stream || !c.Video || c.Width != w || c.Height != h {
			continue
		}
		if c.FPSDen <= 0 || c.FPSNum <= 0 {
			continue
		}
		n := (c.FPSNum + c.FPSDen/2) / c.FPSDen
		if n <= 0 {
			continue
		}
		if lowest == 0 || n < lowest {
			lowest = n
		}
		if n <= want && n > best {
			best = n
		}
	}
	switch {
	case best > 0:
		return best
	case lowest > 0:
		return lowest
	default:
		return want
	}
}
