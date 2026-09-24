package pipeline

import (
	"math"
	"testing"

	"patmonitor/internal/audiocodec"
	"patmonitor/internal/resample"
)

// **The analysis stream comes out at 16 kHz for any microphone**, and that is
// the property the two roads have to share. Without it a 24 kHz microphone would
// deliver 24 and declare them, and on that machine the recogniser could not run
// at all.
//
// 48 kHz is the development case and proves nothing about portability. The ones
// that matter are 24, where the ratio is not a whole number, and 8, where the
// microphone sits **below** the analysis rate and the stream has to go up.
func TestAnalysisPlan(t *testing.T) {
	cases := []struct {
		capture, decim int
		resampled      bool
	}{
		{48000, 3, false},
		{16000, 1, false},
		{24000, 1, true}, // 24/16 is not a whole number: it goes through the resampler
		{12000, 1, true},
		{8000, 1, true}, // it goes up, and half the spectrum comes out empty
	}
	for _, c := range cases {
		decim, resampled := analysisPlan(c.capture)
		if decim != c.decim || resampled != c.resampled {
			t.Errorf("analysisPlan(%d) = (%d, %v), want (%d, %v)",
				c.capture, decim, resampled, c.decim, c.resampled)
		}
		if decim < 1 {
			t.Fatalf("decim %d at %d Hz: it would divide by zero in the averaging loop", decim, c.capture)
		}
	}
}

// Every microphone we accept must have a valid plan, and **it must reach 16
// kHz**: if a rate were one day added to Opus's list without looking here, this
// test would say so.
func TestAnalysisPlanCoversEveryOpusRate(t *testing.T) {
	for _, rate := range audiocodec.SupportedRates {
		decim, resampled := analysisPlan(rate)
		if decim < 1 || rate%decim != 0 {
			t.Errorf("%d Hz: invalid plan (decim %d)", rate, decim)
			continue
		}
		if !resampled && rate/decim != AnalysisSampleRate {
			t.Errorf("%d Hz: averaging by %d reaches %d, not %d",
				rate, decim, rate/decim, AnalysisSampleRate)
		}
		if resampled {
			if _, err := resample.NewStream(rate, AnalysisSampleRate); err != nil {
				t.Errorf("%d Hz: the resampler refuses it: %v", rate, err)
			}
		}
	}
}

// **The two roads must give the same thing where both are possible.** At 48 kHz
// the stream is averaged by three and the resampler is not used — but were it
// ever to be, the stream must not change character: same tone, same level. It is
// the proof that the new road is no worse than the old one in the case where the
// two can be compared.
func TestTheTwoRoadsAgreeWhereBothExist(t *testing.T) {
	const from = 48000
	in := make([]float32, from)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / from))
	}

	// Averaging by three, as the pipeline does.
	decim, _ := analysisPlan(from)
	averaged := make([]float32, 0, len(in)/decim)
	for i := 0; i+decim <= len(in); i += decim {
		var s float32
		for j := range decim {
			s += in[i+j]
		}
		averaged = append(averaged, s/float32(decim))
	}

	rs, err := resample.New(from, AnalysisSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	filtered := rs.Convert(in)

	if len(filtered) != len(averaged) {
		t.Fatalf("%d samples against %d", len(filtered), len(averaged))
	}
	// The energy in the middle is what is compared: the two roads have
	// different filters — an average of three and a windowed sinc — so the
	// samples do not coincide, but the tone must come out at the same level.
	energy := func(s []float32) float64 {
		var e float64
		for _, v := range s[len(s)/4 : 3*len(s)/4] {
			e += float64(v) * float64(v)
		}
		return math.Sqrt(e / float64(len(s)/2))
	}
	a, b := energy(averaged), energy(filtered)
	if d := 20 * math.Log10(b/a); math.Abs(d) > 0.5 {
		t.Errorf("the resampler gives %.2f dB against the average: the two roads diverge", d)
	}
}

func TestTheClampKeepsSamplesInRange(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int16
	}{
		{0, 0}, {100.4, 100}, {-100.4, -100},
		{40000, 32767}, {-40000, -32768},
		{32767.9, 32767}, {-32768.9, -32768},
	} {
		if got := toS16(c.in); got != c.want {
			t.Errorf("toS16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
