package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"patmonitor/internal/config"
	"patmonitor/internal/tunnel"
)

// serverWithoutPassword is the state the monitor is in immediately after a
// reset: no credential, so no authentication possible.
func serverWithoutPassword(t *testing.T) *Server {
	t.Helper()
	s, err := New(Options{
		Config: storeFor(t, config.Default()),
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func setupReq(t *testing.T, s *Server, throughFunnel bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "a-new-password", "confirm": "a-new-password"})
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	if throughFunnel {
		// This is how the funnel declares the real visitor: not with a header,
		// which the caller would write, but in the context starting from the
		// connection. See tunnel.WithSourceAddr.
		r = r.WithContext(tunnel.WithSourceAddr(r.Context(),
			netip.MustParseAddrPort("203.0.113.7:44321")))
	} else {
		r.RemoteAddr = "192.168.1.40:5555"
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// **The test that matters.** With no password there is no authentication, so
// /api/setup is open to whoever reaches it — and that is right at the first
// start. But if the password is cleared while the tunnel is on, for the length
// of that window the public address means "set the password and take the
// camera": the first person through takes it, and the owner finds themselves
// locked out of their own house.
func TestTheFirstConfigurationIsNotDoneFromTheInternet(t *testing.T) {
	s := serverWithoutPassword(t)

	w := setupReq(t, s, true)
	if w.Code != http.StatusForbidden {
		t.Fatalf("first configuration accepted from the Internet: %d %s", w.Code, w.Body.String())
	}
	if s.conf().HasPassword() {
		t.Fatal("a request from the Internet set the password")
	}
}

// From the home network, though, it has to work, otherwise the first start
// never starts.
func TestTheFirstConfigurationIsDoneFromHome(t *testing.T) {
	s := serverWithoutPassword(t)

	w := setupReq(t, s, false)
	if w.Code != http.StatusOK {
		t.Fatalf("first configuration refused from the local network: %d %s", w.Code, w.Body.String())
	}
	if !s.conf().HasPassword() {
		t.Fatal("the password was not set")
	}
}

// ResetPassword clears everything and closes everything. It is the command that
// makes it possible to get back in when the password is no longer remembered.
func TestTheResetClearsThePasswordAndClosesTheSessions(t *testing.T) {
	s, token := serverWithPassword(t, "the-forgotten-one")

	if err := s.ResetPassword(); err != nil {
		t.Fatal(err)
	}
	if s.conf().HasPassword() {
		t.Fatal("the password is still there after the reset")
	}
	if s.sessions.valid(token) {
		t.Fatal("a session survived the reset")
	}
}

// And until there is a new one, the monitor is reachable by nobody: requireAuth
// imposes it, and it has to be checked because it is what makes the reset a safe
// thing rather than an open door.
func TestWithNoPasswordNobodyGetsIn(t *testing.T) {
	s, _ := serverWithPassword(t, "the-forgotten-one")
	if err := s.ResetPassword(); err != nil {
		t.Fatal(err)
	}

	for _, route := range []string{"/api/status", "/ws"} {
		r := httptest.NewRequest(http.MethodGet, route, nil)
		r.RemoteAddr = "192.168.1.40:5555"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
			t.Errorf("%s answers %d with no password, wanted 401/403", route, w.Code)
		}
	}
}
