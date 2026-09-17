package audio

import (
	"context"
	"encoding/binary"
	"math"
	"time"
)

// CaptureTone generates a test tone instead of opening a microphone.
//
// It has the same signature as Capture and stands in for it: everything
// downstream — downmix to mono, gain, Opus packing, the analysis stream, hub,
// SDP, browser — receives exactly what it would receive from a real microphone
// and has no way of telling the difference.
//
// It is there for two practical reasons. The first is that on a laptop with the
// lid closed the microphone array disappears, and without this the audio branch
// cannot be exercised at all. The second is diagnostic: when someone hears
// nothing, this separates the microphone from the rest of the chain in one go.
//
// What it does NOT exercise is the WASAPI capture: precisely the part where the
// APO bypass lives. A tone you can hear says the rest of the chain works, not
// that the microphone will be audible.
func CaptureTone(
	ctx context.Context,
	opts Options,
	onStart func(Stream) error,
	onData func(pcm []byte, silent bool) error,
) error {
	// Two integer channels at 48 kHz: the format a microphone array presents
	// itself with, so the downmix to mono is exercised too instead of being
	// stepped over by an already-mono stream.
	format := StreamFormat{SampleRate: 48000, Channels: 2, BitsPerSample: 16}

	if err := onStart(Stream{
		Format:     format,
		DeviceName: "test tone",
		DeviceID:   "tone",
		// This is not raw mode and must not declare itself as one: saying "raw"
		// here would suggest the APO bypass had been verified.
		RawMode: false,
	}); err != nil {
		return err
	}

	const (
		// Delivery cadence, the same one WASAPI delivers with in shared mode.
		block = 10 * time.Millisecond
		// -20 dBFS: clearly audible without clipping, and it leaves headroom for
		// the microphone gain if anyone has configured one.
		amplitude = 0.1
		// The tone alternates between two pitches every second. A fixed tone
		// does not tell audio that has stalled from audio that is flowing: the
		// alternation can be heard to stop, and if its rhythm is not one second
		// then the declared time does not match the real one.
		lowHz, highHz = 440.0, 880.0
		switchEvery   = time.Second
	)

	ticker := time.NewTicker(block)
	defer ticker.Stop()

	start := time.Now()
	var (
		emitted int64   // frames already delivered
		phase   float64 // current phase, in radians
		pcm     []byte
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			// How many samples to produce is decided by the clock, not by the
			// ticker.
			//
			// On Windows a 10 ms ticker fires every 15, and delivering 480
			// frames on every tick would produce audio more slowly than real
			// time: the RTP timestamps would fall behind and the delay would
			// grow without end, which is exactly the fault a fake microphone
			// must not simulate. A real device delivers the samples that time
			// has produced: the same is done here.
			due := int64(now.Sub(start).Seconds() * float64(format.SampleRate))
			n := due - emitted
			if n <= 0 {
				continue
			}

			need := int(n) * format.BytesPerFrame()
			if cap(pcm) < need {
				pcm = make([]byte, need)
			}
			pcm = pcm[:need]

			for i := int64(0); i < n; i++ {
				// The pitch changes, the phase does not: breaking the phase
				// would produce a click every second, that is, a wideband
				// transient exactly where a simple signal is wanted.
				elapsed := time.Duration(float64(emitted+i) / float64(format.SampleRate) * float64(time.Second))
				freq := lowHz
				if (elapsed/switchEvery)%2 == 1 {
					freq = highHz
				}
				phase += 2 * math.Pi * freq / float64(format.SampleRate)
				if phase > 2*math.Pi {
					phase -= 2 * math.Pi
				}

				s := int16(amplitude * math.MaxInt16 * math.Sin(phase))
				off := int(i) * format.BytesPerFrame()
				for c := 0; c < format.Channels; c++ {
					binary.LittleEndian.PutUint16(pcm[off+c*2:], uint16(s))
				}
			}
			emitted = due

			if err := onData(pcm, false); err != nil {
				return err
			}
		}
	}
}
