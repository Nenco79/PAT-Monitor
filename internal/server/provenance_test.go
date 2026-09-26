package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"patmonitor/internal/tunnel"
)

// anyRoad is the origin the tests open their sessions from when the road is
// not what they are about. The tailnet is the one road whose sessions are
// accepted on every other, being encrypted end to end: see sessionStore.check.
var anyRoad = origin{Kind: "tailnet", Class: originTailnet}

// setupFrom posts a first-time password as our own setup page would, from
// this PC's loopback, calling the monitor by `host`.
func setupFrom(t *testing.T, s *Server, host string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "a-new-password", "confirm": "a-new-password"})
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(string(body)))
	r.Host = host
	r.Header.Set("Content-Type", "application/json")
	// Exactly what a same-origin fetch sends, and what a rebinding page sends
	// too: to the browser it is the same origin as the name it loaded.
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Origin", "http://"+host)
	r.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// **A page that re-points its own name at this PC does not get to set the
// password.**
//
// With no password set, /api/setup takes one from whoever is at this PC, and
// "at this PC" was read off the socket alone. A site whose DNS answer is
// switched to 127.0.0.1 after it loads makes the owner's browser connect from
// loopback, and every other check agrees with it: the request is same-origin
// to the browser, and Origin equals Host because both are the attacker's name.
// It then logs in, reads the public address from /api/status and walks in
// through the Funnel, which a reset does not take down.
//
// **The defect was put back and this test fails with it**: with
// setupNotFromThisPC reading the origin class alone, the foreign name claims
// the monitor.
func TestAPageUnderAForeignNameCannotSetThePasswordAtThisPC(t *testing.T) {
	for _, host := range []string{"not-the-monitor.example:8080", "desktop-pc:8080", "localhost.example"} {
		s := serverWithoutPassword(t)
		w := setupFrom(t, s, host)
		if s.conf().HasPassword() {
			t.Errorf("Host %q: the monitor was claimed (status %d)", host, w.Code)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("Host %q: answered %d, wanted 403", host, w.Code)
		}
	}

	// And the names the tray and a person at this PC really use still work,
	// or the refusal would be the first start refused.
	for _, host := range []string{"localhost:8080", "LOCALHOST:8080", "127.0.0.1:8080", "[::1]:8080", "192.168.1.20:8080", "localhost."} {
		s := serverWithoutPassword(t)
		if w := setupFrom(t, s, host); !s.conf().HasPassword() {
			t.Errorf("Host %q: the first configuration was refused (status %d)", host, w.Code)
		}
	}
}

// Whoever types the machine's name at this PC is at the right machine, and is
// sent to the page by the address they arrived on rather than refused.
func TestTheSetupPageSendsANameOnToTheAddress(t *testing.T) {
	s := serverWithoutPassword(t)
	r := httptest.NewRequest(http.MethodGet, "/setup", nil)
	r.Host = "desktop-pc:8080"
	r.RemoteAddr = "127.0.0.1:5555"
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(local)))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "http://localhost:8080/setup" {
		t.Errorf("answered %d towards %q, wanted 303 to the address", w.Code, w.Header().Get("Location"))
	}
}

// statusWith asks for /api/status with a session cookie, from wherever
// prepare puts the request.
func statusWith(s *Server, token string, prepare func(*http.Request)) int {
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	prepare(r)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w.Code
}

func fromTheLAN(r *http.Request) { r.RemoteAddr = "192.168.1.40:5555" }

func fromTheFunnel(r *http.Request) {
	*r = *r.WithContext(tunnel.WithSourceAddr(r.Context(),
		netip.MustParseAddrPort("203.0.113.7:44321")))
	r.TLS = &tls.ConnectionState{}
}

// **A cookie that crossed the house in clear does not open the public
// address.**
//
// The LAN listener is plain HTTP by design, so whoever can see the Wi-Fi can
// read its cookie; and there was one session store for every road, so that
// cookie also worked through the Funnel, from anywhere, and renewed itself for
// as long as it was used. The owner's browser never does this on its own — the
// home address and the public one are two hosts, with two cookie jars — so the
// only way a LAN-born cookie reaches the Funnel is somebody carrying it.
//
// **The defect was put back and this test fails with it**: with the road left
// out of sessionStore.check, the LAN session answers 200 from the Funnel.
func TestASessionOpenedAtHomeDoesNotOpenThePublicAddress(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")

	home, err := s.sessions.create("192.168.1.40", origin{Class: originLocal})
	if err != nil {
		t.Fatal(err)
	}
	if code := statusWith(s, home, fromTheFunnel); code != http.StatusUnauthorized {
		t.Errorf("a LAN session from the Funnel answered %d, wanted 401", code)
	}
	// Refused there, and still the key it was at home: the refusal must not be
	// a way for whoever holds a copy to log the owner out.
	if code := statusWith(s, home, fromTheLAN); code != http.StatusOK {
		t.Errorf("the same session at home answered %d after the refusal", code)
	}

	// The encrypted roads carry their sessions everywhere, which is the phone
	// with Tailscale switched off on the way out of the house.
	tail, err := s.sessions.create("192.0.2.10", origin{Class: originTailnet})
	if err != nil {
		t.Fatal(err)
	}
	if code := statusWith(s, tail, fromTheFunnel); code != http.StatusOK {
		t.Errorf("a tailnet session from the Funnel answered %d, wanted 200", code)
	}
}

