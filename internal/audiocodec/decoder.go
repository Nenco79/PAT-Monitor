package audiocodec

import (
	"fmt"

	"patmonitor/internal/opuswasm"
)

// The other direction: from Opus to PCM, for the talk-back.
//
// **It is the same libopus as the encoder**, and that is why this direction
// costs fifty lines instead of a new dependency. The embedded WebAssembly
// module carries the decoder too, so the binary stays one and there is no
// second implementation to be trusted.
//
// **It is not pat-opus's decoder.** There the decoding is done with pion/opus
// on purpose, because it was written by somebody else and its job is to check
// ours; here what is needed is a **complete** decoder, because what arrives was
// encoded by a browser and not by us — fullband, stereo, with FEC and DTX if it
// likes. A partial implementation would be fine until the packet it cannot read
// arrives, and on that day the talk-back would fall silent without saying why.

// maxFrameSamples is the longest Opus frame: 120 ms at 48 kHz, per channel. It
// only sizes the working buffer — browsers send 20 ms — but a short buffer here
// would not give a truncated frame, it would give an error on every packet
// longer than expected.
const maxFrameSamples = 48000 / 1000 * 120

// Decoder expands Opus packets into 16-bit PCM.
//
// Like the encoder, **it is not safe for concurrent use**: it carries the
// stream's state, and every source needs one of its own.
type Decoder interface {
	// Decode returns the packet's samples, interleaved if there is more than
	// one channel.
	Decode(packet []byte) ([]int16, error)
	// Conceal produces the frame that stands in for a lost packet.
	//
	// **It is not silence.** Opus knows how to carry the waveform on for a
	// frame or two, and the difference is audible: a gap filled with zeros
	// makes a click on every lost packet, which on a mobile network means
	// often.
	Conceal() ([]int16, error)
	// Channels and SampleRate describe what Decode returns.
	Channels() int
	SampleRate() int
	Close() error
}

type opusDecoder struct {
	dec      *opuswasm.Decoder
	buf      []int16
	channels int
	rate     int
}

// NewOpusDecoder prepares the decoder.
//
// **Decoding is at 48 kHz mono, always, whatever arrives.** That is not a
// simplification: Opus has a fixed internal rate and the decoder delivers at
// whatever rate it is asked for, without us doing the resampling — and it
// handles the downmix to mono itself, which it knows how to do better than an
// average written here. It is the capture rule the other way round: the
// conversion is done by whoever has the right to do it.
func NewOpusDecoder(sampleRate, channels int) (Decoder, error) {
	if sampleRate == 0 {
		sampleRate = SampleRate
	}
	if channels == 0 {
		channels = Channels
	}
	if !RateSupported(sampleRate) {
		return nil, fmt.Errorf("audiocodec: Opus does not decode at %d Hz; it accepts %v",
			sampleRate, SupportedRates)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("audiocodec: %d channels, expected 1 or 2", channels)
	}
	// Every decoder gets its own WebAssembly module: see internal/opuswasm.
	dec, err := opuswasm.NewDecoder(bg, sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("audiocodec: creating the Opus decoder at %d Hz: %w", sampleRate, err)
	}
	return &opusDecoder{
		dec:      dec,
		buf:      make([]int16, maxFrameSamples*channels),
		channels: channels,
		rate:     sampleRate,
	}, nil
}

func (d *opusDecoder) Channels() int   { return d.channels }
func (d *opusDecoder) SampleRate() int { return d.rate }

func (d *opusDecoder) Decode(packet []byte) ([]int16, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("audiocodec: empty packet")
	}
	n, err := d.dec.Decode(bg, packet, d.buf)
	if err != nil {
		return nil, fmt.Errorf("audiocodec: decode: %w", err)
	}
	// n is the number of samples **per channel**.
	return append([]int16(nil), d.buf[:n*d.channels]...), nil
}

func (d *opusDecoder) Conceal() ([]int16, error) {
	// **It is asked for one frame, and the buffer's own size would ask for
	// six.** `opus_decode` on a real packet reports *the packet's* duration
	// whatever buffer it is offered — 20 ms into a 120 ms buffer — which is why
	// `Decode` can hand over `d.buf` whole. With a null pointer there is no
	// packet duration to report, so the buffer's length **is** the request:
	// `opuswasm.call` passes `len(pcm)/channels` as `frame_size`, and `d.buf` is
	// sized at `maxFrameSamples`, the longest Opus frame there is.
	//
	// Measured before the fix: 5760 samples, 120 ms, for every one of three
	// consecutive concealments standing in for 20 ms packets. The caller
	// conceals **once per failed packet** (`internal/rtc/talkback.go`), so those
	// extra 100 ms were not covering the loss, they were queueing behind it: at
	// `renderMaxQueue` of 400 ms, four concealed packets fill the queue and what
	// `Player.Write` then throws away is deliberately the **oldest** — the
	// beginning of the sentence somebody is speaking into the room.
	n, err := d.dec.Conceal(bg, d.buf[:FrameSamplesAt(d.rate)*d.channels])
	if err != nil {
		return nil, fmt.Errorf("audiocodec: packet loss concealment: %w", err)
	}
	return append([]int16(nil), d.buf[:n*d.channels]...), nil
}

// Close frees this decoder's module. See its twin in audiocodec.go: since every
// codec has one of its own, closing is no longer a formality.
func (d *opusDecoder) Close() error { return d.dec.Close(bg) }
