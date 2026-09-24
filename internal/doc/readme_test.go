package doc

import (
	"os"
	"strings"
	"testing"
)

// The README's source layout is a hand-written list of the tree, and it had
// drifted: `internal/ced`, `internal/gguf`, `internal/guard`, `internal/resample`
// and `internal/doc` were all missing, that is, the sound recogniser that the
// features list advertises three sections higher, and the package that keeps a
// panic from switching the camera off.
//
// **Nothing said so, and nothing could.** The configuration table has a guard of
// its own in `internal/config`, which is why that one is complete; this list had
// none, so it aged the way a generated-and-committed file ages — the repository
// describing a tree it no longer has, with everything green. It is the same
// shape as the licence folder, and the remedy is the same: the list is kept
// because a reader wants it, and it is watched because it cannot keep itself.
//
// The two directions are not equally likely and both are here. A package added
// and not listed is the one that happens, because whoever adds it is looking at
// the code; a package listed and removed is the rarer one, and it is not
// dangerous, it is **false** — the README promises a place in the tree that is
// not there.

const readmePath = "../../README.md"

func readREADME(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("README.md cannot be read: %v", err)
	}
	return string(b)
}

// layout is the set of packages the source layout table names. It reads that
// section alone: `internal/i18n/catalogs/` is mentioned in the prose underneath
// and naming a package in a sentence is not listing it in the table.
func layout(t *testing.T) map[string]bool {
	t.Helper()

	md := readREADME(t)
	_, after, ok := strings.Cut(md, "## Source layout")
	if !ok {
		t.Fatal("there is no source layout section: this test is looking in the wrong place")
	}
	body, _, _ := strings.Cut(after, "\n## ")

	out := make(map[string]bool)
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		rest := line
		for {
			_, after, ok := strings.Cut(rest, "`internal/")
			if !ok {
				break
			}
			name, tail, ok := strings.Cut(after, "`")
			if !ok {
				break
			}
			out["internal/"+name] = true
			rest = tail
		}
	}
	if len(out) == 0 {
		t.Fatal("no package read from the table: this test is looking at nothing")
	}
	return out
}

func TestTheSourceLayoutNamesEveryPackage(t *testing.T) {
	listed := layout(t)

	dirs, err := os.ReadDir("../")
	if err != nil {
		t.Fatalf("internal cannot be listed: %v", err)
	}
	onDisk := make(map[string]bool)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		pkg := "internal/" + d.Name()
		onDisk[pkg] = true
		if !listed[pkg] {
			t.Errorf("%s is a package and the README's source layout does not name it", pkg)
		}
	}
	for pkg := range listed {
		if !onDisk[pkg] {
			t.Errorf("the README's source layout names %s, which is not in the tree", pkg)
		}
	}
	if len(onDisk) < 20 {
		t.Fatalf("%d packages read: this test is looking at nothing", len(onDisk))
	}
}

// The tools are not in that table — they are described where somebody would go
// looking for them, which for `pat-diag` is the diagnostics section and for
// `pat-sign` is the sentence about running things from source. So what is asked
// of them is weaker and is the thing that matters: **a command that exists and
// is written down nowhere is a command nobody runs.**
func TestEveryCommandIsNamedSomewhereInTheREADME(t *testing.T) {
	md := readREADME(t)

	dirs, err := os.ReadDir("../../cmd")
	if err != nil {
		t.Fatalf("cmd cannot be listed: %v", err)
	}
	checked := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		checked++
		if !strings.Contains(md, d.Name()) {
			t.Errorf("cmd/%s exists and the README never names it", d.Name())
		}
	}
	if checked < 5 {
		t.Fatalf("%d commands read: this test is looking at nothing", checked)
	}
}
