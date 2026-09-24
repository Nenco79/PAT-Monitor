package server

import (
	"fmt"
	"testing"
	"time"
)

// TestTheGlobalLimiterNeverCloses is the property that matters more than any
// other: a slowdown that became a block would be a way of switching the baby
// monitor off from outside, by getting the password wrong enough times.
func TestTheGlobalLimiterNeverCloses(t *testing.T) {
	g := newGlobalLimiter()
	now := time.Unix(0, 0)

	var d time.Duration
	for range 10_000 {
		d = g.fail(now)
	}
	if d > globalMaxDelay {
		t.Fatalf("after 10000 attempts the wait is %v, past the cap of %v", d, globalMaxDelay)
	}
	if d <= 0 {
		t.Fatalf("after 10000 attempts it does not slow down at all")
	}
}

// TestTheGlobalLimiterForgivesTheFirstAttempts: whoever mistypes in the dark
// must not make everybody else pay.
func TestTheGlobalLimiterForgivesTheFirstAttempts(t *testing.T) {
	g := newGlobalLimiter()
	now := time.Unix(0, 0)

	for i := range globalFreeFailures {
		if d := g.fail(now); d != 0 {
			t.Fatalf("at attempt %d it already slows by %v", i+1, d)
		}
	}
	if d := g.fail(now); d <= 0 {
		t.Fatalf("past the threshold it should slow down, instead it is %v", d)
	}
}

// TestTheGlobalLimiterDecays checks that the pressure goes back to zero by
// itself.
//
// It is the difference between a slowdown and a state one enters and stays in:
// without the decay, a burst of attempts would leave the monitor slow for the
// rest of its life.
func TestTheGlobalLimiterDecays(t *testing.T) {
	g := newGlobalLimiter()
	now := time.Unix(0, 0)

	for range globalFreeFailures + 40 {
		g.fail(now)
	}
	if d := g.delay(now); d <= 0 {
		t.Fatalf("it should have been under pressure, wait %v", d)
	}

	// Five minutes of quiet clear 50 failures at the declared rate.
	later := now.Add(5 * time.Minute)
	if d := g.delay(later); d != 0 {
		t.Errorf("after five minutes of quiet the wait is still %v", d)
	}
}

// TestRevokeAll checks that no session survives, including that of whoever
// asked for the revocation.
func TestRevokeAll(t *testing.T) {
	s := newSessionStore(time.Hour)
	defer s.close()

	first, err := s.create("192.168.1.10")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.create("109.54.184.22")
	if err != nil {
		t.Fatal(err)
	}
	if n := s.count(); n != 2 {
		t.Fatalf("active sessions %d, wanted 2", n)
	}

	if n := s.revokeAll(); n != 2 {
		t.Errorf("revokeAll closed %d sessions, wanted 2", n)
	}
	if s.valid(first) || s.valid(second) {
		t.Error("a session survived the revocation")
	}
	if n := s.count(); n != 0 {
		t.Errorf("after the revocation %d sessions remain", n)
	}
}

// **The lockout table does not grow for ever.**
//
// Its keys are the caller's to choose — through the Funnel they are the
// Internet's addresses, real ones — and nothing ever removed a record whose
// address did not come back: `retryAfter` drops a stale one only when that same
// address knocks again, and `success` only on a login that worked. So the table
// held every address that had ever got a password wrong, for the life of the
// process. The session store beside it has had a sweeper since it was written.
//
// **The defect was put back and this test fails with it**, at 20 000 records
// against the 4096 allowed.
func TestTheLockoutTableIsBounded(t *testing.T) {
	l := newLimiter()
	for i := range maxTrackedAddresses * 5 {
		l.fail(fmt.Sprintf("198.51.100.%d:%d", i%256, i))
	}
	l.mu.Lock()
	n := len(l.m)
	l.mu.Unlock()
	if n > maxTrackedAddresses {
		t.Errorf("%d addresses tracked, and the table holds %d: it grows with "+
			"every address that ever got a password wrong", n, maxTrackedAddresses)
	}
}

// And the sweep is what makes room, so an address that has gone quiet for
// longer than the window frees its place instead of blocking a new one.
func TestAQuietAddressFreesItsPlace(t *testing.T) {
	l := newLimiter()
	old := time.Now().Add(-2 * attemptWindow)
	l.mu.Lock()
	for i := range maxTrackedAddresses {
		l.m[fmt.Sprintf("old-%d", i)] = &attemptRecord{failures: 1, last: old}
	}
	l.mu.Unlock()

	// Past the ceiling, but every record in there is stale: the newcomer must
	// still be tracked, or a table full of ghosts would switch the lockout off.
	for i := 0; i <= freeAttempts; i++ {
		l.fail("192.168.1.40")
	}
	if d := l.retryAfter("192.168.1.40"); d <= 0 {
		t.Error("a new address got no lockout with the table full of records " +
			"nobody has used for an hour")
	}
}

// **A full table refuses newcomers and does not evict whoever is locked out.**
//
// The other direction looks tidier and is a way in: whoever is serving a
// lockout could clear it by knocking from four thousand other addresses, which
// is precisely the caller this table exists to slow down.
func TestAFullTableDoesNotClearSomebodyElsesLockout(t *testing.T) {
	l := newLimiter()
	for i := 0; i <= freeAttempts; i++ {
		l.fail("192.168.1.40")
	}
	locked := l.retryAfter("192.168.1.40")
	if locked <= 0 {
		t.Fatal("the address was not locked out to begin with")
	}
	for i := range maxTrackedAddresses * 2 {
		l.fail(fmt.Sprintf("198.51.100.%d:%d", i%256, i))
	}
	if d := l.retryAfter("192.168.1.40"); d <= 0 {
		t.Error("the lockout was evicted to make room: knocking from enough " +
			"addresses now clears one's own")
	}
}
