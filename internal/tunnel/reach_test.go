package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// **The reachability check exists so as not to believe an answer.**
//
// The tunnel said "active" three seconds after start, with a valid certificate,
// an ingress grant and an accepted serve config — three answers — while from
// outside the address stayed unfindable for minutes, because the public name
// was not in DNS yet. These tests watch the piece that turns those answers into
// a measured effect, and each one crosses a branch that, wrong, would produce a
// false alarm or a false all-clear.

func testTunnel(t *testing.T) *Tunnel {
	t.Helper()
	return New(Config{
		Hostname: "patmon-test",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// viaFunnel marks the request as having arrived from the Internet, which is
// what `withFunnelSource` does on real connections: the address is put in the
// connection by Tailscale, not in a header.
func viaFunnel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithSourceAddr(r.Context(), netip.MustParseAddrPort("203.0.113.7:44321"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ordinaryPage stands in for the monitor: if it gets called, the probe was not
// intercepted.
func ordinaryPage(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusSeeOther)
	})
}

// A probe that comes back in through the funnel and finds itself is the only
// one that verifies.
func TestTheProbeOnlyPassesWhenItComesBackThroughTheFunnel(t *testing.T) {
	tu := testTunnel(t)
	var reachedMonitor bool
	srv := httptest.NewServer(viaFunnel(tu.serveReach(ordinaryPage(&reachedMonitor))))
	defer srv.Close()

	code, err := tu.probeReach(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if code != ReachVerified {
		t.Errorf("outcome %q, wanted %q", code, ReachVerified)
	}
	if reachedMonitor {
		t.Error("the probe reached the monitor instead of being intercepted")
	}
}

// **Reaching ourselves by the inside route is not a verification, and not a
// fault either.**
//
// It is the case of a machine where the Tailscale client is also installed:
// MagicDNS resolves the public name to the tailnet address, the request arrives
// all the same and has crossed nothing. Without this branch such a machine —
// the developer's own — would declare itself unreachable with a perfectly
// working funnel, which is the most likely false alarm in the whole mechanism.
func TestReachingOurselvesOverTheTailnetIsNotAFailure(t *testing.T) {
	tu := testTunnel(t)
	var reachedMonitor bool
	srv := httptest.NewServer(tu.serveReach(ordinaryPage(&reachedMonitor))) // no viaFunnel
	defer srv.Close()

	code, err := tu.probeReach(context.Background(), srv.URL)
	if !errors.Is(err, errReachDirect) {
		t.Fatalf("error %v, wanted errReachDirect", err)
	}
	if code != ReachUnknown {
		t.Errorf("outcome %q, wanted %q: \"not checkable\" is not \"does not work\"",
			code, ReachUnknown)
	}
}

// Somebody answering in our place does not verify us.
func TestSomeoneElseAnsweringIsNotAVerification(t *testing.T) {
	tu := testTunnel(t)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello from a captive portal")
		}))
	defer srv.Close()

	code, _ := tu.probeReach(context.Background(), srv.URL)
	if code != ReachFailed {
		t.Errorf("outcome %q, wanted %q", code, ReachFailed)
	}
}

// A wrongly guessed nonce sees what anybody would see.
//
// There is no secret to protect — the public address already declares by itself
// that there is a monitor here — but a route that answers differently to a
// guessed path is one more detail handed to whoever is probing.
func TestAWrongNonceSeesTheOrdinaryPage(t *testing.T) {
	tu := testTunnel(t)
	tu.reachNonce.Store("the-right-one")

	var reachedMonitor bool
	srv := httptest.NewServer(viaFunnel(tu.serveReach(ordinaryPage(&reachedMonitor))))
	defer srv.Close()

	resp, err := http.Get(srv.URL + reachPath + "the-wrong-one")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !reachedMonitor {
		t.Error("a wrong nonce was treated specially")
	}
}

// And before the nonce exists, the route is indistinguishable from the rest.
//
// It is the case of the first instant: the tunnel is already serving and no
// probe has gone out. Without the check on the empty nonce, a request with an
// empty path would get an answer that ought not to exist.
func TestBeforeAnyProbeThePathIsNotSpecial(t *testing.T) {
	tu := testTunnel(t)
	var reachedMonitor bool
	srv := httptest.NewServer(viaFunnel(tu.serveReach(ordinaryPage(&reachedMonitor))))
	defer srv.Close()

	resp, err := http.Get(srv.URL + reachPath + "anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !reachedMonitor {
		t.Error("with no nonce in memory the route answered all the same")
	}
}

