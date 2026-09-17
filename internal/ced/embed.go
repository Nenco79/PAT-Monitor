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
