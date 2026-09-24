package ced

import (
	"math"
	"testing"
)

// **The real model does not take part in these tests**, and that is not
// laziness: it weighs eleven megabytes, it lives outside the repository, and a
// test that went looking for it on the disk of whoever runs it would pass here
// and fail everywhere else. What the real model proves — that the numbers are
// the right ones — is proved once against the oracle and written down in
// baselines/ced.txt.
//
// What is tested here is the **layout**: where each number ends up. It is the
// family of defects a comparison with the oracle catches late and blind, and
// that a refactor can reintroduce without touching a formula.
func tiny(t *testing.T, depth int) *Model {
	t.Helper()
	const (
		dim       = 4
		heads     = 2
		patch     = 2
		nMels     = 4
		maxFrames = 8
		classes   = 3
		nFFT      = 16
		hop       = 4
	)
	nFreqs := nFFT/2 + 1
	win := make([]float32, nFFT)
	for i := range win {
		win[i] = 1
	}
	bank := make([]float32, nMels*nFreqs)
	for b := range nMels {
		bank[b*nFreqs+b] = 1
	}
	fe, err := NewFrontend(nFFT, hop, nMels, win, bank, 1e-10, 1, 10, 120)
	if err != nil {
		t.Fatal(err)
	}
	ones := func(n int) []float32 {
		s := make([]float32, n)
		for i := range s {
			s[i] = 1
		}
		return s
	}
	m := &Model{
		fe:          fe,
		labels:      []string{"a", "b", "c"},
		rate:        16000,
		dim:         dim,
		heads:       heads,
		headDim:     dim / heads,
		mlpDim:      4 * dim,
		patch:       patch,
		nMels:       nMels,
		freqPatches: nMels / patch,
		maxFrames:   maxFrames,
		maxPatches:  maxFrames / patch,
		epsBN:       1e-5,
		epsEnc:      1e-6,
		epsHead:     1e-5,
		bnScale:     ones(nMels),
		bnShift:     make([]float32, nMels),
		patchW:      make([]float32, dim*patch*patch),
		patchB:      make([]float32, dim),
		timePos:     make([]float32, dim*(maxFrames/patch)),
		freqPos:     make([]float32, dim*(nMels/patch)),
		normW:       ones(dim),
		normB:       make([]float32, dim),
		headNormW:   ones(dim),
		headNormB:   make([]float32, dim),
		headW:       make([]float32, classes*dim),
		headB:       make([]float32, classes),
	}
	if m.index, err = indexLabels(m.labels); err != nil {
		t.Fatal(err)
	}
	m.blocks = make([]block, depth)
	for i := range m.blocks {
		b := &m.blocks[i]
		b.norm1W, b.norm1B = ones(dim), make([]float32, dim)
		b.norm2W, b.norm2B = ones(dim), make([]float32, dim)
		b.qkvW, b.qkvB = make([]float32, 3*dim*dim), make([]float32, 3*dim)
		b.projW, b.projB = make([]float32, dim*dim), make([]float32, dim)
		b.fc1W, b.fc1B = make([]float32, m.mlpDim*dim), make([]float32, m.mlpDim)
		b.fc2W, b.fc2B = make([]float32, dim*m.mlpDim), make([]float32, dim)
	}
	return m
}

// **The flattening is frequency-major**, that is, the token is `pf*tp + pt`.
// Turning it round leaves the same tokens with the same values in a different
// order, and **gives no error at all**: the positionals are already inside, so
// the attention is not invariant and out comes a model that runs and answers at
// random. Here the weight reads a single cell, so every token carries the
// number of the cell it came from.
func TestTheTokensAreLaidOutFrequencyMajor(t *testing.T) {
	m := tiny(t, 0)
	m.patchW[0] = 1 // dimension 0, top-left corner of the patch
	const stride = 6
	mel := make([]float32, m.nMels*stride)
	for f := 0; f < m.nMels; f++ {
		for x := range stride {
			mel[f*stride+x] = float32(f*100 + x)
		}
	}
	m.alloc(m.freqPatches * 3)
	m.patchEmbed(mel, stride, 0, 3)

	want := []float32{0, 2, 4, 200, 202, 204}
	for n, w := range want {
		if got := m.buf.tokens[n*m.dim]; got != w {
			t.Errorf("token %d = %v, wanted %v (time-major would give %v)",
				n, got, w, []float32{0, 200, 2, 202, 4, 204}[n])
		}
	}
}