// **Inside the grace a failure is not announced.**
//
// It is the branch that decides whether the mechanism helps or does harm: the
// public name appears in DNS minutes after start — more than ten, measured — so
// the first attempts fail by construction. Declaring "does not answer" there
// would mean an alarm guaranteed on every start, that is, an alarm nobody reads
// any more.
func TestAFailureInsideTheGraceIsNotAnAlarm(t *testing.T) {
	failed := errors.New("no such host")

	cases := []struct {
		name       string
		code       ReachCode
		err        error
		sinceStart time.Duration
		fails      int
		want       ReachCode
	}{
		{"just started", ReachFailed, failed, 30 * time.Second, 1, ReachChecking},
		{"a moment before the grace ends", ReachFailed, failed,
			reachGrace - time.Second, 1, ReachChecking},
		// **Past the grace a confirmation is still wanted.** One attempt gone
		// wrong is also a home network flickering for two seconds, and with the
		// slow recheck it would nail the alarm up for ten minutes.
		{"past the grace, first stumble", ReachFailed, failed,
			reachGrace + time.Second, 1, ReachChecking},
		{"past the grace, confirmed", ReachFailed, failed,
			reachGrace + time.Second, reachFailsToConfirm, ReachFailed},
		{"verified straight away", ReachVerified, nil, time.Second, 0, ReachVerified},
		{"reached from inside, after the grace", ReachUnknown, errReachDirect,
			time.Hour, 0, ReachUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reachVerdict(c.code, c.err, c.sinceStart, c.fails); got != c.want {
				t.Errorf("verdict %q, wanted %q", got, c.want)
			}
		})
	}
}

// **The grace has to cover the window in which the name is not published yet.**
//
// The number is not a matter of taste: on a node switched back on after a day
// off, `patmon-1` was **absent** from public DNS at +5 minutes from start and
// **present** at +11. A grace shorter than that does not delay an alarm: **it
// manufactures one**, on every slow cold start, and with it a banner, a chime
// and an amber icon over a tunnel that is about to work.
//
// Six minutes is half the worst case observed, and the file itself carries the
// measurement that says so three paragraphs above. **A measurement written next
// to a constant that contradicts it protects nothing**: this test ties the two
// together, so lowering the constant fails instead of looking like tuning.
func TestTheGraceCoversTheMeasuredPublicationWindow(t *testing.T) {
	// The worst case observed: present at +11 minutes.
	const measuredWindow = 11 * time.Minute

	if reachGrace <= measuredWindow {
		t.Errorf("grace %v against a measured window of %v: every slow cold "+
			"start would declare a healthy tunnel unreachable",
			reachGrace, measuredWindow)
	}
	// And the confirmation must not come so late that the alarm is useless: the
	// second attempt falls within a couple of minutes of the first.
	if reachMaxWait > 5*time.Minute {
		t.Errorf("longest wait %v: the confirmation would arrive too late for "+
			"the alarm to be worth anything", reachMaxWait)
	}
}

// **The wait is declared, and it is not silence.**
//
// The minutes between the tunnel opening and the first successful probe used to
// be ReachUnknown, and the badge fell back on the phase, which says "active" —
// that is, in the one window where the address does not work the monitor
// declared it ready. The optimistic claim survived, moved one step along.
//
// The one exception is the machine where the check cannot be made: there
// "checking" would promise an outcome that is never coming.
func TestTheWaitIsDeclaredInsteadOfLookingLikeSilence(t *testing.T) {
	failed := errors.New("no such host")

	if got := reachVerdict(ReachFailed, failed, time.Second, 1); got != ReachChecking {
		t.Errorf("just after start the outcome is %q: the viewer would see the "+
			"phase, which says \"active\" exactly while the address does not answer", got)
	}
	if got := reachVerdict(ReachUnknown, errReachDirect, time.Second, 0); got != ReachUnknown {
		t.Errorf("where the check cannot be made the outcome is %q: that would "+
			"be a verification promised and never delivered", got)
	}
}

