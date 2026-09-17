package applog

import (
	"errors"
	"io"
	"sync"
)

// Fanout writes to several destinations, and **one that breaks does not silence
// the others**.
//
// It exists because io.MultiWriter writes in order and **returns on the first
// error**, without trying the destinations after it. With the log wired as
// io.MultiWriter(os.Stderr, file), and built without a console, os.Stderr is an
// invalid handle:
//
//	os.Stderr.Write            n=0  "The handle is invalid"
//	MultiWriter(stderr, file)  the file gets 0 bytes
//	MultiWriter(file, stderr)  the file gets all 17
//
// That is, **the log on file dies in exactly the configuration it was invented
// for**: the one without a console, where it is the only witness. A process
// that exits during the night leaves an empty log folder, and the diagnosis
// starts from nothing — which is the very loss that made the log go to a file
// in the first place.
//
// Hence the rule: **the log does its best towards each destination, and no
// destination speaks for the others.**
//
// **There is deliberately no way to ask how many are still alive.** There was
// one, `Alive`, whose doc said "whoever wants to declare at startup where the
// log will actually end up needs it" - and nothing in the program ever called
// it: `main` declares the destination by construction, from
// `Writer.CaptureCrashes`, which is a fact it holds already. Worse, the answer
// would have been wrong for the destination that matters. A destination is
// marked dead here only when its `Write` returns an error, and `applog.Writer`
// never returns one - by design, and argued where it is written: *a witness
// that falls must not stop what it was watching*. So the file counted as alive
// for as long as the process ran, including after the log had died.
//
// It is the shape the dead-code chapter names: the unused things a tool cannot
// see, because they are not code but promises - and `deadcode -test` could not
// have found this one either, the tests keeping it reachable. Whoever wants the
// question answered has to make `Writer` able to say it is no longer open
// first; until then there is nothing here to read.
type Fanout struct {
	mu   sync.Mutex
	dest []destination
}

type destination struct {
	w io.Writer
	// dead: after the first error it is not tried again.
	//
	// This is not only saving syscalls. A destination that fails on every line
	// fails **for every line**, and reporting that fault would generate as much
	// noise as the log itself — the same reason the file stops retrying after
	// its first error.
	dead bool
}

// NewFanout builds the multiple destination. Nils are dropped, so the caller
// does not have to assemble the list in pieces.
func NewFanout(w ...io.Writer) *Fanout {
	f := &Fanout{}
	for _, x := range w {
		if x != nil {
			f.dest = append(f.dest, destination{w: x})
		}
	}
	return f
}

// ErrNoDestination says no destination accepted the line.
var ErrNoDestination = errors.New("applog: no log destination available")

// Write delivers to every destination still alive.
//
// **It returns success if at least one accepted**, and that is not generosity:
// the caller is slog, which can do nothing with an error — it certainly cannot
// write it to the log. The only useful thing is that the line arrives wherever
// it can arrive.
func (f *Fanout) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	written := 0
	for i := range f.dest {
		if f.dest[i].dead {
			continue
		}
		if _, err := f.dest[i].w.Write(p); err != nil {
			f.dest[i].dead = true
			continue
		}
		written++
	}
	if written == 0 {
		return 0, ErrNoDestination
	}
	return len(p), nil
}
