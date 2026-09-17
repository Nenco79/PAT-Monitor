package main

import (
	"io"
	"log/slog"
	"testing"

	"patmonitor/internal/config"
)

// **A badly written ICE entry must not be able to shut everybody out.**
//
// The hub builds one PeerConnection per viewer with this list, so a row
// `NewPeerConnection` refuses is not a missing fallback: it is the monitor
// unreachable, at home and from outside, with one failure per visit and no
// cause written anywhere.
//
// **And the bad case is not the one that comes to mind.** A URL with a typo in
// the scheme is caught by anybody rereading the line; what this exists for is
// that the answer arrives from the consumer, at a moment nobody is watching.
func TestABadICEServerIsDroppedInsteadOfBreakingEveryViewer(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	stun := []string{"stun:stun.l.google.com:19302"}

	for _, c := range []struct {
		name string
		cfg  config.Config
		want int
	}{
		{"one good stun", config.Config{STUNServers: stun}, 1},
		{"several good ones", config.Config{
			STUNServers: append([]string{"stun:stun1.l.google.com:19302"}, stun...),
		}, 2},
		// The row this test exists for. It read `0` for as long as the whole
		// list travelled in one entry: the consumer was asked about all of them
		// together, so a single typo answered for the lot — and the assertion
		// recorded that as the intended outcome, which is the worst thing a test
		// can do with a defect.
		{"a malformed one does not carry the good ones away", config.Config{
			STUNServers: append([]string{"not a url"}, stun...),
		}, 1},
		{"malformed and nothing else", config.Config{
			STUNServers: []string{"stun:"},
		}, 0},
		{"nothing at all", config.Config{}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := iceServers(c.cfg, quiet)
			if len(got) != c.want {
				t.Fatalf("%d servers instead of %d: %v", len(got), c.want, got)
			}
			// Every entry left has to be genuinely valid: counting them is not
			// enough, since if the filter kept the wrong one the number would
			// come out the same.
			for _, s := range got {
				if err := iceServerUsable(s); err != nil {
					t.Errorf("an entry the consumer refuses survived: %v", err)
				}
			}
		})
	}
}

// One URL per entry is the property the filtering rests on, and it is also what
// makes the diagnosis's `stun_usable` a count of servers rather than of rows.
// Grouping them again would leave every test above passing — the good cases are
// the same list either way — and quietly restore both defects.
func TestEveryServerTravelsInAnEntryOfItsOwn(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{STUNServers: []string{
		"stun:stun.l.google.com:19302",
		"stun:stun1.l.google.com:19302",
	}}

	got := iceServers(cfg, quiet)
	if len(got) != 2 {
		t.Fatalf("%d entries for two servers: %v", len(got), got)
	}
	for _, s := range got {
		if len(s.URLs) != 1 {
			t.Errorf("an entry carries %d URLs: %v", len(s.URLs), s.URLs)
		}
	}
}
