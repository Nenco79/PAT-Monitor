package rtc

import "testing"

// TestEachRollingWindowKeepsItsOwnRules checks each rolling window of this
// package against what its own row declares it does, on a sequence with gaps, a
// full window, odd sums and a run of gaps long enough to empty it. The model is
// written from the row and not from rollingMean, so a site whose parameters
// drift from what its doc argues fails here: flip `ages` or `round` on a row and
// that row goes red.
func TestEachRollingWindowKeepsItsOwnRules(t *testing.T) {
	seq := []int{30, 0, 31, 29, 33, 28, 0, 27, 0, 0, 0, 0, 35, 36, 0, 34, 37, 32, 29}

	rows := []struct {
		name        string
		feed        func() func(int) int
		keep, speak int
		ages, round bool
	}{
		{"qualityGovernor.note", func() func(int) int {
			return newQualityGovernor(30, 2500).note
		}, qualityThroughputWindow, qualityThroughputWindow, false, false},
		{"qualityGovernor.noteQP", func() func(int) int {
			return newQualityGovernor(30, 2500).noteQP
		}, qualityQPWindow, qualityQPWindow, true, true},
		{"sentWindow.add", func() func(int) int {
			var w sentWindow
			return w.add
		}, sentWindowSamples, sentWindowMinSamples, true, false},
		{"scaleGovernor.noteQP", func() func(int) int {
			var g scaleGovernor
			return g.noteQP
		}, scaleQPWindow, 1, false, false},
	}
	for _, r := range rows {
		feed := r.feed()
		var held []int
		for i, v := range seq {
			switch {
			case v > 0:
				held = append(held, v)
				if len(held) > r.keep {
					held = held[1:]
				}
			case r.ages && len(held) > 0:
				held = held[1:]
			}
			want := 0
			if len(held) >= r.speak && len(held) > 0 {
				sum := 0
				for _, x := range held {
					sum += x
				}
				want = sum / len(held)
				if r.round && 2*(sum%len(held)) >= len(held) {
					want++
				}
			}
			if got := feed(v); got != want {
				t.Errorf("%s: sample %d (%d): got %d, want %d", r.name, i, v, got, want)
			}
		}
	}
}
