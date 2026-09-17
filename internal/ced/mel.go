package ced

import (
	"fmt"
	"math"
)

// Frontend turns a waveform into the mel spectrogram in decibels that the model
// expects as input.
//
// **The window and the filterbank are not built here: they come from the
// model's file.** They are its own, measured together with the weights, and
// rebuilding them means guessing a convention — CED's window, for one, is a
// *periodic* Hann and not the symmetric one written by instinct. See
// internal/gguf.
//
// **The waveform's scale matters, and that is not obvious.** The topDB floor is
// relative to the spectrogram's maximum, so on its own it would absorb any
// volume; but underneath it there is amin, which stands still at 1e-10, and on
// a band holding nothing it bites first. It follows that a waveform in [-1,1]
// and one in 16-bit integer units **do not give the model the same input**, and
// the difference shows exactly where the room is flat, that is, at night. What
// is expected here is samples in [-1,1], which is what whoever opens a WAV
// reads, and it is to be re-checked against the oracle the first time scores
// are compared.
//
// **It is not reentrant**: the working buffers are its own, because the
// transform runs a thousand times per window and allocating on every frame
// would cost more than the transform. One per goroutine.
type Frontend struct {
	nFFT   int
	hop    int
	nMels  int
	nFreqs int

	window []float32
	fb     []float32 // [nMels][nFreqs], one row per band

	// The constants of the conversion to decibels, also from the file.
	amin  float64
	ref   float64
	mult  float64
	topDB float64

	fft    *fft
	padded []float32
	frame  []float32
	spec   []float32
}

// NewFrontend builds the front-end from the pieces read out of the model.
func NewFrontend(nFFT, hop, nMels int, window, filterbank []float32, amin, ref, mult, topDB float64) (*Frontend, error) {
	if nFFT <= 0 || nFFT&(nFFT-1) != 0 {
		return nil, fmt.Errorf("ced: n_fft is %d, which is not a power of two", nFFT)
	}
	if hop <= 0 {
		return nil, fmt.Errorf("ced: hop is %d", hop)
	}
	nFreqs := nFFT/2 + 1
	if len(window) != nFFT {
		return nil, fmt.Errorf("ced: the window is %d long, n_fft is %d", len(window), nFFT)
	}
	if len(filterbank) != nMels*nFreqs {
		return nil, fmt.Errorf("ced: the filterbank has %d weights, %d mels by %d bins want %d",
			len(filterbank), nMels, nFreqs, nMels*nFreqs)
	}
	if amin <= 0 {
		return nil, fmt.Errorf("ced: amin is %v, and the logarithm needs a floor above zero", amin)
	}
	return &Frontend{
		nFFT: nFFT, hop: hop, nMels: nMels, nFreqs: nFreqs,
		window: window, fb: filterbank,
		amin: amin, ref: ref, mult: mult, topDB: topDB,
		fft:   newFFT(nFFT),
		frame: make([]float32, nFFT),
		spec:  make([]float32, nFreqs),
	}, nil
}

// Frames says how many frames a waveform of n samples will produce.
//
// With centred padding the sum is `1 + n/hop` and not `1 + (n-nFFT)/hop`: the
// reflection adds exactly nFFT samples, and the two cancel out. Getting it
// wrong gives a spectrogram shorter than the model expects, which somebody then
// fills with zeros.
func (fe *Frontend) Frames(n int) int { return 1 + n/fe.hop }

// Mel returns the spectrogram in decibels, one row per band: the value of band
// f at frame t sits in mel[f*frames+t].
func (fe *Frontend) Mel(wave []float32) (mel []float32, frames int, err error) {
	pad := fe.nFFT / 2
	if len(wave) <= pad {
		return nil, 0, fmt.Errorf("ced: %d samples cannot be reflected around a %d sample pad",
			len(wave), pad)
	}
	frames = fe.Frames(len(wave))

	// **The padding is a reflection, not zeros**, and that is not a detail: the
	// head and tail frames come out different, and those are exactly the ones
	// where a sound can begin. The reflection does not repeat the edge sample —
	// padded[i] = wave[pad-i], so the first reflected sample is wave[pad] and
	// the last is wave[1].
	need := len(wave) + 2*pad
	if cap(fe.padded) < need {
		fe.padded = make([]float32, need)
	}
	p := fe.padded[:need]
	reflectPad(p, wave, pad)

	mel = make([]float32, fe.nMels*frames)
	for t := 0; t < frames; t++ {
		// The last frame always fits, and it is worth seeing why: it starts at
		// (n/hop)*hop, which by integer division does not exceed n, and ends
		// nFFT further on, that is, within n + nFFT, which is exactly the
		// padded length. A branch completing with zeros here would never run,
		// and it would sit there being reassuring without doing anything.
		seg := p[t*fe.hop : t*fe.hop+fe.nFFT]
		for i := range fe.frame {
			fe.frame[i] = seg[i] * fe.window[i]
		}
		fe.fft.power(fe.frame, fe.spec)
		for b := 0; b < fe.nMels; b++ {
			row := fe.fb[b*fe.nFreqs : (b+1)*fe.nFreqs]
			var s float32
			for k, w := range row {
				s += w * fe.spec[k]
			}
			mel[b*frames+t] = s
		}
	}

	fe.toDB(mel)
	return mel, frames, nil
}

// toDB converts to decibels and clips at the bottom.
//
// **The clip is relative to this spectrogram's maximum, not to an absolute
// value**, and that is the property that decides the shape of everything else:
// the normalisation depends on the whole window, so the front-end cannot work
// frame by frame in a stream — the ten seconds are accumulated and transformed
// together. Whoever wrote an incremental API would find out here that it cannot
// be done.
func (fe *Frontend) toDB(mel []float32) {
	logRef := fe.mult * math.Log10(math.Max(fe.amin, fe.ref))
	amax := float32(math.Inf(-1))
	for i, v := range mel {
		db := float32(fe.mult*math.Log10(math.Max(float64(v), fe.amin)) - logRef)
		mel[i] = db
		if db > amax {
			amax = db
		}
	}
	if fe.topDB <= 0 {
		return
	}
	floor := amax - float32(fe.topDB)
	for i, v := range mel {
		if v < floor {
			mel[i] = floor
		}
	}
}

// reflectPad fills dst with wave surrounded by pad reflected samples.
//
// The reflection **does not repeat the edge sample**: on the left it goes from
// wave[pad] down to wave[1], on the right from wave[len-2] down to
// wave[len-1-pad]. Repeating it — which is the other convention, the one numpy
// calls `symmetric` — shifts the content by one sample and gives no error at
// all: the spectrogram comes out plausible and different from the model's.
func reflectPad(dst, wave []float32, pad int) {
	n := len(wave)
	for i := 0; i < pad; i++ {
		dst[i] = wave[pad-i]
		dst[len(dst)-1-i] = wave[n-1-pad+i]
	}
	copy(dst[pad:], wave)
}
