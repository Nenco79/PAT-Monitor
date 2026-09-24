package gguf

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
)

// **The test builds its own file.** A real GGUF lives in workbench/, weighs eleven
// megabytes and is not in the repository: a test that went looking for it on
// the disk of whoever runs it would pass here and fail everywhere else, which
// is the quietest way of testing nothing.
type builder struct {
	meta  []byte
	nKV   int
	descs []byte
	nT    int
	data  []byte
}

func (b *builder) u32(v uint32) []byte {
	x := make([]byte, 4)
	binary.LittleEndian.PutUint32(x, v)
	return x
}

func (b *builder) u64(v uint64) []byte {
	x := make([]byte, 8)
	binary.LittleEndian.PutUint64(x, v)
	return x
}

func (b *builder) str(s string) []byte {
	return append(b.u64(uint64(len(s))), s...)
}

func (b *builder) kvU32(k string, v uint32) {
	b.meta = append(b.meta, b.str(k)...)
	b.meta = append(b.meta, b.u32(kUint32)...)
	b.meta = append(b.meta, b.u32(v)...)
	b.nKV++
}

func (b *builder) kvF32(k string, v float32) {
	b.meta = append(b.meta, b.str(k)...)
	b.meta = append(b.meta, b.u32(kFloat32)...)
	b.meta = append(b.meta, b.u32(math.Float32bits(v))...)
	b.nKV++
}

func (b *builder) kvStrings(k string, vs ...string) {
	b.meta = append(b.meta, b.str(k)...)
	b.meta = append(b.meta, b.u32(kArray)...)
	b.meta = append(b.meta, b.u32(kString)...)
	b.meta = append(b.meta, b.u64(uint64(len(vs)))...)
	for _, v := range vs {
		b.meta = append(b.meta, b.str(v)...)
	}
	b.nKV++
}

func (b *builder) tensor(name string, dims []uint64, t Type, raw []byte) {
	b.descs = append(b.descs, b.str(name)...)
	b.descs = append(b.descs, b.u32(uint32(len(dims)))...)
	for _, d := range dims {
		b.descs = append(b.descs, b.u64(d)...)
	}
	b.descs = append(b.descs, b.u32(uint32(t))...)
	b.descs = append(b.descs, b.u64(uint64(len(b.data)))...)
	b.data = append(b.data, raw...)
	b.nT++
}

func (b *builder) bytes() []byte {
	out := append([]byte("GGUF"), b.u32(3)...)
	out = append(out, b.u64(uint64(b.nT))...)
	out = append(out, b.u64(uint64(b.nKV))...)
	out = append(out, b.meta...)
	out = append(out, b.descs...)
	for len(out)%32 != 0 {
		out = append(out, 0)
	}
	return append(out, b.data...)
}

func f32bytes(vs ...float32) []byte {
	out := make([]byte, 0, len(vs)*4)
	for _, v := range vs {
		x := make([]byte, 4)
		binary.LittleEndian.PutUint32(x, math.Float32bits(v))
		out = append(out, x...)
	}
	return out
}

func f16bytes(hs ...uint16) []byte {
	out := make([]byte, 0, len(hs)*2)
	for _, h := range hs {
		x := make([]byte, 2)
		binary.LittleEndian.PutUint16(x, h)
		out = append(out, x...)
	}
	return out
}

func sample(t *testing.T) *File {
	t.Helper()
	b := &builder{}
	b.kvU32("ced.embed_dim", 192)
	b.kvF32("ced.bn_eps", 1e-5)
	b.kvStrings("ced.labels", "Speech", "Bark", "Baby cry, infant cry")
	b.tensor("norm.weight", []uint64{4}, TypeF32, f32bytes(1, -2, 0.5, 1e-8))
	b.tensor("proj.weight", []uint64{4}, TypeF16, f16bytes(0x3C00, 0xC000, 0x0001, 0x03FF))
	f, err := Parse(b.bytes())
	if err != nil {
		t.Fatalf("the file built here does not read back: %v", err)
	}
	return f
}

