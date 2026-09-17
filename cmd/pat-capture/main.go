// End-to-end test of the capture pipeline: it opens the webcam and the
// microphone, encodes video and audio, and checks that the four streams arrive
// with sensible data.
//
// No browser is needed: this command isolates the pipeline from the WebRTC
// transport, so a capture problem does not get confused with a network problem.
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"time"

	"patmonitor/internal/detect"
	"patmonitor/internal/devices"
	"patmonitor/internal/diag"
	"patmonitor/internal/encoder"
	"patmonitor/internal/media"
	"patmonitor/internal/mf"
	"patmonitor/internal/pipeline"
)

var (
	duration  = flag.Duration("d", 15*time.Second, "test duration")
	width     = flag.Int("w", 1280, "width")
	height    = flag.Int("h", 720, "height")
	fps       = flag.Int("fps", 30, "framerate")
	bitrate   = flag.Int("br", 2500, "video bitrate in kbit/s")
	bitrate2  = flag.Int("br2", 0, "halfway through, ask for this bitrate and measure whether the command bites")
	brMode    = flag.String("brmode", "command", "how to change the bitrate: command (SetValue) or reconfigure")
	height2   = flag.Int("h2", 0, "halfway through, drop to this height and measure whether the pixels change")
	width2    = flag.Int("w2", 0, "the width to go with -h2 (default: 16:9 of it, rounded to macroblocks)")
	fps2      = flag.Int("fps2", 0, "halfway through, drop to this frame rate and measure whether the frames really fall")
	rateCtl   = flag.String("rc", "cbr", "how the bits are spent: cbr, quality, capped")
	qLevel    = flag.Int("q", 0, "quality level 1-100 for -rc quality (0 = default)")
	minQP     = flag.Int("minqp", 0, "quantiser floor 1-51 for -rc capped (0 = default)")
	maxQP     = flag.Int("maxqp", 0, "quantiser ceiling 1-51: how far the encoder may degrade to stay in the bitrate (0 = free)")
	micGain   = flag.Float64("gain", 0, "microphone gain in dB")
	preferEnc = flag.String("prefer", "", "force an encoder (e.g. h264_qsv)")
	pinNative = flag.Bool("pin", false, "pin the camera's native format, so a size change must go through a converter")
	keepFRC   = flag.Bool("frc", false, "leave the video processor free to invent frames, as Media Foundation does by default")
	verbose   = flag.Bool("v", false, "debug logging")
	fmp4Out   = flag.String("fmp4", "", "write a fragmented MP4 to this file, to exercise the muxer")
	testTone  = flag.Bool("tone", false, "use a test tone instead of the microphone")
	camWanted = flag.String("cam", "", "measure on the camera whose name or link contains this text")
	pliEvery  = flag.Duration("pli", 0, "ask for a keyframe this often and measure how long it takes to arrive")
)

// containsFold is a case-insensitive substring match, because Windows hands the
// same symbolic link back in different cases depending on who is asked — which
// is the reason devices.Pick compares that way too.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// pliQuietBeforeEnd is how long before the end the keyframe requests stop.
//
// Two seconds is more than a GOP, so the last request still has room to be
// answered and counted, and it leaves the teardown to itself.
const pliQuietBeforeEnd = 2 * time.Second

// pliWindowFrames is how long a keyframe request has to be answered in.
//
// It is the pipeline's own window, and it must stay the same number: a tool that
// judges by a different rule from the thing it is checking answers a question
// nobody asked.
const pliWindowFrames = 5

// bitrateTookHold says whether the throughput followed a bitrate command.
//
// **The question is "did it follow?", not "did it land on the number?"**, and
// the two differ exactly where it matters. The first version measured the
// distance from the target — `|after - asked| < |before - asked| / 2` — which
// fails an encoder that **overshot**, that is, one that obeyed more than it was
// asked to. Measured on an NVIDIA RTX 4080 with a camera delivering half its
// nominal cadence: asked 800, produced 1113 before and 444 after, and the tool
// printed `congestion control on this machine is a fiction` about an encoder
// that had just cut its throughput by 60%. A wrong measurement always accuses
// somebody else.
//
// It is the rule this project already wrote for the watchdog inside the
// product — the signature is "does not follow", not "sits above" — met again
// in the instrument that was supposed to check it. The comment above the old
// line stated the right rule while the line implemented another one.
//
// **And a request above what is already coming out is not a verdict**, which is
// the same guard as probeVerdict: if the encoder was producing less than the new
// request, nothing was taken away and the throughput is still decided by the
// scene.
func bitrateTookHold(before, after, asked float64) (held, judgeable bool) {
	gap := before - asked
	if gap <= 0 {
		return false, false
	}
	return before-after >= gap/2, true
}

