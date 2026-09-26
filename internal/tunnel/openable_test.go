package tunnel

import (
	"testing"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// **What the tray hands to the shell is an https page on tailscale.com.** The
// action link comes from the control server and the tray opens it with
// ShellExecute, which runs whatever it is given — a UNC path to an executable,
// a `file:` URL, a protocol handler.
//
// **The defect was put back and this test fails with it**: with the login
// address passed through unchecked, the share and the handler reach ActionURL.
func TestOnlyATailscalePageIsHandedToTheShell(t *testing.T) {
	cases := []struct {
		link string
		kept bool
	}{
		{"https://login.tailscale.com/a/1a2b3c", true},
		{"https://tailscale.com/kb/1223/funnel", true},
		{"https://LOGIN.TAILSCALE.COM/a/1", true},
		{`\somewhere.example\share\run.exe`, false},
		{"file:///C:/Windows/System32/calc.exe", false},
		{"search-ms:query=x&crumb=location:\\somewhere.example", false},
		{"http://login.tailscale.com/a/1", false},
		{"https://tailscale.com.somewhere.example/a/1", false},
		{"https://user@login.tailscale.com/a/1", false},
		{"https:login.tailscale.com", false},
		{"", false},
	}
	tun := New(Config{Hostname: "test"})
	for _, c := range cases {
		st := &ipnstate.Status{BackendState: ipn.NeedsLogin.String(), AuthURL: c.link}
		got, _ := tun.evaluate(t.Context(), nil, st)
		if kept := got.ActionURL != ""; kept != c.kept {
			t.Errorf("%q: handed on %v, wanted %v", c.link, kept, c.kept)
		}
	}
}
