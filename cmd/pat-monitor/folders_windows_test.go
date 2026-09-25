//go:build windows

package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"patmonitor/internal/version"
)

// **The videos folder is chosen by writing in it, not by being told about it.**
//
// `SHGetKnownFolderPath` answers with a path for a program that has no right to
// touch it: packaged as an MSIX, the videos library is a capability, and a
// package that has not declared it gets exactly that pair — a folder that
// exists and a write that fails. Asked at start-up the refusal still changes
// which folder is used; asked by the first clip it is a recording lost at three
// in the morning.
//
// The proof is handed in, because a function that interrogates the operating
// system can only be run and never tested — and it would run on the disk of
// whoever runs the tests, which is a defect this file has already paid for
// once.
//
// **Verified to catch**: with the error from the first `prove` ignored, the
// second subtest keeps the videos folder and says so.
func TestTheClipsFolderIsChosenByWritingInIt(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	videos := filepath.Join(dir, "Video")
	quiet := slog.New(slog.DiscardHandler)

	t.Run("the videos folder answers", func(t *testing.T) {
		got := clipsFolder(videos, cfg, func(d string) (string, error) { return d, nil }, quiet)
		if want := filepath.Join(videos, version.Product); got != want {
			t.Errorf("folder %q instead of %q", got, want)
		}
	})

	t.Run("the videos folder refuses the write", func(t *testing.T) {
		asked := []string{}
		got := clipsFolder(videos, cfg, func(d string) (string, error) {
			asked = append(asked, d)
			if strings.HasPrefix(d, videos) {
				return "", errors.New("access is denied")
			}
			return d, nil
		}, quiet)
		if want := filepath.Join(filepath.Dir(cfg), "video"); got != want {
			t.Errorf("folder %q instead of the fallback %q: a path that exists is "+
				"not a path we may write in", got, want)
		}
		if len(asked) != 2 {
			t.Errorf("%d folders were tried: the fallback is proved too", len(asked))
		}
	})

	t.Run("nothing can be written anywhere", func(t *testing.T) {
		got := clipsFolder(videos, cfg, func(string) (string, error) {
			return "", errors.New("access is denied")
		}, quiet)
		// **It records anyway**, and that is the decision rather than an
		// omission: a baby monitor that will not watch because there is nowhere
		// to put the films is the one answer worse than a clip that fails.
		if want := filepath.Join(filepath.Dir(cfg), "video"); got != want {
			t.Errorf("folder %q: with both proofs refused the old folder is still used", got)
		}
	})

	// **And the folder that comes back is where Windows really wrote, not where
	// it was asked to.** It is the whole point of the second half of
	// `provenDir`: under MSIX a desktop program's `%APPDATA%` is redirected into
	// the package's store, so the two folder commands in the notification-area
	// panel would hand Explorer — which is not in the package — a real path with
	// nothing in it.
	t.Run("the answer is where it really went", func(t *testing.T) {
		elsewhere := filepath.Join(dir, "redirected")
		got := clipsFolder(videos, cfg, func(string) (string, error) { return elsewhere, nil }, quiet)
		if got != elsewhere {
			t.Errorf("folder %q: the path asked for was kept instead of the one "+
				"Windows answered with", got)
		}
	})
}

