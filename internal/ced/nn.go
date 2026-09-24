package ced

import "math"

// dot is the scalar product, with four accumulators instead of one.
//
// This is not micro-optimisation: with a single accumulator every addition
// waits for the one before it, and a dependency chain 768 long leaves the
// floating-point unit idle most of the time. Four independent chains fill it.
// **The order of the additions changes**, and with it the last digit: whoever
// compares against an oracle has to allow for that, because PyTorch sums in an
// order of its own too and neither of them is the one written in the formula.
func dot(a, b []float32) float32 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}

// linear applies y = Wx + b to n row vectors at once.
//
// **W arrives as [out][in], which is the order PyTorch writes it in**, so the
// product is between two contiguous rows and there is nothing to transpose.
// Transposing to do the product "the right way round" would cost a copy and
// give a strided read, that is, the slow road written as if it were the
// straight one.
//
// Ninety per cent of the work is in here: 3.2 GFLOP per window. If it ever has
// to go faster — more goroutines, or assembly, which with CGO_ENABLED=0 is
// still allowed — this function is the one to touch and nothing else.
func linear(out, x, w, bias []float32, n, in, outDim int) {
	for i := range n {
		xr := x[i*in : i*in+in]
		or := out[i*outDim : (i+1)*outDim]
		for o := range or {
			or[o] = dot(xr, w[o*in:o*in+in]) + bias[o]
		}
	}
}

// layerNorm normalises each row over its dim components, in place.
//
// **The variance is the population one**, divided by dim and not by dim-1: that
// is what PyTorch computes, and the other would give a small systematic
// deviation on every row of every block — that is, a model that answers almost
// right, which is the fault this project fears most.
func layerNorm(x, w, b []float32, n, dim int, eps float32) {
	inv := 1 / float32(dim)
	for i := range n {
		r := x[i*dim : (i+1)*dim]
		var mean float32
		for _, v := range r {
			mean += v
		}
		mean *= inv
		var variance float32
		for _, v := range r {
			d := v - mean
			variance += d * d
		}
		variance *= inv
		scale := float32(1 / math.Sqrt(float64(variance+eps)))
		for k, v := range r {
			r[k] = (v-mean)*scale*w[k] + b[k]
		}
	}
}

// gelu is the **exact** one, with the error function.
//
// The hyperbolic-tangent approximation is the form most often written down, and
// this is where it will not do. Measured, it deviates by up to **4.7e-04 on a
// single activation**, and there are 9216 activations per token across twelve
// layers: on its own it eats the whole margin this model is compared against
// the oracle with, which over the entire pass measures 6.7e-04 in the worst
// case. A deviation that sits below every test's threshold and above the
// comparison's is the quickest way to spend a week looking for it elsewhere.
func gelu(x []float32) {
	for i, v := range x {
		d := float64(v)
		x[i] = float32(d * 0.5 * (1 + math.Erf(d/math.Sqrt2)))
	}
}

// softmax normalises a row of scores in place.
//
// **The maximum is subtracted before exponentiating**, and that is not caution:
// the attention scores reach a few tens, exp of those numbers overflows in
// float32, and an infinity divided by an infinity gives NaN. A NaN here does
// not protest: it propagates quietly until all 527 probabilities are NaN, which
// from outside looks like a model that did not understand the scene.
func softmax(r []float32) {
	max := r[0]
	for _, v := range r[1:] {
		if v > max {
			max = v
		}
	}
	var sum float32
	for i, v := range r {
		e := float32(math.Exp(float64(v - max)))
		r[i] = e
		sum += e
	}
	inv := 1 / sum
	for i := range r {
		r[i] *= inv
	}
}

// sigmoid turns the logits into independent probabilities, in place.
func sigmoid(x []float32) {
	for i, v := range x {
		x[i] = float32(1 / (1 + math.Exp(-float64(v))))
	}
}
