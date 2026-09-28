package doc

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// update rewrites the index in `CLAUDE.md` instead of comparing it. **The flag
// is this package's and no other's**, so the command is the one the opening
// marker spells out: `go test ./... -update` hands it to every package, and each
// of the others refuses a flag it does not define.
var update = flag.Bool("update", false, "rewrite the index in CLAUDE.md from the headings")

// The two markers the index lives between. The opening one is also the only
// instruction whoever is about to edit the list by hand will see, so it names
// the command in full.
const (
	indexOpen  = "<!-- index: generated from the headings by go test ./internal/doc -run TestTheIndexIsTheHeadings -update; do not edit -->"
	indexClose = "<!-- end of index -->"
)

// indexWidth is the column an entry below the top level wraps at, the width the
// rest of the file is written to.
const indexWidth = 79

// renderIndex is the index as the headings write it. A top-level chapter is one
// bold line whatever its length, as it always was; below it, two spaces to the
// level, and an entry too long for the width carries on two spaces deeper than
// its dash, so that the nesting still reads at the margin.
func renderIndex(cs []chapter) []string {
	var out []string
	for _, c := range cs {
		if c.title == theIndexOwnChapter {
			continue
		}
		if c.depth == 2 {
			out = append(out, "- **"+c.title+"**")
			continue
		}
		indent := strings.Repeat(" ", 2*(c.depth-2))
		words := strings.Fields(c.title)
		line := indent + "- " + words[0]
		for _, w := range words[1:] {
			if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= indexWidth {
				line += " " + w
				continue
			}
			out = append(out, line)
			line = indent + "  " + w
		}
		out = append(out, line)
	}
	return out
}

// indexBlock finds the markers in `CLAUDE.md` and returns the lines after the
// opening one up to the closing one, as indices into lines. The markers must be
// there once each, in that order, and inside the index's own chapter: a block
// anywhere else would be regenerated faithfully in a place nobody reads as the
// index.
func indexBlock(lines []string) (from, to int, err error) {
	var opens, closes []int
	chapterAt, nextAt := -1, len(lines)
	for i, line := range lines {
		switch {
		case line == indexOpen:
			opens = append(opens, i)
		case line == indexClose:
			closes = append(closes, i)
		case line == "## "+theIndexOwnChapter:
			chapterAt = i
		case strings.HasPrefix(line, "## ") && chapterAt >= 0 && nextAt == len(lines):
			nextAt = i
		}
	}
	switch {
	case chapterAt < 0:
		return 0, 0, fmt.Errorf("the chapter %q is not there: this test is looking in the wrong place", theIndexOwnChapter)
	case len(opens) != 1 || len(closes) != 1:
		return 0, 0, fmt.Errorf("CLAUDE.md carries %d opening markers and %d closing ones, and each must be there once", len(opens), len(closes))
	case opens[0] > closes[0]:
		return 0, 0, fmt.Errorf("the closing marker, at line %d, comes before the opening one, at line %d", closes[0]+1, opens[0]+1)
	case opens[0] < chapterAt || closes[0] > nextAt:
		return 0, 0, fmt.Errorf("the markers, at lines %d and %d, are not inside the chapter %q", opens[0]+1, closes[0]+1, theIndexOwnChapter)
	}
	return opens[0] + 1, closes[0], nil
}

// **The index is generated from the headings, and this compares the two.** It
// was a list written by hand and watched by five tests, one for each way such a
// list goes wrong — a chapter missing, an entry invented, the order, a rule said
// twice, the depth — and every one of those is a difference between the list
// and the headings, which is what this finds in one go. Whoever writes a
// chapter runs the command in the marker and commits what it writes.
func TestTheIndexIsTheHeadings(t *testing.T) {
	text := readCLAUDE(t)
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	from, to, err := indexBlock(lines)
	if err != nil {
		t.Fatal(err)
	}
	want := renderIndex(chapters(t))
	if len(want) < 100 {
		t.Fatalf("%d index lines rendered: this guard is looking at nothing", len(want))
	}
	got := lines[from:to]

	if *update {
		if strings.Join(got, "\n") == strings.Join(want, "\n") {
			return
		}
		out := append(append(append([]string{}, lines[:from]...), want...), lines[to:]...)
		if err := os.WriteFile(claudePath, []byte(strings.Join(out, eol)), 0o644); err != nil {
			t.Fatalf("CLAUDE.md cannot be written: %v", err)
		}
		t.Logf("the index in CLAUDE.md was rewritten from the headings: %d lines, was %d", len(want), len(got))
		return
	}

	for i := 0; i < len(got) || i < len(want); i++ {
		g, w := "(nothing)", "(nothing)"
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Fatalf("CLAUDE.md:%d the index says\n\t%s\nand the headings say\n\t%s\n"+
				"the index is generated: run go test ./internal/doc -run TestTheIndexIsTheHeadings -update",
				from+i+1, g, w)
		}
	}
}
