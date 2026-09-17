package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The ordinary case, and the one that must not be broken by the deadline: the
// parts finish, and what comes back is their answer and not the deadline's.
func TestAShutdownThatFinishesIsNotCutShort(t *testing.T) {
	quitting := make(chan struct{})
	close(quitting)
	want := errors.New("the capture failed")

	got := waitOrLeave(quietLog(), func() error { return want }, quitting, time.Minute,
		func() error { return errors.New("the deadline fired on a shutdown that finished") })

	if !errors.Is(got, want) {
		t.Errorf("got %v, wanted the group's own error", got)
	}
}

// **The fault this exists for.** A part that never comes back used to hold the
// process for ever: measured, twenty-four minutes with the camera lit, the port
// already released and `shutdown complete` never written. The remedy is not to
// unblock it — nothing here can — but to stop waiting for it.
//
// **The defect was put back to watch this fail**: with `return <-done` in place
// of the two selects, this reports `waitOrLeave never came back: the deadline
// did not fire` after its own five seconds. The wait lives in a goroutine here
// for exactly that reason — a test that asserted the deadline by blocking on it
// would, when the deadline is gone, hang instead of failing, and a hung test
// says nothing about what it was watching.
func TestAShutdownThatHangsIsLeftToTheSystem(t *testing.T) {
	quitting := make(chan struct{})
	close(quitting)
	stuck := errors.New("left to the operating system")

	done := make(chan error, 1)
	go func() {
		done <- waitOrLeave(quietLog(), func() error { select {} }, quitting, 10*time.Millisecond,
			func() error { return stuck })
	}()

	select {
	case got := <-done:
		if !errors.Is(got, stuck) {
			t.Errorf("got %v, wanted the deadline's answer", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitOrLeave never came back: the deadline did not fire")
	}
}

// **The deadline starts at the request to stop, not at start-up.** A monitor
// that has been running for hours has not been late for anything, and a
// deadline that ran from the beginning would end it mid-night.
func TestTheDeadlineDoesNotRunBeforeTheQuit(t *testing.T) {
	quitting := make(chan struct{}) // never closed: nobody has asked to stop
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- waitOrLeave(quietLog(), func() error { <-release; return nil }, quitting,
			time.Millisecond, func() error { return errors.New("the deadline fired with no quit") })
	}()

	select {
	case got := <-done:
		t.Fatalf("came back with %v while the monitor was still running", got)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if got := <-done; got != nil {
		t.Errorf("got %v, wanted a clean finish", got)
	}
}

// The dump is the reason the deadline is worth having, so it has to contain
// something: a stack dump that is empty, or that holds only the goroutine
// asking for it, would leave the next reader exactly where this one was.
func TestTheDumpNamesTheGoroutines(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	go func() { <-blocked }()

	got := stacks()

	if len(got) < 100 {
		t.Fatalf("the dump is %d bytes: it cannot be describing a running program", len(got))
	}
	if want := "goroutine "; !strings.Contains(got, want) {
		t.Errorf("the dump does not carry %q, so it is not a stack dump", want)
	}
}
