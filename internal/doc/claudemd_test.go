// Package doc holds no code: it holds the guard on the one list `CLAUDE.md`
// keeps about itself.
//
// That file's own rule is that two lists of the same thing always diverge, and
// its table of contents is a second list of its headings. It is kept anyway,
// and deliberately — "read in sequence, the table of contents **is** the list
// of rules" — because a rule one has to stumble upon is a rule nobody reads
// before breaking it. What cannot be kept is the divergence, and when this
// guard was written the index had already lost a chapter: "The reserve for the
// parameter sets is not where it looks", that is, a rule unreachable from the
// one page meant to carry all of them. Nothing had said so, because **an
// incomplete index looks exactly like an index.**
//
// **The index and the chapters are in separate files, and holding those
// together is what this package is for.** `CLAUDE.md` carries the front page
// and the whole index; the chapters live in `.claude/rules/`, sliced at heading
// boundaries so that each one arrives on its own when Claude reads the code it
// governs. The slices are contiguous and their names carry the order, so
// concatenating them in that order reproduces the document — which is what
// every test below reads. **A document split across sixteen files can lose a
// chapter in a way one file could not**: it can fall out between two slices,
// and nothing at the edges would look wrong. That is the direction the tiling
// test covers, and it is the reason the split was allowed at all.
package doc

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const claudePath = "../../CLAUDE.md"
const rulesDir = "../../.claude/rules"

// theIndexOwnChapter is the one heading that must not be listed: it is the
// chapter the index lives in. It is exempted **by name**, not by position — an
// exemption on "the first chapter" would cover for ever whatever ends up
// there next, which is a hole with a name rather than an argued exception.
const theIndexOwnChapter = "The headings are the rules"

// chapter is a heading, or the entry that must stand for it. Depth is the
// number of hashes, so 2 for `##` and 5 for `#####`.
//
// `where` and `line` are for the message and nothing else; what the order test
// compares is the position in the sequence, because a line number means nothing
// once the document is fourteen files.
type chapter struct {
	title string
	depth int
	where string
	line  int
}

// source is one file of the document, and the order of the slice is the order
// of the document.
type source struct {
	where string
	text  string
}

func readCLAUDE(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("CLAUDE.md cannot be read: %v", err)
	}
	return string(b)
}

// sliceNumber is the number a slice's name opens with, and -1 for a name that
// opens with none.
func sliceNumber(name string) int {
	digits := 0
	for digits < len(name) && name[digits] >= '0' && name[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits == len(name) || name[digits] != '-' {
		return -1
	}
	n, err := strconv.Atoi(name[:digits])
	if err != nil {
		return -1
	}
	return n
}

// ruleFiles are the slices, in the order their names give them.
//
// **The order is carried by the filenames and not by a list written here**,
// because a list here would be the second list this whole package exists to
// refuse: it would have to be edited every time a slice is added, and the day
// somebody forgot, the order test would be comparing the document against a
// stale idea of itself.
//
// **They are sorted by the number and not as text**, which is not a nicety: as
// text, `110-` sorts before `20-`, so the first slice added past ninety-nine
// would silently move to the front of the document and the order test would
// start accusing the index of a defect that is in the sort. Reading the number
// also means the gaps can stay ten wide without anybody having to pad them.
func ruleFiles(t *testing.T) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(rulesDir, "*.md"))
	if err != nil {
		t.Fatalf("the rules cannot be listed: %v", err)
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := sliceNumber(filepath.Base(found[i])), sliceNumber(filepath.Base(found[j]))
		if a != b {
			return a < b
		}
		return found[i] < found[j]
	})
	return found
}

// sources is the document: the front page first, then the slices in order.
func sources(t *testing.T) []source {
	t.Helper()

	out := []source{{"CLAUDE.md", readCLAUDE(t)}}
	for _, path := range ruleFiles(t) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s cannot be read: %v", path, err)
		}
		out = append(out, source{filepath.Base(path), string(b)})
	}
	return out
}

// title normalises the one way the two forms legitimately differ: the index
// puts its top level in bold, and both wrap at 79 columns. Everything else is
// compared as written — the heading **is** the rule, so an entry that
// paraphrases it is not that rule.
func title(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "**", "")), " ")
}

// chapters reads the headings, which are the original, from every source in
// order. Fenced code blocks are skipped: a `#` inside one is somebody else's
// syntax, and counting it would make this guard demand an index entry for a
// shell comment.
func chapters(t *testing.T) []chapter {
	t.Helper()

	var out []chapter
	for _, src := range sources(t) {
		fenced := false
		for i, line := range strings.Split(src.text, "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			depth := len(line) - len(strings.TrimLeft(line, "#"))
			if depth < 2 || depth > 5 || !strings.HasPrefix(line[depth:], " ") {
				continue
			}
			out = append(out, chapter{title(line[depth+1:]), depth, src.where, i + 1})
		}
	}
	if len(out) == 0 {
		t.Fatal("no heading read: this test is looking in the wrong place")
	}
	return out
}

