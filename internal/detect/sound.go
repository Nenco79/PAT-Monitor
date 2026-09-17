package detect

import "time"

// Sound spots two sounds in the room: a cry and a bark.
//
// **It recognises the shape, not the sound.** The only two things that do not
// depend on the microphone, the gain and the distance are the **band** and the
// **cadence**, and they come from a dog's larynx and a baby's breathing: a bark
// lasts between 69 and 1000 ms and arrives in bursts 0.1-0.5 s apart, a cry is
// a long cycle of exhalation and pause, and both sit between 120 and 1640 Hz.
// The absolute level, on the other hand, changes from house to house, so no
// dBFS appears here: **the thresholds are distances from the floor**, and the
// room measures its own floor.
//
// **What comes out of here is not an alert: it is a "look at this".** With
// these means a shape can be told apart, not a species — a conversation is long
// and in the right band, and from here it is indistinguishable from a cry. What
// says what is in the room is internal/ced, and this package tells it when to
// look: the codes stay `cry` and `bark`.
//
// **Hence the tuning, which is a gate's**: it errs towards letting through,
// because whatever does not pass here the model will never see. The numbers are
// next to the constants.
//
// **The floor is only tracked while nothing is happening.** A reference that
// adapts during the event absorbs exactly the signal it is meant to detect, and
// a three-minute cry would disappear inside its own floor. It is the same
// mistake as quantiser thresholds learned from the machine, where the reference
// chased the scene.
type Sound struct {
	// Rate is the analysis stream's rate, needed to turn zero crossings into
	// Hz.
	Rate int

	// TriggerDB and ReleaseDB are the two thresholds, in dB **above the
	// floor**. Two and not one, for the same reason as motion: with a single
	// one, a sound just above the limit switches on and off on every block.
	TriggerDB float64
	ReleaseDB float64
	// FloorTau is the time constant with which the floor tracks the room. Slow
	// on purpose: the floor describes the empty room, not the last second.
	FloorTau time.Duration
	// FloorMin and FloorMax keep the floor inside sensible values even if the
	// room does something strange for half an hour.
	FloorMin float64
	FloorMax float64

	// MinHz and MaxHz are the accepted band, estimated from the zero crossings.
	// Outside it there is the thud below and the hiss above.
	MinHz float64
	MaxHz float64

	// The bark: short bursts close together.
	//
	// **BarkMin is a real parameter whose default cannot fire**, and that is
	// worth knowing before anybody retunes it. A burst is measured between two
	// blocks, and the blocks handed to Feed are 100 ms — by construction and in
	// both readers, the monitor and pat-sounds — so the duration reaching
	// classify is always a positive multiple of 100 ms. The shortest burst that
	// can exist, one block, is already past DefaultBarkMin of 70 ms: the bound
	// sits off the grid it is compared against. `pat-sounds gate -sweep` shows
	// the field itself is live — above 100 ms it does bite — so what is inert is
	// the default, not the knob.
	//
	// **It is left where it is on purpose.** The gate errs towards letting
	// through, because what it refuses the model never sees, and a lower bound
	// that never fires loses nothing. Raising it onto the grid would be
	// tightening a gate to no measured end, which is the wrong direction here.
	BarkMin    time.Duration
	BarkMax    time.Duration
	BarkHits   int
	BarkWindow time.Duration

	// The cry: long bursts. CryLong is how long a single burst has to last to
	// be enough on its own.
	CryMin    time.Duration
	CryLong   time.Duration
	CryHits   int
	CryWindow time.Duration

	// MaxBurst is how long a sound can last **without a single pause** before it
	// stops being an event and becomes the room.
	//
	// **It is needed because a frozen floor, left alone, freezes forever.** If
	// somebody switches a fan on, the level rises by twenty dB and stays there:
	// the floor no longer follows it — it is frozen by the event — and the
	// detector stays lit for life, never recognising anything and never
	// recovering. Found by a test, not by reasoning.
	//
	// The criterion that separates the two cases is not the duration in itself:
	// **it is that neither a cry nor a bark is continuous.** A cry is a cycle of
	// exhalation and pause, a bark is separate bursts. Twenty seconds of
	// unbroken sound is neither, it is a machine that has been switched on, and
	// then the floor moves up to it and things start again.
	MaxBurst time.Duration

	// Settle is how long an event stays declared after the last piece of
	// evidence.
	//
	// It does two jobs: it holds the alert long enough for whoever reads the
	// status every three seconds to see it, and it stops a dog barking for ten
	// minutes from announcing itself thirty times.
	Settle time.Duration

	floor     float64
	haveFloor bool
	loud      bool
	burstFrom time.Time
	burstBand bool
	last      time.Time
	barkHits  []time.Time
	cryHits   []time.Time
	cryUntil  time.Time
	barkUntil time.Time
	lastLevel float64
	lastCross float64
}

