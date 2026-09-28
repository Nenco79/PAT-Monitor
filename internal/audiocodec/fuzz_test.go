package audiocodec

import (
	"math"
	"testing"
)

// FuzzDecodeWhatAViewerSends feeds the talk-back decoder packets nobody
// encoded.
//
// **The payload is the viewer's**: whatever a page puts in its RTP packets
// reaches libopus inside the WebAssembly module, one decoder per track, for as
// long as the track lives. So the properties are the ones the talk-back leans
// on: no panic, never more samples than the longest Opus frame, a whole number
// of frames, and a decoder that still works after a packet it refused —
// because Receive conceals and carries on with the same one.
func FuzzDecodeWhatAViewerSends(f *testing.F) {
	enc, err := NewOpus(SampleRate, 32)
	if err != nil {
		f.Fatal(err)
	}
	defer enc.Close()
	frame := enc.FrameSamples()
	pcm := make([]int16, frame)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/float64(SampleRate)))
	}
	good, err := enc.Encode(pcm)
	if err != nil {
		f.Fatal(err)
	}
	good = append([]byte(nil), good...)

	f.Add(good)
	f.Add([]byte{0x00})
	f.Add([]byte{0xff, 0xff})             // code 3, 63 frames of the longest mode
	f.Add([]byte{0x03, 0xbf, 0xff, 0xff}) // code 3, VBR, padding flag
	f.Add(append([]byte{0x0b}, make([]byte, 1274)...))
	f.Add(make([]byte, 1276)) // one byte over libopus's single-frame limit
	f.Add(make([]byte, 4097)) // one byte over the decoder's own buffer

	dec, err := NewOpusDecoder(SampleRate, Channels)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { dec.Close() })
	limit := maxFrameSamples * dec.Channels()

	f.Fuzz(func(t *testing.T, packet []byte) {
		out, err := dec.Decode(packet)
		if err == nil {
			if len(out) > limit {
				t.Fatalf("%d samples from one packet, over the %d of the longest frame", len(out), limit)
			}
			if len(out)%dec.Channels() != 0 {
				t.Fatalf("%d samples is not a whole number of %d-channel frames", len(out), dec.Channels())
			}
			return
		}
		if _, err := dec.Conceal(); err != nil {
			t.Fatalf("concealment after a refused packet: %v", err)
		}
		if _, err := dec.Decode(good); err != nil {
			t.Fatalf("a good packet refused after a bad one: %v", err)
		}
	})
}
