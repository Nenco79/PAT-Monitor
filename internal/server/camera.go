package server

import (
	"net/http"

	"patmonitor/internal/config"
)

// The camera's routes: which ones there are, and which one is used.
//
// They are the microphone's twins — the same shape, the same order, the same
// refusals — and they are so by construction: both run through listDevices
// and chooseDevice, in microphone.go, so whoever comes to change one of the two
// changes both, and there is no second way of doing it to work out. See there
// for the arguments, which hold unchanged: **the list does not travel with the
// status**, because enumerating costs a round through Media Foundation and the
// heartbeat asks every three seconds from every open page; what does live in
// the status is which camera is open and which one was chosen.
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
// **There is no `Default` field here, nor on the microphone**, which had one:
// it was serialised, it was documented, and no page read it. The entry for
// "the first usable one" is drawn by the page from its own catalogue, and its
// tooltip names the camera in use — which, when that entry
// is the selected one, **is** the camera an empty choice opens, by construction.
// A field with no consumer cannot break, and the sentence explaining it cannot
// fail: that pair is the worst of both places, and `deadcode` does not see it
// because it is JSON and not code.
type Camera struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c Camera) deviceID() string { return c.ID }

// apiCameras lists the available cameras.
func (s *Server) apiCameras(w http.ResponseWriter, r *http.Request) {
	listDevices(s, w, "camera", s.opts.Cameras, ErrCamListFailed)
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
//
// **A camera that is not connected is refused, and it is not the same thing as
// the fallback**: the two are not in contradiction. A camera unplugged at three
// in the morning must not stop the monitor, while a camera chosen at this
// instant and not there is a choice that can be corrected by whoever is looking
// at the box.
func (s *Server) apiCamera(w http.ResponseWriter, r *http.Request) {
	chooseDevice(s, w, r, "camera", s.opts.Cameras, ErrCamListFailed, ErrNoSuchCam,
		func(c *config.Config, id string) {
			c.CameraDeviceID = id
			// **The superseded key goes with the choice.** It is read only when
			// the id is empty, so a choice of "the first usable one" made from
			// the box would otherwise be overruled at the next start by a
			// `camera_name` written years ago — a command that moves nothing
			// until a restart, and then moves something nobody asked for.
			c.CameraName = ""
		}, s.opts.UseCamera)
}