// **These values tune a gate, not a decider**, and that is the whole
// difference: what says what is in the room is the model, and whatever does not
// pass here the model will never see. So it errs towards letting through.
//
// Measured on three public sets with `pat-sounds gate` — the numbers are in
// baselines/sounds.txt:
//
//   - **a single burst, not two**. With two, recall on cry is 92.5% and on
//     bark 65%; with one they become 100% and 85%, at the price of eleven
//     points more negatives that the model will throw out. As a decider two
//     bursts are right; as a gate they are twenty points of lost events.
//   - **the band stays 150-2500 Hz, and buys very little**: removing it
//     altogether costs four points of negatives and does not touch the
//     positives. Narrowing it to the 120-1640 Hz of the physiological
//     literature would cost 12.5 points of recall on cry to gain 5.7. It stays
//     because it is nearly free.
//   - **what the gate really separates is the intermittent from the
//     continuous**, and there it is good: rain 0%, engine idling 0%, air
//     conditioning 2%. A rooster passes at 100% and that is not a defect —
//     telling it from a dog is the model's job.
//
// **The distance from the floor is not something a dataset can teach**: it is a
// property of the room, not of the sound. The twelve dB rest on the only
// measurement that concerns them — the floor measured with a real microphone in
// an empty room, where this gate opens six times in three minutes — and they
// are raised above the 3 dB of the literature because at night 3 dB would be
// tripped by a radiator.
const (
	DefaultTriggerDB = 12.0
	DefaultReleaseDB = 6.0
	DefaultFloorTau  = 45 * time.Second
	DefaultFloorMin  = -90.0
	DefaultFloorMax  = -30.0
	DefaultMinHz     = 150.0
	DefaultMaxHz     = 2500.0
	// 70 ms is off the 100 ms block grid and so never filters anything: see
	// the note on the BarkMin field.
	DefaultBarkMin     = 70 * time.Millisecond
	DefaultBarkMax     = 400 * time.Millisecond
	DefaultBarkHits    = 1
	DefaultBarkWindow  = 1500 * time.Millisecond
	DefaultCryMin      = 500 * time.Millisecond
	DefaultCryLong     = 1200 * time.Millisecond
	DefaultCryHits     = 1
	DefaultCryWindow   = 6 * time.Second
	DefaultMaxBurst    = 20 * time.Second
	DefaultSoundSettle = 20 * time.Second
)

func NewSound(rate int) *Sound {
	return &Sound{
		Rate:       rate,
		TriggerDB:  DefaultTriggerDB,
		ReleaseDB:  DefaultReleaseDB,
		FloorTau:   DefaultFloorTau,
		FloorMin:   DefaultFloorMin,
		FloorMax:   DefaultFloorMax,
		MinHz:      DefaultMinHz,
		MaxHz:      DefaultMaxHz,
		BarkMin:    DefaultBarkMin,
		BarkMax:    DefaultBarkMax,
		BarkHits:   DefaultBarkHits,
		BarkWindow: DefaultBarkWindow,
		CryMin:     DefaultCryMin,
		CryLong:    DefaultCryLong,
		CryHits:    DefaultCryHits,
		CryWindow:  DefaultCryWindow,
		MaxBurst:   DefaultMaxBurst,
		Settle:     DefaultSoundSettle,
	}
}

// SoundState is what is being heard now.
type SoundState struct {
	// Cry and Bark stay true for the whole length of the episode, not only for
	// the block it was recognised in: whoever reads the status does so every
	// few seconds, and a bark lasts half a second.
	Cry  bool
	Bark bool
	// Floor and Level are the measured floor and the current level, in dBFS.
	// They leave this package for the log: they are the two numbers that show
	// whether a threshold is badly placed, and without them there is nothing to
	// look at.
	Floor float64
	Level float64
	// DominantHz is the coarse estimate of the dominant frequency.
	DominantHz float64
}

