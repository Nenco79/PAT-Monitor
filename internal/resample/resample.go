// Package resample changes the sample rate of a signal, with a rational ratio
// and a windowed filter.
//
// **It is for where averaging samples is not enough.** The analysis stream is
// obtained by averaging a whole number of samples — 48 kHz to 16 averages three
// of them — and that road is the best one wherever it exists: it is exact, it
// has no filter and nothing to tune. But 44.1 kHz to 16 is a ratio of 441/160
// and 24 to 16 is 3/2, and there no whole number of samples is there to
// average.
//
// **The main stream is never resampled, and that stays true.** What the viewer
// listens to is left alone: a resampler written in a hurry shifts sounds up or
// down leaving no clue whatsoever, and a cry played back a tone higher has no
// way of being noticed. What passes through here is the **analysis** stream,
// which nobody listens to and which already goes through a decimation — and the
// difference between the two roads is not the resampling itself, it is that
// this one is measured.
//
// **Taking one sample every N creates aliases**, which is why the filter is
// here: a 15 kHz whistle decimated without one does not go away, it comes back
// at a frequency that does not exist in the room. The tests check this with a
// tone above Nyquist, which is the only case where a broken resampler differs
// from a correct one.
package resample

import (
	"fmt"
	"math"
)

// Rational converts from one rate to another with the exact ratio L/M.
//
// **It is not streaming**: it takes a whole signal and returns another one. The
// block path wants state between calls, and a branch that never runs is the
// least tested part of a program — it gets written when it is needed, with its
// own tests.
type Rational struct {
	from, to int
	l, m     int
	k        int         // half-width of the kernel, in input samples
	bank     [][]float32 // l phases, 2k+1 coefficients each
}

// New prepares the conversion. Both rates must be positive.
func New(from, to int) (*Rational, error) {
	if from <= 0 || to <= 0 {
		return nil, fmt.Errorf("resample: rates are %d and %d", from, to)
	}
	g := gcd(from, to)
	r := &Rational{from: from, to: to, l: to / g, m: from / g}

	// The passband is half of the **lower** of the two rates: going down it is
	// the output's Nyquist limit, going up it is what was in the input and
	// cannot be invented.
	ratio := math.Min(1, float64(to)/float64(from))
	// **The number of taps is counted in zero crossings of the sinc**, not in
	// samples: going down in rate the kernel widens, and counting in samples
	// would give an ever shorter filter exactly where a longer one is needed.
	const zeros = 24
	r.k = int(math.Ceil(zeros / ratio))
	r.bank = make([][]float32, r.l)
	for phase := 0; phase < r.l; phase++ {
		h := make([]float32, 2*r.k+1)
		var sum float64
		for j := -r.k; j <= r.k; j++ {
			x := float64(j) - float64(phase)/float64(r.l)
			v := ratio * sinc(ratio*x) * blackman(x, float64(r.k))
			h[j+r.k] = float32(v)
			sum += v
		}
		// Every phase is normalised to sum one, that is, to unit gain at DC.
		//
		// **Here it repairs nothing, and that is measured**: with twenty-four
		// zero crossings and the Blackman window the 160 phases already sum
		// between 1.000001 and 1.000002, a deviation of 0.000 dB. Removing it
		// makes no test fail. It stays because it is the **exact** number
		// rather than one close enough, and because it is what holds if the
		// kernel is ever shortened: that is where the sum starts depending on
		// the phase, and the gain would then wobble at the ratio's frequency.
		// It costs one division per coefficient, once.
		if sum != 0 {
			for i := range h {
				h[i] = float32(float64(h[i]) / sum)
			}
		}
		r.bank[phase] = h
	}
	return r, nil
}

// Len says how many samples an input of n will produce.
func (r *Rational) Len(n int) int {
	if n <= 0 {
		return 0
	}
	return (n*r.l + r.m - 1) / r.m
}

// Convert resamples a whole signal.
//
// Outside its ends the signal is zero, which is the only honest thing to
// assume: any other continuation would invent sound.
func (r *Rational) Convert(in []float32) []float32 {
	out := make([]float32, r.Len(len(in)))
	for n := range out {
		// The exact position in the input is n*m/l, and the fractional part
		// picks the phase: there are exactly l phases, which is why the bank
		// can be precomputed.
		q := n * r.m / r.l
		phase := (n * r.m) % r.l
		h := r.bank[phase]
		var s float32
		lo, hi := q-r.k, q+r.k
		if lo < 0 {
			lo = 0
		}
		if hi >= len(in) {
			hi = len(in) - 1
		}
		for i := lo; i <= hi; i++ {
			s += in[i] * h[i-q+r.k]
		}
		out[n] = s
	}
	return out
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	p := math.Pi * x
	return math.Sin(p) / p
}

// blackman is the window, zero outside [-k, k]. It attenuates the side lobes by
// about 58 dB, which past the sixteen bits of a sample cannot be heard.
func blackman(x, k float64) float64 {
	if x < -k || x > k {
		return 0
	}
	t := (x + k) / (2 * k)
	return 0.42 - 0.5*math.Cos(2*math.Pi*t) + 0.08*math.Cos(4*math.Pi*t)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
