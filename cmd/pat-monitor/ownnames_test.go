package main

import (
	"slices"
	"strings"
	"testing"
)

// The names a person at home types for this PC are composed from what Windows
// and the tunnel answer: the machine's names, the same under `.local` and under
// each suffix a router handed out, and the node's name whole and short.
// Nothing else is let in, since a name added here is a name a rebinding page
// could try to borrow — so the list is asserted exactly, both ways.
func TestThePCsOwnNamesAreComposedFromTheSystemsAnswers(t *testing.T) {
	got := ownNames(
		[]string{"DESKTOP-PC", "desktop-pc", "desktop-pc.corp.example"},
		[]string{"", "fritz.box", "corp.example.", "FRITZ.BOX"},
		"https://patmon-1a2b3c.quercia-lieve.ts.net/",
		"patmon-1a2b3c",
	)
	want := []string{
		"DESKTOP-PC", "desktop-pc.corp.example",
		"DESKTOP-PC.local", "DESKTOP-PC.fritz.box",
		"patmon-1a2b3c.quercia-lieve.ts.net", "patmon-1a2b3c",
	}
	lower := func(s []string) []string {
		out := make([]string, len(s))
		for i, v := range s {
			out[i] = strings.ToLower(v)
		}
		slices.Sort(out)
		return out
	}
	if !slices.Equal(lower(got), lower(want)) {
		t.Errorf("got %q\nwant %q", got, want)
	}

	// With nothing answered there is nothing to let in, and no empty name
	// that would match an empty Host.
	if got := ownNames(nil, []string{"fritz.box"}, "", ""); len(got) != 0 {
		t.Errorf("with no answers: %q", got)
	}
}
