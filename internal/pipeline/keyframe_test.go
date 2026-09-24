package pipeline

import "testing"

// frame simulates an Access Unit coming out of the encoder.
func frame(p *Pipeline, keyframe bool) {
	p.keyframeSeen(p.Stats.VideoFrames.Add(1), keyframe)
}

// TestKeyframeAnEncoderThatObeys: if the keyframe arrives within the tolerance,
// the request was served and the encoder is trusted.
func TestKeyframeAnEncoderThatObeys(t *testing.T) {
	p := New(Config{})

	for range 6 {
		p.keyframeAsked()
		frame(p, false) // one frame of delay: the encoder already has some in flight
		frame(p, true)
		frame(p, false)
	}

	if p.kfServed == 0 {
		t.Error("no request was recorded as served")
	}
	if p.kfDeclaredBad.Load() {
		t.Error("an encoder that obeys was declared useless")
	}
}

// TestKeyframeAnEncoderThatIgnores is the case this check exists to find: the
// encoder accepts the command, answers yes and produces nothing. No error code
// reveals it, only the frames that go past.
func TestKeyframeAnEncoderThatIgnores(t *testing.T) {
	p := New(Config{})

	for range 5 {
		p.keyframeAsked()
		for j := 0; j <= keyframeGraceFrames; j++ {
			frame(p, false)
		}
	}

	if p.kfServed != 0 {
		t.Errorf("%d requests counted as served by an encoder that produced none", p.kfServed)
	}
	if !p.kfDeclaredBad.Load() {
		t.Fatal("the encoder was not declared deaf after five dropped requests")
	}
	// And from then on whoever asks for a keyframe has to know it, instead of
	// believing one is on its way.
	if err := p.ForceKeyFrame(); err == nil {
		t.Error("ForceKeyFrame kept quiet about an encoder that does not answer")
	}
}

// TestKeyframeThePeriodicOneDoesNotCount: a keyframe arriving well after the
// request is the GOP's own, and taking it for an answer would make a deaf
// encoder look like a working one.
func TestKeyframeThePeriodicOneDoesNotCount(t *testing.T) {
	p := New(Config{})

	p.keyframeAsked()
	for j := 0; j <= keyframeGraceFrames; j++ {
		frame(p, false)
	}
	frame(p, true) // the periodic keyframe, out of time

	if p.kfServed != 0 {
		t.Error("a periodic keyframe was mistaken for an answer")
	}
}

// TestKeyframeWithoutRequestsNothingIsJudged: with no requests nothing is said,
// neither good nor bad. It is the difference between "it does not work" and "I
// do not know yet".
func TestKeyframeWithoutRequestsNothingIsJudged(t *testing.T) {
	p := New(Config{})
	for i := range 50 {
		frame(p, i%20 == 0)
	}
	if p.kfAsked != 0 || p.kfServed != 0 || p.kfDeclaredBad.Load() {
		t.Errorf("with no requests something was judged: asked %d, served %d, bad %v",
			p.kfAsked, p.kfServed, p.kfDeclaredBad.Load())
	}
}
