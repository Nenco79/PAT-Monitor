package ced

import (
	_ "embed"
	"sync"

	"patmonitor/internal/gguf"
)

// weights are the weights the monitor ships.
//
// **They belong to somebody else**: mispeech/ced-tiny, Apache-2.0, copyright
// Xiaomi. The notice lives in licenses/manually-added/ced-tiny/, and it is
// there by hand for the same reason as libopus: pat-licenses enumerates
// **modules**, and a file embedded with go:embed is not a module — no tool
// would see it.
//
//	ced-tiny-q8_0.gguf   6,211,616 bytes
//	sha256               48bee4e2fc3cc85d7806e03471db24e77fda6c2a2e81ffe9ef67caebaf2bd674
//
// **The fingerprint is the line that matters**, not the address it came from:
// it is the only thing that says whether what is downloaded again is what we
// are running.
//
// **Why the quantised variant and not the f16.** Measured over 527 classes and
// three sets: q8_0 deviates from f16 by up to 1.5e-02 of probability and leaves
// the top five in the same order. On an alarm threshold that is noise, and it
// is five megabytes less to ship. **In memory they cost the same** — the
// weights are converted to float32 on opening — so the saving is in the file.
//
// **And it ships to everyone.** There is no machine that can take it and one
// that cannot, because the sum is done **per event and not per hour**: measured
// in an empty room at night, the gate opens six times in three minutes and the
// model costs 1.7% of a core. The worst case is unbroken crying, where the cap
// of one classification every five seconds is worth 10% of a core for as long
// as it lasts. Offering it "only if the hardware can take it" was a constraint
// written for a design that wanted ONNX Runtime and the GPU, and it fell with
// that design.
//
// **That number was wrong for two commits, and it is worth knowing why.** It
// said zero, and that was true: it had been measured when the gate asked for
// two bursts. Then the gate moved to one — which opens on an isolated burst —
// and nobody had redone the measurement. **A measurement belongs to the
// configuration it was taken in**, otherwise it outlives the thing it
// described.
//
//go:embed ced-tiny-q8_0.gguf
var weights []byte

var (
	once     sync.Once
	embedded *Model
	embedErr error
)

// Embedded is the model that travels in the binary, loaded the first time it is
// asked for.
//
// **It loads lazily and once**: it is ~22 MB of float32 and 96 ms, which are
// not spent at the start of a monitor whose job is to open the camera quickly.
// The model is not reentrant — one Stream per goroutine, and whoever wants two
// calls New themselves.
func Embedded() (*Model, error) {
	once.Do(func() {
		f, err := gguf.Parse(weights)
		if err != nil {
			embedErr = err
			return
		}
		embedded, embedErr = New(f)
	})
	return embedded, embedErr
}

// WatchedClasses are the AudioSet classes that count for each event: the
// monitor watches them and pat-sounds measures them by default, so both
// commands read this one list rather than a copy each.
//
// It is far narrower than one would be tempted to write. "Dog"
// is the strongest class on real barks and catches `brushing_teeth` at
// 0.442; "Domestic animals, pets" gives 0.877 on a cat. **Every extra class
// brings its own false positives**: at threshold 0.10 "Bark + Dog" makes
// fifteen false positives on ESC-50 where "Bark + Bow-wow" makes one.
var WatchedClasses = map[string][]string{
	"bark": {"Bark", "Bow-wow"},
	"cry":  {"Baby cry, infant cry", "Crying, sobbing"},
}

// SoundThreshold is the probability beyond which a class counts.
//
// **It was 0.20 and the measurement moved it**, over three public datasets of
// negatives — 20,857 clips for the cry, 19,779 for the bark — plus 457 real
// cries from donateacry. Both codes gain, and they gain much more than they
// cost:
//
//	                   caught at 0.20   at 0.15      false, 0.20 -> 0.15
//	cry, donateacry    363/457 (79%)    383 (83%)    ESC-50   0 -> 1
//	cry, FSD50K         13/42  (30%)     15 (35%)    US8K     1 -> 4
//	bark, US8K         660/998 (66%)    698 (69%)    FSD50K   2 -> 4
//	bark, FSD50K        74/122 (60%)     77 (63%)    bark, all 13 -> 15
//
// **Sixty-three more real events against eight more false ones**, which on
// the negatives is 0.014% to 0.043% for the cry and 0.066% to 0.076% for the
// bark. And the next step down is where it turns: 0.20 to 0.15 buys 63 for 8,
// 0.15 to 0.10 buys 49 for **22**. The knee is here, and it was measured
// rather than chosen.
//
// **What enters at 0.15 is worth naming**, because a count hides it: ESC-50's
// cat at 0.181; three `children_playing` on UrbanSound8K, 0.160 to 0.185, in
// a class whose worst was already through at 0.292; a door squeak at 0.192
// and a gasp at 0.187 on FSD50K. Nothing new in kind — the same confusions
// the old threshold already had at its own edge.
//
// **It is not a per-class threshold** because CED's probabilities are not
// comparable between classes — on an empty room the model is 38% sure it
// hears a mouse — but these four have their floor measured at the same place,
// and the move was checked on both codes precisely because one number serves
// two.
//
// **It is one number for both commands, and it lives here so that it is.** The
// tool used to hold a copy, kept equal to the monitor's by a test reading its
// source, because a sweep rerun without -threshold had measured the chain at
// 0.20 after the monitor had moved to 0.15. It is untyped because the monitor
// compares it with float32 probabilities and pat-sounds with float64 ones too,
// and each gets 0.15 in its own type.
const SoundThreshold = 0.15
