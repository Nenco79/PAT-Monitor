package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// **The fault this test guards against: "authorise this device" said to
// somebody who already has.**
//
// evaluate compared the backend state against Running alone, so every other
// situation fell into the same branch. Live, that meant a device that appeared
// in the Tailscale list, stayed off because the tailnet requires manual
// approval, and a path that went on asking for it to be authorised — that is,
// it sent the reader looking for a fault in the program instead of in the
// tailnet settings.
func TestEveryBackendStateSaysItsOwnThing(t *testing.T) {
	cases := []struct {
		state ipn.State
		want  Phase
	}{
		{ipn.NoState, PhaseNeedsLogin},
		{ipn.NeedsLogin, PhaseNeedsLogin},
		{ipn.Stopped, PhaseNeedsLogin},
		{ipn.NeedsMachineAuth, PhaseNeedsApproval},
		{ipn.Starting, PhaseStarting},
		{ipn.InUseOtherUser, PhaseError},
	}
	tun := New(Config{Hostname: "test"})
	for _, c := range cases {
		st := &ipnstate.Status{BackendState: c.state.String(), AuthURL: "https://example/a/1"}
		got, ready := tun.evaluate(t.Context(), nil, st)
		if ready {
			t.Errorf("%s: declared ready", c.state)
		}
		if got.Phase != c.want {
			t.Errorf("%s: phase %q, wanted %q", c.state, got.Phase, c.want)
		}
	}
}

// Whoever is waiting for approval must not be handed the authorisation address:
// it has already been used, and offering it again invites them to redo the
// thing that worked. What they need is the device list, which is where the
// action is.
func TestWaitingForApprovalPointsAtTheDeviceList(t *testing.T) {
	tun := New(Config{Hostname: "test"})
	st := &ipnstate.Status{
		BackendState: ipn.NeedsMachineAuth.String(),
		AuthURL:      "https://login.tailscale.com/a/1a2b3c",
	}
	got, _ := tun.evaluate(t.Context(), nil, st)
	if got.ActionURL == st.AuthURL {
		t.Error("offered the authorisation address to somebody who has already authorised")
	}
	if got.ActionURL == "" || got.Action == "" {
		t.Errorf("state with nothing to do: %+v", got)
	}
}

// Starting is transient: it asks nothing of anybody. An instruction that
// appears and vanishes in two seconds is worse than no instruction.
func TestStartingAsksForNothing(t *testing.T) {
	tun := New(Config{Hostname: "test"})
	st := &ipnstate.Status{BackendState: ipn.Starting.String(), AuthURL: "https://example/a/1"}
	got, _ := tun.evaluate(t.Context(), nil, st)
	if got.Action != "" || got.ActionURL != "" {
		t.Errorf("starting asks for something: %+v", got)
	}
}

// The real name is in the public address, which comes from the node: the one in
// the configuration is only what we asked for.
func TestFirstLabel(t *testing.T) {
	cases := map[string]string{
		"https://patmon-1.quercia-lieve.ts.net": "patmon-1",
		"https://patmon.quercia-lieve.ts.net":   "patmon",
		"https://nodots":                        "",
		"":                                      "",
	}
	for url, want := range cases {
		if got := firstLabel(url); got != want {
			t.Errorf("from %q we get %q instead of %q", url, got, want)
		}
	}
}

// If the tailnet renames the node it gets said **and** recorded. Without the
// second half the losing request repeats on every start, and the day the short
// name came free the public address would change by itself again.
func TestARenamedNodeIsBothAnnouncedAndRecorded(t *testing.T) {
	var seen string
	tn := New(Config{
		Hostname:   "patmon",
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnHostname: func(n string) { seen = n },
	})

	tn.checkHostname("https://patmon-1.quercia-lieve.ts.net")
	if seen != "patmon-1" {
		t.Errorf("the assigned name was not recorded: %q", seen)
	}

	// And when it agrees nothing is touched: an alert that always sounds stops
	// being read.
	seen = ""
	tn.checkHostname("https://patmon.quercia-lieve.ts.net")
	if seen != "" {
		t.Errorf("callback invoked for an identical name: %q", seen)
	}
}

// **QueryFeature is not asked every two seconds.** It goes to Tailscale's
// control server, and while the funnel attribute is missing evaluate runs on
// every tick: a tailnet left without it for a day was forty thousand requests
// with the same answer.
//
// **The defect was put back and this test fails with it**: with the query
// asked unconditionally, a minute of ticks asks it thirty times.
func TestTheFunnelQuestionIsNotAskedEveryTick(t *testing.T) {
	tun := New(Config{Hostname: "test"})
	asked := 0
	tun.query = func(context.Context, *local.Client, string) (string, string, bool) {
		asked++
		return "ask your admin", "https://login.tailscale.com/admin", false
	}
	st := &ipnstate.Status{BackendState: ipn.Running.String()}

	for range 30 { // a minute of two-second ticks
		got, ready := tun.evaluate(t.Context(), nil, st)
		if ready || got.Phase != PhaseNeedsFunnel || got.ActionText != "ask your admin" {
			t.Fatalf("the remembered answer was not given: %+v", got)
		}
	}
	if asked != 1 {
		t.Errorf("QueryFeature asked %d times in a minute, wanted once", asked)
	}

	// Its time come, it is asked again, and a "done" ends the wait at once.
	tun.funnel.asked = time.Now().Add(-time.Hour)
	tun.query = func(context.Context, *local.Client, string) (string, string, bool) {
		asked++
		return "", "", true
	}
	if _, ready := tun.evaluate(t.Context(), nil, st); !ready || asked != 2 {
		t.Errorf("a due question was not asked again, or its done was lost: ready=%v asked=%d", ready, asked)
	}
}

// **A failure that repeats is written once.** waitReady reads the status every
// two seconds, and a read that keeps failing wrote an Error line on every tick.
//
// **The defect was put back and this test fails with it**: with fail writing
// unconditionally, thirty ticks write thirty Error lines.
func TestAFailureThatRepeatsIsWrittenOnce(t *testing.T) {
	var buf bytes.Buffer
	tun := New(Config{Hostname: "test",
		Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))})

	for range 30 {
		tun.fail(StepStatusRead, errors.New("localapi: unavailable"))
	}
	if n := strings.Count(buf.String(), "level=ERROR"); n != 1 {
		t.Errorf("30 identical failures wrote %d Error lines, 1 is wanted", n)
	}
	// A different failure is news.
	tun.fail(StepStatusRead, errors.New("localapi: something else"))
	if n := strings.Count(buf.String(), "level=ERROR"); n != 2 {
		t.Errorf("a different failure was not written: %d Error lines", n)
	}
}
