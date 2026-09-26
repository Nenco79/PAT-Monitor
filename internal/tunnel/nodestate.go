package tunnel

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

// moveTheNode returns the folder tsnet keeps the node in, carrying it over from
// where it used to be the first time.
//
// **The node's private keys lived in the roaming profile**, beside the
// configuration in %APPDATA%. On a domain with roaming profiles or a
// redirected folder that one is copied to a file server at every logoff and
// back onto every machine the user signs into — the identity of a node, which
// is meant to be one machine, travelling to the others. The configuration is
// preferences and may roam; the identity may not, so it goes to the local
// folder, which never leaves the machine.
//
// **It is moved, never created afresh.** A new identity is a new node in the
// tailnet, and the public name is taken by the old one: the bookmark on the
// watcher's phone would stop answering, which is the migration the names
// chapter says a hostname change is. So an identity that is there is renamed
// across, and one that cannot be — another copy of the monitor holding it, a
// rename refused — is used where it is, this time, and said.
//
// rename is handed in so that both directions can be tested on a temporary
// folder instead of on the profile of whoever runs the tests.
func moveTheNode(former, current string, rename func(from, to string) error, log *slog.Logger) string {
	dir := filepath.Join(current, "tsnet")
	if former == "" {
		return dir
	}
	old := filepath.Join(former, "tsnet")
	if _, err := os.Stat(old); err != nil {
		return dir // nothing to carry over
	}
	if _, err := os.Stat(dir); err == nil {
		// Both are there: the new one is what the last start used, so the old
		// one is a leftover and is left alone rather than guessed about.
		log.Warn("an old copy of the Tailscale node's identity is still in the roaming profile",
			"old", old, "in_use", dir)
		return dir
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Warn("the Tailscale node's folder cannot be read, the old one is used",
			"folder", dir, "error", err)
		return old
	}
	if err := os.MkdirAll(current, 0o700); err != nil {
		log.Warn("the Tailscale node's identity could not be moved, the old folder is used",
			"from", old, "to", dir, "error", err)
		return old
	}
	if err := rename(old, dir); err != nil {
		log.Warn("the Tailscale node's identity could not be moved, the old folder is used",
			"from", old, "to", dir, "error", err)
		return old
	}
	log.Info("the Tailscale node's identity moved out of the roaming profile", "from", old, "to", dir)
	return dir
}
