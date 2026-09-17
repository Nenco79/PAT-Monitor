package server

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// Two defects reported by somebody using the guided path, and neither was
// visible to the compiler, to the tests or to a rereading. What follows is the
// guard for each.

// The comments are removed with withoutComments, which lives beside the
// catalogue tests: the reason is the same, and two different copies of the same
// pass would end up not removing the same things.

var closestClass = regexp.MustCompile(`closest\('\.([a-zA-Z0-9_-]+)'\)`)

// `closest` with a class that does not exist **does not protest**: it returns
// null, and the next line throws an exception that stops everything that came
// after.
//
// It happened. The QR code was moved inside the address panel — two panels
// become one — and this page went on looking for `.inquadra`, which from that
// moment did not exist. The status loop went on looping, the rest updated, and
// **the Tailscale address to open never appeared**: that is, the only thing that
// step exists for.
//
// Renaming a class is a change to the stylesheet, and nobody goes looking for
// what interrogates it from JavaScript. This test does.
func TestEveryClassLookedUpFromTheCodeExistsInItsPage(t *testing.T) {
	for _, c := range pagesAndScripts(t) {
		js := withoutComments(readAsset(t, c.js))
		html := readAsset(t, c.html)
		for _, m := range closestClass.FindAllStringSubmatch(js, -1) {
			class := m[1]
			// The class is looked for among those declared, not as a bare
			// string: "addr" also appears inside "addr-actions", and a
			// substring search would absolve a class that had vanished.
			if !declaresClass(html, class) {
				t.Errorf("%s looks for .%s, which %s does not have: closest() returns null and the next line throws",
					c.js, class, c.html)
			}
		}
	}
}

// Ids count as much as classes: `el('x')` on an id that is not there returns
// null, and the next line throws exactly as `closest` does. The difference is
// that ids get renamed even more readily, because they look like a matter
// internal to the page.
var idLookedUp = regexp.MustCompile(`\bel\(\'([a-zA-Z0-9_-]+)\'\)`)

// madeByTheCode are the ids the page builds for itself, so they are not in the
// markup and it is not a defect. The list is deliberately short: if it grows, it
// is the sign that the page is writing itself from JavaScript, and then this
// test protects less and less.
var madeByTheCode = map[string]bool{
	// The line declaring why the path is no longer updating: it exists only
	// when there is something to declare.
	"heartbeat-stopped": true,
}

func TestEveryIdTouchedByTheCodeExistsInItsPage(t *testing.T) {
	for _, c := range pagesAndScripts(t) {
		js := withoutComments(readAsset(t, c.js))
		html := readAsset(t, c.html)
		for _, m := range idLookedUp.FindAllStringSubmatch(js, -1) {
			id := m[1]
			if madeByTheCode[id] || strings.Contains(html, `id="`+id+`"`) {
				continue
			}
			t.Errorf("%s touches #%s, which %s does not have: el() returns null and the next line throws",
				c.js, id, c.html)
		}
	}
}

func declaresClass(html, class string) bool {
	for _, attr := range regexp.MustCompile(`class="([^"]*)"`).FindAllStringSubmatch(html, -1) {
		if strings.Contains(" "+attr[1]+" ", " "+class+" ") {
			return true
		}
	}
	return false
}

// **`hidden` has to mean hidden, and on its own it does not.**
//
// `[hidden] { display: none }` lives in the browser's sheet, and any `display`
// written by us beats it — not by specificity, but because the author's style
// always wins over the default one. Measured on the guided path: four elements
// marked `hidden` were visible, among them the away-from-home address panel and
// its green tick, which appeared to whoever had chosen to stay at home.
//
// The question is asked **per page**, not per sheet, because that is the shape
// of the promise: `element.hidden = true` has to hide on every page, whichever
// of its sheets carries the rule. Asked per sheet it would also accuse the ones
// that dress nothing — `icons.css`, `i18n.css` — which have no reason to carry
// it.
//
// The sheets are read **from the markup**, which is what makes this cover the
// pages nobody thought of: a hand-written pair list is what left the recordings
// page unwatched by the two guards above.
func TestHiddenMeansHiddenOnEveryPage(t *testing.T) {
	noSpaces := regexp.MustCompile(`\s+`)

	pages, err := fs.Glob(webFS, "web/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no page to examine: %v", err)
	}
	for _, p := range pages {
		name := path.Base(p)
		html := readAsset(t, name)

		sheets := reSheet.FindAllStringSubmatch(html, -1)
		if len(sheets) == 0 {
			// A page with no sheet has nothing to satisfy; a page that carries a
			// <link this expression does not recognise would leave the
			// examination in silence, which is the failure this test cannot
			// afford.
			if strings.Contains(html, `rel="stylesheet"`) {
				t.Errorf("%s carries a stylesheet none of which is recognised: "+
					"it would leave this check in silence", name)
			}
			continue
		}

		covered := false
		var loaded []string
		for _, m := range sheets {
			loaded = append(loaded, m[1])
			css := noSpaces.ReplaceAllString(readAsset(t, m[1]), "")
			if strings.Contains(css, "[hidden]{display:none!important;}") {
				covered = true
			}
		}
		if !covered {
			t.Errorf("%s loads %s and none of them has the rule on [hidden]: "+
				"any display beats it, and element.hidden = true stops hiding",
				name, strings.Join(loaded, ", "))
		}
	}
}