// **The time positional is sliced, not stretched.** It is the property that
// makes CED accept different durations: with three steps out of four available
// the first three are used. Stretching them would use the first, the second and
// the fourth, which gives no error and moves every token to a position that is
// not its own.
func TestTheTimePositionsAreSlicedNotStretched(t *testing.T) {
	m := tiny(t, 0)
	for pt := 0; pt < m.maxPatches; pt++ {
		m.timePos[0*m.maxPatches+pt] = float32(pt + 1)
	}
	mel := make([]float32, m.nMels*6)
	m.alloc(m.freqPatches * 3)
	m.patchEmbed(mel, 6, 0, 3)
	for pf := 0; pf < m.freqPatches; pf++ {
		for pt := range 3 {
			if got := m.buf.tokens[(pf*3+pt)*m.dim]; got != float32(pt+1) {
				t.Errorf("band %d step %d: position %v, wanted %v", pf, pt, got, pt+1)
			}
		}
	}
}

// The frequency positional is per band, and the same for every time step: it is
// the other half of the separability.
func TestTheFrequencyPositionIsThatOfTheBand(t *testing.T) {
	m := tiny(t, 0)
	for pf := 0; pf < m.freqPatches; pf++ {
		m.freqPos[0*m.freqPatches+pf] = float32(10 * (pf + 1))
	}
	mel := make([]float32, m.nMels*6)
	m.alloc(m.freqPatches * 3)
	m.patchEmbed(mel, 6, 0, 3)
	for pf := 0; pf < m.freqPatches; pf++ {
		for pt := range 3 {
			if got := m.buf.tokens[(pf*3+pt)*m.dim]; got != float32(10*(pf+1)) {
				t.Errorf("band %d step %d: %v, wanted %v", pf, pt, got, 10*(pf+1))
			}
		}
	}
}

// **A stretch that starts at t0 really does start at t0.** An off-by-one here
// shifts the whole window and produces nothing visible: the only symptom would
// be a slightly lower score.
func TestAChunkStartsWhereItIsToldTo(t *testing.T) {
	m := tiny(t, 0)
	m.patchW[0] = 1
	const stride = 8
	mel := make([]float32, m.nMels*stride)
	for f := 0; f < m.nMels; f++ {
		for x := range stride {
			mel[f*stride+x] = float32(x)
		}
	}
	m.alloc(m.freqPatches * 2)
	m.patchEmbed(mel, stride, 4, 2)
	if got := m.buf.tokens[0]; got != 4 {
		t.Errorf("the first token of the stretch starting at 4 is %v", got)
	}
	if got := m.buf.tokens[1*m.dim]; got != 6 {
		t.Errorf("the second is %v, wanted 6", got)
	}
}

// **The last stretch is padded with zeros at the tail, not at the head.**
// Padding at the head would put the sound at the end of the window, where the
// positionals are different ones: no error, a wrong score.
func TestThePaddedChunkKeepsTheTailAtTheFront(t *testing.T) {
	const nMels, frames, maxFrames = 3, 10, 8
	mel := make([]float32, nMels*frames)
	for b := range nMels {
		for t0 := range frames {
			mel[b*frames+t0] = float32(b*100 + t0)
		}
	}
	dst := make([]float32, nMels*maxFrames)
	for i := range dst {
		dst[i] = -1 // dirty, so it shows if it is not zeroed
	}
	padChunk(dst, mel, nMels, frames, 8, 2, maxFrames)
	for b := range nMels {
		row := dst[b*maxFrames : (b+1)*maxFrames]
		if row[0] != float32(b*100+8) || row[1] != float32(b*100+9) {
			t.Errorf("band %d: the tail is %v %v", b, row[0], row[1])
		}
		for i := 2; i < maxFrames; i++ {
			if row[i] != 0 {
				t.Errorf("band %d position %d: %v instead of zero", b, i, row[i])
			}
		}
	}
}

// **The two branches add to the residual, they do not replace it.** With the
// weights at zero and only the biases non-zero, what comes out of each branch
// is its bias and nothing else: so the output is the input plus two known
// constants.
//
// Zeroing the biases as well would **absolve a branch that was not added at
// all** — with a residual of zero, adding it and forgetting it are the same
// thing.
func TestTheTwoBranchesAddToTheResidual(t *testing.T) {
	m := tiny(t, 1)
	b := &m.blocks[0]
	for i := range b.projB {
		b.projB[i] = float32(i) + 1 // 1, 2, 3, 4
	}
	for i := range b.fc1B {
		b.fc1B[i] = 1 // gelu(1) = 0.8413, but fc2W is zero and does not look at it
	}
	for i := range b.fc2B {
		b.fc2B[i] = 10
	}
	const n = 4
	m.alloc(n)
	in := make([]float32, n*m.dim)
	for i := range in {
		in[i] = float32(i) - 5
	}
	copy(m.buf.tokens, in)
	m.block(b, n)
	for i := range in {
		want := in[i] + float32(i%m.dim) + 1 + 10
		if got := m.buf.tokens[i]; math.Abs(float64(got-want)) > 1e-5 {
			t.Fatalf("component %d: %v, wanted %v (input %v)", i, got, want, in[i])
		}
	}
}

