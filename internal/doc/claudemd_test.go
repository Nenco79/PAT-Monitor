// Package doc holds no code: it holds the guards on the index `CLAUDE.md`
// keeps of itself.
//
// That file's own rule is that two lists of the same thing always diverge, and
// its table of contents is a second list of its headings. It is kept anyway,
// and deliberately — "read in sequence, the table of contents **is** the list
// of rules" — because a rule one has to stumble upon is a rule nobody reads
// before breaking it. What cannot be kept is the divergence, and when the first
// guard here was written the index had already lost a chapter: "The reserve for
// the parameter sets is not where it looks", that is, a rule unreachable from
// the one page meant to carry all of them. Nothing had said so, because **an
// incomplete index looks exactly like an index.**
//
// **So the list of headings is no longer written by hand: it is generated from
// them** (`index_test.go`), and a missing, invented, reordered, doubled or
// misfiled entry — each of which had a test of its own while the list was
// typed — is now a difference from the render, which is the one thing that test
// compares. The table of slices above it is still written by hand, because what
// it says about each slice is prose, and it is watched like any hand-written
// list.
//
// **The index and the chapters are in separate files, and holding those
// together is what this package is for.** `CLAUDE.md` carries the front page
// and the whole index; the chapters live in `.claude/rules/`, sliced at heading
// boundaries so that each one arrives on its own when Claude reads the code it
// governs. The slices are contiguous and their names carry the order, so
// concatenating them in that order reproduces the document — which is what
// every test below reads. **A document split across files can lose a chapter in
// a way one file could not**: it can fall out between two slices,
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

// chapter is a heading. Depth is the number of hashes, so 2 for `##` and 5 for
// `#####`.
//
// `where` and `line` are for the message and nothing else: the order of the
// document is the position in the sequence, because a line number means nothing
// once the document is several files.
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
// somebody forgot, the index would be generated from a stale idea of the
// document.
//
// **They are sorted by the number and not as text**, which is not a nicety: as
// text, `110-` sorts before `20-`, so the first slice added past ninety-nine
// would silently move to the front of the document, and the generated index
// would follow it there without a word. Reading the number also means the gaps
// can stay ten wide without anybody having to pad them.
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

// title is a heading's text with its whitespace collapsed and any bold taken
// out, which is the form the index is rendered from. Nothing else is touched —
// the heading **is** the rule, so an entry that paraphrased it would not be that
// rule.
func title(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "**", "")), " ")
}

// chapters reads the headings, which are the original, from every source in
// order. Fenced code blocks are skipped: a `#` inside one is somebody else's
// syntax, and counting it would put a shell comment in the index.
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
// copy-and-paste during a re-slice produces — and the index cannot see it: it
// is generated from the headings, so it would print the duplicated one twice
// and `TestTheIndexIsTheHeadings` would pass on it. The document would state a
// rule twice, and this is the only test that counts the headings rather than
// copying them.
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
	var globs []string
	for _, path := range ruleFiles(t) {
		g, _ := frontmatter(t, path)
		globs = append(globs, g...)
	}

	checked := 0
	for _, pkg := range packageDirs(t) {
		if theUngovernedPackages[pkg] {
			continue
		}
		checked++
		governed := false
		for _, g := range globs {
			if governs(g, pkg) {
				governed = true
				break
			}
		}
		if !governed {
			t.Errorf("no slice names %s, so its rules never reach whoever works on it", pkg)
		}
	}
	if checked < 20 {
		t.Fatalf("%d packages read: this guard is looking at nothing", checked)
	}
}

// packageDirs are the directories under `internal/` and `cmd/`, as
// slash-separated paths from the root of the repository.
func packageDirs(t *testing.T) []string {
	t.Helper()
	const root = "../../"

	var out []string
	for _, parent := range []string{"internal", "cmd"} {
		dirs, err := os.ReadDir(filepath.Join(root, parent))
		if err != nil {
			t.Fatalf("%s cannot be listed: %v", parent, err)
		}
		for _, d := range dirs {
			if d.IsDir() {
				out = append(out, parent+"/"+d.Name())
			}
		}
	}
	return out
}

// governs says whether a slice's glob brings it into a session working on pkg:
// the glob names something inside that directory. The coverage test and the
// word budget both ask it, so that "governed" means one thing in both.
func governs(glob, pkg string) bool {
	return strings.HasPrefix(glob, pkg+"/")
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
// only thing that says so: the index is generated in the order these numbers
// give. So a name without one has no place, and two names with the same one
// have a place that depends on how the rest of the name happens to spell —
// which is the sort deciding the document instead of the author. The gaps are ten wide so that a slice can be cut out of another one
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
