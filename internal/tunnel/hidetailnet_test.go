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