func main() {
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// --- device selection ---
	all, err := devices.ListCameras()
	if err != nil {
		fatal("%v", err)
	}
	if len(all) == 0 {
		fatal("no camera in the system")
	}
	var cam devices.Device

	// **Which camera is measured is a choice, on a machine that has several.**
	// Without it the instrument takes whatever comes first, and on a machine
	// where the monitor is running that is the one it is holding. Two of the
	// same model make the name useless, so the text is matched against the link
	// as well.
	//
	// **This search is over the unfiltered list, and the default below is not**,
	// which is the one asymmetry here: filtering is what the monitor does for
	// whoever did not choose, and here somebody has. Pointing this tool at an
	// infrared sensor on purpose is a measurement; being handed one without
	// asking is the defect the other branch closes.
	if *camWanted != "" {
		found := false
		for _, c := range all {
			if containsFold(c.Name, *camWanted) || containsFold(c.Link(), *camWanted) {
				cam, found = c, true
				break
			}
		}
		if !found {
			fatal("no camera matches %q: %s", *camWanted, devices.Names(all))
		}
	} else {
		// **With nothing asked for, the instrument opens the camera the monitor
		// would open**, and the first of the enumeration is not that. The comment
		// that used to stand here called it "the monitor rule", and that claim
		// went stale underneath it: the monitor goes through `devices.Pick`,
		// which drops the infrared sensors first. Windows Hello puts one in most
		// laptops, and where it enumerates first this tool measured the infrared
		// sensor while the monitor filmed the room — same binary, same flags, a
		// different device, and **nothing anywhere saying so**. Every number in
		// `baselines/` was taken with this tool.
		//
		// It is the rule this repository already keeps about `pat-diag` — an
		// instrument that opens differently proves nothing — met from the side
		// nobody had looked at.
		cam, _, err = devices.Pick(all, "")
		if err != nil {
			fatal("%v", err)
		}
	}

	settings := encoder.DefaultSettings()
	settings.Width, settings.Height, settings.FPS = *width, *height, *fps
	settings.BitrateKbps = *bitrate

	fmt.Println(strings.Repeat("=", 76))
	fmt.Printf("  webcam:    %s\n", cam.Name)
	// Asked for by hand and infrared: it is reached, and it is declared. An
	// infrared sensor read as ordinary video gives a picture the encoder
	// measures happily and nobody can watch.
	if cam.IsLikelyIR() {
		fmt.Printf("  NOTE:      that is an infrared sensor, which the monitor never opens\n")
	}
	fmt.Printf("  requested: %dx%d@%d, %d kbit/s\n", *width, *height, *fps, *bitrate)
	// **Here it declares and does not correct.** The monitor lowers the size on
	// its own, but this is a measuring instrument and the numbers were chosen by
	// whoever ran it: changing them underneath would mean reporting a test that
	// is not the one asked for. Saying so is needed, though, because with the
	// scaler on, a request larger than the camera **succeeds** — and what gets
	// measured is a software enlargement, not the camera.
	if w, h, f, clamped, err := mf.PickCameraSize(cam.Link(), *width, *height, *fps); err == nil && clamped {
		fmt.Printf("  NOTE:      the camera declares at most %dx%d@%d: what follows measures an upscale\n", w, h, f)
	}
	fmt.Printf("  duration:  %s\n", *duration)
	fmt.Println(strings.Repeat("=", 76))

	// --- statistics gathered from the four streams ---
	var mu sync.Mutex
	// The state of the keyframe-on-demand test, under the same lock as the sink
	// that reads it.
	var (
		pliAsked, pliMissed, pliRefused int
		pliAskedFrame                   int64
		pliAskedAt                      time.Time
		pliDelays                       []time.Duration
	)

	var (
		videoBytes, audioBytes int64
		// A snapshot at the moment the bitrate changes, so as to weigh the two
		// halves separately instead of the mean, which would hide the difference.
		bytesAtSwitch   int64
		timeAtSwitch    time.Time
		sawSPS, sawPPS  bool
		profileLevelIDs []string
		spsSizes        []string
		spsRaw          []string
		motionFrames    int
		// interSizes are the bytes of every frame that is not a keyframe.
		//
		// **It is the only instrument that tells a duplicated frame from a real
		// one.** The Source Reader's video processor is documented to match the
		// output type's `MF_MT_FRAME_RATE`, so a camera delivering less than the
		// cadence we write can have the difference made up by repetition — and
		// from every count, every rate and every delivery measure, a repeated
		// frame and a filmed one look the same. They do not look the same to the
		// encoder: a frame identical to its predecessor is a P slice with no
		// residual, tens of bytes, while a real one at these sizes is
		// kilobytes. Two populations three orders apart need no threshold.
		interSizes     []int
		levelAcc       detect.Accumulator
		lastLevel      detect.Block
		prevGray       []byte
		motionScoreMax float64
	)
	videoDelivery := fullRun(time.Second / time.Duration(settings.FPS))
	audioDelivery := fullRun(opusFrameDuration)
	// The analysis PCM is born of the same captured block that feeds the Opus.
	// Comparing the two deliveries says whether an irregularity comes from the
	// microphone or from the encoder, without having to guess.
	pcmDelivery := fullRun(analysisBlockDuration)
	// The two stages of the audio chain, measured with the pipeline under load:
	// they are the only stretch that cannot be observed from outside.
	trace := &pipeline.Trace{
		MicCallback: fullRun(wasapiPeriod),
		AudioEncode: fullRun(opusFrameDuration),
	}
	// Exercising the fMP4 muxer on the real stream. It is the format both the
	// playback fallback and the recorded clips need, and the only proof that
	// counts is that an independent player can open the result.
	var rec *fmp4Recorder
	if *fmp4Out != "" {
		rec, err = newFMP4Recorder(*fmp4Out, time.Second/time.Duration(settings.FPS))
		if err != nil {
			fatal("%v", err)
		}
		defer rec.close()
	}

	// An unknown criterion is refused instead of falling back on the default: a
	// measurement taken with -rc misspelt would say "cbr" while calling itself
	// something else, and that is exactly how a test accuses somebody else.
	rc := mf.RateControl(*rateCtl)
	if rc != mf.RateCBR && rc != mf.RateQuality && rc != mf.RateCapped {
		fatal("unknown rate control: %q (cbr, quality or capped)", *rateCtl)
	}

	start := time.Now()

	p := pipeline.New(pipeline.Config{
		CameraLink: cam.Link(),
		// **The size stays the one asked for**, which is what the NOTE above
		// promises: the monitor lowers it to what the camera declares, and here
		// that would measure the camera while the report says it is measuring an
		// upscale. An instrument that quietly changes what it measures is worse
		// than one that measures the wrong thing loudly.
		KeepRequestedSize: true,
		Width:             settings.Width,
		Height:            settings.Height,
		FPS:               settings.FPS,
		BitrateKbps:       settings.BitrateKbps,
		// From the settings: the report below computes how many keyframes the
		// run should have produced from the same field, and a literal here would
		// let the two part company.
		GOPSeconds:              settings.KeyframeSecs,
		RateControl:             rc,
		Quality:                 *qLevel,
		MinQP:                   *minQP,
		MaxQP:                   *maxQP,
		PreferEncoder:           *preferEnc,
		PinNativeFormat:         *pinNative,
		KeepFrameRateConversion: *keepFRC,
		MicGainDB:               *micGain,
		AudioTestTone:           *testTone,
		Trace:                   trace,
		Log:                     log,
	})

	sinks := pipeline.Sinks{
		Video: func(au media.AccessUnit) {
			mu.Lock()
			defer mu.Unlock()
			now := time.Now()
			videoDelivery.Mark(now)
			videoBytes += int64(len(au.Data))
			if !au.Keyframe {
				interSizes = append(interSizes, len(au.Data))
			}
			// **A request that goes unanswered has to expire**, and the first
			// version of this had no way to: only a keyframe cleared the
			// pending request, so on an encoder that ignores the command the
			// very first one would stay pending for ever and the measurement
			// would report a single request instead of the silence of forty.
			// The window closes on the frame count, exactly as the pipeline's
			// watchdog closes it.
			if !pliAskedAt.IsZero() {
				since := p.Stats.VideoFrames.Load() - pliAskedFrame
				switch {
				case au.Keyframe && since <= pliWindowFrames:
					pliDelays = append(pliDelays, now.Sub(pliAskedAt))
					pliAskedAt = time.Time{}
				case since > pliWindowFrames:
					pliMissed++
					pliAskedAt = time.Time{}
				}
			}
			if rec != nil {
				if err := rec.feedVideo(au); err != nil {
					fmt.Fprintf(os.Stderr, "fmp4: %v\n", err)
				}
				if err := rec.flush(now); err != nil {
					fmt.Fprintf(os.Stderr, "fmp4: %v\n", err)
				}
			}
			// Checks that the parameter sets accompany the stream: without
			// SPS/PPS before every keyframe a browser cannot start.
			media.IterateAnnexB(au.Data, func(n media.NAL) bool {
				switch n.Type {
				case media.NALTypeSPS:
					sawSPS = true
					// **All** the profile-level-ids are collected, not just the
					// first. The SDP declares the one from the start and cannot
					// be renegotiated without interrupting whoever is watching:
					// if the encoder changes one along the way — which a
					// reconfiguration can cause, since it produces a new SPS —
					// the stream stops matching what the browser expects and
					// decoding fails silently. Keeping only the first hides
					// exactly the thing one wants to know.
					if id, err := media.ProfileLevelID(n.Data); err == nil &&
						!slices.Contains(profileLevelIDs, id) {
						profileLevelIDs = append(profileLevelIDs, id)
					}
					// The size is read from the SPS, which is what the decoder
					// sees: the Source Reader and the encoder can only
					// **declare** that they have changed format.
					// The comparison is on the **bytes**, not on the size: two
					// different SPS declaring the same pixels are news, and
					// deduplicating on the size would hide them.
					raw := hex.EncodeToString(n.Data)
					if !slices.Contains(spsRaw, raw) {
						spsRaw = append(spsRaw, raw)
						w, h, err := media.SPSSize(n.Data)
						s := fmt.Sprintf("%dx%d", w, h)
						if err != nil {
							s = "unreadable: " + err.Error()
						} else if !slices.Contains(spsSizes, s) {
							spsSizes = append(spsSizes, s)
						}
						log.Debug("new SPS", "size", s, "bytes", raw)
					}
				case media.NALTypePPS:
					sawPPS = true
				}
				return true
			})
		},
		Audio: func(pkt []byte) {
			mu.Lock()
			defer mu.Unlock()
			audioDelivery.Mark(time.Now())
			audioBytes += int64(len(pkt))
			if rec != nil {
				rec.feedAudio(pkt)
			}
		},
		Motion: func(frame []byte, _, _ int) {
			mu.Lock()
			defer mu.Unlock()
			motionFrames++
			if prevGray != nil {
				if s := meanAbsDiff(prevGray, frame); s > motionScoreMax {
					motionScoreMax = s
				}
			}
			prevGray = append(prevGray[:0], frame...)
		},
		Level: func(pcm []byte) {
			mu.Lock()
			defer mu.Unlock()
			pcmDelivery.Mark(time.Now())
			lastLevel = levelAcc.Add(pcm)
		},
	}

	runCtx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()

	// Hot bitrate test: halfway through, a different value is asked for and what
	// comes out before and after is weighed.
	//
	// It exists because the same question, put to the encoder, got a false
	// answer: `IsModifiable` declares that the bitrate can be changed hot,
	// `SetValue` answers S_OK, and the frames go on coming out at the old value.
	// Measured in service: 300 kbit/s asked for over twenty seconds, 2500
	// produced, and 33% of the packets lost over a cellular network. From here
	// on every attempted remedy is judged by this test, which needs no viewer
	// and repeats identically.
	if *bitrate2 > 0 {
		go func() {
			select {
			case <-runCtx.Done():
				return
			case <-time.After(*duration / 2):
			}
			mu.Lock()
			bytesAtSwitch, timeAtSwitch = videoBytes, time.Now()
			mu.Unlock()
			// The two roads are tried by the same test, one after the other,
			// on the same machine: it is the only way of knowing whether the
			// second is really needed or is merely paying for a keyframe for
			// nothing.
			change, how := p.SetBitrate, "command"
			if strings.HasPrefix(*brMode, "rec") {
				change, how = p.ReconfigureBitrate, "reconfiguration"
			}
			if err := change(*bitrate2); err != nil {
				fmt.Printf("\n  bitrate change refused (%s): %v\n\n", how, err)
				return
			}
			fmt.Printf("\n  --- asked for %d kbit/s (was %d), by %s ---\n\n", *bitrate2, *bitrate, how)
		}()
	}

	// Keyframes on demand. A viewer that loses the reference sends a PLI, and
	// what answers it is `AVEncVideoForceKeyFrame`: if the encoder ignores it the
	// picture stays frozen until the periodic keyframe, that is, up to a whole
	// GOP. The property is declared per vendor and the declaration is worth
	// nothing — AMD answers `E_NOTIMPL` to `IsModifiable` and produces them
	// perfectly well — so the only road is to ask and count.
	//
	// **A request counts as answered within five frames**, which is the window
	// the pipeline's own watchdog uses: beyond it, what arrives is the periodic
	// keyframe and not our answer. With a request more often than the GOP, an
	// encoder that ignored them would show delays around the GOP rather than
	// around a frame, so the distribution says which happened even before the
	// count does.
	if *pliEvery > 0 {
		go func() {
			t := time.NewTicker(*pliEvery)
			defer t.Stop()
			// **The asking stops before the run does**, and that is an
			// experiment rather than caution. Both runs with this flag ended in
			// an access violation at teardown on one machine, and the two
			// hypotheses — our request racing the encoder's release, or the
			// encoder's own worker thread faulting as it is dismantled — predict
			// the same silence. Leaving the last two seconds clear removes the
			// first one: if the crash survives it, the race is not ours.
			last := time.Now().Add(*duration - pliQuietBeforeEnd)
			for {
				select {
				case <-runCtx.Done():
					return
				case <-t.C:
				}
				if time.Now().After(last) {
					return
				}
				mu.Lock()
				waiting := !pliAskedAt.IsZero()
				mu.Unlock()
				if waiting {
					continue // the previous one has not been answered yet
				}
				mu.Lock()
				pliAskedAt, pliAskedFrame, pliAsked = time.Now(), p.Stats.VideoFrames.Load(), pliAsked+1
				mu.Unlock()
				if err := p.ForceKeyFrame(); err != nil {
					mu.Lock()
					pliAskedAt, pliRefused = time.Time{}, pliRefused+1
					mu.Unlock()
				}
			}
		}()
	}

	// Resolution scale test: halfway through, a smaller size is asked for. The
	// verdict comes from the SPS, not from what the pipeline declares.
	if *height2 > 0 || *fps2 > 0 {
		go func() {
			select {
			case <-runCtx.Done():
				return
			case <-time.After(*duration / 2):
			}
			h2, f2 := *height, *fps
			if *height2 > 0 {
				h2 = *height2
			}
			if *fps2 > 0 {
				f2 = *fps2
			}
			// **The width is asked for, not derived**, and the reason is that
			// the monitor's own steps are not 16:9. The resolution scale takes
			// fractions of the starting size and rounds each to a macroblock, so
			// three quarters of 1280x720 is **960x528** — 1.82:1 — while a width
			// computed from the height gives 928x528. Testing 928 would be a
			// tool asking a size the monitor never asks for, which is the trap
			// already paid for by the probe that opened the camera with a
			// Direct3D device the monitor does not use.
			w2 := *width2
			if w2 == 0 {
				w2 = h2 * 16 / 9
				w2 -= w2 % 16 // H.264 sizes go by macroblocks
			}
			p.SetVideoFormat(w2, h2, f2, f2)
			fmt.Printf("\n  --- asked for %dx%d@%d ---\n\n", w2, h2, f2)
		}()
	}

	// Periodic status line, so that the pipeline can be seen to be alive.
	done := make(chan struct{})
	go func() {
		defer close(done)
		// One second, not two: the periodic line is not only there to show that
		// the pipeline is alive, it is there to read the **transients** — how
		// long the quantiser takes to come back after a command, how long a
		// degradation lasts when the scene changes abruptly. At two seconds a
		// transient of three passes for two samples and cannot be told from a
		// step.
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var prevBytes int64
		prevAt := start
		for {
			select {
			case <-runCtx.Done():
				return
			case now := <-t.C:
				mu.Lock()
				lvl := lastLevel
				bytes := videoBytes
				mu.Unlock()

				kbps := 0.0
				if dt := now.Sub(prevAt).Seconds(); dt > 0 {
					kbps = float64(bytes-prevBytes) * 8 / 1000 / dt
				}
				prevBytes, prevAt = bytes, now

				// The mean over the last second, **not** the quantiser of the
				// last frame: that is what whoever decides sees, and a single
				// frame jumps ten points between a keyframe and an ordinary one.
				// Looking at a quantity other than the one decisions are made on
				// is a refined way of measuring the wrong thing.
				qp := "  -"
				if v, ok := p.TakeRecentQP(); ok {
					qp = fmt.Sprintf("%3d", v)
				}
				fmt.Printf("  %4.0fs  video %5d frames (%d key)  %6.0f kbit/s  qp %s  audio %5d pkt  level %6.1f dBFS\n",
					time.Since(start).Seconds(),
					p.Stats.VideoFrames.Load(), p.Stats.Keyframes.Load(),
					kbps, qp,
					p.Stats.AudioPackets.Load(), lvl.RMSdBFS)
			}
		}
	}()

	runErr := p.Run(runCtx, sinks)
	<-done
	elapsed := time.Since(start)

	// --- report ---
	mu.Lock()
	defer mu.Unlock()

	fmt.Println()
	fmt.Println(strings.Repeat("=", 76))
	fmt.Printf("  VERDICT — %.1fs actual\n", elapsed.Seconds())
	fmt.Println(strings.Repeat("=", 76))

	vFrames := p.Stats.VideoFrames.Load()
	keys := p.Stats.Keyframes.Load()
	aPkts := p.Stats.AudioPackets.Load()
	total := detect.Block{}
	if levelAcc.Result().Samples > 0 {
		total = levelAcc.Result()
	}

	pass := true
	check := func(ok bool, format string, a ...any) {
		mark := "OK  "
		if !ok {
			mark, pass = "FAIL", false
		}
		fmt.Printf("  [%s] %s\n", mark, fmt.Sprintf(format, a...))
	}

	check(vFrames > 0, "video: %d frames, %.1f fps, %.0f kbit/s",
		vFrames, float64(vFrames)/elapsed.Seconds(), float64(videoBytes)*8/1000/elapsed.Seconds())

	// The two halves of the hot bitrate test. The mean is no use here: it hides
	// exactly what one wants to see.
	if !timeAtSwitch.IsZero() {
		beforeSecs := timeAtSwitch.Sub(start).Seconds()
		afterSecs := time.Since(timeAtSwitch).Seconds()
		if beforeSecs > 0 && afterSecs > 0 {
			before := float64(bytesAtSwitch) * 8 / 1000 / beforeSecs
			after := float64(videoBytes-bytesAtSwitch) * 8 / 1000 / afterSecs
			held, judgeable := bitrateTookHold(before, after, float64(*bitrate2))
			switch {
			case !judgeable:
				fmt.Printf("  [    ] hot bitrate: asked %d with %.0f already coming out, "+
					"so nothing was taken away and no verdict is possible\n", *bitrate2, before)
			default:
				check(held, "hot bitrate: asked %d, produced %.0f before and %.0f after",
					*bitrate2, before, after)
				if !held {
					fmt.Println("         the command was accepted and not executed:")
					fmt.Println("         congestion control on this machine is a fiction.")
				}
			}
		}
	}
	check(keys > 0, "keyframes: %d (expected ~%.0f with a %ds GOP)",
		keys, elapsed.Seconds()/float64(settings.KeyframeSecs), settings.KeyframeSecs)

	if pliAsked > 0 {
		slices.Sort(pliDelays)
		switch {
		case len(pliDelays) == 0:
			check(false, "keyframes on demand: %d asked, none arrived within %d frames "+
				"(%d refused outright)", pliAsked, pliWindowFrames, pliRefused)
			fmt.Println("         the encoder does not answer the request: after a loss the")
			fmt.Println("         picture stays frozen until the periodic keyframe.")
		default:
			mid := pliDelays[len(pliDelays)/2]
			check(pliMissed == 0 && pliRefused == 0,
				"keyframes on demand: %d asked, %d arrived within %d frames, "+
					"median %v, max %v (%d late, %d refused)",
				pliAsked, len(pliDelays), pliWindowFrames,
				mid.Round(time.Millisecond), pliDelays[len(pliDelays)-1].Round(time.Millisecond),
				pliMissed, pliRefused)
		}
	}
	// More than one profile-level-id is a fault, not a curiosity: the SDP
	// declares only one and whoever is watching has already set up the decoder on it.
	check(sawSPS && sawPPS && len(profileLevelIDs) <= 1,
		"parameter sets in the stream: SPS=%v PPS=%v, profile-level-id=%s",
		sawSPS, sawPPS, strings.Join(profileLevelIDs, " then "))
	if *height2 > 0 {
		// Two sizes are the expected outcome: the starting one and the one
		// asked for. Only one means the change did not reach the decoder,
		// however much the reader and the encoder may both have accepted it.
		check(len(spsSizes) == 2, "resolution scaling: sizes seen in the stream %s",
			strings.Join(spsSizes, " then "))
	}
	// **Are the frames filmed, or repeated?** It is a reading and not a verdict:
	// on a still scene at a high bitrate real frames get cheap too, so what says
	// "repeated" is not a low median but a population of frames that cost
	// essentially nothing while others cost kilobytes.
	if n := len(interSizes); n > 0 {
		sorted := slices.Clone(interSizes)
		slices.Sort(sorted)
		at := func(p int) int { return sorted[(p*(n-1))/100] }
		tiny := 0
		for _, v := range sorted {
			if v < 500 {
				tiny++
			}
		}
		fmt.Printf("  [    ] frames between keyframes: %d, bytes p10 %d  median %d  p90 %d  —  under 500 bytes: %d (%d%%)\n",
			n, at(10), at(50), at(90), tiny, 100*tiny/n)
	}
	// The quantiser is the number that says whether the picture will hold up
	// under movement, and it is not an OK/FAIL: it is a measurement to be read.
	// It is declared even when it is missing, because an encoder that does not
	// expose it is information — it means that on this machine quality can only
	// be governed blind.
	if qp := p.QP(); qp.Known {
		// **Where the number comes from has to be said with the number.** The
		// attribute is the mean of the macroblocks, the stream is the slice
		// quantiser, and on Quick Sync they are six and a half points apart:
		// without the provenance, two machines filming the same room look as
		// though they were filming two different rooms.
		fmt.Printf("  [    ] quantiser: mean %.1f, p95 %d, max %d, over %d frames (from %s)\n",
			qp.Mean, qp.P95, qp.Max, qp.Samples, p.QPSource())
		// A reading that never varies is not a measurement, and it is dangerous
		// because it has every appearance of one: it is the case of the slice
		// quantiser on Quick Sync, nailed at 26 on every frame. Downstream
		// `qualityGovernor` notices and stops believing it; here it is said at
		// once, because this is the test one looks at.
		if qp.Samples > 60 && qp.Max == qp.P95 && float64(qp.Max) == qp.Mean {
			fmt.Printf("  [FAIL] quantiser stuck at %d over all %d frames: that reading does not measure this encoder\n",
				qp.Max, qp.Samples)
		}
		// The quantiser comes from the stream; the sample attribute, where it
		// exists, serves only as a cross-check. If the two roads diverge that
		// has to be said: the one to believe is the stream, but knowing it is
		// better than discovering it.
		//
		// **The state of the comparison is always declared, even when the
		// attribute is absent.** Printing it only when the attribute is present
		// makes "they agree", "they diverge" and "I could not compare" look
		// identical from outside, which is exactly the ambiguity the attribute
		// inventory exists to remove.
		// Whether the attribute is present is established **by trying to read
		// it**, not by enumerating the sample's keys: the enumeration answers
		// zero even where the attribute reads perfectly well, so it does not
		// tell "it is not there" from "I cannot see it".
		withAttr, withoutAttr, gapSum, bias := p.QPAttributeCounts()
		div := p.QPDivergences()
		switch {
		case withAttr == 0:
			// **"It does not declare it" and "we do not know how to ask" look
			// the same from outside**, and here the difference decides where the
			// quantiser comes from on this machine. So the attributes the
			// encoder really does set on its samples are listed: if it sets
			// others and the quantiser is not there, the absence is its own and
			// the matter is closed; if it sets none at all, we cannot tell the
			// two apart and that has to be said instead of concluding.
			keys, hasQP := p.SampleAttributes()
			switch {
			case hasQP:
				fmt.Printf("  [FAIL] the quantiser attribute is on the samples but we read it "+
					"on none of the %d frames\n", withoutAttr)
			case len(keys) > 0:
				fmt.Printf("  [    ] no cross-check: over %d frames the encoder sets %d attributes "+
					"and the quantiser is not among them — the absence is its own, we use the stream\n",
					withoutAttr, len(keys))
			default:
				fmt.Printf("  [    ] no cross-check: over %d frames not one attribute — we cannot "+
					"tell 'it declares none' from 'we cannot read them'\n", withoutAttr)
			}
		case div == 0:
			fmt.Printf("  [OK  ] stream and attribute agree on all %d frames "+
				"where the attribute was present\n", withAttr)
		case streamIsConstant(p, withAttr):
			// **Diverging is not being wrong, if one of the two does not vary.**
			// On Quick Sync the slice quantiser is constant — measured, 26.0
			// over 696 frames, keyframes included — and the whole control goes
			// through the macroblock deltas, which do not appear in the header.
			// The reading from the stream is right: it is that field that says
			// nothing.
			//
			// That it was not our parser was established by the experiment that
			// separates the two hypotheses, which predicted the same numbers:
			// same parser and same machine with the Windows software encoder,
			// where the stream varies (mean 30.7, max 45) and agrees with the
			// attribute at **direction +0.0**, that is, with no systematic error.
			//
			// Marking it FAIL would be an alarm permanently lit on half the
			// machines in the world, and an alarm that is always on stops being
			// read.
			streamQP, _ := p.QPStreamRange()
			fmt.Printf("  [    ] the stream always declares %d over all %d frames: this encoder keeps the "+
				"slice quantiser constant and works per macroblock, so the attribute is the one to trust\n",
				streamQP, withAttr)
		default:
			// **By how much they diverge matters more than how often.** We read
			// the **slice** quantiser; the encoder may report the mean of the
			// macroblocks, which with adaptive quantisation differs from it by
			// little. A couple of points is that difference, and it is
			// agreement. Many points would mean one of the two readings is
			// broken, and the one to believe stays the stream — it is what the
			// decoder reads.
			//
			// **And in which direction matters more than by how much.** Adaptive
			// quantisation moves the macroblocks now above and now below the
			// slice quantiser, so its deviation cancels out and the mean
			// direction stays near zero. A deviation always on the same side is
			// not a phenomenon, it is one of the two readings getting it wrong
			// the same way every time — and the absolute value alone confuses
			// the two.
			mean := float64(gapSum) / float64(div)
			direction := float64(bias) / float64(div)
			mark := "OK  "
			switch {
			case mean > 2:
				mark = "FAIL"
			case direction > 1 || direction < -1:
				// A small gap but all on one side: that is not agreement, it is
				// a small systematic error.
				mark = "FAIL"
			}
			fmt.Printf("  [%s] stream and attribute: mean gap %.1f points (direction %+.1f) "+
				"over %d frames of %d that diverge (slice against macroblock mean)\n",
				mark, mean, direction, div, withAttr)
		}
	} else {
		// **"It does not declare it" and "we do not know how to ask" look the
		// same from outside**, and the difference decides where the quantiser
		// comes from. So the attributes the encoder really does set on its
		// samples are listed: if the quantiser is not among them, the absence is
		// its own.
		keys, hasQP := p.SampleAttributes()
		switch {
		case hasQP:
			fmt.Printf("  [FAIL] quantiser: the attribute is on the samples but we do not read it\n")
		case len(keys) > 0:
			fmt.Printf("  [    ] quantiser: this encoder does not declare it "+
				"(%d attributes on the samples, none is the QP)\n", len(keys))
			for _, k := range keys {
				fmt.Printf("           %s\n", k)
			}
		default:
			fmt.Printf("  [    ] quantiser: this encoder does not declare it " +
				"(its samples carry no attribute at all)\n")
		}
	}
	check(aPkts > 0, "audio: %d Opus packets, %.0f kbit/s",
		aPkts, float64(audioBytes)*8/1000/elapsed.Seconds())
	check(motionFrames > 0, "motion: %d gray frames, max frame-to-frame difference %.2f",
		motionFrames, motionScoreMax)
	check(total.Samples > 0, "audio analysis: %.1fs, %s",
		float64(total.Samples)/pipeline.AnalysisSampleRate, total.Explain())

	// Regularity of delivery. The browser estimates the jitter from these
	// intervals and sizes its playout buffer accordingly, which is constant
	// latency: delivery in bursts costs delay even with zero packets lost and
	// even over localhost.
	//
	// The number to look at is "mean per burst": how much audio or video is
	// delivered all at once. It is the quantity the receiver's buffer has to
	// cover, so it compares directly with the jitterBufferMinimumDelay read in
	// webrtc-internals.
	fmt.Println()
	videoDelivery.Report(os.Stdout, "video (Annex-B)")
	audioDelivery.Report(os.Stdout, "audio (Opus in Ogg)")
	pcmDelivery.Report(os.Stdout, "audio (raw PCM)")
	trace.MicCallback.Report(os.Stdout, "WASAPI callback (under load)")
	trace.AudioEncode.Report(os.Stdout, "audio (encoder output)")

	// Bypassing the OEM effects is the reason the audio is captured from WASAPI
	// instead of with the rest of the pipeline.
	check(p.RawAudioMode(), "audio in WASAPI raw mode (OEM effects bypassed): %v", p.RawAudioMode())

	if r := p.Stats.Restarts.Load(); r > 0 {
		check(false, "pipeline restarts: %d (expected 0)", r)
	}
	if d := p.Stats.AudioDropped.Load(); d > 0 {
		fmt.Printf("  [warn] audio blocks dropped: %d\n", d)
	}
	if runErr != nil {
		check(false, "pipeline error: %v", runErr)
	}

	fmt.Println(strings.Repeat("-", 76))
	if pass {
		fmt.Println("  Pipeline working: the four streams arrive with valid data.")
	} else {
		fmt.Println("  Pipeline NOT working: see the FAIL lines above.")
	}
	fmt.Println()

	if !pass {
		os.Exit(1)
	}
}