// And the wait does not reach the log.
//
// The log is reread by whoever is looking, in the morning, for what happened,
// and a wait resolved in eleven seconds is nothing happening — while it runs on
// every start.
func TestOnlyTheTwoOutcomesReachTheLog(t *testing.T) {
	var lines int
	tu := New(Config{Log: slog.New(countLines{&lines})})
	// The outcome is only written on an open tunnel: without this line the
	// guard in setReach would refuse it, and the count would say zero for the
	// wrong reason.
	tu.setState(State{Phase: PhaseRunning, PublicURL: "https://example.ts.net"})
	lines = 0

	tu.setReach(ReachChecking, "https://example.ts.net")
	tu.setReach(ReachUnknown, "https://example.ts.net")
	if lines != 0 {
		t.Errorf("%d lines for a wait and an \"I do not know\": the log is not "+
			"where one looks for what is happening right now", lines)
	}
	tu.setReach(ReachVerified, "https://example.ts.net")
	if lines != 1 {
		t.Errorf("%d lines after a successful verification, wanted 1", lines)
	}
}

// **The outcome is not written on a tunnel that is no longer open.**
//
// The check lives in a goroutine that `Run` launches and does not wait for: on
// the way out of a server fault — where `fail` writes PhaseError — it went on
// interrogating the public address and depositing its outcome over a state that
// describes something else. The badge shows `reach` **instead of** the phase, so
// the viewer read "does not answer from outside" rather than the real error: the
// diagnosis replaced by its least useful symptom.
//
// The child context closes the goroutine, but the guard is here because
// cancelling a context still lets the round already begun finish — and a
// microsecond race against a `defer` is what gets lost at the first reshuffle.
func TestTheOutcomeNeverLandsOnAStateThatIsNoLongerRunning(t *testing.T) {
	tu := testTunnel(t)
	tu.fail(StepFunnelServe, errors.New("serve: closed"))

	tu.setReach(ReachFailed, "https://example.ts.net")

	st := tu.State()
	if st.Reach != ReachUnknown {
		t.Errorf("outcome %q deposited on phase %q: the badge would say "+
			"\"does not answer from outside\" instead of the error", st.Reach, st.Phase)
	}
	if st.Phase != PhaseError {
		t.Errorf("phase %q, wanted %q", st.Phase, PhaseError)
	}
}

// The outcome is logged when it changes, not while it lasts.
//
// It is the shape of the alerts, and here the reason is the same: the check
// repeats all night, and one line per attempt would be the volume of the log
// decided by a timer.
func TestTheOutcomeIsLoggedOnlyWhenItChanges(t *testing.T) {
	var lines int
	tu := New(Config{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	tu.cfg.Log = slog.New(countLines{&lines})
	// The outcome is only written on an open tunnel: without this line the
	// guard in setReach would refuse it, and the count would say zero for the
	// wrong reason.
	tu.setState(State{Phase: PhaseRunning, PublicURL: "https://example.ts.net"})
	lines = 0

	tu.setReach(ReachVerified, "https://example.ts.net")
	tu.setReach(ReachVerified, "https://example.ts.net")
	tu.setReach(ReachVerified, "https://example.ts.net")
	if lines != 1 {
		t.Errorf("%d lines for the same outcome repeated, wanted 1", lines)
	}
	tu.setReach(ReachFailed, "https://example.ts.net")
	if lines != 2 {
		t.Errorf("%d lines after a change of outcome, wanted 2", lines)
	}
}

// **The outcome does not touch the rest of the state.**
//
// setReach deliberately does not go through setState: that replaces the whole
// state, and called from the probe's goroutine it would throw away the phase,
// the public address and the warning gathered when the tunnel opened — that is,
// the remote-access panel would go empty at the instant the verification
// succeeds.
func TestTheOutcomeDoesNotOverwriteTheRestOfTheState(t *testing.T) {
	tu := testTunnel(t)
	tu.setState(State{
		Phase:     PhaseRunning,
		PublicURL: "https://example.ts.net",
		Warning:   WarningNoIngress,
	})

	tu.setReach(ReachVerified, "https://example.ts.net")

	st := tu.State()
	if st.Phase != PhaseRunning || st.PublicURL != "https://example.ts.net" ||
		st.Warning != WarningNoIngress {
		t.Errorf("the state was overwritten: %+v", st)
	}
	if st.Reach != ReachVerified {
		t.Errorf("outcome %q, wanted %q", st.Reach, ReachVerified)
	}
}

// countLines is an slog handler that counts records without writing them.
type countLines struct{ n *int }

func (c countLines) Enabled(context.Context, slog.Level) bool { return true }
func (c countLines) Handle(context.Context, slog.Record) error {
	*c.n++
	return nil
}
func (c countLines) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c countLines) WithGroup(string) slog.Handler      { return c }
