// Package detect analyses the analysis streams the pipeline produces: the audio
// level (for cry detection and for noticing a mute microphone) and the movement
// in the video.
package detect

import (
	"fmt"
	"math"
)

// FullScale is the maximum amplitude of a 16-bit PCM sample.
const FullScale = 32768.0

// SilenceFloorDBFS is the level below which a block counts as silence for every
// practical purpose.
const SilenceFloorDBFS = -99.0

// Block is the result of analysing one window of PCM samples.
type Block struct {
	Samples int
	// RMSdBFS is the effective level, the quantity that follows the perception
	// of loudness; it is the one the cry thresholds are tuned on.
	RMSdBFS float64
	// PeakdBFS is the largest sample in the window.
	PeakdBFS float64
	Peak     int16
	// ZeroRatio is the fraction of samples exactly equal to zero. It is what
	// tells a working microphone from a muted one: the noise floor of a real
	// ADC wobbles and produces very few exact zeros, while a muted path, or one
	// behind a noise gate, produces almost nothing else.
	ZeroRatio float64
	// StdDevLSB is the standard deviation in quantisation units: it measures
	// how "alive" the noise floor is.
	StdDevLSB float64
	// CrossRate is the fraction of samples where the signal changes sign.
	//
	// It is the only frequency information we allow ourselves, and it costs one
	// comparison per sample: multiplied by the sample rate and divided by two
	// it gives an estimate of the dominant component. It tells a thud — a door,
	// a footstep, the dog jumping off the sofa, all energy below 200 Hz — from
	// a cry or a bark, which sit between 120 and 1640 Hz — see Sound, which
	// holds that band. Real frequency analysis
	// would say a great deal more and cost a great deal more: here it is enough
	// to know which decade we are in.
	CrossRate float64
}

// AnalyzeS16 analyses a block of 16-bit mono PCM.
func AnalyzeS16(pcm []int16) Block { return sumsOf(pcm).block() }

// sums are the raw totals of a stretch of samples, before any division: what
// one block and a whole accumulation have in common, so that both are built by
// the same walk and turned into a Block by the same arithmetic.
//
// **They are integers, and that makes them exact.** A square is at most 2^30,
// so sumSq overflows int64 only after 2^33 samples, about fifty hours at 48
// kHz; the float64 totals they replace were exact only up to about 2^23
// samples at full scale.
type sums struct {
	n, zeros int
	// crossings counts the sign changes between non-zero samples.
	//
	// **A field left at zero is not a missing value, it is a measurement.**
	// `Result` used to build a Block with four of its five numbers measured and
	// `CrossRate` at its zero value — which does not read as "not computed", it
	// reads as **0 Hz**. Handed to `Sound.Feed` that is below the band, so the
	// gate would refuse everything, silently and for ever: nothing does that
	// today, which made it a trap rather than a defect. It is the family this
	// project names twice over — *zero dBFS is full scale*, *a meter that cannot
	// measure does not draw silence* — and the cheapest answer is not to label
	// the zero but to remove it: every Block is built from a sums, and a sums
	// always has its crossings.
	crossings  int
	sum, sumSq int64
	peak       int16
	// first and last are the first and the last non-zero sample, zero if there
	// is none. They are what join needs to count the crossing that falls on a
	// boundary, and nothing else reads them.
	first, last int16
}

// sumsOf walks the samples, and it is the only place they are walked.
func sumsOf(pcm []int16) sums {
	s := sums{n: len(pcm)}
	for _, v := range pcm {
		if v == 0 {
			s.zeros++
		} else {
			// A sign change is only counted between non-zero samples: an exact
			// zero is not a crossing, and on a muted path — which delivers
			// almost nothing but zeros — it would produce one every two
			// samples, that is, the signature of a very high frequency signal
			// in place of silence.
			if s.last != 0 && (v > 0) != (s.last > 0) {
				s.crossings++
			}
			if s.first == 0 {
				s.first = v
			}
			s.last = v
		}
		if a := abs16(v); a > s.peak {
			s.peak = a
		}
		s.sum += int64(v)
		s.sumSq += int64(v) * int64(v)
	}
	return s
}

// join appends b, which follows a in the stream.
//
// The one crossing neither side can see is the one on the boundary: each block
// is walked afresh and never counts its first non-zero sample, while the
// accumulation is one stream, not a row of independent windows — so it is
// counted here, once, and not lost. **It is the first non-zero sample, not the
// first sample**: a block opening on an exact zero used to skip the join
// altogether, and gated or quiet signals are the ones full of zeros.
func (a *sums) join(b sums) {
	if a.last != 0 && b.first != 0 && (a.last > 0) != (b.first > 0) {
		a.crossings++
	}
	if a.first == 0 {
		a.first = b.first
	}
	if b.last != 0 {
		a.last = b.last
	}
	a.n += b.n
	a.zeros += b.zeros
	a.crossings += b.crossings
	a.sum += b.sum
	a.sumSq += b.sumSq
	a.peak = max(a.peak, b.peak)
}

