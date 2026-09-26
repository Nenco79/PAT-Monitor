package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"patmonitor/internal/tunnel"
)

// loginReq is a login as our own page sends it.
func loginReq(password string, prepare func(*http.Request)) *http.Request {
	body, _ := json.Marshal(map[string]string{"password": password})
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	prepare(r)
	return r
}

// **A burst from one address is weighed one attempt at a time.**
//
// The lockout was checked once, before the queue for a hash, and the first
// failure is recorded only once argon2 has run: every request sent in that
// tenth of a second found the address clean and was weighed, however many
// there were. An audit put it at the machine's whole hashing rate from one
// address, against the three attempts and a wait the lockout promises.
//
// What is counted is the failures the limiter recorded, which is exactly the
// number of passwords that were tried.
//
// **The defect was put back and this test fails with it**: with the lockout
// read once and no attempt held in flight, all twenty are weighed.
func TestABurstFromOneAddressIsWeighedOneAtATime(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	const burst = 20

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, loginReq("a-wrong-guess", fromTheFunnel))
		}()
	}
	close(start)
	wg.Wait()

	s.limiter.mu.Lock()
	rec := s.limiter.m["203.0.113.7"]
	s.limiter.mu.Unlock()
	if rec == nil {
		t.Fatal("no attempt was recorded at all: the test is not reaching the limiter")
	}
	if rec.failures > freeAttempts {
		t.Errorf("%d guesses sent together were all weighed: %d passwords tried, "+
			"where the lockout allows %d before a wait", burst, rec.failures, freeAttempts)
	}
}

// **The Internet's queue is not the house's.** With every shared slot held —
// which a caller with many addresses can do for as long as it likes — a login
// from home still gets an answer, on the slot kept for it; one from the
// Internet waits, which is what "slowed down" means there.
//
// **The defect was put back and this test fails with it**: with one pool for
// every road, the home login waits behind the Internet's until it gives up.
func TestAnAttackThroughTheFunnelDoesNotQueueTheHouse(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	for range cap(s.hashing) {
		s.hashing <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(s.hashing) {
			<-s.hashing
		}
	})

	try := func(prepare func(*http.Request)) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		r := loginReq("a-long-password", func(*http.Request) {}).WithContext(ctx)
		prepare(r)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return strings.Contains(w.Header().Get("Set-Cookie"), sessionCookieName)
	}
	if !try(fromTheLAN) {
		t.Error("with the shared slots full, a correct login from home got no session")
	}
	if try(fromTheFunnel) {
		t.Error("with the shared slots full, a login from the Internet took the house's slot")
	}
}

// A public IPv6 address is one of 2^64 in one subscriber's hands, so the /64 is
// what a lockout is charged to; the private ranges keep the whole address,
// because there a /64 is every node of a tailnet or every device on a Wi-Fi.
//
// **The defect was put back and this test fails with it**: keyed by the whole
// address, a caller rotating inside its /64 is a new stranger every time.
func TestAnIPv6LockoutIsChargedToTheSubscriber(t *testing.T) {
	keyVia := func(addr string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(tunnel.WithSourceAddr(r.Context(), netip.MustParseAddrPort(addr)))
		return clientKey(r)
	}
	if a, b := keyVia("[2001:db8:1:2::1]:4000"), keyVia("[2001:db8:1:2:ffff::9]:4000"); a != b {
		t.Errorf("two addresses of one /64 are two keys: %q and %q", a, b)
	}
	if a, b := keyVia("[2001:db8:1:2::1]:4000"), keyVia("[2001:db8:1:3::1]:4000"); a == b {
		t.Errorf("two /64s share one key: %q", a)
	}

	keyFrom := func(remote string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		return clientKey(r)
	}
	for _, pair := range [][2]string{
		{"[fd7a:115c:a1e0::1]:4000", "[fd7a:115c:a1e0::2]:4000"}, // two tailnet nodes
		{"[fe80::1]:4000", "[fe80::2]:4000"},                     // two devices on the Wi-Fi
	} {
		if a, b := keyFrom(pair[0]), keyFrom(pair[1]); a == b {
			t.Errorf("%s and %s share one key %q: somebody else's house is not one caller",
				pair[0], pair[1], a)
		}
	}
	if k := keyVia("203.0.113.7:4000"); k != "203.0.113.7" {
		t.Errorf("an IPv4 address became %q", k)
	}
}
