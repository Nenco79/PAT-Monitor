package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/config"
	"patmonitor/internal/record"
	"patmonitor/internal/version"
)

// **A fault has no seconds to show.** The seconds before the camera stopped do
// not explain why it stopped, and recording on every alert would fill the folder
// on exactly the night when something is wrong.
//
// The question is asked about **all** the codes that exist, taken from the real
// table: a code added tomorrow falls under this test without anybody having to
// remember it.
func TestOnlyEventsAreRecorded(t *testing.T) {
	for _, c := range alerts.AllCodes() {
		a := alerts.Alert{Code: c, Level: alerts.LevelOf(c)}
		want := a.Level == alerts.Event
		if got := recordsClip(a); got != want {
			t.Errorf("code %q (%s) records=%v, expected %v", c, a.Level, got, want)
		}
	}
	// And the test has to have both cases genuinely in hand, otherwise a
	// function that always answers the same thing would pass too.
	if !recordsClip(alerts.Alert{Level: alerts.Event}) || recordsClip(alerts.Alert{Level: alerts.Fault}) {
		t.Fatal("recordsClip does not tell an event from a fault")
	}
}

// **The clips live in the videos folder of whoever uses the PC**, not among the
// application's data: they are films, and `%APPDATA%` is hidden.
//
// The system folder arrives as a parameter deliberately — the real question to
// Windows, from here, always answers — so that the fallback can be tested too,
// which is the half nobody ever runs.
func TestClipsLiveInTheVideosFolderOfTheUser(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")

	got := clipsDir(filepath.Join(dir, "Video"), cfg)
	if want := filepath.Join(dir, "Video", version.Product); got != want {
		t.Errorf("folder %q instead of %q", got, want)
	}

	// **The subfolder's name is the product's, and it is taken from there.** A
	// second copy of the string would diverge at the first rename, and this is a
	// folder with somebody's films inside it: changing it leaves files behind.
	if !strings.Contains(got, version.Product) {
		t.Errorf("the folder %q does not carry the product name", got)
	}

	// With no answer from the system the clips go back where they used to be: a
	// folder we know is writable, because the log and the Tailscale node's
	// identity already live down there.
	fallback := clipsDir("", cfg)
	if want := filepath.Join(dir, "video"); fallback != want {
		t.Errorf("fallback %q instead of %q", fallback, want)
	}
	if fallback != oldClipsDir(cfg) {
		t.Errorf("the fallback %q is not the previous folder %q: whoever upgrades "+
			"without a videos folder would see their clips vanish", fallback, oldClipsDir(cfg))
	}
}

// **The clips left in the previous folder are declared**, once.
//
// Whoever upgrades would find `/clips` empty with their recordings still on the
// disk, and from outside that is indistinguishable from lost clips. Nothing is
// migrated and nothing is deleted — they are somebody's files — but neither is
// anything left unsaid.
func TestTheClipsLeftBehindAreDeclaredOnceAndOnlyIfThereAreAny(t *testing.T) {
	check := func(t *testing.T, prepare func(dir string), want bool) {
		t.Helper()
		dir := t.TempDir()
		cfg := filepath.Join(dir, "config.yaml")
		old := oldClipsDir(cfg)
		prepare(old)

		var lines strings.Builder
		log := slog.New(slog.NewTextHandler(&lines, nil))
		store := record.NewStore(clipsDir(filepath.Join(dir, "Video"), cfg), record.StoreConfig{})
		tellAboutOldClips(old, store, log)

		if said := strings.Contains(lines.String(), "older recordings"); said != want {
			t.Errorf("declared=%v, expected %v (log: %q)", said, want, lines.String())
		}
	}

	t.Run("with old clips it says so", func(t *testing.T) {
		check(t, func(dir string) { fill(t, dir, 1, 8) }, true)
	})
	t.Run("an empty folder is not news", func(t *testing.T) {
		check(t, func(dir string) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}, false)
	})
	t.Run("with no folder nothing is said", func(t *testing.T) {
		check(t, func(string) {}, false)
	})

	// And with the fallback in force the previous folder **is** the current one:
	// saying it there would mean announcing as lost some clips that are being
	// served.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	fill(t, oldClipsDir(cfg), 1, 8)
	var lines strings.Builder
	store := record.NewStore(clipsDir("", cfg), record.StoreConfig{})
	tellAboutOldClips(oldClipsDir(cfg), store, slog.New(slog.NewTextHandler(&lines, nil)))
	if strings.Contains(lines.String(), "older recordings") {
		t.Error("with the fallback the previous clips are the current ones: they have not been left behind")
	}
}

