// Package audiocodec compresses the microphone's PCM into the format the
// browser accepts.
//
// The menu is not Windows' to decide but the browser's: WebRTC allows Opus,
// G.722 and G.711 and nothing else. Windows has excellent encoders — AAC, FLAC,
// MP3 — but none of them is negotiated live, and the other two allowed entries
// have 7 and 4 kHz of bandwidth against Opus's 20. Opus is therefore not a
// compromise: it is the ceiling.
//
// The encoder is real libopus, compiled to WebAssembly and run by wazero, which
// is pure Go. There is no cgo, no external process, and above all no rewrite of
// the codec to be trusted: inside is the reference implementation, and the
// embedded module weighs half a megabyte.
package audiocodec

import (
	"fmt"
	"slices"
	"time"

	"patmonitor/internal/opuswasm"
)

// Parameters of the audio stream. They have to stay aligned with
// opusFrameDuration in internal/rtc: that is the duration the hub declares on
// every packet, and if the two did not match, audio time would run faster or
// slower than real time.
const (
	// SampleRate is the preferred rate: the one Windows' shared capture
	// delivers practically everywhere.
	SampleRate = 48000
	Channels   = 1

	FrameDuration = 20 * time.Millisecond
	// FrameSamples is how many samples go into a packet at SampleRate. For the
	// other rates see FrameSamplesAt.
	FrameSamples = SampleRate / int(time.Second/FrameDuration)

	// maxPacketBytes is the ceiling of an Opus packet at 20 ms. It is here only
	// to size the working buffer: at 64 kbit/s real packets run around 160
	// bytes.
	maxPacketBytes = 1275
)

// SupportedRates are the input rates Opus accepts.
//
// The list is closed and it is not our choice: they are the five the codec
// declares, and outside them a resampler would be needed. In particular
// **44.1 kHz is not there**, and it is the likeliest of the unhandled rates: a
// microphone arriving at it is refused out loud, not converted behind the
// scenes.
var SupportedRates = []int{8000, 12000, 16000, 24000, 48000}

// RateSupported says whether Opus can compress directly at this rate.
func RateSupported(rate int) bool {
	return slices.Contains(SupportedRates, rate)
}

// FrameSamplesAt is how many samples make a frame of FrameDuration at the given
// rate.
func FrameSamplesAt(rate int) int {
	return rate / int(time.Second/FrameDuration)
}

// Encoder compresses one frame of 16-bit mono PCM.
//
// It is not safe for concurrent use: the encoder carries the stream's state,
// and every consumer needs one of their own.
type Encoder interface {
	// Encode returns the compressed packet. pcm must hold exactly
	// FrameSamples() samples. An empty packet is not an error: it means the
	// encoder decided to transmit nothing for this frame.
	Encode(pcm []int16) ([]byte, error)
	// FrameSamples is the frame length this encoder insists on, which depends
	// on the rate it was created with.
	FrameSamples() int
	Close() error
}

type opusEncoder struct {
	enc    *opuswasm.Encoder
	buf    []byte
	frame  int
	sample int
}

// NewOpus prepares the encoder at the given rate and bitrate.
//
// The rate is the microphone's and not a preference of ours: Opus compresses
// natively at five rates, and receiving one of them directly avoids resampling,
// which is the only operation in this chain able to shift the pitch of sounds
// without leaving a trace.
//
// The application is "audio" and not "voip" because ambient sound matters here
// too, and speech-oriented profiles tend to penalise what is not voice — that
// is, exactly the sounds that say what is happening in the room.
func NewOpus(sampleRate, bitrateKbps int) (Encoder, error) {
	if sampleRate == 0 {
		sampleRate = SampleRate
	}
	if !RateSupported(sampleRate) {
		return nil, fmt.Errorf("audiocodec: Opus does not compress at %d Hz; it accepts %v",
			sampleRate, SupportedRates)
	}
	if bitrateKbps <= 0 {
		bitrateKbps = 64
	}
	// Every encoder gets its own WebAssembly module: see internal/opuswasm.
	enc, err := opuswasm.NewEncoder(bg, sampleRate, Channels)
	if err == nil {
		err = enc.SetBitrate(bg, bitrateKbps*1000)
	}
	if err != nil {
		return nil, fmt.Errorf("audiocodec: creating the Opus encoder at %d Hz: %w", sampleRate, err)
	}
	return &opusEncoder{
		enc:    enc,
		buf:    make([]byte, maxPacketBytes),
		frame:  FrameSamplesAt(sampleRate),
		sample: sampleRate,
	}, nil
}

func (e *opusEncoder) FrameSamples() int { return e.frame }

func (e *opusEncoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) != e.frame {
		return nil, fmt.Errorf("audiocodec: expected %d samples at %d Hz, got %d",
			e.frame, e.sample, len(pcm))
	}
	n, err := e.enc.Encode(bg, pcm, e.buf)
	if err != nil {
		return nil, fmt.Errorf("audiocodec: encode: %w", err)
	}
	if n == 0 {
		return nil, nil
	}
	// The working buffer is reused on every frame, so the packet has to be
	// copied: whoever receives it queues it and consumes it later.
	return append([]byte(nil), e.buf[:n]...), nil
}

// Close frees this encoder's WebAssembly module. Since the module is one per
// codec, not closing it means keeping one alive for every restart of the
// capture — and the capture restarts every time the microphone goes away and
// comes back.
func (e *opusEncoder) Close() error { return e.enc.Close(bg) }