// pagesAndScripts pairs every page with every script it loads, read **from the
// markup**.
//
// The pairs used to be written by hand, two of them, and that list is the second
// list that diverges: `clips.html` and `clips.js` arrived later and nobody added
// them, so the two guards above went on watching two pages out of five while the
// recordings page was watched by nobody. A hand-written list of what to check
// protects exactly what somebody remembered.
//
// `dictionary.js` is left out because the server composes it: there is no file
// to read.
func pagesAndScripts(t *testing.T) []struct{ js, html string } {
	t.Helper()
	pages, err := fs.Glob(webFS, "web/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no page to examine: %v", err)
	}
	script := regexp.MustCompile(`src="/([^"]+\.js)"`)

	var out []struct{ js, html string }
	for _, p := range pages {
		name := path.Base(p)
		for _, m := range script.FindAllStringSubmatch(readAsset(t, name), -1) {
			if m[1] == "dictionary.js" {
				continue
			}
			out = append(out, struct{ js, html string }{m[1], name})
		}
	}
	if len(out) == 0 {
		t.Fatal("no page loads a script: the guards above would be watching nothing")
	}
	return out
}

// topLevelDeclaration finds the names a script puts in the shared scope: only
// those at column zero, which in these files is what top level means. The
// keyword is captured because it decides **which** of the two defects it is.
var topLevelDeclaration = regexp.MustCompile(`(?m)^(const|let|var|function|class)\s+([A-Za-z_$][\w$]*)`)

// **Two scripts on one page share one scope, and a name declared by both is a
// defect either way — but not the same defect.**
//
// With `const`, `let` or `class` the second file **does not parse at all**. Not
// one line runs, and there is no clue on the page: the markup is there, the first
// script has already filled the words, and what is missing is everything the
// second one would have done. On the recordings page that was the listing, the
// player, the four commands and the summary line — a page that looks half alive.
//
// With `function` or `var` it is legal JavaScript, and that is worse in the
// other direction: the later declaration silently replaces the earlier, so one
// of the two scripts calls something it did not write and nothing says so.
//
// It happened translating, in the first form: `i18n.js` had `LINGUA` and
// `clips.js` had `LANG`, two declarations of the same value under two names.
// Renaming the first to `LANG` made them collide, which is the good outcome — a
// silent duplication became a loud error — but nothing in Go noticed, and
// parsing each file on its own does not either, because each one is valid.
//
// The rule the fix follows is the older one: **there is one language, and
// `i18n.js` owns it.** This test only makes the collision impossible to ship.
func TestNoTwoScriptsOnAPageDeclareTheSameName(t *testing.T) {
	// Which of the two things a repeated name does, so the message says the true
	// one: a wrong explanation sends the reader looking in the wrong place, and
	// on this page that is the whole cost of the defect.
	lexical := map[string]bool{"const": true, "let": true, "class": true}

	byPage := map[string][]string{}
	for _, c := range pagesAndScripts(t) {
		byPage[c.html] = append(byPage[c.html], c.js)
	}
	for page, scripts := range byPage {
		type decl struct{ js, keyword string }
		where := map[string]decl{}
		for _, js := range scripts {
			src := withoutComments(readAsset(t, js))
			for _, m := range topLevelDeclaration.FindAllStringSubmatch(src, -1) {
				keyword, name := m[1], m[2]
				if first, seen := where[name]; seen && first.js != js {
					what := "the later one silently replaces the earlier, and one of the two scripts calls something it did not write"
					if lexical[keyword] || lexical[first.keyword] {
						what = "the second script does not parse at all"
					}
					t.Errorf("%s loads both %s and %s, and each declares %q at the top level: %s",
						page, first.js, js, name, what)
					continue
				}
				where[name] = decl{js, keyword}
			}
		}
	}
}

// The warning panel has several sources and shows the most urgent of those lit.
// **The order is a hand-written list**, and a source missing from it is not an
// error and not a warning: `showWarning` files the text and the panel never
// picks it, so the message simply never appears — which is the whole family of
// this file, a lookup that answers "nothing" instead of complaining.
//
// It got its own guard when the camera's box arrived with a fifth source: the
// microphone's box had been the only one added since the list was written, and
// the next one is always the one that is forgotten.
var warningSource = regexp.MustCompile(`showWarning\('([a-z-]+)'`)
var warningOrderList = regexp.MustCompile(`(?s)const warningOrder = \[(.*?)\]`)

func TestEveryWarningSourceIsInTheOrder(t *testing.T) {
	js := withoutComments(readAsset(t, "app.js"))

	m := warningOrderList.FindStringSubmatch(js)
	if m == nil {
		t.Fatal("this test is looking in the wrong place: warningOrder was not found")
	}
	order := map[string]bool{}
	for _, s := range regexp.MustCompile(`'([a-z-]+)'`).FindAllStringSubmatch(m[1], -1) {
		order[s[1]] = true
	}
	if len(order) == 0 {
		t.Fatal("this test is looking in the wrong place: warningOrder read as empty")
	}

	sources := map[string]bool{}
	for _, s := range warningSource.FindAllStringSubmatch(js, -1) {
		sources[s[1]] = true
	}
	if len(sources) == 0 {
		t.Fatal("this test is looking in the wrong place: no call to showWarning was found")
	}
	for s := range sources {
		if !order[s] {
			t.Errorf("showWarning(%q) is written and %q is not in warningOrder: that message can never appear", s, s)
		}
	}
	// The other direction: an entry left behind after its source has gone says
	// the list is a list of what somebody remembered, not of what there is.
	for s := range order {
		if !sources[s] {
			t.Errorf("warningOrder carries %q and nobody writes it any more", s)
		}
	}
}
