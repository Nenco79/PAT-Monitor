package server

import (
	"encoding/json"
	"net/http"

	"patmonitor/internal/config"
)

// The microphone's routes: which ones there are, and which one is used.
//
// **The list does not travel with the status**, and that is a decision. The
// status is asked for by the heartbeat every three seconds from every open page,
// and enumerating the WASAPI endpoints means a COM thread and a round of
// property stores: a cost that would be paid all night for a select box almost
// nobody opens. Here it is asked for when the details open, that is, when
// somebody is about to look at it — and whoever unplugs one while the box is
// open reopens it, which is the declared price of not enumerating constantly.
//
// What **does** live in the status is which microphone is open now and which
// one was chosen: two fields the page already has, and asking for them twice by
// different roads is how the two halves diverge.

// Microphone is a capture endpoint, for whoever has to choose one.
//
// **The name is not the identifier**: two microphones of the same model share
// it, and Windows changes it when the user renames the device. What gets saved
// is the ID.
type Microphone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Default says this is the one Windows gives the default role to. **There
	// may be none** — it takes no more than the default having been a Bluetooth
	// headset that has gone away — and then the page shows the one that is
	// capturing instead.
	Default bool `json:"default"`
}

// apiMicrophones lists the available microphones.
func (s *Server) apiMicrophones(w http.ResponseWriter, r *http.Request) {
	if s.opts.Microphones == nil {
		// No enumeration: the page keeps only the entry for the one in use, and
		// the box cannot be pressed. It is also what the tests see, since they
		// do not build the monitor.
		writeJSON(w, http.StatusOK, map[string]any{"devices": []Microphone{}})
		return
	}
	devs, err := s.opts.Microphones()
	if err != nil {
		s.log.Error("microphone enumeration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrMicListFailed)
		return
	}
	if devs == nil {
		devs = []Microphone{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// apiMicrophone chooses which microphone to capture from.
//
// **The order is check, write, apply**, the same as `apiRemoteEnable` and for
// the same reason: a choice applied and not persisted would come back by itself
// at the next start with nothing to say so, and a choice persisted that could
// not be applied is seen at once, because the page shows the microphone that is
// **open**.
//
// The empty ID is a valid choice like the others: it means "whichever Windows
// calls the default", that is, follow the role rather than pin a device.
func (s *Server) apiMicrophone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID *string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil || body.ID == nil {
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
		return
	}
	id := *body.ID

	// **An ID that does not exist is refused here**, rather than discovered at
	// the next opening: the capture would fall back on the default without
	// whoever pressed knowing it, and the choice would stay written in the file
	// saying something that does not happen. Whoever cannot enumerate cannot ask
	// the question, and then trusts: better an unverified choice than an inert
	// box.
	if id != "" && s.opts.Microphones != nil {
		devs, err := s.opts.Microphones()
		if err != nil {
			s.log.Error("microphone enumeration", "error", err)
			writeJSONError(w, http.StatusInternalServerError, ErrMicListFailed)
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
			writeJSONError(w, http.StatusConflict, ErrNoSuchMic)
			return
		}
	}

	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.MicDeviceID = id
	}); err != nil {
		s.log.Error("saving the configuration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	// **It reopens even when the ID is the previous one.** Repeating the choice
	// is the only way the viewer has of saying "try again now", and it is what
	// is needed when the chosen microphone was unplugged and has come back.
	if s.opts.UseMicrophone != nil {
		s.opts.UseMicrophone(id)
	}
	s.log.Info("microphone chosen", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}
