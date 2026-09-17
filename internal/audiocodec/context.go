package audiocodec

import "context"

// **There is no lock here, and its absence is the point.**
//
// A lock belongs here only while libopus runs in a single WebAssembly module
// for the whole process: with one C stack for two goroutines, encoder and
// decoder overwrite each other and __stack_chk_fail fires inside opus_encode,
// which from outside looks like the audio capture dying. Serialising covers
// that defect and leaves standing a rule somebody has to remember — anyone
// calling the module from outside this package brings it back in silence.
//
// Now every Encoder and every Decoder has its **own** module (see
// internal/opuswasm), so they share nothing and there is nothing to protect.
// The test that demonstrates it stayed where it was, in concurrency_test.go: it
// is the behaviour that has to keep holding, not the remedy it was obtained
// with.
//
// bg is the context handed to those calls. wazero wants one on every
// invocation, and this package's interfaces do not have one: an audio frame
// lasts twenty milliseconds and is not an operation anybody should be able to
// cancel halfway. Carrying one down to here would mean changing the signature
// of Encode and dragging it through the hub and the pipeline for a value nobody
// would use.
var bg = context.Background()
