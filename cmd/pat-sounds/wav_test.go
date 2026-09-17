package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// **The reader is tested on files built here.** The datasets are not in the
// repository and weigh twelve gigabytes: a test that looked for them on the
// disk of whoever runs it would skip everywhere, that is, it would test
// nothing. Without these, the "extensible" format went through unnoticed.

func chunk(id string, body []byte) []byte {
	out := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func u16(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func u32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

// wavFile composes a WAV. With `extensible` the format code becomes 0xFFFE and
// the real one moves inside the subformat GUID, which is exactly the case half
// of UrbanSound8K uses.
func wavFile(t *testing.T, format, channels, rate, bits int, extensible bool, data []byte) string {
	t.Helper()
	fmtBody := append([]byte{}, u16(uint16(format))...)
	if extensible {
		fmtBody = u16(formatExtensible)
	}
	fmtBody = append(fmtBody, u16(uint16(channels))...)
	fmtBody = append(fmtBody, u32(uint32(rate))...)
	fmtBody = append(fmtBody, u32(uint32(rate*channels*bits/8))...)
	fmtBody = append(fmtBody, u16(uint16(channels*bits/8))...)
	fmtBody = append(fmtBody, u16(uint16(bits))...)
	if extensible {
		fmtBody = append(fmtBody, u16(22)...)             // cbSize
		fmtBody = append(fmtBody, u16(uint16(bits))...)   // valid bits
		fmtBody = append(fmtBody, u32(3)...)              // channel mask
		fmtBody = append(fmtBody, u16(uint16(format))...) // the real format
		fmtBody = append(fmtBody, make([]byte, 14)...)    // the rest of the GUID
	}
	body := append([]byte("WAVE"), chunk("fmt ", fmtBody)...)
	// A chunk we do not care about, in the middle: skipping it wrongly shifts
	// the data.
	body = append(body, chunk("LIST", []byte("INFOxxx"))...)
	body = append(body, chunk("data", data)...)
	out := append([]byte("RIFF"), u32(uint32(len(body)))...)
	out = append(out, body...)

	path := filepath.Join(t.TempDir(), "t.wav")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func s16le(vs ...int16) []byte {
	b := make([]byte, len(vs)*2)
	for i, v := range vs {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

func TestPlainSixteenBitMono(t *testing.T) {
	p := wavFile(t, formatPCM, 1, 16000, 16, false, s16le(0, 16384, -16384, 32767))
	w, rate, err := loadWAV(p)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 16000 || len(w) != 4 {
		t.Fatalf("%d Hz, %d samples", rate, len(w))
	}
	for i, want := range []float32{0, 0.5, -0.5, 32767.0 / 32768} {
		if math.Abs(float64(w[i]-want)) > 1e-6 {
			t.Errorf("sample %d = %v, expected %v", i, w[i], want)
		}
	}
}

// **The "extensible" format is an indirection, not a format.** Refusing it lost
// 45% of UrbanSound8K, and the clips disappeared among thousands of skipped
// rows: the recall that came out of it looked like the model's.
func TestTheExtensibleFormatIsFollowed(t *testing.T) {
	data := s16le(0, 16384, -16384)
	plain := wavFile(t, formatPCM, 1, 44100, 16, false, data)
	ext := wavFile(t, formatPCM, 1, 44100, 16, true, data)

	a, ra, err := loadWAV(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, rb, err := loadWAV(ext)
	if err != nil {
		t.Fatalf("an extensible WAV was refused: %v", err)
	}
	if ra != rb || len(a) != len(b) {
		t.Fatalf("%d Hz %d samples against %d Hz %d samples", ra, len(a), rb, len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("sample %d: %v against %v", i, a[i], b[i])
		}
	}
}

// Stereo is averaged, and the average is what the model wants: a single channel.
func TestStereoBecomesMono(t *testing.T) {
	p := wavFile(t, formatPCM, 2, 16000, 16, false, s16le(32767, -32768, 16384, 16384))
	w, _, err := loadWAV(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 2 {
		t.Fatalf("%d frames from two channels", len(w))
	}
	if math.Abs(float64(w[0])) > 1e-4 {
		t.Errorf("two opposite channels give %v instead of zero", w[0])
	}
	if math.Abs(float64(w[1]-0.5)) > 1e-4 {
		t.Errorf("two equal channels give %v instead of 0.5", w[1])
	}
}

// Eight bits are **unsigned**: centring them wrongly gives a DC offset of half
// full scale that carries into the whole spectrogram.
func TestEightBitIsUnsigned(t *testing.T) {
	p := wavFile(t, formatPCM, 1, 8000, 8, false, []byte{128, 255, 0, 192})
	w, _, err := loadWAV(p)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float32{0, 0.9921875, -1, 0.5} {
		if math.Abs(float64(w[i]-want)) > 1e-6 {
			t.Errorf("sample %d = %v, expected %v", i, w[i], want)
		}
	}
}

// Twenty-four bits are three bytes whose sign has to be extended by hand,
// because there is no integer of that size.
func TestTwentyFourBitKeepsItsSign(t *testing.T) {
	p := wavFile(t, formatPCM, 1, 48000, 24, false, []byte{
		0x00, 0x00, 0x40, // +2^22 = +0.5
		0x00, 0x00, 0xC0, // -2^22 = -0.5
	})
	w, _, err := loadWAV(p)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(w[0]-0.5)) > 1e-6 || math.Abs(float64(w[1]+0.5)) > 1e-6 {
		t.Errorf("24 bit: %v and %v, expected 0.5 and -0.5", w[0], w[1])
	}
}

func TestFloatSamplesPassThrough(t *testing.T) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data, math.Float32bits(0.25))
	binary.LittleEndian.PutUint32(data[4:], math.Float32bits(-0.75))
	p := wavFile(t, formatFloat, 1, 44100, 32, false, data)
	w, _, err := loadWAV(p)
	if err != nil {
		t.Fatal(err)
	}
	if w[0] != 0.25 || w[1] != -0.75 {
		t.Errorf("float: %v", w)
	}
}

// **A format we cannot read is refused.** Interpreted as PCM it would give noise
// with the air of a signal, that is, a clip that enters the statistics saying
// something false — worse than a skipped one.
func TestAFormatWeCannotReadIsRefused(t *testing.T) {
	const formatALaw = 6
	p := wavFile(t, formatALaw, 1, 8000, 8, false, []byte{1, 2, 3, 4})
	if _, _, err := loadWAV(p); err == nil {
		t.Error("an A-law WAV passed for PCM")
	}
	if _, _, err := loadWAV(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Error("a file that does not exist passed")
	}
	bad := filepath.Join(t.TempDir(), "bad.wav")
	if err := os.WriteFile(bad, []byte("NOPE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadWAV(bad); err == nil {
		t.Error("four random bytes passed for a WAV")
	}
}
