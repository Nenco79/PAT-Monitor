package ced

import (
	"math"
	"math/rand"
	"testing"
)

// dft is the transform written straight from the definition: slow, obvious, and
// **not built from the same reasoning** as the fast one. Comparing the fast one
// with itself would prove nothing — it is the same form of cross-check as
// pat-opus, which decodes with an implementation that is not ours.
func dft(in []float64) []float64 {
	n := len(in)
	out := make([]float64, n/2+1)
	for k := 0; k <= n/2; k++ {
		var re, im float64
		for t := 0; t < n; t++ {
			a := -2 * math.Pi * float64(k) * float64(t) / float64(n)
			re += in[t] * math.Cos(a)
			im += in[t] * math.Sin(a)
		}
		out[k] = re*re + im*im
	}
	return out
}

func TestTheFastTransformAgreesWithTheDefinition(t *testing.T) {
	for _, n := range []int{16, 64, 512} {
		f := newFFT(n)
		in := make([]float32, n)
		ref := make([]float64, n)
		r := rand.New(rand.NewSource(int64(n)))
		for i := range in {
			v := r.Float64()*2 - 1
			in[i] = float32(v)
			ref[i] = v
		}
		got := make([]float32, n/2+1)
		f.power(in, got)
		want := dft(ref)

		// The tolerance is relative to the total energy: on a random signal the
		// small bins have few significant bits, and insisting on their digits
		// would mean chasing the order of accumulation.
		var tot float64
		for _, v := range want {
			tot += v
		}
		tol := tot * 1e-6
		for k := range want {
			if math.Abs(float64(got[k])-want[k]) > tol {
				t.Errorf("n=%d bin %d: %v, wanted %v (tolerance %v)", n, k, got[k], want[k], tol)
				break
			}
		}
	}
}

// A sine exactly on a bin has to put all its energy there and none elsewhere:
// it is the case where a permutation error is visible to the eye, because the
// line appears in a bin that has nothing to do with it.
func TestASineOnABinLandsOnThatBin(t *testing.T) {
	const n = 512
	f := newFFT(n)
	for _, bin := range []int{1, 7, 64, 255} {
		in := make([]float32, n)
		for i := range in {
			in[i] = float32(math.Sin(2 * math.Pi * float64(bin) * float64(i) / n))
		}
		out := make([]float32, n/2+1)
		f.power(in, out)

		best, at := float32(0), -1
		var tot float32
		for k, v := range out {
			tot += v
			if v > best {
				best, at = v, k
			}
		}
		if at != bin {
			t.Errorf("the sine on bin %d has its peak on %d", bin, at)
		}
		if best < 0.99*tot {
			t.Errorf("bin %d: the peak holds only %.1f%% of the energy", bin, 100*float64(best/tot))
		}
	}
}

// A constant is all in bin zero, and it is the case that catches a wrong scale
// factor: |X_0|^2 is (n*c)^2 and not n*c^2.
func TestAConstantIsAllInBinZero(t *testing.T) {
	const n = 512
	f := newFFT(n)
	in := make([]float32, n)
	for i := range in {
		in[i] = 0.5
	}
	out := make([]float32, n/2+1)
	f.power(in, out)
	want := float32(n) * 0.5 * float32(n) * 0.5
	if math.Abs(float64(out[0]-want)) > float64(want)*1e-5 {
		t.Errorf("bin 0 = %v, wanted %v", out[0], want)
	}
	for k := 1; k <= n/2; k++ {
		if out[k] > want*1e-9 {
			t.Errorf("a constant left %v in bin %d", out[k], k)
			break
		}
	}
}

// Parseval: the energy in time and the energy in frequency are the same thing.
// **It is the only test that looks at every bin together** — the others look
// where they already know something has to be.
func TestEnergyIsConserved(t *testing.T) {
	const n = 512
	f := newFFT(n)
	in := make([]float32, n)
	r := rand.New(rand.NewSource(7))
	var inTime float64
	for i := range in {
		v := r.Float64()*2 - 1
		in[i] = float32(v)
		inTime += v * v
	}
	out := make([]float32, n/2+1)
	f.power(in, out)

	// The bins between 1 and n/2-1 have a twin in the half we do not compute.
	inFreq := float64(out[0]) + float64(out[n/2])
	for k := 1; k < n/2; k++ {
		inFreq += 2 * float64(out[k])
	}
	inFreq /= n
	if math.Abs(inFreq-inTime) > inTime*1e-5 {
		t.Errorf("energy in time %v, in frequency %v", inTime, inFreq)
	}
}

// The working buffer is reused on every frame: two transforms in a row have to
// give the same result, otherwise there is state that survives.
func TestNothingSurvivesBetweenTransforms(t *testing.T) {
	const n = 512
	f := newFFT(n)
	a := make([]float32, n)
	b := make([]float32, n)
	r := rand.New(rand.NewSource(11))
	for i := range a {
		a[i] = float32(r.Float64())
		b[i] = float32(r.Float64())
	}
	first := make([]float32, n/2+1)
	second := make([]float32, n/2+1)
	f.power(a, first)
	f.power(b, make([]float32, n/2+1))
	f.power(a, second)
	for k := range first {
		if first[k] != second[k] {
			t.Fatalf("bin %d: %v the first time, %v the third", k, first[k], second[k])
		}
	}
}
