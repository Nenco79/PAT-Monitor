package resample

// Stream is the same resampler, in blocks.
//
// **It exists because the analysis stream arrives fifty times a second**, and
// Convert assumes it sees the whole signal: called on each block, every block
// would be treated as if silence came before and after it, and at the two edges
// the filter would produce a step. Fifty steps a second are a hum at the
// block's frequency — audible, and inside the band a cry is looked for at that.
//
// What is kept between calls is the **tail** of samples the filter still needs,
// and the absolute position reached so far: the two things Convert can afford
// to ignore and this cannot.
type Stream struct {
	r *Rational
	// hist are the samples still useful, and from is the absolute index of the
	// first one.
	hist []float32
	from int64
	// n is the next output sample to produce. **Absolute from the start of the
	// stream**: counting it within the block would restart the phase from zero
	// on every call, and the phase is exactly what keeps time aligned.
	n int64
	// seen is how many samples have come in altogether.
	seen int64
	out  []float32
}

// NewStream opens the conversion in blocks.
func NewStream(from, to int) (*Stream, error) {
	r, err := New(from, to)
	if err != nil {
		return nil, err
	}
	return &Stream{r: r}, nil
}

// Write consumes a block and returns the samples that can already be produced.
// The result **is valid until the next call**: the buffer is reused, because
// this function runs fifty times a second.
//
// Fewer come out than the ratio would suggest until the tail is full: the
// filter looks ahead, so the first output sample only comes out once enough
// input has arrived to cover half its kernel.
func (s *Stream) Write(in []float32) []float32 {
	s.hist = append(s.hist, in...)
	s.seen += int64(len(in))
	k := int64(s.r.k)
	last := s.seen - 1 // absolute index of the last sample in

	s.out = s.out[:0]
	for {
		q := s.n * int64(s.r.m) / int64(s.r.l)
		if q+k > last {
			break
		}
		phase := (s.n * int64(s.r.m)) % int64(s.r.l)
		h := s.r.bank[phase]
		var acc float32
		lo, hi := q-k, q+k
		if lo < 0 {
			lo = 0 // before the start the signal is zero, as in Convert
		}
		for i := lo; i <= hi; i++ {
			acc += s.hist[i-s.from] * h[int(i-q+k)]
		}
		s.out = append(s.out, acc)
		s.n++
	}

	// Throw away what will not be needed again: the next output sample starts
	// at q-k, and nothing before that is ever looked at again.
	q := s.n * int64(s.r.m) / int64(s.r.l)
	keep := q - k
	if keep < 0 {
		keep = 0
	}
	if d := keep - s.from; d > 0 {
		s.hist = append(s.hist[:0], s.hist[d:]...)
		s.from = keep
	}
	return s.out
}
