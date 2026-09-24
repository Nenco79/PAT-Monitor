package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// loadWAV reads a WAV and delivers it in mono, samples in [-1, 1].
//
// **The public datasets are not uniform, and that is the first place one comes
// to a halt.** ESC-50 is all mono 16-bit at 44.1 kHz; UrbanSound8K keeps
// Freesound's original format, that is 8, 16, 24 and 32 bits, mono and stereo,
// from 8 to 192 kHz. A reader that only knew about 16 bits would silently skip
// a slice of the dataset and make its recall lower, which would read as a
// defect of the model.
//
// It returns the waveform and its rate: **the caller does the resampling**,
// because that is where the required rate is known and where the filter bank
// can be kept from one clip to the next.
func loadWAV(path string) ([]float32, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("%s: not a WAV", path)
	}
	i := 12
	var rate, channels, bits, format int
	for i+8 <= len(b) {
		id := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4 : i+8]))
		if n < 0 || i+8 > len(b) {
			break
		}
		body := b[i+8:]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, fmt.Errorf("%s: fmt chunk is %d bytes", path, len(body))
			}
			format = int(binary.LittleEndian.Uint16(body[0:2]))
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
			// **The "extensible" format is not a format: it is an
			// indirection.** The real code sits in the first two bytes of the
			// subformat GUID, at the end of the chunk. Ignoring it and refusing
			// 0xFFFE means throwing away **45%** of UrbanSound8K — measured —
			// and nobody notices, because those clips come out as skipped rows
			// among thousands of others and the recall that follows looks like
			// the model's.
			if format == formatExtensible {
				if len(body) < 40 {
					return nil, 0, fmt.Errorf("%s: extensible fmt chunk is %d bytes", path, len(body))
				}
				format = int(binary.LittleEndian.Uint16(body[24:26]))
			}
		case "data":
			if channels == 0 || rate == 0 {
				return nil, 0, fmt.Errorf("%s: data before fmt", path)
			}
			if n > len(body) {
				n = len(body)
			}
			w, err := decodePCM(body[:n], channels, bits, format)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: %w", path, err)
			}
			return w, rate, nil
		}
		i += 8 + n + n%2
	}
	return nil, 0, fmt.Errorf("%s: no data chunk", path)
}

// The format codes we know how to read, plus the indirection. The others — A-law,
// mu-law, ADPCM — **are refused instead of being interpreted as PCM**: what would
// come out is noise with the air of a signal, which is worse than a skipped clip.
const (
	formatPCM        = 1
	formatFloat      = 3
	formatExtensible = 0xFFFE
)

func decodePCM(b []byte, channels, bits, format int) ([]float32, error) {
	per := bits / 8
	if per == 0 {
		return nil, fmt.Errorf("%d bits per sample", bits)
	}
	if format == formatFloat && bits != 32 {
		return nil, fmt.Errorf("floating point at %d bits", bits)
	}
	if format != formatPCM && format != formatFloat {
		return nil, fmt.Errorf("format %d is not PCM", format)
	}
	frames := len(b) / per / channels
	out := make([]float32, frames)
	for k := range frames {
		var sum float64
		for c := range channels {
			o := (k*channels + c) * per
			switch {
			case format == formatFloat:
				sum += float64(math.Float32frombits(binary.LittleEndian.Uint32(b[o:])))
			case bits == 8:
				// **The 8 bits of a WAV are unsigned**, and centring them wrongly
				// gives a DC offset of half full scale that carries into everything.
				sum += (float64(b[o]) - 128) / 128
			case bits == 16:
				sum += float64(int16(binary.LittleEndian.Uint16(b[o:]))) / 32768
			case bits == 24:
				v := int32(b[o]) | int32(b[o+1])<<8 | int32(b[o+2])<<16
				if v&0x800000 != 0 {
					v |= ^0xFFFFFF // the sign is extended by hand: there is no int24
				}
				sum += float64(v) / 8388608
			case bits == 32:
				sum += float64(int32(binary.LittleEndian.Uint32(b[o:]))) / 2147483648
			default:
				return nil, fmt.Errorf("%d bits per sample", bits)
			}
		}
		out[k] = float32(sum / float64(channels))
	}
	return out, nil
}
