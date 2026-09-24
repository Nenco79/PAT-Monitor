package record

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"patmonitor/internal/media"
)

const (
	// clipPrefix is the name of a prunable clip, keptPrefix that of one
	// somebody has asked to keep.
	//
	// **The difference between the two lives in the name and not in a state
	// beside it**, and that is not a shortcut: prune looks only at clipPrefix,
	// so a kept clip is not exempt through a condition somebody has to remember
	// — the pruning does not see it at all. And the filesystem stays the only
	// index, so there is no state that can be left orphaned by a deleted file.
	clipPrefix = "clip-"
	keptPrefix = "keep-"

	// clipSuffix is the extension, and nameLayout how the event's instant is
	// written: a format that sorts itself, so alphabetical order is
	// chronological order.
	clipSuffix = ".mp4"
	nameLayout = "20060102-150405"
)

// clipName is the exact shape of a clip's name.
//
// It is needed because **the name arrives from whoever is watching**, inside a
// URL, and with us it becomes a path on disk. It is the lesson already paid for
// with the language cookie, which ended up concatenated into a file name: it is
// validated against a strict shape, and then looked up in the real folder.
var clipName = regexp.MustCompile(`^(?:clip|keep)-\d{8}-\d{6}-[a-z][a-z0-9-]*\.mp4$`)

// codeName is the part of the name that carries the event's code.
//
// **It exists because whoever writes and whoever reads have to agree by
// construction.** Save composes the name by dropping Clip.Code in without
// looking at it: a code with a character outside this shape — an underscore, a
// capital — would produce a file that List, Open, Delete and the pruning **all
// skip**, that is, an invisible clip taking up space and counting in neither
// ceiling. No error anywhere, and the folder grows forever.
//
// The event codes we have today all fit; the moment a new one is added is
// exactly the moment nobody re-checks this file.
var codeName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ErrBadCode says an event code cannot become a file name.
var ErrBadCode = errors.New("record: not a usable event code")

// UsableCode says whether an event code can become a clip name.
//
// It is exported because the guard lives where the real codes are visible: they
// cannot be enumerated here without importing whoever defines them.
func UsableCode(code string) bool { return codeName.MatchString(code) }

// ErrBadName says a name is not a clip's.
var ErrBadName = errors.New("record: not a clip name")

// StoreConfig is the retention.
type StoreConfig struct {
	// MaxBytes is how much space the prunable clips may take. Zero: no ceiling.
	MaxBytes int64
	// MaxAge is how long a prunable clip may live. Zero: no expiry.
	MaxAge time.Duration
	Log    *slog.Logger
}

// Store is the folder of clips.
//
// **The two ceilings are two because they answer two different questions.**
// Space is the promise that matters — a monitor must not be able to fill the
// disk of whoever hosts it while nobody is watching — and age keeps
// six-month-old nights from being kept just because there is room. A count of
// files says nothing about either: twenty one-minute clips are twenty times
// twenty ten-second ones.
type Store struct {
	dir string
	cfg StoreConfig

	mu sync.RWMutex
}

// NewStore prepares the store. The folder is created on the first clip.
func NewStore(dir string, cfg StoreConfig) *Store {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Store{dir: dir, cfg: cfg}
}

// Dir is the folder of clips.
func (s *Store) Dir() string { return s.dir }

// Entry is one clip on disk.
//
// There is no state file beside the clips: the instant and the code are in the
// name, the size comes from Stat, the duration is read from the file's header,
// and "kept" is the prefix. Two lists of the same thing always diverge, and
// here the second list would be the one saying which files exist.
type Entry struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
	Code string    `json:"code"`
	// Millis is the duration, which travels in JSON as a number: composing
	// "15 s" is grammar, and the page is the one that knows the grammar.
	//
	// **Absent when it could not be read**, and not zero: a clip truncated by a
	// power cut has no duration, and declaring one of "0:00" would give the
	// plausible and wrong number this project fears more than an error. The "I
	// do not know" signal is carried by the absence, as it is for the unknown
	// commit in the version line.
	Millis int64 `json:"ms,omitempty"`
	Bytes  int64 `json:"bytes"`
	Kept   bool  `json:"kept"`
	// Audio says whether the clip has a sound track.
	//
	// **It is there so the viewer's browser does not get blamed.** A clip
	// recorded while the microphone was absent is video only, and from inside
	// the page that is identical to a player that cannot decode the audio:
	// without this field, whoever opens the recordings on an iPhone is told
	// their browser cannot manage, over a track that was never written.
	//
	// It is read from the file like the duration, and for the same reason: it
	// is the only place that knows, and a second list would diverge.
	Audio    bool          `json:"audio"`
	Duration time.Duration `json:"-"`
}

