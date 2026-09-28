package detect

import (
	"math"
	"testing"
)

// **The accumulated block carries a crossing rate, and it used to carry a zero.**
//
// `Result` measured four of the Block's five numbers and left `CrossRate` at its
// zero value, which is not "not computed": multiplied by the rate it is **0 Hz**,
// that is, below the band `Sound.Feed` accepts — so an accumulated block handed
// to the gate would have refused every sound there is, silently and for ever.
// Nothing did that, which is what made it a trap rather than a defect, and it is
// the family this package already states twice: zero dBFS is full scale, and a
// meter that cannot measure does not draw silence.
func TestTheAccumulatedBlockMeasuresTheCrossingRate(t *testing.T) {
	// A square wave alternating sign every sample: every adjacent pair is a
	// crossing, so the rate is (n-1)/n over one block.
	const n = 64
	s := make([]int16, n)
	for i := range s {
		if i%2 == 0 {
			s[i] = 1000
		} else {
			s[i] = -1000
		}
	}

	var a Accumulator
	a.Add(s)
	got := a.Result().CrossRate
	if want := float64(n-1) / float64(n); math.Abs(got-want) > 1e-9 {
		t.Errorf("CrossRate %.4f, wanted %.4f: an accumulated block that reports no "+
			"crossings reports 0 Hz, which the gate reads as below the band", got, want)
	}
}

// **The crossing on a block boundary is counted once and not lost.** The
// accumulation is one stream, not a row of independent windows: `AnalyzeS16`
// starts each block afresh and cannot see across the join, which is exactly why
// the accumulator keeps the last non-zero sample.
func TestACrossingOnTheBlockBoundaryIsCounted(t *testing.T) {
	var a Accumulator
	a.Add([]int16{1000, 1000})
	a.Add([]int16{-1000, -1000})
	// Four samples, one sign change, and it falls between the two blocks.
	if got, want := a.Result().CrossRate, 1.0/4.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("CrossRate %.4f, wanted %.4f: the crossing between two blocks "+
			"was lost, so a tone split across blocks reads lower than it is", got, want)
	}

	// **And when the second block opens on an exact zero.** The join was looked
	// for on the block's first sample only, so a zero there hid it — and gated
	// or quiet signals are the ones full of zeros. Put back and watched failing.
	var b Accumulator
	b.Add([]int16{1000, 1000})
	b.Add([]int16{0, -1000})
	if got, want := b.Result().CrossRate, 1.0/4.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("CrossRate %.4f, wanted %.4f: a block opening on a zero hid the join", got, want)
	}
}

// And the rule the per-block analysis already keeps: an exact zero is not a
// crossing. On a muted path — which delivers almost nothing else — counting it
// would produce one every two samples, that is, the signature of a very high
// frequency in place of silence.
func TestAnExactZeroIsNotACrossing(t *testing.T) {
	var a Accumulator
	a.Add([]int16{1000, 0, 1000, 0, 1000})
	if got := a.Result().CrossRate; got != 0 {
		t.Errorf("CrossRate %.4f on a signal that never changes sign: the zeros "+
			"were counted as crossings, which is what a muted path looks like", got)
	}
}

// The empty accumulator declares silence rather than a measurement, which is the
// same rule one field across.
func TestAnEmptyAccumulatorDeclaresSilence(t *testing.T) {
	var a Accumulator
	got := a.Result()
	if got.Samples != 0 || got.RMSdBFS != SilenceFloorDBFS {
		t.Errorf("an empty accumulator answered %+v", got)
	}
}

// **A stream cut anywhere reads as the stream whole.** The accumulation joins
// the blocks it is handed, and wherever the cut falls — before the first
// non-zero sample, inside a run of zeros, on a sign change, next to MinInt16 —
// joining the two halves has to give the very totals one walk over the whole
// gives, field by field. The crossing on the boundary is the one term only join
// can count: with it removed, this was watched failing.
func TestJoiningTheHalvesGivesTheWhole(t *testing.T) {
	vectors := map[string][]int16{
		"empty":         {},
		"all zeros":     {0, 0, 0, 0, 0},
		"leading zeros": {0, 0, 0, 1000, -1000, 500},
		"zeros inside":  {1000, 0, 0, -1000, 0, 1000, 0},
		"alternation":   {1000, -1000, 1000, -1000, 1000, -1000},
		"extremes":      {math.MinInt16, 0, math.MaxInt16, math.MinInt16, -1, 1},
		"one sample":    {-7},
	}
	for name, v := range vectors {
		whole := sumsOf(v)
		for k := 0; k <= len(v); k++ {
			joined := sumsOf(v[:k])
			joined.join(sumsOf(v[k:]))
			if joined != whole {
				t.Errorf("%s split at %d: joined %+v, whole %+v", name, k, joined, whole)
			}
			var a Accumulator
			a.Add(v[:k])
			a.Add(v[k:])
			if got, want := a.Result(), AnalyzeS16(v); got != want {
				t.Errorf("%s split at %d: accumulated %+v, analysed whole %+v", name, k, got, want)
			}
		}
	}
}
