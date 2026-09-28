package server

import (
	"net/http"
	"strings"
	"testing"

	"patmonitor/internal/config"
)

// The camera's routes are the microphone's twins, and what they share is tested
// once, as a row of the table in `microphone_test.go`. What is **not** a twin is
// here: clearing the superseded key, and the fallback arriving as a fact
// instead of being worked out from two links.

const aCameraLink = `\\?\usb#vid_046d&pid_082d#aaa#{guid}\global`
const anotherLink = `\\?\usb#vid_046d&pid_082d#bbb#{guid}\global`

func fakeCameras(t *testing.T) (s *Server, token string, applied *string) {
	t.Helper()
	s, token = serverWithPassword(t, "a long enough password")
	var chosen string
	applied = &chosen
	s.opts.Cameras = func() ([]Camera, error) {
		return []Camera{
			{ID: aCameraLink, Name: "HD Pro Webcam C920"},
			{ID: anotherLink, Name: "HD Pro Webcam C920"},
		}, nil
	}
	s.opts.UseCamera = func(id string) { chosen = id }
	return s, token, applied
}

// **Choosing from the box clears the superseded `camera_name`.**
//
// That key is read only when the id is empty, so choosing "the first usable one"
// from the page would be overruled at the next start by a name written years
// ago: a command that moves nothing until a restart, and then moves something
// nobody asked for.
func TestChoosingACameraClearsTheSupersededName(t *testing.T) {
	s, token, _ := fakeCameras(t)
	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.CameraName = "Some Old Webcam"
	}); err != nil {
		t.Fatal(err)
	}

	if w := choose(t, s, "/api/camera", token, ""); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if s.conf().CameraName != "" {
		t.Errorf("camera_name survived in memory: %q", s.conf().CameraName)
	}
	if savedConfig(t, s.opts.Config).CameraName != "" {
		t.Errorf("camera_name survived on the file as %q: at the next start it "+
			"would command over the choice just made",
			savedConfig(t, s.opts.Config).CameraName)
	}
}

// **The page reads the fact and not the two links.**
//
// The obvious form — chosen different from open — is the microphone's, and it is
// wrong here: the chosen link comes out of a file and the open one out of the
// enumeration, and Windows gives the same symbolic link back in different cases
// depending on who is asked. The capture matches them once and publishes the
// outcome, so what is watched from here is that somebody reads **that**.
func TestThePageReadsTheCameraFallbackAndNotTheLinks(t *testing.T) {
	js := readAsset(t, "app.js")

	if !strings.Contains(js, "cameraFallback") {
		t.Error("app.js does not read `cameraFallback`: the status sends it and nobody looks")
	}
	if !strings.Contains(js, "viewer.stats.camera-other") {
		t.Error("app.js does not ask for the word for \"another camera is being watched\"")
	}
	// And the wrong form is not there: a comparison between the two links would
	// declare another camera about the right one, on a machine where the file
	// spells the link in another case.
	if strings.Contains(js, "cameraId !== s.cameraChosen") {
		t.Error("app.js compares the two camera links: that comparison is what CameraFallback replaces")
	}
}
