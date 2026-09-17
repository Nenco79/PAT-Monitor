package rtc

import "testing"

// The thresholds have to sit where the measurements put the break: blockiness
// measured at 38.5 on Quick Sync, and libwebrtc considers the picture broken at
// 37 (openh264) and 39 (VideoToolbox). Thirty-eight falls in the middle of three
// independent measurements.
func TestTheBreakThresholdSitsWhereTheBreakWasMeasured(t *testing.T) {
	lim := qpThresholds()
	if !lim.known {
		t.Fatal("the thresholds must always be there: they are no longer learnt")
	}
	if lim.breakAt < 37 || lim.breakAt > 39 {
		t.Errorf("break threshold %d outside the measured range 37-39", lim.breakAt)
	}
	if lim.breakAt > 51 {
		t.Errorf("break threshold %d: the quantiser ends at 51, so it would never fire", lim.breakAt)
	}
}

// Between climb and break there has to be room for the cost of one step: going
// up, the pixels grow by three quarters and the quantiser worsens by 4-5 points,
// so with less room one climbs only to come straight back down — that is, the
// picture changes shape twice for nothing.
func TestOneStepFitsBetweenClimbAndBreak(t *testing.T) {
	lim := qpThresholds()
	if lim.breakAt-lim.climbAt < 5 {
		t.Errorf("between climb (%d) and break (%d) there is no room for the step",
			lim.climbAt, lim.breakAt)
	}
}

// And the thresholds do not depend on the room: that is the whole point. A
// learnt reference swings by eleven points in one evening on the same machine,
// and in the dark it gets stricter exactly when the picture is good.
func TestTheThresholdsNeverChange(t *testing.T) {
	a := qpThresholds()
	b := qpThresholds()
	if a != b {
		t.Errorf("the thresholds changed between two readings: %+v against %+v", a, b)
	}
}
