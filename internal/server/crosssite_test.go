package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"patmonitor/internal/config"
	"patmonitor/internal/tunnel"
)

// formPost builds the thing this guard is about: a plain form submission, which
// is what somebody else's page can send here without asking anyone's
// permission. `application/x-www-form-urlencoded` is a *simple request* — no
// preflight, no CORS — and the attacker never needs to read the answer, because
// the write has already happened by then.
func formPost(path string, fields map[string]string) *http.Request {
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "192.168.1.40:5555"
	return r
}

// **The window this closes is the one the tray opens.**
//
// After "reset the password" the monitor has no credential, so /api/setup
// accepts one from whoever reaches it — by design, because at that moment there
// is nothing to prove ownership with. What was not by design is that the
// browser of the person who owns the monitor would carry the submission there
// on behalf of any page they happened to be reading: the attacker chooses the
// password, and the Funnel, which nothing takes down on a reset, then serves it
// to them from the Internet.
//
// **The defect was put back and this test fails with it**: with the check
// removed from credentials, the cross-site form is accepted and the monitor is
// claimed.
func TestAPasswordIsNotSetByAFormFromSomebodyElsesPage(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		claimed bool
	}{
		{"a page on another site", map[string]string{
			"Sec-Fetch-Site": "cross-site", "Origin": "https://not-the-monitor.example",
		}, false},
		{"a sibling name under the same suffix", map[string]string{
			"Sec-Fetch-Site": "same-site", "Origin": "https://somebody-else.quercia-lieve.ts.net",
		}, false},
		{"an old browser, Origin only", map[string]string{
			"Origin": "https://not-the-monitor.example",
		}, false},
		{"our own setup page", map[string]string{
			"Sec-Fetch-Site": "same-origin", "Origin": "http://example.com",
		}, true},
		{"the address typed by hand", map[string]string{
			"Sec-Fetch-Site": "none",
		}, true},
		{"a client that is not a browser", nil, true},
	}

	for _, c := range cases {
		s := serverWithoutPassword(t)
		r := formPost("/api/setup", map[string]string{
			"password": "a-new-password", "confirm": "a-new-password",
		})
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		// Origin is compared against the host the request arrived on, so the
		// same-origin case has to name this request's own.
		if c.headers["Sec-Fetch-Site"] == "same-origin" {
			r.Header.Set("Origin", "http://"+r.Host)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)

		claimed := s.conf().HasPassword()
		if claimed != c.claimed {
			t.Errorf("%s: the monitor was claimed=%v, wanted %v (status %d)",
				c.name, claimed, c.claimed, w.Code)
		}
		// A form submission is answered the way every other form refusal here
		// is — a redirect back to the page carrying the code — so what is
		// asserted is the code, not the status. It is the same answer whoever
		// sent it: an attacker's frame cannot read the page it lands on, and a
		// genuine mix-up gets the banner.
		if !c.claimed && !strings.Contains(w.Header().Get("Location"), "error=cross-site") {
			t.Errorf("%s: answered %d towards %q, and a submission from elsewhere "+
				"is refused as cross-site", c.name, w.Code, w.Header().Get("Location"))
		}
	}
}

// **/api/login goes through the same door, and /api/password never reaches
// it.**
//
// Login is the other route that takes a credential with no cookie to protect
// it, so it is refused here. The password change is not: it sits behind
// requireAuth, which turns away a request with no session before credentials is
// ever called — and in a browser it could not carry one anyway, the session
// cookie being SameSite=Strict. That is the older protection and the stronger
// one; the check in credentials stands behind it rather than in front.
func TestLoginRefusesTheSameSubmissionAndTheChangeNeverGetsThere(t *testing.T) {
	s := serverWithoutPassword(t)
	r := formPost("/api/login", map[string]string{"password": "x"})
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://not-the-monitor.example")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "error=cross-site") {
		t.Errorf("/api/login answered %d towards %q to a cross-site form", w.Code, loc)
	}

	r = formPost("/api/password", map[string]string{"current": "y", "password": "x"})
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("/api/password answered %d to a request with no session, and "+
			"requireAuth is what must answer it", w.Code)
	}
}

// The same refusal asked for as JSON, where the status is the answer rather
// than a redirect. It is the shape a page's fetch() uses, and it is the one
// that carries the 403.
func TestTheJSONShapeOfTheCrossSiteRefusalIsA403(t *testing.T) {
	s := serverWithoutPassword(t)
	body, _ := json.Marshal(map[string]string{"password": "a-new-password", "confirm": "a-new-password"})
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.RemoteAddr = "192.168.1.40:5555"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("answered %d, wanted 403", w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != string(ErrCrossSite) {
		t.Errorf("the code was %v, wanted %q", out["error"], ErrCrossSite)
	}
	if s.conf().HasPassword() {
		t.Error("the monitor was claimed anyway")
	}
}

// **The expensive work comes after the cheap refusal.**
//
// argon2id is 64 MiB and about a tenth of a second by choice, and apiSetup ran
// it before finding out a password was already set — that is, on every call for
// the rest of the machine's life, from anyone at home, with no limiter
// anywhere. What the test can assert without timing anything is the order: the
// second call has to be refused, and after enough refusals the limiter has to
// start saying come back later, which is the thing that was missing.
func TestSetupRefusesBeforeItHashes(t *testing.T) {
	s, err := New(Options{
		Config: storeFor(t, config.Default()),
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	body, _ := json.Marshal(map[string]string{"password": "a-new-password", "confirm": "a-new-password"})
	call := func() int {
		r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.168.1.40:5555"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}

	if code := call(); code != http.StatusOK {
		t.Fatalf("the first configuration answered %d", code)
	}
	if code := call(); code != http.StatusConflict {
		t.Errorf("a second configuration answered %d, wanted 409", code)
	}
	// freeAttempts of them are tolerated, and after that the answer is a wait:
	// without the limiter this route answered 409 for ever, each time having
	// paid for a hash first.
	var last int
	for range freeAttempts + 2 {
		last = call()
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after %d refused attempts the answer is still %d: the route is "+
			"not rate limited, and every one of them costs 64 MiB of argon2",
			freeAttempts+3, last)
	}
}

// The QR generator is engraved for whoever is in the house: from the Internet
// it was an unauthenticated encoder of arbitrary addresses, served from the
// owner's own trusted origin.
func TestTheQRCodeIsNotEngravedForTheInternet(t *testing.T) {
	s := serverWithoutPassword(t)

	r := httptest.NewRequest(http.MethodGet, "/qr?u=https://not-the-monitor.example", nil)
	r = r.WithContext(tunnel.WithSourceAddr(r.Context(),
		netip.MustParseAddrPort("203.0.113.7:44321")))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("from the Funnel /qr answered %d, wanted 403", w.Code)
	}

	// And at home it still works, or the refusal would be a way of switching
	// the guided path off.
	r = httptest.NewRequest(http.MethodGet, "/qr?u=https://patmon.quercia-lieve.ts.net", nil)
	r.RemoteAddr = "192.168.1.40:5555"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("at home /qr answered %d: the guided path shows this at two steps", w.Code)
	}
}
