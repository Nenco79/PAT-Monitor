package detect

import (
	"math"
	"testing"
	"time"
)

const soundRate = 16000

// block makes a 20 ms analysis block with a given level and dominant frequency.
// It does not synthesise the wave: the chain under test starts after
// AnalyzeS16LE, and going through samples would test two things at once.
func block(dbfs, hz float64) Block {
	return Block{
		Samples:   soundRate / 50,
		RMSdBFS:   dbfs,
		PeakdBFS:  dbfs + 3,
		CrossRate: 2 * hz / float64(soundRate),
	}
}

const (
	quiet = -70.0 // empty room
	loud  = -40.0 // somebody making themselves heard: 30 dB above
)

// run sends 20 ms blocks for the given duration and returns the last state.
func run(s *Sound, b Block, from time.Time, d time.Duration) (SoundState, time.Time) {
	var st SoundState
	t := from
	for ; t.Sub(from) < d; t = t.Add(20 * time.Millisecond) {
		st = s.Feed(b, t)
	}
	return st, t
}

func newTuned() (*Sound, time.Time) {
	s := NewSound(soundRate)
	t := time.Date(2026, 8, 24, 23, 0, 0, 0, time.UTC)
	// A minute of empty room, so the floor is as settled as it would be after
	// a few minutes with the monitor running.
	_, t = run(s, block(quiet, 500), t, time.Minute)
	return s, t
}

func TestAQuietRoomHearsNothing(t *testing.T) {
	s, at := newTuned()
	st, _ := run(s, block(quiet, 500), at, 30*time.Second)
	if st.Cry || st.Bark {
		t.Errorf("cry=%v bark=%v on an empty room", st.Cry, st.Bark)
	}
	if math.Abs(st.Floor-quiet) > 1 {
		t.Errorf("floor %.1f, wanted close to %.1f", st.Floor, quiet)
	}
}

// Two short bursts close together are a bark: it is the cadence that says so,
// not the level.
func TestTwoShortBurstsAreABark(t *testing.T) {
	s, at := newTuned()

	for i := 0; i < 2; i++ {
		_, at = run(s, block(loud, 900), at, 200*time.Millisecond)
		_, at = run(s, block(quiet, 500), at, 300*time.Millisecond)
	}
	st := s.Feed(block(quiet, 500), at)

	if !st.Bark {
		t.Error("two 200 ms bursts 300 ms apart were not read as a bark")
	}
	if st.Cry {
		t.Error("read as a cry as well: the two shapes must not overlap")
	}
}

// **A single burst opens the gate, and it did not used to.**
//
// Asking for two bursts was right while this was the decider: it kept out thuds
// in the right band — a door, an object falling. As a gate it is the opposite,
// and that is measured: two bursts cost twenty points of recall on barking,
// that is, one dog in five the model will never see. The door is thrown out by
// the model, which knows how to tell it apart; this one does not, and must not
// try.
func TestASingleBurstOpensTheGate(t *testing.T) {
	s, at := newTuned()
	_, at = run(s, block(loud, 900), at, 200*time.Millisecond)
	st, _ := run(s, block(quiet, 500), at, time.Second)

	if !st.Bark {
		t.Error("an isolated burst did not open the gate: the model will never see it")
	}
}

// A long burst is a cry, and one is enough.
func TestALongBurstIsACry(t *testing.T) {
	s, at := newTuned()
	_, at = run(s, block(loud, 500), at, 1500*time.Millisecond)
	st := s.Feed(block(quiet, 500), at)

	if !st.Cry {
		t.Error("a 1.5 s burst was not read as a cry")
	}
	if st.Bark {
		t.Error("read as a bark as well")
	}
}

// **The false positive this detector exists to avoid.** A thud is as loud as a
// bark and lasts as long as a bark: the only thing that tells them apart is the
// band.
func TestAThumpIsNotABark(t *testing.T) {
	s, at := newTuned()

	for i := 0; i < 3; i++ {
		_, at = run(s, block(loud, 60), at, 150*time.Millisecond) // 60 Hz: a thud
		_, at = run(s, block(quiet, 500), at, 300*time.Millisecond)
	}
	st := s.Feed(block(quiet, 500), at)

	if st.Bark || st.Cry {
		t.Errorf("a 60 Hz thud was recognised: cry=%v bark=%v", st.Cry, st.Bark)
	}
}

func TestHissIsNotACry(t *testing.T) {
	s, at := newTuned()
	// 6 kHz: hiss, above the band of the voice.
	_, at = run(s, block(loud, 6000), at, 2*time.Second)
	st := s.Feed(block(quiet, 500), at)

	if st.Cry || st.Bark {
		t.Errorf("a 6 kHz hiss was recognised: cry=%v bark=%v", st.Cry, st.Bark)
	}
}

