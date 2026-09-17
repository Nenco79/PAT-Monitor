package server

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The stylesheets have no single place where the palette lives: the viewer and
// the login page read `style.css`, which carries the dark in `:root` and the
// earth in `body.day`; the guided path reads `onboarding.css`, which carries
// the same earth in its own `:root`. They are two copies of the same palette,
// and nobody holds them together.
//
// **This is the divergence the project has already paid for**, with `--t-h1`
// written 28 in one sheet and 24 in the other with nothing to say so. Then it
// was one value; here there are eighteen, and for each of them the question asks
// itself.
//
// The two palettes **have** to stay different where they are different — the
// dark and the earth are a choice, not a defect — so the test compares only the
// blocks that declare the **same** palette, the light one.

var (
	reToken   = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	reUsed    = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)\s*([,)])`)
	reSheet   = regexp.MustCompile(`<link[^>]+href="/([a-z0-9.]+\.css)"`)
	reDefined = regexp.MustCompile(`(--[a-z0-9-]+)\s*:`)
	reAlias   = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)\s*\)`)
)

// block extracts a rule's declarations, given its selector.
func block(t *testing.T, css, selector string) map[string]string {
	t.Helper()
	re := regexp.MustCompile(`(?s)(?m)^` + regexp.QuoteMeta(selector) + `\s*\{(.*?)\n\}`)
	m := re.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("the block %q is not found: the test is looking in the wrong place", selector)
	}
	out := map[string]string{}
	for _, d := range reToken.FindAllStringSubmatch(m[1], -1) {
		out[d[1]] = strings.TrimSpace(d[2])
	}
	return out
}

// The light palette is written twice. As long as it is, it has at least to be
// written the same.
func TestTheTwoLightPalettesAgree(t *testing.T) {
	day := block(t, readAsset(t, "style.css"), "body.day")
	guided := block(t, readAsset(t, "onboarding.css"), ":root")

	var common []string
	for k := range day {
		if _, ok := guided[k]; ok {
			common = append(common, k)
		}
	}
	sort.Strings(common)

	// Without this line, a change of selector would turn the test into one that
	// compares nothing and always passes.
	if len(common) < 15 {
		t.Fatalf("only %d tokens in common: the test is no longer looking at the two palettes", len(common))
	}

	// **Colours are compared, not words.** `--phase` is `var(--accent)` on one
	// side and `var(--c-home)` on the other: two spellings of the same colour. A
	// textual comparison would accuse them, and whoever read the error would go
	// and unify two lines that are fine as they are.
	for _, k := range common {
		a, b := resolve(day, day[k]), resolve(guided, guided[k])
		if a != b {
			t.Errorf("%s is %s in style.css and %s in onboarding.css", k, a, b)
		}
	}
}

// resolve replaces the aliases with the colour they arrive at, within the same
// palette.
//
// A `var()` inside a token's declaration resolves **where it is written**, that
// is, in the block containing it: it is precisely the rule that made `/setup`'s
// button come out with the dark's teal. Here it serves the opposite purpose,
// understanding that two different spellings say the same colour.
func resolve(palette map[string]string, v string) string {
	for i := 0; i < 8; i++ {
		m := reAlias.FindStringSubmatch(v)
		if m == nil {
			break
		}
		inner, ok := palette[m[1]]
		if !ok {
			break // not of this palette: left as it is
		}
		v = strings.Replace(v, m[0], inner, 1)
	}
	return strings.TrimSpace(v)
}

