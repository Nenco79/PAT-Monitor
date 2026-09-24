package audiocodec

import (
	"math"
	"sync"
	"testing"
)

// **Encoding and decoding at the same time must not corrupt anything.**
//
// Live, this happened on pressing "Talk": the talk-back decoder inside the
// WebRTC goroutine, the capture encoder every twenty milliseconds, and the
// audio capture dead twice in thirteen seconds with __stack_chk_fail inside
// opus_encode. Underneath was a single WebAssembly module for the whole
// process, so one C stack for two goroutines.
//
// **The test stays, the remedy does not.** The remedy was a lock in
// audiocodec; now every codec has its own module and there is nothing left to
// serialise. This test looks at neither: it looks at the **behaviour** —
// encoding and decoding together must not corrupt anything — which is why it
// survived a change of design.
//
// Verified that it catches: putting the single module back makes it fail three
// times out of three, with the stack trace from back then. See
// internal/opuswasm.
func TestEncodeAndDecodeAtTheSameTime(t *testing.T) {
	enc, err := NewOpus(SampleRate, 64)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewOpusDecoder(SampleRate, Channels)
	if err != nil {
		t.Fatal(err)
	}

	// A frame of real signal: silence takes a shorter road inside the encoder
	// and exercises less of it.
	frame := make([]int16, enc.FrameSamples())
	for i := range frame {
		frame[i] = int16(9000 * math.Sin(2*math.Pi*440*float64(i)/float64(SampleRate)))
	}
	packet, err := enc.Encode(frame)
	if err != nil || len(packet) == 0 {
		t.Fatalf("first packet: %v", err)
	}

	const rounds = 300
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range rounds {
			if _, err := enc.Encode(frame); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range rounds {
			if _, err := dec.Decode(packet); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("simultaneous use of the WebAssembly module: %v", err)
	}
}
