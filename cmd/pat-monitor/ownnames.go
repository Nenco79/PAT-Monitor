package main

import (
	"net/url"
	"strings"
)

// ownNames composes the names this PC answers to on the house's network and on
// the tailnet, for server.Options.OwnNames.
//
// **Every piece is the system's answer and none is guessed**, because a name
// missing from here is a person refused at login for typing their own
// computer's name: `computer` is what Windows calls the machine, `suffixes` the
// DNS suffixes its network adapters were handed, `nodeURL` and `nodeLabel` the
// node's MagicDNS name as the tunnel knows it. The short names are then written
// under `.local`, which is mDNS, and under each suffix, which is how a router
// names the devices it serves: `desktop.fritz.box`, `desktop.lan`.
//
// It asks nothing itself, so both directions can be tested with the answers in
// hand.
func ownNames(computer, suffixes []string, nodeURL, nodeLabel string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.TrimSuffix(strings.TrimSpace(n), ".")
		if n == "" || seen[strings.ToLower(n)] {
			return
		}
		seen[strings.ToLower(n)] = true
		out = append(out, n)
	}

	for _, c := range computer {
		add(c)
	}
	for _, c := range computer {
		if c == "" || strings.Contains(c, ".") {
			continue
		}
		add(c + ".local")
		for _, s := range suffixes {
			if s = strings.Trim(strings.TrimSpace(s), "."); s != "" {
				add(c + "." + s)
			}
		}
	}

	if u, err := url.Parse(nodeURL); err == nil && u.Hostname() != "" {
		add(u.Hostname())
		if label, _, ok := strings.Cut(u.Hostname(), "."); ok {
			add(label)
		}
	}
	add(nodeLabel)
	return out
}
