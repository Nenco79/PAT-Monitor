// Checks that WASAPI capture in raw mode really does bypass the OEM's Audio
// Processing Objects.
//
// The comparison is A/B on the same microphone: first with the processing chain
// active, then in raw mode. If the bypass works, raw mode shows a live noise
// floor (a standard deviation of a few quantisation units, few exact zeros)
// while the normal mode delivers digital silence. No sound needs to be made.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"time"

	"patmonitor/internal/audio"
	"patmonitor/internal/audiocodec"
	"patmonitor/internal/detect"
	"patmonitor/internal/diag"
)

var (
	duration   = flag.Duration("d", 6*time.Second, "duration of each measurement phase")
	deviceID   = flag.String("device", "", "endpoint ID to use (empty = default)")
	outputOnly = flag.Bool("out", false, "test the talk-back audio output instead of the microphone")
)

func main() {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The output is tested separately: it is the other half of the room, and
	// whoever comes here for the talk-back does not need to wait for two
	// measurements of the microphone.
	if *outputOnly {
		testOutput(*duration)
		return
	}

	fmt.Println("=== WASAPI CAPTURE ENDPOINTS ===")
	devs, err := audio.ListCaptureDevices()
	if err != nil {
		fatal("enumeration: %v", err)
	}
	for _, d := range devs {
		mark := " "
		if d.IsDefault {
			mark = "*"
		}
		fmt.Printf(" %s %s\n   %s\n", mark, d.Name, d.ID)
	}
	if len(devs) == 0 {
		fatal("no active capture endpoint")
	}
	fmt.Println()

	// Normal mode first, then raw: the order does not matter, but measuring
	// them one after the other reduces the risk that something else changes in
	// between.
	normal, nStream, nErr := measure(ctx, modeShared, *duration)
	rawBlk, rStream, rErr := measure(ctx, modeRaw, *duration)
	excl, eStream, eErr := measure(ctx, modeExclusive, *duration)

	fmt.Println(strings.Repeat("=", 76))
	fmt.Println("  COMPARISON")
	fmt.Println(strings.Repeat("=", 76))

	report("with OEM processing", normal, nStream, nErr)
	report("RAW mode", rawBlk, rStream, rErr)
	report("EXCLUSIVE mode", excl, eStream, eErr)

	// The two roads that bypass the effects should deliver the same level. If
	// they do not, the difference is a gain that one of the two does not
	// receive, and it has to be reapplied downstream: that is why this
	// measurement exists.
	if rErr == nil && eErr == nil && rawBlk.Samples > 0 && excl.Samples > 0 {
		fmt.Printf("\n  raw vs exclusive: %+.1f dB difference in RMS\n",
			excl.RMSdBFS-rawBlk.RMSdBFS)
	}

	fmt.Println(strings.Repeat("=", 76))
	verdict(normal, nErr, rawBlk, rErr)
}

// The three ways of opening the microphone.
type captureMode int

const (
	modeShared captureMode = iota
	modeRaw
	modeExclusive
)

func (m captureMode) String() string {
	switch m {
	case modeRaw:
		return "raw"
	case modeExclusive:
		return "exclusive"
	}
	return "shared"
}

// measure captures for the given duration and returns the statistics.
func measure(ctx context.Context, mode captureMode, d time.Duration) (detect.Block, audio.Stream, error) {
	label := mode.String()
	fmt.Printf("measuring in %s mode for %s...\n", label, d)

	runCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	var acc detect.Accumulator
	var stream audio.Stream

	// Cadence of the callback. In shared event-driven mode Windows should wake
	// us on every device period, typically 10 ms. If it delivers in bursts
	// instead, that grouping propagates intact along the whole pipeline as far
	// as the browser's playout buffer, where it is constant latency.
	cadence := &diag.Delivery{Nominal: 10 * time.Millisecond}

	err := audio.Capture(runCtx,
		audio.Options{
			DeviceID:  *deviceID,
			Raw:       mode == modeRaw,
			Exclusive: mode == modeExclusive,
		},
		func(s audio.Stream) error {
			stream = s
			fmt.Printf("  opened: %s — %s (mode=%s, no OEM effects=%v)\n",
				s.DeviceName, s.Format, s.Mode, s.RawMode)
			return nil
		},
		func(pcm []byte, silent bool) error {
			cadence.Mark(time.Now())
			acc.Add(toS16Channel0(pcm, stream.Format))
			return nil
		},
	)
	cadence.Report(os.Stdout, "WASAPI callback ("+label+")")
	// The context expiring is the normal end of the measurement.
	if err != nil && runCtx.Err() == nil {
		return detect.Block{}, stream, err
	}
	return acc.Result(), stream, nil
}

