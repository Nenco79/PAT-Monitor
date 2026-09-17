package ced

import (
	"math"
	"math/rand"
	"testing"
)

// dot sums in four chains instead of one, so its result is not that of the
// left-to-right sum: what is tested is that the deviation stays where it
// should, and **on lengths that are not multiples of four**, which is where a
// badly written tail loses or repeats a term.
func TestTheFourChainSumIsStillTheSum(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 192, 767, 768} {
		a := make([]float32, n)
		b := make([]float32, n)
		var want float64
		for i := range a {
			a[i] = float32(r.NormFloat64())
			b[i] = float32(r.NormFloat64())
			want += float64(a[i]) * float64(b[i])
		}
		got := float64(dot(a, b))
		if math.Abs(got-want) > 1e-4*(1+math.Abs(want)) {
			t.Errorf("n=%d: %v, wanted %v", n, got, want)
		}
	}
}

// linear multiplies by rows: y[o] = W[o]·x + b[o]. **The case that catches a
// transposition is a non-square matrix with different numbers in every cell** —
// with a square symmetric one the wrong direction gives the same result.
func TestLinearMultipliesByRows(t *testing.T) {
	// two vectors of 3, to 2 outputs
	x := []float32{1, 2, 3, 10, 20, 30}
	w := []float32{
		1, 0, 0, // takes the first component
		0, 0, 2, // twice the third
	}
	bias := []float32{100, 200}
	out := make([]float32, 4)
	linear(out, x, w, bias, 2, 3, 2)
	want := []float32{101, 206, 110, 260}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("linear = %v, wanted %v", out, want)
		}
	}
}

// The normalisation leaves mean zero and variance one, and the variance is the
// **population** one: with the n-1 divisor the deviation would come out larger
// by a factor sqrt(n/(n-1)), which over 192 components is three thousandths —
// not enough to break anything, enough to shift every row of every block.
func TestLayerNormLeavesMeanZeroAndVarianceOne(t *testing.T) {
	const dim = 8
	x := []float32{3, -1, 4, 1, -5, 9, 2, 6}
	w := make([]float32, dim)
	b := make([]float32, dim)
	for i := range w {
		w[i] = 1
	}
	layerNorm(x, w, b, 1, dim, 0)
	var mean, variance float64
	for _, v := range x {
		mean += float64(v)
	}
	mean /= dim
	for _, v := range x {
		variance += (float64(v) - mean) * (float64(v) - mean)
	}
	variance /= dim
	if math.Abs(mean) > 1e-5 {
		t.Errorf("mean %v", mean)
	}
	if math.Abs(variance-1) > 1e-4 {
		t.Errorf("variance %v: with the n-1 divisor it would be %v", variance, float64(dim-1)/dim)
	}
	// And the scale and the shift really are applied.
	y := []float32{3, -1, 4, 1, -5, 9, 2, 6}
	for i := range w {
		w[i] = 2
		b[i] = 5
	}
	layerNorm(y, w, b, 1, dim, 0)
	for i := range y {
		if d := float64(y[i] - (2*x[i] + 5)); math.Abs(d) > 1e-4 {
			t.Errorf("component %d: %v instead of %v", i, y[i], 2*x[i]+5)
		}
	}
}

// **The GELU is the exact one, and the proof is that it deviates from the
// hyperbolic tangent.** Written with the approximation, this test would fail:
// that is what makes it a test rather than a re-reading.
func TestGeluIsTheExactOneNotTheApproximation(t *testing.T) {
	// Known values: gelu(0)=0, gelu(1)=0.8413447, gelu(-1)=-0.1586553.
	x := []float32{0, 1, -1, 3, -3}
	want := []float64{0, 0.8413447, -0.1586553, 2.9959502, -0.0040498}
	got := append([]float32(nil), x...)
	gelu(got)
	for i := range want {
		if d := math.Abs(float64(got[i]) - want[i]); d > 1e-6 {
			t.Errorf("gelu(%v) = %v, wanted %v", x[i], got[i], want[i])
		}
	}
	// And the deviation from the tanh approximation is larger than the
	// tolerance the model is compared against the oracle with.
	worst := 0.0
	for v := -4.0; v <= 4; v += 0.01 {
		exact := v * 0.5 * (1 + math.Erf(v/math.Sqrt2))
		approx := 0.5 * v * (1 + math.Tanh(math.Sqrt(2/math.Pi)*(v+0.044715*v*v*v)))
		if d := math.Abs(exact - approx); d > worst {
			worst = d
		}
	}
	if worst < 1e-4 {
		t.Errorf("the two forms deviate by only %.2e: the distinction would not be worth making", worst)
	}
	t.Logf("exact against approximate: up to %.2e", worst)
}

// **Subtracting the maximum is not caution.** The attention scores reach a few
// tens, and without taking the maximum off, exp overflows in float32: out would
// come a NaN, which does not protest and propagates until all 527 probabilities
// are NaN.
func TestSoftmaxSurvivesLargeScores(t *testing.T) {
	r := []float32{100, 101, 99}
	softmax(r)
	var sum float64
	for _, v := range r {
		if math.IsNaN(float64(v)) {
			t.Fatalf("softmax on large scores = %v", r)
		}
		sum += float64(v)
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Errorf("sum %v", sum)
	}
	if !(r[1] > r[0] && r[0] > r[2]) {
		t.Errorf("the order is not that of the scores: %v", r)
	}
	// The easy case, with the answer known in advance: two equal values make a
	// half each.
	e := []float32{7, 7}
	softmax(e)
	if e[0] != 0.5 || e[1] != 0.5 {
		t.Errorf("two equal scores give %v", e)
	}
}

func TestSigmoidIsPerClass(t *testing.T) {
	x := []float32{0, 2, -2, 100, -100}
	sigmoid(x)
	if x[0] != 0.5 {
		t.Errorf("sigmoid(0) = %v", x[0])
	}
	if math.Abs(float64(x[1]+x[2])-1) > 1e-6 {
		t.Errorf("sigmoid(2) and sigmoid(-2) are not complementary: %v %v", x[1], x[2])
	}
	// At the extremes it saturates, and at the bottom it **does not reach exact
	// zero**: a denormal is left, 3.8e-44. That is the right value, and
	// insisting on the zero would accuse correct code.
	if x[3] != 1 || x[4] <= 0 || x[4] > 1e-40 {
		t.Errorf("at the extremes: %v %v", x[3], x[4])
	}
	// **They do not sum to one, and that is the point**: they are 527 separate
	// questions.
	var sum float32
	for _, v := range x {
		sum += v
	}
	if math.Abs(float64(sum)-1) < 1e-6 {
		t.Error("the probabilities sum to one: somebody put a softmax in")
	}
}