// Save writes a clip and prunes the folder.
func (s *Store) Save(c Clip) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !UsableCode(c.Code) {
		// Refusing is worse than saving, but far better than saving a file
		// nobody sees again: that means a folder growing with no ceiling, at
		// night, without a line.
		return fmt.Errorf("%w: %q", ErrBadCode, c.Code)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("record: creating %s: %w", s.dir, err)
	}
	// The name carries the **event's instant**, not the one the file was closed
	// at: whoever looks for a clip looks for when something happened.
	//
	// And it carries it in **local time, declared**: that name is read by a
	// person looking at a folder, and parseName reads it back with
	// ParseInLocation. Leaving it in the instant's own zone makes the round
	// trip come back shifted by a time zone, which is a plausible and wrong
	// number.
	//
	// **And the prefix is the clip's to decide**, because one is born held: the
	// one asked for by hand. It goes through here and not through a rename
	// straight afterwards — between Save and a Keep sits the pruning, which
	// runs at the end of this function and does not see the state somebody has
	// in mind. With a tight quota the clip just asked for would be the only
	// candidate to vanish.
	name := prefixFor(c.Keep) + c.At.Local().Format(nameLayout) + "-" + c.Code + clipSuffix
	path := filepath.Join(s.dir, name)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = WriteClip(f, c.Snapshot)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// A half-written file opens and does not read: worse than an absent
		// one, because it is discovered when it is needed.
		os.Remove(path)
		return err
	}

	s.cfg.Log.Info("clip saved", "file", name, "code", c.Code, "kept", c.Keep,
		"frames", len(c.Video), "audio", len(c.Audio),
		"span_ms", c.Span().Milliseconds(), "bytes", c.Bytes())
	s.prune(time.Now())
	return nil
}

// List lists the clips, most recent first.
func (s *Store) List() ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.list(true)
}

// Open opens a clip to serve it.
//
// The caller closes the file. The name is validated **and** looked up in the
// folder: a right shape does not prove the file exists.
func (s *Store) Open(name string) (*os.File, Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path, err := s.resolve(name)
	if err != nil {
		return nil, Entry{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Entry{}, err
	}
	e, err := entryOf(f, name)
	if err != nil {
		f.Close()
		return nil, Entry{}, err
	}
	// The duration is read by walking the file: whoever serves it wants it from
	// the start.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, Entry{}, err
	}
	return f, e, nil
}

// Delete deletes a clip.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.resolve(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.cfg.Log.Info("clip deleted", "file", name)
	return nil
}

// Keep exempts a clip from the retention by renaming it.
//
// **On Windows renaming an open file fails**, and while somebody is downloading
// it the file is open: the error goes back to whoever pressed, who can try
// again. Hiding it would give a lock that sometimes does not close.
//
// **It returns the new name**, and that is not a convenience: after the rename
// the old one no longer answers. Whoever was watching that clip had the old URL
// in their player, and the next range request got a 404 — that is, pressing the
// lock on a playing clip interrupted it with a generic error. Only this package
// knows how to compose the name, and recomposing it in the page would be the
// second copy of the prefix rule.
func (s *Store) Keep(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.resolve(name)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(name, keptPrefix) {
		return name, nil // already kept: pressing twice is not an error
	}
	renamed := prefixFor(true) + strings.TrimPrefix(name, clipPrefix)
	if err := os.Rename(path, filepath.Join(s.dir, renamed)); err != nil {
		return "", err
	}
	s.cfg.Log.Info("clip kept", "file", renamed)
	return renamed, nil
}

// Release puts a kept clip back under the retention, and returns the new name.
//
// **It is Keep's other half, and without it the lock is one-way.** It is needed
// now that clips asked for by hand are born held: without a way of releasing
// them, every press of the button adds a few megabytes no rule will ever touch
// again — that is, the promise about the disk of whoever hosts us stops holding
// for the one kind of clip the user produces on command.
//
// Everything that holds for Keep holds here: the new name goes back to whoever
// pressed because the old one no longer answers, and on Windows renaming an
// open file fails — whoever is downloading it holds it open, and the error is
// reported rather than hidden.
func (s *Store) Release(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.resolve(name)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(name, keptPrefix) {
		return name, nil // already prunable: pressing twice is not an error
	}
	renamed := prefixFor(false) + strings.TrimPrefix(name, keptPrefix)
	if err := os.Rename(path, filepath.Join(s.dir, renamed)); err != nil {
		return "", err
	}
	// **It is declared, and that is not symmetry with Keep.** From here on the
	// pruning can delete that clip, at night and with nobody asking: the line
	// saying when it stopped being protected is the only explanation left in
	// the morning.
	s.cfg.Log.Info("clip released", "file", renamed)
	return renamed, nil
}

// prefixFor chooses the name's prefix. It lives in a function because the rule
// "the lock is the name" is applied by three of them — whoever saves, whoever
// keeps and whoever releases — and three copies diverge.
func prefixFor(keep bool) string {
	if keep {
		return keptPrefix
	}
	return clipPrefix
}

// Prune deletes the clips that overrun either ceiling.
func (s *Store) Prune(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prune(now)
}