// toS16Channel0 extracts the first channel and converts it to s16le, the format
// the analyser expects.
//
// One channel is taken rather than averaging them: averaging channels whose
// noise is uncorrelated would lower the level by about 3 dB and falsify the
// count of exact zeros, which is precisely the figure under examination.
func toS16Channel0(b []byte, f audio.StreamFormat) []byte {
	bytesPerSample := f.BitsPerSample / 8
	frame := f.Channels * bytesPerSample
	if frame <= 0 || bytesPerSample <= 0 {
		return nil
	}
	frames := len(b) / frame
	out := make([]byte, 0, frames*2)

	for i := range frames {
		off := i * frame // channel 0
		var v int16
		switch {
		case f.Float && bytesPerSample == 4:
			bits := uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
			v = clampToInt16(float64(math.Float32frombits(bits)) * 32768)
		case !f.Float && bytesPerSample == 2:
			v = int16(uint16(b[off]) | uint16(b[off+1])<<8)
		case !f.Float && bytesPerSample == 4:
			u := uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
			v = int16(int32(u) >> 16)
		default:
			return nil
		}
		out = append(out, byte(v), byte(v>>8))
	}
	return out
}

func clampToInt16(f float64) int16 {
	switch {
	case f > math.MaxInt16:
		return math.MaxInt16
	case f < math.MinInt16:
		return math.MinInt16
	}
	return int16(f)
}

func report(label string, b detect.Block, s audio.Stream, err error) {
	fmt.Printf("\n  %s\n", label)
	if err != nil {
		fmt.Printf("    FAILED: %v\n", err)
		return
	}
	if b.Samples == 0 {
		fmt.Printf("    no sample received\n")
		return
	}
	fmt.Printf("    format: %s   raw obtained: %v\n", s.Format, s.RawMode)
	fmt.Printf("    %.1f s analysed\n", float64(b.Samples)/float64(maxInt(s.Format.SampleRate, 1)))
	fmt.Printf("    RMS %.1f dBFS   peak %.1f dBFS (%d LSB)\n", b.RMSdBFS, b.PeakdBFS, b.Peak)
	fmt.Printf("    exact zeros %.1f%%   noise floor %.2f LSB   -> %s\n",
		b.ZeroRatio*100, b.StdDevLSB, b.Health())
}