// opusFrameDuration is the duration of one Opus packet produced by the
// pipeline. It equals audiocodec.FrameDuration: it is repeated here because it
// is the *expected* value, and a reference would make it true by construction.
const opusFrameDuration = 20 * time.Millisecond

// analysisBlockDuration is the duration of the analysis PCM block read from the
// pipeline, fixed in readLevel.
const analysisBlockDuration = 100 * time.Millisecond

// fmp4Recorder writes the fMP4 muxer's output to a file.
//
// It exists to exercise the muxer on the real stream: an independent player
// that opens the file and reads its duration, codec and frame count is a proof
// no test built on synthetic data can give.
type fmp4Recorder struct {
	f        *os.File
	mux      *media.FMP4Muxer
	frameDur time.Duration
	sps, pps []byte
	// started becomes true at the first keyframe after the initialisation: a
	// segment starting with a differential frame is not decodable.
	started   bool
	lastFlush time.Time
}

func newFMP4Recorder(path string, frameDur time.Duration) (*fmp4Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", path, err)
	}
	return &fmp4Recorder{f: f, frameDur: frameDur}, nil
}

func (r *fmp4Recorder) feedVideo(au media.AccessUnit) error {
	if r.mux == nil {
		// The parameter sets describe the track and have to be collected before
		// the initialisation can be written.
		media.IterateAnnexB(au.Data, func(n media.NAL) bool {
			switch n.Type {
			case media.NALTypeSPS:
				r.sps = append(r.sps[:0], n.Data...)
			case media.NALTypePPS:
				r.pps = append(r.pps[:0], n.Data...)
			}
			return true
		})
		if len(r.sps) == 0 || len(r.pps) == 0 {
			return nil
		}
		mux, err := media.NewFMP4Muxer(r.sps, r.pps, 2)
		if err != nil {
			return err
		}
		init, err := mux.Init()
		if err != nil {
			return err
		}
		if _, err := r.f.Write(init); err != nil {
			return err
		}
		r.mux = mux
		r.lastFlush = time.Now()
	}

	if !r.started {
		if !au.Keyframe {
			return nil
		}
		r.started = true
	}
	return r.mux.AddVideo(au.Data, r.frameDur)
}