// fill puts fake clips of the given date and size in KB into a folder.
func fill(t *testing.T, dir string, day, kb int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("clip-202609%02d-120000-motion.mp4", day)
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, kb*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// **The test `min_qp` never had.** That key was in the configuration, it was
// documented, and it never reached the pipeline: a knob that moves nothing is
// worse than a missing knob, because whoever turns it concludes the program does
// not respond and looks for the fault where it is not.
//
// The **unit** is checked here too, which is the silent way of getting it wrong:
// megabytes written as bytes would give a quota one millionth of the size, that
// is, a monitor that deletes every clip as soon as it writes it.
//
// **And that the store stays inside the test's folder.** That is not pedantry:
// since the clips moved to the videos folder, `clipStore` asked the system for
// the real one here too, and this test wrote **four megabytes of fake clips into
// the developer's Videos folder** — with the right names, that is, things the
// monitor would then have listed among the recordings. It passed green, and
// looking at the disk found it. `insideTheTest` is the guard, and it catches:
// putting the question to the system back inside `clipStore` makes all three
// tests below fail, declaring the real path.
func TestTheRetentionKeysReachTheStore(t *testing.T) {
	t.Run("megabytes are megabytes", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.Default()
		cfg.SetPath(filepath.Join(dir, "config.yaml"))
		cfg.ClipsMaxMB, cfg.ClipsMaxDays = 2, 0
		s := clipStore(cfg, dir, slog.New(slog.DiscardHandler))
		insideTheTest(t, s, dir)

		a := fill(t, s.Dir(), 1, 512)
		b := fill(t, s.Dir(), 2, 512) // one megabyte in all, under the quota
		if removed, err := s.Prune(time.Date(2026, 9, 3, 0, 0, 0, 0, time.Local)); err != nil || removed != 0 {
			t.Fatalf("deleted %d clips on a 2 MB quota with 1 MB in the folder (err %v)", removed, err)
		}
		if !exists(s.Dir(), a) || !exists(s.Dir(), b) {
			t.Error("a clip has vanished: the quota was read in bytes instead of megabytes")
		}

		fill(t, s.Dir(), 3, 2048) // now three megabytes: the cap bites
		if removed, _ := s.Prune(time.Date(2026, 9, 4, 0, 0, 0, 0, time.Local)); removed == 0 {
			t.Error("the quota was exceeded and nothing was deleted")
		}
	})

	t.Run("days are days", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.Default()
		cfg.SetPath(filepath.Join(dir, "config.yaml"))
		cfg.ClipsMaxMB, cfg.ClipsMaxDays = 0, 2
		s := clipStore(cfg, dir, slog.New(slog.DiscardHandler))
		insideTheTest(t, s, dir)

		old := fill(t, s.Dir(), 1, 10)
		recent := fill(t, s.Dir(), 9, 10)
		if _, err := s.Prune(time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)); err != nil {
			t.Fatal(err)
		}
		if exists(s.Dir(), old) {
			t.Error("a nine-day-old clip survived a two-day expiry")
		}
		if !exists(s.Dir(), recent) {
			t.Error("a one-day-old clip was deleted: the days were read as something else")
		}
	})

	t.Run("zero means no limit", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.Default()
		cfg.SetPath(filepath.Join(dir, "config.yaml"))
		cfg.ClipsMaxMB, cfg.ClipsMaxDays = 0, 0
		s := clipStore(cfg, dir, slog.New(slog.DiscardHandler))
		insideTheTest(t, s, dir)

		fill(t, s.Dir(), 1, 4096)
		if removed, _ := s.Prune(time.Date(2030, 1, 1, 0, 0, 0, 0, time.Local)); removed != 0 {
			t.Error("with the caps at zero something was deleted")
		}
	})
}

// Retention is on by default, like detection: a configuration without those keys
// must not let the folder grow for ever.
func TestTheRetentionIsOnByDefault(t *testing.T) {
	cfg := config.Default()
	if cfg.ClipsMaxMB <= 0 || cfg.ClipsMaxDays <= 0 {
		t.Errorf("defaults off: %d MB, %d days", cfg.ClipsMaxMB, cfg.ClipsMaxDays)
	}
}

// **The event's code becomes a file name, and the guardian is here.**
//
// `record` cannot enumerate the codes without importing whoever defines them,
// and from `alerts` the shape of the names is not visible: the question is asked
// at the point where both things are visible, which is the same place where it
// is decided who records.
//
// The defect it prevents is silent and permanent: a code out of shape — M7 will
// bring other sounds — produces a file that the listing, the opening, the
// deletion and the pruning **all skip**, that is, an invisible clip that takes
// up space and counts towards neither cap.
func TestEveryRecordableCodeCanBecomeAFileName(t *testing.T) {
	seen := 0
	for _, c := range alerts.AllCodes() {
		if !recordsClip(alerts.Alert{Code: c, Level: alerts.LevelOf(c)}) {
			continue
		}
		seen++
		if !record.UsableCode(string(c)) {
			t.Errorf("the code %q cannot become a clip name: the file would be "+
				"invisible to the listing, the pruning and the deletion", c)
		}
	}
	if seen == 0 {
		t.Fatal("no recordable code: the test looked at nothing")
	}
}

// insideTheTest demands that the store live inside the test's temporary folder.
//
// A test that writes outside its own home is not noisy: it is invisible until
// somebody looks at the disk, and in the meantime it leaves files with the name
// of a recording inside the developer's real folder.
func insideTheTest(t *testing.T, s *record.Store, dir string) {
	t.Helper()
	if !strings.HasPrefix(s.Dir(), dir) {
		t.Fatalf("the store is in %q, outside the test's folder %q: this test is "+
			"writing to the disk of whoever runs it", s.Dir(), dir)
	}
}
