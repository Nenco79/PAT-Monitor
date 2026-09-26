package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"patmonitor/internal/config"
	"patmonitor/internal/record"
)

// serverWithClip prepares a server with a valid session and a store with one
// clip in it, of known content: that way a range request is judged on the bytes
// and not on the status code.
func serverWithClip(t *testing.T, content []byte) (*Server, string, *record.Store, string) {
	t.Helper()

	hash, err := config.HashPassword("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.PasswordHash = hash

	dir := t.TempDir()
	store := record.NewStore(dir, record.StoreConfig{})
	const name = "clip-20260901-080634-motion.mp4"
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := New(Options{
		Config: storeFor(t, cfg),
		Clips:  store,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	token, err := s.sessions.create("test", anyRoad)
	if err != nil {
		t.Fatal(err)
	}
	return s, token, store, name
}

func request(t *testing.T, s *Server, token, method, url string, range_ string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, url, nil)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	if range_ != "" {
		r.Header.Set("Range", range_)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// **Range requests are what make a clip navigable.** Without them, whoever wants
// to jump to second twelve downloads the first twelve seconds: on five megabytes
// through the funnel that is the difference between watching and waiting.
func TestTheClipRouteAnswersPartialRequests(t *testing.T) {
	content := make([]byte, 4096)
	for i := range content {
		content[i] = byte(i)
	}
	s, token, _, name := serverWithClip(t, content)

	w := request(t, s, token, http.MethodGet, "/api/clips/"+name, "bytes=100-199")
	if w.Code != http.StatusPartialContent {
		t.Fatalf("code %d instead of 206", w.Code)
	}
	if got := w.Body.Len(); got != 100 {
		t.Errorf("%d bytes delivered instead of 100", got)
	}
	if string(w.Body.Bytes()) != string(content[100:200]) {
		t.Error("the bytes delivered are not the ones asked for")
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Error("the server does not declare that it accepts range requests")
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("type %q instead of video/mp4", ct)
	}

	// And the whole request goes on working.
	if w := request(t, s, token, http.MethodGet, "/api/clips/"+name, ""); w.Code != http.StatusOK ||
		w.Body.Len() != len(content) {
		t.Errorf("the whole request gave %d with %d bytes", w.Code, w.Body.Len())
	}
}

// Downloading is asked for with a parameter, and only the header changes: the
// file's name is composed by the server from what it has already validated.
func TestDownloadingAClipAsksForAnAttachment(t *testing.T) {
	s, token, _, name := serverWithClip(t, []byte("x"))

	w := request(t, s, token, http.MethodGet, "/api/clips/"+name+"?download", "")
	if d := w.Header().Get("Content-Disposition"); d == "" {
		t.Error("no Content-Disposition: the file opens in the browser instead of downloading")
	}
	w = request(t, s, token, http.MethodGet, "/api/clips/"+name, "")
	if d := w.Header().Get("Content-Disposition"); d != "" {
		t.Errorf("Content-Disposition %q without it having been asked for", d)
	}
}

// **The name comes from the viewer, inside an address.** The validation lives in
// the store; this test checks that it goes through there on the HTTP road too,
// and that the refusal is a 404 with a code, not a trace of file paths.
func TestAClipRequestCannotEscapeTheDirectory(t *testing.T) {
	s, token, store, _ := serverWithClip(t, []byte("x"))
	outside := filepath.Join(filepath.Dir(store.Dir()), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"..%2Fsecret.txt",
		"..%5Csecret.txt",
		"monitor.log",
		"clip-20260902-080634-motion.mp4", // right shape, file that is not there
	}
	for _, n := range bad {
		w := request(t, s, token, http.MethodGet, "/api/clips/"+n, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %q answered %d instead of 404", n, w.Code)
		}
		if b := w.Body.String(); b != "" {
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err == nil {
				if body["error"] != string(ErrNoSuchClip) {
					t.Errorf("GET %q answered with the code %v", n, body["error"])
				}
			}
		}
	}
}

// The lock and the deletion go through the same routes, and have to stay behind
// the session: they are recordings of a child.
func TestTheClipRoutesNeedASession(t *testing.T) {
	s, token, _, name := serverWithClip(t, []byte("x"))

	cases := []struct {
		method, url string
	}{
		{http.MethodGet, "/api/clips"},
		{http.MethodGet, "/api/clips/" + name},
		{http.MethodPost, "/api/clips/" + name + "/keep"},
		{http.MethodPost, "/api/clips/" + name + "/release"},
		{http.MethodPost, "/api/clips/" + name + "/delete"},
	}
	for _, p := range cases {
		// **The data routes answer 401**, not a redirect: inside a <video> tag a
		// 303 towards /login means nothing, and the player would report a
		// generic failure.
		if w := request(t, s, "", p.method, p.url, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no session answered %d instead of 401", p.method, p.url, w.Code)
		}
		if w := request(t, s, token, p.method, p.url, ""); w.Code == http.StatusUnauthorized {
			t.Errorf("%s %s with the session answered 401", p.method, p.url)
		}
	}
	// The page, though, is a page, and there the redirect is the right answer.
	if w := request(t, s, "", http.MethodGet, "/clips", ""); w.Code != http.StatusSeeOther {
		t.Errorf("the page with no session answered %d instead of 303", w.Code)
	}
}

// The listing and the two actions, one after the other: it is the test that the
// whole HTTP road works, not only its pieces.
func TestKeepingAndDeletingThroughTheRoutes(t *testing.T) {
	s, token, store, name := serverWithClip(t, []byte("x"))

	w := request(t, s, token, http.MethodPost, "/api/clips/"+name+"/keep", "")
	if w.Code != http.StatusOK {
		t.Fatalf("the lock answered %d", w.Code)
	}
	// **The new name comes back in the response.** Without it, whoever was
	// watching that clip is left with the old address in the player, and the
	// next range request receives a 404: the lock would stop playback.
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unreadable response: %v", err)
	}
	if response["name"] == nil || response["name"] == name {
		t.Errorf("the response does not carry the new name: %v", response)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 1 || !entries[0].Kept {
		t.Fatalf("after the lock: %v (err %v)", entries, err)
	}

	kept := entries[0].Name
	if w := request(t, s, token, http.MethodPost, "/api/clips/"+kept+"/delete", ""); w.Code != http.StatusOK {
		t.Fatalf("the deletion answered %d", w.Code)
	}
	if entries, _ := store.List(); len(entries) != 0 {
		t.Errorf("the clip is still there: %v", entries)
	}
}

// With no store the page shows its empty state: nothing is broken, there is
// nothing to list. It is also the condition of the tests that do not build one.
func TestWithoutAStoreTheListingIsEmpty(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	w := request(t, s, token, http.MethodGet, "/api/clips", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code %d instead of 200", w.Code)
	}
	var entries []record.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unreadable response: %v (%s)", err, w.Body.String())
	}
	if len(entries) != 0 {
		t.Errorf("%d clips listed with no store", len(entries))
	}
}

// **A fallback that resembles success has to announce itself**, and that line
// cost its half hour: wiring the routes, the `Store` was not passed to the
// server, and the empty listing made the mistake indistinguishable from the
// normal case — two clips in the folder, an empty page, and not a word anywhere.
//
// It is the family of the GUID that does not protest and of the route that falls
// onto the fallback: where a fallback state exists, "it answered" stops being
// evidence that somebody is there.
func TestAMissingStoreSaysSo(t *testing.T) {
	hash, err := config.HashPassword("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.PasswordHash = hash

	var logged bytes.Buffer
	s, err := New(Options{
		Config: storeFor(t, cfg),
		Log:    slog.New(slog.NewTextHandler(&logged, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	token, err := s.sessions.create("test", anyRoad)
	if err != nil {
		t.Fatal(err)
	}

	// Two rounds, as a page reloading would do.
	request(t, s, token, http.MethodGet, "/api/clips", "")
	request(t, s, token, http.MethodGet, "/api/clips", "")

	lines := strings.Count(logged.String(), "clip store is not configured")
	if lines == 0 {
		t.Error("the missing store left no line: the wiring mistake would stay invisible")
	}
	// **Once only.** One line per request would be one line a second, and the
	// volume of the log must not be dictated by whoever reloads the page.
	if lines > 1 {
		t.Errorf("the line appeared %d times instead of once", lines)
	}
}

// **Releasing is the lock's other half, and the whole road has to be tested.**
//
// It is needed since clips asked for by hand are born kept: without it, every
// press of the "Record" button leaves a few megabytes on the disk that no rule
// will ever touch again.
func TestReleasingThroughTheRoutes(t *testing.T) {
	s, token, store, name := serverWithClip(t, []byte("x"))

	if w := request(t, s, token, http.MethodPost, "/api/clips/"+name+"/keep", ""); w.Code != http.StatusOK {
		t.Fatalf("the lock answered %d", w.Code)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 1 || !entries[0].Kept {
		t.Fatalf("after the lock: %v (err %v)", entries, err)
	}
	kept := entries[0].Name

	w := request(t, s, token, http.MethodPost, "/api/clips/"+kept+"/release", "")
	if w.Code != http.StatusOK {
		t.Fatalf("the release answered %d (%s)", w.Code, w.Body.String())
	}
	// The new name comes back here too, and for the lock's reason: after the
	// rename the old one no longer answers, and whoever was watching that clip
	// has the old address in the player.
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unreadable response: %v", err)
	}
	if response["name"] == nil || response["name"] == kept {
		t.Errorf("the response does not carry the new name: %v", response)
	}
	entries, err = store.List()
	if err != nil || len(entries) != 1 || entries[0].Kept {
		t.Fatalf("after the release: %v (err %v)", entries, err)
	}
	// And the clip still exists: releasing is not deleting.
	if w := request(t, s, token, http.MethodGet, "/api/clips/"+entries[0].Name, ""); w.Code != http.StatusOK {
		t.Errorf("the released clip is no longer served: %d", w.Code)
	}
}
