package rtc

// The quantiser thresholds, and why they are a fixed number.
//
// For a while they were **learnt**: the quantiser this machine produces at full
// bandwidth was measured and the thresholds were distances from there. The idea
// came from a true fact — libwebrtc uses 24/37 with openh264 and 28/39 with
// VideoToolbox for the same H.264 — but **what it learnt was the room**:
//
//	20:27  reference=20  threshold=28
//	23:31  reference=31  threshold=39
//
// Eleven points in one evening, that is the whole break margin, and in the worst
// direction: in the dark the scene costs little and the threshold gets stricter
// **exactly when the picture is good**. A hard scene is what the thresholds have
// to detect, and a reference that absorbs it is the sensor chasing the signal;
// no time constant fixes that.
//
// Thirty-eight is where **three independent measurements** put the break:
// blockiness at 38.5 on Quick Sync, and libwebrtc's two values, 37 and 39. What
// is lost is adaptation between chips, which is the thing that was seen not to
// work.
const (
	// qpReference is the quantiser at which the picture counts as fine on any
	// H.264 encoder.
	//
	// It is the same value as the quality loop's default target, and not by
	// coincidence: they are the same idea, "the picture is clean here". They
	// stay two distinct numbers, though, because they answer two different
	// questions — the target is how much to spend, the threshold is when to take
	// pixels away — and whoever switches the saving off with `target_qp: 0` must
	// not switch the scale off as well.
	qpReference = 30

	// qpBreakSpan: how far above the reference the picture is broken.
	//
	// Eight points is about two and a half times fewer bits per pixel. The
	// resulting threshold, 38, is consistent with everything measured:
	// blockiness at 38.5 on Quick Sync, and 37/39 in libwebrtc.
	qpBreakSpan = 8

	// qpClimbSpan: how far above the reference one may sit and still afford to
	// go **up** a step.
	//
	// It is not the break the other way round, and it is tied to it by the
	// arithmetic of the step: going up, the pixels grow by about three quarters
	// and the quantiser worsens by 4-5 points, so that cost has to fit between
	// the two thresholds. Starting from reference+3 one arrives at reference+8,
	// that is exactly at the limit and no further. Whoever touches `qpBreakSpan`
	// has to touch this too.
	qpClimbSpan = 3
)

// qpLimits are the thresholds the scale compares the measured quantiser with.
// known stays in the type because the scale has to be able to work without a
// quantiser too, falling back on the bandwidth: that is the case of the encoder
// it cannot be read from.
type qpLimits struct {
	known   bool
	breakAt int
	climbAt int
}

// qpThresholds are the thresholds in force. They depend neither on the machine
// nor on the moment: if one day they had to be tuned for a chip that behaves
// very differently, there is one right knob and it is measured with
// `pat-capture`, not learnt while the monitor works.
func qpThresholds() qpLimits {
	return qpLimits{
		known:   true,
		breakAt: qpReference + qpBreakSpan,
		climbAt: qpReference + qpClimbSpan,
	}
}
