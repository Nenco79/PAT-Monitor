package main

import (
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"patmonitor/internal/guard"
)

// exitShutdownStuck is the exit code of a monitor that was asked to stop and
// could not finish.
//
// It is its own code and not the 1 of an error, because the two say different
// things to whoever finds the process gone: 1 is a monitor that failed, this is
// a monitor that did everything it was asked and was still holding something
// the operating system had to take back. The chapter on releasing the encoder
// says why that is acceptable; this says it happened.
const exitShutdownStuck = 3

// shutdownLimit is how long the parts have to finish once stopping has been
// asked for.
//
// **The arithmetic is not a round number chosen for comfort**: the orderly
// shutdown gives the HTTP server five seconds of its own to close the viewers'
// connections, and everything else — the capture, the recorder, the tunnel —
// closes in well under a second on every clean shutdown in this repository's
// logs. Ten is that budget doubled, so a monitor that reaches it is not slow,
// it is stuck.
const shutdownLimit = 10 * time.Second

// waitOrLeave waits for the monitor's parts, and once stopping has been asked
// for it gives them a deadline.
//
// **It exists because a shutdown that never ends keeps the camera.** The
// capture can block inside Media Foundation while rebuilding the encoder: the
// quit then closes the tunnel, revokes the sessions and releases the port, and
// `g.Wait()` sits on a goroutine that is never coming back. The process stays
// alive with the webcam lit and no picture, ended by a kill and not by itself,
// `shutdown complete` never written.
//
// **And the cost is two monitors, not one.** The port has already been given
// up, so a second copy started to put things right comes up cleanly, finds the
// camera held by the first, and answers `0xC00D3704` at every retry.
//
// **What it does is stop waiting, not unblock anything.** The thread is inside
// a driver call and nothing in this program can reach it; what the operating
// system can do — and does, at once, on any process that exits — is take the
// camera, the handles and the memory back. It is the argument already written
// for the encoder released at the end, applied one floor up: a resource the
// system is about to reclaim is not worth waiting for.
//
// The deadline starts at the request to stop and not at start-up, which is the
// only shape that cannot cut a working monitor short: before that moment there
// is nothing to be late for.
func waitOrLeave(log *slog.Logger, wait func() error, quitting <-chan struct{}, limit time.Duration, stuck func() error) error {
	done := make(chan error, 1)
	guard.Go(log, "the wait on the monitor's parts", func() { done <- wait() })

	select {
	case err := <-done:
		return err
	case <-quitting:
	}
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		return stuck()
	}
}

// leaveItToTheSystem writes down what the process was doing and ends it.
//
// **The dump is the whole point of the line.** The night this was written the
// question — which of the two Media Foundation calls did not return — had no
// answer, because the binary that ships carries no profiler and `dlv attach`
// wants a build that is not stripped. `runtime.Stack` needs neither: it is the
// same information `/debug/pprof/goroutine?debug=2` gives, taken from inside,
// and it costs a few hundred kilobytes written once in the life of a process
// that is about to end anyway.
//
// It goes to the log at Error level and therefore to the file, which is the
// only witness left on a machine nobody is sitting in front of.
func leaveItToTheSystem(log *slog.Logger, limit time.Duration) error {
	log.Error("the shutdown did not finish: leaving the rest to the operating system",
		"waited", limit,
		"note", "the camera and the handles are released by the process ending",
		"stacks", stacks())
	os.Exit(exitShutdownStuck)
	return nil // not reached: os.Exit does not return
}

// dumpOnce keeps the stall's dump to one per process: the fault it describes
// does not clear on its own, so a second copy would be the same stacks again
// under a longer log.
var dumpOnce sync.Once

// dumpStacksOnce writes every goroutine's stack, the first time it is asked.
//
// It is called where a fault is **announced** and not where it is measured:
// the announcement happens once, has already passed the grace, and is the
// instant whoever reads the log afterwards goes looking for.
func dumpStacksOnce(log *slog.Logger, why string) {
	dumpOnce.Do(func() {
		log.Error("goroutine dump", "why", why, "stacks", stacks())
	})
}

// stacks returns every goroutine's stack.
//
// The buffer grows rather than being guessed at: `runtime.Stack` truncates
// without saying so, and a truncated dump is the shape of fault where the one
// goroutine that matters is the one missing from the end. Ninety-two goroutines
// measured on this program take about 60 kB, so the first doubling is normally
// the last.
func stacks() string {
	buf := make([]byte, 128*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}
