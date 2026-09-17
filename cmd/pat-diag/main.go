// Diagnostic command: it says what this machine offers and how the monitor will
// use it.
//
// It is the first thing to run when something is wrong. If a user reports
// "stuttering video", "CPU at 100%" or "I cannot hear anything", the output of
// this command says at once whether there is a usable hardware encoder and
// whether the microphone really opens.
//
// It asks its questions the same way the monitor asks them: the encoders are
// asked of Media Foundation instead of trying to encode, and the microphone is
// opened instead of being looked up in a list. A device that appears in a list
// and then does not open is exactly the fault being looked for.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"patmonitor/internal/audio"
	"patmonitor/internal/devices"
	"patmonitor/internal/mf"
	"patmonitor/internal/wincom"
)

var (
	prefer      = flag.String("prefer", "", "narrow the choice to encoders whose name contains this text")
	width       = flag.Int("w", 1280, "test width")
	height      = flag.Int("h", 720, "test height")
	fps         = flag.Int("fps", 30, "test frame rate")
	bitrateKbps = flag.Int("b", 2500, "test bitrate in kbit/s")
)

func main() {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The microphone is tested first, because it lives on a thread of its own
	// and has nothing to do with Media Foundation.
	micLine := probeMicrophone(ctx)

	// **An `S_FALSE` from CoInitializeEx means success** — "COM was already
	// initialised here" — and treating it as a failure would stop the tool
	// before printing anything. What is lost is the camera's format list: when
	// the open fails with MF_E_INVALIDMEDIATYPE that is the only thing that
	// says what should have been asked for. A diagnostic tool that shuts down
	// on a success is the worst place to keep that mistake.
	//
	// `Required` because here the encoders are queried and the camera is opened
	// the way the monitor opens it: it is the same question as `runVideo`, so
	// it deserves the same answer. And if the apartment belonged to somebody
	// else, a report that says so is worth more than a listing produced in an
	// apartment that is not the right one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	release, err := wincom.Init(wincom.MTA, wincom.Required)
	defer release()
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}
	if err := mf.Startup(); err != nil {
		fmt.Printf("Media Foundation unavailable: %v\n", err)
		os.Exit(1)
	}
	defer mf.Shutdown()

	section("CAPTURE DEVICES")
	cams, err := devices.ListCameras()
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		os.Exit(1)
	}
	var usable []devices.Device
	for _, c := range cams {
		if c.IsLikelyIR() {
			continue
		}
		usable = append(usable, c)
	}
	fmt.Printf("  usable webcams: %d\n", len(usable))
	for _, c := range usable {
		fmt.Printf("  [video] %s\n", c.Name)
		// **Whole, and it used to be cut at 88.** This is the symbolic link, that
		// is, the value of camera_device_id, and this is the only place it can be
		// read: 94 characters on the development laptop, so the cut produced an
		// identifier that **looks complete and matches nothing** — the monitor
		// would refuse to start and list names, which for two cameras of the same
		// model is the same name twice. Truncating a name costs a glance;
		// truncating an identifier costs the choice it exists for.
		fmt.Printf("          %s\n", c.Link())
		printCameraFormats(c.Link())
	}
	// Infrared cameras are reported, so that it does not look as though a
	// device were missing: they exist on these laptops for face recognition and
	// give a useless picture as ordinary video.
	var ir []string
	for _, c := range cams {
		if c.IsLikelyIR() {
			ir = append(ir, c.Name)
		}
	}
	if len(ir) > 0 {
		fmt.Printf("\n  excluded (infrared cameras): %s\n", strings.Join(ir, ", "))
	}
	fmt.Printf("\n  [audio] %s\n", micLine)

	section("H.264 ENCODERS")
	list, err := mf.ListH264Encoders()
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  found: %d\n", len(list))
	for _, e := range list {
		kind := "synchronous"
		if e.Async {
			kind = "asynchronous"
		}
		fmt.Printf("    %s (%s)\n", e.Name, kind)
	}
	mf.ReleaseEncoders(list)

	// The Direct3D device is not an optimisation detail: without it no hardware
	// encoder works.
	var dev *mf.D3DDevice
	if d, derr := mf.NewD3DDevice(); derr != nil {
		fmt.Printf("\n  Direct3D 11: NOT available (%v)\n", derr)
		fmt.Println("  Without a Direct3D device only the software encoders remain usable.")
	} else {
		dev = d
		defer dev.Release()
		fmt.Println("\n  Direct3D 11: available")
		// Adapter and driver are not curiosities: they say which machine a
		// baseline was taken on, and a driver that has changed is the first
		// thing to suspect when a measurement stops matching the one before.
		if name, driver, aerr := dev.AdapterInfo(); aerr != nil {
			fmt.Printf("  adapter: cannot be queried (%v)\n", aerr)
		} else if driver == "" {
			fmt.Printf("  adapter: %s (driver version not declared)\n", name)
		} else {
			fmt.Printf("  adapter: %s, driver %s\n", name, driver)
		}
	}

	section("CONFIGURATION TEST")
	fmt.Printf("  requested: %dx%d@%d, %d kbit/s, baseline profile\n\n", *width, *height, *fps, *bitrateKbps)

	enc, err := mf.NewVideoEncoder(mf.VideoEncoderConfig{
		Width: *width, Height: *height, FPS: *fps,
		BitrateKbps: *bitrateKbps,
		Profile:     mf.H264ProfileBase,
		NameFilter:  *prefer,
		Device:      dev,
		GOPFrames:   *fps * 2,
	})
	if err != nil {
		fmt.Printf("  no configurable encoder:\n  %v\n", err)
		os.Exit(1)
	}
	defer enc.Close()

	fmt.Printf("  chosen encoder: %s\n", enc.Name)
	fmt.Print(enc.Diagnose())
	if enc.Async() {
		fmt.Printf("  subscription test  %s\n", enc.SelfTest())
	}

	section("RESOLUTION SCALING")
	probeScaling(usable, *width, *height, *fps)

	section("VERDICT")
	if enc.UsesDevice() {
		fmt.Println("  Encoding happens on the GPU.")
		fmt.Println()
		fmt.Println("  Note: Intel accounts for the encoder's work under Task Manager's")
		fmt.Println("  \"Video Decode\" node, because the media engine is a single one.")
		fmt.Println("  Seeing zero on \"Video Encode\" does not mean the GPU is idle.")
	} else {
		fmt.Println("  WARNING: encoding happens on the CPU.")
		fmt.Println("  On a monitor left on all night this shows: power, heat and fan.")
		fmt.Println("  Check the graphics adapter drivers.")
	}
	fmt.Println()
}

