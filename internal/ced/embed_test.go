package ced

import (
	"math"
	"sort"
	"testing"
)

// **Since the weights travel in the binary, part of the parity is a test and no
// longer a report.** The comparison over the 527 classes against ced.cpp stays
// in baselines/ced.txt — it wants somebody else's executable — but a signal
// generated here, with the score the oracle gave back then, can be replayed on
// every `go test`.
//
// It is the half that protects against the worst fault: a refactor that leaves
// the model running and the answers different.
func TestTheEmbeddedModelStillAnswersLikeTheOracle(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(m.Labels()), 527; got != want {
		t.Errorf("%d classes, wanted %d", got, want)
	}
	if got, want := m.SampleRate(), 16000; got != want {
		t.Errorf("%d Hz, wanted %d", got, want)
	}
	if got := m.WindowSeconds(); math.Abs(got-10.11) > 0.01 {
		t.Errorf("window of %.3f s, wanted 10.11", got)
	}

	// Ten seconds of a 440 Hz sine. The oracle, on the same file, says "Sine
	// wave" 0.9119 and "Chirp tone" 0.0476.
	w := make([]float32, m.WindowSamples())
	for i := range w {
		w[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 16000))
	}
	scores, err := m.Scores(w)
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.Index("Sine wave")
	if err != nil {
		t.Fatal(err)
	}
	if scores[i] < 0.85 {
		t.Errorf("\"Sine wave\" on a sine is worth %.4f, the oracle said 0.9119", scores[i])
	}
	// And it has to be the brightest one: a right score on the right class in
	// the middle of a high background would not be the same thing.
	idx := make([]int, len(scores))
	for k := range idx {
		idx[k] = k
	}
	sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	if idx[0] != i {
		t.Errorf("the brightest class is %q at %.4f, not \"Sine wave\"",
			m.Labels()[idx[0]], scores[idx[0]])
	}
}

// The embedded list carries AudioSet's **full** names, not HuggingFace's short
// ones.
//
// That is the difference that makes naming a class possible: in the short list
// "Inside" appears three times, and "Baby cry" distinguishes nothing. A wrong
// name does not produce an error further on, it produces a monitor watching the
// wrong class — which is why Index returns an error and not just any index.
//
// **The four below are a sample, not the list the monitor watches**: that lives
// in cmd/pat-monitor, and that is where it has to be looked at — a second list
// copied into this package would tell the truth about itself while the other
// one changed. What keeps it honest is newRecogniser, which resolves them all
// and is called by a test of its own.
func TestTheEmbeddedModelUsesTheLongAudioSetNames(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"Bark", "Bow-wow",
		"Baby cry, infant cry", "Crying, sobbing",
	} {
		if _, err := m.Index(name); err != nil {
			t.Errorf("%v", err)
		}
	}
	if _, err := m.Index("Baby cry"); err == nil {
		t.Error("HuggingFace's short name got through: the two lists are not the same one")
	}
}

// Embedded loads once and returns the same model: it is ~22 MB of float32, and
// two callers getting two copies would pay for it twice with nobody asking.
func TestTheEmbeddedModelIsLoadedOnce(t *testing.T) {
	a, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("two calls gave two models")
	}
}
