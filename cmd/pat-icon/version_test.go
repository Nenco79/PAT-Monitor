package main

import (
	"strings"
	"testing"

	"patmonitor/internal/version"
)

// **The description leads with the name, because it has a reader nobody
// planned for.**
//
// That field was born for the file's details sheet — whoever redistributes, the
// tools that scan executables — and Windows uses it for something else too: it
// puts it **at the top of the tray notifications**, where whoever is looking
// expects the program's name. With a sentence starting "Pet and baby monitor:
// webcam and microphone…", the notice that remote access is open arrives under
// the heading of a brochure.
//
// This is not a test on the wording: the sentence can be rewritten whenever.
// It is a test on **where it starts**, which is the only part that second
// reader sees.
func TestTheDescriptionLeadsWithTheProductName(t *testing.T) {
	v := versionInfo()
	if !strings.HasPrefix(v.Description, version.Product) {
		t.Errorf("the description is %q: Windows shows it at the top of the "+
			"notifications, and there it has to start with %q", v.Description, version.Product)
	}
	// And the name is taken from version, not rewritten: two copies diverge at
	// the first rename, and this is the one nobody re-reads.
	if v.Product != version.Product {
		t.Errorf("product %q instead of %q", v.Product, version.Product)
	}
}
