package config

import (
	"errors"
	"fmt"
	"sync"
)

// Store is the configuration, once.
//
// **It exists because `config.Config` used to exist twice**, and the two copies
// failed in the two different ways one value in two places always does. `main`
// held one in a local that four closures on four goroutines read and two wrote,
// with nothing between them: a race. `internal/server` held another, taken by
// value at construction, from which every route that changes a setting rebuilt
// the whole struct and wrote it back: a stale base. They were one defect seen
// from two sides — and repairing either alone leaves the other, which is why
// both halves are here rather than a lock in one place and a notification in the
// other.
//
// The concrete loss was the node name Tailscale grants. `OnHostname` wrote it to
// the file and to `main`'s copy; the server's copy still held the name asked for,
// so the next setting saved from any page put the old one back — the public
// address changing in silence, which is the one outcome that callback exists to
// prevent.
//
// It is handed **by pointer** to everyone who needs it. A `Store` copied is two
// stores, which is the defect again with a mutex in it.
type Store struct {
	mu  sync.RWMutex
	cfg Config
	// over is what this run changes without it being the file's: see
	// Override.
	over func(*Config)
}

// NewStore takes the configuration as loaded. From here on it is the only copy
// that matters, and the value handed in must not be kept by the caller.
func NewStore(c Config) *Store { return &Store{cfg: c} }

// ErrSave says the configuration could not be written to disk.
//
// It separates the two ways an update fails, because the callers answer them
// differently: a change the caller itself refused — a password that cannot be
// exposed, a device that is not there — is a conflict and carries its own code,
// while a disk that refused is the monitor's fault and carries the generic one.
// Without the distinction every refusal would read as a failed write.
var ErrSave = errors.New("config: the configuration could not be written")

// Get returns the configuration as it stands.
//
// It is a copy, deliberately: a reader that held a pointer would be a second
// place the value can be written from, which is what this type exists to remove.
// Whoever needs two fields takes one Get — read twice, the two can come from two
// different configurations.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view(s.cfg)
}

// Override lays a change over what Get answers that is never written to the
// file.
//
// **A value given for one run is not a setting.** `-listen` overrides the
// address for this run, and it used to be set on the configuration itself:
// the first save of anything — a setting changed from the page, the tunnel
// recording its name — marshalled the whole struct, and every later start
// listened on the address a one-off flag had asked for, with nothing in the
// log saying where it came from. What Get answers is the file with this laid
// over it; what Update hands `change` and writes is the file alone.
func (s *Store) Override(over func(*Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.over = over
}

// view is c with the run's override laid over it.
func (s *Store) view(c Config) Config {
	if s.over != nil {
		s.over(&c)
	}
	return c
}

// Update applies a change and writes the result to disk, returning what was
// written.
//
// **The lock is held across the save, and that is the point rather than an
// oversight.** The defect being removed is a base read at one moment and written
// back at another; releasing the lock to touch the disk would put that window
// straight back, and two updates racing would each save a struct missing the
// other's field. Serialising them is the whole job. The file is a few hundred
// bytes, and the only readers made to wait are `Get`'s.
//
// It follows that `change` must do nothing but set fields on what it is handed:
// anything that reaches back into this store deadlocks, and anything slow is
// slow with the lock held.
//
// On a refusal — from `change` or from the disk — the configuration in memory is
// left exactly as it was, so a failed save cannot be observed as a change that
// took.
func (s *Store) Update(change func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cfg
	if err := change(&next); err != nil {
		return s.view(s.cfg), err
	}
	if err := next.Save(); err != nil {
		return s.view(s.cfg), fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.cfg = next
	return s.view(next), nil
}

// Set applies a change that cannot fail. It is Update for the ordinary case, so
// that a caller with nothing to refuse does not have to write `return nil`.
func (s *Store) Set(change func(*Config)) (Config, error) {
	return s.Update(func(c *Config) error {
		change(c)
		return nil
	})
}
