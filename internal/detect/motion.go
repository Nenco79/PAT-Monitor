package detect

import "time"

// Motion says whether something is moving in the room.
//
// It receives the pipeline's analysis frames: 160x90 in greyscale, five a
// second. **That is not a crop, it is a box average** — from 1280x720 it is 64
// sensor pixels averaged into each of these — so the image noise arrives here
// already divided by eight. It is the reason a detector this simple can work.
//
// **It compares against a blended frame, not against the previous one.** The
// blend is a first-order filter, one multiplication per pixel: it costs nothing
// and removes the noise left after the downscale, which in the dark is not
// little, because the automatic exposure raises the gain. The price is half a
// second of delay on the response, which on a baby monitor nobody notices.
//
// **The difference is measured with the global shift removed**, and this is the
// protection that matters more than all the others: when the automatic exposure
// changes gain **every** pixel changes together, and without that subtraction
// each adjustment of the camera becomes motion. In the dark the exposure hunts
// — measured elsewhere: 9.8 → 24.2 → 16.1 frames a second over twenty-six
// seconds — so it would have happened every night. A change that concerns the
// whole image is the camera, not the room.
//
// **It announces itself once per episode.** While the movement continues there
// is nothing new to say; it becomes news again only after the room has been
// still for a while. It is the same rule as the set of alerts, seen from the
// sensor's side.
type Motion struct {
	// PixelThreshold is how much a pixel has to change to count, on the 0-255
	// scale after the blend.
	PixelThreshold int
	// StartRatio is the fraction of changed pixels above which motion is
	// declared; StopRatio the one below which things go back to still. **There
	// are two and not one**: with a single threshold, movement just above the
	// limit switches on and off on every frame.
	StartRatio float64
	StopRatio  float64
	// Settle is how long the room has to stay still before the next movement
	// becomes news again. It also covers the gaps: whoever reads the status
	// does so every one or three seconds, and half a second of movement would
	// slip between two readings.
	Settle time.Duration

	smooth   []uint16 // the blended frame, in 8.8 fixed point
	prev     []uint16 // the one from the previous turn
	srcW     int      // what size the previous frame came from
	srcH     int
	moving   bool
	quiet    time.Time // since when the room has been below StopRatio
	ratio    float64
	maxDelta int
}

// The default thresholds are **provisional and declared as such**.
//
// They are not measured: they are the starting point for measuring them. The
// number to watch is Ratio, which the monitor writes to the log at -v, and the
// real thresholds are chosen from those numbers with the real room and the
// people who live in it. It is the lesson of the quantiser thresholds, which
// when guessed learned the room instead of the chip.
const (
	DefaultPixelThreshold = 12
	DefaultStartRatio     = 0.006 // ~86 pixels out of 14400
	DefaultStopRatio      = 0.003
	DefaultSettle         = 20 * time.Second
)

func NewMotion() *Motion {
	return &Motion{
		PixelThreshold: DefaultPixelThreshold,
		StartRatio:     DefaultStartRatio,
		StopRatio:      DefaultStopRatio,
		Settle:         DefaultSettle,
	}
}

// MotionState is what was seen in this frame.
type MotionState struct {
	// Ratio is the fraction of changed pixels. It is the number to watch when
	// tuning the thresholds, and it is the only reason it leaves this package.
	Ratio float64
	// Moving says whether the episode is in progress.
	Moving bool
	// Started is true **only** on the frame where the episode begins.
	Started bool
	// MaxDelta is the pixel that changed the most, in 0-255 levels.
	//
	// **It is there to tell two zeros apart.** A ratio of zero can mean "the
	// room is still" or "I am comparing two identical frames", that is, a
	// detector seeing nothing because of a wiring defect, and from the log the
	// two cases are the same line. On a live camera there is always a level or
	// two of noise: if this is zero as well, the fault is ours. It is the same
	// question as the Invoke counter — an event that never arrived and an event
	// that was discarded look the same from outside.
	//
	// It also says, in passing, where to put PixelThreshold, which is the
	// threshold it is compared against.
	MaxDelta int
}

