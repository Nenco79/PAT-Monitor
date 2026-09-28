package opuswasm

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Decoder expands Opus packets into 16-bit PCM.
//
// Like the encoder it carries the stream's state, so one is needed per sender.
// And like the encoder it has its own module.
type Decoder struct {
	in       *instance
	statePtr uint32
	decode   api.Function
	channels int
}

// NewDecoder prepares a decoder that delivers at the given rate.
//
// The conversion is libopus's to do: asking it for 16 kHz from a 48 kHz stream
// beats resampling downstream, because that is the only operation in this chain
// able to shift the pitch of sounds without leaving a trace.
func NewDecoder(ctx context.Context, sampleRate, channels int) (*Decoder, error) {
	in, state, err := openCodec(ctx, "decoder", channels,
		uint64(int32(sampleRate)), uint64(int32(channels)))
	if err != nil {
		return nil, err
	}
	d := &Decoder{in: in, statePtr: state, channels: channels}
	if d.decode, err = in.function("opus_decode"); err != nil {
		_ = in.close(ctx)
		return nil, err
	}
	return d, nil
}

// Decode expands a packet into pcm and returns the samples **per channel**.
func (d *Decoder) Decode(ctx context.Context, packet []byte, pcm []int16) (int, error) {
	if len(packet) == 0 {
		return 0, fmt.Errorf("opuswasm: empty packet")
	}
	if len(packet) > int(d.in.pktLen) {
		return 0, fmt.Errorf("opuswasm: packet of %d bytes, over the %d byte limit", len(packet), d.in.pktLen)
	}
	if !d.in.mod.Memory().Write(d.in.pktPtr, packet) {
		return 0, fmt.Errorf("opuswasm: writing the packet outside the module memory")
	}
	return d.call(ctx, d.in.pktPtr, len(packet), pcm)
}

// Conceal produces the frame that stands in for a packet that never arrived.
//
// **It is opus_decode with a null pointer**, not a separate function: the
// concealment is made by the decoder out of its own state, which is why it has
// to be asked of the one following the stream and not of a fresh one.
func (d *Decoder) Conceal(ctx context.Context, pcm []int16) (int, error) {
	return d.call(ctx, ptrNull, 0, pcm)
}

func (d *Decoder) call(ctx context.Context, dataPtr uint32, dataLen int, pcm []int16) (int, error) {
	if len(pcm)%d.channels != 0 {
		return 0, fmt.Errorf("opuswasm: a %d sample buffer is not a whole number of %d-channel frames", len(pcm), d.channels)
	}
	frame := len(pcm) / d.channels
	if frame == 0 {
		return 0, fmt.Errorf("opuswasm: no room for the samples")
	}
	if uint32(len(pcm)*sizeOfInt16) > d.in.pcmLen {
		return 0, fmt.Errorf("opuswasm: a %d sample buffer is over the %d byte limit", len(pcm), d.in.pcmLen)
	}
	r, err := d.decode.Call(ctx,
		uint64(d.statePtr),
		uint64(dataPtr),
		uint64(int32(dataLen)),
		uint64(d.in.pcmPtr),
		uint64(int32(frame)),
		0, // no FEC: concealment is asked for with Conceal
	)
	if err != nil {
		return 0, fmt.Errorf("opuswasm: opus_decode: %w", err)
	}
	n := int32(r[0])
	if n < 0 {
		return 0, Status(n)
	}
	if int(n) > frame {
		return 0, fmt.Errorf("opuswasm: opus_decode returned %d samples into room for %d", n, frame)
	}
	if err := d.in.readPCM(pcm[:int(n)*d.channels]); err != nil {
		return 0, err
	}
	return int(n), nil
}

// Close frees the module.
func (d *Decoder) Close(ctx context.Context) error { return d.in.close(ctx) }
