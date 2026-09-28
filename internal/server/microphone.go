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
//
// **The camera's routes run through the same two functions**, listDevices and
// chooseDevice, below: what differs between the two devices is handed in —
// the enumeration, the codes, the key that is saved, who applies it — and what
// is the same is written once.

// Microphone is a capture endpoint, for whoever has to choose one.
//
// **The name is not the identifier**: two microphones of the same model share
// it, and Windows changes it when the user renames the device. What gets saved
// is the ID.
type Microphone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m Microphone) deviceID() string { return m.ID }

// apiMicrophones lists the available microphones.
func (s *Server) apiMicrophones(w http.ResponseWriter, r *http.Request) {
	listDevices(s, w, "microphone", s.opts.Microphones, ErrMicListFailed)
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
	chooseDevice(s, w, r, "microphone", s.opts.Microphones, ErrMicListFailed, ErrNoSuchMic,
		func(c *config.Config, id string) { c.MicDeviceID = id }, s.opts.UseMicrophone)
}

// device is what the two boxes list: something with an ID that gets saved.
type device interface{ deviceID() string }

// listDevices answers a box's list: the devices `list` enumerates, as `what`.
//
// A nil `list` is no enumeration: the page keeps only the entry for the device
// in use, and the box cannot be pressed. It is also what the tests see, since
// they do not build the capture.
//
// **The list travels as `[]` and never as `null`**, even when the enumeration
// answers nil: the page separates "I have not asked yet" from "there are
// none", and a `null` would make the two the same — the box would wait for
// ever.
func listDevices[T device](s *Server, w http.ResponseWriter, what string,
	list func() ([]T, error), failed errCode) {
	if list == nil {
		writeJSON(w, http.StatusOK, map[string]any{"devices": []T{}})
		return
	}
	devs, err := list()
	if err != nil {
		s.log.Error(what+" enumeration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, failed)
		return
	}
	if devs == nil {
		devs = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// chooseDevice checks, writes and applies the choice of a device, in that
// order: `save` writes the ID into the configuration and `use`, when there is
// one, hands it to whoever holds the device open.
//
// **The two codes are arguments and not fields of a struct**, on purpose:
// TestNoProseTravelsAsACode reads them at the call site, and a code that
// arrived here inside a value would be a code nobody checks.
func chooseDevice[T device](s *Server, w http.ResponseWriter, r *http.Request, what string,
	list func() ([]T, error), failed, missing errCode,
	save func(c *config.Config, id string), use func(id string)) {
	var body struct {
		ID *string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil || body.ID == nil {
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
		return
	}
	id := *body.ID

	// **An ID that is not there is refused here**, rather than discovered at
	// the next opening: the capture would fall back on another device without
	// whoever pressed knowing it, and the choice would stay written in the file
	// saying something that does not happen. Whoever cannot enumerate cannot ask
	// the question, and then trusts: better an unverified choice than an inert
	// box.
	if id != "" && list != nil {
		devs, err := list()
		if err != nil {
			s.log.Error(what+" enumeration", "error", err)
			writeJSONError(w, http.StatusInternalServerError, failed)
			return
		}
		found := false
		for _, d := range devs {
			if d.deviceID() == id {
				found = true
				break
			}
		}
		if !found {
			writeJSONError(w, http.StatusConflict, missing)
			return
		}
	}

	if _, err := s.opts.Config.Set(func(c *config.Config) { save(c, id) }); err != nil {
		s.saveFailed(w, err)
		return
	}

	// **It reopens even when the ID is the previous one.** Repeating the choice
	// is the only way the viewer has of saying "try again now", and it is what
	// is needed when the chosen device was unplugged and has come back.
	if use != nil {
		use(id)
	}
	s.log.Info(what+" chosen", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}
