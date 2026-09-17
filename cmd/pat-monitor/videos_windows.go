package main

import (
	"log/slog"

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
// The empty string is the answer "I do not know": `clipsDir` reads it and
// decides. The value is not cached — it is asked once per start, and by
// `clipStore`, which runs once.
func videosDir(log *slog.Logger) string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Videos, windows.KF_FLAG_CREATE)
	if err != nil {
		log.Warn("the videos folder could not be asked of the system, "+
			"the recordings will stay next to the configuration", "error", err)
		return ""
	}
	return dir
}
