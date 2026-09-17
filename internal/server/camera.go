package server

import (
	"encoding/json"
	"net/http"

	"patmonitor/internal/config"
)

// The camera's routes: which ones there are, and which one is used.
//
// They are the microphone's twins, and deliberately so — the same shape, the
// same order, the same refusals — because whoever comes to change one of the
// two should not have to work out which of the two ways of doing it applies.
// See microphone.go for the arguments, which hold unchanged: **the list does
// not travel with the status**, because enumerating costs a round through Media
// Foundation and the heartbeat asks every three seconds from every open page;
// what does live in the status is which camera is open and which one was
// chosen.
//
// **The one asymmetry is what an empty choice means.** For the microphone it is
// "follow the role Windows calls default", which is a thing the system decides
// and keeps — measured: `MediaDevice` has `GetDefaultAudioCaptureId` and no
// video twin. There is no such role for cameras, so here it is "the first
// usable one", which is a rule of ours: the words for that entry are the page's
// and they cannot be the microphone's without claiming something Windows does
// not do.

// Camera is a webcam, for whoever has to choose one.
//
// **The ID is the symbolic link**, ninety-four characters of device path on the
// development machine: it is what identifies a camera across restarts, while
// the name does not — two cameras of the same model share it.
// **There is no `Default` field here**, and the microphone's is the reason to
// look twice at that: it is serialised, it is documented, and no page reads it.
// The entry for "the first usable one" is drawn by the page from its own
// catalogue, and its tooltip names the camera in use — which, when that entry
// is the selected one, **is** the camera an empty choice opens, by construction.
// A field with no consumer cannot break, and the sentence explaining it cannot
// fail: that pair is the worst of both places, and `deadcode` does not see it
// because it is JSON and not code.
type Camera struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// apiCameras lists the available cameras.
func (s *Server) apiCameras(w http.ResponseWriter, r *http.Request) {
	if s.opts.Cameras == nil {
		// No enumeration: the page keeps only the entry for the one in use, and
		// the box cannot be pressed. It is also what the tests see, since they
		// do not build the capture.
		writeJSON(w, http.StatusOK, map[string]any{"devices": []Camera{}})
		return
	}
	devs, err := s.opts.Cameras()
	if err != nil {
		s.log.Error("camera enumeration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrCamListFailed)
		return
	}
	if devs == nil {
		devs = []Camera{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// apiCamera chooses which camera to capture from.
//
// **The order is check, write, apply**, as for the microphone and for the same
// reason: a choice applied and not persisted would come back by itself at the
// next start with nothing to say so, and a choice persisted that could not be
// applied is seen at once, because the page shows the camera that is **open**.
//
// The empty ID is a valid choice like the others: it means "the first usable
// one", that is, follow the rule rather than pin a device.
func (s *Server) apiCamera(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID *string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil || body.ID == nil {
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
		return
	}
	id := *body.ID

	// **An ID that is not connected is refused here**, rather than discovered at
	// the next open: the capture would fall back on another camera without
	// whoever pressed knowing it, and the choice would stay written in the file
	// saying something that does not happen. Whoever cannot enumerate cannot ask
	// the question, and then trusts: better an unverified choice than an inert
	// box.
	//
	// **It is not the same thing as the fallback**, and the two are not in
	// contradiction: a camera unplugged at three in the morning must not stop
	// the monitor, while a camera chosen at this instant and not there is a
	// choice that can be corrected by whoever is looking at the box.
	if id != "" && s.opts.Cameras != nil {
		devs, err := s.opts.Cameras()
		if err != nil {
			s.log.Error("camera enumeration", "error", err)
			writeJSONError(w, http.StatusInternalServerError, ErrCamListFailed)
			return
		}
		found := false
		for _, d := range devs {
			if d.ID == id {
				found = true
				break
			}
		}
		if !found {
			writeJSONError(w, http.StatusConflict, ErrNoSuchCam)
			return
		}
	}

	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.CameraDeviceID = id
		// **The superseded key goes with the choice.** It is read only when the
		// id is empty, so a choice of "the first usable one" made from the box
		// would otherwise be overruled at the next start by a `camera_name`
		// written years ago — a command that moves nothing until a restart, and
		// then moves something nobody asked for.
		c.CameraName = ""
	}); err != nil {
		s.log.Error("saving the configuration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	// **It reopens even when the ID is the previous one.** Repeating the choice
	// is the only way the viewer has of saying "try again now", and it is what
	// is needed when the chosen camera was unplugged and has come back.
	if s.opts.UseCamera != nil {
		s.opts.UseCamera(id)
	}
	s.log.Info("camera chosen", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}
