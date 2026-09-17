package opuswasm

import (
	"context"
	"fmt"
)

// Encoder compresses 16-bit mono or stereo PCM into Opus packets.
//
// It is not safe for concurrent use, and that is not an oversight: it carries
// the stream's state, so two goroutines writing to it would produce a stream no
// decoder can follow. One per consumer is needed — which is exactly why each
// gets its own module.
type Encoder struct {
	in       *instance
	statePtr uint32
	encode   callable
	setBR    callable
	channels int
}

// callable holds only what we need of a module function.
type callable = interface {
	Call(ctx context.Context, params ...uint64) ([]uint64, error)
}

// NewEncoder prepares an encoder at the given rate.
//
// **The application is "audio" and not "voip"**, the same choice audiocodec
// documents: ambient sound matters here too, and speech-oriented profiles
// penalise what is not voice — that is, exactly the sounds that say what is
// happening in the room. It is not a parameter because it is not the caller's
// decision: changing it is changing the product.
func NewEncoder(ctx context.Context, sampleRate, channels int) (*Encoder, error) {
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("opuswasm: %d channels, expected 1 or 2", channels)
	}
	in, err := open(ctx, channels, maxFrameSamples, maxPacketBytes)
	if err != nil {
		return nil, err
	}
	e := &Encoder{in: in, channels: channels}

	getSize, err := in.function("opus_encoder_get_size")
	if err != nil {
		return nil, e.closeOnError(ctx, err)
	}
	initF, err := in.function("opus_encoder_init")
	if err != nil {
		return nil, e.closeOnError(ctx, err)
	}
	if e.encode, err = in.function("opus_encode"); err != nil {
		return nil, e.closeOnError(ctx, err)
	}
	if e.setBR, err = in.function("bridge_encoder_set_bitrate"); err != nil {
		return nil, e.closeOnError(ctx, err)
	}

	r, err := getSize.Call(ctx, uint64(channels))
	if err != nil {
		return nil, e.closeOnError(ctx, fmt.Errorf("opuswasm: opus_encoder_get_size: %w", err))
	}
	if e.statePtr, err = in.alloc(ctx, uint32(r[0])); err != nil {
		return nil, e.closeOnError(ctx, err)
	}
	r, err = initF.Call(ctx, uint64(e.statePtr), uint64(int32(sampleRate)), uint64(int32(channels)), uint64(int32(appAudio)))
	if err != nil {
		return nil, e.closeOnError(ctx, fmt.Errorf("opuswasm: opus_encoder_init: %w", err))
	}
	if c := int32(r[0]); c != opusOK {
		return nil, e.closeOnError(ctx, Status(c))
	}
	return e, nil
}

// closeOnError frees the module and returns the error that caused the retreat:
// a constructor that fails must not leave a module alive.
func (e *Encoder) closeOnError(ctx context.Context, err error) error {
	_ = e.in.close(ctx)
	return err
}

// SetBitrate fixes the bitrate in bits per second.
func (e *Encoder) SetBitrate(ctx context.Context, bps int) error {
	r, err := e.setBR.Call(ctx, uint64(e.statePtr), uint64(int32(bps)))
	if err != nil {
		return fmt.Errorf("opuswasm: setting the bitrate: %w", err)
	}
	if c := int32(r[0]); c != opusOK {
		return Status(c)
	}
	return nil
}

// Encode compresses one frame and writes the packet into out, returning how
// many bytes it wrote.
//
// **A single byte is not an error**: it is a frame the encoder decided not to
// transmit, and whoever receives it must not mistake it for a fault.
func (e *Encoder) Encode(ctx context.Context, pcm []int16, out []byte) (int, error) {
	if len(pcm)%e.channels != 0 {
		return 0, fmt.Errorf("opuswasm: %d samples are not a whole number of %d-channel frames", len(pcm), e.channels)
	}
	if len(out) == 0 {
		return 0, fmt.Errorf("opuswasm: no room for the packet")
	}
	if err := e.in.writePCM(pcm); err != nil {
		return 0, err
	}
	room := len(out)
	if room > int(e.in.pktLen) {
		room = int(e.in.pktLen)
	}
	r, err := e.encode.Call(ctx,
		uint64(e.statePtr),
		uint64(e.in.pcmPtr),
		uint64(int32(len(pcm)/e.channels)),
		uint64(e.in.pktPtr),
		uint64(int32(room)),
	)
	if err != nil {
		return 0, fmt.Errorf("opuswasm: opus_encode: %w", err)
	}
	n := int32(r[0])
	if n < 0 {
		return 0, Status(n)
	}
	if int(n) > room {
		return 0, fmt.Errorf("opuswasm: opus_encode wrote %d bytes into a %d byte buffer", n, room)
	}
	b, ok := e.in.mod.Memory().Read(e.in.pktPtr, uint32(n))
	if !ok {
		return 0, fmt.Errorf("opuswasm: reading the packet outside the module memory")
	}
	copy(out, b)
	return int(n), nil
}

// Close frees the module, and with it everything that was inside.
func (e *Encoder) Close(ctx context.Context) error { return e.in.close(ctx) }
