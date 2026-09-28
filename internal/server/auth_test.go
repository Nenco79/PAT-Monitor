package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// create generates a new session token for a caller arriving from `from`,
// against the revocation generation now in force. The server always goes
// through createAt, with the generation it read before the hash; the tests
// that are not about revocation have no generation to hold.
func (s *sessionStore) create(from origin) (string, error) {
	return s.createAt(s.current(), from)
}

// retryAfter returns how long is left until the unlock; zero if not locked.
// The server reads retryAfterLocked inside limiter.begin, under the same lock
// as the admission; the tests read it on its own.
func (l *limiter) retryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.retryAfterLocked(key)
}

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

	first, err := s.create(origin{Class: originLocal})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.create(origin{Class: originInternet})
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

// **A login that was verifying while the sessions were revoked does not come
// out of it with a session.** argon2 takes a tenth of a second, and the hash
// it verifies against is read before it starts: a change or a reset landing in
// that window was undone by the create that followed.
//
// **The defect was put back and this test fails with it**: with the generation
// check removed from createAt, the stale login gets its token.
func TestALoginVerifiedAcrossARevocationGetsNoSession(t *testing.T) {
	s := newSessionStore(time.Hour)
	defer s.close()

	gen := s.current()
	s.revokeAll() // the owner changes the password while argon2 runs
	if token, err := s.createAt(gen, anyRoad); err == nil {
		t.Fatalf("a login verified against the old password got a session: %q", token)
	}
	if n := s.count(); n != 0 {
		t.Errorf("%d sessions are open after the revocation", n)
	}
	if _, err := s.createAt(s.current(), anyRoad); err != nil {
		t.Errorf("a login begun after the revocation was refused: %v", err)
	}
}

// **The generation is read inside the hashing slot and before argon2**, and
// checkPassword is the one place that order is written.
//
// Read after the verification, it would be the generation of a password that
// may already have stopped being the password: createAt would compare it with
// itself and hand the stale login its session, which is the case
// TestALoginVerifiedAcrossARevocationGetsNoSession exists for — and that test
// drives the store, not the handler, so it cannot see the handler read the
// count late. Read before the slot, it would move while the login waits in the
// queue, refusing a correct password for a change that happened before argon2
// began. So the source is asked.
//
// **The defect was put back and this test fails with it**: with `gen =` moved
// below VerifyPassword, the order is reported.
func TestThePasswordCheckReadsTheGenerationBeforeTheHash(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "checkPassword" && fn.Recv != nil {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("checkPassword is not in server.go: this guard is looking at nothing")
	}
	first := map[string]token.Pos{}
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			name := calleeName(call.Fun)
			if _, seen := first[name]; !seen {
				first[name] = call.Pos()
			}
		}
		return true
	})
	slot, current, verify := first["hashSlot"], first["current"], first["VerifyPassword"]
	if slot == token.NoPos || current == token.NoPos || verify == token.NoPos {
		t.Fatalf("checkPassword no longer calls hashSlot, current and VerifyPassword "+
			"(found %v, %v, %v): the order this guards has moved somewhere else",
			slot != token.NoPos, current != token.NoPos, verify != token.NoPos)
	}
	if !(slot < current && current < verify) {
		t.Errorf("checkPassword reads the generation at %s, the slot at %s and the "+
			"hash at %s: it must be slot, then generation, then hash",
			fset.Position(current), fset.Position(slot), fset.Position(verify))
	}
}
