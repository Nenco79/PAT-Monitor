package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipelineWithTone builds a pipeline whose audio capture is the test tone: it
// has the same shape as WASAPI and asks for no device, so the cycle under test —
// open, close on our own decision, reopen — is exactly the real one.
func pipelineWithTone(t *testing.T) *Pipeline {
	t.Helper()
	return New(Config{
		AudioTestTone: true,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// waitFor waits for a condition to become true, instead of assuming an order
// between two goroutines.
//
// **An order is waited for, not assumed**: a test that takes for granted which
// of two goroutines goes first spins for its whole timeout on the machine where
// the other one does.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s did not happen in time", what)
}

// **Choosing a microphone closes the capture, and the close is not a fault.**
//
// It is the hinge of the whole feature: the choice is applied by **reopening**,
// so if `SetMicDevice` did not reach as far as the close, the select box would
// save a line in the file and move nothing — and a knob that moves nothing is
// worse than an absent knob. And the error it exits with has to be the one the
// supervisor recognises: with any other error, changing microphone would write
// "audio unavailable" in the log and wait out the backoff.
func TestChoosingAMicrophoneReopensTheCapture(t *testing.T) {
	p := pipelineWithTone(t)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan error, 1)
	go func() { done <- p.runAudio(ctx, Sinks{}) }()

	waitFor(t, "the microphone opening", func() bool { return p.Microphone().ID != "" })

	p.SetMicDevice("another endpoint")

	select {
	case err := <-done:
		if !errors.Is(err, errMicSwitch) {
			t.Fatalf("the capture exited with %v, not with the close we decided on", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the capture did not close: the choice does not reach the microphone")
	}

	// And the choice stays, so the reopen uses it: it is the other half of the
	// command, and without it the same microphone as before would be reopened.
	if got := p.micWantedID(); got != "another endpoint" {
		t.Errorf("after the choice the endpoint to open is %q", got)
	}
}

// **It reopens even when the ID is the one from before.**
//
// It is the only way whoever is watching has of saying "try again now": if the
// chosen microphone was unplugged, capture is coming from another one and the
// select box shows that one — repeating the choice is the natural gesture, and a
// branch that discarded it because "nothing changed" would make it mute.
func TestChoosingTheSameMicrophoneStillReopens(t *testing.T) {
	p := pipelineWithTone(t)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan error, 1)
	go func() { done <- p.runAudio(ctx, Sinks{}) }()
	waitFor(t, "the microphone opening", func() bool { return p.Microphone().ID != "" })

	p.SetMicDevice(p.micWantedID())

	select {
	case err := <-done:
		if !errors.Is(err, errMicSwitch) {
			t.Fatalf("the capture exited with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("repeating the choice reopens nothing")
	}
}

// **What opens is not what was asked for, and it is the opener that says so.**
//
// The capture falls back on the default when the chosen endpoint is no longer
// there — a USB stick gets pulled out — and without this distinction the page
// would show the name of a microphone that is capturing nothing: the wrong
// confidence in the worst place.
func TestTheOpenMicrophoneIsNotTheChosenOne(t *testing.T) {
	p := pipelineWithTone(t)
	p.SetMicDevice("an endpoint that does not exist")

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = p.runAudio(ctx, Sinks{}) }()

	waitFor(t, "the microphone opening", func() bool { return p.Microphone().ID != "" })

	if open := p.Microphone(); open.ID == p.micWantedID() {
		t.Errorf("the open endpoint (%q) and the chosen one are the same field", open.ID)
	}
	if p.Microphone().Name == "" {
		t.Error("the open endpoint has no name to show")
	}
}

// **The fallback is declared when it changes, not on every reopen.**
//
// Since a chosen endpoint that is absent falls back on the default instead of
// failing, "I opened another microphone" is a **state**: on a machine that does
// not obtain raw mode the recheck reopens every two minutes, and an unguarded
// line would come back ~720 times a night — that is, it would bury the rest of
// the log, which is the defect already paid for with "raw mode not obtained" and
// with the viewer refusals.
func TestTheFallbackIsAnnouncedOnceNotAtEveryReopen(t *testing.T) {
	var counter lineCounter
	p := New(Config{
		AudioTestTone: true,
		MicDeviceID:   "an endpoint that does not exist",
		Log:           slog.New(&counter),
	})

	// Two opens in a row on the same device: the second is the planned reopen,
	// and it has nothing new to say.
	for range 2 {
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = p.runAudio(ctx, Sinks{}); close(done) }()
		waitFor(t, "the microphone opening", func() bool { return p.Microphone().ID != "" })
		stop()
		<-done
	}

	if n := counter.count("the chosen microphone is not available"); n != 1 {
		t.Errorf("the fallback was announced %d times over two identical opens", n)
	}
}

// **Closing the capture is not enough to make it reopen.**
//
// If the open is failing, the supervisor waits up to thirty seconds between one
// attempt and the next, and there is no capture there to cancel: the choice
// would sit written and without effect for half a minute, while the page has
// already taken it as done. The wake-up is the other half of the command.
func TestChoosingWakesTheSupervisorFromItsBackoff(t *testing.T) {
	p := pipelineWithTone(t)

	select {
	case <-p.micWake:
		t.Fatal("the wake-up was already armed before any choice")
	default:
	}

	p.SetMicDevice("another endpoint")

	select {
	case <-p.micWake:
	default:
		t.Fatal("the choice does not wake whoever is waiting to try again")
	}

	// **A wake-up is a fact, not a queue**: two choices close together must
	// leave neither two reopens in the chamber nor whoever sends them waiting.
	p.SetMicDevice("one")
	p.SetMicDevice("two")
	<-p.micWake
	select {
	case <-p.micWake:
		t.Error("two choices left two wake-ups")
	default:
	}
}

// lineCounter counts the messages written to the log. It serves one question —
// how many times this line came out — and for that a twenty-line handler costs
// less than any library.
type lineCounter struct {
	mu   sync.Mutex
	seen []string
}

func (c *lineCounter) Enabled(context.Context, slog.Level) bool { return true }
func (c *lineCounter) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, r.Message)
	return nil
}
func (c *lineCounter) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *lineCounter) WithGroup(string) slog.Handler      { return c }