// **The trap already paid for on the quantiser thresholds, in audio form.** If
// the floor tracked during the event, a long cry would eat its own reference
// and disappear.
//
// The cry in this test is **cyclic**, as a real cry is: a second or two of
// voice, half a second of pause. A continuous thirty-second tone would not be a
// cry but a machine that has been switched on, and that is the case MaxBurst
// handles on purpose.
func TestTheFloorDoesNotFollowTheEvent(t *testing.T) {
	s, at := newTuned()
	before := s.floor

	for i := 0; i < 15; i++ {
		_, at = run(s, block(loud, 500), at, 1500*time.Millisecond)
		_, at = run(s, block(quiet, 500), at, 500*time.Millisecond)
	}
	if s.floor > before+1 {
		t.Errorf("the floor rose from %.1f to %.1f during the sound: it is eating the event", before, s.floor)
	}
	if st := s.Feed(block(quiet, 500), at); !st.Cry {
		t.Error("after thirty seconds of cyclic crying the detector stopped seeing it")
	}
}

// **The defect the test found:** a continuous sound that never ends is not an
// event, it is the room having changed. Without this way out the floor would
// stay frozen forever and the detector lit for life.
func TestASteadyNoiseBecomesTheNewFloor(t *testing.T) {
	s, at := newTuned()

	// A fan: twenty-five dB above the floor, and it does not stop.
	st, at := run(s, block(-45, 500), at, DefaultMaxBurst+5*time.Second)
	if st.Cry || st.Bark {
		t.Errorf("a continuous noise was recognised: cry=%v bark=%v", st.Cry, st.Bark)
	}
	if s.floor < -50 {
		t.Errorf("floor %.1f: it stayed frozen below the new noise", s.floor)
	}

	// And from then on what sits above the fan is heard again.
	for i := 0; i < 2; i++ {
		_, at = run(s, block(-25, 900), at, 200*time.Millisecond)
		_, at = run(s, block(-45, 500), at, 300*time.Millisecond)
	}
	if st := s.Feed(block(-45, 500), at); !st.Bark {
		t.Error("with the fan on, the dog cannot be heard any more")
	}
}

// The floor adapts to the room, which is the way to have nothing to measure by
// hand: the same relative threshold holds in a silent house and in one with the
// street below.
func TestTheFloorFollowsTheRoom(t *testing.T) {
	s := NewSound(soundRate)
	t0 := time.Date(2026, 8, 24, 23, 0, 0, 0, time.UTC)

	_, at := run(s, block(-80, 500), t0, 2*time.Minute)
	if math.Abs(s.floor-(-80)) > 1.5 {
		t.Errorf("floor %.1f in a room at -80 dBFS", s.floor)
	}

	// The room gets noisier: after a few time constants the floor gets there,
	// and what used to be an event goes back to being the floor.
	_, at = run(s, block(-55, 500), at, 4*time.Minute)
	if math.Abs(s.floor-(-55)) > 2 {
		t.Errorf("floor %.1f after four minutes at -55 dBFS", s.floor)
	}
	_ = at
}

// An episode lasts beyond the last burst: whoever reads the status every three
// seconds has to be able to see it, and a bark lasts half a second.
func TestAnEpisodeOutlastsTheSound(t *testing.T) {
	s, at := newTuned()
	for i := 0; i < 2; i++ {
		_, at = run(s, block(loud, 900), at, 200*time.Millisecond)
		_, at = run(s, block(quiet, 500), at, 300*time.Millisecond)
	}

	if st := s.Feed(block(quiet, 500), at.Add(10*time.Second)); !st.Bark {
		t.Error("the episode ended ten seconds later: nobody would see it")
	}
	if st := s.Feed(block(quiet, 500), at.Add(DefaultSoundSettle+time.Second)); st.Bark {
		t.Error("the episode never ends")
	}
}

// **The defect found by the first live run.** The first blocks arrive before
// the microphone delivers anything and are worth digital silence: taken as the
// floor, everything that follows looks fifty dB above the room, and the
// detector stays blind until MaxBurst notices. Measured: thirty seconds with
// the floor at -90 and the room at -41.
func TestDigitalSilenceIsNotAFloor(t *testing.T) {
	s := NewSound(soundRate)
	t0 := time.Date(2026, 8, 24, 23, 0, 0, 0, time.UTC)

	// half a second of nothing, as when the microphone opens
	_, at := run(s, block(SilenceFloorDBFS, 0), t0, 500*time.Millisecond)
	if s.haveFloor {
		t.Error("the floor initialised itself on digital silence")
	}

	// then the room arrives, and the floor takes it at once
	st, at := run(s, block(-41, 500), at, 2*time.Second)
	if math.Abs(st.Floor-(-41)) > 1 {
		t.Errorf("floor %.1f after two seconds of a room at -41: it should have started there", st.Floor)
	}
	if st.Cry || st.Bark {
		t.Errorf("the normal room was recognised as an event: cry=%v bark=%v", st.Cry, st.Bark)
	}
	_ = at
}
