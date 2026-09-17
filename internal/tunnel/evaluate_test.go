package tunnel

import (
	"io"
	"log/slog"
	"testing"

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
