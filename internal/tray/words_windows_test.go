//go:build windows

package tray

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	"patmonitor/internal/i18n"
	"patmonitor/internal/version"
)

// **The `tray.` prefix has a guard of its own, and it is here.**
//
// The pages' guard lives in `internal/server` and looks at the markup: from
// there every tray entry looks like an orphan, because no page names them. And
// above all, from there the **codes cannot be expanded** — `tray.fault.` plus a
// value of Fault — which is exactly where an entry gets forgotten: the new code
// is added in Go, the catalogue lags behind, and the key appears in the menu.
//
// The question is put both ways, as for the pages: a key asked for and not
// translated, and an entry left behind for a key nobody asks for any more.

// The keys written out in full in the package's source.
var trayKey = regexp.MustCompile(`"(tray\.[a-z0-9.-]+)"`)

func keysAsked(t *testing.T) map[string]bool {
	t.Helper()
	keys := map[string]bool{}

	sources, err := filepath.Glob("*.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("no source to look at: %v", err)
	}
	for _, s := range sources {
		if strings.HasSuffix(s, "_test.go") {
			continue // tests name keys to test them, not to show them
		}
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range trayKey.FindAllStringSubmatch(string(b), -1) {
			// The prefixes of the composed keys — `"tray.fault."` — are not
			// keys: they are expanded below with the list of codes, and taken
			// whole they would have the catalogue looking for an entry that
			// must not exist.
			if strings.HasSuffix(m[1], ".") {
				continue
			}
			keys[m[1]] = true
		}
	}

	// The two composed prefixes, expanded with the **authoritative** list: Go's,
	// not a copy written here, which would only say it is equal to itself.
	for _, f := range AllFaults() {
		keys["tray.fault."+string(f)] = true
	}
	for _, n := range AllNotes() {
		keys["tray.note."+string(n)] = true
	}
	return keys
}

func catalogues(t *testing.T) map[string]map[string]any {
	t.Helper()
	outside := map[string]map[string]any{}
	entries, err := fs.ReadDir(i18n.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range entries {
		name, ok := strings.CutSuffix(v.Name(), ".json")
		if !ok {
			continue
		}
		b, err := fs.ReadFile(i18n.FS, v.Name())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s is not valid JSON: %v", v.Name(), err)
		}
		outside[name] = m
	}
	return outside
}

