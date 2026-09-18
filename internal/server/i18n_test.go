package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"patmonitor/internal/alerts"
	"patmonitor/internal/config"
	"patmonitor/internal/detect"
	"patmonitor/internal/i18n"
	"patmonitor/internal/tunnel"
)

// **The catalogues and the pages are two halves that can come apart in
// silence.**
//
// There is no text in the markup, there are keys, and every language has its
// catalogue — English is the main one, which all the others overlay. The price
// of this shape is precise: **a key with no entry shows as a key**, and no
// compiler sees it. Whoever adds a button and forgets the line in the catalogue
// publishes a button that says `setup.submit`.
//
// This file is the guard, and it is the same shape as `licenses_test.go` and
// `assets_test.go`: the question is put **both ways**, because the one that gets
// forgotten is always the second — an entry left behind for a key that no longer
// exists is not dangerous, it is **false**, and it makes something that is not
// there look translated.

// keysFromMarkup gathers the keys declared in the pages.
//
// A real parser is used and not a regular expression: `data-i18n` can sit on a
// tag with ten attributes, and a regex that works today breaks at the first
// touch of the markup. The question is the same one the browser asks, so it is
// asked with the same tool.
func keysFromMarkup(t *testing.T, source string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("markup cannot be parsed: %v", err)
	}

	var out []string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				switch a.Key {
				case "data-i18n":
					out = append(out, a.Val)
					// An element filled by `textContent` cannot have children:
					// it would delete them. Better said here than discovered
					// with a bold gone missing.
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode {
							t.Errorf("<%s data-i18n=%q> contains <%s>: the translated "+
								"text would delete it", n.Data, a.Val, c.Data)
						}
					}
				case "data-i18n-html":
					out = append(out, a.Val)
				case "data-i18n-plain":
					// The same sentence without its Windows accelerator marker.
					// It is how the drawing of the tray panel carries the
					// panel's own labels instead of copies, so the keys are
					// `tray.…` and their entries live where the tray's do —
					// but a key wrong here shows as a key like any other, and
					// without this line nothing would say so.
					out = append(out, a.Val)
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode {
							t.Errorf("<%s data-i18n-plain=%q> contains <%s>: the "+
								"translated text would delete it", n.Data, a.Val, c.Data)
						}
					}
				case "data-i18n-attr":
					// `attribute:key` pairs, separated by spaces.
					for _, pair := range strings.Fields(a.Val) {
						_, key, ok := strings.Cut(pair, ":")
						if !ok || key == "" {
							t.Errorf("<%s data-i18n-attr=%q>: the key is missing in %q",
								n.Data, a.Val, pair)
							continue
						}
						out = append(out, key)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return out
}

// Keys are recognised by their shape, wherever they are written.
//
// **Searching for `T('...')` is not enough**, and the first draft did that: a
// key can live inside a map and pass through T many lines later — it is the case
// of the signalling reasons. Looking for it by shape finds it anyway, and the
// risk of catching some ordinary string is minimal: `viewer.state.live` looks
// like nothing else written anywhere.
var reLiteralKey = regexp.MustCompile(`'([a-z][a-z0-9]*(?:\.[a-z0-9-]+)+)'`)

// And the **composed** keys declare a prefix: `T('viewer.alert.' + c)`.
//
// They are the right shape — the code comes from the server and the page picks
// the word — and they are also blind to any static extraction. The guard's first
// draft took them for whole keys and declared all thirty-two real entries
// orphans, which was a good way of noticing.
//
// They are expanded with the **authoritative** list, which lives in Go: the
// alert codes, the tunnel phases, the microphone's health. That way the test no
// longer checks that the catalogue is consistent with itself, but that **every
// code the server can emit has a word in every language** — which is the real
// question, and it catches the new code added on the Go side and forgotten over
// here.
var rePrefixKey = regexp.MustCompile(`\bT(?:N|Or)?\('([a-z][a-z0-9.-]*\.)'\s*\+`)

// A key is an identifier, not a sentence. The check exists because the previous
// shape used the sentence itself as the key, and without a written constraint it
// is the thing one falls back into out of convenience: `T('Sign in')` works
// perfectly well until somebody translates.
var keyShape = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9-]+)+$`)

// And a composed key's prefix is half a key: it ends with a dot because the
// missing piece is put there by the code arriving from the server.
var prefixShape = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9-]+)*\.$`)

