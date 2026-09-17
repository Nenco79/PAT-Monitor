// Package opuswasm runs libopus, compiled to WebAssembly, inside wazero.
//
// **One module per codec, not one per process.** That is the whole reason this
// package exists instead of a dependency: the library that hands us libopus
// kept a single module — one runtime, one linear memory, one C stack — created
// with a sync.Once and without a lock, and with the talk-back the decoder
// entered it from the WebRTC goroutine while the encoder was inside. The two
// overwrote each other's stack, __stack_chk_fail fired inside libopus, and the
// audio capture died. Here every Encoder and every Decoder instantiates its
// **own** module: separate memories, separate stacks, and nothing left to
// serialise. The defect is not covered by a rule, it no longer exists.
//
// The compilation is shared, which is the way wazero wants to be used
// (examples/concurrent-instantiation): compiling is expensive and produces
// immutable code, instantiating is cheap and produces the state. So one
// CompiledModule for the process, one instance per codec.
//
// # What we ship, and where it comes from
//
// libopus.wasm is **libopus 1.5.2** plus a two-hundred-line C bridge, compiled
// for wasm32-wasi with zig cc. It is not ours: it comes verbatim from
// github.com/jj11hh/opus@v1.0.1, MIT, where it lives in
// wasm-bridge/build/wasm_bridge.
//
//	sha256  6377b9938a21044a4f56a53c1c336ba3e7a6112b5f3d1474aeb22beefe2176fb
//
// **It is an opaque blob, and it is the opposite of the .syso**: that one stays
// out of the repository because build.ps1 derives it entirely from the drawing,
// this one stays in because without a WebAssembly toolchain we cannot
// regenerate it.
//
// **Taking it out of go.mod did not take away the way to fetch it again.** The
// module proxy keeps versions forever and does not care what we depend on, so
// two commands and no dependency are enough:
//
//	curl -sO https://proxy.golang.org/github.com/jj11hh/opus/@v/v1.0.1.zip
//	unzip -p v1.0.1.zip 'github.com/jj11hh/opus@v1.0.1/wasm-bridge/build/wasm_bridge'
//
// Verified: 535091 bytes, and the sha256 above. **The line that matters is the
// fingerprint**, not the address: it is the only thing that says whether the
// file downloaded again is the one we are running.
//
// **What is missing is not the copy, it is the upgrade.** That repository has
// two versions, v1.0.0 and v1.0.1, both from May 2025, and inside is libopus
// 1.5.2 while upstream has reached 1.6.1. So no road was lost by dropping the
// dependency: that road had already stopped. Going up means compiling it
// ourselves — the libopus source, a non-variadic bridge for OPUS_SET_BITRATE
// (the only CTL we use, some ten lines), and zig or wasi-sdk — and then nothing
// of anyone else's would be left in that file, and the MIT notice would go with
// the last byte.
//
// The two licences to keep live in licenses/manually-added/, one folder per
// work: opus/ for the codec (BSD-3), opus-wasm-bridge/ for the bridge (MIT).
// **No tool can find them**: from here on that code is no longer a Go module,
// and pat-licenses enumerates modules.
//
// # What the module exposes
//
// The libopus ABI, not a contract invented by somebody: opus_encode,
// opus_decode, the *_get_size/*_init pairs, malloc and free. The only additions
// are the bridge_* wrappers, and they exist for one reason: libopus's CTLs go
// through opus_encoder_ctl, which is **variadic**, and a C variadic cannot be
// called from outside a wasm module.
package opuswasm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	_ "embed"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed libopus.wasm
var binary []byte

// maxFrameSamples is the longest Opus frame, 120 ms at 48 kHz, per channel. It
// sizes the buffers each instance allocates once.
const maxFrameSamples = 48000 / 1000 * 120

// maxPacketBytes is how large an incoming packet we accept. An Opus frame fits
// in 1275 bytes by specification; a packet can carry more than one, but it
// arrives inside a datagram and the MTU keeps it far below this ceiling.
// Anyone sending a larger one gets an error rather than a truncation.
const maxPacketBytes = 4096

// The libopus ABI constants, settled in opus_defines.h and public.
//
// **Getting one wrong protests**, which is why they can be written here instead
// of being read back from the module: opus_encoder_init answers OPUS_BAD_ARG to
// an application it does not know, and every call reports its own code. This is
// not the family of the GUID that gets accepted and does nothing.
const (
	opusOK        = 0
	appAudio      = 2049 // OPUS_APPLICATION_AUDIO
	sizeOfInt16   = 2
	ptrNull       = 0
	sampleRate48k = 48000
)

// Status is a libopus return code.
type Status int

func (e Status) Error() string {
	if s, ok := statusNames[int(e)]; ok {
		return fmt.Sprintf("opus: %s (%d)", s, int(e))
	}
	return fmt.Sprintf("opus: error %d", int(e))
}

// The libopus codes, with the words from opus_strerror.
var statusNames = map[int]string{
	-1: "bad argument",
	-2: "buffer too small",
	-3: "internal error",
	-4: "corrupted stream",
	-5: "request not implemented",
	-6: "invalid state",
	-7: "memory allocation failed",
}

// The compiled module: one per process, immutable, shared.
var (
	once       sync.Once
	wasmRT     wazero.Runtime
	compiled   wazero.CompiledModule
	compileErr error
)

