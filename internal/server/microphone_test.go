package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patmonitor/internal/config"
)

// The two boxes' routes run through the same two functions, so their tests are
// one table with a row per device — and the second row exists rather than
// being taken as read because each route hands those functions its own
// enumeration, codes and key, and any one of them can be the wrong one. What
// is **not** shared is in camera_test.go and at the bottom of this file:
// clearing the camera's superseded key, and what the page reads of each.

// deviceRoute is one row: a device's two routes and where its choice lands.
type deviceRoute struct {
	name string
	// list and choice are the two routes.
	list, choice string
	// fake prepares a server that can list two devices and notes who the
	// choice is handed to.
	fake func(t *testing.T) (s *Server, token string, applied *string)
	// connected is one of the two, and absent is an ID nobody has.
	connected, absent string
	noSuch            errCode
	// chosen reads the choice out of a configuration.
	chosen func(config.Config) string
	// status is what the capture reports, chosenIn reads the choice out of
	// the status, and untouched says the capture's half is still what it was.
	status    Status
	chosenIn  func(Status) string
	untouched func(Status) bool
}

func deviceRoutes() []deviceRoute {
	return []deviceRoute{
		{
			name: "microphone", list: "/api/microphones", choice: "/api/microphone",
			fake:      fakeMicrophones,
			connected: `{0.0.1.00000000}.usb`, absent: `{0.0.1.00000000}.unplugged`,
			noSuch:   ErrNoSuchMic,
			chosen:   func(c config.Config) string { return c.MicDeviceID },
			status:   Status{Microphone: "Microphone Array", MicrophoneID: `{0.0.1.00000000}.array`},
			chosenIn: func(st Status) string { return st.MicrophoneChosen },
			// The two fields stay distinct: the open one goes on being the one
			// the capture actually opened, which has not changed yet.
			untouched: func(st Status) bool { return st.MicrophoneID == `{0.0.1.00000000}.array` },
		},
		{
			name: "camera", list: "/api/cameras", choice: "/api/camera",
			fake:      fakeCameras,
			connected: anotherLink, absent: "a link nobody has",
			noSuch:   ErrNoSuchCam,
			chosen:   func(c config.Config) string { return c.CameraDeviceID },
			status:   Status{Camera: "HD Pro Webcam C920", CameraFallback: true},
			chosenIn: func(st Status) string { return st.CameraChosen },
			// The choice does not touch what the capture reports: the swap has
			// not happened yet, and the page has to go on showing what is on
			// screen.
			untouched: func(st Status) bool { return st.CameraFallback && st.Camera == "HD Pro Webcam C920" },
		},
	}
}

// fakeMicrophones prepares a server that can list two microphones and notes who
// the choice is handed to.
func fakeMicrophones(t *testing.T) (s *Server, token string, applied *string) {
	t.Helper()
	s, token = serverWithPassword(t, "a long enough password")
	var chosen string
	applied = &chosen
	s.opts.Microphones = func() ([]Microphone, error) {
		return []Microphone{
			{ID: "{0.0.1.00000000}.array", Name: "Microphone Array"},
			{ID: "{0.0.1.00000000}.usb", Name: "USB Microphone"},
		}, nil
	}
	s.opts.UseMicrophone = func(id string) { chosen = id }
	return s, token, applied
}

