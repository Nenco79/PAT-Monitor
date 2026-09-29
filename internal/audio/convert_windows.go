package audio

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ToMonoS16 converts a PCM block from the endpoint's native format to 16-bit
// mono, and returns how many samples it wrote into dst.
//
// The downmix is the average of the channels, not their sum: summing, a
// four-capsule microphone hearing the same thing would clip.
//
// It does not resample, and that is not an oversight: Opus accepts five rates
// only, and a badly written resampler is a fault you can hear and cannot see in
// any measurement. If the endpoint delivers a rate that will not do, the caller
// has to know — see audiocodec.RateSupported.
func ToMonoS16(src []byte, f StreamFormat, dst []int16) (int, error) {
	bpf := f.BytesPerFrame()
	if bpf <= 0 {
		return 0, fmt.Errorf("audio: unconvertible format: %s", f)
	}
	frames := min(len(src)/bpf, len(dst))

	bps := f.BitsPerSample / 8
	for i := range frames {
		base := i * bpf
		var sum float64
		for c := 0; c < f.Channels; c++ {
			v, err := sampleAt(src[base+c*bps:], f)
			if err != nil {
				return 0, err
			}
			sum += v
		}
		dst[i] = SaturateS16(sum / float64(f.Channels) * 32767)
	}
	return frames, nil
}

// FirstChannelS16 extracts the first channel of a PCM block as 16-bit samples,
// and returns how many it wrote into dst. It is the form the analyser expects,
// for the instruments that judge a microphone's noise floor.
//
// One channel is taken rather than averaging them: averaging channels whose
// noise is uncorrelated would lower the level by about 3 dB and falsify the
// count of exact zeros, which is precisely the figure under examination. For
// the same reason the scale is 32768 and not ToMonoS16's 32767: a 16-bit
// sample comes back unchanged, so an exact zero stays one and a single LSB of
// noise is not rounded away.
func FirstChannelS16(src []byte, f StreamFormat, dst []int16) (int, error) {
	bpf := f.BytesPerFrame()
	if bpf <= 0 {
		return 0, fmt.Errorf("audio: unconvertible format: %s", f)
	}
	frames := min(len(src)/bpf, len(dst))
	for i := range frames {
		v, err := sampleAt(src[i*bpf:], f)
		if err != nil {
			return 0, err
		}
		x := v * 32768
		if !f.Float {
			// Floored, as an arithmetic shift would: truncating toward zero
			// turns every sample within one LSB below zero into an exact
			// zero, doubling the count this reader exists to report.
			x = math.Floor(x)
		}
		dst[i] = SaturateS16(x)
	}
	return frames, nil
}

// sampleAt reads a single sample and normalises it into [-1, 1].
func sampleAt(b []byte, f StreamFormat) (float64, error) {
	switch {
	case f.Float && f.BitsPerSample == 32:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), nil
	case f.Float && f.BitsPerSample == 64:
		return math.Float64frombits(binary.LittleEndian.Uint64(b)), nil
	case !f.Float && f.BitsPerSample == 16:
		return float64(int16(uint16(b[0])|uint16(b[1])<<8)) / 32768, nil
	case !f.Float && f.BitsPerSample == 24:
		// The 24-bit sample is packed: sign-extend it up to 32 bits.
		v := int32(uint32(b[0])<<8 | uint32(b[1])<<16 | uint32(b[2])<<24)
		return float64(v) / 2147483648, nil
	case !f.Float && f.BitsPerSample == 32:
		v := int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
		return float64(v) / 2147483648, nil
	}
	return 0, fmt.Errorf("audio: unconvertible samples: %s", f)
}

// SaturateS16 turns a sample already on the 16-bit scale into an int16,
// saturating instead of wrapping.
//
// Floating-point PCM can exceed unity, a gain can push a sample past full scale
// and a filter can overshoot the peak it was given, and a sample that wraps
// becomes a full-scale click of the opposite sign: far more audible than
// clipping. It is the one saturation of the capture path, so that the downmix,
// the gain and the analysis resampler cannot clip three different ways.
func SaturateS16(v float64) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

// The list of rates Opus accepts does not live here but in
// audiocodec.SupportedRates, where the encoder lives: written in two places, the
// two lists diverge sooner or later and whichever is updated second becomes a
// trap. A capture package has no business knowing who will compress its samples.