func compile(ctx context.Context) error {
	once.Do(func() {
		rt := wazero.NewRuntime(ctx)
		wasi_snapshot_preview1.MustInstantiate(ctx, rt)
		cm, err := rt.CompileModule(ctx, binary)
		if err != nil {
			_ = rt.Close(ctx)
			compileErr = fmt.Errorf("opuswasm: compiling libopus.wasm: %w", err)
			return
		}
		wasmRT, compiled = rt, cm
	})
	return compileErr
}

// instance is a live module with the functions we need and the buffers it
// allocates once.
//
// **The buffers live here and not in the individual calls**, and that is the
// secondary gain of having one module per codec: whoever shares a memory has to
// ask for and hand back space on every packet, fifty times a second in each
// direction. Whoever has their own keeps them.
type instance struct {
	mod api.Module

	malloc api.Function
	pcmPtr uint32 // PCM buffer inside the module
	pktPtr uint32 // packet buffer inside the module
	pcmLen uint32
	pktLen uint32
}

// open instantiates a new module and allocates the working buffers in it.
func open(ctx context.Context, channels int, pcmSamples, pktBytes int) (*instance, error) {
	if err := compile(ctx); err != nil {
		return nil, err
	}
	// The name stays empty: two instances with the same name refuse each other,
	// and here there are as many as there are live codecs. And the start
	// function is _initialize, not _start: the module is built as a **reactor**
	// — a library that stays available — and not as a program that starts and
	// finishes.
	cfg := wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize")
	mod, err := wasmRT.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("opuswasm: instantiating libopus: %w", err)
	}
	in := &instance{mod: mod, malloc: mod.ExportedFunction("malloc")}
	if in.malloc == nil {
		_ = mod.Close(ctx)
		return nil, errors.New("opuswasm: libopus.wasm does not export malloc")
	}
	in.pcmLen = uint32(pcmSamples * channels * sizeOfInt16)
	in.pktLen = uint32(pktBytes)
	if in.pcmPtr, err = in.alloc(ctx, in.pcmLen); err != nil {
		_ = mod.Close(ctx)
		return nil, err
	}
	if in.pktPtr, err = in.alloc(ctx, in.pktLen); err != nil {
		_ = mod.Close(ctx)
		return nil, err
	}
	return in, nil
}

func (in *instance) alloc(ctx context.Context, n uint32) (uint32, error) {
	r, err := in.malloc.Call(ctx, uint64(n))
	if err != nil {
		return 0, fmt.Errorf("opuswasm: malloc(%d): %w", n, err)
	}
	p := uint32(r[0])
	if p == ptrNull {
		return 0, fmt.Errorf("opuswasm: malloc(%d) returned NULL", n)
	}
	return p, nil
}

// function fetches an export, and **its absence is an error straight away**: a
// missing export would otherwise give a nil called later, that is, a panic in
// the middle of the night instead of a refusal at opening time.
func (in *instance) function(name string) (api.Function, error) {
	f := in.mod.ExportedFunction(name)
	if f == nil {
		return nil, fmt.Errorf("opuswasm: libopus.wasm does not export %s", name)
	}
	return f, nil
}

// writePCM copies the samples into the module's buffer. Little-endian, which is
// WebAssembly's order by definition — not that of the machine it runs on.
func (in *instance) writePCM(pcm []int16) error {
	n := uint32(len(pcm) * sizeOfInt16)
	if n > in.pcmLen {
		return fmt.Errorf("opuswasm: %d samples do not fit the %d byte buffer", len(pcm), in.pcmLen)
	}
	mem := in.mod.Memory()
	for i, v := range pcm {
		if !mem.WriteUint16Le(in.pcmPtr+uint32(i*sizeOfInt16), uint16(v)) {
			return errors.New("opuswasm: writing PCM outside the module memory")
		}
	}
	return nil
}

// readPCM reads back the samples the decoder produced.
func (in *instance) readPCM(pcm []int16) error {
	mem := in.mod.Memory()
	for i := range pcm {
		v, ok := mem.ReadUint16Le(in.pcmPtr + uint32(i*sizeOfInt16))
		if !ok {
			return errors.New("opuswasm: reading PCM outside the module memory")
		}
		pcm[i] = int16(v)
	}
	return nil
}

func (in *instance) close(ctx context.Context) error {
	// Closing the module frees its whole memory, buffers and codec state
	// included: there is no free to call first.
	return in.mod.Close(ctx)
}

// Version is the libopus that is actually running.
//
// It is needed because that number cannot be read anywhere else: the module is
// a blob, and its version appears neither in go.mod nor in the log. Whoever
// wonders whether they are running 1.5.2 or 1.6.1 asks this.
func Version(ctx context.Context) (string, error) {
	in, err := open(ctx, 1, 1, 1)
	if err != nil {
		return "", err
	}
	defer in.close(ctx)

	f, err := in.function("opus_get_version_string")
	if err != nil {
		return "", err
	}
	r, err := f.Call(ctx)
	if err != nil {
		return "", fmt.Errorf("opuswasm: opus_get_version_string: %w", err)
	}
	return cString(in.mod, uint32(r[0]))
}

// cString reads a zero-terminated string out of the module's memory.
func cString(mod api.Module, ptr uint32) (string, error) {
	mem := mod.Memory()
	var b []byte
	for i := uint32(0); i < 256; i++ {
		c, ok := mem.ReadByte(ptr + i)
		if !ok {
			return "", errors.New("opuswasm: string outside the module memory")
		}
		if c == 0 {
			return string(b), nil
		}
		b = append(b, c)
	}
	return "", errors.New("opuswasm: unterminated string")
}
