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
	// a cry or a bark, which sit between 400 and 1600. Real frequency analysis
	// would say a great deal more and cost a great deal more: here it is enough
	// to know which decade we are in.
	CrossRate float64
}

// AnalyzeS16LE analyses a block of 16-bit little-endian interleaved mono PCM.
// Trailing odd bytes are ignored.
func AnalyzeS16LE(b []byte) Block {
	n := len(b) / 2
	if n == 0 {
		return Block{RMSdBFS: SilenceFloorDBFS, PeakdBFS: SilenceFloorDBFS}
	}

	var sum, sumSq float64
	var zeros, crossings int
	var peak int16
	prev := int16(0)
	for i := range n {
		v := int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
		if v == 0 {
			zeros++
		}
		// A sign change is only counted between non-zero samples: an exact zero
		// is not a crossing, and on a muted path — which delivers almost
		// nothing but zeros — it would produce one every two samples, that is,
		// the signature of a very high frequency signal in place of silence.
		if i > 0 && v != 0 && prev != 0 && (v > 0) != (prev > 0) {
			crossings++
		}
		if v != 0 {
			prev = v
		}
		if a := abs16(v); a > peak {
			peak = a
		}
		f := float64(v)
		sum += f
		sumSq += f * f
	}

	mean := sum / float64(n)
	// The variance is computed about the mean: any DC offset of the converter
	// must not be mistaken for noise.
	variance := sumSq/float64(n) - mean*mean
	if variance < 0 {
		variance = 0
	}

	return Block{
		Samples:   n,
		RMSdBFS:   toDBFS(math.Sqrt(sumSq/float64(n)) / FullScale),
		PeakdBFS:  toDBFS(float64(peak) / FullScale),
		Peak:      peak,
		ZeroRatio: float64(zeros) / float64(n),
		StdDevLSB: math.Sqrt(variance),
		CrossRate: float64(crossings) / float64(n),
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
type Accumulator struct {
	samples int
	sumSq   float64
	sum     float64
	zeros   int
	peak    int16
	// crossings and prev carry the sign-change count across the blocks.
	//
	// **A field left at zero is not a missing value, it is a measurement.**
	// `Result` used to build a Block with four of its five numbers measured and
	// `CrossRate` at its zero value — which does not read as "not computed", it
	// reads as **0 Hz**. Handed to `Sound.Feed` that is below the band, so the
	// gate would refuse everything, silently and for ever: nothing does that
	// today, which made it a trap rather than a defect. It is the family this
	// project names twice over — *zero dBFS is full scale*, *a meter that cannot
	// measure does not draw silence* — and the cheapest answer is not to label
	// the zero but to remove it.
	//
	// `prev` is the last non-zero sample of the previous block, so a crossing
	// that falls on a block boundary is counted once and not lost: the
	// accumulation is one stream, not a row of independent windows.
	crossings int
	prev      int16
}

// Add takes in a block of PCM.
func (a *Accumulator) Add(b []byte) Block {
	blk := AnalyzeS16LE(b)
	n := len(b) / 2
	// **The crossings inside the block are taken from the block**, not counted a
	// second time here: what does and does not count as a sign change is written
	// in AnalyzeS16LE and must not be restated, or the two copies diverge at the
	// first correction with nothing failing. CrossRate is crossings over these
	// same n samples, so the count comes back exactly.
	a.crossings += int(math.Round(blk.CrossRate * float64(n)))
	for i := range n {
		v := int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
		if v == 0 {
			a.zeros++
		}
		// The one crossing the block cannot see is the join with the block
		// before: AnalyzeS16LE starts afresh every time and never counts its
		// first sample, while the accumulation is one stream. It is the only
		// place the rule is repeated, and it is repeated for one pair.
		if i == 0 && v != 0 && a.prev != 0 && (v > 0) != (a.prev > 0) {
			a.crossings++
		}
		if v != 0 {
			a.prev = v
		}
		if abs := abs16(v); abs > a.peak {
			a.peak = abs
		}
		f := float64(v)
		a.sum += f
		a.sumSq += f * f
	}
	a.samples += n
	return blk
}

// Result returns the cumulative statistics.
func (a *Accumulator) Result() Block {
	if a.samples == 0 {
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
	mean := a.sum / float64(a.samples)
	variance := a.sumSq/float64(a.samples) - mean*mean
	if variance < 0 {
		variance = 0
	}
	return Block{
		Samples:   a.samples,
		RMSdBFS:   toDBFS(math.Sqrt(a.sumSq/float64(a.samples)) / FullScale),
		PeakdBFS:  toDBFS(float64(a.peak) / FullScale),
		Peak:      a.peak,
		ZeroRatio: float64(a.zeros) / float64(a.samples),
		StdDevLSB: math.Sqrt(variance),
		CrossRate: float64(a.crossings) / float64(a.samples),
	}
}