// block turns the totals into the Block's numbers.
func (s sums) block() Block {
	if s.n == 0 {
		// **Samples is the field that says "nothing was measured"**, and it is
		// read: `Sound.Feed` opens with `if b.Samples == 0 { return s.state(now) }`
		// and returns before it ever looks at the crossing rate, and pat-capture
		// gates on `Result().Samples > 0`.
		//
		// So the zero left in CrossRate here is not the trap it looks like. The
		// levels do need care — they are set to the silence floor because zero
		// dBFS is full scale, which would read as "blaring" — while zero
		// crossings is simply what silence has, and the one consumer that could
		// misread it has already returned. The review recorded the opposite and
		// the correction is worth keeping: the danger was in the level, never in
		// this field.
		return Block{RMSdBFS: SilenceFloorDBFS, PeakdBFS: SilenceFloorDBFS}
	}
	n := float64(s.n)
	sumSq := float64(s.sumSq)
	mean := float64(s.sum) / n
	// The variance is computed about the mean: any DC offset of the converter
	// must not be mistaken for noise.
	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0
	}

	return Block{
		Samples:   s.n,
		RMSdBFS:   toDBFS(math.Sqrt(sumSq/n) / FullScale),
		PeakdBFS:  toDBFS(float64(s.peak) / FullScale),
		Peak:      s.peak,
		ZeroRatio: float64(s.zeros) / n,
		StdDevLSB: math.Sqrt(variance),
		CrossRate: float64(s.crossings) / n,
	}
}

func toDBFS(ratio float64) float64 {
	if ratio <= 0 {
		return SilenceFloorDBFS
	}
	db := 20 * math.Log10(ratio)
	if db < SilenceFloorDBFS {
		return SilenceFloorDBFS
	}
	return db
}

func abs16(v int16) int16 {
	if v < 0 {
		if v == math.MinInt16 {
			return math.MaxInt16
		}
		return -v
	}
	return v
}

// MicHealth is the verdict on a microphone's state.
type MicHealth int

const (
	// MicOK: the signal has a plausible noise floor.
	MicOK MicHealth = iota
	// MicDigitalSilence: the path is delivering zeros. A microphone muted in
	// hardware, disabled, or a noise gate cutting everything.
	MicDigitalSilence
	// MicSuspiciouslyQuiet: there is a noise floor, but so low as to suggest
	// zeroed gain or very aggressive noise suppression.
	MicSuspiciouslyQuiet
)

// The codes the microphone's state travels through the API as.
//
// **They are codes and not sentences, and the difference was worth a latent
// fault.** The state travels in JSON to the page and to the tray, and there
// somebody has to be able to ask "is it digital silence?". While the answer was
// an Italian sentence, that comparison lived in three places — trayStatus,
// app.js, onboarding.js — and translating the interface would have made it
// false **with no error anywhere**: the alert about a microphone delivering
// zeros, that is, the most treacherous fault a baby monitor can have, would
// have switched itself off in silence.
//
// The rule that remains: **codes travel through the API, words belong only at
// the edges.** The sentences to show are chosen by whoever draws the page, who
// is also the only one who knows what language they are speaking.
const (
	MicCodeOK             = "ok"
	MicCodeDigitalSilence = "digital-silence"
	MicCodeQuiet          = "quiet"
	MicCodeUnknown        = "unknown"
)

// Code is the verdict in stable form, to put in JSON and to compare against.
func (h MicHealth) Code() string {
	switch h {
	case MicOK:
		return MicCodeOK
	case MicDigitalSilence:
		return MicCodeDigitalSilence
	case MicSuspiciouslyQuiet:
		return MicCodeQuiet
	}
	return MicCodeUnknown
}

// String is the verdict spelled out, for the log and for the diagnostic tools.
// **It does not cross the API**: whoever has to decide something uses Code.
func (h MicHealth) String() string {
	switch h {
	case MicOK:
		return "working"
	case MicDigitalSilence:
		return "digital silence"
	case MicSuspiciouslyQuiet:
		return "suspiciously quiet"
	}
	return "unknown"
}

// The verdict's thresholds. Derived from how a real ADC behaves: the floor of a
// working microphone wobbles by at least a few quantisation units and touches
// exact zero only rarely.
const (
	zeroRatioMuted   = 0.60 // above: the path is dead
	stdDevMinLSB     = 1.0  // below: no noise floor worthy of the name
	quietPeakDBFSMax = -60.0
)

// Health judges the microphone's state from the aggregated statistics.
//
// The point is to tell apart two situations that from the dB alone look
// identical: a silent room with a working microphone, and a microphone
// delivering nothing. The difference is in the noise floor, not in the level.
func (b Block) Health() MicHealth {
	switch {
	case b.ZeroRatio >= zeroRatioMuted || b.StdDevLSB < stdDevMinLSB:
		return MicDigitalSilence
	case b.PeakdBFS < quietPeakDBFSMax:
		return MicSuspiciouslyQuiet
	default:
		return MicOK
	}
}

// Explain describes in one line why the verdict is what it is, for diagnostics.
func (b Block) Explain() string {
	return fmt.Sprintf(
		"RMS %.1f dBFS, peak %.1f dBFS (%d LSB), exact zeros %.1f%%, floor %.2f LSB -> %s",
		b.RMSdBFS, b.PeakdBFS, b.Peak, b.ZeroRatio*100, b.StdDevLSB, b.Health())
}

// Accumulator aggregates several blocks to form a verdict over a long window,
// where the instantaneous values would be too noisy.
type Accumulator struct{ s sums }

// Add takes in a block of PCM, and returns that block's own analysis.
func (a *Accumulator) Add(pcm []int16) Block {
	b := sumsOf(pcm)
	a.s.join(b)
	return b.block()
}

// Result returns the cumulative statistics.
func (a *Accumulator) Result() Block { return a.s.block() }
