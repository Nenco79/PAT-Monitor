// pat-opus tests the embedded audio encoder.
//
// It answers two questions, and deliberately leaves out a third: the codec's
// correctness is not in doubt, because inside there is real libopus, compiled
// to WebAssembly. What has to be checked is what we put around it.
//
//  1. What it costs. Average CPU is not enough: what matters most is how long a
//     single call takes, which has to sit comfortably inside a frame's 20 ms,
//     because the encoder runs on the same path as the capture.
//  2. Whether the wiring is right. It encodes here and decodes with pion/opus,
//     which is an independent implementation: if the two roads agree, the PCM
//     going in and the PCM coming out resemble each other.
//
// The test is run twice. First on a synthetic signal, which is repeatable and
// does not depend on how quiet the room is; then on the real microphone, which
// is the only way of measuring the cost under the conditions it will run in.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"slices"
	"time"

	"patmonitor/internal/audio"
	"patmonitor/internal/audiocodec"
	"patmonitor/internal/opuswasm"

	pionopus "github.com/pion/opus"
)

var (
	duration = flag.Duration("d", 20*time.Second, "duration of the microphone test")
	deviceID = flag.String("device", "", "microphone endpoint ID; empty uses the default")
	bitrate  = flag.Int("b", 64, "bitrate in kbit/s")
	// The rate is a parameter because not every microphone delivers 48 kHz:
	// Opus compresses five of them, and on a new machine the first question is
	// whether the chain copes with the one the microphone there produces.
	rate = flag.Int("rate", audiocodec.SampleRate, "sample rate (8000, 12000, 16000, 24000, 48000)")
)

// frameSamples is the frame length at the chosen rate.
func frameSamples() int { return audiocodec.FrameSamplesAt(*rate) }

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// Startup includes compiling the WebAssembly module into native code, which
	// happens once but weighs on the application's start-up time.
	start := time.Now()
	enc, err := audiocodec.NewOpus(*rate, *bitrate)
	if err != nil {
		return err
	}
	defer enc.Close()
	// **libopus's version cannot be read anywhere else.** The codec arrives as
	// an embedded WebAssembly module, so it appears neither in `go.mod` nor in
	// the log: whoever wonders which one is running asks it, and this tool is
	// where that question gets asked.
	libopusVersion, err := opuswasm.Version(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("encoder startup: %v  (%s)\n\n", time.Since(start).Round(time.Millisecond), libopusVersion)

	if err := syntheticTest(enc); err != nil {
		return err
	}
	return micTest(enc)
}

// ---------- synthetic test ----------

func syntheticTest(enc audiocodec.Encoder) error {
	const seconds = 3
	src := sweep(seconds * (*rate))

	packets, _, _, err := encodeAll(enc, src)
	if err != nil {
		return err
	}
	out, err := decodeAll(packets)
	if err != nil {
		return err
	}

	lag, corr := bestAlignment(src, out)
	fmt.Printf("synthetic signal (sweep from 200 Hz to %.0f Hz, at %d Hz)\n",
		math.Min(8000.0, 0.45*float64(*rate)), *rate)
	fmt.Printf("  packets            %d\n", len(packets))
	fmt.Printf("  actual bitrate     %.1f kbit/s\n", kbps(packets, seconds*time.Second))
	fmt.Printf("  codec delay        %d samples (%.1f ms)\n", lag, float64(lag)*1000/float64(*rate))
	fmt.Printf("  correlation        %.4f%s\n", corr, verdict(corr))
	fmt.Printf("  S/N ratio          %.1f dB\n\n", snr(src, out, lag))
	return nil
}

// sweep generates a frequency sweep, which covers the whole useful band with a
// signal whose likeness can be measured unambiguously.
//
// The sweep stops below the Nyquist frequency, which depends on the sampling
// rate. That is not pedantry: at 8 kHz a sweep reaching 8 kHz is half of it
// above the limit, so it goes in already distorted and comes out worse — and
// the signal-to-noise ratio being read is the **test's**, not the codec's.
// Measured: 21 dB at 8 kHz against 37 at 16 kHz, which made a codec that was
// working well look unsuitable.
func sweep(n int) []int16 {
	const f0 = 200.0
	// 90% of Nyquist leaves the anti-alias filter some margin.
	f1 := math.Min(8000.0, 0.45*float64(*rate))
	out := make([]int16, n)
	phase := 0.0
	for i := range out {
		t := float64(i) / float64(n)
		freq := f0 * math.Pow(f1/f0, t)
		phase += 2 * math.Pi * freq / float64(*rate)
		out[i] = int16(0.5 * 32767 * math.Sin(phase))
	}
	return out
}

