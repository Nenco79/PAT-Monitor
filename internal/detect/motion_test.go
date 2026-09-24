package detect

import (
	"math/rand"
	"testing"
	"time"
)

const (
	w = 160
	h = 90
)

var start = time.Date(2026, 8, 24, 23, 0, 0, 0, time.UTC)

// room is a still scene, with the noise a sensor always has.
func room(noise int, r *rand.Rand) []byte {
	f := make([]byte, w*h)
	for i := range f {
		v := min(max(90+r.Intn(noise+1)-noise/2, 0), 255)
		f[i] = byte(v)
	}
	return f
}

// square draws a light rectangle: it is the "somebody moving".
func square(f []byte, x, y, side int) []byte {
	out := append([]byte(nil), f...)
	for yy := y; yy < y+side && yy < h; yy++ {
		for xx := x; xx < x+side && xx < w; xx++ {
			out[yy*w+xx] = 220
		}
	}
	return out
}

// feed sends n identical frames, to let the filter settle.
func feed(m *Motion, f []byte, n int, t time.Time) MotionState {
	var st MotionState
	for i := range n {
		st = one(m, f, t.Add(time.Duration(i)*200*time.Millisecond))
	}
	return st
}

// The source for the tests that are not about size: the detector resets the
// comparison when the source size changes, so whoever is not testing that
// always declares a single one.
const (
	testW = 1280
	testH = 720
)

// one feeds the detector a frame, from an always identical source.
func one(m *Motion, f []byte, t time.Time) MotionState {
	return m.Feed(f, testW, testH, t)
}

// **The test that matters more than any other: a still room does not move.** A
// detector that gets this wrong wakes somebody every night, and after three
// nights nobody reads the alert any more.
func TestAStillRoomDoesNotMove(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	m := NewMotion()

	for i := range 50 {
		st := one(m, room(8, r), start.Add(time.Duration(i)*200*time.Millisecond))
		if st.Moving {
			t.Fatalf("motion on a still room at frame %d, ratio %.4f", i, st.Ratio)
		}
	}
}

func TestSomethingMovingIsSeen(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	base := room(8, r)
	m := NewMotion()
	feed(m, base, 10, start)

	// A 20x20 rectangle on 160x90 is 2.8% of the image: a baby in the cot shot
	// from close by, or a dog crossing the room.
	st := one(m, square(base, 40, 30, 20), start.Add(2*time.Second))
	if !st.Started {
		t.Errorf("motion not detected, ratio %.4f", st.Ratio)
	}
}

// **The false positive that would have arrived every night.** The automatic
// exposure changes gain and the whole image brightens: without removing the
// global shift, every breath of the camera becomes motion.
func TestAGlobalBrightnessShiftIsNotMotion(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	base := room(8, r)
	m := NewMotion()
	feed(m, base, 10, start)

	// Twenty levels at once across the whole image: far more than a real
	// adjustment makes, and more than twice the per-pixel threshold.
	brighter := make([]byte, len(base))
	for i, p := range base {
		brighter[i] = p + 20
	}
	for i := range 10 {
		if st := one(m, brighter, start.Add(time.Duration(2+i)*time.Second)); st.Moving {
			t.Fatalf("the camera breathing was mistaken for motion, ratio %.4f", st.Ratio)
		}
	}
}

// Continuous movement announces itself **once**, not on every frame.
func TestContinuousMotionIsAnnouncedOnce(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	base := room(8, r)
	m := NewMotion()
	feed(m, base, 10, start)

	announced := 0
	for i := range 40 {
		// the rectangle moves on every frame: real, continuous motion
		st := one(m, square(base, 20+i, 30, 20), start.Add(time.Duration(2000+i*200)*time.Millisecond))
		if st.Started {
			announced++
		}
	}
	if announced != 1 {
		t.Errorf("continuous motion announced %d times", announced)
	}
}

// And the other direction: once the room has been still long enough, moving
// again **is** news again.
func TestMovingAgainAfterStillnessIsNews(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	base := room(8, r)
	m := NewMotion()
	feed(m, base, 10, start)

	if !one(m, square(base, 40, 30, 20), start.Add(2*time.Second)).Started {
		t.Fatal("first movement not detected")
	}

	// The room goes still again. Before Settle has passed there is no rearming:
	// that is the period that stops intermittent movement from chiming ten
	// times a minute.
	still := start.Add(3 * time.Second)
	feed(m, base, 5, still)
	if st := one(m, base, still.Add(10*time.Second)); !st.Moving {
		t.Error("the episode ended before Settle: jerky movement would chime on every burst")
	}
	if st := one(m, base, still.Add(DefaultSettle+time.Second)); st.Moving {
		t.Fatal("the episode did not end after Settle")
	}

	if !one(m, square(base, 40, 30, 20), still.Add(DefaultSettle+2*time.Second)).Started {
		t.Error("movement after the quiet was not announced")
	}
}

