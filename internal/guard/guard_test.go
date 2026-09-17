package guard

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// quiet is a logger writing into a buffer, so that a test can read what the
// caught panic really wrote.
func quiet() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func TestAPanicComesBackAsAnError(t *testing.T) {
	log, out := quiet()

	// **One way of raising a deliberate panic, and this is the file's own.**
	// This line used to write into a nil map, which is legal Go that panics at
	// run time and was the shorter incantation — and it is also the one shape
	// every static analyser reports, for ever, as a nil dereference on a line
	// whose entire subject is that it panics. The other five deliberate panics
	// in this file are already written this way, twenty-two lines apart, and
	// `Run` cannot tell the two apart anyway: it formats whatever `recover`
	// returns. So the count decided it, five against one, and what goes with
	// the nil map is a permanent false positive that costs every future reader
	// the same minute.
	err := Run(log, "video capture", func() error { panic("boom") })

	if err == nil {
		t.Fatal("a panic came back as no error at all: the supervisor would " +
			"have read it as a capture that ended on its own")
	}
	if !strings.Contains(err.Error(), "video capture") {
		t.Errorf("the error does not name the subsystem: %v", err)
	}
	if !strings.Contains(out.String(), "panic caught") {
		t.Error("nothing was written: a caught panic with no line is a fault that never happened")
	}
}

// The stack is written by whoever catches, and that is the property that
// matters: the caller is allowed to throw the error away — the accessories do
// exactly that — and the diagnosis must not go with it.
func TestTheStackIsWrittenEvenIfTheCallerIgnoresTheError(t *testing.T) {
	log, out := quiet()

	_ = Run(log, "the tray", func() error { panic("no icon") })

	got := out.String()
	if !strings.Contains(got, "stack=") {
		t.Fatal("no stack in the line")
	}
	if !strings.Contains(got, "guard.TestTheStackIsWrittenEvenIfTheCallerIgnoresTheError") {
		t.Error("the stack does not carry the function that panicked")
	}
}

// The site is the first thing read, so it has to name the function that
// panicked and not one of the handler's own frames.
func TestTheSiteNamesWhoPanickedAndNotTheHandler(t *testing.T) {
	log, out := quiet()

	_ = Run(log, "x", func() error { panic("here") })

	got := out.String()
	if !strings.Contains(got, "at=") {
		t.Fatal("no site in the line: the stack alone is a wall nobody reads")
	}
	// It must not be the guard's own frame, which is what a reader would be
	// sent to by counting the lines wrongly.
	if strings.Contains(got, "at=patmonitor/internal/guard.Run") {
		t.Error("the site points at the catcher instead of the culprit")
	}
	if !strings.Contains(got, "guard_test.go:") {
		t.Errorf("the site does not point into this test: %s", got)
	}
}

