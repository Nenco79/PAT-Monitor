package web

import (
	"os"
	"strings"
	"testing"
)

func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := Assets.ReadFile(name)
	if err != nil {
		t.Fatalf("asset %s: %v", name, err)
	}
	return string(b)
}

// **The embed patterns are a list of kinds, and a list protects what somebody
// remembered.** An image or a font dropped in this folder would be on the disk,
// cited by a page, and absent from the binary: the server answers the fallback
// page to its address, which is the silent 404 the asset routes already have a
// chapter about, one step earlier.
//
// So the folder is read and every file in it that is not Go has to be among the
// embedded ones.
//
// **The defect was put back and this test fails with it**: with a `probe.svg`
// beside the pages it names that file, and with `*.css` taken out of the
// pattern it names the four sheets.
func TestEveryFileHereIsEmbedded(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		checked++
		if _, err := Assets.ReadFile(e.Name()); err != nil {
			t.Errorf("%s is in this folder and not in the binary: the pattern on Assets does not name its kind", e.Name())
		}
	}
	if checked < 10 {
		t.Fatalf("%d files read: this guard is looking at nothing", checked)
	}
}