// The pipeline rebuilds the capture, and then the frame changes size. Comparing
// it with the previous one would mean nothing.
func TestAResizedFrameResetsInsteadOfFiring(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	m := NewMotion()
	feed(m, room(8, r), 10, start)

	small := make([]byte, 80*45)
	for i := range small {
		small[i] = 200
	}
	if st := one(m, small, start.Add(3*time.Second)); st.Moving || st.Started {
		t.Error("a change of frame size was read as motion")
	}
}

// The number needed to tune the thresholds has to come out every time, and it
// is the one watched in the log to choose the real thresholds.
//
// **The test measures the margin too**, which is what says whether the default
// thresholds sit somewhere sensible: between the noise of a still room and real
// movement there has to be room, otherwise no value separates the two cases.
func TestTheRatioSeparatesNoiseFromMotion(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	base := room(30, r) // loud noise: ±15 levels, as in the dark
	m := NewMotion()
	feed(m, base, 10, start)

	noise := one(m, room(30, r), start.Add(2*time.Second)).Ratio
	if noise >= DefaultStartRatio {
		t.Errorf("noise alone is worth %.4f, already above the threshold of %.4f", noise, DefaultStartRatio)
	}

	motion := one(m, square(base, 40, 30, 20), start.Add(3*time.Second)).Ratio
	if motion <= noise {
		t.Errorf("motion %.4f cannot be told from noise %.4f", motion, noise)
	}
	if m.ratio != motion {
		t.Error("the stored ratio and the one handed out disagree")
	}
	t.Logf("noise %.4f · motion %.4f · threshold %.4f", noise, motion, DefaultStartRatio)
}

// **A change of source size is not motion.**
//
// The analysis frame is always 160x90, so its length never changes and a guard
// watching the length never fires. What changes is the provenance of each cell
// — from 1280x720 it is the average of 8x8 sensor pixels, from 640x352 of 4x4,
// and the aspect ratios are not even the same — and since it is not a uniform
// offset, subtracting the camera's breathing does not touch it.
//
// Measured: twelve events out of twenty fell between 0.22 and 0.59 s after a
// format change, with the ratio between 0.0246 and 0.0275, while real movement
// in the same log gave from 0.0115 to 0.7047. A false banner and a false chime
// every time the network made the size change, that is, at night.
func TestASizeChangeIsNotMotion(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	base := room(8, r)
	different := square(base, 40, 30, 20)

	// **Control**: from the same source that difference is motion. Without this
	// half the test would pass with the detector switched off.
	m := NewMotion()
	feed(m, base, 10, start)
	if !one(m, different, start.Add(2*time.Second)).Started {
		t.Fatal("the difference used is not worth a movement: this test proves nothing")
	}

	// The same frame, declared from a different size, is not.
	m2 := NewMotion()
	feed(m2, base, 10, start)
	if st := m2.Feed(different, 640, 352, start.Add(2*time.Second)); st.Started {
		t.Errorf("a size change announced motion (ratio %.4f)", st.Ratio)
	}

	// And it does not become motion on the next turn either: the reference
	// restarted from there.
	if st := m2.Feed(different, 640, 352, start.Add(2200*time.Millisecond)); st.Started {
		t.Errorf("motion appeared on the following frame (ratio %.4f)", st.Ratio)
	}

	// **But real movement after the change is still seen.** Resetting the
	// comparison must not mean switching the detector off.
	moved := square(different, 100, 30, 20)
	seen := false
	for i := 0; i < 3 && !seen; i++ {
		st := m2.Feed(moved, 640, 352, start.Add(time.Duration(3000+i*200)*time.Millisecond))
		seen = st.Started
	}
	if !seen {
		t.Error("after a size change the detector sees nothing at all")
	}
}

// An episode in progress does not end on a size change: the image changed, not
// the room.
func TestASizeChangeDoesNotEndAnEpisode(t *testing.T) {
	r := rand.New(rand.NewSource(13))
	base := room(8, r)

	m := NewMotion()
	feed(m, base, 10, start)
	if !one(m, square(base, 40, 30, 20), start.Add(2*time.Second)).Started {
		t.Fatal("the episode did not start")
	}
	if st := m.Feed(base, 640, 352, start.Add(2200*time.Millisecond)); !st.Moving {
		t.Error("the size change closed an episode in progress")
	}
}