// entries reads the list. An entry can be **wrapped over several lines**, so a
// line that does not open a new item belongs to the one above it: reading the
// first line alone would compare half a title and then accuse the chapter of
// not existing.
func entries(t *testing.T, md string) []chapter {
	t.Helper()

	_, after, ok := strings.Cut(md, "## "+theIndexOwnChapter)
	if !ok {
		t.Fatalf("the chapter %q is not there: this test is looking in the wrong place", theIndexOwnChapter)
	}
	body, _, _ := strings.Cut(after, "\n## ")

	var out []chapter
	for i, line := range strings.Split(body, "\n") {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if item := strings.TrimLeft(line, " "); strings.HasPrefix(item, "- ") {
			if indent%2 != 0 {
				t.Errorf("the index entry %q is indented by %d, which is not a level", title(item[2:]), indent)
			}
			out = append(out, chapter{title(item[2:]), indent/2 + 2, "CLAUDE.md", i + 1})
			continue
		}
		// A continuation: it belongs to the entry above.
		if text := strings.TrimSpace(line); text != "" && len(out) > 0 {
			out[len(out)-1].title = title(out[len(out)-1].title + " " + text)
		}
	}
	if len(out) == 0 {
		t.Fatal("no entry read from the index: this test is looking in the wrong place")
	}
	return out
}

// The direction that gets forgotten: a chapter written and nobody to say so.
// Whoever writes it has it in front of them, and cannot see that the page
// carrying every rule has stopped carrying theirs.
func TestEveryChapterIsInTheIndex(t *testing.T) {
	md := readCLAUDE(t)

	listed := make(map[string]bool)
	for _, e := range entries(t, md) {
		listed[e.title] = true
	}
	for _, c := range chapters(t) {
		if c.title == theIndexOwnChapter {
			if listed[c.title] {
				t.Errorf("the index lists the chapter it lives in, %q", c.title)
			}
			continue
		}
		if !listed[c.title] {
			t.Errorf("%s:%d %q is a chapter and the index does not carry it", c.where, c.line, c.title)
		}
	}
}

// And the opposite direction: a chapter renamed or deleted and its entry left
// standing. It is not dangerous, it is **false** — the index promises a rule
// the file no longer states, and whoever goes looking for it finds nothing.
func TestTheIndexInventsNoChapter(t *testing.T) {
	md := readCLAUDE(t)

	real := make(map[string]bool)
	for _, c := range chapters(t) {
		real[c.title] = true
	}
	for _, e := range entries(t, md) {
		if !real[e.title] {
			t.Errorf("the index carries %q, which is not a chapter of the file", e.title)
		}
	}
}

// "Read in sequence, the table of contents is the list of rules": the sequence
// is part of what it asserts. A chapter moved without its entry moving leaves
// an order that is neither the old one nor the new one.
//
// **What is compared is the position in the sequence, not the line number**,
// and the difference stopped being cosmetic when the document became fourteen
// files: line 40 of the second slice comes after line 300 of the first, so a
// check on line numbers would have declared the order broken at every slice
// boundary — that is, it would have failed for the one reason that is not a
// defect, and been switched off for it.
func TestTheIndexKeepsTheDocumentsOrder(t *testing.T) {
	md := readCLAUDE(t)

	at := make(map[string]int)
	for i, c := range chapters(t) {
		at[c.title] = i + 1
	}

	previous, from := 0, ""
	for _, e := range entries(t, md) {
		position, ok := at[e.title]
		if !ok {
			continue // the other direction's business
		}
		if position < previous {
			t.Errorf("the index puts %q after %q, the file the other way round", e.title, from)
		}
		previous, from = position, e.title
	}
}