// And the direction nobody watches: a token a page uses and none of the sheets
// that page loads declares.
//
// **It does not protest.** The line becomes invalid and vanishes, and what
// remains is the property's default value: it happened with `--radius-s`, which
// lives only in `style.css`, and the language selector had sharp corners in the
// guided path. It was true of `--gap-ico` too, declared in `icons.css`, which
// `/login` and `/setup` did not load although they read the rules in
// `style.css` that use it.
//
// The sheets are not listed here: they are read **from the page**, which is the
// only one that knows which it really loads.
func TestEveryTokenAPageUsesIsDeclaredSomewhereItLoads(t *testing.T) {
	pages, err := webFS.ReadDir("web")
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for _, e := range pages {
		if !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		html := readAsset(t, e.Name())

		sheets := []string{e.Name()}
		for _, m := range reSheet.FindAllStringSubmatch(html, -1) {
			sheets = append(sheets, m[1])
		}
		if len(sheets) == 1 {
			// **One guard per page, not one at the end.** `reSheet` recognises
			// one form of link: change it, and this page would leave the
			// examination **in silence**, and the global guard would stay green
			// thanks to the others. It is the same blindness
			// TestEveryAliasIsRecomputedInTheLightPalette documents three
			// functions further down — looking at what is there says nothing
			// about what is missing — seen from another side.
			//
			// A **stylesheet** is looked for, not any `<link`: a page with only
			// an icon or a manifest has no sheets to satisfy, and accusing it
			// would mean failing the test for a defect of the test.
			if strings.Contains(html, `rel="stylesheet"`) {
				t.Errorf("%s carries a <link but none is recognised: "+
					"the test is leaving the examination instead of doing it", e.Name())
			}
			continue
		}
		seen++

		declared := map[string]bool{}
		for _, f := range sheets {
			for _, m := range reDefined.FindAllStringSubmatch(readAsset(t, f), -1) {
				declared[m[1]] = true
			}
		}
		for _, f := range sheets {
			for _, m := range reUsed.FindAllStringSubmatch(readAsset(t, f), -1) {
				// With a fallback after the comma the absence is expected, not a
				// defect: `var(--phase, var(--ink-2))` already says what to do.
				if m[2] == "," || declared[m[1]] {
					continue
				}
				t.Errorf("%s uses %s (from %s), which no sheet of that page declares",
					e.Name(), m[1], f)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no page examined: the test is looking at nothing")
	}
}

// **An alias declared on `:root` does not follow whoever redefines its target.**
//
// `--accent: var(--c-home)` resolves where it is written: on `:root` it becomes
// the dark's teal at once, and the elements inherit that colour, not the
// reference. `body.day` can rewrite `--c-home` as much as it likes — it no
// longer reaches it. It happened: `/setup`'s "Set password" button, on the cream
// page, came out `#3FA8A4`, that is, the hue tuned for the almost-black.
//
// The test above does not catch it, and that is instructive: removing the fixing
// line, the token **vanishes** from `body.day`, so it leaves the intersection
// and is no longer compared. A test that looks only at what is in both is blind
// to what is missing from one.
//
// The rule is exact and has no thresholds: an alias has to be recomputed **if
// and only if** the token it follows changes between the two palettes.
func TestEveryAliasIsRecomputedInTheLightPalette(t *testing.T) {
	css := readAsset(t, "style.css")
	night := block(t, css, ":root")
	day := block(t, css, "body.day")

	checked := 0
	for name, value := range night {
		m := reAlias.FindStringSubmatch(value)
		if m == nil {
			continue // not an alias: its value depends on nobody
		}
		if _, changes := day[m[1]]; !changes {
			continue // it follows a token the light palette does not touch
		}
		checked++
		if _, ok := day[name]; !ok {
			t.Errorf("%s is %s and the light palette redefines %s, but does not recompute %s: "+
				"the dark's colour remains", name, value, m[1], name)
		}
	}
	if checked == 0 {
		t.Fatal("no alias examined: the test is looking at nothing")
	}
}

// **A downstream sheet rewrites by hand what is not declared where it is
// needed**, and it is the single cause behind three different defects:
// `--radius-s` lived only in `style.css`, so `i18n.css` wrote itself `10px`,
// `onboarding.css` could not use it and invented twelve radii over six sizes,
// and the shadow ended up copied in three places — neutral even where everything
// else is warm.
//
// The cure was declaring the whole scales in every sheet that carries a palette;
// this test is what stops it going back. It does not list the allowed values —
// that would be the second list — but the **shape**: a radius or a shadow comes
// from a token, and the only exceptions are the ones a token cannot express.
func TestNoSheetRewritesTheScaleByHand(t *testing.T) {
	// What is not a step and cannot be one:
	//   999px  the pill — it depends on the height, not on the scale
	//   50%    the circle
	//   none   the absence, which is a decision and not a value
	//
	// **It is judged piece by piece, not as a whole value.** Demanding a single
	// `var()` the test would wrongly accuse an asymmetric radius —
	// `var(--radius) var(--radius) 0 0` for a panel resting on an edge — which
	// has no hand-written steps at all. A literal is still caught: in
	// `0 1px 2px #00000014` the pieces `1px` and `2px` are not allowed.
	allowed := regexp.MustCompile(`^(var\(--[a-z0-9-]+\)|999px|50%|0|none)$`)
	allAllowed := func(v string) bool {
		for _, piece := range strings.Fields(v) {
			if !allowed.MatchString(piece) {
				return false
			}
		}
		return strings.TrimSpace(v) != ""
	}
	// **No anchor at the start of the line.** With `^\s*` a rule written on a
	// single line — `.explainer-body .art { background: …; box-shadow: none; }` —
	// stays invisible: the second declaration is not the first of its line, so it
	// passed green because the test was not looking at it.
	reDecl := regexp.MustCompile(`(?:^|[{;])\s*(border-radius|box-shadow)\s*:\s*([^;}]+)[;}]`)

	// **And the pages are watched too, not only the sheets.** The markup carries
	// `style=` by a declared choice — the blades of grass have their own
	// animation duration in there — so a radius written in an attribute would be
	// invisible to a test that scans only the `.css`. There are none today:
	// which is the reason to add the line now, not later.
	//
	// The SVGs' `rx`/`ry` deliberately stay out, and that is not an oversight:
	// they are the drawing's geometry in its `viewBox` units, not screen pixels,
	// and mapping them onto a step of the scale would mean deforming the
	// illustration to make it resemble the interface.
	reInline := regexp.MustCompile(`style="([^"]*)"`)
	seen := 0
	look := func(name, text string) {
		for _, m := range reDecl.FindAllStringSubmatch(text, -1) {
			seen++
			if v := strings.TrimSpace(m[2]); !allAllowed(v) {
				t.Errorf("%s: %s is %q instead of a token", name, m[1], v)
			}
		}
	}

	sheets, err := webFS.ReadDir("web")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range sheets {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".css"):
			look(name, readAsset(t, name))
		case strings.HasSuffix(name, ".html"):
			for _, m := range reInline.FindAllStringSubmatch(readAsset(t, name), -1) {
				// An attribute has no lines: it is read one declaration at a
				// time, with the trailing semicolon `reDecl` asks for.
				for _, d := range strings.Split(m[1], ";") {
					if strings.TrimSpace(d) != "" {
						look(name+" (style=)", "\n"+strings.TrimSpace(d)+";")
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no declaration examined: the test is looking at nothing")
	}
}

// **A token that exists on one side and is missing on the other is what forces
// hand-writing later.** It is `--radius-s`'s story, declared only in
// `style.css`: `i18n.css` could not use it and wrote itself `10px`,
// `onboarding.css` could not either and invented six sizes. Before that it had
// happened to `--t-h1`, 28 in one sheet and 24 in the other.
//
// The tests that existed do not catch it, and for a precise reason:
// `TestNoSheetRewritesTheScaleByHand` sees the literal **after** somebody has
// written it, and `TestEveryTokenAPageUsesIsDeclaredSomewhereItLoads` sees only
// the tokens somebody actually uses — a `--radius-xxs` declared in one sheet
// alone passes undisturbed until the day it is needed on the other side.
//
// The **names** are compared, not the values: the two palettes' values have to
// differ, and it is the other test that watches them.
//
// The exceptions are few, argued one by one, and they are the part to read
// before lengthening it: a new name in here goes in only if declaring it in the
// other sheet would mean **inventing** something, not copying it.
var onlyOnOneSide = map[string]string{
	"--veil": "the veil over the video: the configuration page has nothing to " +
		"veil, and a value invented for symmetry would be dead weight",
	"--c-choice": "the plum of the fork: it is a step that exists only in the " +
		"guided path, and giving it a version for the dark would mean choosing a " +
		"colour, not completing a scale",
	// **By name and not by prefix.** `--preview-` exempted for ever any future
	// name, and among those there would be a `--preview-radius`, which instead
	// has to be shared like every other radius: an exemption covering things not
	// yet written is not an argued exception, it is a hole with a name. Three
	// lines instead of one, and it is the right price.
	"--c-rec": "the red of the recording dot: in the guided path there is nothing " +
		"to record, and giving it a version for the earth would mean choosing a " +
		"colour, not completing a scale",
	"--preview-foot":  "the dark island under the preview: a surface of a component that lives on one page",
	"--preview-track": "as above, the track of the preview's level meter",
	"--preview-ink":   "as above, the reading in decibels on that band",
	"--tray-wash": "the accent wash of the drawn tray panel: the mixture the tray " +
		"computes for the command with the focus, in a figure that lives on one page",
}

func exempt(name string) (string, bool) {
	r, ok := onlyOnOneSide[name]
	return r, ok
}

func TestBothPaletteSheetsDeclareTheSameNames(t *testing.T) {
	names := func(css string, selectors ...string) map[string]bool {
		out := map[string]bool{}
		for _, s := range selectors {
			for k := range block(t, css, s) {
				out[k] = true
			}
		}
		return out
	}
	// For `style.css` both blocks count: the palette is one thing written in two
	// pieces, and `--c-home` is in both.
	here := names(readAsset(t, "style.css"), ":root", "body.day")
	there := names(readAsset(t, "onboarding.css"), ":root")

	if len(here) < 30 || len(there) < 30 {
		t.Fatalf("%d and %d names read: the test is not looking at the palettes", len(here), len(there))
	}

	missing := func(where string, a, b map[string]bool) {
		var out []string
		for k := range a {
			if !b[k] {
				if _, ok := exempt(k); !ok {
					out = append(out, k)
				}
			}
		}
		sort.Strings(out)
		for _, k := range out {
			t.Errorf("%s declares %s and the other sheet does not: either it is declared in "+
				"both, or it goes in onlyOnOneSide with the reason", where, k)
		}
	}
	missing("style.css", here, there)
	missing("onboarding.css", there, here)
}

// **And inside `style.css` the same shape, one level deeper.** The test above
// merges `:root` and `body.day` into one set, and rightly so — for a sheet
// with two palettes the palette is that sum. But that way a token declared
// **only** in `body.day` and absent from `:root` passes: it is in the sum, so
// the comparison with the other sheet is satisfied.
//
// And nobody else catches it.
// `TestEveryTokenAPageUsesIsDeclaredSomewhereItLoads` gathers the declared ones
// from the whole sheet without looking at which block they are in, so it counts
// it as declared; `TestEveryAliasIsRecomputedInTheLightPalette` looks at aliases
// alone. A non-alias token present by day and absent by night would make its
// line invalid on the **night pages** — the viewer and the login — with nothing
// to protest. It is `--radius-s` again, inside one sheet rather than between
// two.
//
// The opposite direction is **not** demanded: `:root` carries everything that
// does not change with the light — the type scale, the spacing, the radii, the
// stage — and `body.day` rewrites only what the light changes. Demanding full
// parity would mean redeclaring forty identical values.
func TestTheDayPaletteOverridesNothingTheNightOneLacks(t *testing.T) {
	css := readAsset(t, "style.css")
	night := block(t, css, ":root")
	day := block(t, css, "body.day")

	if len(night) < 30 || len(day) < 15 {
		t.Fatalf("%d and %d tokens read: the test is not looking at the two blocks",
			len(night), len(day))
	}

	var out []string
	for k := range day {
		if _, ok := night[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	for _, k := range out {
		t.Errorf("body.day declares %s and :root does not: on the night pages that "+
			"line is invalid, and nobody says so", k)
	}
}

// **The `font` shorthand and a token do not go together, and the failure is
// silent.**
//
// Reported from a phone photograph of a browser this machine has not got: the
// details panel's group headings came out at the size an `h2` has when nobody
// styles it. Measured off the picture — rows at 13 px, headings at ~19.5,
// which is exactly `1.5em` of 13 — so the declaration
// `font: 600 var(--t-micro)/1.4 var(--sans)` was not applied at all there,
// while on the same version an iPhone renders it right.
//
// It costs nothing to be safe from it: the same four properties written as
// longhands cannot be dropped as a group, and where one of them fails the other
// three still hold. Verified to change nothing here — the computed font of
// every element of the three pages, 354 of them, is identical before and after
// the rewrite — which is the only way to make a sweep of this size without
// trusting it.
//
// **And an element with a rule of its own in the browser is where it shows.**
// On a `<span>` a dropped declaration leaves the inherited size and nobody
// notices; on an `h2`, a `<button>` or an `<input>` the browser has an opinion
// of its own, and that is what appears.
func TestNoFontShorthandCarriesAToken(t *testing.T) {
	sheets, err := fs.Glob(webFS, "web/*.css")
	if err != nil || len(sheets) == 0 {
		t.Fatalf("no stylesheet found: %v", err)
	}
	reShorthand := regexp.MustCompile(`(^|[^-])font:\s*[^;]*var\(`)
	longhands := 0
	for _, sheet := range sheets {
		// The comment above tells this story and writes the shorthand while
		// telling it.
		css := reCSSComments.ReplaceAllString(readAsset(t, strings.TrimPrefix(sheet, "web/")), "")
		for _, line := range strings.Split(css, "\n") {
			if reShorthand.MatchString(line) {
				t.Errorf("%s: %q writes the font as a shorthand carrying a token: "+
					"where that form is not parsed the whole declaration goes, and "+
					"the browser's own rule for that element appears instead",
					sheet, strings.TrimSpace(line))
			}
			if strings.Contains(line, "font-size: var(") {
				longhands++
			}
		}
	}
	// The floor: a guard that has stopped reading the sheets passes green, and
	// this one is a search for something that must not be there.
	if longhands < 20 {
		t.Errorf("only %d font sizes read from a token: the guard is no longer "+
			"reading the sheets", longhands)
	}
}