func TestASynthesisedFileReadsBack(t *testing.T) {
	f := sample(t)
	if got, err := f.Int("ced.embed_dim"); err != nil || got != 192 {
		t.Errorf("embed_dim = %d, %v", got, err)
	}
	if got, err := f.Float("ced.bn_eps"); err != nil || math.Abs(got-1e-5) > 1e-12 {
		t.Errorf("bn_eps = %v, %v", got, err)
	}
	labels, err := f.Strings("ced.labels")
	if err != nil || len(labels) != 3 || labels[1] != "Bark" {
		t.Errorf("labels = %v, %v", labels, err)
	}

}

// An integer metadata value can be written on four bytes or on eight without
// the meaning changing, and whoever reads should not have to know which.
func TestAnIntegerIsReadWhateverItsWidth(t *testing.T) {
	b := &builder{}
	b.meta = append(b.meta, b.str("wide")...)
	b.meta = append(b.meta, b.u32(kUint64)...)
	b.meta = append(b.meta, b.u64(1012)...)
	b.nKV++
	b.tensor("x", []uint64{1}, TypeF32, f32bytes(0))
	f, err := Parse(b.bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.Int("wide"); err != nil || got != 1012 {
		t.Errorf("Int on a uint64 = %d, %v", got, err)
	}
}

// **The half-precision cases that matter are the denormals**, not the normal
// one: in a network the small weights are the majority, and a conversion that
// gets them wrong produces a model that runs and answers badly. Written with a
// counter starting at -1 it was off by a factor of four, and only here.
func TestHalfPrecisionCoversTheHardCases(t *testing.T) {
	cases := []struct {
		h    uint16
		want float32
		name string
	}{
		{0x0000, 0, "zero"},
		{0x3C00, 1, "one"},
		{0xC000, -2, "minus two"},
		{0x0400, math.Float32frombits(0x38800000), "the smallest normal"},
		{0x0001, math.Float32frombits(0x33800000), "the smallest denormal"},
		{0x03FF, math.Float32frombits(0x387FC000), "the largest denormal"},
		{0x7C00, float32(math.Inf(1)), "infinity"},
		{0xFC00, float32(math.Inf(-1)), "minus infinity"},
	}
	for _, c := range cases {
		got := f16(c.h)
		if got != c.want {
			t.Errorf("%s: f16(%#04x) = %v (%#08x), wanted %v (%#08x)",
				c.name, c.h, got, math.Float32bits(got), c.want, math.Float32bits(c.want))
		}
	}
	if z := f16(0x8000); math.Float32bits(z) != 0x80000000 {
		t.Errorf("negative zero lost its sign: %#08x", math.Float32bits(z))
	}
	if n := f16(0x7E00); !math.IsNaN(float64(n)) {
		t.Errorf("a NaN became %v: the mantissa is not being kept", n)
	}
}

func TestTheTensorsComeBackAsFloat32(t *testing.T) {
	f := sample(t)
	n, err := f.Tensor("norm.weight")
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.F32()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float32{1, -2, 0.5, 1e-8} {
		if got[i] != want {
			t.Errorf("norm.weight[%d] = %v, wanted %v", i, got[i], want)
		}
	}
	p, err := f.Tensor("proj.weight")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != TypeF16 {
		t.Fatalf("the type read is %s", p.Type)
	}
	pf, err := p.F32()
	if err != nil || len(pf) != 4 || pf[0] != 1 || pf[1] != -2 {
		t.Errorf("proj.weight = %v, %v", pf, err)
	}
}

// Q8_0: one half-precision scale and thirty-two integers. The whole block is
// tested because an error in its length shifts **every** block after it.
func TestQ8BlocksDequantise(t *testing.T) {
	raw := make([]byte, q8BlockBytes)
	binary.LittleEndian.PutUint16(raw, 0x3800) // scale 0.5
	for i := range q8BlockValues {
		raw[2+i] = byte(int8(i - 16))
	}
	b := &builder{}
	b.tensor("q", []uint64{q8BlockValues}, TypeQ8_0, raw)
	f, err := Parse(b.bytes())
	if err != nil {
		t.Fatal(err)
	}
	q, err := f.Tensor("q")
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.F32()
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		want := 0.5 * float32(int8(i-16))
		if got[i] != want {
			t.Errorf("q[%d] = %v, wanted %v", i, got[i], want)
		}
	}
}

// A number of values that does not divide into blocks is a file lying about its
// own shape, and that has to be said instead of reading the next block halfway.
func TestAQ8TensorThatDoesNotDivideIntoBlocksIsRefused(t *testing.T) {
	b := &builder{}
	b.tensor("q", []uint64{20}, TypeQ8_0, make([]byte, q8BlockBytes))
	if _, err := Parse(b.bytes()); err == nil {
		t.Error("twenty values in blocks of thirty-two got through")
	}
}

