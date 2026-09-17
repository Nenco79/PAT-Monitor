package gguf

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The metadata value types, from the format specification.
const (
	kUint8 uint32 = iota
	kInt8
	kUint16
	kInt16
	kUint32
	kInt32
	kFloat32
	kBool
	kString
	kArray
	kUint64
	kInt64
	kFloat64
)

// cursor reads forward, declaring when the space runs out instead of panicking
// on a slice: a truncated file is something that happens, and it has to be
// reported where the file it belongs to can still be named.
type cursor struct {
	b   []byte
	p   int
	err error
}

// need says whether n more bytes are there, and **the comparison is a
// subtraction**.
//
// Written `c.p+n > len(c.b)`, which is the form that comes to mind, a length
// close to the maximum of an integer makes the sum overflow negative: the check
// passes, and the slice taken right after panics. Measured with a tensor name
// 0x7FFFFFFFFFFFFFFF bytes long — that is, a file someone can build, or an
// interrupted copy can produce. Here c.p never exceeds len(c.b), so the
// difference never overflows.
func (c *cursor) need(n int) bool {
	if c.err != nil {
		return false
	}
	if n < 0 || n > len(c.b)-c.p {
		c.err = fmt.Errorf("gguf: truncated at byte %d, %d more needed", c.p, n)
		return false
	}
	return true
}

func (c *cursor) u8() uint8 {
	if !c.need(1) {
		return 0
	}
	v := c.b[c.p]
	c.p++
	return v
}

func (c *cursor) u16() uint16 {
	if !c.need(2) {
		return 0
	}
	v := binary.LittleEndian.Uint16(c.b[c.p:])
	c.p += 2
	return v
}

func (c *cursor) u32() uint32 {
	if !c.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(c.b[c.p:])
	c.p += 4
	return v
}

func (c *cursor) u64() uint64 {
	if !c.need(8) {
		return 0
	}
	v := binary.LittleEndian.Uint64(c.b[c.p:])
	c.p += 8
	return v
}

func (c *cursor) str() string {
	n := int(c.u64())
	if n < 0 || !c.need(n) {
		return ""
	}
	s := string(c.b[c.p : c.p+n])
	c.p += n
	return s
}

// value reads a metadata value of the declared type. Lists can nest by
// specification, so this recurses.
func (c *cursor) value(t uint32) any {
	switch t {
	case kUint8:
		return c.u8()
	case kInt8:
		return int8(c.u8())
	case kUint16:
		return c.u16()
	case kInt16:
		return int16(c.u16())
	case kUint32:
		return c.u32()
	case kInt32:
		return int32(c.u32())
	case kFloat32:
		return math.Float32frombits(c.u32())
	case kBool:
		return c.u8() != 0
	case kString:
		return c.str()
	case kArray:
		et := c.u32()
		n := int(c.u64())
		// An absurd count in a corrupt file would allocate everything
		// available before noticing: the cap is what would fit in the file even
		// with one-byte elements.
		if n < 0 || n > len(c.b) {
			c.err = fmt.Errorf("gguf: array of %d elements in a %d byte file", n, len(c.b))
			return nil
		}
		out := make([]any, 0, n)
		for i := 0; i < n && c.err == nil; i++ {
			out = append(out, c.value(et))
		}
		return out
	case kUint64:
		return c.u64()
	case kInt64:
		return int64(c.u64())
	case kFloat64:
		return math.Float64frombits(c.u64())
	}
	c.err = fmt.Errorf("gguf: unknown metadata type %d", t)
	return nil
}