// ---------- microphone test ----------

func micTest(enc audiocodec.Encoder) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()

	var (
		format  audio.StreamFormat
		mono    []int16
		scratch = make([]int16, 1<<16)
	)

	err := audio.Capture(ctx,
		audio.Options{DeviceID: *deviceID, Raw: true, FallbackOnRawFailure: true},
		func(s audio.Stream) error {
			format = s.Format
			fmt.Printf("microphone: %s, raw=%v\n", s.Format, s.RawMode)
			if !audiocodec.RateSupported(s.Format.SampleRate) {
				return fmt.Errorf("rate %d Hz not accepted by Opus: a resampler would be needed",
					s.Format.SampleRate)
			}
			// The encoder has already been created: if the microphone delivers
			// another rate, compressing it without saying so would give audio
			// at the wrong pitch and not a single number out of place.
			if s.Format.SampleRate != *rate {
				return fmt.Errorf("the microphone captures at %d Hz and the test runs at %d: "+
					"re-run with -rate %d", s.Format.SampleRate, *rate, s.Format.SampleRate)
			}
			return nil
		},
		func(pcm []byte, silent bool) error {
			n, err := audio.ToMonoS16(pcm, format, scratch)
			if err != nil {
				return err
			}
			mono = append(mono, scratch[:n]...)
			return nil
		})
	if err != nil && ctx.Err() == nil {
		return err
	}
	if len(mono) < frameSamples() {
		return fmt.Errorf("only %d samples captured", len(mono))
	}

	// Encoding is measured after the capture and not during it, so that the
	// number does not include waiting for blocks from the microphone.
	packets, took, wall, err := encodeAll(enc, mono)
	if err != nil {
		return err
	}
	out, err := decodeAll(packets)
	if err != nil {
		return err
	}

	audioLen := time.Duration(len(mono)) * time.Second / time.Duration(*rate)

	fmt.Printf("\nmicrophone, %v of audio\n", audioLen.Round(time.Second))
	fmt.Printf("  input level          %.1f dBFS%s\n", dbfs(mono), quietWarning(mono))
	fmt.Printf("  output level         %.1f dBFS\n", dbfs(out))
	fmt.Printf("  packets              %d\n", len(packets))
	fmt.Printf("  actual bitrate       %.1f kbit/s\n", kbps(packets, audioLen))

	// On room sound, waveform likeness is not the right question: Opus is a
	// perceptual codec and reconstructs noise instead of copying it, so a
	// perfect stream can have a low correlation. What has to match is how the
	// energy is distributed across the bands.
	fmt.Printf("  spectral deviation\n")
	printSpectralDiff(mono, out)

	fmt.Printf("\nencoding cost\n")
	printQuantiles(took)
	fmt.Printf("  fraction of a core   %.2f%%  (%v of compute for %v of audio)\n",
		100*float64(wall)/float64(audioLen), wall.Round(time.Millisecond), audioLen.Round(time.Second))

	return loudTest(enc, mono)
}

// loudTest repeats the test on the same audio raised to the level of crying.
//
// It tells apart two explanations for the deviation seen above. If it comes
// from a quiet room sitting at the codec's lower limit — where Opus synthesises
// noise instead of copying nothing, by design — then raising the level must
// make it disappear. If it stays, it is not the level: it is our wiring, and
// that has to be looked into before going any further.
//
// The gain is raised rather than making noise in the house: it is one in the
// morning.
func loudTest(enc audiocodec.Encoder, mono []int16) error {
	const gainDB = 25
	gain := math.Pow(10, gainDB/20.0)

	loud := make([]int16, len(mono))
	for i, v := range mono {
		loud[i] = clamp16(float64(v) * gain)
	}

	packets, _, _, err := encodeAll(enc, loud)
	if err != nil {
		return err
	}
	out, err := decodeAll(packets)
	if err != nil {
		return err
	}

	fmt.Printf("\nsame room, raised by %d dB (the level of crying)\n", gainDB)
	fmt.Printf("  input level          %.1f dBFS\n", dbfs(loud))
	fmt.Printf("  output level         %.1f dBFS\n", dbfs(out))
	fmt.Printf("  spectral deviation\n")
	printSpectralDiff(loud, out)
	return nil
}

