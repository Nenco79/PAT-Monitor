package resample

import (
	"math"
	"testing"
)

// tone generates a sine of n samples at hz, sampled at rate.
func tone(hz float64, rate, n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / float64(rate)))
	}
	return s
}

// measure estimates the amplitude and frequency of a sine by correlating it
// with a test sine and cosine. **It is the right question and not an FFT**:
// here the frequency to look for is known, and what is wanted is how much of it
// there is — an FFT would answer the same question inside a wide bin and with
// its own losses. The first and last tenth are skipped, where the filter is not
// full yet.
func measure(s []float32, hz float64, rate int) float64 {
	from, to := len(s)/10, len(s)-len(s)/10
	var re, im float64
	for i := from; i < to; i++ {
		a := 2 * math.Pi * hz * float64(i) / float64(rate)
		re += float64(s[i]) * math.Cos(a)
		im += float64(s[i]) * math.Sin(a)
	}
	n := float64(to - from)
	return 2 * math.Hypot(re, im) / n
}

// **A tone inside the band comes out whole and at the same frequency.** This is
// the case that catches a wrong gain and a badly normalised phase.
func TestATonePassesThroughAtTheSameLevel(t *testing.T) {
	for _, c := range []struct{ from, to int }{
		{44100, 16000}, {24000, 16000}, {48000, 16000}, {16000, 48000},
	} {
		r, err := New(c.from, c.to)
		if err != nil {
			t.Fatal(err)
		}
		for _, hz := range []float64{100, 440, 1000, 3000, 6000} {
			in := tone(hz, c.from, c.from) // one second
			out := r.Convert(in)
			if got, want := len(out), c.to; math.Abs(float64(got-want)) > 1 {
				t.Errorf("%d->%d: %d samples for one second, wanted %d", c.from, c.to, got, want)
			}
			amp := measure(out, hz, c.to)
			if math.Abs(amp-1) > 0.02 {
				t.Errorf("%d->%d at %.0f Hz: amplitude %.4f instead of 1", c.from, c.to, hz, amp)
			}
		}
	}
}

// **What tells a resampler from a decimation is a tone above Nyquist.** Taking
// one sample every N, a 15 kHz whistle does not go away: it folds onto a
// frequency that does not exist in the room, and from there on nobody can know
// it was never there. Both halves are checked here: that the tone is gone,
// **and** that it has not reappeared somewhere else.
func TestAToneAboveNyquistIsGoneAndDoesNotFoldBack(t *testing.T) {
	const from, to = 44100, 16000
	r, err := New(from, to)
	if err != nil {
		t.Fatal(err)
	}
	for _, hz := range []float64{9000, 12000, 15000, 20000} {
		in := tone(hz, from, from)
		out := r.Convert(in)
		// The alias of hz after sampling at `to` lands here.
		alias := math.Abs(hz - math.Round(hz/float64(to))*float64(to))
		amp := measure(out, hz, to)         // where it was
		aliasAmp := measure(out, alias, to) // where it would land if it folded
		db := 20 * math.Log10(math.Max(amp, aliasAmp)+1e-12)
		if db > -40 {
			t.Errorf("%.0f Hz: %.1f dB left (alias at %.0f Hz), wanted below -40", hz, db, alias)
		}
	}
}

// DC must not change: this is the proof that every phase sums to one. Without
// the per-phase normalisation the gain wobbles with the ratio, and out of it
// comes a hum that is heard and not seen.
func TestAConstantStaysTheSameConstant(t *testing.T) {
	for _, c := range []struct{ from, to int }{{44100, 16000}, {24000, 16000}, {8000, 16000}} {
		r, err := New(c.from, c.to)
		if err != nil {
			t.Fatal(err)
		}
		in := make([]float32, c.from)
		for i := range in {
			in[i] = 0.25
		}
		out := r.Convert(in)
		// Look at the middle: at the edges the kernel runs off the signal.
		for i := len(out) / 4; i < 3*len(out)/4; i++ {
			if math.Abs(float64(out[i])-0.25) > 1e-4 {
				t.Fatalf("%d->%d: at sample %d DC is %v instead of 0.25",
					c.from, c.to, i, out[i])
			}
		}
	}
}

// Same rate in and out: the signal must not change, and the filter must reduce
// to a zero delay. It is the degenerate case that gets broken first, because
// nobody tests it.
func TestTheSameRateChangesNothing(t *testing.T) {
	r, err := New(16000, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if r.l != 1 || r.m != 1 {
		t.Fatalf("the ratio did not reduce: %d/%d", r.l, r.m)
	}
	in := tone(440, 16000, 16000)
	out := r.Convert(in)
	if len(out) != len(in) {
		t.Fatalf("%d samples instead of %d", len(out), len(in))
	}
	for i := len(in) / 4; i < 3*len(in)/4; i++ {
		if d := math.Abs(float64(out[i] - in[i])); d > 1e-3 {
			t.Fatalf("at sample %d: %v instead of %v", i, out[i], in[i])
		}
	}
}

// How many samples come out is known beforehand, and has to match.
func TestTheLengthIsKnownInAdvance(t *testing.T) {
	r, err := New(44100, 16000)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1, 441, 44100, 5 * 44100, 12345} {
		if got := len(r.Convert(make([]float32, n))); got != r.Len(n) {
			t.Errorf("n=%d: %d samples, Len promised %d", n, got, r.Len(n))
		}
	}
	if got, want := r.Len(5*44100), 5*16000; got != want {
		t.Errorf("five seconds give %d samples, wanted %d", got, want)
	}
}

func TestABadRateIsRefused(t *testing.T) {
	if _, err := New(0, 16000); err == nil {
		t.Error("a zero rate got through")
	}
	if _, err := New(44100, -1); err == nil {
		t.Error("a negative rate got through")
	}
}
