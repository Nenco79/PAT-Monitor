package main

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"patmonitor/internal/devices"
	"patmonitor/internal/mf"
)

// probeFraming reads the camera's automatic framing — the control behind the
// "Automatic framing" switch in Windows' camera settings — and, with off, turns
// it off and reads it again.
//
// **The witness is the state and the picture, not the window.** On an ACER HD
// User Facing the window the driver declares read 1.000, the whole view, while
// the picture was plainly a crop around a face; what told the truth was the
// driver saying it was following and, with -framing-shots, the luma plane
// saved before and after. So the window is printed, because it is what the
// driver says, and it is not what a verdict rests on. The state after the
// switch is read fifteen frames later, so that it is the driver's while it
// streams and not the echo of what was just written.
//
// The camera is the one the monitor opens with camera_device_id empty, or the
// one -cam names, opened as the monitor opens it by default — without
// pin_native_format, which is an experiment and off — and frames are read
// first: the control "only applies while the camera is actively streaming".
func probeFraming(want string, off bool, hold time.Duration, shots string) {
	section("AUTOMATIC FRAMING")
	cams, err := devices.ListCameras()
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		return
	}
	// A name is matched too, as pat-capture does: it is what a person reads in
	// the list, and the link is what the monitor keeps.
	link := want
	for _, c := range cams {
		if want != "" && c.Name == want {
			link = c.Link()
		}
	}
	cam, fellBack, err := devices.Pick(cams, link)
	if err != nil {
		fmt.Println("  no camera")
		return
	}
	if fellBack {
		fmt.Printf("  NOTE: %q is not connected; measuring the first usable camera instead.\n", want)
	}
	fmt.Printf("  camera: %s\n", cam.Name)
	reader, err := mf.OpenCamera(cam.Link(), *width, *height, *fps)
	if err != nil {
		fmt.Printf("  open failed: %v\n", err)
		return
	}
	defer reader.Release()
	if _, err := frameBytes(reader); err != nil {
		fmt.Printf("  no frame: %v\n", err)
		return
	}

	show := func(when string) {
		f, err := reader.Framing()
		if errors.Is(err, mf.ErrNoFraming) {
			fmt.Printf("  %s: %v\n", when, err)
			return
		}
		if err != nil {
			fmt.Printf("  %s: read failed: %v\n", when, err)
			return
		}
		fmt.Printf("  %s: can follow a face %v, following %v\n", when, f.CanFollow, f.Following)
		if f.PayloadErr != nil {
			fmt.Printf("  %s: payload not readable: %v\n", when, f.PayloadErr)
			return
		}
		fmt.Printf("  %s: window as declared, origin %.3f,%.3f size %.3f (not a witness)\n",
			when, f.OriginX, f.OriginY, f.Size)
		fmt.Printf("  %s: payload %d bytes % x\n", when, len(f.Payload), f.Payload)
	}
	frames := func(n int) bool {
		for range n {
			if _, err := frameBytes(reader); err != nil {
				fmt.Printf("  no frame: %v\n", err)
				return false
			}
		}
		return true
	}
	shot := func(name string) {
		if shots == "" {
			return
		}
		path := filepath.Join(shots, name)
		if err := saveLuma(reader, path); err != nil {
			fmt.Printf("  picture %s: %v\n", name, err)
			return
		}
		fmt.Printf("  picture: %s\n", path)
	}

	// Six seconds first, so that a framing that is on has had time to close in
	// on a face: read at the first frame, the picture had not moved yet.
	settle := time.Now().Add(6 * time.Second)
	for time.Now().Before(settle) {
		if !frames(1) {
			return
		}
	}
	show("as found")
	shot("framing-before.png")
	if !off {
		fmt.Println("\n  -framing-off turns it off for this session and reads it again.")
		return
	}
	if err := reader.StopFollowing(); err != nil {
		fmt.Printf("  turning it off: %v\n", err)
		return
	}
	if !frames(15) {
		return
	}
	show("after turning it off")
	shot("framing-after.png")
	if hold > 0 {
		fmt.Printf("\n  holding the camera for %v: look at the switch in Windows' settings now.\n", hold)
		deadline := time.Now().Add(hold)
		for time.Now().Before(deadline) {
			if !frames(1) {
				return
			}
		}
		show("at the end")
	}
}

// saveLuma writes the next frame's luma plane as a grey PNG. The reader
// delivers NV12, whose first width x height bytes are exactly that plane.
func saveLuma(r *mf.SourceReader, path string) error {
	w, h, _, _, _, err := r.CurrentFormat()
	if err != nil {
		return err
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	if err := withFrame(r, func(b []byte) error {
		if len(b) < w*h {
			return fmt.Errorf("frame of %d bytes for %dx%d", len(b), w, h)
		}
		copy(img.Pix, b[:w*h])
		return nil
	}); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	// A picture that did not reach the disk whole must not be printed as
	// evidence: the close is where a short write is reported.
	return f.Close()
}