// probeScaling checks whether the Source Reader can deliver smaller frames
// while the camera stays open on its own format.
//
// It is the premise of the resolution scale, and it has to be established
// **before** building on it: if the reader does not scale, the only road is
// reopening the camera on another format, which interrupts the video and also
// changes what the motion detector sees. Those are two different designs, and
// this measurement is what decides between them.
//
// The verdict comes from **the frame's bytes**, not from what SetCurrentMediaType
// answers nor from what GetCurrentMediaType declares afterwards: a format that
// is accepted and not applied would say yes to both. An NV12 frame occupies
// width x height x 3/2, so the measurement is unambiguous.
func probeScaling(cams []devices.Device, w, h, fps int) {
	if len(cams) == 0 {
		fmt.Println("  no camera: cannot be checked")
		return
	}
	// **Without the Direct3D device, because that is how the monitor opens
	// it.** With a D3D manager the reader converts through DXVA, without one it
	// converts in software, and those are two different paths inside Media
	// Foundation. The monitor passes nil deliberately — it needs the frames in
	// system memory, because motion detection has to read their luminance plane
	// — so a tool that opens with D3D can declare green a machine on which the
	// monitor does not deliver a single frame.
	//
	// The rule is already written for the encoder: **the check has to be taken
	// as close as possible to whoever consumes the data.** It holds for whoever
	// writes the check too.
	reader, err := mf.OpenCamera(cams[0].Link(), w, h, fps, nil)
	if err != nil {
		fmt.Printf("  open failed: %v\n", err)
		return
	}
	defer reader.Release()

	// An enlargement **succeeds**, and said without a warning it reads as "the
	// camera can manage it". On a webcam that declares 640x480 as its maximum
	// this section would otherwise declare "1280x720 delivered".
	if mw, mh, mfps, clamped, e := mf.PickCameraSize(cams[0].Link(), w, h, fps); e == nil && clamped {
		fmt.Printf("  NOTE: the camera declares at most %dx%d@%d — every larger row below\n",
			mw, mh, mfps)
		fmt.Printf("        is the reader enlarging, not the camera.\n\n")
	}

	// The sizes are those of the scale: full, halfway, minimum. Among them
	// there is one **the camera does not offer**, and that is the one that
	// matters: if it arrives, the reader really is scaling. Asking only for
	// native sizes would not tell scaling from switching the camera's mode,
	// which is the other design — the one that interrupts the video at every
	// step.
	steps := []struct{ w, h int }{{w, h}, {960, 540}, {848, 480}, {640, 360}, {w, h}}
	for i, s := range steps {
		if i > 0 {
			if err := reader.SetOutputSize(s.w, s.h, fps); err != nil {
				fmt.Printf("  %4dx%-4d  refused: %v\n", s.w, s.h, err)
				continue
			}
		}
		got, err := frameBytes(reader)
		if err != nil {
			fmt.Printf("  %4dx%-4d  no frame: %v\n", s.w, s.h, err)
			continue
		}
		want := s.w * s.h * 3 / 2
		verdict := "delivered"
		if got != want {
			verdict = fmt.Sprintf("DELIVERS SOMETHING ELSE (%d bytes, i.e. %d pixels)", got, got*2/3)
		}
		fmt.Printf("  %4dx%-4d  %s\n", s.w, s.h, verdict)
	}
	fmt.Println()
	fmt.Println("  The last row repeats the starting size: if it comes back, the change")
	fmt.Println("  is reversible and the scaler can climb again when the bandwidth does.")
}

