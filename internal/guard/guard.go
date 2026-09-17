// Package guard keeps a panic from taking the whole monitor down with it.
//
// A panic in any goroutine ends the process, and this program has nineteen of
// them: the recogniser, the recordings, the talk-back, the notification area.
// Every one of those is an accessory, and every one of them could switch the
// camera off — which is the invariant on separate lives (see "Audio and video
// must not be able to switch each other off") broken one floor below, where
// that rule cannot reach, because it is a property of the runtime and not of
// our code.
//
// **What this package does not do is invent a way of restarting anything.** The
// monitor already knows how to come back from a fault: Pipeline.Run restarts
// the video with a backoff, superviseAudio restarts the microphone, and both
// carry log lines saying so. What they did not know is that a panic **is** a
// fault. Turned into an error, it reaches machinery that has been running for
// months.
//
// The trade is declared, because it is the argument against this package and it
// is a real one: recovering hides bugs, and a program that swallows its own
// panics goes on running in a state nobody designed. Three things hold that
// down here. The stack is **always written**, at Error level, by whoever
// catches it and not by whoever receives the error — so a caller who decides to
// carry on cannot make the diagnosis disappear. Nothing is caught in the
// middle of the work: the catch sits at the top of a subsystem, where the state
// below it is about to be thrown away and rebuilt anyway. And what cannot be
// recovered from — a concurrent map write, a stack overflow — is not caught by
// anybody here and never will be: for that there is applog.Writer.CaptureCrashes,
// which is the other half and the reason the two were written together.
package guard

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"
)

// ErrPanic marks an error that was a panic, so that a caller can tell one from
// an ordinary fault of the same function.
//
// It is needed wherever the two get different answers. The tunnel files a
// panic under a step of its own — "an unexpected fault inside the monitor" —
// and that sentence reaches the page and the notification area: filed over an
// ordinary error it would be a wrong diagnosis shown to whoever is trying to
// put something right. Today those functions answer nil and the confusion is
// latent, which is exactly when it is cheap to remove.
var ErrPanic = errors.New("panic")

// Run calls fn and turns a panic inside it into an error.
//
// It is the shape both kinds of caller need, which is why there is one function
// and not two: whoever has to report the fault takes the error — for the
// capture, that is the supervisor that restarts it — and whoever has to outlive
// it ignores the error, having already had the stack written for them.
func Run(log *slog.Logger, what string, fn func() error) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Taken here and not at the call site: at the call site the stack is
		// the one after the unwinding, that is, the frames of whoever was
		// waiting rather than of whoever failed.
		stack := string(debug.Stack())
		// **The order of these is the whole readability of the line.** A text
		// handler quotes a value containing newlines, so the stack arrives as
		// one escaped run of three thousand characters: put first, it buries
		// the three things that answer the question — which subsystem, what
		// happened, and where — under a wall nobody reads at seven in the
		// morning. Last, the line opens with the answer and the wall is behind
		// it, for the rare time it is wanted.
		attrs := []any{"in", what, "value", fmt.Sprint(r)}
		if at := site(stack); at != "" {
			attrs = append(attrs, "at", at)
		}
		attrs = append(attrs, "stack", stack)
		logger(log).Error("panic caught", attrs...)
		err = fmt.Errorf("%w in %s: %v", ErrPanic, what, r)
	}()
	return fn()
}

// Go runs fn in a goroutine that cannot take the process down with it.
//
// It is for the goroutines nobody waits for, where there is no error for Run to
// hand back and the only thing to do with a panic is to write it down.
func Go(log *slog.Logger, what string, fn func()) {
	go func() {
		_ = Run(log, what, func() error {
			fn()
			return nil
		})
	}()
}

// site is the frame that panicked: the first one under the runtime's own.
//
// It is the answer to "where", and it is worth extracting because the stack it
// comes from is unreadable inside a log line. The shape read is the runtime's:
// a line at column zero naming the function, the next indented with its file
// and its line, and `panic(...)` sitting between the handler's frames and the
// guilty one.
//
//	patmonitor/internal/guard.Run.func1()
//		.../guard.go:52 +0x65
//	panic({0x...})
//		.../panic.go:860 +0x13a
//	patmonitor/internal/pipeline.(*Pipeline).runVideo()   <- this one
//		.../pipeline.go:2210 +0x28
//
// **It refuses rather than guesses.** A stack whose shape it does not
// recognise gives an empty string and the attribute is left off: a plausible
// wrong frame would send whoever reads it to the wrong function, and the whole
// stack is on the same line for exactly that case.
func site(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "panic(") && !strings.HasPrefix(l, "runtime.gopanic(") {
			continue
		}
		// The panicking frame is the function two lines down: the one under
		// `panic(` is its own file.
		if i+3 >= len(lines) {
			return ""
		}
		fn, where := lines[i+2], strings.TrimSpace(lines[i+3])
		if fn == "" || !strings.Contains(where, ":") {
			return ""
		}
		// **The arguments are cut at the last bracket, not the first.** A frame
		// of a method reads `pkg.(*Pipeline).simulatePanic(0x25…, {0x7f…}, 0x64?)`
		// — taken from a real log — so cutting at the first would leave
		// `pkg.` and throw the name away, and trimming a bare "()" leaves the
		// whole run of hexadecimal in a line meant to be read at a glance.
		if cut := strings.LastIndex(fn, "("); cut > 0 {
			fn = fn[:cut]
		}
		if cut := strings.LastIndex(where, " "); cut >= 0 {
			where = where[:cut] // drop the "+0x28" offset, which says nothing here
		}
		return fn + " " + where
	}
	return ""
}

// After is time.AfterFunc with the same protection as Go.
//
// **A timer's function runs in a goroutine of its own**, which is the same
// thing a `go` statement makes and does not look like one: a panic there ends
// the process, and no recover up the call chain that armed the timer can catch
// it, because by the time it fires that chain is long gone. It is a separate
// entry point rather than something Go could cover, and the sweep that reads
// the source refuses the bare form for exactly that reason.
//
// The timer is handed back, so that whoever has to stop it still can.
func After(log *slog.Logger, what string, d time.Duration, fn func()) *time.Timer {
	return time.AfterFunc(d, func() {
		_ = Run(log, what, func() error {
			fn()
			return nil
		})
	})
}

// logger keeps a missing logger from turning into a second panic inside the
// handler for the first one, where there would be nothing left to catch it.
func logger(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
