//go:build windows

package tray

import (
	"math"
	"testing"
	"time"
)

// The cycle passes through the full colour, and comes back to it.
//
// **That is the property that separates a breath from a wrong colour**: if the
// minimum were not zero, the icon would never show the true green and the viewer
// would see a dimmed hue oscillating — that is, a colour that is none of the
// five in the palette, for the whole length of the check.
func TestThePulseAlwaysComesBackToTheFullColour(t *testing.T) {
	if got := pulseDim(0); got != 0 {
		t.Errorf("at the start of the cycle the dimming is %v, wanted 0", got)
	}
	if got := pulseDim(pulsePeriod); math.Abs(got) > 1e-9 {
		t.Errorf("at the end of the cycle the dimming is %v, wanted 0", got)
	}
	if got := pulseDim(pulsePeriod / 2); math.Abs(got-pulseDepth) > 1e-9 {
		t.Errorf("halfway through the cycle the dimming is %v, wanted %v", got, pulseDepth)
	}
}

// The cycle has to be made of enough frames to read as a breath, and must not go
// as far as switching the icon off.
//
// **The first draft of this test caught nothing**, and the mistake is worth more
// than the test: it measured the jump between two frames and compared it against
// a ceiling **derived from the same two parameters** it was meant to judge — so
// shortening the period to a quarter widened the ceiling along with the jump,
// and the test stayed green on a flicker. It is the family already paid for
// elsewhere: a test written by the same mistake it ought to catch catches
// nothing.
//
// The two requirements now do not depend on the values chosen: a cycle wants at
// least twelve frames, and the depth stays a dimming — beyond that it is no
// longer a breath but an icon going out, and the viewer sees a hole where the
// monitor was.
func TestTheCycleIsSmoothEnoughToReadAsBreathing(t *testing.T) {
	const minFrames = 12

	frames := int(pulsePeriod / pulseStep)
	if frames < minFrames {
		t.Errorf("the cycle has %d frames, fewer than the %d it takes not to "+
			"see the steps", frames, minFrames)
	}
	if pulseDepth <= 0 {
		t.Errorf("depth %.2f: nothing would be seen moving", pulseDepth)
	}
}

// **Every redraw of the icon brings the tooltip back too.**
//
// With the version 4 contract the standard little window is off and has to be
// asked for with `NIF_SHOWTIP`, which `notifyData` adds only to whoever carries
// `nifTip`: an icon-only update does not leave the tooltip as it was, **it takes
// it away**. With the pulse updating sixteen times a second, the result is an
// icon that moves and no longer answers the pointer.
//
// The defect was already described in the package and code written later
// reintroduced it, which is exactly what a test can prevent and a comment
// cannot.
func TestEveryIconUpdateAsksForTheTooltipToo(t *testing.T) {
	tr := &Tray{}
	nid := tr.notifyData(redrawFlags)

	if nid.UFlags&nifShowtip == 0 {
		t.Error("the redraw does not ask for NIF_SHOWTIP: with the v4 contract " +
			"the icon loses its tooltip on every frame")
	}
	if nid.UFlags&nifIcon == 0 {
		t.Error("the redraw does not carry the icon")
	}
}

// **At the bottom of the pulse the icon is still readable.**
//
// There used to be a ceiling on the depth here, and it was the wrong
// constraint: it said "do not go too low" when the thing to guarantee is
// "something is left to see". With the crescent exempt from the dimming the two
// stop coinciding, and indeed the depth has been able to reach one — that is,
// the disc goes out entirely — without the icon disappearing.
//
// The test looks at the pixels: in the darkest frame the bright ones of the moon
// have to remain. Dimming those too takes the count to zero, which is the defect
// this test is named after.
func TestAtTheBottomOfTheCycleTheIconIsStillReadable(t *testing.T) {
	const size = 32
	pix := iconPixels(PhaseOutside, size, 1)

	var bright int
	for i := 0; i+3 < len(pix); i += 4 {
		// Premultiplied: a bright pixel is bright on all three channels, and
		// only where it is opaque as well.
		if pix[i+3] > 0xF0 && pix[i] > 0xC0 && pix[i+1] > 0xC0 && pix[i+2] > 0xC0 {
			bright++
		}
	}
	if bright < 30 {
		t.Errorf("at the bottom of the cycle %d bright pixels remain: halfway "+
			"through the beat the icon would be a hole in the taskbar rather "+
			"than a presence", bright)
	}
}

// **Switching the pulse off restores the full colour.**
//
// Without that, the last frame drawn stays — a green dimmed by chance, which
// means nothing and never goes away: the icon is the state, and a state that
// depends on when an animation stopped is not a state.
func TestStoppingThePulseRestoresTheFullColour(t *testing.T) {
	tr := &Tray{}
	tr.pulsing = true
	tr.dim = pulseDepth
	// With no window the redraw has nowhere to go, and must not even try: what
	// matters here is that the dimming goes back to zero.
	tr.hwnd = 0

	tr.setPulse(false)
	if tr.pulsing {
		t.Error("the pulse is still on")
	}
	if tr.dim != 0 {
		t.Errorf("dimming %v after switching off, wanted 0", tr.dim)
	}
}

// The pulse is armed and disarmed only when the condition changes.
func TestThePulseIsArmedOnlyOnChange(t *testing.T) {
	tr := &Tray{}

	tr.setPulse(true)
	first := tr.pulseFrom
	if !tr.pulsing || first.IsZero() {
		t.Fatal("the pulse did not come on")
	}

	time.Sleep(2 * time.Millisecond)
	tr.setPulse(true)
	if !tr.pulseFrom.Equal(first) {
		t.Error("a second switch-on reset the phase: the cycle would start " +
			"over on every round of the one-second timer")
	}
}