// frameBytes reads one frame and reports its size in bytes.
//
// After a format change the first attempts can come back empty: the Source
// Reader delivers (nil, nil) when it has nothing ready yet, and mistaking that
// for a fault would say the resize does not work when in fact nothing had
// arrived yet.
func frameBytes(r *mf.SourceReader) (int, error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _, err := r.ReadSample()
		if err != nil {
			return 0, err
		}
		if s == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		n := 0
		buf, err := s.Buffer()
		if err == nil {
			err = buf.WithBytes(func(b []byte) error { n = len(b); return nil })
			buf.Release()
		}
		s.Release()
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("no frame within 3s")
}

// probeMicrophone opens the default microphone and reports what it found.
//
// Opening it is the only way to really know: an endpoint can appear among the
// devices and then refuse, and with the laptop lid closed the microphone array
// disappears altogether while the camera stays.
func probeMicrophone(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	var line string
	err := audio.Capture(ctx,
		audio.Options{Raw: true, FallbackOnRawFailure: true},
		func(s audio.Stream) error {
			mode := "raw (OEM effects bypassed)"
			if !s.RawMode {
				mode = "NOT raw: the signal goes through the OEM effects and may be filtered"
			}
			line = fmt.Sprintf("%s\n          %s\n          %s\n          %s",
				s.DeviceName, s.Format.String(), mode, volumeLine(s.Volume))
			cancel() // knowing that it opens is enough
			return nil
		},
		func([]byte, bool) error { return nil },
	)
	if line == "" {
		if err == nil {
			err = fmt.Errorf("no format declared")
		}
		return fmt.Sprintf("NO MICROPHONE: %v", err)
	}
	return line
}

// volumeLine describes the gain the endpoint makes available.
//
// It varies a great deal from machine to machine — 30 dB on one, zero on
// another — and the distinction between hardware and software decides whether
// it is worth using: a gain applied before the converter improves the
// signal-to-noise ratio, a digital one raises everything and adds no
// information.
func volumeLine(v audio.VolumeInfo) string {
	if !v.Available {
		if v.Why != "" {
			return "volume: cannot be queried — " + v.Why
		}
		return "volume: no control exposed"
	}
	kind := "digital (applied by Windows)"
	if v.Hardware {
		kind = "HARDWARE (applied by the device)"
	}
	muted := ""
	if v.Muted {
		muted = ", MUTED"
	}
	return fmt.Sprintf("volume: %.1f dB on a scale from %.1f to %.1f — %s%s",
		v.CurrentDB, v.MinDB, v.MaxDB, kind, muted)
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Printf("  %s\n", title)
	fmt.Println(strings.Repeat("=", 78))
}

// printCameraFormats shows the modes the camera declares.
//
// It is not a detail for the curious: when the open fails with
// MF_E_INVALIDMEDIATYPE, this list is the only thing that says what should have
// been asked for. Without it, nothing is left but trying one combination at a
// time.
func printCameraFormats(link string) {
	formats, err := mf.CameraFormats(link)
	if err != nil {
		fmt.Printf("          formats cannot be listed: %v\n", err)
		return
	}
	if len(formats) == 0 {
		fmt.Printf("          declares no format\n")
		return
	}

	// Grouped by stream: when there is more than one, knowing which one carries
	// what is precisely the information needed.
	stream := -1
	for _, f := range formats {
		if f.Stream != stream {
			stream = f.Stream
			kind := "not video"
			if f.Video {
				kind = "video"
			}
			fmt.Printf("          stream %d (%s):\n", stream, kind)
		}
		fmt.Printf("            %2d) %s\n", f.Index, f)
	}
}
