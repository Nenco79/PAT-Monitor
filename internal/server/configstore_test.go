package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"patmonitor/internal/config"
)

// **The defect the store was written for, end to end.**
//
// The server used to take the configuration by value at construction, and every
// route that changes a setting rebuilt the whole struct from that copy and wrote
// it back. So a field written by somebody else in the meantime was undone by the
// next save from any page — concretely the node name Tailscale grants, recorded
// by `main` on the tunnel's goroutine, which no route has any business carrying.
// The user-visible outcome was the one that callback exists to prevent: the
// public address changing in silence, and the next start asking again for the
// name that was already refused.
//
// The shape matters more than the field: **what is asserted is that the server
// carries no copy of its own.** Any value changed outside it and not by a route
// has to survive a route saving something else, and there is no test on the
// field the route did change that could see it.
func TestASaveFromAPageKeepsWhatTheTunnelWrote(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")

	// The tunnel records the name the tailnet actually granted.
	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.FunnelHostname = "patmon-granted"
	}); err != nil {
		t.Fatal(err)
	}

	// And then a page saves something else entirely.
	r := httptest.NewRequest(http.MethodPost, "/api/onboarding/done", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	// On the file, which is the half that decides what tomorrow's start asks for.
	if got := savedConfig(t, s.opts.Config).FunnelHostname; got != "patmon-granted" {
		t.Errorf("the file carries %q: the granted name was overwritten by a save "+
			"from a page, so the next start asks for the losing name again and the "+
			"public address changes with nobody having asked", got)
	}
	// And in memory, since the two are now one value.
	if got := s.conf().FunnelHostname; got != "patmon-granted" {
		t.Errorf("in memory the name is %q", got)
	}
	// And the route's own change took, or the test would pass on a save that
	// never happened.
	if !savedConfig(t, s.opts.Config).OnboardingDone {
		t.Error("the route saved nothing: this test would pass on any broken save")
	}
}
