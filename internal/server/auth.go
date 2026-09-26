package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
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
	// bornAtHome says the session was opened on the home network, over plain
	// HTTP: see sessionStore.check for why it is not accepted from the Internet.
	bornAtHome bool
	// cookieSet is when the browser was last handed this token.
	cookieSet time.Time
}

// sessionCheck is what a token turned out to be.
type sessionCheck int

const (
	// sessionInvalid is no session at all: unknown, expired or empty.
	sessionInvalid sessionCheck = iota
	sessionValid
	// sessionValidStaleCookie is valid, and the browser's copy of the cookie is
	// old enough to be handed out again.
	sessionValidStaleCookie
	// sessionWrongRoad is alive, and presented from the Internet although it
	// was opened on the home network.
	sessionWrongRoad
)

// bornAtHome says whether a session opened from this origin travelled the home
// network in clear. The LAN listener is plain HTTP by design, so its cookie
// can be read by anybody who can see the traffic; the tailnet and the Funnel
// are encrypted, and this PC's own connections never reach a wire.
func bornAtHome(o origin) bool {
	switch o.Class {
	case originLocal, originThisPC, originUnknown:
		return true
	}
	return false
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

// create generates a new session token for a caller arriving from `from`.
func (s *sessionStore) create(remote string, from origin) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.m[token] = session{expires: now.Add(s.ttl), remote: remote,
		bornAtHome: bornAtHome(from), cookieSet: now}
	return token, nil
}

// valid says whether a token is a live session for a request from home. It is
// check with the road left out, for the callers that only ask whether a
// session survived something — a password change, a reset.
func (s *sessionStore) valid(token string) bool {
	return s.check(token, false) != sessionInvalid
}

// check validates a token for a request that arrived from the Internet or not,
// and extends its life.
//
// **A session opened on the home network is not accepted from the Internet.**
// The LAN listener is plain HTTP, so its cookie crosses the Wi-Fi in clear, and
// the session store is one for every road: a cookie read off a shared network
// was also a key to the public address, and sliding renewal kept it alive after
// whoever read it had left. A browser never presents one cookie on both roads
// on its own — the cookie belongs to the host, and the home address and the
// public one are two hosts — so what this refuses is a cookie that has been
// carried from one to the other by somebody else.
//
// The token is not revoked: it is still the key it always was at home, and a
// refusal that deleted it would let whoever holds a copy log the owner out.
func (s *sessionStore) check(token string, public bool) sessionCheck {
	if token == "" {
		return sessionInvalid
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
		return sessionInvalid
	}
	sess := s.m[found]
	now := time.Now()
	if now.After(sess.expires) {
		delete(s.m, found)
		return sessionInvalid
	}
	if public && sess.bornAtHome {
		return sessionWrongRoad
	}
	// Sliding renewal: whoever watches the monitor every night must not have to
	// authenticate again at a fixed expiry.
	sess.expires = now.Add(s.ttl)
	// **And the browser's copy has to slide with it**, or the renewal is a
	// promise only the server keeps: the cookie's MaxAge was fixed at login, so
	// the browser threw the token away a week later while the server still
	// held it. Handing it out again every half life keeps the two in step
	// without a Set-Cookie on every status round.
	verdict := sessionValid
	if now.Sub(sess.cookieSet) > s.ttl/2 {
		sess.cookieSet = now
		verdict = sessionValidStaleCookie
	}
	s.m[found] = sess
	return verdict
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
	// inFlight counts the attempts each address has between begin and its
	// release. See begin.
	inFlight map[string]int
}

func newLimiter() *limiter {
	return &limiter{m: make(map[string]*attemptRecord), inFlight: make(map[string]int)}
}

// busyRetry is what an address is told when it already has an attempt being
// weighed: come back in a second, by which time that one has an answer.
const busyRetry = time.Second

// begin admits one attempt from an address, or says how long to wait.
//
// **The lockout was checked once, before the queue for a hash, and a burst
// walked straight past it.** The first failure is recorded only after argon2
// has run, so every request that arrived in that tenth of a second found the
// address clean, took its place in the queue, and was weighed however long the
// queue grew — a thousand guesses sent together were a thousand guesses, where
// the lockout promises three and then a wait. An audit measured it at the
// machine's whole hashing rate from a single address.
//
// So an address has **one attempt at a time**: the next is admitted only once
// the last has been counted, which is the order the lockout was written for. A
// person types a password and waits for the answer; the only caller with two
// in flight is a program. The release must be called when the attempt has been
// counted, success or failure.
func (l *limiter) begin(key string) (release func(), wait time.Duration) {
	if d := l.retryAfter(key); d > 0 {
		return nil, d
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] > 0 {
		return nil, busyRetry
	}
	l.inFlight[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.inFlight[key]--; l.inFlight[key] <= 0 {
				delete(l.inFlight, key)
			}
		})
	}, 0
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
		return addressKey(src.Addr())
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return addressKey(a)
	}
	return host
}

// addressKey is the unit a lockout is charged to.
//
// **A public IPv6 address is one of 2^64 in the same subscriber's hands**: a
// provider hands out a /64 at the least, so an address per attempt costs the
// caller nothing, and a lockout per address is no lockout at all. The /64 is
// the unit, which is what the address plan itself calls a single network. The
// private ranges keep the whole address, because there the /64 is somebody
// else's house — the tailnet's ULA space holds every node of the tailnet, and a
// link-local prefix every device on the Wi-Fi.
func addressKey(a netip.Addr) string {
	a = a.Unmap()
	if a.Is6() && a.IsGlobalUnicast() && !a.IsPrivate() {
		p, err := a.Prefix(64)
		if err == nil {
			return p.String()
		}
	}
	return a.String()
}
