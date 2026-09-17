package ced

import "math"

// fft is a real-to-power transform of fixed length, with the twiddle factors
// and the buffers prepared once.
//
// **It is the complex transform applied to a real signal, that is, twice the
// work needed, and that is a choice.** The trick that packs a 512-point real
// signal into a 256-point complex one and costs half exists; it is not needed
// here. Measured against the model's cost: the ten-second window is 1012
// frames, each one a 512-point transform, ~47 MFLOP in all — against the ~3.5
// GFLOP of the transformer behind it. That is seven milliseconds out of five
// hundred. Buying one per cent with an algorithm that has twice as many ways to
// go wrong is not a bargain.
type fft struct {
	n    int
	rev  []int
	cos  []float32
	sin  []float32
	re   []float32
	im   []float32
	half []float32
}

// newFFT prepares an n-point transform, with n a power of two.
func newFFT(n int) *fft {
	f := &fft{
		n:    n,
		rev:  make([]int, n),
		cos:  make([]float32, n/2),
		sin:  make([]float32, n/2),
		re:   make([]float32, n),
		im:   make([]float32, n),
		half: make([]float32, n/2+1),
	}
	// The bit-reversed permutation: index j is built from the previous one by
	// adding a one that carries from the top instead of from the bottom.
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		f.rev[i] = j
	}
	for k := 0; k < n/2; k++ {
		a := -2 * math.Pi * float64(k) / float64(n)
		f.cos[k] = float32(math.Cos(a))
		f.sin[k] = float32(math.Sin(a))
	}
	return f
}

// power fills out with |X_k|^2 for k from 0 to n/2 inclusive, that is, n/2+1
// values. The buffer belongs to the receiver: this function runs a thousand
// times per window and must not allocate.
func (f *fft) power(in []float32, out []float32) {
	n := f.n
	for i := range f.im {
		f.im[i] = 0
	}
	// The input is permuted as it goes in: each sample is copied straight to
	// the place it belongs.
	f.re[0] = in[0]
	for i := 1; i < n; i++ {
		f.re[f.rev[i]] = in[i]
	}

	for length := 2; length <= n; length <<= 1 {
		step := n / length
		for start := 0; start < n; start += length {
			k := 0
			for i := start; i < start+length/2; i++ {
				wr, wi := f.cos[k], f.sin[k]
				j := i + length/2
				tr := f.re[j]*wr - f.im[j]*wi
				ti := f.re[j]*wi + f.im[j]*wr
				f.re[j] = f.re[i] - tr
				f.im[j] = f.im[i] - ti
				f.re[i] += tr
				f.im[i] += ti
				k += step
			}
		}
	}

	for k := 0; k <= n/2; k++ {
		out[k] = f.re[k]*f.re[k] + f.im[k]*f.im[k]
	}
}