func keysFromCode(t *testing.T, source string) []string {
	var out []string
	for _, m := range reLiteralKey.FindAllStringSubmatch(source, -1) {
		out = append(out, m[1])
	}
	for _, m := range rePrefixKey.FindAllStringSubmatch(source, -1) {
		codes, known := codesForPrefix[m[1]]
		if !known {
			t.Errorf("the composed key %q has no list of codes in "+
				"codesForPrefix: without one, nobody can check that the entries "+
				"are all there", m[1]+"…")
			continue
		}
		for _, c := range codes {
			out = append(out, m[1]+c)
		}
	}
	return out
}

// Where the composed keys' codes come from. **The list is Go's**, not a copy
// written here: a copy would only say it is equal to itself.
var codesForPrefix = map[string][]string{
	"viewer.alert.":     append(alertCodes(), "unknown"),
	"viewer.recovered.": append(alertCodes(), "unknown"),
	"viewer.phase.":     append(phaseCodes(), "unknown"),
	// The reachability check's outcome. ReachUnknown is not there because it is
	// not shown: with no evidence the badge goes back to saying the phase.
	"viewer.reach.":      reachCodes(),
	"viewer.mic.health.": micHealthCodes(),
	// The server's refusals. `unknown` is not a code the server sends: it is
	// `TErr`'s fallback for when the cached dictionary does not know a new code
	// yet — an updated monitor and an old page are two versions of the same
	// product.
	"err.":            append(AllErrCodes(), "unknown"),
	"tunnel.action.":  actionCodes(),
	"tunnel.step.":    stepCodes(),
	"tunnel.warning.": warningCodes(),
}

func reachCodes() []string {
	var f []string
	for _, r := range tunnel.AllReaches() {
		f = append(f, string(r))
	}
	return f
}

func warningCodes() []string {
	var f []string
	for _, w := range tunnel.AllWarnings() {
		f = append(f, string(w))
	}
	return f
}

func actionCodes() []string {
	var f []string
	for _, a := range tunnel.AllActions() {
		f = append(f, string(a))
	}
	return f
}

func stepCodes() []string {
	var f []string
	for _, s := range tunnel.AllSteps() {
		f = append(f, string(s))
	}
	return f
}

func alertCodes() []string {
	var f []string
	for _, c := range alerts.AllCodes() {
		f = append(f, string(c))
	}
	return f
}

func phaseCodes() []string {
	var f []string
	for _, p := range expectedPhases {
		if p != tunnel.PhaseOff {
			f = append(f, string(p)) // with the tunnel off there is no panel
		}
	}
	return f
}

func micHealthCodes() []string {
	return []string{
		detect.MicCodeOK, detect.MicCodeDigitalSilence,
		detect.MicCodeQuiet, detect.MicCodeUnknown,
	}
}

// allKeys gathers what the pages ask to have translated.
func allKeys(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	keys := map[string]bool{}

	pages, _ := fs.Glob(s.assets, "*.html")
	for _, p := range pages {
		b, err := fs.ReadFile(s.assets, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keysFromMarkup(t, string(b)) {
			keys[k] = true
		}
	}

	// `i18n.js` is watched like the others: it defines `T`, and since the
	// language selector exists it also **uses** a key. Excluding it on principle
	// would have made that key invisible — and indeed the first draft excluded
	// it, and the test declared the key an orphan.
	scripts, _ := fs.Glob(s.assets, "*.js")
	for _, p := range scripts {
		b, err := fs.ReadFile(s.assets, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keysFromCode(t, string(b)) {
			keys[k] = true
		}
	}

	// And one key is read by the **server**, not by a page: `lang.name` is the
	// language's name written in that language, which `serveDictionary` puts in
	// the selector's list. It is here because the extractor looks at the pages,
	// and from there that key is invisible.
	keys["lang.name"] = true
	return keys
}

// The catalogues are read from `internal/i18n`, where they live: here they are
// looked at as **files**, key by key, to compare them with the pages. Consulting
// them is done with `i18n.Open`, and that is another question.
func cataloguesOnDisk(t *testing.T, s *Server) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	entries, err := fs.ReadDir(i18n.FS, ".")
	if err != nil {
		t.Fatalf("catalogue folder: %v", err)
	}
	for _, v := range entries {
		if path.Ext(v.Name()) != ".json" {
			continue
		}
		b, err := fs.ReadFile(i18n.FS, v.Name())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("%s is not valid JSON: %v", v.Name(), err)
			continue
		}
		out[strings.TrimSuffix(v.Name(), ".json")] = m
	}
	if out["en"] == nil {
		t.Fatal("en.json is missing: it is the base the other languages overlay")
	}
	return out
}