// Parse reads a GGUF already in memory.
//
// **It takes bytes and not a path**, and that is not a convenience: what gets
// shipped will arrive from go:embed, so it is in memory already, and an API
// asking for a file would force writing it to disk to read it back.
func Parse(b []byte) (*File, error) {
	c := &cursor{b: b}
	if !c.need(4) || string(b[:4]) != "GGUF" {
		return nil, fmt.Errorf("gguf: not a GGUF file")
	}
	c.p = 4
	f := &File{Version: c.u32(), Meta: map[string]any{}, tensors: map[string]*Tensor{}, Alignment: 32}
	if f.Version != 2 && f.Version != 3 {
		return nil, fmt.Errorf("gguf: version %d is not one we read", f.Version)
	}
	nTensor := int(c.u64())
	nKV := int(c.u64())
	if c.err != nil {
		return nil, c.err
	}
	if nTensor < 0 || nTensor > len(b)/8 || nKV < 0 || nKV > len(b)/8 {
		return nil, fmt.Errorf("gguf: %d tensors and %d metadata in a %d byte file", nTensor, nKV, len(b))
	}

	for i := 0; i < nKV && c.err == nil; i++ {
		k := c.str()
		f.Meta[k] = c.value(c.u32())
	}
	if c.err != nil {
		return nil, c.err
	}
	if a, err := f.Int("general.alignment"); err == nil && a > 0 {
		f.Alignment = a
	}

	// The tensor descriptions carry an offset **relative to the start of the
	// data**, which begins after the aligned header: adding it to the current
	// position without aligning gives tensors shifted a little, that is,
	// weights that are plausible and wrong.
	type desc struct {
		name string
		dims []uint64
		typ  Type
		off  uint64
	}
	descs := make([]desc, 0, nTensor)
	for i := 0; i < nTensor && c.err == nil; i++ {
		d := desc{name: c.str()}
		nd := int(c.u32())
		if nd < 0 || nd > 4 {
			return nil, fmt.Errorf("gguf: tensor %q has %d dimensions", d.name, nd)
		}
		// **The product is checked as it is built**, not the dimensions one by
		// one.
		//
		// Further on that product overflows silently, and it does not give a
		// huge number: it gives **zero**. `1<<62` times `32` is zero in a
		// 64-bit integer, and out comes a tensor declaring it has no values at
		// all instead of an error. Checking each dimension against the file
		// length covers that case — but four dimensions of 262144, each smaller
		// than a 262 KB file, make 2^72, which is zero again: **that guards the
		// case, not the property.**
		//
		// No tensor can hold more values than the file has bytes, because the
		// most compact type we read spends more than one on each: the product
		// stops there, and so never comes near overflowing.
		d.dims = make([]uint64, nd)
		count := uint64(1)
		for j := range d.dims {
			d.dims[j] = c.u64()
			if d.dims[j] > uint64(len(b)) {
				return nil, fmt.Errorf("gguf: tensor %q has a dimension of %d in a %d byte file",
					d.name, d.dims[j], len(b))
			}
			count *= d.dims[j]
			if count > uint64(len(b)) {
				return nil, fmt.Errorf("gguf: tensor %q has %v, which is more values than a %d byte file holds",
					d.name, d.dims, len(b))
			}
		}
		d.typ = Type(c.u32())
		d.off = c.u64()
		descs = append(descs, d)
	}
	if c.err != nil {
		return nil, c.err
	}

	base := (c.p + f.Alignment - 1) / f.Alignment * f.Alignment
	for _, d := range descs {
		t := &Tensor{Name: d.name, Dims: d.dims, Type: d.typ}
		n, err := byteSize(d.typ, t.Count())
		if err != nil {
			return nil, fmt.Errorf("gguf: tensor %q: %w", d.name, err)
		}
		from := base + int(d.off)
		if from < 0 || from+n > len(b) {
			return nil, fmt.Errorf("gguf: tensor %q runs from %d to %d, past the end of a %d byte file",
				d.name, from, from+n, len(b))
		}
		t.data = b[from : from+n]
		f.tensors[d.name] = t
	}
	return f, nil
}

// The shape of a Q8_0 block: one half-precision scale and thirty-two signed
// integers. **Q8_1 is the one with two scales and thirty-six bytes**, and we do
// not read it: taking that shape for this one reads each scale out of the
// previous block's weights, which is noise rather than a less accurate model.
const (
	q8BlockValues = 32
	q8BlockBytes  = 2 + q8BlockValues
)

func byteSize(t Type, n int) (int, error) {
	switch t {
	case TypeF32:
		return n * 4, nil
	case TypeF16:
		return n * 2, nil
	case TypeQ8_0:
		if n%q8BlockValues != 0 {
			return 0, fmt.Errorf("%d values do not divide into blocks of %d", n, q8BlockValues)
		}
		return n / q8BlockValues * q8BlockBytes, nil
	}
	return 0, fmt.Errorf("we do not read %s", t)
}