func (c *lineCounter) count(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.seen {
		if strings.HasPrefix(m, prefix) {
			n++
		}
	}
	return n
}

// **The recheck watches three fallbacks, not one.**
//
// The raw-mode one was there from the start; the **device** one came with the
// choice of microphone. Without it, on a machine that does obtain raw mode —
// that is, where everything is fine — the timer would not arm at all: whoever
// plugged the USB stick back in would stay on the lid microphone until the
// program restarts, with the page showing the choice and the audio coming from
// somewhere else.
//
// **The mute is the third, and it is the one that makes a command lie.** The
// panel offers the Windows Sound page under *microphone: muted in Windows*, and
// the gain is held at zero for the life of the open: with raw mode obtained and
// nothing to fall back to, no timer was armed, so unmuting changed nothing at
// all and the fault stayed red until the program restarted. **A remedy the
// interface offers has to reach the monitor.**
//
// The table is also the explanation: the answers, and why.
//
// **Verified to catch**: with `|| muted` removed from the predicate, the two
// muted rows fail.
func TestARecheckIsArmedForEveryFallback(t *testing.T) {
	const usb = "{0.0.1.00000000}.usb"
	const array = "{0.0.1.00000000}.array"

	cases := []struct {
		name         string
		raw, muted   bool
		wanted, open string
		tone         bool
		want         bool
	}{
		{"everything as asked: no timer", true, false, usb, usb, false, false},
		{"no raw mode: try again, as always", false, false, usb, usb, false, true},
		{"the microphone asked for was not there: try again for that too", true, false, usb, array, false, true},
		{"neither one works: one timer is enough", false, false, usb, array, false, true},
		{"the default role is not a fallback, whatever it opens", true, false, "", array, false, false},
		{"the test tone does not go through WASAPI: nothing to recheck", false, false, usb, array, true, false},
		{"muted on the good path: nothing else would ever look again", true, true, usb, usb, false, true},
		{"muted on the default role, which is not a fallback either", true, true, "", array, false, true},
	}
	for _, c := range cases {
		if got := micRecheckWanted(c.raw, c.muted, c.wanted, c.open, c.tone); got != c.want {
			t.Errorf("%s: recheck=%v, want %v", c.name, got, c.want)
		}
	}
}