// **A panic the runtime raises, which the rest of this file no longer covers.**
//
// Every other deliberate panic here is an explicit `panic(value)`, and that is
// the one traceback shape where the function that failed sits immediately under
// `panic(`. A runtime panic — an index out of range, a nil map write, a nil
// dereference — is what really happens at three in the morning, and its stack is
// not the same: `site` has to walk past the runtime's own frames to name the
// culprit. Without this, that arithmetic is exercised against a single shape.
//
// The index comes out of a function so that no static analyser can fold it. A
// test whose whole subject is a panic gets reported as a defect by every
// analyser that cannot tell "panics on purpose" from "panics by mistake", and
// this file paid that once already with a nil-map write flagged for ever on the
// line above.
func TestASiteIsNamedForARuntimePanicToo(t *testing.T) {
	log, out := quiet()

	err := Run(log, "the recogniser", func() error {
		var empty []int
		_ = empty[firstIndex()]
		return nil
	})

	if err == nil {
		t.Fatal("a runtime panic came back as no error: the supervisor would have " +
			"read it as work that finished on its own")
	}
	if !strings.Contains(err.Error(), "the recogniser") {
		t.Errorf("the error does not name the subsystem: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "at=") {
		t.Fatal("no site in the line: on a runtime panic the stack is all that is left")
	}
	if strings.Contains(got, "at=patmonitor/internal/guard.Run") {
		t.Error("the site points at the catcher instead of the culprit")
	}
	if !strings.Contains(got, "guard_test.go:") {
		t.Errorf("the site does not point into this test: %s", got)
	}
}

// firstIndex hides a zero from the analysers. See the test above.
func firstIndex() int { return 0 }

// The real stack of the live test, kept verbatim: a frame with arguments is
// what every frame below the top looks like, and the first version of site()
// handed the hexadecimal to the reader.
func TestTheSiteOfAFrameWithArgumentsIsJustTheName(t *testing.T) {
	stack := "goroutine 71 [running]:" + nl +
		"runtime/debug.Stack()" + nl +
		"\tD:/go/src/runtime/debug/stack.go:26 +0x5e" + nl +
		"patmonitor/internal/guard.Run.func1()" + nl +
		"\tC:/PAT-Monitor/internal/guard/guard.go:53 +0x7b" + nl +
		"panic({0x7ff63a72ffe0?, 0x7ff63aaec840?})" + nl +
		"\tD:/go/src/runtime/panic.go:860 +0x13a" + nl +
		"patmonitor/internal/pipeline.(*Pipeline).simulatePanic(0x2597315bc708?, {0x7ff63a9c70f4?, 0x5?}, 0x64?)" + nl +
		"\tC:/PAT-Monitor/internal/pipeline/pipeline.go:1992 +0x12" + nl

	want := "patmonitor/internal/pipeline.(*Pipeline).simulatePanic " +
		"C:/PAT-Monitor/internal/pipeline/pipeline.go:1992"
	if got := site(stack); got != want {
		t.Errorf("site was %q, wanted %q", got, want)
	}
}

// A shape it does not recognise must give nothing rather than a plausible
// wrong frame: the whole stack is on the same line for that case.
func TestAnUnknownStackIsRefusedRatherThanGuessed(t *testing.T) {
	for _, s := range []string{
		"",
		"goroutine 1 [running]:\nsomething.Else()\n\t/x.go:1 +0x1\n",
		"panic(0x1)\n",                          // cut short
		"panic(0x1)\n\t/panic.go:1\nlonely()\n", // no file line under the culprit
	} {
		if got := site(s); got != "" {
			t.Errorf("guessed %q from a stack it should have refused:\n%s", got, s)
		}
	}
}

// Run must not change what a function that does not panic does.
func TestWithoutAPanicNothingChanges(t *testing.T) {
	log, out := quiet()
	want := errors.New("an ordinary fault")

	if got := Run(log, "x", func() error { return want }); !errors.Is(got, want) {
		t.Errorf("the error was not handed back: %v", got)
	}
	if got := Run(log, "x", func() error { return nil }); got != nil {
		t.Errorf("nil became %v", got)
	}
	if out.Len() != 0 {
		t.Errorf("a function that did not panic wrote a line: %s", out)
	}
}

// A panic is told apart from an ordinary fault of the same function, because
// two callers here answer them differently: the tunnel files a panic under a
// step of its own, whose sentence reaches the page and the tray.
func TestOnlyAPanicCarriesTheMark(t *testing.T) {
	log, _ := quiet()

	if err := Run(log, "x", func() error { panic("boom") }); !errors.Is(err, ErrPanic) {
		t.Errorf("a panic did not carry the mark: %v", err)
	}
	ordinary := errors.New("an ordinary fault")
	if err := Run(log, "x", func() error { return ordinary }); errors.Is(err, ErrPanic) {
		t.Error("an ordinary fault was marked as a panic: whoever tells the two " +
			"apart would show the wrong diagnosis")
	}
}

// Go is the shape for the goroutines nobody waits for: what it must guarantee
// is that the process is still there afterwards.
//
// **What is waited for is the line, not the goroutine.** The first version of
// this test waited on a WaitGroup that fn itself released, and fn's own defers
// run while the stack is still unwinding — that is, before the recover that
// writes. It failed on a package that was working, which is the same trap
// already paid for in the talk-back test: an order is waited for, not assumed.
func TestAGoroutineThatPanicsDoesNotEndTheProcess(t *testing.T) {
	written := make(chan string, 4)
	log := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) {
		written <- string(p)
	}), nil))

	Go(log, "an accessory", func() { panic("gone") })

	select {
	case line := <-written:
		if !strings.Contains(line, "panic caught") || !strings.Contains(line, "an accessory") {
			t.Errorf("the line does not say what happened: %s", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine died without writing anything")
	}
}

// writerFunc hands each write to a function, so a test can wait for a line
// instead of sharing a buffer with another goroutine.
type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) {
	f(p)
	return len(p), nil
}

// A missing logger must not turn into a second panic inside the handler for
// the first, where there is nothing left to catch it.
func TestANilLoggerIsNotASecondPanic(t *testing.T) {
	if err := Run(nil, "x", func() error { panic("boom") }); err == nil {
		t.Error("no error came back")
	}
}

// nl is the newline a Go stack is split on.
const nl = "\n"
