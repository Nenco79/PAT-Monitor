package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// press sends a request that speaks about **one** toggle.
func press(t *testing.T, s *Server, token, body string) map[string]bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/detect", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// **Touching one must not move the other.** With plain booleans in place of
// pointers, a request that speaks about crying would switch barking off simply
// by not naming it: from the phone the two buttons would be seen moving
// together, which is exactly the defect that was reported.
func TestOneToggleDoesNotDragTheOther(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	got := press(t, s, token, `{"cry":false}`)
	if got["cry"] {
		t.Error("crying did not switch off")
	}
	if !got["bark"] {
		t.Error("switching crying off switched barking off too")
	}
	if s.conf().DetectCry || !s.conf().DetectBark {
		t.Errorf("configuration: cry=%v bark=%v", s.conf().DetectCry, s.conf().DetectBark)
	}

	got = press(t, s, token, `{"bark":false}`)
	if got["cry"] || got["bark"] {
		t.Errorf("after switching barking off too: %v", got)
	}

	got = press(t, s, token, `{"cry":true}`)
	if !got["cry"] {
		t.Error("crying did not come back on")
	}
	if got["bark"] {
		t.Error("switching crying back on switched barking back on too")
	}

	// The third must not move at all through any of this: it came later, and it
	// is exactly the case where one of the branches gets forgotten.
	if !got["motion"] {
		t.Error("motion switched off with nobody naming it")
	}
	if got := press(t, s, token, `{"motion":false}`); got["motion"] || !got["cry"] {
		t.Errorf("switching motion off: %v", got)
	}
}

// The route changes the configuration, so it cannot sit outside authentication:
// whoever has no session does not decide what somebody else's room listens for.
func TestDetectionWantsASession(t *testing.T) {
	s, _ := serverWithPassword(t, "a long enough password")

	r := httptest.NewRequest(http.MethodPost, "/api/detect", strings.NewReader(`{"cry":false}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code == http.StatusOK {
		t.Error("detection was changed with no session")
	}
	if !s.conf().DetectCry {
		t.Error("and the configuration changed all the same")
	}
}

// **The test that was wanted, and was missing.** The status declared "off"
// because nobody filled the two fields: whoever builds the Status lives in
// another package, and not filling them is not an error anybody reports. From
// outside it looked like two buttons coming on together.
//
// Now they are reported by the same place that changes them, and this test
// watches them **through the status route**, that is, from where the page reads
// them.
func TestTheStatusReportsTheToggles(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	state := func() map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// They come on by default, and that is how the page has to see them.
	if got := state(); got["detectCry"] != true || got["detectBark"] != true || got["detectMotion"] != true {
		t.Fatalf("initial state: cry=%v bark=%v motion=%v, wanted on",
			got["detectCry"], got["detectBark"], got["detectMotion"])
	}

	press(t, s, token, `{"cry":false}`)

	got := state()
	if got["detectCry"] != false {
		t.Error("the status still says on after switching it off")
	}
	if got["detectBark"] != true || got["detectMotion"] != true {
		t.Error("the status declares the others off too: they move together")
	}
}
