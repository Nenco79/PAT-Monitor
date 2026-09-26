package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"path/filepath"
	"patmonitor/internal/config"
)

// serverWithPassword prepares a server with a password already set and a valid
// session, that is, the condition in which one arrives at the password change.
// storeFor is the configuration store a test hands the server: a real one,
// writing to a file of the test's own.
//
// **It is a real store on purpose.** The stubs it replaces existed to watch what
// the routes wrote, and a stub can only report what it was handed; the file is
// what survives a restart, which is the half those tests say they are about. So
// the assertion moves one step further out — `savedConfig` reads it back.
func storeFor(t *testing.T, cfg config.Config) *config.Store {
	t.Helper()
	cfg.SetPath(filepath.Join(t.TempDir(), "config.yaml"))
	return config.NewStore(cfg)
}

// savedConfig is what reached the file, read back.
func savedConfig(t *testing.T, store *config.Store) config.Config {
	t.Helper()
	got, err := config.Load(store.Get().Path())
	if err != nil {
		t.Fatalf("the configuration could not be read back: %v", err)
	}
	return got
}

// unwritableStore cannot save, and needs no rigged filesystem to do it: a
// configuration with no path is the one thing Save refuses outright, and the
// path is the only thing taken away. It stands in for the full disk the old
// stubs returned an error for.
func unwritableStore(cfg config.Config) *config.Store {
	cfg.SetPath("")
	return config.NewStore(cfg)
}

func serverWithPassword(t *testing.T, password string) (*Server, string) {
	t.Helper()

	hash, err := config.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.PasswordHash = hash

	s, err := New(Options{
		Config: storeFor(t, cfg),
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
	return s, token
}

// change sends a password change request with the given session.
func change(t *testing.T, s *Server, token, current, next string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"current": current, "password": next, "confirm": next,
	})
	r := httptest.NewRequest(http.MethodPost, "/api/password", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// **The property that matters most: a valid session is not enough.**
//
// A cookie is a bearer token, it says "somebody had got in from this browser"
// and not "it is you". Without the current password, whoever comes to a browser
// left open locks the owner of the house out of the house's monitor.
func TestNoChangeWithoutTheCurrentPassword(t *testing.T) {
	s, token := serverWithPassword(t, "old-password")

	w := change(t, s, token, "the-wrong-one", "new-password")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("with the wrong current password it answered %d, wanted 401", w.Code)
	}
	// And the old one has to still hold: a failed attempt cannot leave the
	// monitor in an intermediate state.
	if !config.VerifyPassword(s.conf().PasswordHash, "old-password") {
		t.Error("a refused change touched the password anyway")
	}
}

// With no session there is not even a discussion: the route sits behind
// requireAuth, and the project's criterion is refusal by default.
func TestNoChangeWithoutASession(t *testing.T) {
	s, _ := serverWithPassword(t, "old-password")

	w := change(t, s, "", "old-password", "new-password")
	if w.Code == http.StatusOK {
		t.Fatal("password change succeeded with no session")
	}
}

// With both proofs — session and current password — the change happens.
func TestWithTheCurrentPasswordItChanges(t *testing.T) {
	s, token := serverWithPassword(t, "old-password")

	w := change(t, s, token, "old-password", "new-password")
	if w.Code != http.StatusOK {
		t.Fatalf("change refused: %d %s", w.Code, w.Body.String())
	}
	if !config.VerifyPassword(s.conf().PasswordHash, "new-password") {
		t.Error("the new password is not in force")
	}
	if config.VerifyPassword(s.conf().PasswordHash, "old-password") {
		t.Error("the old password still works")
	}
}

// **Changing the password kills every session, including that of whoever
// asks.** If it is changed because it has fallen into somebody's hands, leaving
// the devices already in alive cancels the reason for the change.
func TestTheChangeClosesEverySession(t *testing.T) {
	s, token := serverWithPassword(t, "old-password")
	other, err := s.sessions.create("another device", anyRoad)
	if err != nil {
		t.Fatal(err)
	}

	if w := change(t, s, token, "old-password", "new-password"); w.Code != http.StatusOK {
		t.Fatalf("change refused: %d", w.Code)
	}

	if s.sessions.valid(other) {
		t.Error("another device stayed connected after the password change")
	}
	if s.sessions.valid(token) {
		t.Error("whoever changed the password stayed connected: the page has to get back in")
	}
}

// The two new ones have to match, and the check is on the server as well: the
// one in the page is convenience, not a guarantee.
func TestTheTwoNewOnesHaveToMatch(t *testing.T) {
	s, token := serverWithPassword(t, "old-password")

	body, _ := json.Marshal(map[string]string{
		"current": "old-password", "password": "one-password", "confirm": "another-one",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/password", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("two different passwords accepted: %d", w.Code)
	}
}

// A password that is too short is refused by config.HashPassword, and the
// refusal has to reach this far instead of being absorbed.
func TestAPasswordThatIsTooShortIsRefused(t *testing.T) {
	s, token := serverWithPassword(t, "old-password")

	w := change(t, s, token, "old-password", "short")
	if w.Code == http.StatusOK {
		t.Fatal("a password below the minimum length was accepted")
	}
	if !config.VerifyPassword(s.conf().PasswordHash, "old-password") {
		t.Error("a refused change touched the password anyway")
	}
}

// **The password change goes through the rate limiter.** Without it, this route
// becomes an unwatched oracle for guessing the current password: a back door
// round all the work in auth.go.
func TestThePasswordChangeIsUnderTheRateLimiter(t *testing.T) {
	s, token := serverWithPassword(t, "old-password")

	// It gets it wrong until the limiter starts refusing. The number is
	// deliberately generous: the test is not "after how many", it is "sooner or
	// later, yes".
	limited := false
	for range 20 {
		if w := change(t, s, token, "wrong", "new-password"); w.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("twenty wrong attempts and no slowdown: the route is an oracle")
	}
}
