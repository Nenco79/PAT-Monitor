package opuswasm

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
)

const (
	rate  = 48000
	frame = rate / 50 // 20 ms, the packet cadence
)

// tone generates a sine, which is the simplest signal on which it shows whether
// the chain carried something rather than noise.
func tone(n int, hz float64) []int16 {
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(0.4 * 32767 * math.Sin(2*math.Pi*hz*float64(i)/rate))
	}
	return pcm
}

// correlation compares two already aligned signals.
func correlation(a, b []int16) float64 {
	var sxy, sxx, syy float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		sxy += x * y
		sxx += x * x
		syy += y * y
	}
	if sxx == 0 || syy == 0 {
		return 0
	}
	return sxy / math.Sqrt(sxx*syy)
}

// Two codecs have to live in two modules, and this is a comparison of identity
// and not of content.
//
// **Comparing the bytes catches nothing** — write a tone into one's buffer,
// another into the other's, and insist the first has not changed — because
// malloc hands out distinct blocks even inside a single memory. The buffers
// were never the danger. The danger is the **C stack**, which has no address
// that can be looked at from here and only shows while two calls are standing
// on it together — that is, in the concurrency test below, which does fail with
// a single module.
//
// This one stays because it is the only **static** check: a refactor that went
// back to sharing the module fails it at once, without depending on how two
// goroutines happen to interleave.
func TestTwoCodecsGetTwoModules(t *testing.T) {
	ctx := context.Background()
	a, err := NewEncoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	b, err := NewDecoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)

	if a.in.mod == b.in.mod {
		t.Fatal("encoder and decoder share the module: memory and C stack in common, " +
			"which is the fault this package exists to remove")
	}
}

// The round trip, with the only check that matters: what comes out resembles
// what went in. A mistake in the offsets or in the byte order would not give an
// error, it would give noise.
func TestEncodeThenDecode(t *testing.T) {
	ctx := context.Background()
	enc, err := NewEncoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close(ctx)
	if err := enc.SetBitrate(ctx, 64000); err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close(ctx)

	const frames = 50 // one second
	in := tone(frame*frames, 440)
	pkt := make([]byte, 1275)
	out := make([]int16, 0, len(in))
	buf := make([]int16, frame)
	for f := 0; f < frames; f++ {
		n, err := enc.Encode(ctx, in[f*frame:(f+1)*frame], pkt)
		if err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		if n <= 0 {
			t.Fatalf("frame %d: packet of %d bytes", f, n)
		}
		m, err := dec.Decode(ctx, pkt[:n], buf)
		if err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		out = append(out, buf[:m]...)
	}
	if len(out) != len(in) {
		t.Fatalf("%d samples in, %d out", len(in), len(out))
	}
	// The codec delays by 6.5 ms at 48 kHz: the comparison starts past the
	// transient.
	const delay = 312
	c := correlation(in[:len(in)-delay], out[delay:])
	if c < 0.99 {
		t.Errorf("correlation %.4f: the round trip does not carry the signal", c)
	}
}

// Concealment has to produce samples, not an error and not silence: a lost
// packet and a decoder that says nothing are the same thing from outside.
func TestConcealProducesAFrame(t *testing.T) {
	ctx := context.Background()
	enc, err := NewEncoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close(ctx)
	dec, err := NewDecoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close(ctx)

	// First the decoder is given something to conceal from: concealment is
	// built on the stream's state, and on a freshly born decoder there is none.
	in := tone(frame*10, 440)
	pkt := make([]byte, 1275)
	buf := make([]int16, frame)
	for f := 0; f < 10; f++ {
		n, err := enc.Encode(ctx, in[f*frame:(f+1)*frame], pkt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(ctx, pkt[:n], buf); err != nil {
			t.Fatal(err)
		}
	}
	n, err := dec.Conceal(ctx, buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != frame {
		t.Errorf("%d samples concealed, wanted %d", n, frame)
	}
	var peak int16
	for _, v := range buf[:n] {
		if v > peak {
			peak = v
		}
	}
	if peak == 0 {
		t.Error("the concealment produced exact silence")
	}
}

// **The defect this package repairs, and the test that catches it.**
//
// Verified by putting the earlier design back — a sync.Once around the
// instance, and no closing, which is how it was: it fails three times out of
// three, with the exact stack trace from the log back then.
//
//	opuswasm: opus_encode: wasm error: unreachable
//	wasm stack trace:
//	    wasm_bridge.__stack_chk_fail()
//	    wasm_bridge.opus_fft_impl(i32,i32)
//	    wasm_bridge.run_analysis(...)
//	    wasm_bridge.opus_encode(i32,i32,i32,i32,i32) i32
//
// With one module per codec there is nothing to serialise, and indeed there is
// no lock here: that is the difference between a rule somebody has to remember
// and a property of the construction.
func TestEncodeAndDecodeAtTheSameTime(t *testing.T) {
	ctx := context.Background()
	enc, err := NewEncoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close(ctx)
	dec, err := NewDecoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close(ctx)

	// A real packet to feed the decoder, produced by a separate encoder so as
	// not to touch the one under test.
	seed, err := NewEncoder(ctx, rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	pkt := make([]byte, 1275)
	n, err := seed.Encode(ctx, tone(frame, 440), pkt)
	if err != nil {
		t.Fatal(err)
	}
	_ = seed.Close(ctx)
	pkt = pkt[:n]

	const rounds = 300
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		in, buf := tone(frame, 440), make([]byte, 1275)
		for i := 0; i < rounds; i++ {
			if _, err := enc.Encode(ctx, in, buf); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]int16, frame)
		for i := 0; i < rounds; i++ {
			if _, err := dec.Decode(ctx, pkt, buf); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("encoding and decoding together: %v", err)
	}
}

// The libopus version cannot be read anywhere else: the module is a blob and it
// does not appear in go.mod. If the file is ever replaced, this line is the only
// place that says so.
func TestVersion(t *testing.T) {
	v, err := Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "libopus ") {
		t.Fatalf("unexpected version: %q", v)
	}
	t.Log(v)
}
