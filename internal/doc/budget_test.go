package doc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// alwaysBudget and packageBudget are the two ceilings, in words, on what a
// session has to read before it has typed anything. The first is `CLAUDE.md`
// plus the slice with no `paths`, which arrive in every session; the second is
// that plus every slice whose globs cover the directory being worked on, which
// is what opening one file there brings in.
//
// **They are what the condensation left, rounded up, and they only go down.**
// Measured on 2026-09-28 when it ended: 6978 words always loaded, and 53070 for
// internal/server, the heaviest package. A chapter that pushes a package over
// is the question "does this belong in the slice every session in that package
// reads?", not a number to raise.
const (
	alwaysBudget  = 7000
	packageBudget = 53500
)

// words is what a file costs a session, counted the plain way: runs of
// non-space. A table row or a code line costs what its tokens are.
func words(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", path, err)
	}
	return len(strings.Fields(string(b)))
}

// **An instruction nobody can hold is an instruction nobody follows**, and the
// slicing is what keeps the record holdable: this is the test that notices when
// it stops doing so. A chapter added to a slice that half the tree loads, or a
// glob widened to catch one more file, raises what every session in those
// packages starts with, and nothing else would say so.
//
// The matching is `governs`, the same the coverage test uses, so the words
// charged to a package are those of exactly the slices that test calls its
// rules.
func TestNoPackageLoadsMoreThanItsBudget(t *testing.T) {
	always := words(t, claudePath)
	type slice struct {
		name  string
		globs []string
		words int
	}
	var slices []slice
	for _, path := range ruleFiles(t) {
		globs, declared := frontmatter(t, path)
		if !declared {
			always += words(t, path)
			continue
		}
		slices = append(slices, slice{filepath.Base(path), globs, words(t, path)})
	}
	if always > alwaysBudget {
		t.Errorf("every session starts with %d words, over the %d of alwaysBudget", always, alwaysBudget)
	}

	heaviest, most, checked := "", 0, 0
	for _, pkg := range packageDirs(t) {
		total := always
		for _, s := range slices {
			for _, g := range s.globs {
				if governs(g, pkg) {
					total += s.words
					break
				}
			}
		}
		checked++
		if total > most {
			heaviest, most = pkg, total
		}
	}
	if checked < 20 {
		t.Fatalf("%d packages read: this guard is looking at nothing", checked)
	}
	t.Logf("always loaded: %d words; heaviest package: %s, %d words", always, heaviest, most)
	if most > packageBudget {
		t.Errorf("a session in %s starts with %d words, over the %d of packageBudget", heaviest, most, packageBudget)
	}
}