func TestEveryTrayKeyIsInEveryCatalogue(t *testing.T) {
	keys := keysAsked(t)
	if len(keys) == 0 {
		t.Fatal("no key found: the extractor is looking at nothing")
	}

	for language, cat := range catalogues(t) {
		var missing []string
		for k := range keys {
			if _, ok := cat[k]; !ok {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		for _, m := range missing {
			t.Errorf("%s: the entry %q is missing", language, m)
		}
	}
}

func TestNoTrayEntryIsAnOrphan(t *testing.T) {
	keys := keysAsked(t)

	for language, cat := range catalogues(t) {
		var orphans []string
		for k := range cat {
			if strings.HasPrefix(k, "tray.") && !keys[k] {
				orphans = append(orphans, k)
			}
		}
		sort.Strings(orphans)
		for _, o := range orphans {
			t.Errorf("%s: nobody asks for %q", language, o)
		}
	}
}

// **The tooltip's limit holds in every language, not in one.**
//
// `szTip` is 128 characters and `copyTip` truncates without saying anything. A
// German sentence is on average a third longer than an Italian one, so testing
// it on the writer's language alone means discovering the cut on somebody
// else's machine. So it is tested across **every catalogue**, in the worst case:
// the longest verdict, the longest summary, and the product identity that is
// always there.
func TestTheTooltipFitsItsBufferInEveryLanguage(t *testing.T) {
	// **The version grows, and under `go test` it is not even stamped.** The
	// third line is `productName` plus the version, which here is "PAT Monitor
	// 0.5.0" because the linker has stamped nothing, while a real binary
	// carries at least a revision and one day will carry two more digits. The
	// difference is added by hand: without it the test allows a margin that the
	// shipped build does not have, and that is exactly the margin on which the
	// tooltip would be cut.
	const grownIdentity = "PAT Monitor 1.10 r1234"
	growth := len(grownIdentity) - len(productName+" "+version.Short())
	if growth < 0 {
		growth = 0
	}

	for language := range catalogues(t) {
		tr := &Tray{dictionary: i18n.Open([]string{language})}

		worst := 0
		var which string
		for _, f := range AllFaults() {
			for _, n := range append(AllNotes(), NoteNone) {
				for _, p := range []Phase{PhaseStarting, PhaseHome, PhaseOutside, PhaseCheck, PhaseFault} {
					st := Status{
						Phase: p, Fault: f, Note: n,
						Viewers: 999, Devices: 999, Uptime: "1000h0m0s",
						PublicURL: "https://example.ts.net",
					}
					if n := len([]rune(tr.tooltip(st))); n > worst {
						worst, which = n, tr.tooltip(st)
					}
				}
			}
		}
		if worst+growth > 127 {
			t.Errorf("%s: the worst tooltip is %d characters (%d measured plus %d "+
				"for a grown version), the buffer holds 127:\n%s",
				language, worst+growth, worst, growth, which)
		}
	}
}

// **The menu accelerators have to be distinct, and they are per language.**
//
// The `&` before a letter assigns it: `&Open` answers to O. Two entries with the
// same letter give no error — Windows cycles through them instead of invoking
// them — so the keyboard stops driving the menu and nobody says so. It is a
// defect that **is created by translating**: English was clean and Italian had
// `D` twice, on "Da fare" and on "Disconnetti tutti i dispositivi", that is, on
// a harmless entry and on the one that cannot be undone.
//
// The computer running the monitor may be attached to a television and awkward
// to drive, and the keyboard is the only way of getting there: it is written
// among the menu's rules, and this is the test that keeps it true in every
// language.
func TestEveryLanguageHasDistinctMenuAccelerators(t *testing.T) {
	for language, cat := range catalogues(t) {
		byLetter := map[rune]string{}
		for key, v := range cat {
			if !strings.HasPrefix(key, "tray.menu.") {
				continue
			}
			phrase, _ := v.(string)
			i := strings.Index(phrase, "&")
			if i < 0 || i+1 >= len(phrase) {
				t.Errorf("%s: %q has no accelerator: %q", language, key, phrase)
				continue
			}
			letter := unicode.ToUpper([]rune(phrase[i+1:])[0])
			if other, double := byLetter[letter]; double {
				t.Errorf("%s: the letter %q is on two entries, %q and %q: from the "+
					"keyboard the menu stops answering and nobody says so",
					language, string(letter), other, key)
				continue
			}
			byLetter[letter] = key
		}
		if len(byLetter) == 0 {
			t.Errorf("%s: no menu entry found", language)
		}
	}
}

// The line the onboarding illustration draws in bold has to exist for every
// phase: without it the tooltip would begin with an empty line.
//
// **It holds in every language**, and it is the case where the absence would
// show up as a code: `T` returns the key when the entry is missing, so a
// forgotten phase would appear as `tray.verdict.fault` under the pointer.
func TestEveryPhaseHasAVerdictInEveryLanguage(t *testing.T) {
	for language := range i18n.Languages() {
		tr := &Tray{dictionary: i18n.Open([]string{language})}
		for _, p := range []Phase{PhaseStarting, PhaseHome, PhaseOutside, PhaseCheck, PhaseFault} {
			v := tr.verdict(p)
			if v == "" {
				t.Errorf("%s: phase %q with no verdict", language, p)
			}
			if strings.HasPrefix(v, "tray.verdict.") {
				t.Errorf("%s: phase %q shows the key instead of the word: %q", language, p, v)
			}
		}
	}
}
