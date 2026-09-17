package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"sync"
	"time"

	"patmonitor/internal/guard"
	"patmonitor/internal/tunnel"
)

const sessionCookieName = "bm_session"

// session is an authenticated session.
type session struct {
	expires time.Time
	// remote is only for diagnosis: the session is not tied to the IP, because
	// a phone going from Wi-Fi to a mobile network changes address and would be
	// disconnected exactly when it is needed.
	remote string
}

// sessionStore keeps the sessions in memory. A restart of the app invalidates
// them, which is acceptable and in some ways desirable.
type sessionStore struct {
	mu   sync.Mutex
	m    map[string]session
	ttl  time.Duration
	stop chan struct{}
}

func newSessionStore(ttl time.Duration) *sessionStore {
	s := &sessionStore{
		m:    make(map[string]session),
		ttl:  ttl,
		stop: make(chan struct{}),
	}
	guard.Go(nil, "the session sweeper", func() { s.gc() })
	return s
}

// create generates a new session token.
func (s *sessionStore) create(remote string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[token] = session{expires: time.Now().Add(s.ttl), remote: remote}
	return token, nil
}

// valid checks a token and extends its life.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Constant-time comparison over every key: it avoids revealing by timing
	// how many leading bytes of the token are right.
	var found string
	for k := range s.m {
		if len(k) == len(token) && subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			found = k
			break
		}
	}
	if found == "" {
		return false
	}
	sess := s.m[found]
	if time.Now().After(sess.expires) {
		delete(s.m, found)
		return false
	}
	// Sliding renewal: whoever watches the monitor every night must not have to
	// authenticate again at a fixed expiry.
	sess.expires = time.Now().Add(s.ttl)
	s.m[found] = sess
	return true
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
}

// revokeAll invalidates every session and says how many it closed.
//
// It serves the case that makes a long session a risk rather than a
// convenience: a lost phone. Without it the only remedy would be restarting the
// monitor — that is, switching the baby monitor off to fix its security, which
// at three in the morning is a remedy worse than the disease.
func (s *sessionStore) revokeAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.m)
	s.m = make(map[string]session)
	return n
}

func (s *sessionStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

func (s *sessionStore) close() { close(s.stop) }

func (s *sessionStore) gc() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			now := time.Now()
			s.mu.Lock()
			for k, v := range s.m {
				if now.After(v.expires) {
					delete(s.m, k)
				}
			}
			s.mu.Unlock()
		}
	}
}

// ---------- rate limiting ----------

// Parameters of the progressive lockout.
const (
	// freeAttempts are the attempts tolerated before the slowdown begins: it
	// serves not to punish whoever mistypes in the dark.
	freeAttempts = 3
	baseLockout  = 5 * time.Second
	maxLockout   = 15 * time.Minute
	// attemptWindow is the time after which failed attempts are forgotten.
	attemptWindow = 30 * time.Minute
)

type attemptRecord struct {
	failures    int
	lockedUntil time.Time
	last        time.Time
}

// maxTrackedAddresses is how many addresses the lockout table will hold.
//
// **The keys are the caller's to choose, and nothing was ever removing them.**
// A record is deleted when its own address comes back after the window has
// passed, or when somebody logs in successfully — and an address that never
// returns does neither. Through the Funnel the addresses are real and they are
// the Internet's, so the table grew with the number of distinct addresses that
// had ever got a password wrong, for the life of the process. The session store
// beside it has had a sweeper since it was written; this one was the same shape
// with the sweeper missing.
//
// Four thousand records is a few hundred kilobytes, and it is far more than a
// house ever produces.
const maxTrackedAddresses = 4096

// limiter applies a progressive lockout per address.
//
// The funnel's URL is public: without this, the password would be brute-forcible
// from the Internet at whatever speed the network allows.
type limiter struct {
	mu sync.Mutex
	m  map[string]*attemptRecord
}

func newLimiter() *limiter {
	return &limiter{m: make(map[string]*attemptRecord)}
}

// retryAfter returns how long is left until the unlock; zero if not locked.
func (l *limiter) retryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec := l.m[key]
	if rec == nil {
		return 0
	}
	if time.Since(rec.last) > attemptWindow {
		delete(l.m, key)
		return 0
	}
	if d := time.Until(rec.lockedUntil); d > 0 {
		return d
	}
	return 0
}

// sweepLocked drops the records whose window has passed.
//
// It runs when the table is full rather than on a ticker, which is the same
// answer `retryAfter` already gives for one key: a record nobody comes back for
// costs nothing until the space it holds is wanted. A sweeper goroutine would
// need a lifetime, and this object is created without one.
func (l *limiter) sweepLocked(now time.Time) {
	for k, rec := range l.m {
		if now.Sub(rec.last) > attemptWindow {
			delete(l.m, k)
		}
	}
}