// **A rule said twice is the one thing a list of rules must not do**, and the
// three tests above all let it through.
//
// The order check compares `line < previous`, which admits two entries pointing
// at the same heading; "every chapter is in the index" is satisfied by the first
// of them and "the index invents no chapter" by both, because neither counts
// occurrences. So a duplicate — which is exactly what an edit made by hand
// produces, the index being written by hand on purpose — passed all three, and
// the table of contents would have asserted a rule twice while the file states
// it once.
func TestTheIndexSaysEachRuleOnce(t *testing.T) {
	md := readCLAUDE(t)

	seen := make(map[string]int)
	for _, e := range entries(t, md) {
		seen[e.title]++
		if seen[e.title] == 2 {
			t.Errorf("the index carries %q twice (the second at line %d): read in "+
				"sequence the table of contents is the list of rules, and a rule "+
				"listed twice is two rules", e.title, e.line)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no index entry was read: this test is looking at nothing")
	}
}

// The nesting is carried by the indentation, two spaces to the level, and it is
// the only thing saying which rule is a case of which. A `####` filed at the
// margin reads as a chapter in its own right.
func TestTheIndentationCarriesTheNesting(t *testing.T) {
	md := readCLAUDE(t)

	depth := make(map[string]int)
	for _, c := range chapters(t) {
		depth[c.title] = c.depth
	}
	for _, e := range entries(t, md) {
		if d, ok := depth[e.title]; ok && d != e.depth {
			t.Errorf("the index files %q at level %d, the file writes it with %d hashes", e.title, e.depth, d)
		}
	}
}

// theAlwaysLoadedSlice is the one slice that declares no `paths`, so that the
// rules holding wherever one works arrive in every session. It is exempted **by
// name** for the reason the index's own chapter is: an exemption on "the last
// file" would cover whatever lands there next.
const theAlwaysLoadedSlice = "160-craft.md"

// frontmatter returns the globs a slice declares, and whether it declared the
// key at all. The two are not the same answer: a file with no `paths` is loaded
// in every session, and a file with `paths` and nothing under it matches
// nothing and is loaded in none.
func frontmatter(t *testing.T, path string) (globs []string, declared bool) {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", path, err)
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, false
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "paths:") {
			declared = true
			continue
		}
		if item := strings.TrimSpace(line); declared && strings.HasPrefix(item, "- ") {
			globs = append(globs, strings.Trim(strings.TrimSpace(item[2:]), `"`))
		}
	}
	return globs, declared
}

// A slice with no `paths` loads in every session, which is the cost the split
// was made to remove: one forgotten line and the chapter is back in the
// startup budget, with nothing to see but a document that reads correctly.
func TestEverySliceSaysWhichCodeItGoverns(t *testing.T) {
	files := ruleFiles(t)
	for _, path := range files {
		name := filepath.Base(path)
		globs, declared := frontmatter(t, path)
		if name == theAlwaysLoadedSlice {
			if declared {
				t.Errorf("%s is the slice that must load in every session and it declares paths", name)
			}
			continue
		}
		if !declared {
			t.Errorf("%s declares no paths, so it is loaded in every session like the front page", name)
			continue
		}
		if len(globs) == 0 {
			t.Errorf("%s declares paths and lists none, so it matches no file and is never loaded", name)
		}
	}
	if len(files) < 10 {
		t.Fatalf("%d slices read: this guard is looking at nothing", len(files))
	}
}

// **A glob that matches nothing switches its slice off, greenly.** It is the
// family this repository knows best — a wrong GUID does not complain, a missing
// CSS token does not complain — and here the symptom is the worst kind: the
// rules for a package simply stop arriving, and the only way to notice is to
// find out afterwards that they were not followed.
//
// What is checked is the literal part of the glob, up to the first wildcard:
// that much is a path, and a path either exists or was mistyped.
func TestEveryGlobNamesSomethingThatExists(t *testing.T) {
	const root = "../../"

	checked := 0
	for _, path := range ruleFiles(t) {
		globs, _ := frontmatter(t, path)
		for _, glob := range globs {
			literal := glob
			if i := strings.IndexAny(glob, "*?["); i >= 0 {
				literal = glob[:i]
			}
			literal = strings.TrimSuffix(literal, "/")
			if literal == "" {
				t.Errorf("%s declares %q, which is a wildcard with nothing in front of it", filepath.Base(path), glob)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(literal))); err != nil {
				t.Errorf("%s declares %q and %s does not exist, so that rule is switched off in silence",
					filepath.Base(path), glob, literal)
			}
			checked++
		}
	}
	if checked < 20 {
		t.Fatalf("%d globs read: this guard is looking at nothing", checked)
	}
}

// **The slices tile the document, and this is the direction one file could not
// lose.** A chapter can now be in two slices at once — which is what a
// copy-and-paste during a re-slice produces — and every test above lets it
// through: the index carries it once, so "every chapter is listed" is satisfied
// by the first copy, "the index invents nothing" by both, and the order check
// by neither. The document would state a rule twice while the index says it
// once, which is the mirror image of the duplicate the index test already
// refuses.
func TestNoChapterIsInTwoSlices(t *testing.T) {
	seen := make(map[string]chapter)
	for _, c := range chapters(t) {
		if first, ok := seen[c.title]; ok {
			t.Errorf("%q is written in %s:%d and again in %s:%d", c.title, first.where, first.line, c.where, c.line)
			continue
		}
		seen[c.title] = c
	}
	if len(seen) < 100 {
		t.Fatalf("%d chapters read: this guard is looking at nothing", len(seen))
	}
}

