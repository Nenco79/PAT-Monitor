package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/config"
	"patmonitor/internal/record"
	"patmonitor/internal/version"
)

// pruneInterval is the slow heartbeat of the pruning.
//
// **It exists because expiry by age has no event to set it off.** Pruning runs
// after every save, and with the quota alone that would be enough: space only
// grows by writing. But a clip can turn fourteen days old during a week in which
// nothing happens, and then nobody would look at it again.
//
// An hour is generous: precision here is no use to anybody, and reading the
// folder is the only thing that costs.
const pruneInterval = time.Hour

// clipsDir is the clips folder: `Video\PAT Monitor` of whoever uses the PC.
//
// **Clips are the user's things, not application data.** They used to live in
// `%APPDATA%\PAT Monitor\video`, next to the configuration and the log, and that
// is the right place for those two — nobody opens them by hand — but the wrong
// one for films: `%APPDATA%` is hidden, appears in no library, and whoever wants
// to watch a clip outside the browser has to know there is a folder Explorer
// does not show. The videos folder, instead, is where any program of this kind
// puts them, and it is also where whoever has read nothing looks for them.
//
// **The system says which folder it is**, not `%USERPROFILE%\Video`: that name
// changes with the language of Windows and the folder can be moved — to
// OneDrive, to another disk — and then composing it by hand would write to a
// place that is not the one the user sees. It is the usual rule: ask the system
// instead of deducing.
//
// An empty `videos` means "the system did not answer", and then the clips go
// back next to the configuration. **The fallback is the folder we used to
// use**: the log and the Tailscale node's identity already live down there. The
// parameter exists so that the choice can be tested both ways without a rigged
// machine — the question to the system, from here, always answers.
//
// **This composes a name and asserts nothing about it.** Whether that folder
// can be written to is a different question and is asked by `clipsFolder`, by
// writing.
func clipsDir(videos, cfgPath string) string {
	if videos == "" {
		return filepath.Join(filepath.Dir(cfgPath), "video")
	}
	// The product name is held by `version`, which is the only place it lives:
	// a second copy would diverge at the first rename, and this one is **a
	// folder with somebody's films inside it** — that is, the kind of name that
	// cannot be changed without leaving files behind.
	return filepath.Join(videos, version.Product)
}

// clipsFolder is the folder the clips really go into.
//
// **The videos folder is chosen by writing in it, not by being told about it.**
// `SHGetKnownFolderPath` answers with a path for a program that has no right to
// touch it — under MSIX the videos library is a capability, and a package that
// has not declared it gets exactly that: a folder that exists and a write that
// fails. Asked here, at start-up, the refusal still changes which folder is
// used; asked by the first clip, it is a recording lost at three in the
// morning. The proof itself is `provenDir`'s, and this is the one caller of it
// that has a second folder to fall back on.
//
// **The fallback is proved too, and it is still taken when the proof fails.**
// It is where the log and the node's identity already live, so a machine that
// cannot write there has troubles a clip is not going to reveal; what the
// second attempt buys is the **resolution** — the path Explorer has to be given
// — which is the part packaging changes. Refusing to record because a folder
// could not be proved would be the one answer worse than recording into a
// folder that turns out to be read-only: a baby monitor that will not watch
// because there is nowhere to put the films.
//
// `prove` is handed in rather than called by name for the reason that governs
// every function here that touches the disk: it interrogates the operating
// system, so inside this one it would run on the disk of whoever runs the
// tests — and both of its directions are branches that decide where somebody's
// recordings go.
func clipsFolder(videos, cfgPath string, prove func(string) (string, error), log *slog.Logger) string {
	if videos != "" {
		want := clipsDir(videos, cfgPath)
		real, err := prove(want)
		if err == nil {
			return real
		}
		log.Warn("the videos folder cannot be written to, the recordings will "+
			"stay next to the configuration", "dir", want, "error", err)
	}
	fallback := clipsDir("", cfgPath)
	real, err := prove(fallback)
	if err != nil {
		log.Warn("the recordings folder cannot be written to and is used anyway: "+
			"a clip will say so when it fails", "dir", fallback, "error", err)
		return fallback
	}
	return real
}

