package applog

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// broken is a destination that always fails, like os.Stderr in a process
// without a console.
type broken struct{ attempts int }

func (r *broken) Write(p []byte) (int, error) {
	r.attempts++
	return 0, errors.New("the handle is invalid")
}

// **With io.MultiWriter the file received nothing** because stderr, which came
// first, failed: the log died in the configuration without a console, that is,
// the only one where it is indispensable.
func TestABrokenSinkDoesNotSilenceTheOthers(t *testing.T) {
	var file bytes.Buffer
	stderr := &broken{}

	// The defect is demonstrated first, so that if anyone ever goes back to
	// io.MultiWriter this line says why they cannot.
	if _, err := io.MultiWriter(stderr, &file).Write([]byte("line\n")); err == nil {
		t.Fatal("io.MultiWriter did not report stderr's error")
	}
	if file.Len() != 0 {
		t.Fatal("io.MultiWriter wrote to the file: the defect is no longer the one described")
	}

	file.Reset()
	f := NewFanout(stderr, &file)
	n, err := f.Write([]byte("line\n"))
	if err != nil {
		t.Errorf("error with one destination alive: %v", err)
	}
	if n != 5 {
		t.Errorf("n=%d, wanted 5", n)
	}
	if got := file.String(); got != "line\n" {
		t.Errorf("the file received %q", got)
	}
}

// The order must not matter. It was the whole difference between a log that
// exists and one that does not, and it must never be again.
func TestTheOrderOfTheSinksDoesNotMatter(t *testing.T) {
	for _, c := range []struct {
		name  string
		first bool
	}{{"broken one first", true}, {"broken one second", false}} {
		t.Run(c.name, func(t *testing.T) {
			var file bytes.Buffer
			var f *Fanout
			if c.first {
				f = NewFanout(&broken{}, &file)
			} else {
				f = NewFanout(&file, &broken{})
			}
			if _, err := f.Write([]byte("line\n")); err != nil {
				t.Errorf("error: %v", err)
			}
			if file.String() != "line\n" {
				t.Errorf("the file received %q", file.String())
			}
		})
	}
}

// **A destination that fails is not retried.** It would fail on every line, and
// reporting that would generate as much noise as the log: it is the same rule
// that holds for the file.
func TestADeadSinkIsNotRetried(t *testing.T) {
	var file bytes.Buffer
	stderr := &broken{}
	f := NewFanout(stderr, &file)

	for range 10 {
		f.Write([]byte("line\n"))
	}
	if stderr.attempts != 1 {
		t.Errorf("%d attempts on a dead destination, wanted 1", stderr.attempts)
	}
	if strings.Count(file.String(), "line") != 10 {
		t.Errorf("the file lost lines: %q", file.String())
	}
}

// If they all die it says so, instead of letting anyone believe the log exists.
// Nobody can do anything about it — the only place to write it would be the log
// — but the return value is the only thing that tells "written" from "lost".
func TestWithNoSinkLeftItSaysSo(t *testing.T) {
	f := NewFanout(&broken{}, &broken{})
	if _, err := f.Write([]byte("line\n")); !errors.Is(err, ErrNoDestination) {
		t.Errorf("error %v, wanted ErrNoDestination", err)
	}
}

func TestNilSinksAreIgnored(t *testing.T) {
	var file bytes.Buffer
	f := NewFanout(nil, &file, nil)
	if _, err := f.Write([]byte("line\n")); err != nil {
		t.Errorf("error: %v", err)
	}
	if file.String() != "line\n" {
		t.Errorf("the file received %q", file.String())
	}
}