// Every key used has to have an entry in every catalogue.
//
// **In English too**, and there it is the only place where the absence falls
// back on nothing: below the main language there is nothing else, and the page
// shows the key. For the other languages the test stays just as strict — a page
// half in one language and half in another is not a product, it is a building
// site.
func TestEveryKeyIsInEveryCatalogue(t *testing.T) {
	s := serverWithoutPassword(t)
	keys := allKeys(t, s)
	if len(keys) == 0 {
		t.Fatal("no key found: the extractor is looking at nothing")
	}

	for language, cat := range cataloguesOnDisk(t, s) {
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

// And the opposite direction: an entry for a key that no longer exists.
//
// It is not dangerous, it is **false** — it declares translated something no
// page shows — and it is what gets left behind when a piece of interface is
// removed. It is the same question `licenses_test.go` asks of the folders.
func TestNoCatalogueEntryIsAnOrphan(t *testing.T) {
	s := serverWithoutPassword(t)
	keys := allKeys(t, s)

	for language, cat := range cataloguesOnDisk(t, s) {
		var orphans []string
		for k := range cat {
			// **The pages are not the only reader.** The `tray.…` entries are
			// consumed by `internal/tray`, which speaks Windows' language and
			// not the browser's, and looking for them here would declare them
			// all orphans. That prefix's guard lives in its own package, where
			// the codes can also be expanded — which cannot be seen from here.
			if strings.HasPrefix(k, "tray.") {
				continue
			}
			if !keys[k] {
				orphans = append(orphans, k)
			}
		}
		sort.Strings(orphans)
		for _, o := range orphans {
			t.Errorf("%s: %q appears in no page", language, o)
		}
	}
}

// The markup allowed in `data-i18n-html` entries is five tags and no
// attributes.
//
// That text ends up in `innerHTML`, so in principle the catalogue could write
// anything into the page. The catalogues are embedded assets and do not come
// from outside, but the protection that matters is another: **`onboarding.js`
// looks for its own buttons by id**, and an entry containing a `<button>` or an
// `id` would switch a command off with no error. No attributes, then, and only
// emphasis.
func TestTranslatedMarkupIsOnlyEmphasis(t *testing.T) {
	s := serverWithoutPassword(t)

	// The keys the markup consumes as HTML.
	asHTML := map[string]bool{}
	pages, _ := fs.Glob(s.assets, "*.html")
	for _, p := range pages {
		b, _ := fs.ReadFile(s.assets, p)
		for _, m := range regexp.MustCompile(
			`data-i18n-html="([^"]+)"`).FindAllStringSubmatch(string(b), -1) {
			asHTML[m[1]] = true
		}
	}

	allowed := map[string]bool{"strong": true, "em": true, "b": true, "code": true, "br": true}
	tag := regexp.MustCompile(`<\s*/?\s*([a-zA-Z][a-zA-Z0-9]*)([^>]*)>`)

	for language, cat := range cataloguesOnDisk(t, s) {
		for k := range asHTML {
			v, _ := cat[k].(string)
			for _, m := range tag.FindAllStringSubmatch(v, -1) {
				name := strings.ToLower(m[1])
				if !allowed[name] {
					t.Errorf("%s: %q contains <%s>, which is not emphasis", language, k, name)
				}
				if strings.TrimSpace(strings.TrimSuffix(m[2], "/")) != "" {
					t.Errorf("%s: %q has attributes on <%s>: %q", language, k, name, m[2])
				}
			}
		}
		// And the opposite direction: a sentence with emphasis inside it, marked
		// with `data-i18n` rather than `data-i18n-html`, would show the tags as
		// text. It shows at once, but it shows in production.
		for k, val := range cat {
			v, ok := val.(string)
			if !ok || asHTML[k] || !tag.MatchString(v) {
				continue
			}
			t.Errorf("%s: %q contains markup but no page uses it with "+
				"data-i18n-html: the tags would show as text", language, k)
		}
	}
}

// **No sentence is written inside the page's code.**
//
// This is the hole the other tests could not see: they look at the keys that
// **are** there, both ways, and a hand-written sentence is not a key — so nobody
// sees it. Three strings got through in one go (the success branch of the
// preview, the copy confirmation, and the step count left in the markup), all of
// them beside translated lines: **the branch that works gets forgotten because
// nobody looks at it**, and in English one got a page with the errors translated
// and the successes in another language.
//
// What is looked for is what gets **assigned to text**. The literals that are
// keys are removed first: `T('onb.copied')` is exactly the right shape. What is
// left with three letters in a row is prose, and prose lives in the catalogues.
func TestNoProseIsWrittenInThePageCode(t *testing.T) {
	s := serverWithoutPassword(t)

	// Assignments to text, up to the semicolon: `.textContent = …;`
	assign := regexp.MustCompile(`(?s)\.(?:textContent|innerHTML)\s*=([^;]*);`)
	// And the attributes that carry words. `aria-pressed` and `tabindex` do not.
	attribute := regexp.MustCompile(
		`(?s)setAttribute\(\s*'(?:title|alt|aria-label|aria-valuetext|placeholder)'\s*,([^;]*)\)`)
	literal := regexp.MustCompile(`'((?:[^'\\]|\\.)*)'`)
	threeLetters := regexp.MustCompile(`\p{L}{3,}`)

	scripts, _ := fs.Glob(s.assets, "*.js")
	for _, p := range scripts {
		if p == "i18n.js" {
			continue // it is the one that defines T: its strings are the mechanism
		}
		b, err := fs.ReadFile(s.assets, p)
		if err != nil {
			t.Fatal(err)
		}
		src := withoutComments(string(b))

		for _, re := range []*regexp.Regexp{assign, attribute} {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				for _, l := range literal.FindAllStringSubmatch(m[1], -1) {
					// A key is the right shape, and so is the **prefix** of a
					// composed key: `'tunnel.warning.'` plus a code is exactly
					// how a word is chosen from the code arriving from the
					// server. Without this line the prose guard would accuse
					// precisely the shape the other guard demands.
					if keyShape.MatchString(l[1]) || prefixShape.MatchString(l[1]) ||
						!threeLetters.MatchString(l[1]) {
						continue
					}
					t.Errorf("%s: the sentence %q is written in the code instead of "+
						"living in the catalogues", p, l[1])
				}
			}
		}
	}
}

// withoutComments removes `//` and the slash-star sections.
//
// It is needed because this project's comments are long and quote freely the
// sentences they talk about: without removing them the test would accuse the
// explanations instead of the code.
func withoutComments(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 4
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}

// A key is an identifier, not a sentence.
func TestKeysLookLikeKeys(t *testing.T) {
	s := serverWithoutPassword(t)
	var bad []string
	for k := range allKeys(t, s) {
		if !keyShape.MatchString(k) {
			bad = append(bad, k)
		}
	}
	sort.Strings(bad)
	for _, k := range bad {
		t.Errorf("%q does not have the shape of a key (page.thing, lower case)", k)
	}
}

// The dictionary is always delivered, and it carries the main language below the
// one asked for.
func TestTheDictionaryIsAlwaysServableJavaScript(t *testing.T) {
	s := serverWithoutPassword(t)

	for _, c := range []struct{ header, inside string }{
		{"", `"login.submit":"Sign in"`},
		{"it", `"login.submit":"Accedi"`},
		{"en", `"login.submit":"Sign in"`},
		{"xx", `"login.submit":"Sign in"`},
	} {
		w := askDictionary(t, s, c.header)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
			t.Errorf("Accept-Language %q: Content-Type %q", c.header, ct)
		}
		// The response depends on the language asked for **and** on the
		// selector's cookie: without declaring both, an intermediary would serve
		// everybody the catalogue of the first one through.
		vary := w.Header().Get("Vary")
		for _, want := range []string{"Accept-Language", "Cookie"} {
			if !strings.Contains(vary, want) {
				t.Errorf("Accept-Language %q: Vary does not name %s (Vary=%q)",
					c.header, want, vary)
			}
		}
		if body := w.Body.String(); !strings.Contains(body, c.inside) {
			t.Errorf("Accept-Language %q: the body does not contain %q\n%s",
				c.header, c.inside, body)
		}
	}
}

