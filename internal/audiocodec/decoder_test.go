package audiocodec

import (
	"math"
	"testing"
)

// **The test that matters is the round trip**: a tone is encoded, decoded, and
// then looked at to see whether it is still the same tone. A decoder returning
// silence, or noise, or the wrong frequency would pass any check on buffer
// lengths.
//
// It does not replace pat-opus, which decodes with somebody else's
// implementation: there **our** encoder is checked against something
// independent, here what is checked is that the pair the talk-back uses really
// works.
func TestATonePassesThroughAndComesBack(t *testing.T) {
	const (
		rate = 48000
		freq = 440.0
	)
	enc, err := NewOpus(rate, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := NewOpusDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	// Twenty frames: the first few are thrown away because the codec has a
	// start-up delay, and without dropping them a tone would be compared
	// against the silence that comes before it.
	var out []int16
	phase := 0.0
	step := 2 * math.Pi * freq / rate
	for i := range 20 {
		pcm := make([]int16, enc.FrameSamples())
		for j := range pcm {
			pcm[j] = int16(0.5 * 32767 * math.Sin(phase))
			phase += step
		}
		pkt, err := enc.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt) == 0 {
			continue
		}
		got, err := dec.Decode(pkt)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != FrameSamplesAt(rate) {
			t.Fatalf("frame %d: %d samples, wanted %d", i, len(got), FrameSamplesAt(rate))
		}
		if i >= 5 {
			out = append(out, got...)
		}
	}

	// The tone is there if the energy is there. A mute decoder is the likeliest
	// fault and the hardest to notice: the talk-back "works", the button lights
	// up, and nothing is heard in the room.
	var sum float64
	peak := 0.0
	for _, s := range out {
		v := float64(s) / 32768
		sum += v * v
		peak = math.Max(peak, math.Abs(v))
	}
	rms := math.Sqrt(sum / float64(len(out)))
	if rms < 0.1 {
		t.Errorf("RMS %.4f: the decoder is not returning the tone", rms)
	}
	if peak > 0.99 {
		t.Errorf("peak %.4f: the signal comes out distorted", peak)
	}

	// And it has to be **that** tone: the zero crossings are counted, which at
	// 440 Hz are 880 a second. It is the same coarse measure the sound detector
	// uses, and it is enough to tell 440 from 220 or 880.
	zeros := 0
	for i := 1; i < len(out); i++ {
		if (out[i-1] < 0) != (out[i] < 0) {
			zeros++
		}
	}
	estimated := float64(zeros) / 2 * rate / float64(len(out))
	if math.Abs(estimated-freq) > 30 {
		t.Errorf("estimated frequency %.0f Hz instead of %.0f: the decoder shifts the pitch", estimated, freq)
	}
}

// A lost packet is concealed, not filled with zeros: Opus carries the waveform
// on, and the difference is heard as a click on every loss.
func TestALostPacketIsConcealedNotSilenced(t *testing.T) {
	enc, err := NewOpus(48000, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := NewOpusDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	phase := 0.0
	step := 2 * math.Pi * 440 / 48000
	for range 10 {
		pcm := make([]int16, enc.FrameSamples())
		for j := range pcm {
			pcm[j] = int16(0.5 * 32767 * math.Sin(phase))
			phase += step
		}
		pkt, err := enc.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt) > 0 {
			if _, err := dec.Decode(pkt); err != nil {
				t.Fatal(err)
			}
		}
	}

	concealed, err := dec.Conceal()
	if err != nil {
		t.Fatal(err)
	}
	if len(concealed) == 0 {
		t.Fatal("no samples from the concealment")
	}
	// **And it is one frame long, which was the property nobody asserted.**
	// The concealment stands in for *one* packet — the caller in
	// internal/rtc/talkback.go asks for one per packet that would not decode —
	// and `opus_decode` with a null pointer returns as many samples as the
	// buffer it is handed. Handed the working buffer whole it returned 5760,
	// that is 120 ms for a 20 ms packet, and those extra 100 ms per loss went
	// into a playback queue capped at 400 ms, where what is dropped is
	// deliberately the oldest: the start of the sentence.
	if want := FrameSamplesAt(dec.SampleRate()) * dec.Channels(); len(concealed) != want {
		t.Errorf("the concealment is %d samples (%d ms) where a packet is %d (%d ms): "+
			"it queues in front of the voice instead of standing in for it",
			len(concealed), len(concealed)*1000/dec.SampleRate(),
			want, want*1000/dec.SampleRate())
	}
	var sum float64
	for _, s := range concealed {
		v := float64(s) / 32768
		sum += v * v
	}
	if rms := math.Sqrt(sum / float64(len(concealed))); rms < 0.05 {
		t.Errorf("RMS %.4f: the concealment is returning silence", rms)
	}
}

func TestTheDecoderRefusesWhatItCannotDo(t *testing.T) {
	// 44.1 kHz is the likeliest of the rates Opus does not do, and it is
	// refused out loud rather than converted behind the scenes.
	if _, err := NewOpusDecoder(44100, 1); err == nil {
		t.Error("44100 Hz accepted")
	}
	if _, err := NewOpusDecoder(48000, 3); err == nil {
		t.Error("3 channels accepted")
	}
	dec, err := NewOpusDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	if _, err := dec.Decode(nil); err == nil {
		t.Error("an empty packet accepted")
	}
}
