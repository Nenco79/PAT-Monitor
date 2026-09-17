package rtc

// The cadence **declared** to the encoder has to resemble the real one.
//
// The encoder divides the budget by the cadence it believes it has. Same room,
// same bitrate asked for, changing only the declared number:
//
//	declared 30 → 18.4 fps real → 1598 kbit/s of 2500 (64%) → QP 27.9
//	declared 15 → 13.7 fps real → 2312 kbit/s of 2500 (92%) → QP 24.3
//
// That is 3.6 points of quantiser given away, in the **ordinary condition of the
// night**: with little light the automatic exposure takes the camera down to
// 9-20 fps. The GOP follows from it too, kept in seconds and translated into
// frames by the declared cadence — at 9 real fps the keyframes arrive every 6.6
// seconds instead of every 2, and whoever opens the page waits.
//
// **The asymmetry is the opposite of the instinctive one.** Declaring **more**
// than reality costs only waste. Declaring **less** overshoots the cap, because
// if the light comes back every frame receives twice its due — and packets are
// lost. So it goes up quickly, comes down slowly, and when in doubt declares on
// the high side.

const (
	// cadenceConfirmUp: samples before **raising** the declared cadence.
	//
	// Two seconds. It is the delay in noticing that the camera has picked up
	// speed, and the time for which the cap is overshot: keeping it short is the
	// half that protects.
	cadenceConfirmUp = 2

	// cadenceConfirmDown: samples before **lowering** it.
	//
	// Ten seconds, because here the only cost of waiting is the waste we already
	// had, while being wrong means overshooting. And because the light flickers:
	// without this wait a passing cloud would rebuild the encoder twice.
	cadenceConfirmDown = 10

	// cadenceDownMargin: how far below the declared cadence the real one has to
	// sit for coming down to be worth it. A tenth: below that the gain does not
	// pay for rebuilding the encoder.
	cadenceDownMargin = 0.9

	// cadenceTolerance: by how much a step may be overshot without climbing to
	// the next.
	//
	// **It is needed because rounding up bounces on exact values.** Observed live
	// in the dark: the camera delivered 9.8 fps, the next sample a round 10.0,
	// and "the first step not below" went from 10 to 12 and back — three encoder
	// rebuilds in a minute over two tenths of a frame. Half a frame of tolerance
	// costs 5% of under-spending in the worst case and takes the bounce away.
	cadenceTolerance = 0.5
)

// cadenceSteps are the values that can be declared, lowest first.
//
// A short list and not any number at all: without steps the measured cadence
// swings by a frame and the encoder would be rebuilt constantly.
//
// **It is not the resolution scale's list, and today they agree anyway.**
// `scaleCadences` is {5, 2}, and both are values here — which was not true while
// that list held divisors, since a preset of 30 then imposed a 6 this list has
// never had. Nothing depended on the agreement, because when the scale imposes a
// cadence it is the **cap** that commands and this list is not consulted; the
// note stays because two lists of the same idea diverge silently, and the last
// time these two did it took printing the ladder to notice.
var cadenceSteps = []int{2, 5, 10, 12, 15, 20, 24, 30}

// cadenceToDeclare chooses the value to declare for a measured cadence.
//
// **It rounds up**, which is the safe direction: declaring more than the truth
// spends slightly less than allowed, declaring less overshoots the cap. With 18.4
// measured, 20 is declared, and the encoder spends 92% of what it is allowed
// instead of 61%.
func cadenceToDeclare(measured float64, max int) int {
	if measured <= 0 {
		return max
	}
	for _, s := range cadenceSteps {
		if float64(s) >= measured-cadenceTolerance && s <= max {
			return s
		}
	}
	return max
}

// cadenceGovernor decides the cadence to declare to the encoder.
//
// Like the other governors it knows neither the encoder nor the camera: it takes
// numbers and says what to declare. The fault it guards against is an encoder
// rebuilt constantly by flickering light, and that one does not reproduce twice
// alike from life.
type cadenceGovernor struct {
	declared int
	// capFPS is the last cap seen, and it serves to notice **when it changes**: a
	// cap that rises is the one event after which the measured cadence stops
	// describing the camera.
	capFPS   int
	up, down int
}

func newCadenceGovernor(initial int) *cadenceGovernor {
	return &cadenceGovernor{declared: initial, capFPS: initial}
}

// target says what to declare to the encoder.
//
//	measured  the cadence of the frames the encoder is really receiving
//	capFPS    the maximum allowed: the preset, or the cadence the scale imposes
//
// The cap commands always and at once: when the scale comes down to the bottom
// steps it is already dropping frames, and declaring more would mean dividing the
// budget by a cadence that does not exist — the same fault, in miniature.
func (c *cadenceGovernor) target(measured float64, capFPS int) (fps int, changed bool) {
	if c == nil || capFPS <= 0 {
		return capFPS, false
	}
	// **The cap commands in both directions**, and the half that rises is the one
	// that is easy to leave out.
	//
	// Coming down it is obvious: it is a limit, and it is respected at once.
	// Going up it is much less so, and it is the direction that cost six minutes
	// at 2 fps. While the scale holds the cadence down, the gate delivers what
	// the scale decided, so the **measured** cadence is that number and no longer
	// says anything about what the camera could give. Inheriting it beyond the
	// moment the cap rises again would mean inferring from our own last decision
	// that no different one can be taken.
	//
	// So it restarts from the new cap and lets the measurement reconverge, which
	// is true again as soon as the gate opens. In the meantime more than the truth
	// is declared, which is the direction that costs only waste — this file's
	// asymmetry, applied to the moment of not knowing.
	if capFPS != c.capFPS {
		rising := capFPS > c.capFPS
		c.capFPS = capFPS
		if rising || c.declared > capFPS {
			c.up, c.down = 0, 0
			if c.declared == capFPS {
				return c.declared, false
			}
			c.declared = capFPS
			return c.declared, true
		}
	}

	wanted := cadenceToDeclare(measured, capFPS)
	switch {
	case wanted > c.declared:
		// Going up: it is the direction that protects against overshooting, so
		// few confirmations.
		c.down = 0
		c.up++
		if c.up < cadenceConfirmUp {
			return c.declared, false
		}
	case wanted < c.declared && measured <= float64(c.declared)*cadenceDownMargin:
		// Coming down: only if the real cadence sits appreciably below the
		// declared one, and only after it has done so for long enough.
		c.up = 0
		c.down++
		if c.down < cadenceConfirmDown {
			return c.declared, false
		}
	default:
		c.up, c.down = 0, 0
		return c.declared, false
	}

	c.up, c.down = 0, 0
	if wanted == c.declared {
		return c.declared, false
	}
	c.declared = wanted
	return c.declared, true
}
