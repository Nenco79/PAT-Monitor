package tunnel

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// **The tailnet's name does not reach the log at the default level.** The
// tunnel wrote it in the address line, in the certificate's two lines, in the
// serve configuration and inside the probe's errors — the file people attach
// to an issue, carrying DNS tied to an account.
//
// **The defect was put back and this test fails with it**: with New no longer
// wrapping the logger, every line below carries the name whole.
func TestTheTailnetNameStaysOutOfTheLog(t *testing.T) {
	var buf bytes.Buffer
	tn := New(Config{
		Hostname: "patmon-1a2b3c",
		Log:      slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	log := tn.cfg.Log

	url := "https://patmon-1a2b3c.quercia-lieve.ts.net"
	log.Info("remote access active", "url", url)
	log.Info("TLS certificate ready", "domain", "patmon-1a2b3c.quercia-lieve.ts.net")
	log.Info("serve configuration", "config", `{"Web":{"patmon-1a2b3c.quercia-lieve.ts.net:443":{}}}`)
	log.Warn("public address does not answer from the Internet",
		"error", errors.New(`Get "`+url+`/.well-known/x": dial tcp: i/o timeout`))
	log.With("peer", "quercia-lieve.ts.net").Info("bound in advance")
	log.Info("a message naming " + url)

	shown := strings.SplitAfter(buf.String(), "\n")
	for _, line := range shown {
		if strings.Contains(line, "quercia-lieve") {
			t.Errorf("the tailnet reached the log: %s", line)
		}
	}
	// The node's label stays: it says which installation the line is about.
	if !strings.Contains(buf.String(), "patmon-1a2b3c.<tailnet>.ts.net") {
		t.Errorf("the node's label went with the tailnet:\n%s", buf.String())
	}

	// `-v` keeps the whole name, for whoever is diagnosing on purpose.
	buf.Reset()
	log.Debug("session", "url", url)
	if !strings.Contains(buf.String(), "quercia-lieve") {
		t.Error("the debug line lost the name too")
	}
}

// **The account's email does not reach the log at any level.** tsnet writes
// the signed-in account at Debug, so a `-v` log carried the owner's address to
// wherever it was attached; the tailnet's name is kept at Debug for diagnosis,
// the email is not, because nothing is diagnosed with it.
//
// **The defect was put back and this test fails with it**: with Debug passed
// through untouched, the address is in the first line.
func TestTheAccountsEmailStaysOutOfTheLogAtEveryLevel(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(HideTailnet(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	log.Debug("active login: somebody.else@example.com")
	log.Debug("login", "account", "Somebody.Else+tag@mail.example.co.uk")
	log.Info("profile", "error", errors.New("user somebody@example.org is not an admin"))
	log.With("who", "a.b@example.net").Debug("bound in advance")

	for _, line := range strings.SplitAfter(buf.String(), "\n") {
		if strings.Contains(line, "@example") {
			t.Errorf("an email address reached the log: %s", line)
		}
	}
	if strings.Count(buf.String(), "<email>") != 4 {
		t.Errorf("wanted four addresses hidden:\n%s", buf.String())
	}

	// What looks like one and is not stays as it is.
	buf.Reset()
	log.Debug("pion", "at", "go@v1.26", "peer", "user@host")
	if !strings.Contains(buf.String(), "go@v1.26") || !strings.Contains(buf.String(), "user@host") {
		t.Errorf("text that is not an address was hidden:\n%s", buf.String())
	}
}

// **The sign-in link is a key, and the file keeps where it points and not the
// key.** Until somebody opens it, whoever opens it first can take the node
// into their own tailnet, and the log is the file people attach to an issue
// while they are stuck at exactly that step.
//
// **The defect was put back and this test fails with it**: without the
// pattern, the Info line carries the link whole.
func TestTheSignInLinkStaysOutOfTheLog(t *testing.T) {
	var buf bytes.Buffer
	tn := New(Config{
		Hostname: "patmon-1a2b3c",
		Log:      slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	link := "https://login.tailscale.com/a/1a2b3c4d5e6f"
	tn.cfg.Log.Info("remote access", "phase", "needs-login", "open", link)
	tn.cfg.Log.Info("To authenticate, visit:\n\n\t" + link)

	if strings.Contains(buf.String(), "1a2b3c4d5e6f") {
		t.Errorf("the sign-in key reached the log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "login.tailscale.com/a/<hidden>") {
		t.Errorf("the line no longer says where the link points:\n%s", buf.String())
	}

	buf.Reset()
	tn.cfg.Log.Debug("go to", "url", link)
	if !strings.Contains(buf.String(), "1a2b3c4d5e6f") {
		t.Error("the debug line lost the link too")
	}
}
