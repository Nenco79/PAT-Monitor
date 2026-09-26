package rtc

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// The talk-back logic is tested without speakers: `Player` is an interface for
// that purpose. What stays outside — WASAPI — has its own proof, and it is not a
// test: it is the tone measured in loopback, because "written into the buffer"
// is not "it was heard".

type fakePlayer struct {
	mu      sync.Mutex
	samples int
	closed  bool
}

func (f *fakePlayer) Write(pcm []int16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples += len(pcm)
}

func (f *fakePlayer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakePlayer) state() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.samples, f.closed
}

func newTestTalkback(t *testing.T) (*Talkback, *[]*fakePlayer) {
	t.Helper()
	var opened []*fakePlayer
	var mu sync.Mutex
	tb := NewTalkback(func(int) (Player, error) {
		mu.Lock()
		defer mu.Unlock()
		p := &fakePlayer{}
		opened = append(opened, p)
		return p, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return tb, &opened
}

// **One person speaks at a time.** Two voices together in a room are not a
// conversation, and mixing them would need a mixer for a case that does not
// arise.
func TestOnlyOneViewerHoldsTheFloor(t *testing.T) {
	tb, opened := newTestTalkback(t)
	now := time.Now()

	if _, ok := tb.acquire(1, now); !ok {
		t.Fatal("the first one did not get the floor")
	}
	if _, ok := tb.acquire(2, now); ok {
		t.Error("the second one got the floor while the first was speaking")
	}
	// The first carries on without reopening anything: a second audio output
	// opened for the same speaker would be two voices overlapping with
	// themselves.
	if _, ok := tb.acquire(1, now.Add(time.Second)); !ok {
		t.Error("the first one lost the floor while speaking")
	}
	if len(*opened) != 1 {
		t.Errorf("%d audio outputs opened, want 1", len(*opened))
	}

	tb.release(1, "test")
	if _, ok := tb.acquire(2, now.Add(2*time.Second)); !ok {
		t.Error("the second one did not get the floor after the first stopped")
	}
}

// **The monitor counts the time, not the browser.** A page closing abruptly, a
// network dropping, a phone freezing: in all three cases the packets stop
// arriving and nobody tells us. Without this expiry the room would stay mute for
// ever.
func TestSilenceGivesTheFloorBack(t *testing.T) {
	tb, opened := newTestTalkback(t)
	now := time.Now()
	if _, ok := tb.acquire(1, now); !ok {
		t.Fatal("no floor")
	}

	tb.expire(now.Add(talkIdle / 2))
	if !tb.Active() {
		t.Error("the floor expired before its time")
	}
	tb.expire(now.Add(talkIdle + time.Second))
	if tb.Active() {
		t.Error("the floor did not expire after the silence")
	}
	if _, closed := (*opened)[0].state(); !closed {
		t.Error("the audio output was left open")
	}
}

// The absolute cap exists for the case that ruins the night: a page left open
// with the microphone on by mistake, which would keep the room mute until
// morning.
func TestATalkCannotLastForever(t *testing.T) {
	tb, _ := newTestTalkback(t)
	now := time.Now()
	if _, ok := tb.acquire(1, now); !ok {
		t.Fatal("no floor")
	}
	// Whoever really speaks refreshes their own time with every packet, so the
	// silence expiry never fires: that is exactly the case the other one is
	// needed for.
	//
	// **The loop has to cross the boundary, not stop on it.** Arriving exactly
	// at `talkMax`, where a `>` does not fire, the test accuses the code of a
	// defect that is its own. It is the usual question: before saying a
	// threshold is badly tuned, look at which side the equals sign is on.
	end := now.Add(talkMax + 5*time.Second)
	for t2 := now; t2.Before(end); t2 = t2.Add(time.Second) {
		tb.acquire(1, t2)
		tb.expire(t2)
	}
	if tb.Active() {
		t.Errorf("a turn went past %v", talkMax)
	}

	// **And it must not be taken back on the next packet.** The cap exists for
	// the microphone left on, that is for the case where the packets do not
	// stop: if the next packet were enough to come back in, the room would stay
	// mute all night in two-minute blocks. This is the half a first draft does
	// not have, and that this test found.
	t2 := end
	for range 200 { // four seconds of packets, one every twenty milliseconds
		t2 = t2.Add(20 * time.Millisecond)
		tb.acquire(1, t2)
		tb.expire(t2)
		if tb.Active() {
			t.Fatalf("floor taken back after %v of uninterrupted stream", t2.Sub(end))
		}
	}

	// Whoever really does fall silent comes back once the room has been heard:
	// it is not a punishment, it is a pause.
	back := end.Add(talkCooldown + time.Second)
	if _, ok := tb.acquire(1, back); !ok {
		t.Error("the floor does not come back even after falling silent")
	}

	// And another viewer does not pay for them beyond the room's pause.
	tb.release(1, "test")
	if _, ok := tb.acquire(2, back.Add(talkCooldown)); !ok {
		t.Error("a second viewer was kept out by the first one's exhaustion")
	}
}

// talkFor sends one viewer's packets every twenty milliseconds for d, running
// the expiry as the ticker would, and says how long of it the room was muted.
func talkFor(tb *Talkback, viewer int64, from time.Time, d time.Duration) (time.Time, time.Duration) {
	var muted time.Duration
	step := 20 * time.Millisecond
	t2 := from
	for ; t2.Before(from.Add(d)); t2 = t2.Add(step) {
		tb.acquire(viewer, t2)
		tb.expire(t2)
		if tb.Active() {
			muted += step
		}
	}
	return t2, muted
}

// **The cap is the room's, not the turn's.** Two pages handing the floor to
// each other, or one pausing for a breath before each two minutes, used to
// keep the room mute indefinitely: the cap was measured on the turn, and each
// hand-over or each pause started a new one.
//
// **The defect was put back and this test fails with it**: with the cap
// measured on the turn and no cooldown, the two pages keep the room mute for
// the whole ten minutes, and the page that pauses leaves it heard for thirteen
// seconds in nearly twelve minutes.
func TestTheRoomIsHeardBetweenStretchesOfTalk(t *testing.T) {
	t.Run("two pages taking turns", func(t *testing.T) {
		tb, _ := newTestTalkback(t)
		now := time.Now()
		var total time.Duration
		// Both send without a break for ten minutes: whoever does not hold the
		// floor is knocking for it with every packet.
		step := 20 * time.Millisecond
		for t2 := now; t2.Before(now.Add(10 * time.Minute)); t2 = t2.Add(step) {
			tb.acquire(1, t2)
			tb.acquire(2, t2)
			tb.expire(t2)
			if tb.Active() {
				total += step
			}
		}
		assertHeard(t, total, 10*time.Minute)
	})

	t.Run("one page pausing for breath", func(t *testing.T) {
		tb, _ := newTestTalkback(t)
		now := time.Now()
		var total time.Duration
		t2 := now
		for t2.Before(now.Add(10 * time.Minute)) {
			var muted time.Duration
			t2, muted = talkFor(tb, 1, t2, talkMax-5*time.Second)
			total += muted
			// Just over the idle limit, so the turn ends on silence rather
			// than on the cap.
			t2 = t2.Add(talkIdle + 100*time.Millisecond)
			tb.expire(t2)
		}
		assertHeard(t, total, t2.Sub(now))
	})
}

// **A microphone left on stays out after the room's pause, too.** The
// cooldown refuses every packet for half a minute, and those packets are the
// only evidence that the microphone is still on: if they are not counted, it
// comes out of the pause looking silent and takes the floor back.
//
// **The defect was put back and this test fails with it**: with the check for
// the spent viewer after the cooldown's refusal, the floor is taken back the
// first packet after the pause.
func TestAMicrophoneLeftOnDoesNotComeBackAfterThePause(t *testing.T) {
	tb, _ := newTestTalkback(t)
	now := time.Now()
	end, _ := talkFor(tb, 1, now, talkMax+time.Second)
	if tb.Active() {
		t.Fatal("the stretch was not cut")
	}
	if _, muted := talkFor(tb, 1, end, 3*talkCooldown); muted > 0 {
		t.Errorf("a microphone that never stopped took the floor back for %v", muted)
	}
}

// assertHeard asks that of a stretch the room was audible at least one
// cooldown per talkMax of talk.
func assertHeard(t *testing.T, muted, over time.Duration) {
	t.Helper()
	heard := over - muted
	stretches := over / (talkMax + talkCooldown)
	// A second of slack for the packets and ticks that fall on the edges.
	if want := time.Duration(stretches)*talkCooldown - time.Second; heard < want {
		t.Errorf("of %v the room was heard for %v, at least %v is wanted", over, heard.Round(time.Second), want)
	}
}

// **Half duplex: while somebody speaks, the room is not sent.** It is the only
// defence against the echo, and it lives in one line of WriteAudio: if it goes,
// whoever speaks hears themselves come back and the monitor howls.
func TestWhileSomeoneTalksTheRoomIsNotSent(t *testing.T) {
	tb, _ := newTestTalkback(t)
	h := &Hub{talk: tb, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if h.talk.Active() {
		t.Fatal("somebody is speaking without having taken the floor")
	}
	tb.acquire(1, time.Now())
	if !h.talk.Active() {
		t.Error("WriteAudio would go on sending the room while somebody speaks")
	}
	tb.release(1, "test")
	if h.talk.Active() {
		t.Error("the room stays mute after the speaking has stopped")
	}
}

// A hub without talk-back must not trip over itself: `Active` on a nil pointer
// is the ordinary case when there is no audio output.
func TestNoTalkbackIsNotAFault(t *testing.T) {
	var tb *Talkback
	if tb.Active() || tb.Speaker() != 0 {
		t.Error("an absent talk-back declares itself active")
	}
	tb.Receive(nil, 1) // must not panic
}

// If the audio output does not open, the floor is not granted: better nobody
// speaking than somebody who believes they are speaking while the room is
// silent.
func TestAFailedOutputDoesNotGrantTheFloor(t *testing.T) {
	tb := NewTalkback(func(int) (Player, error) {
		return nil, errors.New("no speakers")
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, ok := tb.acquire(1, time.Now()); ok {
		t.Error("floor granted with no audio output")
	}
	if tb.Active() {
		t.Error("the monitor declares it is listening for a voice it cannot play")
	}
}

// **The lock must not stay taken while the device is opened.**
//
// Over that mutex pass `Active`, which WriteAudio consults for every audio
// packet, and the status page the tray asks every second. Holding it across
// `open` hands a driver the right to stop everything that touches it: in the
// good case that is stuttering audio for the 313 ms of the open, in the bad case
// a monitor stopped with the camera on.
func TestOpeningTheOutputDoesNotBlockTheRest(t *testing.T) {
	unblock := make(chan struct{})
	// **The open announces that it has begun**, otherwise the test assumes an
	// order it never asked for: the first one's goroutine can start after the
	// body of the test has already reached the second `acquire`, and then the two
	// roles swap — it is the second that enters the open, where it stays,
	// because `unblock` closes only at the end. Observed as a ten-minute
	// timeout, with the blocked open called from the second one's line.
	inside := make(chan struct{}, 1)
	tb := NewTalkback(func(int) (Player, error) {
		select {
		case inside <- struct{}{}:
		default:
		}
		<-unblock // a device that takes an age to open
		return &fakePlayer{}, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	got := make(chan bool, 1)
	go func() {
		_, ok := tb.acquire(1, time.Now())
		got <- ok
	}()
	select {
	case <-inside:
	case <-time.After(2 * time.Second):
		t.Fatal("the first one did not even try to open the audio output")
	}

	// While the open is in progress, whoever asks for the state has to get
	// through.
	answered := make(chan bool, 1)
	go func() {
		tb.Active()
		tb.Speaker()
		answered <- true
	}()
	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		t.Fatal("the talk-back state is blocked by the audio output opening")
	}

	// And a second packet must not open a second output.
	if _, ok := tb.acquire(2, time.Now()); ok {
		t.Error("floor granted to a second viewer while the output was opening")
	}

	close(unblock)
	if !<-got {
		t.Error("the first one did not get the floor")
	}
}

// **An output that does not open is not asked for fifty times a second.**
//
// Whoever speaks sends a packet every twenty milliseconds, and asking the device
// for each one means, where there is no output — inside a Remote Desktop
// session, to name one that happens — fifty failed COM calls a second and fifty
// identical lines of log. Reproduced live, with HRESULT 0x80070057 on every
// packet.
func TestAnOutputThatRefusesIsNotAskedFiftyTimesASecond(t *testing.T) {
	var attempts int
	var mu sync.Mutex
	tb := NewTalkback(func(int) (Player, error) {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		return nil, errors.New("HRESULT 0x80070057")
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Two seconds of packets: a hundred attempts, if nothing stops.
	now := time.Now()
	for range 100 {
		tb.acquire(1, now)
		now = now.Add(20 * time.Millisecond)
	}
	mu.Lock()
	first := attempts
	mu.Unlock()
	if first != 1 {
		t.Errorf("%d opens attempted in two seconds, want 1", first)
	}

	// Once the wait has passed it is tried again: it is a momentary refusal, not
	// a sentence — whoever plugs the speakers in while the monitor runs has to
	// be able to speak.
	tb.acquire(1, now.Add(talkOpenRetry+time.Second))
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Errorf("%d opens after the wait, want 2", attempts)
	}
}
