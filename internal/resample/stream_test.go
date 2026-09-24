package resample

import (
	"math"
	"math/rand"
	"testing"
)

// **The test that matters is that block by block gives what all at once
// gives.** A block resampler goes wrong in one way only — it loses the state
// between calls — and the symptom is a step at the edge of every block, that
// is, a hum at the block's frequency. It raises no error, and it falls inside
// the band a cry is looked for in.
func TestBlockByBlockGivesTheSameAsAllAtOnce(t *testing.T) {
	for _, c := range []struct{ from, to int }{
		{44100, 16000}, {24000, 16000}, {48000, 16000}, {16000, 48000}, {16000, 16000},
	} {
		r, err := New(c.from, c.to)
		if err != nil {
			t.Fatal(err)
		}
		in := make([]float32, c.from) // one second
		rnd := rand.New(rand.NewSource(int64(c.from)))
		for i := range in {
			in[i] = float32(math.Sin(2*math.Pi*440*float64(i)/float64(c.from)) * 0.5)
			in[i] += float32(rnd.NormFloat64() * 0.01)
		}
		whole := r.Convert(in)

		// **The blocks are of different sizes on purpose**, and one is empty:
		// the pipeline delivers whatever WASAPI hands it, which is not a round
		// number, and a resampler that only worked on equal blocks would break
		// on the first machine with a different buffer.
		s, err := NewStream(c.from, c.to)
		if err != nil {
			t.Fatal(err)
		}
		var got []float32
		// The loop advances because every turn holds non-zero sizes: the empty
		// block is one of the cases to exercise, not an exit condition. Exiting
		// on it would consume a third of the signal and then blame the code for
		// losing the tail.
		sizes := []int{0, 1, 160, 1600, 37, 4096, 999}
		for i, k := 0, 0; i < len(in); k++ {
			n := sizes[k%len(sizes)]
			if i+n > len(in) {
				n = len(in) - i
			}
			got = append(got, s.Write(in[i:i+n])...)
			i += n
		}
		// Streaming, where the signal ends is not known, so the tail — the last
		// half kernel — does not come out: the prefix is what gets compared.
		if len(got) > len(whole) {
			t.Fatalf("%d->%d: %d samples in blocks, %d whole", c.from, c.to, len(got), len(whole))
		}
		if d := len(whole) - len(got); d > 2*r.k*c.to/c.from+2 {
			t.Errorf("%d->%d: %d samples missing at the tail, too many", c.from, c.to, d)
		}
		for i := range got {
			if math.Abs(float64(got[i]-whole[i])) > 1e-6 {
				t.Fatalf("%d->%d: sample %d, in blocks %v, whole %v",
					c.from, c.to, i, got[i], whole[i])
			}
		}
	}
}

// The tail must not grow without end: this is a stream that runs all night, and
// a slice that lengthens with every block is a slow leak.
func TestTheHistoryDoesNotGrow(t *testing.T) {
	s, err := NewStream(44100, 16000)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float32, 4410) // a tenth of a second
	var maxHist int
	for range 600 { // one minute
		s.Write(in)
		if len(s.hist) > maxHist {
			maxHist = len(s.hist)
		}
	}
	// The kernel is 2k+1, and the tail must not exceed it by more than a block.
	if want := 2*s.r.k + 1 + len(in); maxHist > want {
		t.Errorf("the tail reached %d samples, the kernel plus a block is %d",
			maxHist, want)
	}
}

// A tone has to come out at the same frequency and the same level in blocks
// too: this is the proof that the phase does not restart from zero on every
// call.
func TestATonePassesThroughABlockedStream(t *testing.T) {
	const from, to = 44100, 16000
	s, err := NewStream(from, to)
	if err != nil {
		t.Fatal(err)
	}
	in := tone(1000, from, from*2)
	var got []float32
	for i := 0; i < len(in); i += 441 {
		got = append(got, s.Write(in[i:i+441])...)
	}
	if amp := measure(got, 1000, to); math.Abs(amp-1) > 0.02 {
		t.Errorf("amplitude %.4f instead of 1", amp)
	}
}

func TestABadRateIsRefusedByTheStreamToo(t *testing.T) {
	if _, err := NewStream(0, 16000); err == nil {
		t.Error("a zero rate got through")
	}
}
