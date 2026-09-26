package server

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"patmonitor/internal/guard"
)

// refusalRun writes a refusal that anybody can provoke **as an alert, not as an
// event**: the first of a run at Warn, the rest at Debug, and one line when the
// run has gone quiet saying how many there were and for how long.
//
// It is refuseViewer's shape for the refusals nobody is admitted after. Those
// two routes are public and are refused before the limiter is consulted —
// rightly, because a refused submission never reaches argon2 and has nothing to
// slow down — so without this every request was a line, and the caller decided
// how many. With each header allowed 64 KB, about five hundred padded posts
// from the Funnel are the 32 MB the log keeps, and they rotate it away whole —
// including the only lines that said who had been guessing passwords.
//
// A run ends on silence rather than on an admission, because there is no
// admission to wait for: whoever posts from a foreign page is never let in.
type refusalRun struct {
	log   *slog.Logger
	what  string        // the message of the first line; the summary reuses it
	quiet time.Duration // how long without refusals closes the run

	mu    sync.Mutex
	n     int
	since time.Time
	timer *time.Timer
}

// refusalQuiet is how long a run of refusals has to stay silent to be over.
//
// A minute separates a burst from the next one at human scale — whoever is
// hammering does it in seconds — and is short enough for the closing line to
// arrive while somebody is still looking.
const refusalQuiet = time.Minute

func newRefusalRun(log *slog.Logger, what string) *refusalRun {
	return &refusalRun{log: log, what: what, quiet: refusalQuiet}
}

// refuse counts one refusal and writes it at the level its place in the run
// deserves. args are the line's attributes; any value the caller wrote has to
// go through forLog first.
func (q *refusalRun) refuse(args ...any) {
	q.mu.Lock()
	first := q.n == 0
	if first {
		q.since = time.Now()
	}
	q.n++
	n := q.n
	if q.timer == nil {
		q.timer = guard.After(q.log, "closing a run of refusals", q.quiet, q.close)
	} else {
		q.timer.Reset(q.quiet)
	}
	q.mu.Unlock()

	if first {
		q.log.Warn(q.what, args...)
		return
	}
	q.log.Debug(q.what+" (again)", append(args, "in_a_row", n)...)
}

// close ends the run, if it is still quiet, and says how it went.
func (q *refusalRun) close() {
	q.mu.Lock()
	n, since := q.n, q.since
	q.n, q.timer = 0, nil
	q.mu.Unlock()

	// A run of one has already been said in full by its only line.
	if n > 1 {
		q.log.Info(q.what+": the run is over", "refused", n,
			"lasted", time.Since(since).Round(time.Second))
	}
}

// stop lets go of the timer, so that a closed server leaves nothing armed.
func (q *refusalRun) stop() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.timer != nil {
		q.timer.Stop()
	}
}

// maxLogValue is how much of a value the caller wrote reaches the log.
//
// A header is allowed 64 KB by the servers and slog quotes but does not cut, so
// one Origin could be one line of 64 KB. What makes a header worth logging —
// which site, which road — fits in far less; the rest is only what the caller
// chose to add.
const maxLogValue = 128

// forLog cuts a value the caller wrote to maxLogValue bytes, on a character
// boundary, and says how much was left out.
func forLog(s string) string {
	if len(s) <= maxLogValue {
		return s
	}
	cut := maxLogValue
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s…(+%d bytes)", s[:cut], len(s)-cut)
}
