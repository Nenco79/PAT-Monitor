package server

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patmonitor/internal/push"
)

// withPush attaches a notifier over a store in the test's own folder.
func withPush(t *testing.T, s *Server) *push.Notifier {
	t.Helper()
	n, err := push.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	s.opts.Push = n
	return n
}

func aPushBody(t *testing.T) string {
	t.Helper()
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	enc := base64.RawURLEncoding
	b, _ := json.Marshal(map[string]any{
		"endpoint": "https://fcm.googleapis.com/fcm/send/abc:def",
		"keys":     map[string]string{"p256dh": enc.EncodeToString(ua.PublicKey().Bytes()), "auth": enc.EncodeToString(auth)},
		"lang":     "it",
	})
	return string(b)
}

func pushRequest(method, path, token, body string, encrypted bool) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Host = "patmon-1a2b3c.quercia-lieve.ts.net"
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	if encrypted {
		r.TLS = &tls.ConnectionState{}
	}
	return r
}

// **Every notification route wants a session, the receipt included.** A
// subscription is a request that the monitor call a URL; a receipt anybody
// could post would be lines in the log written by anybody.
func TestTheNotificationRoutesNeedASession(t *testing.T) {
	s, _ := serverWithPassword(t, "a long enough password")
	withPush(t, s)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/push/key"},
		{http.MethodPost, "/api/push/subscribe"},
		{http.MethodPost, "/api/push/unsubscribe"},
		{http.MethodPost, "/api/push/test"},
		{http.MethodPost, "/api/push/seen"},
		{http.MethodGet, "/push.js"},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, pushRequest(rt.method, rt.path, "", aPushBody(t), true))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusSeeOther {
			t.Errorf("%s %s without a session: %d", rt.method, rt.path, w.Code)
		}
	}
}

// **A subscription is taken only on the encrypted road**, and its origin is
// the address it arrived on: that is where a tap on the notification leads.
func TestASubscriptionIsTakenOnlyOnTheEncryptedRoad(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")
	withPush(t, s)

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, pushRequest(http.MethodPost, "/api/push/subscribe", token, aPushBody(t), false))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), string(ErrPushInsecure)) {
		t.Fatalf("in plain HTTP: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, pushRequest(http.MethodPost, "/api/push/subscribe", token, aPushBody(t), true))
	if w.Code != http.StatusOK {
		t.Fatalf("encrypted: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, pushRequest(http.MethodPost, "/api/push/test", token,
		`{"endpoint":"https://fcm.googleapis.com/fcm/send/nobody"}`, true))
	var got struct{ Result string }
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Result != string(push.Gone) {
		t.Fatalf("a test for an endpoint nobody subscribed: %q, want %q", got.Result, push.Gone)
	}
}

// A command from another origin in the owner's own browser is refused here
// as everywhere behind a session: subscribing is making the monitor call out.
func TestASubscriptionFromAnotherOriginIsRefused(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")
	withPush(t, s)
	r := pushRequest(http.MethodPost, "/api/push/subscribe", token, aPushBody(t), true)
	r.Header.Set("Sec-Fetch-Site", "same-site")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("from a sibling origin: %d", w.Code)
	}
}

// With no notifier there is an answer that says so, and the page keeps its
// row hidden on it.
func TestAMonitorWithoutNotificationsSaysSo(t *testing.T) {
	s, token := serverWithPassword(t, "a long enough password")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, pushRequest(http.MethodGet, "/api/push/key", token, "", true))
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), string(ErrPushUnavailable)) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// **The service worker is open**: a browser refreshes it in the background
// with whatever cookie it has, and one behind a session would stay the old one
// for ever once the session expired.
func TestTheServiceWorkerIsServedWithoutASession(t *testing.T) {
	s, _ := serverWithPassword(t, "a long enough password")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/javascript") {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Content-Type"))
	}
}