func clamp16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// ---------- measurements ----------

// encodeAll encodes all the PCM and also returns the timings.
//
// The total is taken with a single reading of the clock around the loop, and
// not by summing the durations of the individual calls: on Windows the timer's
// resolution is coarse compared with an encode that lasts fractions of a
// millisecond, and the sum of a thousand rounded measurements is well out. The
// individual durations stay useful for the quantiles, where the tail is what
// matters.
func encodeAll(enc audiocodec.Encoder, pcm []int16) ([][]byte, []time.Duration, time.Duration, error) {
	frames := len(pcm) / frameSamples()
	packets := make([][]byte, 0, frames)
	took := make([]time.Duration, 0, frames)

	wallStart := time.Now()
	for i := range frames {
		frame := pcm[i*frameSamples() : (i+1)*frameSamples()]
		t0 := time.Now()
		pkt, err := enc.Encode(frame)
		took = append(took, time.Since(t0))
		if err != nil {
			return nil, nil, 0, err
		}
		if pkt != nil {
			packets = append(packets, pkt)
		}
	}
	return packets, took, time.Since(wallStart), nil
}

func decodeAll(packets [][]byte) ([]int16, error) {
	dec, err := pionopus.NewDecoderWithOutput((*rate), audiocodec.Channels)
	if err != nil {
		return nil, fmt.Errorf("decoder pion/opus: %w", err)
	}
	out := make([]int16, 0, len(packets)*frameSamples())
	buf := make([]int16, frameSamples()*2)
	for _, pkt := range packets {
		n, err := dec.DecodeToInt16(pkt, buf)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		out = append(out, buf[:n]...)
	}
	return out, nil
}

// bestAlignment looks for the offset that maximises the likeness between the
// two signals.
//
// It is needed because Opus introduces a delay of its own: comparing sample by
// sample without aligning, a perfect stream would look like noise.
func bestAlignment(a, b []int16) (int, float64) {
	const maxLag = 2048
	best, bestCorr := 0, -2.0
	for lag := 0; lag <= maxLag; lag++ {
		if c := correlation(a, b, lag); c > bestCorr {
			best, bestCorr = lag, c
		}
	}
	return best, bestCorr
}

func correlation(a, b []int16, lag int) float64 {
	n := min(len(a), len(b)-lag)
	if n <= 0 {
		return -2
	}
	var sa, sb, saa, sbb, sab float64
	for i := range n {
		x, y := float64(a[i]), float64(b[i+lag])
		sa += x
		sb += y
		saa += x * x
		sbb += y * y
		sab += x * y
	}
	fn := float64(n)
	cov := sab/fn - (sa/fn)*(sb/fn)
	va := saa/fn - (sa/fn)*(sa/fn)
	vb := sbb/fn - (sb/fn)*(sb/fn)
	if va <= 0 || vb <= 0 {
		return -2
	}
	return cov / math.Sqrt(va*vb)
}

// snr measures how much of the original signal's energy survives the encoding.
//
// On a perceptual codec it is not a mark for quality: Opus deliberately discards
// what cannot be heard, and modest values are normal. It serves to tell a
// correct stream from a ruined one.
func snr(a, b []int16, lag int) float64 {
	n := min(len(a), len(b)-lag)
	if n <= 0 {
		return math.NaN()
	}
	var sig, noise float64
	for i := range n {
		x, y := float64(a[i]), float64(b[i+lag])
		sig += x * x
		noise += (x - y) * (x - y)
	}
	if noise == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(sig/noise)
}

