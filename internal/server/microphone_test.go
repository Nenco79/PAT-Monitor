package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeMicrophones prepares a server that can list two microphones and notes who
// the choice is handed to.
func fakeMicrophones(t *testing.T) (s *Server, token string, applied *string) {
	t.Helper()
	s, token = serverWithPassword(t, "a long enough password")
	var chosen string
	applied = &chosen
	s.opts.Microphones = func() ([]Microphone, error) {
		return []Microphone{
			{ID: "{0.0.1.00000000}.array", Name: "Microphone Array", Default: true},
			{ID: "{0.0.1.00000000}.usb", Name: "USB Microphone"},
		}, nil
	}
	s.opts.UseMicrophone = func(id string) { chosen = id }
	return s, token, applied
}

// choose sends the microphone change request with the given session.
func choose(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/microphone", strings.NewReader(body))
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
// The empty ID is a choice like the others and means "whichever Windows calls
// the default": without this branch there would be no way of going back to
// following the role after pinning a device.
func TestChoosingAMicrophoneIsSavedAndApplied(t *testing.T) {
	s, token, applied := fakeMicrophones(t)

	if w := choose(t, s, token, `{"id":"{0.0.1.00000000}.usb"}`); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if s.conf().MicDeviceID != `{0.0.1.00000000}.usb` {
		t.Errorf("the configuration was left with %q", s.conf().MicDeviceID)
	}
	if *applied != `{0.0.1.00000000}.usb` {
		t.Errorf("the capture received %q: the choice is saved and not applied", *applied)
	}
	if savedConfig(t, s.opts.Config).MicDeviceID != `{0.0.1.00000000}.usb` {
		t.Errorf("%q went to the file: the choice is applied and not written, "+
			"that is, it comes back by itself at the next start",
			savedConfig(t, s.opts.Config).MicDeviceID)
	}

	if w := choose(t, s, token, `{"id":""}`); w.Code != http.StatusOK {
		t.Fatalf("the empty ID was refused: %d %s", w.Code, w.Body.String())
	}
	if s.conf().MicDeviceID != "" {
		t.Errorf("it does not go back to the default: %q remains", s.conf().MicDeviceID)
	}
	// **And the empty ID is written, not omitted.** `mic_device_id: ""` in the
	// file is the line that says "follow the Windows role": if it vanished,
	// whoever opens the configuration would no longer find the key they started
	// from.
	if got := savedConfig(t, s.opts.Config).MicDeviceID; got != "" {
		t.Errorf("going back to the default left %q on the file", got)
	}
}

// **A microphone that is not there is refused here.**
//
// Passing it on, the capture would fall back on the default — which is the right
// thing to do when a device is unplugged — and whoever pressed would be left
// with a choice written in the file that describes nothing: the box would say
// one microphone and the monitor would listen to another.
func TestAMicrophoneThatIsNotThereIsRefused(t *testing.T) {
	s, token, applied := fakeMicrophones(t)

	w := choose(t, s, token, `{"id":"{0.0.1.00000000}.unplugged"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != string(ErrNoSuchMic) {
		t.Errorf("the reason for the refusal is %q", body.Error)
	}
	if s.conf().MicDeviceID != "" || *applied != "" {
		t.Errorf("a refused ID got through anyway: configuration %q, capture %q",
			s.conf().MicDeviceID, *applied)
	}
}

// **If it could not be saved, it is not applied.**
//
// A choice applied and not persisted comes back by itself at the next start with
// nothing to say so: the monitor would put itself back on the previous
// microphone and whoever changed it would have no way of knowing why. It is the
// same order as `apiRemoteEnable`, where a tunnel switched on and not written
// would switch itself off.
func TestAChoiceThatCannotBeSavedIsNotApplied(t *testing.T) {
	s, token, applied := fakeMicrophones(t)
	s.opts.Config = unwritableStore(s.conf())

	w := choose(t, s, token, `{"id":"{0.0.1.00000000}.usb"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if *applied != "" {
		t.Errorf("the capture received %q having saved nothing", *applied)
	}
}

// The route changes the configuration, so it wants a session: whoever has none
// does not decide which microphone somebody else's room is listened to from.
func TestTheMicrophoneRoutesNeedASession(t *testing.T) {
	s, _, _ := fakeMicrophones(t)

	if w := choose(t, s, "", `{"id":""}`); w.Code != http.StatusUnauthorized {
		t.Errorf("with no session the change answers %d", w.Code)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/microphones", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("with no session the list answers %d", w.Code)
	}
}

// **Whoever cannot enumerate answers with an empty list, not an error.**
//
// The page treats it as "there is no choosing here": the entry for the
// microphone in use remains and the box cannot be pressed. It is also the test
// tone's case, where the microphone is not opened at all and offering a choice
// the capture ignores would be a knob that moves nothing.
func TestWithoutAnEnumerationTheListIsEmpty(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	r := httptest.NewRequest(http.MethodGet, "/api/microphones", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Devices []Microphone `json:"devices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Devices) != 0 {
		t.Errorf("a list of %d entries with nobody able to list them", len(body.Devices))
	}
	// **And the list is `[]`, not `null`.** The page separates "I have not asked
	// yet" from "there are none", and a `null` would make the two the same: the
	// box would wait for ever.
	if !strings.Contains(w.Body.String(), `"devices":[]`) {
		t.Errorf("the empty list travels as %s", w.Body.String())
	}
}

// **The choice travels with the status, and the server fills it in.**
//
// It is the server that holds the configuration, so it is the only one that
// knows which microphone was asked for: whoever builds the Status knows which
// one is **open**, which is the other half and not the same thing.
func TestTheChosenMicrophoneTravelsWithTheStatus(t *testing.T) {
	s, token, _ := fakeMicrophones(t)
	s.opts.StatusFn = func() Status {
		return Status{Microphone: "Microphone Array", MicrophoneID: `{0.0.1.00000000}.array`}
	}

	if got := s.Status().MicrophoneChosen; got != "" {
		t.Errorf("with no choice made the status declares %q", got)
	}
	if w := choose(t, s, token, `{"id":"{0.0.1.00000000}.usb"}`); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	st := s.Status()
	if st.MicrophoneChosen != `{0.0.1.00000000}.usb` {
		t.Errorf("the status declares %q chosen", st.MicrophoneChosen)
	}
	// The two fields stay distinct: the open one goes on being the one the
	// capture actually opened, which has not changed yet.
	if st.MicrophoneID != `{0.0.1.00000000}.array` {
		t.Errorf("the choice overwrote the open microphone: %q", st.MicrophoneID)
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
