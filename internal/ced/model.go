// Package ced runs CED — Consistent Ensemble Distillation — a sound classifier
// trained on AudioSet, which of a recording says which of the 527 classes it
// hears inside. Among them are "Baby cry, infant cry" and "Bark", which are the
// two sounds this program exists for.
//
// **It runs in pure Go and brings nothing in.** The beaten path would be ONNX
// Runtime, and it was measured and dropped: 108-127 MB to download to obtain a
// 12 MB DLL, to run a 6 MB model with, and on this machine DirectML came out
// **slower** than the CPU (2.42 ms against 1.80 on YAMNet). A matrix multiplier
// written here does 7.1-7.6 GFLOPS on one core, which on this model is ~500 ms
// per ten-second window.
//
// **The model is not ours**: the weights are mispeech/ced's (Xiaomi),
// Apache-2.0, and they travel in a GGUF file that carries the front-end with
// them — the window and the filterbank it was trained with. See internal/gguf.
//
// **This package decides, it does not watch.** It sits downstream of the shape
// detector in internal/detect, which stays the gate: that one looks at every
// block and costs nothing, this one wakes up when the other says there is
// something. Running it continuously would mean half a second of CPU every ten,
// all night, for a room that is still 99% of the time.
package ced

import (
	"fmt"
	"math"

	"patmonitor/internal/gguf"
)

// block is one of the transformer's twelve layers.
type block struct {
	norm1W, norm1B []float32
	qkvW, qkvB     []float32
	projW, projB   []float32
	norm2W, norm2B []float32
	fc1W, fc1B     []float32
	fc2W, fc2B     []float32
}

// scratch are the working buffers, kept between calls.
type scratch struct {
	tokens []float32 // [N][D] what flows between the blocks
	normed []float32 // [N][D] the normalised input of each sublayer
	qkv    []float32 // [N][3D]
	ctx    []float32 // [N][D] the attention's output, before the projection
	res    []float32 // [N][D] the branch to add to the residual
	hidden []float32 // [N][4D]
	scores []float32 // [N] one attention row at a time
	pooled []float32 // [D]
	logits []float32 // [classes]
	padded []float32 // [nMels][maxFrames] for the last piece, when it is split
}

// Model is CED loaded into memory.
//
// **The weights are converted to float32 on opening, not on every use**, and
// from that follows something to know before choosing which file to ship: in
// memory the f16 variant and the q8_0 cost the same, about 22 MB. The
// quantised one saves in the file — how much the binary or the download weighs
// — not in how much it takes up while running.
//
// **It is not reentrant**: the buffers are its own. One per goroutine, or one
// and a queue.
type Model struct {
	fe     *Frontend
	labels []string
	index  map[string]int
	rate   int

	dim, heads, headDim, mlpDim int
	patch                       int
	nMels, freqPatches          int
	maxFrames, maxPatches       int

	epsBN, epsEnc, epsHead float32

	bnScale, bnShift     []float32
	patchW, patchB       []float32
	timePos, freqPos     []float32
	blocks               []block
	normW, normB         []float32
	headNormW, headNormB []float32
	headW, headB         []float32

	buf scratch
}

// readTensor reads a tensor and checks its size.
//
// **The shape is checked on opening**, where getting it wrong is an error with
// a name: further on, a tensor of the wrong size does not fail, it produces
// numbers. The count is on the total values and not on the individual
// dimensions because GGUF writes them in the opposite order to the paper's, and
// comparing them one by one would mean choosing a direction and remembering it
// in a hundred and sixty places.
func readTensor(f *gguf.File, name string, want ...int) ([]float32, error) {
	t, err := f.Tensor(name)
	if err != nil {
		return nil, err
	}
	n := 1
	for _, d := range want {
		n *= d
	}
	if t.Count() != n {
		return nil, fmt.Errorf("ced: %s holds %d values, the shape %v wants %d", name, t.Count(), want, n)
	}
	return t.F32()
}