// The fallback of an incomplete translation and the choice of language are
// tested in `internal/i18n`, where catalogues that do not exist here can be
// fabricated: a half language and one we do not have.

// **The explicit choice wins over the browser's declaration**, which is the
// whole reason the selector exists: `Accept-Language` is often a choice made
// once when the system was installed, or not made at all.
//
// And the cookie's value **cannot become a file path**. It ends up in
// `i18n/<language>.json`, so without validation against the list of catalogues
// it would be an arbitrary read written by whoever visits the page.
func TestTheChosenLanguageBeatsTheBrowser(t *testing.T) {
	s := serverWithoutPassword(t)

	// **The unknown tag is derived and not written.** `fr` stood in this case,
	// and the day French arrived it stopped being a language we do not have:
	// the case then kept its other assertion and lost the one it exists for.
	// `es` walked into the same case a language later, which is the same lesson
	// twice, and the derivation is what makes the second time cost nothing. The
	// candidates are the languages most likely to arrive next, so this list
	// maintains itself for a while, and the `t.Fatal` says so on the day it
	// stops.
	unknown := ""
	for _, tag := range []string{"nl", "pl", "pt", "ja"} {
		if !i18n.Languages()[tag] {
			unknown = tag
			break
		}
	}
	if unknown == "" {
		t.Fatal("every candidate tag has a catalogue now: this test needs one that does not")
	}

	cases := []struct {
		cookie, accept, inside, why string
	}{
		{"it", "en-GB,en;q=0.9", `"login.submit":"Accedi"`,
			"the cookie forces Italian on an English browser"},
		{"en", "it-IT,it;q=0.9", `"login.submit":"Sign in"`,
			"and the other way round, which is the case the selector exists for"},
		{"", "it-IT", `"login.submit":"Accedi"`,
			"with no cookie the browser decides"},
		{unknown, "it-IT", `"login.submit":"Accedi"`,
			"a cookie for a language we do not have does not count"},
		{"../../go.mod", "it-IT", `"login.submit":"Accedi"`,
			"and a path is not a language"},
		{"..%2F..%2Fgo.mod", "it-IT", `"login.submit":"Accedi"`,
			"not even encoded"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/dictionary.js", nil)
		r.RemoteAddr = "192.168.1.40:5555"
		r.Header.Set("Accept-Language", c.accept)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "lang", Value: c.cookie})
		}
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)

		body := w.Body.String()
		if !strings.Contains(body, c.inside) {
			t.Errorf("cookie %q + %q: %q is missing (%s)", c.cookie, c.accept, c.inside, c.why)
		}
		if strings.Contains(body, "module patmonitor") {
			t.Errorf("cookie %q: the body contains a file of the project", c.cookie)
		}
	}
}