// **A broken file declares itself, it does not panic.** A model comes from
// outside — embedded, downloaded, copied halfway — and an index off the end of
// a slice would shut the monitor down instead of saying the model cannot be
// read.
func TestABrokenFileIsReportedAndNotFatal(t *testing.T) {
	b := &builder{}
	b.kvU32("k", 1)
	b.tensor("x", []uint64{4}, TypeF32, f32bytes(1, 2, 3, 4))
	good := b.bytes()

	for _, c := range []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"not a GGUF", []byte("NOPE0000")},
		{"truncated inside the header", good[:12]},
		{"truncated before the data", good[:len(good)-8]},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, err := Parse(c.in)
			if err == nil {
				t.Fatalf("no error on a file that is %s: %v", c.name, f)
			}
			if !strings.Contains(err.Error(), "gguf:") {
				t.Errorf("the error does not say whose it is: %v", err)
			}
		})
	}
	if _, err := Parse(good); err != nil {
		t.Errorf("and yet the intact file fails to parse: %v", err)
	}
}

// A tensor longer than the file is the shape a corrupt file takes, and that has
// to be said instead of handing over bytes that start wherever they land.
func TestATensorPastTheEndIsRefused(t *testing.T) {
	b := &builder{}
	b.tensor("x", []uint64{4096}, TypeF32, f32bytes(1, 2, 3, 4))
	if _, err := Parse(b.bytes()); err == nil {
		t.Error("a tensor longer than the file got through")
	}
}

func TestAnUnknownTensorIsAnError(t *testing.T) {
	f := sample(t)
	if _, err := f.Tensor("this name is not there"); err == nil {
		t.Error("a wrong name returned a tensor")
	}
}

// **A broken file need not only be truncated: it can be hostile.** The numbers
// a GGUF carries are lengths and sizes, and they are used to slice: one close
// to the maximum of an integer overflows the arithmetic that checks them, and
// the check passes. All three of these cases did something worse than an error
// before there was a guard.
func TestCraftedNumbersDoNotGetThrough(t *testing.T) {
	b := &builder{}
	b.tensor("x", []uint64{4}, TypeF32, f32bytes(1, 2, 3, 4))
	good := b.bytes()

	// It panicked: `c.p + n` overflowed negative and the slice was taken with
	// an impossible length.
	t.Run("a string length close to the maximum", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint64(bad[24:], 0x7FFFFFFFFFFFFFFF)
		if _, err := Parse(bad); err == nil {
			t.Error("a string length close to the maximum got through")
		}
	})

	// It did not panic, it did worse: 1<<62 times 32 is **zero** in an integer,
	// and out came a tensor declaring it had no values.
	//
	// **The two shapes are the same one, and a guard on each dimension stops
	// only the first.** Checking every dimension against the file length is
	// enough for two huge dimensions and not for four modest ones: 262144 four
	// times sits under a 262 KB file and multiplies to 2^72, which is zero
	// again.
	for _, dims := range [][]uint64{
		{1 << 62, 32},
		{262144, 262144, 262144, 262144},
	} {
		t.Run(fmt.Sprintf("dimensions that overflow: %v", dims), func(t *testing.T) {
			bb := &builder{}
			bb.tensor("x", dims, TypeF32, f32bytes(1))
			raw := append(bb.bytes(), make([]byte, 262144)...)
			if f, err := Parse(raw); err == nil {
				x, _ := f.Tensor("x")
				t.Errorf("got through, and the tensor declares %d values", x.Count())
			}
		})
	}

	// The alignment goes into a rounding, and a huge value moves the start of
	// the data off the end of the file.
	t.Run("an absurd alignment", func(t *testing.T) {
		bb := &builder{}
		bb.kvU32("general.alignment", 0x7FFFFFFF)
		bb.tensor("x", []uint64{4}, TypeF32, f32bytes(1, 2, 3, 4))
		if _, err := Parse(bb.bytes()); err == nil {
			t.Error("got through")
		}
	})

	if _, err := Parse(good); err != nil {
		t.Errorf("and yet the intact file fails to parse: %v", err)
	}
}