// Feed consumes one analysis frame.
//
// It takes the instant as a parameter for the same reason as the media clock
// and the measured cadence: a function that reads the clock itself cannot be
// tested.
func (m *Motion) Feed(frame []byte, srcW, srcH int, now time.Time) MotionState {
	// **A change of source size is not motion**, and this is the twin of the
	// protection against the global shift: there the camera's gain changes,
	// here where the pixels come from changes.
	//
	// The analysis frame is always 160x90, so its **length never changes** and
	// a guard watching the length never fires: what changes is the provenance
	// of each cell — from 1280x720 it is the average of 8x8 sensor pixels, from
	// 640x352 of 4x4 — and on top of that the aspect ratios are not the same,
	// because the steps round to the macroblock (1.778 against 1.818). Out
	// comes a slightly different image everywhere, and **it is not a uniform
	// offset**, so subtracting the camera's breathing does not touch it.
	//
	// Measured: twelve events out of twenty fell between 0.22 and 0.59 s after
	// a format change, with the ratio between 0.0246 and 0.0275 — three
	// thousandths of spread over twelve episodes, while real movement in the
	// same log gave 0.0115, 0.0153, 0.0328, 0.1178 and 0.7047. A signature, not
	// a coincidence. The seven changes that produced no event are the ones that
	// fell inside the twenty seconds of rearming.
	//
	// Without the guard it is a false banner and a false chime every time the
	// network makes the size change, that is, at night: **an alarm that goes off
	// for no reason stops being read**, and this is the only alarm the program
	// has.
	//
	// It resets and one frame is lost, two hundred milliseconds. An episode
	// already in progress stays in progress: the size changed, not the room.
	if len(m.smooth) != len(frame) || srcW != m.srcW || srcH != m.srcH {
		m.srcW, m.srcH = srcW, srcH
		m.reset(frame)
		return MotionState{Moving: m.moving}
	}

	// First-order filter in 8.8 fixed point. **The eight fractional bits are
	// not elegance:** with 8-bit integers alone the filter stops as soon as the
	// difference falls below four, and that fixed residue is exactly where the
	// noise it was meant to remove lives.
	for i, p := range frame {
		m.smooth[i] += uint16((int(p)<<8 - int(m.smooth[i])) >> 2)
	}

	// First pass: the mean shift, that is, how much the whole image changed. It
	// is the camera breathing, and it has to go.
	sum := 0
	for i := range m.smooth {
		sum += int(m.smooth[i]) - int(m.prev[i])
	}
	shift := sum / len(m.smooth)

	// Second pass: count the pixels that moved **relative to that breathing**.
	threshold := m.PixelThreshold << 8
	changed, peak := 0, 0
	for i := range m.smooth {
		d := int(m.smooth[i]) - int(m.prev[i]) - shift
		if d < 0 {
			d = -d
		}
		if d > peak {
			peak = d
		}
		if d > threshold {
			changed++
		}
	}
	copy(m.prev, m.smooth)

	m.ratio = float64(changed) / float64(len(m.smooth))
	m.maxDelta = peak >> 8
	st := m.decide(now)
	st.MaxDelta = m.maxDelta
	return st
}

// decide applies the hysteresis: two thresholds on the value, and a quiet time
// before rearming.
func (m *Motion) decide(now time.Time) MotionState {
	st := MotionState{Ratio: m.ratio}

	if !m.moving {
		if m.ratio >= m.StartRatio {
			m.moving = true
			m.quiet = time.Time{}
			st.Moving, st.Started = true, true
		}
		return st
	}

	st.Moving = true
	if m.ratio >= m.StopRatio {
		m.quiet = time.Time{} // still moving
		return st
	}
	if m.quiet.IsZero() {
		m.quiet = now
		return st
	}
	if now.Sub(m.quiet) >= m.Settle {
		m.moving = false
		st.Moving = false
	}
	return st
}

func (m *Motion) reset(frame []byte) {
	m.smooth = make([]uint16, len(frame))
	m.prev = make([]uint16, len(frame))
	for i, p := range frame {
		m.smooth[i] = uint16(p) << 8
		m.prev[i] = m.smooth[i]
	}
	m.ratio, m.maxDelta = 0, 0
}