// New loads the model from a GGUF file already read.
func New(f *gguf.File) (*Model, error) {
	geti := func(k string) int {
		v, err := f.Int(k)
		if err != nil {
			return -1
		}
		return v
	}
	getf := func(k string) float64 {
		v, err := f.Float(k)
		if err != nil {
			return math.NaN()
		}
		return v
	}

	m := &Model{
		dim:       geti("ced.embed_dim"),
		heads:     geti("ced.num_heads"),
		patch:     geti("ced.patch_size"),
		nMels:     geti("ced.n_mels"),
		maxFrames: geti("ced.target_length"),
		rate:      geti("ced.sample_rate"),
		epsBN:     float32(getf("ced.bn_eps")),
		epsEnc:    float32(getf("ced.ln_eps_encoder")),
		epsHead:   float32(getf("ced.ln_eps_head")),
	}
	depth := geti("ced.depth")
	nFFT := geti("ced.n_fft")
	hop := geti("ced.hop_size")
	classes := geti("ced.outputdim")
	stride := geti("ced.patch_stride")
	ratio := getf("ced.mlp_ratio")

	for k, v := range map[string]int{
		"ced.embed_dim": m.dim, "ced.num_heads": m.heads, "ced.patch_size": m.patch,
		"ced.n_mels": m.nMels, "ced.target_length": m.maxFrames, "ced.sample_rate": m.rate,
		"ced.depth": depth, "ced.n_fft": nFFT, "ced.hop_size": hop, "ced.outputdim": classes,
		"ced.patch_stride": stride,
	} {
		if v <= 0 {
			return nil, fmt.Errorf("ced: metadata %s is missing or not positive", k)
		}
	}
	if math.IsNaN(float64(m.epsBN)) || math.IsNaN(float64(m.epsEnc)) ||
		math.IsNaN(float64(m.epsHead)) || math.IsNaN(ratio) {
		return nil, fmt.Errorf("ced: an epsilon or the mlp ratio is missing")
	}
	// **A stride different from the patch side is something we cannot do**, and
	// it is worth saying so rather than producing a grid that overlaps in
	// silence.
	if stride != m.patch {
		return nil, fmt.Errorf("ced: patch stride %d differs from patch size %d", stride, m.patch)
	}
	if m.dim%m.heads != 0 {
		return nil, fmt.Errorf("ced: %d dimensions do not divide into %d heads", m.dim, m.heads)
	}
	if m.nMels%m.patch != 0 {
		return nil, fmt.Errorf("ced: %d mel bands do not divide into patches %d wide", m.nMels, m.patch)
	}
	m.headDim = m.dim / m.heads
	m.mlpDim = int(ratio * float64(m.dim))
	m.freqPatches = m.nMels / m.patch
	m.maxPatches = m.maxFrames / m.patch

	var err error
	if m.labels, err = f.Strings("ced.labels"); err != nil {
		return nil, err
	}
	if len(m.labels) != classes {
		return nil, fmt.Errorf("ced: %d labels for %d classes", len(m.labels), classes)
	}
	if m.index, err = indexLabels(m.labels); err != nil {
		return nil, err
	}

	nFreqs := nFFT/2 + 1
	// The bank arrives with its dimensions written the other way round, ne =
	// [257 64], that is, one row per band: it is already the order the
	// front-end wants.
	window, err := readTensor(f, "ced.mel_window", nFFT)
	if err != nil {
		return nil, err
	}
	bank, err := readTensor(f, "ced.mel_filterbank", m.nMels, nFreqs)
	if err != nil {
		return nil, err
	}
	m.fe, err = NewFrontend(nFFT, hop, m.nMels, window, bank,
		getf("ced.a2db_amin"), getf("ced.a2db_ref"),
		getf("ced.a2db_multiplier"), getf("ced.a2db_top_db"))
	if err != nil {
		return nil, err
	}

	// The initial normalisation reduces to one scale and one shift per band:
	// they are frozen statistics, so the sum is done once here instead of over
	// sixty-four thousand values on every window.
	bnW, e1 := readTensor(f, "encoder.init_bn.weight", m.nMels)
	bnB, e2 := readTensor(f, "encoder.init_bn.bias", m.nMels)
	bnMean, e3 := readTensor(f, "encoder.init_bn.running_mean", m.nMels)
	bnVar, e4 := readTensor(f, "encoder.init_bn.running_var", m.nMels)
	if err := firstError(e1, e2, e3, e4); err != nil {
		return nil, err
	}
	m.bnScale = make([]float32, m.nMels)
	m.bnShift = make([]float32, m.nMels)
	for i := range m.bnScale {
		inv := float32(1 / math.Sqrt(float64(bnVar[i]+m.epsBN)))
		m.bnScale[i] = bnW[i] * inv
		m.bnShift[i] = bnB[i] - bnMean[i]*bnW[i]*inv
	}

	var errs []error
	get := func(name string, want ...int) []float32 {
		v, err := readTensor(f, name, want...)
		errs = append(errs, err)
		return v
	}
	m.patchW = get("encoder.patch_embed.proj.weight", m.dim, m.patch, m.patch)
	m.patchB = get("encoder.patch_embed.proj.bias", m.dim)
	m.timePos = get("encoder.time_pos_embed", m.dim, m.maxPatches)
	m.freqPos = get("encoder.freq_pos_embed", m.dim, m.freqPatches)
	m.normW = get("encoder.norm.weight", m.dim)
	m.normB = get("encoder.norm.bias", m.dim)
	m.headNormW = get("outputlayer.0.weight", m.dim)
	m.headNormB = get("outputlayer.0.bias", m.dim)
	m.headW = get("outputlayer.1.weight", classes, m.dim)
	m.headB = get("outputlayer.1.bias", classes)

	m.blocks = make([]block, depth)
	for i := range m.blocks {
		p := fmt.Sprintf("encoder.blocks.%d.", i)
		b := &m.blocks[i]
		b.norm1W = get(p+"norm1.weight", m.dim)
		b.norm1B = get(p+"norm1.bias", m.dim)
		b.qkvW = get(p+"attn.qkv.weight", 3*m.dim, m.dim)
		b.qkvB = get(p+"attn.qkv.bias", 3*m.dim)
		b.projW = get(p+"attn.proj.weight", m.dim, m.dim)
		b.projB = get(p+"attn.proj.bias", m.dim)
		b.norm2W = get(p+"norm2.weight", m.dim)
		b.norm2B = get(p+"norm2.bias", m.dim)
		b.fc1W = get(p+"mlp.fc1.weight", m.mlpDim, m.dim)
		b.fc1B = get(p+"mlp.fc1.bias", m.mlpDim)
		b.fc2W = get(p+"mlp.fc2.weight", m.dim, m.mlpDim)
		b.fc2B = get(p+"mlp.fc2.bias", m.dim)
	}
	if err := firstError(errs...); err != nil {
		return nil, err
	}
	return m, nil
}

