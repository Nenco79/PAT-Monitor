package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

// TestToMonoS16Float32 checks the downmix on a format like the one this
// machine's microphone delivers: four floating-point channels.
func TestToMonoS16Float32(t *testing.T) {
	f := StreamFormat{SampleRate: 48000, Channels: 4, BitsPerSample: 32, Float: true}

	// Two frames: the first with different channels to average, the second all
	// zeroes.
	src := f32le(0.4, 0.2, -0.2, 0.0, 0, 0, 0, 0)
	dst := make([]int16, 2)

	n, err := ToMonoS16(src, f, dst)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d frames converted, want 2", n)
	}

	// (0.4 + 0.2 - 0.2 + 0.0) / 4 = 0.1
	const want = int16(3276) // 0.1 of full scale
	if diff := abs16(dst[0] - want); diff > 1 {
		t.Errorf("channel average = %d, want %d", dst[0], want)
	}
	if dst[1] != 0 {
		t.Errorf("silence converted to %d", dst[1])
	}
}

// TestToMonoS16Clamps checks that a signal past unity saturates instead of
// wrapping: a sample that wraps becomes a full-scale click, far more audible
// than clipping.
func TestToMonoS16Clamps(t *testing.T) {
	f := StreamFormat{SampleRate: 48000, Channels: 1, BitsPerSample: 32, Float: true}
	dst := make([]int16, 2)

	if _, err := ToMonoS16(f32le(2.5, -2.5), f, dst); err != nil {
		t.Fatal(err)
	}
	if dst[0] != 32767 {
		t.Errorf("positive peak = %d, want 32767", dst[0])
	}
	if dst[1] != -32768 {
		t.Errorf("negative peak = %d, want -32768", dst[1])
	}
}

// TestSaturateS16KeepsSamplesInRange checks the one saturation the capture path
// has: truncation inside the range, the extremes pinned at either end.
func TestSaturateS16KeepsSamplesInRange(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int16
	}{
		{0, 0}, {100.4, 100}, {-100.4, -100},
		{40000, 32767}, {-40000, -32768},
		{32767.9, 32767}, {-32768.9, -32768},
	} {
		if got := SaturateS16(c.in); got != c.want {
			t.Errorf("SaturateS16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestToMonoS16Int16 covers the integer path, where there is no scale
// conversion but only the downmix.
func TestToMonoS16Int16(t *testing.T) {
	f := StreamFormat{SampleRate: 48000, Channels: 2, BitsPerSample: 16}
	src := make([]byte, 4)
	binary.LittleEndian.PutUint16(src[0:], uint16(int16(1000)))
	binary.LittleEndian.PutUint16(src[2:], uint16(int16(3000)))

	dst := make([]int16, 1)
	if _, err := ToMonoS16(src, f, dst); err != nil {
		t.Fatal(err)
	}
	if diff := abs16(dst[0] - 2000); diff > 1 {
		t.Errorf("average = %d, want 2000", dst[0])
	}
}

// TestFirstChannelS16ReadsEveryFormat checks the instruments' reader on every
// sample format sampleAt knows, two channels each, the second one loud so that
// reading it or averaging it in shows. The 24-bit and 64-bit float rows are the
// ones pat-wasapi's own converter did not have: on such an endpoint it handed
// the analyser nothing, and reported "no sample received" about a microphone
// that was delivering. A 16-bit sample must come back unchanged, because the
// count of exact zeros is the figure the instrument exists for. For the same
// reason the wider integers floor, and the rows that show it are the negative
// values that are not a multiple of the 16-bit step: -1 and -65537 are where
// truncating toward zero reads one LSB high, and -65536 alone cannot see it.
func TestFirstChannelS16ReadsEveryFormat(t *testing.T) {
	const loud = 0x7000 // on channel 1, on the 16-bit scale
	for _, c := range []struct {
		name string
		f    StreamFormat
		ch0  []int64 // raw values, in the format's own integer or float scale
		want []int16
	}{
		{"int16", StreamFormat{BitsPerSample: 16}, []int64{0, 1, -1, 32767, -32768}, []int16{0, 1, -1, 32767, -32768}},
		{"int24", StreamFormat{BitsPerSample: 24}, []int64{0, 0x123456, -256, -1, 0x7FFFFF, -0x800000}, []int16{0, 0x1234, -1, -1, 32767, -32768}},
		{"int32", StreamFormat{BitsPerSample: 32}, []int64{0, 0x12345678, -65536, -1, -65537}, []int16{0, 0x1234, -1, -1, -2}},
	} {
		c.f.Channels = 2
		bps := c.f.BitsPerSample / 8
		src := make([]byte, 0, len(c.ch0)*2*bps)
		for _, v := range c.ch0 {
			src = appendLE(src, uint64(v), bps)
			src = appendLE(src, uint64(loud)<<(8*(bps-2)), bps)
		}
		checkFirstChannel(t, c.name, src, c.f, c.want)
	}

	f32 := StreamFormat{Channels: 2, BitsPerSample: 32, Float: true}
	checkFirstChannel(t, "float32", f32le(0.25, 0.9, -1, 0.9, 0, 0.9), f32, []int16{8192, -32768, 0})

	f64 := StreamFormat{Channels: 2, BitsPerSample: 64, Float: true}
	var src []byte
	for _, v := range []float64{0.5, 0.9, -0.5, 0.9, 2, 0.9} {
		src = appendLE(src, math.Float64bits(v), 8)
	}
	checkFirstChannel(t, "float64", src, f64, []int16{16384, -16384, 32767})
}

func checkFirstChannel(t *testing.T, name string, src []byte, f StreamFormat, want []int16) {
	t.Helper()
	dst := make([]int16, len(want)+1) // one spare: the count must be the frames
	n, err := FirstChannelS16(src, f, dst)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return
	}
	if n != len(want) {
		t.Errorf("%s: %d samples, want %d", name, n, len(want))
		return
	}
	for i := range want {
		if dst[i] != want[i] {
			t.Errorf("%s: sample %d = %d, want %d", name, i, dst[i], want[i])
		}
	}
}

// appendLE appends the low n bytes of v, little-endian.
func appendLE(b []byte, v uint64, n int) []byte {
	for i := range n {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

func f32le(values ...float32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}