// Feed consumes one analysis block.
func (s *Sound) Feed(b Block, now time.Time) SoundState {
	if b.Samples == 0 {
		return s.state(now)
	}
	if !s.haveFloor {
		// **Digital silence is not a room**, and taking it for the floor costs
		// half a minute of blindness at every start: the first blocks arrive
		// before the microphone delivers anything, they are worth -99, and from
		// there everything that follows is fifty dB above the floor — that is,
		// an unbroken sound, which only clears when MaxBurst notices.
		// Measured live: a floor of -90 for thirty seconds with the room at -41.
		//
		// A genuinely mute microphone never initialises the floor, and that is
		// right: there is nothing to detect there, and the digital-silence
		// alert already says so.
		if b.RMSdBFS <= SilenceFloorDBFS {
			s.last = now
			return s.state(now)
		}
		s.floor, s.haveFloor, s.last = b.RMSdBFS, true, now
	}
	dt := now.Sub(s.last)
	if dt < 0 || dt > time.Second {
		dt = 0 // gaps and clock jumps do not move the floor
	}
	s.last = now
	s.lastLevel = b.RMSdBFS
	s.lastCross = b.CrossRate * float64(s.Rate) / 2

	inBand := s.lastCross >= s.MinHz && s.lastCross <= s.MaxHz

	switch {
	case !s.loud && b.RMSdBFS >= s.floor+s.TriggerDB:
		s.loud, s.burstFrom, s.burstBand = true, now, inBand
	case s.loud && b.RMSdBFS < s.floor+s.ReleaseDB:
		s.loud = false
		s.classify(now.Sub(s.burstFrom), s.burstBand, now)
	case s.loud && s.MaxBurst > 0 && now.Sub(s.burstFrom) > s.MaxBurst:
		// No pause in twenty seconds: this is not an event, it is the new
		// floor. Nothing is classified — it has the shape of nothing we know
		// how to recognise — and the floor moves up to it in one go, otherwise
		// it would stay behind forever.
		s.loud = false
		s.floor = clampFloat(b.RMSdBFS, s.FloorMin, s.FloorMax)
	case s.loud:
		s.burstBand = s.burstBand || inBand
	default:
		// Silence: **only here** does the floor move. During a sound it is
		// frozen, otherwise the sound eats its own reference.
		if s.FloorTau > 0 && dt > 0 {
			a := float64(dt) / float64(s.FloorTau)
			if a > 1 {
				a = 1
			}
			s.floor += (b.RMSdBFS - s.floor) * a
			s.floor = clampFloat(s.floor, s.FloorMin, s.FloorMax)
		}
	}
	return s.state(now)
}

// classify looks at the shape of the burst that has just ended.
//
// The two shapes do not overlap by construction — a bark ends where a cry
// begins — so no precedence between them is needed.
func (s *Sound) classify(dur time.Duration, inBand bool, now time.Time) {
	if !inBand {
		return // a thud, or a hiss: it has not got the right voice
	}
	switch {
	case dur >= s.CryLong:
		s.cryUntil = now.Add(s.Settle)
	case dur >= s.CryMin:
		s.cryHits = recent(append(s.cryHits, now), now, s.CryWindow)
		if len(s.cryHits) >= s.CryHits {
			s.cryUntil = now.Add(s.Settle)
			s.cryHits = nil
		}
	case dur >= s.BarkMin && dur <= s.BarkMax:
		s.barkHits = recent(append(s.barkHits, now), now, s.BarkWindow)
		if len(s.barkHits) >= s.BarkHits {
			s.barkUntil = now.Add(s.Settle)
			s.barkHits = nil
		}
	}
}

func (s *Sound) state(now time.Time) SoundState {
	if !s.haveFloor {
		// **Zero dBFS is full scale**, that is, the loudest sound
		// representable: leaving the fields at zero until there is a
		// measurement makes "I do not know yet" read as "it is playing at full
		// volume". Until it is known, silence is what is declared.
		return SoundState{Floor: SilenceFloorDBFS, Level: SilenceFloorDBFS}
	}
	return SoundState{
		Cry:        now.Before(s.cryUntil),
		Bark:       now.Before(s.barkUntil),
		Floor:      s.floor,
		Level:      s.lastLevel,
		DominantHz: s.lastCross,
	}
}

// recent keeps only the instants inside the window.
func recent(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) <= window {
			out = append(out, t)
		}
	}
	return out
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