// With all scores equal the attention is a mean: it is the case where the
// result is known in advance, and it catches a head reading another head's
// slice.
func TestFlatScoresMakeTheAttentionAMean(t *testing.T) {
	m := tiny(t, 0)
	const n = 3
	m.alloc(n)
	d, hd := m.dim, m.headDim
	for i := range m.buf.qkv[:n*3*d] {
		m.buf.qkv[i] = 0
	}
	// q and k stay at zero — the scores all come out equal. In v a different
	// number goes per token and per head.
	for j := range n {
		for h := 0; h < m.heads; h++ {
			for c := range hd {
				m.buf.qkv[j*3*d+2*d+h*hd+c] = float32((j+1)*10 + h)
			}
		}
	}
	m.attention(n)
	for i := range n {
		for h := 0; h < m.heads; h++ {
			want := float32(20 + h) // mean of 10, 20, 30 plus the head
			for c := range hd {
				if got := m.buf.ctx[i*d+h*hd+c]; math.Abs(float64(got-want)) > 1e-5 {
					t.Errorf("token %d head %d component %d: %v, wanted %v", i, h, c, got, want)
				}
			}
		}
	}
}

// **The buffers are reused between one window and the next**, and none of them
// must carry anything over: two identical windows with a different one in
// between have to give the same result to the bit.
func TestNothingSurvivesBetweenWindows(t *testing.T) {
	m := tiny(t, 2)
	// Non-zero weights, otherwise the test would walk through nothing.
	seed := uint32(9)
	rnd := func() float32 {
		seed = seed*1664525 + 1013904223
		return float32(seed>>8)/float32(1<<24) - 0.5
	}
	fill := func(s []float32) {
		for i := range s {
			s[i] = rnd()
		}
	}
	for i := range m.blocks {
		b := &m.blocks[i]
		fill(b.qkvW)
		fill(b.qkvB)
		fill(b.projW)
		fill(b.projB)
		fill(b.fc1W)
		fill(b.fc1B)
		fill(b.fc2W)
		fill(b.fc2B)
	}
	fill(m.patchW)
	fill(m.headW)
	fill(m.timePos)
	fill(m.freqPos)

	wave := func(n int, f float64) []float32 {
		w := make([]float32, n)
		for i := range w {
			w[i] = float32(math.Sin(float64(i) * f))
		}
		return w
	}
	a := wave(200, 0.3)
	b := wave(500, 0.11)
	first, err := m.Logits(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Logits(b); err != nil {
		t.Fatal(err)
	}
	second, err := m.Logits(a)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("class %d: %v the first time, %v the third", i, first[i], second[i])
		}
	}
	// And the long window goes through the branch that splits and pads, with no
	// NaN.
	long, err := m.Scores(wave(4000, 0.07))
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range long {
		if math.IsNaN(float64(v)) || v < 0 || v > 1 {
			t.Fatalf("class %d: %v", i, v)
		}
	}
}

func TestAWaveTooShortToFillOnePatchIsRefused(t *testing.T) {
	m := tiny(t, 1)
	// Frames(n) = 1 + n/hop: with hop 4 at least 2 frames are needed for one
	// patch.
	if _, err := m.Logits(make([]float32, 9)); err != nil {
		t.Errorf("nine samples make three frames and should have been enough: %v", err)
	}
	if _, err := m.Logits(make([]float32, 3)); err == nil {
		t.Error("three samples got through: they do not fill an eight sample pad")
	}
}

// **The advised window has to sit inside the window, not one sample outside.**
// Multiplying the frames by the hop comes out at 1013, that is, right into the
// hole that method exists to avoid: the test is not on the value — a value can
// be got wrong in two ways — but on the property, which is why it looks at the
// next sample too.
func TestTheAdvisedWindowIsTheLargestThatDoesNotSplit(t *testing.T) {
	m := tiny(t, 0)
	n := m.WindowSamples()
	if got := m.fe.Frames(n); got != m.maxFrames {
		t.Errorf("%d samples make %d frames, the window holds %d", n, got, m.maxFrames)
	}
	if got := m.fe.Frames(n + m.fe.hop); got <= m.maxFrames {
		t.Errorf("one hop more still makes %d frames: the window is not the largest one", got)
	}
	if got := m.WindowSeconds(); math.Abs(got-float64(n)/16000) > 1e-9 {
		t.Errorf("WindowSeconds = %v", got)
	}
}
