package gguf

import (
	"encoding/binary"
	"fmt"
	"math"
)

// F32 returns the tensor as float32, converting where needed.
//
// **It allocates on every call, on purpose.** A model is loaded once and its
// weights stay; keeping a cache inside the tensor would mean two goroutines
// reading the same weight contend on a map, and on this program locks hidden
// inside things that look read-only are a measured way to lose an audio
// capture.
func (t *Tensor) F32() ([]float32, error) {
	n := t.Count()
	out := make([]float32, n)
	switch t.Type {
	case TypeF32:
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(t.data[i*4:]))
		}
	case TypeF16:
		for i := range out {
			out[i] = f16(binary.LittleEndian.Uint16(t.data[i*2:]))
		}
	case TypeQ8_0:
		for b := 0; b*q8BlockValues < n; b++ {
			blk := t.data[b*q8BlockBytes:]
			s := f16(binary.LittleEndian.Uint16(blk))
			for j := 0; j < q8BlockValues; j++ {
				out[b*q8BlockValues+j] = s * float32(int8(blk[2+j]))
			}
		}
	default:
		return nil, fmt.Errorf("gguf: cannot read %s as float32", t.Type)
	}
	return out, nil
}

// f16 converts from IEEE 754 half precision.
//
// **The cases that matter are the denormals and infinity**, not the normal one.
// Written as a multiplication by a power of two, the conversion goes wrong
// silently on exactly the small values — which in a quantised network are the
// majority of the weights, not an exception. Here the number is rebuilt from
// its fields, which takes longer to read and leaves no case uncovered.
func f16(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1F
	frac := uint32(h & 0x03FF)

	switch exp {
	case 0:
		if frac == 0 {
			// Zero, with its sign.
			return math.Float32frombits(sign)
		}
		// Denormal: it is normalised by shifting until the implicit bit falls
		// out, and the exponent follows **how many** shifts that took. Written
		// with a counter starting at -1 the count is off by two, and it is off
		// only here: the normal weights stay right and the small ones come out
		// at half their value.
		k := uint32(0)
		for frac&0x0400 == 0 {
			frac <<= 1
			k++
		}
		frac &= 0x03FF
		return math.Float32frombits(sign | (113-k)<<23 | frac<<13)
	case 0x1F:
		// Infinity or NaN: the exponent goes to all ones and the mantissa is
		// kept, otherwise a NaN would turn into an infinity.
		return math.Float32frombits(sign | 0xFF<<23 | frac<<13)
	}
	return math.Float32frombits(sign | (exp-15+127)<<23 | frac<<13)
}