// prune does the work. It is called with the write lock held.
//
// **Deletion starts from the oldest**, and only among the prunable ones.
func (s *Store) prune(now time.Time) (int, error) {
	entries, err := s.list(false)
	if err != nil {
		return 0, err
	}
	// Oldest first: list hands them over most recent first.
	sort.Slice(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })

	var prunable, keptBytes int64
	for _, v := range entries {
		if v.Kept {
			keptBytes += v.Bytes
			continue
		}
		prunable += v.Bytes
	}

	// **The quota does not delete the last clip left.**
	//
	// Found by trying it: with a quota smaller than one clip — a megabyte, and
	// clips weigh five — the pruning runs right after every save and deletes
	// what has just been written. The result is a feature that does **nothing**,
	// and does not say so: the log announces a clip saved and a clip pruned, and
	// the folder stays empty.
	//
	// Between overrunning a tidiness quota and not recording at all, the right
	// thing is the first: whoever wrote that number wanted to contain the
	// footprint, not switch the recordings off.
	//
	// **Expiry by age, on the other hand, deletes everything**, and that is
	// deliberate: that one is not tidiness, it is a promise about how long a
	// child's recordings live. Keeping one past the deadline so as not to leave
	// the folder empty would break the promise for an aesthetic reason.
	newest := ""
	if len(entries) > 0 {
		for _, entrie := range slices.Backward(entries) {
			if !entrie.Kept {
				newest = entrie.Name
				break
			}
		}
	}

	var removed int
	var freed int64
	for _, v := range entries {
		if v.Kept {
			continue
		}
		tooOld := s.cfg.MaxAge > 0 && now.Sub(v.At) > s.cfg.MaxAge
		overQuota := s.cfg.MaxBytes > 0 && prunable > s.cfg.MaxBytes
		if overQuota && !tooOld && v.Name == newest {
			continue
		}
		if !tooOld && !overQuota {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, v.Name)); err != nil {
			s.cfg.Log.Debug("cannot remove an old clip", "file", v.Name, "error", err)
			continue
		}
		prunable -= v.Bytes
		freed += v.Bytes
		removed++
	}
	if removed > 0 {
		// **A feature that deletes files at night has to be declared**: without
		// this line, in the morning a clip that is not there is
		// indistinguishable from a clip that was never written.
		s.cfg.Log.Info("clips pruned", "removed", removed,
			"freed_kb", freed/1024, "left_kb", prunable/1024, "kept_kb", keptBytes/1024)
	}
	return removed, nil
}

// list reads the folder. The durations are read only when they are needed: they
// want every file opened, and the pruning does not need them.
func (s *Store) list(durations bool) ([]Entry, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no clip yet: that is not a fault
		}
		return nil, err
	}
	var out []Entry
	for _, v := range entries {
		if v.IsDir() || !clipName.MatchString(v.Name()) {
			continue
		}
		e, ok := parseName(v.Name())
		if !ok {
			continue
		}
		info, err := v.Info()
		if err != nil {
			continue
		}
		e.Bytes = info.Size()
		if durations {
			if f, err := os.Open(filepath.Join(s.dir, v.Name())); err == nil {
				if d, err := media.MP4Duration(f); err == nil {
					e.Duration, e.Millis = d, d.Milliseconds()
				} else {
					// It is listed anyway: a clip that does not declare how
					// long it lasts can still be watched and downloaded. But
					// the reason is written down, otherwise a folder of missing
					// durations has nothing to start from.
					s.cfg.Log.Debug("cannot read the clip duration",
						"file", v.Name(), "error", err)
				}
				// The sound track is checked on the same file, already open. An
				// error here is not a fault: "no audio" is declared, which is
				// the cautious direction — the page stays quiet instead of
				// accusing.
				if has, err := media.MP4HasAudio(f); err == nil {
					e.Audio = has
				} else {
					s.cfg.Log.Debug("cannot tell whether the clip has audio",
						"file", v.Name(), "error", err)
				}
				f.Close()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// resolve validates a name and returns the path, only if the file is there.
func (s *Store) resolve(name string) (string, error) {
	// Base first: that way a name holding separators cannot escape the folder,
	// and the expression judges what is left.
	if name != filepath.Base(name) || !clipName.MatchString(name) {
		return "", ErrBadName
	}
	path := filepath.Join(s.dir, name)
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", os.ErrNotExist
	}
	return path, nil
}

// parseName derives the instant, the code and "kept" from the name.
func parseName(name string) (Entry, bool) {
	kept := strings.HasPrefix(name, keptPrefix)
	rest := strings.TrimPrefix(strings.TrimPrefix(name, clipPrefix), keptPrefix)
	rest = strings.TrimSuffix(rest, clipSuffix)
	if len(rest) <= len(nameLayout) {
		return Entry{}, false
	}
	at, err := time.ParseInLocation(nameLayout, rest[:len(nameLayout)], time.Local)
	if err != nil {
		return Entry{}, false
	}
	return Entry{
		Name: name,
		At:   at,
		Code: strings.TrimPrefix(rest[len(nameLayout):], "-"),
		Kept: kept,
	}, true
}

func entryOf(f *os.File, name string) (Entry, error) {
	e, ok := parseName(name)
	if !ok {
		return Entry{}, ErrBadName
	}
	st, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	e.Bytes = st.Size()
	if d, err := media.MP4Duration(f); err == nil {
		e.Duration, e.Millis = d, d.Milliseconds()
	}
	return e, nil
}
