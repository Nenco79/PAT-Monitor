// Package applog writes the log to a file, rotating it by size.
//
// It exists because the monitor is meant to live without a console — a
// notification-area application is built with `-H=windowsgui` and has nowhere
// left to write — and all of this program's diagnosis goes through the log.
// Without a file, a process that exits during the night leaves nothing but a
// camera that went dark and no explanation: the cause has to be inferred rather
// than read.
//
// The file sits next to the configuration, in %APPDATA%\PAT Monitor\log\,
// because that is where a user already knows to look and because it is writable
// without privileges.
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxBytes is how far a file may grow before it is set aside.
	//
	// Eight megabytes are about two days of log at debug level, which is the
	// level a fault is hunted at. At the normal level they are weeks.
	MaxBytes = 8 << 20

	// Keep is how many files are kept besides the one in use.
	//
	// The test that matters for this program lasts a night, and whoever reads
	// it does so the next morning: three spare files cover the time between the
	// fault and the question, without letting the folder grow forever.
	Keep = 3
)

// The three calls into the system sit behind variables so that a test can
// watch **the order** they are made in, which is the whole hazard: the handle
// has to be given up before the file behind it is closed, and taken again after
// the new one is open. Interrogating the real standard error in a test would
// also mean a test that behaves differently depending on how it was launched —
// `go test` has one, a monitor started from the notification area has not.
var (
	noStandardError   = standardErrorIsMissing
	takeStandardError = pointStandardErrorAt
	giveStandardError = releaseStandardError
)

// Writer is a log file that renames itself when it gets large.
//
// The rotation is numbered and not dated on purpose: numbered, the folder never
// grows past Keep+1 files, and whoever is after the latest fault always knows
// it is in the file without a number.
type Writer struct {
	mu   sync.Mutex
	path string
	f    *os.File
	n    int64
	// crashes says whether this file is also standing in for the process's
	// standard error. It has to be remembered, because the file behind it is
	// replaced at every rotation and the runtime keeps no reference to follow.
	crashes bool
}

// New opens the log inside dir, creating it if it is missing.
func New(dir, name string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("log folder: %w", err)
	}
	w := &Writer{path: filepath.Join(dir, name)}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// CaptureCrashes points the process's standard error at this file, so that
// what the runtime writes when it dies is written down.
//
// A panic's trace does not go through any writer of ours, and the errors that
// cannot be recovered from — a concurrent map write, a stack overflow — do not
// go through any recover either: both are written straight to the standard
// error, which a monitor built with `-H=windowsgui` has not got. That is the
// one fault this log exists for, and it was the one it could not record.
//
// It answers whether it took it. **It declines when this process already has a
// standard error of its own**, because then somebody is looking at it, or has
// redirected it on purpose: see crash_windows.go for the measurement of both
// cases. Whoever calls it says so in the log, since a protection that is not in
// force is invisible.
func (w *Writer) CaptureCrashes() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f == nil || !noStandardError() {
		return false
	}
	if err := takeStandardError(w.f); err != nil {
		return false
	}
	w.crashes = true
	return true
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening the log: %w", err)
	}
	// Start from the real size: opening in append mode, a restart must not
	// reset the count and put the rotation off forever.
	size := int64(0)
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	w.f, w.n = f, size
	// The rotation has just replaced the file the runtime was pointed at, and
	// it has no reference of its own to follow: without this, the traces would
	// be captured until the first rotation and no further — a protection that
	// stops working after two days of log, in silence.
	if w.crashes {
		if err := takeStandardError(f); err != nil {
			// **A protection that has lapsed says so, or it is worse than one
			// that was never there.** CaptureCrashes respects this same error
			// and declines; here it was discarded, so a failure left the
			// standard error pointing nowhere while the flag went on claiming
			// otherwise — and the start-up line, "crash traces: this file",
			// would have gone on asserting it for the life of the process.
			//
			// The line is written into the file directly, because this is the
			// one piece of news that cannot go through slog: what has happened
			// concerns the log itself, and the log is the only destination this
			// object is sure of.
			w.crashes = false
			w.note("the standard error could not be taken back after the " +
				"rotation: crash traces no longer reach this file")
		}
	}
	return nil
}

// note writes a line of the log's own, in the shape of the others.
//
// It is called with the lock held and writes straight to the file: going
// through Write would take the lock a second time, and going through slog
// would mean this package holding a logger in order to talk about itself.
func (w *Writer) note(text string) {
	if w.f == nil {
		return
	}
	n, _ := fmt.Fprintf(w.f, "time=%s level=ERROR msg=%s\n",
		time.Now().Format("2006-01-02T15:04:05.000Z07:00"), strconv.Quote(text))
	w.n += int64(n)
}

// Write satisfies io.Writer. A write error is not passed back to the caller as
// a fault of the program: the log is a witness, and a witness that falls must
// not stop what it was watching.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f == nil {
		return len(p), nil
	}
	if w.n+int64(len(p)) > MaxBytes {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.n += int64(n)
	if err != nil {
		// Stop trying: a full disk or a file removed underneath would otherwise
		// raise one error per log line, that is, as much noise as the log
		// itself.
		//
		// The standard error goes with it, and for the same reason as in the
		// rotation: this handle is about to stop existing, and whoever gets its
		// value next must not receive a stack trace.
		if w.crashes {
			giveStandardError()
			w.crashes = false
		}
		w.f.Close()
		w.f = nil
	}
	return len(p), nil
}

// rotate sets the current file aside and opens a new one. It is called with the
// lock held.
func (w *Writer) rotate() {
	// Given up **before** the close and not after: between the two the handle
	// no longer exists, and its value is handed out again to the next thing
	// this process opens. See releaseStandardError.
	if w.crashes {
		giveStandardError()
	}
	w.f.Close()
	w.f = nil

	// The spare files shift by one: .1 is the most recent. The walk goes
	// backwards, otherwise they would overwrite one another.
	for i := Keep; i >= 1; i-- {
		older := w.numbered(i + 1)
		current := w.numbered(i)
		if i == Keep {
			os.Remove(current)
			continue
		}
		os.Rename(current, older)
	}
	os.Rename(w.path, w.numbered(1))

	if err := w.open(); err != nil {
		w.f = nil
	}
}

func (w *Writer) numbered(i int) string {
	ext := filepath.Ext(w.path)
	return strings.TrimSuffix(w.path, ext) + fmt.Sprintf(".%d", i) + ext
}

// Close closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.crashes {
		giveStandardError()
		w.crashes = false
	}
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Path is the file in use, to show whoever asks "where are the logs?".
func (w *Writer) Path() string { return w.path }
