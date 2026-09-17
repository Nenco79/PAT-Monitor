package rtc

import (
	"testing"
	"time"
)

// **A scale built on one camera has no step matching what another delivers**,
// and that is the whole reason the governor is rebuilt when the size the capture
// starts from changes.
//
// The steps are fractions of the starting size — 1, 3/4, 1/2, rounded to the
// macroblock — so from 720p they are 1280x720, 960x528 and 640x352. A 640x480
// webcam is none of those: PickCameraSize hands back its own maximum, that size
// arrives, and `resync` deliberately does not invent a step for it. Left
// unaligned, `atFullSize` answers false for ever, which switches the bandwidth
// saving off and nails the bitrate to the cap for the whole session.
func TestAScaleBuiltOnAnotherCameraCannotAlign(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)

	// First the arithmetic this test rests on, rather than trusting it: 640x480
	// really is not one of the steps.
	for _, s := range g.steps {
		if s.Width == 640 && s.Height == 480 {
			t.Fatalf("640x480 is a step of a 720p scale, and this test believed it "+
				"was not: steps %v", g.steps)
		}
	}

	if _, ok := g.resync(640, 480, 30, time.Now()); ok {
		t.Error("a 720p scale claims to have aligned to a 640x480 capture")
	}
	if g.builtOn(640, 480, 30) {
		t.Error("a 720p scale says it was built on 640x480")
	}

	// Rebuilt on the camera that is really there, full size is a step again —
	// and it is the one that arrives.
	g = newScaleGovernor(640, 480, 30)
	if !g.builtOn(640, 480, 30) {
		t.Fatal("rebuilt on 640x480 and it says it was not")
	}
	if !g.atFullSize() {
		t.Error("rebuilt on the camera's own size and it does not consider itself " +
			"at full size: the saving would stay off")
	}
	if got := g.steps[0]; got.Width != 640 || got.Height != 480 {
		t.Errorf("the first step is %dx%d instead of the camera's own size",
			got.Width, got.Height)
	}
}

// The same size rebuilds nothing: `builtOn` is what keeps the tick from
// throwing the scene's judgements away once a second.
func TestTheSameSizeIsNotARebuild(t *testing.T) {
	g := newScaleGovernor(1280, 720, 30)

	if !g.builtOn(1280, 720, 30) {
		t.Error("the size it was built on is not recognised")
	}
	// The cadence counts too: it is what the bottom steps are made of, so a
	// camera delivering the same pixels at another rate is another scale.
	if g.builtOn(1280, 720, 15) {
		t.Error("a different cadence passes for the same scale, and the cadence " +
			"steps are computed from it")
	}
}

// **A scale that is not there was built on nothing**, which is what makes the
// first build and a rebuild the same line of code in the loop. Written the other
// way round — nil counting as "already built" — the scale would never come into
// being on a hub that reads its size from the capture.
func TestAScaleThatIsNotThereWasBuiltOnNothing(t *testing.T) {
	var g *scaleGovernor

	if g.builtOn(1280, 720, 30) {
		t.Error("a scale that does not exist claims to have been built on a size")
	}
	if g.builtOn(0, 0, 0) {
		t.Error("a scale that does not exist claims to have been built on nothing, " +
			"and the caller guards zero on its own")
	}
}