// theUngovernedPackages are the two that no slice names, and they are exempted
// **by name** with the reason each time.
//
// `internal/doc` is this guard: it holds no product code, and what governs it is
// "Guards, and how they fail", which is in the slice that loads in every session
// anyway. `internal/config` is thinner than it looks — the rules that touch it
// are scattered one paragraph at a time across five slices, so naming them all
// would hand somebody adding a key sixty thousand tokens to find one sentence.
// The one rule a config edit must not miss is on the front page, which is always
// loaded: **a key that is not in the README's table fails `readme_test.go`**.
var theUngovernedPackages = map[string]bool{
	"internal/config": true,
	"internal/doc":    true,
}

// **A package no slice names gets no rules, and nothing says so.** It is the
// quietest way this arrangement can rot: somebody adds `internal/whatever`, the
// tests stay green, and for the rest of that package's life every session works
// on it with the front page and nothing else — which reads exactly like a
// package that has no rules rather than one whose rules never arrive.
func TestEveryPackageIsGovernedBySomeSlice(t *testing.T) {
	const root = "../../"

	var globs []string
	for _, path := range ruleFiles(t) {
		g, _ := frontmatter(t, path)
		globs = append(globs, g...)
	}

	checked := 0
	for _, parent := range []string{"internal", "cmd"} {
		dirs, err := os.ReadDir(filepath.Join(root, parent))
		if err != nil {
			t.Fatalf("%s cannot be listed: %v", parent, err)
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			pkg := parent + "/" + d.Name()
			if theUngovernedPackages[pkg] {
				continue
			}
			checked++
			governed := false
			for _, g := range globs {
				if strings.HasPrefix(g, pkg+"/") {
					governed = true
					break
				}
			}
			if !governed {
				t.Errorf("no slice names %s, so its rules never reach whoever works on it", pkg)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("%d packages read: this guard is looking at nothing", checked)
	}
}

// mapped reads the table in the index that says which slice carries what.
func mapped(t *testing.T) map[string]bool {
	t.Helper()

	out := make(map[string]bool)
	for line := range strings.SplitSeq(readCLAUDE(t), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		if name, _, ok := strings.Cut(line[3:], "`"); ok {
			out[name] = true
		}
	}
	return out
}

// **The map is a second list of the slices, so it is watched like the first.**
// A slice added and not announced is a chapter nobody can find from the one page
// that is supposed to carry the way in; a row left behind for a slice that has
// gone is the index's other failure, one level up — it promises a file that is
// not there.
func TestTheMapNamesEverySlice(t *testing.T) {
	listed := mapped(t)

	onDisk := make(map[string]bool)
	for _, path := range ruleFiles(t) {
		name := filepath.Base(path)
		onDisk[name] = true
		if !listed[name] {
			t.Errorf("%s is a slice and the map in CLAUDE.md does not name it", name)
		}
	}
	for name := range listed {
		if !onDisk[name] {
			t.Errorf("the map in CLAUDE.md names %s, which is not in %s", name, rulesDir)
		}
	}
	if len(listed) == 0 {
		t.Fatal("no row read from the map: this guard is looking at nothing")
	}
}

// **The number in the name is the slice's place in the document**, and it is the
// only thing that says so: the index gives the order, and the guard above reads
// that order off these numbers. So a name without one has no place, and two
// names with the same one have a place that depends on how the rest of the name
// happens to spell — which is the sort deciding the document instead of the
// author. The gaps are ten wide so that a slice can be cut out of another one
// without renaming its neighbours, which is how `70-network.md` and
// `80-encoder.md` came to exist.
func TestEverySliceSaysWhereItGoesAndNoTwoSayTheSame(t *testing.T) {
	at := make(map[int]string)
	for _, path := range ruleFiles(t) {
		name := filepath.Base(path)
		n := sliceNumber(name)
		if n < 0 {
			t.Errorf("%s does not open with a number and a dash, so it has no place in the document", name)
			continue
		}
		if first, taken := at[n]; taken {
			t.Errorf("%s and %s both claim place %d, so which comes first is decided by the alphabet", first, name, n)
			continue
		}
		at[n] = name
	}
	if len(at) < 10 {
		t.Fatalf("%d numbered slices read: this guard is looking at nothing", len(at))
	}
}
