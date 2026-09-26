package tunnel

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// **The node's identity moves out of the roaming profile, and is never created
// afresh.** A new identity is a new node, and the public address stays with the
// old one: the watcher's bookmark stops answering. So what is there is carried
// over, and what cannot be is used where it is.
//
// **The defect was put back and this test fails with it**: with moveTheNode
// handing back the new folder and carrying nothing over, the first case finds
// the identity still in the roaming folder, and the second a fresh one where
// the old could not move.
func TestTheNodeIsMovedNotReplaced(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	identity := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "tsnet"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tsnet", "tailscaled.state"), []byte("the node"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	holds := func(dir string) bool {
		b, err := os.ReadFile(filepath.Join(dir, "tailscaled.state"))
		return err == nil && string(b) == "the node"
	}

	t.Run("carried over the first time", func(t *testing.T) {
		roaming, local := t.TempDir(), filepath.Join(t.TempDir(), "PAT Monitor")
		identity(t, roaming)
		got := moveTheNode(roaming, local, os.Rename, quiet)
		if got != filepath.Join(local, "tsnet") || !holds(got) {
			t.Errorf("the identity is not in the local folder: %s", got)
		}
		if _, err := os.Stat(filepath.Join(roaming, "tsnet")); err == nil {
			t.Error("the identity was left behind in the roaming folder as well")
		}
	})

	t.Run("used where it is when it cannot move", func(t *testing.T) {
		roaming, local := t.TempDir(), filepath.Join(t.TempDir(), "PAT Monitor")
		identity(t, roaming)
		refuse := func(string, string) error { return errors.New("in use") }
		got := moveTheNode(roaming, local, refuse, quiet)
		if got != filepath.Join(roaming, "tsnet") || !holds(got) {
			t.Errorf("a refused move did not fall back to the old identity: %s", got)
		}
	})

	t.Run("nothing to carry", func(t *testing.T) {
		roaming, local := t.TempDir(), filepath.Join(t.TempDir(), "PAT Monitor")
		if got := moveTheNode(roaming, local, os.Rename, quiet); got != filepath.Join(local, "tsnet") {
			t.Errorf("a first start does not use the local folder: %s", got)
		}
	})

	t.Run("both there", func(t *testing.T) {
		roaming, local := t.TempDir(), t.TempDir()
		identity(t, roaming)
		identity(t, local)
		if got := moveTheNode(roaming, local, os.Rename, quiet); got != filepath.Join(local, "tsnet") {
			t.Errorf("with both there the one in use was not kept: %s", got)
		}
		if _, err := os.Stat(filepath.Join(roaming, "tsnet", "tailscaled.state")); err != nil {
			t.Error("the leftover was touched")
		}
	})

	t.Run("nothing has moved", func(t *testing.T) {
		here := t.TempDir()
		identity(t, here)
		if got := moveTheNode("", here, os.Rename, quiet); got != filepath.Join(here, "tsnet") {
			t.Errorf("with no former folder the state dir is not used as it is: %s", got)
		}
	})
}
