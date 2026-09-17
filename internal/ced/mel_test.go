package ced

import (
	"math"
	"testing"
)

// **Reflection has two conventions that differ by one sample**, and the wrong
// one gives no error at all: the spectrogram comes out plausible and different
// from the one the model was trained with. Here the expected result is written
// by hand, element by element, because it is the only way to tell them apart.
func TestReflectingDoesNotRepeatTheEdge(t *testing.T) {
	wave := []float32{10, 20, 30, 40, 50}
	dst := make([]float32, len(wave)+4)
	reflectPad(dst, wave, 2)
	want := []float32{30, 20, 10, 20, 30, 40, 50, 40, 30}
	if len(dst) != len(want) {
		t.Fatalf("length %d, wanted %d", len(dst), len(want))
	}
	for i := range want {
		if dst[i] != want[i] {
			t.Errorf("reflected %v, wanted %v", dst, want)
			break
		}
	}
}

// A made-up bank: one band per bin, so the mel is the spectrum and it is
// possible to reason about where the energy ends up. The window is rectangular
// for the same reason — what is being tested here is the front-end, not the
// Hann.
func testFrontend(t *testing.T, nFFT, hop, nMels int, topDB float64) *Frontend {
	t.Helper()
	nFreqs := nFFT/2 + 1
	win := make([]float32, nFFT)
	for i := range win {
		win[i] = 1
	}
	fb := make([]float32, nMels*nFreqs)
	for b := 0; b < nMels; b++ {
		fb[b*nFreqs+b] = 1
	}
	fe, err := NewFrontend(nFFT, hop, nMels, win, fb, 1e-10, 1, 10, topDB)
	if err != nil {
		t.Fatal(err)
	}
	return fe
}

// The frame count with centred padding is `1 + n/hop`. Getting it wrong gives a
// spectrogram shorter than the model expects, and then somebody lengthens it
// with zeros instead of noticing.
func TestTheFrameCountFollowsTheCentredPadding(t *testing.T) {
	fe := testFrontend(t, 512, 160, 8, 120)
	for _, n := range []int{16000, 16000 * 10, 12345} {
		wave := make([]float32, n)
		for i := range wave {
			wave[i] = float32(math.Sin(float64(i) * 0.01))
		}
		mel, frames, err := fe.Mel(wave)
		if err != nil {
			t.Fatal(err)
		}
		if want := 1 + n/160; frames != want {
			t.Errorf("n=%d: %d frames, wanted %d", n, frames, want)
		}
		if len(mel) != 8*frames {
			t.Errorf("n=%d: %d values for %d bands and %d frames", n, len(mel), 8, frames)
		}
	}
}

// A sine on one bin has to light its own band and leave the others dark. **It
// is the test that catches a row swapped between bands and frames**: with the
// indexing turned round the energy ends up scattered and no band dominates.
func TestASineLightsItsOwnBand(t *testing.T) {
	const nFFT, hop, nMels = 512, 160, 16
	fe := testFrontend(t, nFFT, hop, nMels, 0)
	const bin = 5
	wave := make([]float32, 16000)
	for i := range wave {
		wave[i] = float32(math.Sin(2 * math.Pi * float64(bin) * float64(i) / nFFT))
	}
	mel, frames, err := fe.Mel(wave)
	if err != nil {
		t.Fatal(err)
	}
	// A frame in the middle is examined, far from the reflected edges.
	t0 := frames / 2
	best, at := float32(math.Inf(-1)), -1
	for b := 0; b < nMels; b++ {
		if v := mel[b*frames+t0]; v > best {
			best, at = v, b
		}
	}
	if at != bin {
		t.Errorf("the sine on bin %d lit band %d", bin, at)
	}
	// The other bands have to sit well below: in decibels, "well" is tens.
	for b := 0; b < nMels; b++ {
		if b == bin {
			continue
		}
		if v := mel[b*frames+t0]; v > best-40 {
			t.Errorf("band %d sits at %.1f dB, only %.1f below the peak", b, v, best-v)
		}
	}
}

// The conversion to decibels on values known in advance.
func TestTheDecibelsAreTheOnesWeExpect(t *testing.T) {
	fe := testFrontend(t, 512, 160, 4, 0)
	mel := []float32{1, 100, 1e-10, 0}
	fe.toDB(mel)
	want := []float32{0, 20, -100, -100}
	for i := range want {
		if math.Abs(float64(mel[i]-want[i])) > 1e-4 {
			t.Errorf("toDB[%d] = %v, wanted %v", i, mel[i], want[i])
		}
	}
}

