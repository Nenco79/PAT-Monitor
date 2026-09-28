package rtc

import "testing"

// The thresholds have to sit where the measurements put the break: blockiness
// measured at 38.5 on Quick Sync, and libwebrtc considers the picture broken at
// 37 (openh264) and 39 (VideoToolbox). Thirty-eight falls in the middle of three
// independent measurements.
func TestTheBreakThresholdSitsWhereTheBreakWasMeasured(t *testing.T) {
	if qpBreakAt < 37 || qpBreakAt > 39 {
		t.Errorf("break threshold %d outside the measured range 37-39", qpBreakAt)
	}
	if qpBreakAt > 51 {
		t.Errorf("break threshold %d: the quantiser ends at 51, so it would never fire", qpBreakAt)
	}
}

// Between climb and break there has to be room for the cost of one step: going
// up, the pixels grow by three quarters and the quantiser worsens by 4-5 points,
// so with less room one climbs only to come straight back down — that is, the
// picture changes shape twice for nothing.
func TestOneStepFitsBetweenClimbAndBreak(t *testing.T) {
	if qpBreakAt-qpClimbAt < 5 {
		t.Errorf("between climb (%d) and break (%d) there is no room for the step",
			qpClimbAt, qpBreakAt)
	}
}
