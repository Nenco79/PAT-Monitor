package audiocodec

import "testing"

// TestRateSupported covers the reason the check exists: a microphone at 44.1
// kHz is not a rare case to be ignored, and without a resampler it would
// produce audio at the wrong pitch — a fault that is heard at once and appears
// in no counter.
func TestRateSupported(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		if !RateSupported(rate) {
			t.Errorf("%d Hz ought to be fine for Opus", rate)
		}
	}
	for _, rate := range []int{0, 11025, 22050, 44100, 96000} {
		if RateSupported(rate) {
			t.Errorf("%d Hz is not a rate Opus accepts", rate)
		}
	}
}

// TestFrameSamplesAt checks that a frame always lasts FrameDuration, whatever
// the rate: that is the length the encoder insists on, and getting it wrong
// means an error on every packet.
func TestFrameSamplesAt(t *testing.T) {
	want := map[int]int{8000: 160, 12000: 240, 16000: 320, 24000: 480, 48000: 960}
	for rate, n := range want {
		if got := FrameSamplesAt(rate); got != n {
			t.Errorf("FrameSamplesAt(%d) = %d, wanted %d", rate, got, n)
		}
	}
	if FrameSamplesAt(SampleRate) != FrameSamples {
		t.Errorf("FrameSamples and FrameSamplesAt disagree at the preferred rate")
	}
}

// The refusal has to arrive here and not further down, where it would become an
// error on every frame instead of one at opening time.
func TestNewOpusRefusesUnsupportedRates(t *testing.T) {
	if _, err := NewOpus(44100, 64); err == nil {
		t.Fatal("44100 Hz should have been refused")
	}
}