// choose sends a device change request to route with the given session.
//
// The body is built by json.Marshal because the camera's IDs are Windows device
// paths, mostly backslashes, and a body written by hand would be a test of our
// own quoting.
func choose(t *testing.T, s *Server, route, token, id string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, route, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// The choice is saved **and** applied, in that order.
//
// The empty ID is a choice like the others — "whichever Windows calls the
// default" for the microphone, "the first usable one" for the camera — and
// without that branch there would be no way back after pinning a device.
func TestChoosingADeviceIsSavedAndApplied(t *testing.T) {
	for _, d := range deviceRoutes() {
		t.Run(d.name, func(t *testing.T) {
			s, token, applied := d.fake(t)

			if w := choose(t, s, d.choice, token, d.connected); w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if got := d.chosen(s.conf()); got != d.connected {
				t.Errorf("the configuration was left with %q", got)
			}
			if *applied != d.connected {
				t.Errorf("the capture received %q: the choice is saved and not applied", *applied)
			}
			if got := d.chosen(savedConfig(t, s.opts.Config)); got != d.connected {
				t.Errorf("%q went to the file: the choice is applied and not written, "+
					"that is, it comes back by itself at the next start", got)
			}

			if w := choose(t, s, d.choice, token, ""); w.Code != http.StatusOK {
				t.Fatalf("the empty ID was refused: %d %s", w.Code, w.Body.String())
			}
			if got := d.chosen(s.conf()); got != "" {
				t.Errorf("it does not go back to the default: %q remains", got)
			}
			// **And the empty ID is written, not omitted.** `mic_device_id: ""`
			// in the file is the line that says "follow the Windows role", and
			// `camera_device_id: ""` "follow the rule": if it vanished, whoever
			// opens the configuration would no longer find the key they started
			// from.
			if got := d.chosen(savedConfig(t, s.opts.Config)); got != "" {
				t.Errorf("going back to the default left %q on the file", got)
			}
		})
	}
}

// **A device that is not connected is refused here.**
//
// Passing it on, the capture would fall back on another one — which is the
// right thing to do when a device is unplugged — and whoever pressed would be
// left with a choice written in the file that describes nothing: the box would
// say one device and the monitor would use another.
func TestADeviceThatIsNotConnectedIsRefused(t *testing.T) {
	for _, d := range deviceRoutes() {
		t.Run(d.name, func(t *testing.T) {
			s, token, applied := d.fake(t)

			w := choose(t, s, d.choice, token, d.absent)
			if w.Code != http.StatusConflict {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var body struct{ Error string }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error != string(d.noSuch) {
				t.Errorf("the reason for the refusal is %q", body.Error)
			}
			if got := d.chosen(s.conf()); got != "" || *applied != "" {
				t.Errorf("a refused ID got through anyway: configuration %q, capture %q",
					got, *applied)
			}
		})
	}
}

// **If it could not be saved, it is not applied.**
//
// A choice applied and not persisted comes back by itself at the next start with
// nothing to say so: the monitor would put itself back on the previous device
// and whoever changed it would have no way of knowing why. It is the same order
// as `apiRemoteEnable`, where a tunnel switched on and not written would switch
// itself off.
func TestADeviceChoiceThatCannotBeSavedIsNotApplied(t *testing.T) {
	for _, d := range deviceRoutes() {
		t.Run(d.name, func(t *testing.T) {
			s, token, applied := d.fake(t)
			s.opts.Config = unwritableStore(s.conf())

			w := choose(t, s, d.choice, token, d.connected)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if *applied != "" {
				t.Errorf("the capture received %q having saved nothing", *applied)
			}
		})
	}
}

// The routes change the configuration, so they want a session: whoever has
// none does not decide which room somebody else's monitor shows, nor which
// microphone it is listened to from.
func TestTheDeviceRoutesNeedASession(t *testing.T) {
	for _, d := range deviceRoutes() {
		t.Run(d.name, func(t *testing.T) {
			s, _, _ := d.fake(t)

			if w := choose(t, s, d.choice, "", ""); w.Code != http.StatusUnauthorized {
				t.Errorf("with no session the change answers %d", w.Code)
			}
			r := httptest.NewRequest(http.MethodGet, d.list, nil)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("with no session the list answers %d", w.Code)
			}
		})
	}
}

// **Whoever cannot enumerate answers with an empty list, not an error.**
//
// The page treats it as "there is no choosing here": the entry for the device
// in use remains and the box cannot be pressed. It is also the test tone's
// case, where the microphone is not opened at all and offering a choice the
// capture ignores would be a knob that moves nothing.
func TestWithoutAnEnumerationTheDeviceListIsEmpty(t *testing.T) {
	for _, d := range deviceRoutes() {
		t.Run(d.name, func(t *testing.T) {
			s, token := serverWithPassword(t, "a long enough password")

			r := httptest.NewRequest(http.MethodGet, d.list, nil)
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			// **And the list is `[]`, not `null`.** The page separates "I have
			// not asked yet" from "there are none", and a `null` would make the
			// two the same: the box would wait for ever.
			if !strings.Contains(w.Body.String(), `"devices":[]`) {
				t.Errorf("the empty list travels as %s", w.Body.String())
			}
		})
	}
}

// **The choice travels with the status, and the server fills it in.**
//
// It is the server that holds the configuration, so it is the only one that
// knows which device was asked for: whoever builds the Status knows which one
// is **open**, which is the other half and not the same thing.
func TestTheChosenDeviceTravelsWithTheStatus(t *testing.T) {
	for _, d := range deviceRoutes() {
		t.Run(d.name, func(t *testing.T) {
			s, token, _ := d.fake(t)
			s.opts.StatusFn = func() Status { return d.status }

			if got := d.chosenIn(s.Status()); got != "" {
				t.Errorf("with no choice made the status declares %q", got)
			}
			if w := choose(t, s, d.choice, token, d.connected); w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			st := s.Status()
			if got := d.chosenIn(st); got != d.connected {
				t.Errorf("the status declares %q chosen", got)
			}
			if !d.untouched(st) {
				t.Errorf("the choice overwrote what the capture is reporting: %+v", st)
			}
		})
	}
}

// **The status carries two fields because they can diverge, and the page has to
// read both.**
//
// `MicrophoneChosen` is what is in the file, `MicrophoneID` what is capturing:
// they were separated precisely because one field would have lied in the case
// that matters — the chosen microphone unplugged, the capture fallen back on
// another. Then the page read **one**: the box showed the choice and nothing
// said the monitor was listening to another room, that is, the two fields
// arrived separate in the JSON and were reunited into a lie on the screen.
//
// **The defect was not a wrong value, it was a missing reader**, and no test on
// the value catches it: from here what is watched is that somebody reads it.
func TestThePageReadsWhatIsCapturingAndNotOnlyWhatWasChosen(t *testing.T) {
	js := readAsset(t, "app.js")

	for _, field := range []string{"microphoneId", "microphoneChosen"} {
		if !strings.Contains(js, field) {
			t.Errorf("app.js does not read `%s`: the status sends it and nobody looks", field)
		}
	}
	// And the word for saying so has to be asked of the catalogue, otherwise the
	// branch found above would show a key.
	if !strings.Contains(js, "viewer.stats.microphone-other") {
		t.Error("app.js does not ask for the word for \"it is capturing another microphone\"")
	}
}
