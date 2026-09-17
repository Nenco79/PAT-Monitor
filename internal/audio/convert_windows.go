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
	frames := len(src) / bpf
	if frames > len(dst) {
		frames = len(dst)
	}

	bps := f.BitsPerSample / 8
	for i := 0; i < frames; i++ {
		base := i * bpf
		var sum float64
		for c := 0; c < f.Channels; c++ {
			v, err := sampleAt(src[base+c*bps:], f)
			if err != nil {
				return 0, err
			}
			sum += v
		}
		dst[i] = clampToS16(sum / float64(f.Channels))
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

// clampToS16 saturates instead of wrapping.
//
// Floating-point PCM can exceed unity, and a sample that wraps becomes a
// full-scale click: far more audible than clipping.
func clampToS16(v float64) int16 {
	const peak = 32767
	s := v * peak
	if s > peak {
		return peak
	}
	if s < -peak-1 {
		return -peak - 1
	}
	return int16(s)
}

// The list of rates Opus accepts does not live here but in
// audiocodec.SupportedRates, where the encoder lives: written in two places, the
// two lists diverge sooner or later and whichever is updated second becomes a
// trap. A capture package has no business knowing who will compress its samples.