// **The floor sits at `maximum - topDB`, and the maximum is this spectrogram's
// own.** It is the property from which it follows that the front-end cannot
// work in a stream: here what is tested is that the clip follows the maximum
// instead of standing at an absolute value.
func TestTheFloorFollowsThisSpectrogramsMaximum(t *testing.T) {
	fe := testFrontend(t, 512, 160, 4, 30)
	loud := []float32{1e6, 1, 1e-6, 1e-10}
	quiet := []float32{1e-2, 1e-8, 1e-14, 1e-18}
	fe.toDB(loud)
	fe.toDB(quiet)
	if loud[0] != 60 || loud[len(loud)-1] != 30 {
		t.Errorf("loud = %v, wanted maximum 60 and floor 30", loud)
	}
	if quiet[0] != -20 || quiet[len(quiet)-1] != -50 {
		t.Errorf("quiet = %v, wanted maximum -20 and floor -50", quiet)
	}
	// The shape is the same one shifted: that is what "relative" means.
	for i := range loud {
		if d := loud[i] - quiet[i]; math.Abs(float64(d-80)) > 1e-4 {
			t.Errorf("index %d: the gap is %v instead of a constant 80 dB", i, d)
		}
	}
}

// Turning the volume up shifts the spectrogram by a constant and **does not
// change its shape** — as long as every band sits above the absolute floor. The
// signal is broadband on purpose: that is the condition the invariant holds in.
func TestLouderIsTheSameShapeShifted(t *testing.T) {
	fe := testFrontend(t, 512, 160, 16, 120)
	base := make([]float32, 16000)
	loud := make([]float32, 16000)
	x := uint32(12345)
	for i := range base {
		x = x*1664525 + 1013904223
		v := float32(x>>8)/float32(1<<24) - 0.5
		base[i] = v * 0.01
		loud[i] = v * 10
	}
	a, frames, err := fe.Mel(base)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := fe.Mel(loud)
	if err != nil {
		t.Fatal(err)
	}
	shift := b[0] - a[0]
	for i := range a {
		if d := b[i] - a[i]; math.Abs(float64(d-shift)) > 0.05 {
			t.Fatalf("value %d (band %d, frame %d): gap %v instead of %v",
				i, i/frames, i%frames, d, shift)
		}
	}
	if math.Abs(float64(shift)-60) > 0.5 {
		t.Errorf("a thousand times in amplitude is %v dB, wanted 60", shift)
	}
}

// **There are two floors, and only one is relative.** topDB follows the
// spectrogram's maximum; amin stands still at 1e-10, that is, -100 dB. On a
// band holding nothing the second bites first, and then turning the volume down
// **changes the shape** instead of shifting it.
//
// This is not a quibble: at night the room is flat, and that is the condition
// this program works in. The consequence to keep in mind is that **the scale of
// the input signal matters** — a waveform in [-1,1] and one in 16-bit integer
// units do not give the same spectrogram, and the model was trained on one of
// the two.
func TestTheAbsoluteFloorBitesBeforeTheRelativeOne(t *testing.T) {
	fe := testFrontend(t, 512, 160, 4, 120)
	// One full band and three empty ones, at two volumes far apart.
	loud := []float32{1, 1e-9, 1e-9, 1e-9}
	quiet := []float32{1e-6, 1e-15, 1e-15, 1e-15}
	fe.toDB(loud)
	fe.toDB(quiet)
	if loud[1] != -90 {
		t.Errorf("the loud one's empty band sits at %v, wanted -90 (above amin)", loud[1])
	}
	if quiet[1] != -100 {
		t.Errorf("the quiet one's empty band sits at %v, wanted -100 (amin holds it)", quiet[1])
	}
	if d := (loud[0] - quiet[0]) - (loud[1] - quiet[1]); d == 0 {
		t.Error("the two shapes are the same one shifted: the absolute floor did not bite")
	}
}

// A signal shorter than the padding cannot be reflected, and that has to be
// said instead of reading off the end of the slice.
func TestTooShortToReflectIsAnError(t *testing.T) {
	fe := testFrontend(t, 512, 160, 4, 120)
	if _, _, err := fe.Mel(make([]float32, 200)); err == nil {
		t.Error("two hundred samples were reflected around a 256 sample pad")
	}
}

func TestABadlyShapedFilterbankIsRefused(t *testing.T) {
	win := make([]float32, 512)
	if _, err := NewFrontend(512, 160, 64, win, make([]float32, 64*256), 1e-10, 1, 10, 120); err == nil {
		t.Error("a bank with 256 bins instead of 257 got through")
	}
	if _, err := NewFrontend(500, 160, 64, make([]float32, 500), make([]float32, 64*251), 1e-10, 1, 10, 120); err == nil {
		t.Error("a length that is not a power of two got through")
	}
}
