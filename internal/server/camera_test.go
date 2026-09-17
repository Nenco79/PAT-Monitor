package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patmonitor/internal/config"
)

// The camera's routes are the microphone's twins, so these tests are the twins
// of `microphone_test.go` — and they exist rather than being taken as read
// because "the same shape" is a claim about code somebody will edit. What is
// **not** a twin is the last two: clearing the superseded key, and the fallback
// arriving as a fact instead of being worked out from two links.

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

func chooseCam(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/camera", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// The choice is saved **and** applied, in that order. The empty ID is a choice
// like the others and means "the first usable one": without that branch there
// would be no way back after pinning a camera.
func TestChoosingACameraIsSavedAndApplied(t *testing.T) {
	s, token, applied := fakeCameras(t)

	if w := chooseCam(t, s, token, `{"id":"`+jsonEscape(anotherLink)+`"}`); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if s.conf().CameraDeviceID != anotherLink {
		t.Errorf("the configuration was left with %q", s.conf().CameraDeviceID)
	}
	if *applied != anotherLink {
		t.Errorf("the capture received %q: the choice is saved and not applied", *applied)
	}
	if savedConfig(t, s.opts.Config).CameraDeviceID != anotherLink {
		t.Errorf("%q went to the file: the choice is applied and not written, "+
			"that is, it comes back by itself at the next start",
			savedConfig(t, s.opts.Config).CameraDeviceID)
	}

	if w := chooseCam(t, s, token, `{"id":""}`); w.Code != http.StatusOK {
		t.Fatalf("the empty ID was refused: %d %s", w.Code, w.Body.String())
	}
	if s.conf().CameraDeviceID != "" || savedConfig(t, s.opts.Config).CameraDeviceID != "" {
		t.Errorf("it does not go back to the first usable one: %q in memory, %q on the file",
			s.conf().CameraDeviceID, savedConfig(t, s.opts.Config).CameraDeviceID)
	}
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

	if w := chooseCam(t, s, token, `{"id":""}`); w.Code != http.StatusOK {
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

// **A camera that is not connected is refused here.**
//
// It is not in contradiction with the fallback: a camera unplugged at three in
// the morning must not stop the monitor, while a camera chosen at this instant
// and not there is a choice whoever is looking at the box can still correct.
// Passed on, it would sit in the file describing nothing.
func TestACameraThatIsNotConnectedIsRefused(t *testing.T) {
	s, token, applied := fakeCameras(t)

	w := chooseCam(t, s, token, `{"id":"a link nobody has"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != string(ErrNoSuchCam) {
		t.Errorf("the reason for the refusal is %q", body.Error)
	}
	if s.conf().CameraDeviceID != "" || *applied != "" {
		t.Errorf("a refused ID got through anyway: configuration %q, capture %q",
			s.conf().CameraDeviceID, *applied)
	}
}

// **If it could not be saved, it is not applied.** A choice applied and not
// persisted comes back by itself at the next start with nothing to say so.
func TestACameraChoiceThatCannotBeSavedIsNotApplied(t *testing.T) {
	s, token, applied := fakeCameras(t)
	s.opts.Config = unwritableStore(s.conf())

	w := chooseCam(t, s, token, `{"id":"`+jsonEscape(anotherLink)+`"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if *applied != "" {
		t.Errorf("the capture received %q having saved nothing", *applied)
	}
}

// The route changes the configuration, so it wants a session: whoever has none
// does not decide which room somebody else's monitor shows.
func TestTheCameraRoutesNeedASession(t *testing.T) {
	s, _, _ := fakeCameras(t)

	if w := chooseCam(t, s, "", `{"id":""}`); w.Code != http.StatusUnauthorized {
		t.Errorf("with no session the change answers %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("with no session the list answers %d", w.Code)
	}
}

// **Whoever cannot enumerate answers with an empty list, not an error**, and it
// travels as `[]` and not as `null`: the page separates "I have not asked yet"
// from "there are none", and a null would make the two the same — the box would
// wait for ever.
func TestWithoutAnEnumerationTheCameraListIsEmpty(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	r := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"devices":[]`) {
		t.Errorf("the empty list travels as %s", w.Body.String())
	}
}

// **The chosen camera travels with the status, and the server fills it in**,
// being the only one that holds the configuration. Whoever builds the Status
// knows which camera is **open**, which is the other half.
func TestTheChosenCameraTravelsWithTheStatus(t *testing.T) {
	s, token, _ := fakeCameras(t)
	s.opts.StatusFn = func() Status {
		return Status{Camera: "HD Pro Webcam C920", CameraFallback: true}
	}

	if got := s.Status().CameraChosen; got != "" {
		t.Errorf("with no choice made the status declares %q", got)
	}
	if w := chooseCam(t, s, token, `{"id":"`+jsonEscape(aCameraLink)+`"}`); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	st := s.Status()
	if st.CameraChosen != aCameraLink {
		t.Errorf("the status declares %q chosen", st.CameraChosen)
	}
	// The choice does not touch what the capture reports: the swap has not
	// happened yet, and the page has to go on showing what is on screen.
	if !st.CameraFallback || st.Camera != "HD Pro Webcam C920" {
		t.Errorf("the choice overwrote what the capture is reporting: %+v", st)
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

// jsonEscape is the smallest thing that makes a Windows device path a JSON
// string: these links are mostly backslashes, and a test body written by hand
// would be a test of our own quoting.
func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}