// commandWith posts a detection toggle with a session cookie.
func commandWith(s *Server, token string, headers map[string]string) int {
	r := httptest.NewRequest(http.MethodPost, "/api/detect",
		strings.NewReader(`{"cry":false}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	fromTheLAN(r)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w.Code
}

// **SameSite separates sites, not origins, and a command needs the second.**
//
// Every state-changing route behind requireAuth relied on the session cookie
// being SameSite=Strict. Another port on this PC, or another node of the
// owner's tailnet, is the same site: its requests carry the cookie, and from
// there a page could switch the crying detection off with nobody pressing
// anything — the monitor's one job, silently undone.
//
// **The defect was put back and this test fails with it**: without the check
// in requireAuth, the sibling origin's command is carried out.
func TestACommandFromASiblingOriginIsRefused(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	token, err := s.sessions.create("192.168.1.40", origin{Class: originLocal})
	if err != nil {
		t.Fatal(err)
	}

	if code := commandWith(s, token, map[string]string{
		"Sec-Fetch-Site": "same-site", "Origin": "http://192.168.1.20:9000",
	}); code != http.StatusForbidden {
		t.Errorf("a command from a sibling origin answered %d, wanted 403", code)
	}
	if code := commandWith(s, token, map[string]string{
		"Origin": "http://192.168.1.20:9000",
	}); code != http.StatusForbidden {
		t.Errorf("a command from another origin, Origin only, answered %d, wanted 403", code)
	}
	if !s.conf().DetectCry {
		t.Fatal("the crying detection was switched off by somebody else's page")
	}

	// Our own page, and a client that is not a browser, still command.
	if code := commandWith(s, token, map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusOK {
		t.Errorf("our own page's command answered %d", code)
	}
	if code := commandWith(s, token, nil); code != http.StatusOK {
		t.Errorf("a non-browser client's command answered %d", code)
	}
}

// **The browser's copy of the session slides with the server's.**
//
// The renewal was sliding on the server and fixed in the browser: the cookie's
// MaxAge was set once at login, so whoever watched every night was sent back
// to the login a week later by their own browser, with the session still alive
// on the server. The cookie is handed out again once it is half its life old.
func TestTheBrowsersCookieIsHandedOutAgainAsTheSessionSlides(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	token, err := s.sessions.create("192.168.1.40", origin{Class: originLocal})
	if err != nil {
		t.Fatal(err)
	}

	ask := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		fromTheLAN(r)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if c := ask().Header().Get("Set-Cookie"); c != "" {
		t.Errorf("a fresh cookie was handed out again at once: %q", c)
	}

	s.sessions.mu.Lock()
	sess := s.sessions.m[token]
	sess.cookieSet = time.Now().Add(-s.sessions.ttl)
	s.sessions.m[token] = sess
	s.sessions.mu.Unlock()

	c := ask().Header().Get("Set-Cookie")
	if !strings.Contains(c, token) {
		t.Errorf("an old cookie was not handed out again: %q", c)
	}
	if c := ask().Header().Get("Set-Cookie"); c != "" {
		t.Errorf("the cookie was handed out on every request: %q", c)
	}
}

// The Secure flag follows the connection and not a header the caller writes;
// and HSTS goes only on an answer that arrived encrypted.
func TestTheConnectionDecidesWhatIsSecure(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if isTLS(r) {
		t.Error("a plain request was taken for TLS on the strength of a header")
	}

	s, _ := serverWithPassword(t, "a-long-password")
	plain := httptest.NewRecorder()
	s.Handler().ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/login", nil))
	if h := plain.Header().Get("Strict-Transport-Security"); h != "" {
		t.Errorf("HSTS over plain HTTP: %q", h)
	}
	r = httptest.NewRequest(http.MethodGet, "/login", nil)
	r.TLS = &tls.ConnectionState{}
	enc := httptest.NewRecorder()
	s.Handler().ServeHTTP(enc, r)
	if h := enc.Header().Get("Strict-Transport-Security"); h == "" {
		t.Error("no HSTS on an encrypted answer")
	}
}

// **A tailnet node reached over IPv6 is the tailnet.** Its range sits inside
// the private one, so it was classed as the local network, and a session
// opened from there — encrypted end to end — was refused at the Funnel as a
// cookie carried off the Wi-Fi, with a warning saying so. A phone with
// Tailscale on, which MagicDNS answers over IPv6, met it on the way out.
//
// **The defect was put back and this test fails with it**: without the IPv6
// range the address is "local network" and the session is refused.
func TestATailnetNodeOverIPv6IsTheTailnet(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[fd7a:115c:a1e0::5]:443"
	if c := requestOrigin(r).Class; c != originTailnet {
		t.Fatalf("a tailnet IPv6 address was classed %v", c)
	}
	s, _ := serverWithPassword(t, "a-long-password")
	token, err := s.sessions.create("", requestOrigin(r))
	if err != nil {
		t.Fatal(err)
	}
	if code := statusWith(s, token, fromTheFunnel); code != http.StatusOK {
		t.Errorf("a session opened over the tailnet's IPv6 answered %d from the Funnel", code)
	}
}

// **A page under a foreign name cannot guess at the login from this PC
// either.** Refused only at the setup, a rebinding page could still post to
// /api/login under the owner's own loopback key — locking the owner out and
// holding the hashing slot kept for the house.
//
// **The defect was put back and this test fails with it**: without the name
// check in credentials, the guess is weighed and charged to 127.0.0.1.
func TestAPageUnderAForeignNameCannotGuessFromThisPC(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	r := loginReq("a-wrong-guess", func(r *http.Request) {
		r.RemoteAddr = "127.0.0.1:5555"
		r.Host = "not-the-monitor.example:8080"
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Origin", "http://not-the-monitor.example:8080")
	})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("answered %d, wanted 403", w.Code)
	}
	s.limiter.mu.Lock()
	rec := s.limiter.m["127.0.0.1"]
	s.limiter.mu.Unlock()
	if rec != nil {
		t.Errorf("the guess was charged to this PC's own key: %d failures", rec.failures)
	}
}

// The three pages that post credentials are sent on to an address under a
// name, and a link-local address with a zone, which no URL can carry, becomes
// localhost.
func TestTheCredentialPagesSendANameOnToAnAddress(t *testing.T) {
	for _, c := range []struct {
		path  string
		local net.Addr
		want  string
	}{
		{"/onboarding", &net.TCPAddr{IP: net.IPv4(192, 168, 1, 20), Port: 8080}, "http://192.168.1.20:8080/onboarding"},
		{"/login", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, "http://localhost:8080/login"},
		{"/setup", &net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 8080, Zone: "12"}, "http://localhost:8080/setup"},
	} {
		s := serverWithoutPassword(t)
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		r.Host = "desktop-pc:8080"
		ap := netip.MustParseAddrPort(c.local.String())
		r.RemoteAddr = ap.String()
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, c.local))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != c.want {
			t.Errorf("%s from %s: answered %d towards %q, wanted %q",
				c.path, c.local, w.Code, w.Header().Get("Location"), c.want)
		}
	}
}

// **A refused command does not use up the cookie's renewal.** The half-life
// was marked when the check answered, so a command refused a moment later
// sent no cookie and the next renewal came half a life late — after the
// browser's copy could already have expired.
func TestARefusedCommandDoesNotUseUpTheRenewal(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	token, err := s.sessions.create("192.168.1.40", origin{Class: originLocal})
	if err != nil {
		t.Fatal(err)
	}
	s.sessions.mu.Lock()
	sess := s.sessions.m[token]
	sess.cookieSet = time.Now().Add(-s.sessions.ttl)
	s.sessions.m[token] = sess
	s.sessions.mu.Unlock()

	if code := commandWith(s, token, map[string]string{"Sec-Fetch-Site": "same-site"}); code != http.StatusForbidden {
		t.Fatalf("the sibling's command answered %d", code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	fromTheLAN(r)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Set-Cookie"), token) {
		t.Error("after a refused command the stale cookie was not handed out again")
	}
}

// A socket's own IPv6 address keeps all of it: a house hands every device a
// global address from one /64, and one device's wrong passwords must not lock
// out the others. The /64 is for the Funnel.
func TestAHouseOnIPv6IsNotOneCaller(t *testing.T) {
	a := httptest.NewRequest(http.MethodGet, "/", nil)
	a.RemoteAddr = "[2001:db8:1:2::10]:5000"
	b := httptest.NewRequest(http.MethodGet, "/", nil)
	b.RemoteAddr = "[2001:db8:1:2::11]:5000"
	if clientKey(a) == clientKey(b) {
		t.Errorf("two devices of one house share the key %q", clientKey(a))
	}
}