// oldClipsDir is where the clips lived before they moved to the videos folder.
//
// **Nothing is migrated and nothing is deleted**, but neither is anything left
// unsaid: whoever upgrades would find `/clips` empty with their recordings still
// on the disk, and from outside that is indistinguishable from lost clips. One
// line at startup, and only if there really is something down there.
//
// Moving them ourselves would be the kind choice and is not the right one: they
// are somebody's files, the copy can fail halfway, and a program that moves
// films between two folders at night without anybody asking is worse than a line
// in the log.
func oldClipsDir(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), "video")
}

// tellAboutOldClips says where the previous clips were left, if there are any.
func tellAboutOldClips(dir string, now *record.Store, log *slog.Logger) {
	if dir == now.Dir() {
		return // the fallback: the old clips are the current ones
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // it never existed, which is the normal case of a new installation
	}
	n := 0
	for _, v := range entries {
		if !v.IsDir() && strings.HasSuffix(v.Name(), ".mp4") {
			n++
		}
	}
	if n == 0 {
		return
	}
	log.Warn("older recordings stayed in the previous folder and are not listed any more",
		"clips", n, "old_dir", dir, "new_dir", now.Dir())
}

// clipStore builds the store from the configuration.
//
// **The conversion lives here and in one place only.** Megabytes and days are
// the units of whoever writes the configuration; bytes and durations are those
// of whoever applies the rule, and translating twice is the way to translate
// once badly.
//
// **The folder arrives already decided, and whoever starts up decides it.**
// Asked from in here, the question would reach the **tests** too:
// `TestTheRetentionKeysReachTheStore` wrote four megabytes of fake clips into
// the developer's real Videos folder, and passed green. A test that touches the
// disk of whoever runs it is not a matter of style — and this one put files
// there with the right names, that is, things the monitor would then have listed
// as recordings. **Looking at the folder** found it, not the code nor the tests.
// It used to take the videos folder and compose the rest; it takes the whole
// folder now, because choosing it stopped being a join and became a proof — see
// `clipsFolder`.
func clipStore(cfg config.Config, dir string, log *slog.Logger) *record.Store {
	return record.NewStore(dir, record.StoreConfig{
		MaxBytes: int64(cfg.ClipsMaxMB) << 20,
		MaxAge:   time.Duration(cfg.ClipsMaxDays) * 24 * time.Hour,
		Log:      log,
	})
}

// recordsClip says whether an alert deserves a recording.
//
// **Only the events**, that is, the things that happened in the room. A fault
// has no seconds to show — the seconds before the camera stopped do not explain
// why it stopped — and recording on every alert would fill the folder on exactly
// the night when something is wrong.
//
// It is a function rather than a condition inside the status loop so that it can
// be asked about **all** the codes that exist, including those still to come.
func recordsClip(a alerts.Alert) bool { return a.Level == alerts.Event }

// serveClips writes the clips the recorder hands over and prunes the folder.
//
// It runs on a goroutine of its own, and that is not a detail: writing a few
// megabytes cannot happen where the frames arrive, which is the thread nailed to
// the camera.
//
// **The first prune is at startup**, before any new clip: it is the only one
// that brings a folder left outside back within the caps — whoever lowers the
// quota and restarts expects it to do something, and hooking the prune only to
// writing it would do nothing until the first event.
func serveClips(ctx context.Context, store *record.Store, clips <-chan record.Clip, log *slog.Logger) {
	if _, err := store.Prune(time.Now()); err != nil {
		log.Debug("cannot prune the clips at startup", "error", err)
	}
	tick := time.NewTicker(pruneInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-clips:
			if !ok {
				return
			}
			if err := store.Save(c); err != nil {
				log.Warn("cannot save the clip", "code", c.Code, "error", err)
			}
		case <-tick.C:
			if _, err := store.Prune(time.Now()); err != nil {
				log.Debug("cannot prune the clips", "error", err)
			}
		}
	}
}