// fail records a failed attempt and returns the new lockout.
func (l *limiter) fail(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec := l.m[key]
	if rec == nil || time.Since(rec.last) > attemptWindow {
		if rec == nil && len(l.m) >= maxTrackedAddresses {
			l.sweepLocked(time.Now())
			if len(l.m) >= maxTrackedAddresses {
				// **A full table refuses new records rather than evicting
				// old ones**, and the direction matters. Evicting would let
				// whoever is locked out clear their own lockout by knocking
				// from four thousand other addresses; refusing costs them a
				// lockout they were never going to reach anyway, because a
				// caller with four thousand addresses to spare is already
				// past a per-address limit by construction. What answers
				// that caller is the global slowdown and the ceiling on
				// concurrent hashes, which is where it belongs.
				return 0
			}
		}
		rec = &attemptRecord{}
		l.m[key] = rec
	}
	rec.failures++
	rec.last = time.Now()

	if rec.failures <= freeAttempts {
		return 0
	}
	// Doubling at every attempt past the threshold, with a ceiling.
	d := baseLockout << uint(min(rec.failures-freeAttempts-1, 16))
	if d > maxLockout || d <= 0 {
		d = maxLockout
	}
	rec.lockedUntil = time.Now().Add(d)
	return d
}

// success clears the count after a successful login.
func (l *limiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// ---------- global slowdown ----------

// The per-address lockout is not enough on its own: whoever tries passwords
// from many addresses gets round it entirely, and the funnel's URL is public.
//
// The remedy cannot be a global lockout. That would be a way of **switching the
// monitor off from outside**: it would take no more than getting the password
// wrong enough times for nobody to be able to get in, and it is the same trap
// already paid for on the per-address lockout through the funnel. So nothing is
// ever closed here: things slow down, and that is all.
//
// Two properties make the slowdown harmless to whoever has a right to get in:
//
//   - it is paid **only by getting it wrong**. Whoever types the right password
//     gets in at once even in the middle of an attack; whoever tries at random
//     always gets it wrong, so always pays;
//   - whoever is already in does not come through here. Requests with a valid
//     session cookie do not touch /api/login.
//
// The pressure **decays**: it is not a state one enters and stays in, but a
// level that falls by itself. After a few minutes of quiet it is back to zero
// with nobody having to unlock anything.
const (
	// globalFreeFailures are the failures tolerated before everybody is slowed:
	// somebody mistyping must not make the others pay.
	globalFreeFailures = 10
	// globalDecayPerMinute is how many failures are forgotten every minute.
	// Ten: an attempt every six seconds is a cadence an attack exceeds and a
	// person does not.
	globalDecayPerMinute = 10
	// globalDelayStep is the delay added by each failure past the threshold.
	globalDelayStep = 250 * time.Millisecond
	// globalMaxDelay is the ceiling. It serves not to leave requests hanging for
	// minutes: five seconds are enough to make a distributed attack pointless,
	// because above them there are argon2's hundred milliseconds per attempt and
	// the per-address lockout anyway.
	globalMaxDelay = 5 * time.Second
)

// globalLimiter slows login attempts taken as a whole.
type globalLimiter struct {
	mu       sync.Mutex
	pressure float64
	last     time.Time
}

func newGlobalLimiter() *globalLimiter { return &globalLimiter{} }

// fail records a failed attempt and returns how long to hold the answer back.
func (g *globalLimiter) fail(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.decayLocked(now)
	g.pressure++
	return g.delayLocked()
}

// delay is the current delay without recording anything.
//
// **It serves the test, and says so rather than looking dead.** The
// production road charges the slowdown through `fail`, which is the whole
// design — one pays only by getting it wrong — so nothing else asks this
// question. The decay, though, is the property that separates a slowdown
// from a state one enters and never leaves, and the only way to check it is
// to read the pressure **without adding to it**: `fail` would move the very
// thing being measured.
//
// It is the model `tunnel.WithSourceAddr` sets, and the reason the dead-code
// chapter can be read at all: what the sweep lists from the program is
// either an authoritative code list or a function that declares itself a
// test helper here, in its own comment.
func (g *globalLimiter) delay(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.decayLocked(now)
	return g.delayLocked()
}

func (g *globalLimiter) decayLocked(now time.Time) {
	if g.last.IsZero() {
		g.last = now
		return
	}
	elapsed := now.Sub(g.last)
	if elapsed <= 0 {
		return
	}
	g.last = now
	g.pressure -= elapsed.Minutes() * globalDecayPerMinute
	if g.pressure < 0 {
		g.pressure = 0
	}
}

func (g *globalLimiter) delayLocked() time.Duration {
	over := g.pressure - globalFreeFailures
	if over <= 0 {
		return 0
	}
	d := time.Duration(over * float64(globalDelayStep))
	if d > globalMaxDelay {
		return globalMaxDelay
	}
	return d
}

// sleepCtx waits, but no longer than the request's life: whoever has closed the
// page must not keep a goroutine busy.
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// clientKey identifies the caller for the purposes of rate limiting.
//
// The IP address alone is used and the X-Forwarded-For headers are not looked
// at: those are settable by the client and would allow the lockout to be evaded
// by changing them on every request.
//
// For requests arriving through the funnel, RemoteAddr is the Tailscale ingress
// node that forwarded them and is identical for every visitor: using it would
// mean that whoever tries passwords at random from the Internet locks out
// whoever knows the password too. The real address is put in the context by the
// tunnel, and comes from Tailscale, not from a header the caller could write.
func clientKey(r *http.Request) string {
	if src, ok := tunnel.SourceAddr(r.Context()); ok {
		return src.Addr().String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
