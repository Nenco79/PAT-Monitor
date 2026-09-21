package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// videosDir is the videos folder of whoever is using the PC, asked of Windows.
//
// **`%USERPROFILE%\Video` is not composed by hand**, and that is not pedantry:
// the name is translated — `Videos`, `Video`, `Vídeos`, `Filme` — and the folder
// can be moved, because Windows lets it be redirected and OneDrive does so by
// itself. Composing it by hand would write to a place that exists and that
// nobody looks at, which is the silent way of losing files.
// `SHGetKnownFolderPath` answers with the real one, redirections included.
//
// **`KF_FLAG_CREATE` because the answer has to be a folder that exists.** On a
// profile where that folder has been deleted, the query alone answers with a
// path and does not create it: the first clip would try to write inside it and
// `MkdirAll` would recreate it anyway, but without the properties Windows gives
// it — the icon, the collection type. If it cannot be created, it is right to
// know here and fall back, instead of finding out at the first clip.
//
// **A failure here stops nothing**: the clips go back next to the configuration,
// which is where they used to live, and it is declared. Refusing to watch over a
// child because there is nowhere to put the films would be absurd — it is the
// same rule as the log that cannot be written.
//
// The empty string is the answer "I do not know": `clipsFolder` reads it and
// decides. The value is not cached — it is asked once per start.
//
// **And an answer is not permission.** This folder can be handed back to a
// program that may not write in it, which is why nothing downstream trusts the
// path on its own: see `provenDir` below.
func videosDir(log *slog.Logger) string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Videos, windows.KF_FLAG_CREATE)
	if err != nil {
		log.Warn("the videos folder could not be asked of the system, "+
			"the recordings will stay next to the configuration", "error", err)
		return ""
	}
	return dir
}

// provenDir makes the folder, proves it can be written to, and says where it
// really is.
//
// **Asking Windows for a folder is not being able to write in it**, and that is
// the half `videosDir` above cannot cover: `SHGetKnownFolderPath` answers with
// the videos folder for a program that has no right to touch it, and the
// refusal then arrives at the first clip — that is, at three in the morning,
// into a log nobody is reading, about a recording that is gone. Packaged as an
// MSIX the case stops being hypothetical: the videos library is a capability,
// and a package that has not declared it gets exactly that pair, a path that
// exists and a write that fails. So the folder is chosen by **writing in it**,
// at start-up, where the answer can still change which folder is used.
//
// **And where it really is, is not always where it was asked for.** A packaged
// desktop program has its writes under `%APPDATA%` redirected into the
// package's own store, silently and by design: the program reads and writes
// happily, and Explorer — which is not in the package — opens the path it was
// given and shows an empty folder. That is the one place packaging changes what
// the code does rather than what is around it, and the two folder commands in
// the notification-area panel are what it breaks. `GetFinalPathNameByHandle`
// answers with the name of the file that was really opened, redirection
// included: it is the same rule as everywhere else here — **ask the system
// instead of deducing** — applied to a question one does not think of asking,
// "where did what I just wrote actually go?".
//
// The probe is a file and not the directory handle because writability is what
// is being proved, and a directory that opens is not a directory one can write
// in. It is unique per call: two copies of the monitor starting together would
// otherwise take each other's probe away, and the loser would demote a folder
// that is perfectly good.
//
// A resolution that fails is **not** a failure of this function: the folder has
// been proved writable, which is what the caller needs, and the unresolved path
// is what the program used before anybody thought about packaging. Returning an
// error there would send the clips into the fallback because a name could not
// be spelled.
func provenDir(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("the folder cannot be created: %w", err)
	}
	f, err := os.CreateTemp(dir, ".patmon-*.tmp")
	if err != nil {
		return "", fmt.Errorf("the folder cannot be written to: %w", err)
	}
	real, resolveErr := finalPath(windows.Handle(f.Fd()))
	name := f.Name()
	f.Close()
	os.Remove(name)
	if resolveErr != nil {
		return dir, nil
	}
	return filepath.Dir(real), nil
}

// resolvedDir says where an existing folder really is, and answers with the
// name it was given when it cannot say better.
//
// It is provenDir's other half, for the folders this program does not get to
// choose: the log's, which is already open and being written to, and the old
// clips folder, whose only job is to be compared with the current one — a
// comparison that would go wrong the moment one of the two is resolved and the
// other is not.
//
// **It creates nothing**, and that is the difference from provenDir rather than
// an omission: the old clips folder is declared only when it exists, and a
// function that made it would turn "there is nothing down there" into "there is
// an empty folder down there" on every installation that never had one.
//
// `FILE_FLAG_BACKUP_SEMANTICS` is what lets a directory be opened at all; the
// access asked for is none, because nothing is read through this handle.
func resolvedDir(dir string) string {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return dir
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return dir
	}
	defer windows.CloseHandle(h)

	real, err := finalPath(h)
	if err != nil {
		return dir
	}
	return real
}

// finalPath is the name Windows gives the thing behind an open handle.
//
// `VOLUME_NAME_DOS` asks for the answer in the form whoever opens Explorer can
// use, and it arrives prefixed with \\?\ — which is a legal path and is **not**
// one the shell opens: `ShellExecute` on it does nothing whatever. The prefix is
// taken off, with the UNC form taken off the way the documentation spells it,
// \\?\UNC\server\share standing for \\server\share.
//
// The buffer is grown from the answer rather than sized by guesswork: the call
// returns the length it wanted when it did not fit, which is the one number
// that cannot be wrong.
func finalPath(h windows.Handle) (string, error) {
	// **Both are zero, and they are written all the same.** `x/sys/windows`
	// does not carry them, and a bare 0 in that argument is a number nobody can
	// look up — while these two names say which of the four volume forms and
	// which of the two name forms were asked for, which is the whole content of
	// the call. The values are from fileapi.h.
	const (
		fileNameNormalized = 0x0
		volumeNameDOS      = 0x0
	)
	const flags = fileNameNormalized | volumeNameDOS
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
	if err != nil {
		return "", err
	}
	if int(n) >= len(buf) {
		buf = make([]uint16, n+1)
		if n, err = windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags); err != nil {
			return "", err
		}
		if int(n) >= len(buf) {
			return "", fmt.Errorf("the path does not fit in %d characters", len(buf))
		}
	}
	return stripExtendedPrefix(windows.UTF16ToString(buf[:n])), nil
}

// stripExtendedPrefix removes the \\?\ the resolution answers with.
//
// **It takes a string and not a handle** so that both forms can be tested
// without a file system arranged to produce them: the UNC one needs a share,
// which is exactly the kind of thing a test would go looking for on the disk of
// whoever runs it.
func stripExtendedPrefix(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	if rest, ok := strings.CutPrefix(p, `\\?\`); ok {
		return rest
	}
	return p
}
