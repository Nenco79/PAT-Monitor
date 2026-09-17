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

	// Whoever really does fall silent comes back: it is not a punishment, it is
	// a pause.
	if _, ok := tb.acquire(1, t2.Add(talkIdle+time.Second)); !ok {
		t.Error("the floor does not come back even after falling silent")
	}

	// And another viewer does not pay for them.
	tb.release(1, "test")
	if _, ok := tb.acquire(2, t2.Add(2*talkIdle)); !ok {
		t.Error("a second viewer was kept out by the first one's exhaustion")
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
