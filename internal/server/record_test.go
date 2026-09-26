package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"patmonitor/internal/config"
	"patmonitor/internal/record"
)

// serverWithRecorder prepares a server whose recording answers as `outcome`
// says, and counts how many times it was asked.
func serverWithRecorder(t *testing.T, outcome bool) (*Server, string, *int) {
	t.Helper()

	hash, err := config.HashPassword("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.PasswordHash = hash

	asked := 0
	s, err := New(Options{
		Config: storeFor(t, cfg),
		Record: func() bool {
			asked++
			return outcome
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	token, err := s.sessions.create("test", anyRoad)
	if err != nil {
		t.Fatal(err)
	}
	return s, token, &asked
}

// The whole road: it is pressed, the recorder receives the request, and the
// answer says it is recording.
func TestAskingForAClipByHandReachesTheRecorder(t *testing.T) {
	s, token, asked := serverWithRecorder(t, true)

	w := request(t, s, token, http.MethodPost, "/api/record", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code %d instead of 200 (%s)", w.Code, w.Body.String())
	}
	if *asked != 1 {
		t.Errorf("the recorder received %d requests instead of 1", *asked)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unreadable response: %v", err)
	}
	if response["recording"] != true {
		t.Errorf("the response does not declare the recording: %v", response)
	}
}

// **The refusal travels as a code**, which is what the codes exist for: the
// sentence is chosen by the page, which is also the only one that knows what
// language it is speaking.
//
// And the case is not theoretical: in the first seconds after the start, and
// after every capture restart, the first keyframe has not arrived and the ring
// is empty. A command that does nothing and does not say so is a knob that moves
// nothing.
func TestWithNothingInTheRingTheRefusalIsSaid(t *testing.T) {
	s, token, asked := serverWithRecorder(t, false)

	w := request(t, s, token, http.MethodPost, "/api/record", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("code %d instead of 409", w.Code)
	}
	if *asked != 1 {
		t.Errorf("the recorder received %d requests instead of 1", *asked)
	}
	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unreadable response: %v", err)
	}
	if response["error"] != string(ErrNothingToRecord) {
		t.Errorf("refusal code %q instead of %q", response["error"], ErrNothingToRecord)
	}
}

// With no recorder attached it is not a refusal: it is a piece that is not here,
// and the tests that do not build it see it. The page declares that rather than
// showing a button that answers with a generic error.
func TestWithoutARecorderTheRouteSaysItIsNotThere(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	w := request(t, s, token, http.MethodPost, "/api/record", "")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("code %d instead of 501", w.Code)
	}
	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unreadable response: %v", err)
	}
	if response["error"] != string(ErrRecordUnavailable) {
		t.Errorf("refusal code %q instead of %q", response["error"], ErrRecordUnavailable)
	}
}

// **Recording sits behind a session like everything else**, and it counts for
// more than other routes: it calls for the room to be recorded. Whoever has not
// got in must not be able to ask for it, and with the funnel's public address
// "whoever has not got in" is anybody.
//
// The test watches the opposite direction too — with the session it must **not**
// answer 401 — otherwise a route that always refuses would pass as well.
func TestTheRecordRouteNeedsASession(t *testing.T) {
	s, token, asked := serverWithRecorder(t, true)

	if w := request(t, s, "", http.MethodPost, "/api/record", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("with no session it answered %d instead of 401", w.Code)
	}
	if *asked != 0 {
		t.Errorf("the recorder received %d requests from somebody who had not got in", *asked)
	}
	if w := request(t, s, token, http.MethodPost, "/api/record", ""); w.Code == http.StatusUnauthorized {
		t.Error("with the session it answered 401")
	}
}

// **The manual clip's code is written in two languages, and the two have to
// agree.**
//
// On the monitor's side it lives in `record.CodeManual`, and becomes a piece of
// the file's name; on the page's side it serves to choose the word to show,
// because that code **is not an alert** and has no entry among those. There is
// no way of sharing the constant — one is read by Go, the other by a browser —
// so it is watched from here.
//
// The divergence would be silent and ugly: the clip's row would show `manual`
// instead of "Manual recording", and nobody would notice until they looked at
// one.
func TestThePageKnowsTheManualClipCode(t *testing.T) {
	js := readAsset(t, "clips.js")

	if !strings.Contains(js, "'"+record.CodeManual+"'") {
		t.Errorf("clips.js does not name the code %q: the row of a clip asked for "+
			"by hand would show the code instead of its name", record.CodeManual)
	}
	// **And the word has to be there**, otherwise the branch found above would
	// show a key. The catalogue guard looks for it in every language; here it is
	// only demanded that the page ask for it.
	if !strings.Contains(js, "T('clips.manual')") {
		t.Error("clips.js does not ask for the word for a clip asked for by hand")
	}
}