func firstError(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// indexLabels builds the index by name and **refuses repeated names**.
//
// Whoever picks the classes to watch names them, because a number between 0 and
// 526 written in the code tells nobody what is being watched. But naming means
// that two classes with the same name become one, silently, and nobody looks at
// the second one any more.
//
// **And that is not hypothetical.** In HuggingFace's config.json the labels are
// the short ones, and there "Inside" appears **three times** and "Outside"
// twice: 524 distinct names for 527 classes. In the GGUF they are AudioSet's
// full ones — "Inside, small room", "Inside, large room or hall" — and all 527
// are different, but the difference is in which list whoever converted the
// model used. A file converted from the other list loads, runs, and watches one
// class fewer.
func indexLabels(labels []string) (map[string]int, error) {
	idx := make(map[string]int, len(labels))
	for i, l := range labels {
		if j, dup := idx[l]; dup {
			return nil, fmt.Errorf("ced: classes %d and %d are both named %q, "+
				"so naming one of them is ambiguous", j, i, l)
		}
		idx[l] = i
	}
	return idx, nil
}

// Labels are the class names, in the order of the scores.
func (m *Model) Labels() []string { return m.labels }

// Index is a class's position among the scores, looked up by name.
//
// The name is AudioSet's full one — "Baby cry, infant cry", not "Baby cry" —
// and a name that is not there is an error and not just any index: a class
// silently got wrong produces a monitor watching the wrong thing without saying
// so.
func (m *Model) Index(label string) (int, error) {
	i, ok := m.index[label]
	if !ok {
		return 0, fmt.Errorf("ced: no class named %q", label)
	}
	return i, nil
}

// SampleRate is the rate the model was trained at. **It is not a suggestion**:
// the front-end measures in frequency bins, and a stream at a different rate
// shifts every mel band with nothing to say so.
func (m *Model) SampleRate() int { return m.rate }

// WindowSamples is how many samples are best delivered at once: the window the
// model saw in training, 10.12 seconds.
//
// **Going a little over it costs half the score, and it is a narrow hole.**
// Above the window the piece is split, and the last stretch is padded with
// zeros up to the whole size and averaged with the others **at equal weight**:
// measured on the real model, a 440 Hz sine is worth 0.93 at five seconds, 0.89
// at fifteen, and **0.47 at 10.12**, where the second piece is one frame of
// sound and 1011 of silence. It is not a defect of ours — it is what the
// original model does, and changing it would mean parting from the oracle — but
// from here on windows of this size are what get delivered, not "about ten
// seconds".
//
// **The sum is `(maxFrames-1)*hop`, and writing `maxFrames*hop`** is one sample
// too many, which leads to 1013 frames, that is, right into the hole this
// method exists to avoid. The centred padding adds a whole window, so the
// number of frames is `1 + n/hop` — that `1 +` has to come off going back, and
// it is not visible when writing the multiplication. What found it was the
// **time**: 874 ms instead of 482, that is, two passes instead of one, in a
// measurement where the scores were what was being watched.
func (m *Model) WindowSamples() int { return (m.maxFrames - 1) * m.fe.hop }

// WindowSeconds is the same thing in seconds.
func (m *Model) WindowSeconds() float64 {
	return float64(m.WindowSamples()) / float64(m.rate)
}

// Scores returns one probability per class.
//
// **They are independent probabilities, not a distribution**: the last layer is
// a per-class sigmoid and not a softmax, so they do not sum to one and there is
// no "winning class". It is the right shape for what we need — a recording can
// be a dog and a room and a television left on all at once — and whoever takes
// the maximum of them is asking the wrong question.
func (m *Model) Scores(wave []float32) ([]float32, error) {
	out, err := m.Logits(wave)
	if err != nil {
		return nil, err
	}
	sigmoid(out)
	return out, nil
}

// Logits is the same thing before the sigmoid. It exists because that is where
// the comparison with the oracle happens: on the probability the deviations get
// squashed near zero and one, that is, exactly where a defect would stop
// showing.
func (m *Model) Logits(wave []float32) ([]float32, error) {
	mel, frames, err := m.fe.Mel(wave)
	if err != nil {
		return nil, err
	}
	if frames < m.patch {
		return nil, fmt.Errorf("ced: %d frames are fewer than one %d frame patch", frames, m.patch)
	}
	m.batchNorm(mel, frames)

	out := make([]float32, len(m.labels))

	// **Below the training window nothing is padded**: what there is gets
	// encoded and just as many time positions are used. It is what the original
	// model does, and it is also why the two positional embeddings are
	// separable — the time one gets cut. Padding with zeros to reach 1012 would
	// mean feeding it seconds of silence that are not there.
	if frames <= m.maxFrames {
		m.forward(mel, frames, 0, frames/m.patch)
		copy(out, m.buf.logits)
		return out, nil
	}

	// Above it, the signal is split into whole windows and **the last one is
	// padded with zeros**, because that is how the model was evaluated. The
	// logits are averaged: it is before the sigmoid, which is the only point
	// where an average of two judgements means anything.
	chunks := 0
	for t0 := 0; t0 < frames; t0 += m.maxFrames {
		if width := frames - t0; width >= m.maxFrames {
			m.forward(mel, frames, t0, m.maxPatches)
		} else {
			m.buf.padded = grow(m.buf.padded, m.nMels*m.maxFrames)
			pad := m.buf.padded[:m.nMels*m.maxFrames]
			padChunk(pad, mel, m.nMels, frames, t0, width, m.maxFrames)
			m.forward(pad, m.maxFrames, 0, m.maxPatches)
		}
		for i, v := range m.buf.logits {
			out[i] += v
		}
		chunks++
	}
	inv := 1 / float32(chunks)
	for i := range out {
		out[i] *= inv
	}
	return out, nil
}

// padChunk copies into dst the stretch [t0, t0+width) of a spectrogram `frames`
// wide, and pads with zeros up to maxFrames.
//
// It is three lines and it lives in a function of its own because it is exactly
// the place an off-by-one lives: inside the loop that splits, it would only
// show as a score lower than it should be, that is, it would not show.
func padChunk(dst, mel []float32, nMels, frames, t0, width, maxFrames int) {
	for i := range dst {
		dst[i] = 0
	}
	for b := 0; b < nMels; b++ {
		copy(dst[b*maxFrames:], mel[b*frames+t0:b*frames+t0+width])
	}
}

// batchNorm applies the initial normalisation to the spectrogram, in place.
// **It is per mel band, not per frame**: in the original model the band is the
// channel, and taking it the other way round would normalise every instant
// against itself — which is a sensible thing, a different thing, and not the
// one the model was trained with.
func (m *Model) batchNorm(mel []float32, frames int) {
	for b := 0; b < m.nMels; b++ {
		scale, shift := m.bnScale[b], m.bnShift[b]
		row := mel[b*frames : (b+1)*frames]
		for i, v := range row {
			row[i] = v*scale + shift
		}
	}
}

// forward sends a piece of spectrogram through the network and leaves the
// logits in m.buf.logits. `stride` is the length of a row of `mel`, `t0` where
// the piece begins, `tp` how many time steps fit in it.
func (m *Model) forward(mel []float32, stride, t0, tp int) {
	n := m.freqPatches * tp
	m.alloc(n)
	m.patchEmbed(mel, stride, t0, tp)
	for i := range m.blocks {
		m.block(&m.blocks[i], n)
	}
	x := m.buf.tokens[:n*m.dim]
	layerNorm(x, m.normW, m.normB, n, m.dim, m.epsEnc)

	// **There are two normalisations, one on each side of the average**, and
	// that is how it is in the original model: encoder.norm first, with its own
	// epsilon, and outputlayer.0 after, with another. Two in a row look like
	// one too many and are not — the one after the average works on a vector
	// the others have never seen. The two different epsilons are the
	// confirmation that there are two layers there and not one doubled by
	// oversight.
	pooled := m.buf.pooled[:m.dim]
	for i := range pooled {
		pooled[i] = 0
	}
	for i := 0; i < n; i++ {
		for k, v := range x[i*m.dim : (i+1)*m.dim] {
			pooled[k] += v
		}
	}
	inv := 1 / float32(n)
	for i := range pooled {
		pooled[i] *= inv
	}
	layerNorm(pooled, m.headNormW, m.headNormB, 1, m.dim, m.epsHead)
	linear(m.buf.logits, pooled, m.headW, m.headB, 1, m.dim, len(m.labels))
}

// patchEmbed is the 16x16 convolution with stride 16, plus the two positional
// embeddings.
//
// **The positionals are two and separable**, one for time and one for
// frequency, summed on the grid before flattening it: that is what makes CED a
// model that accepts different durations, because the time one is cut to the
// length needed instead of being interpolated.
//
// **The flattening is frequency-first**: the token is `pf*tp + pt`. Turning it
// round breaks nothing visible — the same tokens with the same values remain,
// in a different order — and the attention is not order-invariant because the
// positionals are already inside: out would come a model that runs and answers
// at random.
func (m *Model) patchEmbed(mel []float32, stride, t0, tp int) {
	p := m.patch
	for pf := 0; pf < m.freqPatches; pf++ {
		for pt := 0; pt < tp; pt++ {
			n := pf*tp + pt
			tok := m.buf.tokens[n*m.dim : (n+1)*m.dim]
			for d := range tok {
				w := m.patchW[d*p*p : (d+1)*p*p]
				s := m.patchB[d] + m.timePos[d*m.maxPatches+pt] + m.freqPos[d*m.freqPatches+pf]
				for kf := 0; kf < p; kf++ {
					row := mel[(pf*p+kf)*stride+t0+pt*p:]
					s += dot(w[kf*p:(kf+1)*p], row[:p])
				}
				tok[d] = s
			}
		}
	}
}

// block is one turn of the transformer: attention and perceptron, each preceded
// by a normalisation and added to the residual.
func (m *Model) block(b *block, n int) {
	d := m.dim
	x := m.buf.tokens[:n*d]
	normed := m.buf.normed[:n*d]
	res := m.buf.res[:n*d]

	copy(normed, x)
	layerNorm(normed, b.norm1W, b.norm1B, n, d, m.epsEnc)
	linear(m.buf.qkv[:n*3*d], normed, b.qkvW, b.qkvB, n, d, 3*d)
	m.attention(n)
	linear(res, m.buf.ctx[:n*d], b.projW, b.projB, n, d, d)
	for i, v := range res {
		x[i] += v
	}

	copy(normed, x)
	layerNorm(normed, b.norm2W, b.norm2B, n, d, m.epsEnc)
	hidden := m.buf.hidden[:n*m.mlpDim]
	linear(hidden, normed, b.fc1W, b.fc1B, n, d, m.mlpDim)
	gelu(hidden)
	linear(res, hidden, b.fc2W, b.fc2B, n, m.mlpDim, d)
	for i, v := range res {
		x[i] += v
	}
}

// attention reads from m.buf.qkv and writes into m.buf.ctx.
//
// **The three projections sit in a single tensor**, and PyTorch's order decides
// where: the output vector is [q|k|v] and inside each one the heads come one
// after another, so a head's slice is contiguous and there is nothing to
// reorder. The row of scores is normalised straight away and only one is kept:
// keeping the whole matrix would cost a quarter of a megabyte never to look at
// it again.
func (m *Model) attention(n int) {
	d, hd := m.dim, m.headDim
	scale := float32(1 / math.Sqrt(float64(hd)))
	ctx := m.buf.ctx[:n*d]
	for i := range ctx {
		ctx[i] = 0
	}
	row := m.buf.scores[:n]
	for h := 0; h < m.heads; h++ {
		off := h * hd
		for i := 0; i < n; i++ {
			q := m.buf.qkv[i*3*d+off : i*3*d+off+hd]
			for j := 0; j < n; j++ {
				k := m.buf.qkv[j*3*d+d+off : j*3*d+d+off+hd]
				row[j] = dot(q, k) * scale
			}
			softmax(row)
			out := ctx[i*d+off : i*d+off+hd]
			for j, p := range row {
				for c, v := range m.buf.qkv[j*3*d+2*d+off : j*3*d+2*d+off+hd] {
					out[c] += p * v
				}
			}
		}
	}
}

func (m *Model) alloc(n int) {
	d := m.dim
	m.buf.tokens = grow(m.buf.tokens, n*d)
	m.buf.normed = grow(m.buf.normed, n*d)
	m.buf.qkv = grow(m.buf.qkv, n*3*d)
	m.buf.ctx = grow(m.buf.ctx, n*d)
	m.buf.res = grow(m.buf.res, n*d)
	m.buf.hidden = grow(m.buf.hidden, n*m.mlpDim)
	m.buf.scores = grow(m.buf.scores, n)
	m.buf.pooled = grow(m.buf.pooled, d)
	m.buf.logits = grow(m.buf.logits, len(m.labels))[:len(m.labels)]
}

func grow(s []float32, n int) []float32 {
	if cap(s) < n {
		return make([]float32, n)
	}
	return s[:cap(s)]
}
