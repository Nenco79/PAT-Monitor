package server

import (
	"archive/zip"
	"errors"
	"io"
	"net/http"
	"os"

	"patmonitor/internal/record"
)

// The clips' routes.
//
// **The file sits under `/api/` and that is not an oversight.**
// `answersWithAPage` treats everything beginning with `/api/` as a request for
// data, so an expired session answers 401 rather than a redirect towards
// `/login` — which inside a `<video>` tag means nothing: the player would
// receive an HTML page in place of a film and report a generic failure, that is,
// the diagnosis would be lost. It is the same asymmetry already paid for on
// `/ws`.

// apiClips lists the clips.
func (s *Server) apiClips(w http.ResponseWriter, r *http.Request) {
	if s.opts.Clips == nil {
		// No store: the page shows its empty state rather than an error, so it
		// holds up even where there is none — in the tests, for instance.
		//
		// **But it says so, and that line cost its half hour.** Wiring the
		// routes, the `Store` was not passed to the server, and this branch made
		// the mistake indistinguishable from the normal case: two clips in the
		// folder and an empty list, with not a word anywhere. A fallback state
		// that resembles success is the same family as the GUID that does not
		// protest.
		s.clipsMissing.Do(func() {
			s.log.Error("the clip store is not configured: the recordings page will stay empty")
		})
		writeJSON(w, http.StatusOK, []record.Entry{})
		return
	}
	entries, err := s.opts.Clips.List()
	if err != nil {
		s.log.Warn("cannot list the clips", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrClipUnreadable)
		return
	}
	if entries == nil {
		entries = []record.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// apiClipFile serves a clip.
//
// **`http.ServeContent` and not a copy by hand**, because it gives range
// requests: those are what let `<video>` jump inside the clip rather than
// downloading all of it, and on a five-megabyte file through the funnel that is
// the difference between watching and waiting.
func (s *Server) apiClipFile(w http.ResponseWriter, r *http.Request) {
	clips := s.clipStore(w)
	if clips == nil {
		return
	}
	f, e, err := clips.Open(r.PathValue("name"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, ErrNoSuchClip)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "video/mp4")
	if r.URL.Query().Has("download") {
		// The file's name is composed by the server and not by whoever asks: it
		// has already been through validation, and copying it back from the
		// request would be the way to put back into circulation what had just
		// been checked.
		w.Header().Set("Content-Disposition", `attachment; filename="`+e.Name+`"`)
	}
	http.ServeContent(w, r, e.Name, e.At, f)
}

// maxZipClips is the most clips one archive carries. A selection is a day or
// two of events, and the ceiling is there so that a request cannot hold every
// clip on the disk open at once.
const maxZipClips = 200

// apiClipsZip hands several clips over as one file, for a selection.
//
// **One download and not one per clip**, because a page that starts several
// at once is asked by Chrome for permission to download "multiple files", and
// on an iPhone only the first arrives. The clips are already compressed, so
// they are stored rather than deflated: deflating H.264 costs the CPU and
// gains nothing.
//
// **Every clip is checked before a byte is written, and each is held only while
// it is written.** Once the archive has begun the status is 200 and cannot
// change, so a name that is not there — deleted by the cleanup since the page
// listed it — would come out as a truncated archive: checked first, it is a 404
// with nothing sent. They are not all kept open through the transfer, though,
// because on Windows a file somebody is reading cannot be renamed or deleted:
// twenty clips going to a phone over the Funnel would have refused every Keep,
// every Delete and the cleanup itself for as long as the download ran. One
// that vanishes in the moment between the check and its turn is left out and
// said in the log.
func (s *Server) apiClipsZip(w http.ResponseWriter, r *http.Request) {
	clips := s.clipStore(w)
	if clips == nil {
		return
	}
	names := r.URL.Query()["name"]
	if len(names) == 0 || len(names) > maxZipClips {
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
		return
	}
	var unique []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		f, _, err := clips.Open(n)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, ErrNoSuchClip)
			return
		}
		f.Close()
		unique = append(unique, n)
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="clips.zip"`)
	zw := zip.NewWriter(w)
	for _, n := range unique {
		if err := addToArchive(zw, clips, n); err != nil {
			if errIsMissingClip(err) {
				s.log.Warn("a clip left the archive: it went while it was being sent", "file", n)
				continue
			}
			s.log.Warn("the clips archive was cut short", "file", n, "error", err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		s.log.Warn("the clips archive was cut short", "error", err)
	}
}

// addToArchive writes one clip into the archive, holding it open only while
// it is written. The name inside is the store's, already validated, as for a
// single download.
func addToArchive(zw *zip.Writer, clips *record.Store, name string) error {
	f, e, err := clips.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	part, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Store, Modified: e.At})
	if err != nil {
		return err
	}
	_, err = io.Copy(part, f)
	return err
}

// apiClipKeep exempts a clip from the retention.
func (s *Server) apiClipKeep(w http.ResponseWriter, r *http.Request) {
	s.renameClip(w, r, (*record.Store).Keep)
}

// apiClipRelease puts a kept clip back under the retention.
//
// **It is the half that was missing**, and it is needed since clips asked for by
// hand are born kept: without it the lock was one-way and every press of the
// "Record" button left a few megabytes on the disk that no rule would ever touch
// again.
func (s *Server) apiClipRelease(w http.ResponseWriter, r *http.Request) {
	s.renameClip(w, r, (*record.Store).Release)
}

// renameClip is the lock's two directions, which differ only in which of the
// store's renames they ask for.
func (s *Server) renameClip(w http.ResponseWriter, r *http.Request,
	rename func(*record.Store, string) (string, error)) {
	clips := s.clipStore(w)
	if clips == nil {
		return
	}
	renamed, err := rename(clips, r.PathValue("name"))
	if err != nil {
		s.refuseClip(w, r.PathValue("name"), err)
		return
	}
	// **The new name goes back to whoever pressed.** After the rename the old
	// one no longer answers, and whoever was watching that clip has the old
	// address in the player: without this field the next range request receives
	// a 404 and the lock stops playback with a generic error.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": renamed})
}

// apiClipDelete deletes a clip.
func (s *Server) apiClipDelete(w http.ResponseWriter, r *http.Request) {
	clips := s.clipStore(w)
	if clips == nil {
		return
	}
	if err := clips.Delete(r.PathValue("name")); err != nil {
		s.refuseClip(w, r.PathValue("name"), err)
		return
	}
	writeOK(w)
}

// clipStore is the store of the clips, or nil with the refusal already
// written: to whoever asks for one clip, "there is no store" and "there is no
// such clip" are the same answer, and only the list says the first out loud.
func (s *Server) clipStore(w http.ResponseWriter) *record.Store {
	if s.opts.Clips == nil {
		writeJSONError(w, http.StatusNotFound, ErrNoSuchClip)
	}
	return s.opts.Clips
}

// refuseClip separates "it is not there" from "it could not be touched".
//
// **The second exists for Windows**, where renaming or deleting an open file
// does not work, and while somebody is downloading it the file is open. Whoever
// pressed has to know they can try again: a lock that sometimes does not lock
// without saying so is worse than no lock.
func (s *Server) refuseClip(w http.ResponseWriter, name string, err error) {
	if errIsMissingClip(err) {
		writeJSONError(w, http.StatusNotFound, ErrNoSuchClip)
		return
	}
	s.log.Warn("cannot touch the clip", "file", name, "error", err)
	writeJSONError(w, http.StatusConflict, ErrClipBusy)
}

// errIsMissingClip recognises the two ways a clip is not there: a name that is
// not a name, and a right name for a file that does not exist.
//
// It is recognised with `errors.Is` and not by comparing the message, which is
// what the codes exist for.
func errIsMissingClip(err error) bool {
	return errors.Is(err, record.ErrBadName) || errors.Is(err, os.ErrNotExist)
}

// apiRecord asks for a clip of now, without waiting for an event.
//
// **The pre-roll is already there**, so nothing new is recorded here: the ring
// is asked to deliver the seconds it holds and to gather the next ones, exactly
// as a movement in the room would make it. It is why the button could appear
// without adding a piece: the machinery is already running.
//
// Whoever records is `cmd/pat-monitor`, which owns the recorder: here it is
// asked for and reported, the same division as `EnableRemote` and
// `UseMicrophone`.
//
// **There are two refusals and both have to be said to whoever pressed**: in
// the first seconds after the start, or after a capture restart, the ring is
// empty because the first keyframe has not arrived, and there is nothing to
// save; and on a nearly full disk the clip would be recorded and then not
// written. A command that does nothing and does not say so is a knob that
// moves nothing, and whoever turns it concludes the program does not answer.
func (s *Server) apiRecord(w http.ResponseWriter, r *http.Request) {
	if s.opts.Record == nil {
		writeJSONError(w, http.StatusNotImplemented, ErrRecordUnavailable)
		return
	}
	if s.opts.Clips != nil && errors.Is(s.opts.Clips.Room(), record.ErrDiskFull) {
		writeJSONError(w, http.StatusInsufficientStorage, ErrDiskFull)
		return
	}
	if !s.opts.Record() {
		writeJSONError(w, http.StatusConflict, ErrNothingToRecord)
		return
	}
	// **The clip is not announced, the request is.** That it was written is said
	// by `clip saved`, which belongs to whoever writes it: saying it here too
	// would give two lines for one file, and the first would be a prediction.
	s.log.Info("clip requested by hand")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recording": true})
}
