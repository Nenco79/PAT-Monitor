package ced

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

func s16(vs ...int16) []byte {
	b := make([]byte, len(vs)*2)
	for i, v := range vs {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

func ramp(from, n int) []byte {
	vs := make([]int16, n)
	for i := range vs {
		vs[i] = int16(from + i)
	}
	return s16(vs...)
}

// **The ring is circular, and from outside it has to look like a queue.** More
// samples than fit are written and then read back: what has to come out is the
// last window, oldest to newest. Getting the wrap wrong gives no error — it
// gives a window cut and stitched back to front, that is, a spectrogram of two
// moments in the wrong order.
func TestTheRingReadsBackAsTheNewestWindowInOrder(t *testing.T) {
	m := tiny(t, 0)
	s := NewStream(m, 0)
	defer s.Close()
	win := m.WindowSamples()

	// First less than a window: only what is there must come out.
	s.FeedS16(ramp(1, win/2), m.rate)
	got, secs, err := s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != win/2 {
		t.Fatalf("with half a window in, %d samples came out", len(got))
	}
	if secs != float64(win/2)/float64(m.rate) {
		t.Errorf("declared duration %v", secs)
	}
	// **The values are checked here, and not only in the wrapped case**: with
	// the ring full, head and head-filled are the same index modulo the length,
	// so a wrong start only shows while the ring is partial. With that defect
	// in place, a test looking only at the wrapped samples stays green and
	// reads thirty-four thousand zeros that were never written.
	for i, v := range got {
		if want := float32(i+1) / 32768; v != want {
			t.Fatalf("with half a window, sample %d is %v and not %v",
				i, v*32768, want*32768)
		}
	}

	// Then far more than a window, wrapping twice.
	s.FeedS16(ramp(win/2+1, 2*win+3), m.rate)
	got, _, err = s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != win {
		t.Fatalf("%d samples instead of a window of %d", len(got), win)
	}
	first := int(got[0]*32768 + 0.5)
	if want := win/2 + 2*win + 3 - win + 1; first != want {
		t.Errorf("the oldest is %d, wanted %d", first, want)
	}
	for i := 1; i < len(got); i++ {
		if int(got[i]*32768+0.5) != int(got[i-1]*32768+0.5)+1 {
			t.Fatalf("the ramp breaks between %d and %d: %v %v",
				i-1, i, got[i-1]*32768, got[i]*32768)
		}
	}
}

// **The copy is not waste.** The classification lasts half a second and the
// ring keeps being rewritten: if snapshot returned a view, halfway through the
// computation the window would hold audio that arrived later.
func TestASnapshotDoesNotChangeUnderneath(t *testing.T) {
	m := tiny(t, 0)
	s := NewStream(m, 0)
	defer s.Close()
	win := m.WindowSamples()
	s.FeedS16(ramp(1, win), m.rate)
	got, _, err := s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	before := append([]float32(nil), got...)
	s.FeedS16(ramp(9000, win), m.rate)
	for i := range before {
		if got[i] != before[i] {
			t.Fatalf("the window taken changed at sample %d", i)
		}
	}
}

// **A rate that changes throws the past away**, it does not mix it in: those
// samples describe the same frequencies with a different time scale, and
// stitched together they would give a spectrogram that never existed.
func TestChangingRateThrowsAwayThePast(t *testing.T) {
	m := tiny(t, 0)
	s := NewStream(m, 0)
	defer s.Close()
	s.FeedS16(ramp(1, 20), m.rate)
	if !s.Ready() {
		t.Fatal("with audio in it, it is not ready")
	}
	s.FeedS16(ramp(1, 20), m.rate*3/2)
	if s.Ready() {
		t.Error("at a rate the model does not accept it declares itself ready")
	}
	if _, _, err := s.snapshot(); err != ErrWrongRate {
		t.Errorf("snapshot at the wrong rate: %v", err)
	}
	if s.Ask() {
		t.Error("it accepted a question that cannot have an answer")
	}
	// And coming back to the right rate it restarts from zero, not from 20
	// samples.
	s.FeedS16(ramp(1, 5), m.rate)
	got, _, err := s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Errorf("after the return there are %d samples: the past was not thrown away", len(got))
	}
}

// A second question while the first is queued is dropped: **an answer is
// already on its way**, and queueing another would mean working twice on the
// same window.
func TestASecondQuestionWhileOneIsQueuedIsDropped(t *testing.T) {
	m := tiny(t, 0)
	s := &Stream{ // no goroutine: the queue never empties
		m:    m,
		ring: make([]float32, m.WindowSamples()),
		ask:  make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	s.FeedS16(ramp(1, 10), m.rate)
	if !s.Ask() {
		t.Fatal("the first question was dropped")
	}
	if s.Ask() {
		t.Error("the second was queued: the queue holds more than one question")
	}
}

// The round trip: audio is delivered, a question is asked, a verdict arrives
// with the model's classes.
func TestAskingProducesAVerdict(t *testing.T) {
	m := tiny(t, 1)
	s := NewStream(m, 0)
	defer s.Close()
	s.FeedS16(ramp(0, m.WindowSamples()), m.rate)
	if !s.Ask() {
		t.Fatal("the question was not taken")
	}
	var r *Result
	for i := 0; i < 200 && r == nil; i++ {
		time.Sleep(5 * time.Millisecond)
		r = s.Last()
	}
	if r == nil {
		t.Fatal("no verdict after a second")
	}
	if r.Err != nil {
		t.Fatalf("verdict with an error: %v", r.Err)
	}
	if len(r.Scores) != len(m.Labels()) {
		t.Errorf("%d scores for %d classes", len(r.Scores), len(m.Labels()))
	}
	if r.Seconds <= 0 || r.At.IsZero() {
		t.Errorf("the verdict says neither how much audio nor when: %+v", r)
	}
}

// **The wait sits where the work happens, not where the asking does**: that way
// it holds for every road a request can come from, and an episode that lasts
// does not keep a core busy.
func TestTheIntervalRatesTheWork(t *testing.T) {
	m := tiny(t, 0)
	s := NewStream(m, 300*time.Millisecond)
	defer s.Close()
	s.FeedS16(ramp(0, m.WindowSamples()), m.rate)

	if !s.Ask() {
		t.Fatal("the first question was dropped")
	}
	first := waitVerdict(t, s, nil)
	// The first one does not wait: it is the next that pays.
	if time.Since(first.At) > 200*time.Millisecond {
		t.Errorf("the first classification waited")
	}
	if !s.Ask() {
		t.Fatal("the second question was dropped")
	}
	second := waitVerdict(t, s, first)
	if d := second.At.Sub(first.At); d < 250*time.Millisecond {
		t.Errorf("the two classifications are %v apart, the interval is %v", d, s.interval)
	}
}

func waitVerdict(t *testing.T, s *Stream, after *Result) *Result {
	t.Helper()
	for i := 0; i < 400; i++ {
		if r := s.Last(); r != nil && r != after {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no verdict")
	return nil
}

// Closing twice must neither block nor panic: whoever closes does not know
// whether somebody else already has, and a close on an already closed channel
// panics.
//
// **It is closed from several goroutines at once too**, which is the case a
// select on the channel does not cover: with one in place of a sync.Once, two
// callers in sequence get through and two together do not.
func TestClosingTwiceIsHarmless(t *testing.T) {
	s := NewStream(tiny(t, 0), 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStream(tiny(t, 0), 0)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			if err := s2.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	close(gate)
	wg.Wait()
}