// The selector needs the list of languages, with each one's name written in that
// language: it is the only word whoever is looking for their own can recognise
// when the page is speaking another.
//
// **The list is read from the catalogues and not written here.** It named
// English and Italian, so German and then French arrived without it — the guard
// covering less than it claimed, and in the direction that looks green.
func TestTheDictionaryCarriesTheLanguageNames(t *testing.T) {
	s := serverWithoutPassword(t)
	body := askDictionary(t, s, "en").Body.String()
	for language, cat := range cataloguesOnDisk(t, s) {
		name, _ := cat["lang.name"].(string)
		if name == "" {
			t.Errorf("%s: no `lang.name`, so the selector cannot offer it", language)
			continue
		}
		want := `"` + language + `":"` + name + `"`
		if !strings.Contains(body, want) {
			t.Errorf("the list of languages does not contain %s:\n%s", want, body)
		}
	}
}

func askDictionary(t *testing.T, s *Server, accept string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/dictionary.js", nil)
	r.RemoteAddr = "192.168.1.40:5555"
	if accept != "" {
		r.Header.Set("Accept-Language", accept)
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	return w
}

// **The password minimum lives in two places, and one of them is not code.**
//
// `err.password-too-short` and `setup.password` write "8" out in full, because a
// sentence with a number in it translates better if the number is there: in
// German "mindestens 8 Zeichen" does not compose with the same word order as
// English. The price is that `MinPasswordLen` and the catalogues can diverge, and
// what would diverge is **a refusal and its explanation**: a field that refuses
// seven characters saying six are enough.
func TestTheCataloguesQuoteTheRealPasswordMinimum(t *testing.T) {
	n := strconv.Itoa(config.MinPasswordLen)

	for language, cat := range cataloguesOnDisk(t, serverWithoutPassword(t)) {
		for _, k := range []string{"err.password-too-short", "setup.password"} {
			phrase, _ := cat[k].(string)
			if !strings.Contains(phrase, n) {
				t.Errorf("%s: %q does not name the real minimum (%s): %q", language, k, n, phrase)
			}
		}
	}
}