// bandEnergies measures the energy in logarithmic bands, in dB.
//
// A DFT over a few frequencies is used instead of a full transform: the bands of
// interest are sixteen, and at that point computing them directly costs less
// than transforming everything.
func bandEnergies(pcm []int16) []float64 {
	const (
		bands  = 16
		window = 2048
		f0, f1 = 100.0, 16000.0
	)
	energy := make([]float64, bands)
	windows := 0
	for start := 0; start+window <= len(pcm); start += window {
		seg := pcm[start : start+window]
		for b := range bands {
			freq := f0 * math.Pow(f1/f0, float64(b)/float64(bands-1))
			k := 2 * math.Pi * freq / float64(*rate)
			var re, im float64
			for i, v := range seg {
				x := float64(v) / 32768
				re += x * math.Cos(k*float64(i))
				im += x * math.Sin(k*float64(i))
			}
			energy[b] += (re*re + im*im) / float64(window*window)
		}
		windows++
	}
	if windows == 0 {
		return energy
	}
	for b := range energy {
		e := energy[b] / float64(windows)
		if e <= 0 {
			energy[b] = -200
			continue
		}
		energy[b] = 10 * math.Log10(e)
	}
	return energy
}

// printSpectralDiff compares band by band and judges only where there is signal.
//
// In a quiet room the bands more than 30 dB below the loudest contain almost
// nothing but the microphone's own noise floor: there a deviation of ten
// decibels is the difference between two silences, and averaging it in with the
// rest would falsify the verdict in both directions.
func printSpectralDiff(in, out []int16) {
	const floorBelowPeak = 30.0
	a, b := bandEnergies(in), bandEnergies(out)

	peak := math.Inf(-1)
	for _, v := range a {
		peak = math.Max(peak, v)
	}

	var sum, worst float64
	counted := 0
	for i := range a {
		freq := 100 * math.Pow(160, float64(i)/float64(len(a)-1))
		d := b[i] - a[i]
		mark := "  ·"
		if a[i] >= peak-floorBelowPeak {
			sum += math.Abs(d)
			worst = math.Max(worst, math.Abs(d))
			counted++
			mark = ""
		}
		fmt.Printf("    %6.0f Hz   in %6.1f   out %6.1f   %+5.1f dB%s\n", freq, a[i], b[i], d, mark)
	}
	if counted == 0 {
		fmt.Println("    no band with enough signal")
		return
	}
	mean := sum / float64(counted)
	note := "  ← the spectra match"
	if mean > 3 {
		note = "  ← TO BE INVESTIGATED"
	}
	fmt.Printf("    over %d bands within 30 dB of the loudest: mean %.1f dB, max %.1f dB%s\n",
		counted, mean, worst, note)
	fmt.Println("    (rows marked · are below that threshold and do not count)")
}

func printQuantiles(d []time.Duration) {
	if len(d) == 0 {
		return
	}
	s := append([]time.Duration(nil), d...)
	slices.Sort(s)
	q := func(p float64) time.Duration { return s[int(float64(len(s)-1)*p)] }
	fmt.Printf("  median %v · p95 %v · p99 %v · max %v\n",
		q(0.5).Round(time.Microsecond), q(0.95).Round(time.Microsecond),
		q(0.99).Round(time.Microsecond), s[len(s)-1].Round(time.Microsecond))
	fmt.Printf("  budget per frame     %v (the max uses %.1f%% of it)\n",
		audiocodec.FrameDuration, 100*float64(s[len(s)-1])/float64(audiocodec.FrameDuration))
}

func kbps(packets [][]byte, d time.Duration) float64 {
	total := 0
	for _, p := range packets {
		total += len(p)
	}
	if d <= 0 {
		return 0
	}
	return float64(total) * 8 / d.Seconds() / 1000
}

func dbfs(pcm []int16) float64 {
	if len(pcm) == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for _, v := range pcm {
		x := float64(v) / 32768
		sum += x * x
	}
	rms := math.Sqrt(sum / float64(len(pcm)))
	if rms == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(rms)
}

func quietWarning(pcm []int16) string {
	if dbfs(pcm) < -60 {
		return "  ← too quiet: the correlation below means nothing"
	}
	return ""
}

func verdict(corr float64) string {
	if corr > 0.9 {
		return "  ← the two implementations agree"
	}
	return "  ← TO BE INVESTIGATED"
}
