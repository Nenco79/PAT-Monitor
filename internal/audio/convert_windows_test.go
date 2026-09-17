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