func verdict(normal detect.Block, nErr error, raw detect.Block, rErr error) {
	switch {
	case rErr != nil:
		fmt.Println("\n  Raw mode is not available on this endpoint.")
		fmt.Println("  Without the bypass the audio stays subject to the OEM processing:")
		fmt.Println("  turn off 'Audio enhancements' on the microphone in Windows Settings.")
	case raw.Samples == 0:
		fmt.Println("\n  Raw mode opened but delivered no samples.")
	case raw.Health() == detect.MicOK && normal.Health() != detect.MicOK:
		fmt.Println("\n  BYPASS CONFIRMED. In raw mode the microphone delivers a real noise")
		fmt.Println("  floor, while with the processing on it delivered digital silence.")
		fmt.Println("  WASAPI raw capture is the right approach: the app will hear the")
		fmt.Println("  crying whatever the Windows audio settings say.")
	case raw.StdDevLSB > normal.StdDevLSB*2 || raw.ZeroRatio < normal.ZeroRatio/2:
		fmt.Println("\n  Raw mode clearly improves the signal (more noise floor, fewer exact")
		fmt.Println("  zeros), so it is bypassing part of the processing, but the result is")
		fmt.Println("  not yet a fully healthy floor. Some always-on processing in the")
		fmt.Println("  hardware is probably still running.")
	case raw.Health() == detect.MicOK && normal.Health() == detect.MicOK:
		fmt.Println("\n  Both modes show a healthy noise floor: on this endpoint the")
		fmt.Println("  processing was not zeroing the signal.")
	default:
		fmt.Println("\n  No measurable difference between the two modes. The silence does")
		fmt.Println("  not come from a bypassable APO: check the microphone level and the")
		fmt.Println("  hardware mute.")
	}
	fmt.Println()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// --- the other direction: the output, that is, the talk-back ----------------

// testOutput answers the question the talk-back will give rise to: "I pressed
// to speak and nothing was heard in the room".
//
// **There are three causes and from outside they look alike**: the output does
// not open, it opens and does not play, or it plays at a volume that cannot be
// heard. The loopback separates the first two — it captures what the audio
// engine is playing, which is the only way of knowing whether a sound really
// came out rather than merely going into a buffer. The third is separated by
// the endpoint volume, which is read rather than guessed.
func testOutput(d time.Duration) {
	fmt.Println("\n=== AUDIO OUTPUT (talk-back) ===")

	vol, err := audio.RenderVolume()
	switch {
	case err != nil:
		fmt.Printf("  volume: cannot be read (%v)\n", err)
	case vol.Muted:
		fmt.Println("  volume: MUTED — whatever is played will not be heard")
	default:
		fmt.Printf("  volume: %.1f dB on a scale from %.0f to %.0f\n",
			vol.CurrentDB, vol.MinDB, vol.MaxDB)
		if vol.CurrentDB < vol.MaxDB-30 {
			fmt.Println("  WARNING: very low. A voice played here might not be heard at")
			fmt.Println("  all, and the monitor has no way of noticing.")
		}
	}

	floor, _, err := audio.LoopbackLevel(d / 3)
	if err != nil {
		fmt.Printf("  loopback unavailable: %v\n", err)
		return
	}
	fmt.Printf("  before the tone: %.1f dBFS\n", floor)

	p, err := audio.NewPlayer(audiocodec.SampleRate, *deviceID, nil)
	if err != nil {
		fmt.Printf("  OPEN FAILED: %v\n", err)
		fmt.Println("  Without an audio output there is no talk-back: the button never appears.")
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		playTone(p, d)
	}()
	time.Sleep(250 * time.Millisecond)
	during, peak, err := audio.LoopbackLevel(d / 2)
	<-done
	_ = p.Close()
	if err != nil {
		fmt.Printf("  loopback interrupted: %v\n", err)
		return
	}

	fmt.Printf("  while playing:  %.1f dBFS (peak %.4f), %d samples dropped\n",
		during, peak, p.Dropped())
	// The verdict has three values, and the third is the one that matters.
	//
	// **A noisy floor allows no conclusion at all**, and saying so is
	// mandatory: a two-valued verdict declares "nothing" whenever the tone does
	// not emerge, and on a machine where something is already playing at
	// -36 dBFS that accuses the driver of a fault that is not there. A wrong
	// measurement always accuses somebody else.
	//
	// The threshold is on the **floor**, not on the rise: below a certain level
	// of silence the measurement holds, above it does not, and no tuning of the
	// rise can make up for a tone that is covered.
	const usableFloor = -60
	rise := during - floor
	switch {
	case rise > 20:
		fmt.Printf("\n  THE SOUND COMES OUT: %.0f dB above the floor. Talk-back has a path.\n", rise)
	case floor > usableFloor:
		fmt.Printf("\n  INCONCLUSIVE: the floor is at %.0f dBFS and covers the tone.\n", floor)
		fmt.Println("  Something is already playing on this machine: close players and")
		fmt.Println("  browser tabs and try again. If the output volume is very low,")
		fmt.Println("  raise it first: the tone comes out attenuated by as much.")
	default:
		fmt.Printf("\n  NOTHING: only %.0f dB above a floor of %.0f dBFS. The path accepts\n",
			rise, floor)
		fmt.Println("  the samples and produces no sound — wrong driver or endpoint.")
	}
}

// playTone plays a concert A for the given duration, going through the same
// encoder/decoder pair as the talk-back: this way the test covers the codec too,
// and not only the output.
func playTone(p *audio.Player, d time.Duration) {
	enc, err := audiocodec.NewOpus(audiocodec.SampleRate, 64)
	if err != nil {
		return
	}
	defer enc.Close()
	dec, err := audiocodec.NewOpusDecoder(audiocodec.SampleRate, 1)
	if err != nil {
		return
	}
	defer dec.Close()

	frame := enc.FrameSamples()
	n := int(d / audiocodec.FrameDuration)
	phase, step := 0.0, 2*math.Pi*440/audiocodec.SampleRate
	for i := range n {
		pcm := make([]int16, frame)
		// Fade in and fade out: a tone that starts abruptly makes a click.
		amp := 0.25
		if i < 5 {
			amp *= float64(i) / 5
		} else if i > n-6 {
			amp *= float64(n-i) / 5
		}
		for j := range pcm {
			pcm[j] = int16(amp * 32767 * math.Sin(phase))
			phase += step
		}
		pkt, err := enc.Encode(pcm)
		if err != nil || len(pkt) == 0 {
			continue
		}
		out, err := dec.Decode(pkt)
		if err != nil {
			continue
		}
		p.Write(out)
		time.Sleep(audiocodec.FrameDuration)
	}
}
