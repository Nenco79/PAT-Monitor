package doc

import (
	"os"
	"strings"
	"testing"
)

// privacyCheckedTailscale is the tailscale.com version the privacy policy's two
// Tailscale claims were last checked against.
//
// **The policy states what the code we ship does, and two of those facts belong
// to Tailscale's library rather than to us**: that the node built into the
// monitor sends its own diagnostic logs to Tailscale, and that a Funnel visit is
// decrypted only on the PC because the TLS is terminated by the node. The page
// says both in its own words and links Tailscale's documentation for the rest,
// so a change on their site cannot make it false. A new version of their library
// can, and nobody reads a privacy policy when bumping a dependency.
//
// So the bump stops here until somebody has looked. Raising the constant is the
// act of saying so, and the failure message is the list of what to look at.
const privacyCheckedTailscale = "v1.102.4"

// tailscaleInGoMod returns the version go.mod requires for tailscale.com.
func tailscaleInGoMod(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("go.mod cannot be read: %v", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := trimRequire(strings.Fields(line)); len(f) >= 2 && f[0] == "tailscale.com" {
			return f[1]
		}
	}
	// **Not found is a failure, not a pass**: a guard that cannot find what it
	// watches and stays green is the vacuous kind the guards chapter lists.
	t.Fatal("go.mod requires no tailscale.com: the guard has lost what it watches")
	return ""
}

// trimRequire drops a leading "require" from a single-line require directive.
func trimRequire(f []string) []string {
	if len(f) > 0 && f[0] == "require" {
		return f[1:]
	}
	return f
}

func TestThePrivacyPolicyWasCheckedAgainstThisTailscale(t *testing.T) {
	got := tailscaleInGoMod(t)
	if got == privacyCheckedTailscale {
		return
	}
	t.Fatalf("go.mod now requires tailscale.com %s, and docs/privacy.html was checked "+
		"against %s. Before raising privacyCheckedTailscale:\n"+
		"  1. tsnet still uploads its logs to log.tailscale.com (tsnet.go, startLogger), "+
		"and PAT Monitor still does not turn that off: the page says so.\n"+
		"  2. tsnet's ListenFunnel still terminates TLS on the node with its own "+
		"certificate: the page says Funnel visits are decrypted only on the PC.\n"+
		"  3. The two pages the policy links still describe the same thing: "+
		"https://tailscale.com/kb/1011/log-mesh-traffic and "+
		"https://tailscale.com/kb/1223/funnel.\n"+
		"If any of them changed, the policy changes in the same commit.",
		got, privacyCheckedTailscale)
}
