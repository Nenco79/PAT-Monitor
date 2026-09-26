package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
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

// setupReq asks for the first configuration from remoteAddr, or through the
// funnel when remoteAddr is empty.
func setupReq(t *testing.T, s *Server, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "a-new-password", "confirm": "a-new-password"})
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	// A name only this PC answers to, so that what these tests refuse is the
	// road and never the name: see namesThisPC.
	r.Host = "localhost:8080"
	if remoteAddr == "" {
		// This is how the funnel declares the real visitor: not with a header,
		// which the caller would write, but in the context starting from the
		// connection. See tunnel.WithSourceAddr. The socket's own address is
		// left at loopback on purpose, which is what Tailscale's local
		// connection looks like: the visitor must still be refused.
		r.RemoteAddr = "127.0.0.1:5555"
		r = r.WithContext(tunnel.WithSourceAddr(r.Context(),
			netip.MustParseAddrPort("203.0.113.7:44321")))
	} else {
		r.RemoteAddr = remoteAddr
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

	w := setupReq(t, s, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("first configuration accepted from the Internet: %d %s", w.Code, w.Body.String())
	}
	if s.conf().HasPassword() {
		t.Fatal("a request from the Internet set the password")
	}
}

// **Nor from another device on the home network.** The home network is not the
// owner: a guest's phone on the Wi-Fi would otherwise choose the password. The
// defect was put back (the check reverted to refusing only the Internet) and
// this test failed with it.
func TestTheFirstConfigurationIsNotDoneFromAnotherDeviceAtHome(t *testing.T) {
	s := serverWithoutPassword(t)

	w := setupReq(t, s, "192.168.1.40:5555")
	if w.Code != http.StatusForbidden {
		t.Fatalf("first configuration accepted from the local network: %d %s", w.Code, w.Body.String())
	}
	if s.conf().HasPassword() {
		t.Fatal("a device on the home network set the password")
	}
}

// arrivingOn gives the request the socket address the server saw it arrive on,
// as net/http does for a real connection.
func arrivingOn(r *http.Request, local string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey,
		net.TCPAddrFromAddrPort(netip.MustParseAddrPort(local))))
}

// **A listener bound to one interface has no loopback**, and the setup must
// still work from this PC: the tray opens that interface's address and Windows
// sends the request from it. A request from the address it arrived on is this
// PC; one from another address on the same network is not. The defect was put
// back (only loopback counted as this PC) and this test failed with it.
func TestTheFirstConfigurationIsDoneAtThisPCOnItsOwnLANAddress(t *testing.T) {
	cases := []struct {
		name, remote string
		allowed      bool
	}{
		{"this PC, by its own address", "192.168.1.10:51000", true},
		{"another device on the same network", "192.168.1.40:51000", false},
	}
	for _, c := range cases {
		s := serverWithoutPassword(t)
		body, _ := json.Marshal(map[string]string{"password": "a-new-password", "confirm": "a-new-password"})
		r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = c.remote
		r.Host = "192.168.1.10:8080"
		r = arrivingOn(r, "192.168.1.10:8080")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if got := w.Code == http.StatusOK; got != c.allowed {
			t.Errorf("%s: answered %d, allowed=%v wanted", c.name, w.Code, c.allowed)
		}
		if s.conf().HasPassword() != c.allowed {
			t.Errorf("%s: password set=%v, wanted %v", c.name, s.conf().HasPassword(), c.allowed)
		}
	}
}

// From the PC itself, though, it has to work, otherwise the first start never
// starts: the guided setup is opened here, on localhost.
func TestTheFirstConfigurationIsDoneAtThisPC(t *testing.T) {
	s := serverWithoutPassword(t)

	w := setupReq(t, s, "127.0.0.1:5555")
	if w.Code != http.StatusOK {
		t.Fatalf("first configuration refused from this PC: %d %s", w.Code, w.Body.String())
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
