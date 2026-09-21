package main

import (
	"strings"
	"testing"
)

// **Inside a package the question is not asked, whatever the configuration
// says.** The updating there belongs to the Store, and a build that also asked
// GitHub would offer somebody a download it must not install: the remedy for a
// packaged program is a store page and not a file.
//
// **It is the stamp and not a default that decides**, which is the case the
// second half covers: a configuration file written before any of this exists
// carries `update_check: true` straight past a changed default, so the answer
// cannot come from the key alone.
//
// **Verified to catch**: with `packaged` dropped from the predicate, the two
// packaged cases fail.
func TestAPackagedBuildNeverAsksGitHub(t *testing.T) {
	for _, c := range []struct {
		wanted, packaged, asks bool
	}{
		{wanted: true, packaged: false, asks: true},
		{wanted: false, packaged: false, asks: false},
		{wanted: true, packaged: true, asks: false},
		{wanted: false, packaged: true, asks: false},
	} {
		if got := asksAboutUpdates(c.wanted, c.packaged); got != c.asks {
			t.Errorf("wanted=%v packaged=%v: asks=%v, wanted %v",
				c.wanted, c.packaged, got, c.asks)
		}
	}
}

// **And the line that reports it says the same thing the code does.** `off`
// printed over a configuration that reads `true` is a line somebody reads as a
// defect and goes looking for; the two are one predicate away from each other
// so that a change to one cannot leave the other announcing a check that no
// longer happens.
func TestTheReportedLineNamesThePackageAsTheReason(t *testing.T) {
	packaged := updateCheckLabel(true, true)
	if strings.Contains(packaged, "on") && !strings.Contains(packaged, "off") {
		t.Errorf("packaged build reports %q, and no request goes out", packaged)
	}
	if !strings.Contains(strings.ToLower(packaged), "store") {
		t.Errorf("packaged build reports %q, which does not say why", packaged)
	}

	// And outside a package the key is what it has always been.
	if on := updateCheckLabel(true, false); !strings.Contains(on, "on") {
		t.Errorf("unpackaged build with the check on reports %q", on)
	}
	if off := updateCheckLabel(false, false); !strings.Contains(off, "off") {
		t.Errorf("unpackaged build with the check off reports %q", off)
	}
}