// provenDir really writes, and what it hands back names the same folder.
//
// **Equivalence and not equality is the assertion**, and the difference is the
// point: the resolution normalises — a short name expands, a case is settled, a
// redirection is followed — so a test comparing strings would be asserting that
// nothing was resolved. What has to hold is that a file written through the
// answer is the file the caller finds under the name it asked for.
//
// It stays inside the test's own folder, which is not pedantry here: this
// function's whole job is to create directories and write in them.
func TestTheFolderIsProvedByWritingAndTheProbeIsTakenBack(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "Video", version.Product)

	got, err := provenDir(want)
	if err != nil {
		t.Fatalf("a folder under the test's own directory could not be proved: %v", err)
	}

	// **The probe does not stay.** It is written into somebody's videos folder
	// at every start, and a file left behind there would be listed by nothing
	// and deleted by nobody.
	left, err := os.ReadDir(got)
	if err != nil {
		t.Fatalf("the folder that was proved cannot be read: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("the probe stayed behind: %v", left)
	}

	if err := os.WriteFile(filepath.Join(got, "clip.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatalf("the folder that was proved refuses a write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(want, "clip.mp4")); err != nil {
		t.Errorf("the answer %q names a different folder from %q: %v", got, want, err)
	}
}

// A folder that cannot be made is not a folder we may record into.
func TestAFolderThatCannotBeMadeIsRefused(t *testing.T) {
	dir := t.TempDir()
	// A file where the folder should go: `MkdirAll` refuses it, which is the
	// cheapest refusal that needs no permissions arranged.
	blocked := filepath.Join(dir, "Video")
	if err := os.WriteFile(blocked, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := provenDir(filepath.Join(blocked, version.Product)); err == nil {
		t.Errorf("a folder that cannot exist was answered with %q", got)
	}
}

// **The extended prefix is taken off, because the shell does not open it.**
//
// `GetFinalPathNameByHandle` answers with `\\?\C:\…`, which is a legal path and
// is not one `ShellExecute` does anything with — so the two folder commands in
// the panel would silently do nothing. It takes a string and not a handle for
// the reason the function's own comment gives: the UNC form needs a share, and
// a test that went looking for one would go looking on the disk of whoever runs
// it.
func TestTheExtendedPrefixIsTakenOff(t *testing.T) {
	for _, c := range [][2]string{
		{`\\?\C:\Users\x\Videos\PAT Monitor`, `C:\Users\x\Videos\PAT Monitor`},
		{`\\?\UNC\server\share\clips`, `\\server\share\clips`},
		{`C:\already\plain`, `C:\already\plain`},
		{``, ``},
	} {
		if got := stripExtendedPrefix(c[0]); got != c[1] {
			t.Errorf("%q became %q, wanted %q", c[0], got, c[1])
		}
	}
}

// **The log line names where the file really is only when that is elsewhere.**
// The redirected pair is the one measured inside the package on 25 September;
// the others are the two ways the system can answer without anything having
// moved, which must not add a second path to the line.
//
// **Verified to catch**: with onDisk comparing case-sensitively, the second
// case fails announcing a move that did not happen.
func TestTheLogLineNamesARedirectedFolderAndNothingElse(t *testing.T) {
	named := `C:\Users\u\AppData\Roaming\PAT Monitor\log`
	for _, c := range []struct {
		name, resolved string
		moved          bool
	}{
		{"redirected into the package",
			`C:\Users\u\AppData\Local\Packages\Nenco.PATMonitor_zehqae2e83nj8\LocalCache\Roaming\PAT Monitor\log`, true},
		{"the same folder, spelled with another case", `C:\Users\U\AppData\Roaming\PAT Monitor\log`, false},
		{"the same folder", named, false},
		{"the system could not say", "", false},
	} {
		got, moved := onDisk(named, c.resolved)
		if moved != c.moved {
			t.Errorf("%s: moved=%v, wanted %v", c.name, moved, c.moved)
		}
		if moved && got != c.resolved {
			t.Errorf("%s: names %q, wanted %q", c.name, got, c.resolved)
		}
	}
}

// **A folder named relatively, or through a short name, is not a moved one.**
// The review's case: with `-config config.yaml` the log folder is just `log`,
// while resolvedDir always answers absolute and in long names, so compared as
// they were the line announced a move that did not happen. longAbs spells the
// program's name the way the system answers. It runs on a folder this test
// creates, from inside its parent, and asks the system nothing else.
//
// **Verified to catch**: comparing the bare name instead of longAbs's, the
// relative case fails announcing a move.
func TestARelativeLogFolderIsNotAMovedOne(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)
	if got, moved := onDisk(longAbs("log"), resolvedDir("log")); moved {
		t.Errorf("a relative folder is reported as moved to %q", got)
	}
	// And the parent itself, which on this machine sits under a short-named
	// profile folder in %TEMP%: the 8.3 half of the same question.
	if got, moved := onDisk(longAbs(parent), resolvedDir(parent)); moved {
		t.Errorf("%q is reported as moved to %q", parent, got)
	}
}