func (r *fmp4Recorder) feedAudio(pkt []byte) {
	if r.mux == nil || !r.started {
		return
	}
	r.mux.AddAudio(pkt, opusFrameDuration)
}

// flush emits a segment if enough time has passed.
func (r *fmp4Recorder) flush(now time.Time) error {
	if r.mux == nil || now.Sub(r.lastFlush) < time.Second {
		return nil
	}
	r.lastFlush = now
	seg, err := r.mux.Segment()
	if err != nil || seg == nil {
		return err
	}
	_, err = r.f.Write(seg)
	return err
}

func (r *fmp4Recorder) close() {
	if r.mux != nil {
		if seg, err := r.mux.Segment(); err == nil && seg != nil {
			_, _ = r.f.Write(seg)
		}
	}
	_ = r.f.Close()
}

// fullRun builds a meter whose window covers the whole test.
//
// diag.Delivery's default is a short sliding window, which is what an
// always-on process needs: it describes what delivery is like now without
// accumulating memory. Here the test has a known duration and the report is
// wanted over all of it, so the window is sized accordingly, with a margin for
// deliveries arriving closer together than nominal.
func fullRun(nominal time.Duration) *diag.Delivery {
	return &diag.Delivery{
		Nominal: nominal,
		Window:  int(*duration/nominal)*2 + 64,
	}
}

// wasapiPeriod is the device period in shared mode on Windows.
const wasapiPeriod = 10 * time.Millisecond

// meanAbsDiff measures the mean difference between two gray frames, normalised.
// It is the basis of motion detection.
func meanAbsDiff(a, b []byte) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var sum int64
	for i := 0; i < n; i++ {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += int64(d)
	}
	return float64(sum) / float64(n)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// streamIsConstant says whether the quantiser read from the stream never
// changed value. That is not a defect of the reading: it is an encoder that
// keeps the slice quantiser fixed and leaves the control to the macroblock
// deltas. It needs enough samples, otherwise a very short test on a still scene
// would say it of anybody.
func streamIsConstant(p *pipeline.Pipeline, samples int64) bool {
	min, max := p.QPStreamRange()
	return samples > 60 && min > 0 && min == max
}
