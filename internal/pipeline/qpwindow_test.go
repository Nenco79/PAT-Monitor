package pipeline

import "testing"

// seeQP records n frames with the same quantiser.
func seeQP(p *Pipeline, qp, n int) {
	for i := 0; i < n; i++ {
		p.qpSeen(qp)
	}
}

// TestAnEmptyWindowInventsNothing: with no samples there is no answer. An absent
// measurement is not a measurement of zero — were it one, the scale would read a
// perfect quantiser exactly when the encoder is producing nothing.
func TestAnEmptyWindowInventsNothing(t *testing.T) {
	p := New(Config{})
	if qp, ok := p.TakeRecentQP(); ok {
		t.Errorf("TakeRecentQP() = %d, true; want not known", qp)
	}
}

// TestTakingEmptiesTheWindow: two takes in a row and the second knows nothing.
// It is the property that makes the window "the last second" rather than
// "everything since the start" — the same mistake the cadence already made.
func TestTakingEmptiesTheWindow(t *testing.T) {
	p := New(Config{})
	seeQP(p, 30, 25)

	if _, ok := p.TakeRecentQP(); !ok {
		t.Fatal("the first take did not see the samples")
	}
	if qp, ok := p.TakeRecentQP(); ok {
		t.Errorf("the second take answered %d; the window was meant to be empty", qp)
	}
}

// TestAKeyframeDoesNotCommandTheScale is the reason the percentile is 90 and not
// 95.
//
// A second of encoding is ~25 frames, of which exactly one is the keyframe, paid
// for with a much worse quantiser by construction. With the p95 over 25 samples
// the threshold falls on the last sample, that is on that very keyframe, and the
// scale would start following a frame that says nothing about how hard the scene
// is.
func TestAKeyframeDoesNotCommandTheScale(t *testing.T) {
	p := New(Config{})
	seeQP(p, 28, 24) // the scene
	seeQP(p, 45, 1)  // the keyframe

	qp, ok := p.TakeRecentQP()
	if !ok {
		t.Fatal("no samples")
	}
	if qp != 28 {
		t.Errorf("TakeRecentQP() = %d; want 28, the scene and not the keyframe", qp)
	}
}

// TestTheTailIsStillVisible: the p90 damps the isolated sample, not the tail. If
// one frame in five is bad it is no longer a keyframe, it is the scene — and the
// scale has to see that one, otherwise there was no point putting it there.
func TestTheTailIsStillVisible(t *testing.T) {
	p := New(Config{})
	seeQP(p, 28, 20)
	seeQP(p, 42, 5)

	qp, ok := p.TakeRecentQP()
	if !ok {
		t.Fatal("no samples")
	}
	if qp != 42 {
		t.Errorf("TakeRecentQP() = %d; want 42: a fifth of the frames at 42 is the scene, not noise", qp)
	}
}

// TestWorseThanTheMean: on the distribution actually measured here — CBR on this
// machine, mean 29.9 and maximum 43 — the percentile has to sit above the mean.
// That is the whole reason for the change: a mean below the break threshold can
// hide the frames that sit above it.
func TestWorseThanTheMean(t *testing.T) {
	p := New(Config{})
	seeQP(p, 29, 20)
	seeQP(p, 33, 3)
	seeQP(p, 39, 1)
	seeQP(p, 43, 1)

	qp, ok := p.TakeRecentQP()
	if !ok {
		t.Fatal("no samples")
	}
	// The mean of this distribution is ~30.
	if qp <= 30 {
		t.Errorf("TakeRecentQP() = %d; want above the mean (~30): the percentile must see the tail", qp)
	}
}

// TestFewSamplesDoNotInvertTheQuestion: with very few frames in the window the
// truncation of the threshold would give zero, and a cumulative count starting
// from zero is already satisfied by the first value of the histogram — that is,
// it would answer with the **best** quantiser exactly when the worst is being
// asked for. It happens on every encoder restart, where the first takes have a
// handful of frames.
func TestFewSamplesDoNotInvertTheQuestion(t *testing.T) {
	cases := []struct {
		name string
		good int
		bad  int
		want int
	}{
		{"a single sample", 0, 1, 40},
		{"two samples", 1, 1, 40},
		{"three samples", 2, 1, 40},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := New(Config{})
			seeQP(p, 25, c.good)
			seeQP(p, 40, c.bad)

			qp, ok := p.TakeRecentQP()
			if !ok {
				t.Fatal("no samples")
			}
			if qp != c.want {
				t.Errorf("TakeRecentQP() = %d; want %d", qp, c.want)
			}
		})
	}
}
