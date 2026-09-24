package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The viewer's bar, and the selector that stopped matching.
//
// **`display: contents` removes the box, not the node.** The bar's two sides —
// `.side` — exist only on the single row, where they become the columns the
// three detectors sit centred between; below the threshold they are
// `display: contents`, so the two-row layout is identical to the previous one,
// measured pixel by pixel. But **selectors look at the DOM**, and the node is
// there: `.bar > button`, which on the phone tightens the padding of the four
// commands, stopped matching them the day they ended up inside a side.
//
// It gave no error. At 390 px the bar went from 106 to 154 pixels tall — a third
// row — and the only thing that said so was the comparison with the measurement
// taken before anything was touched.
//
// This test holds the structure those selectors rest on, in the two ways it
// breaks: removing the sides, and adding a command outside a side. It is not a
// duplicate of the measurement — that one is redone by hand driving a browser,
// and nobody redoes it before a commit.

var (
	reBar         = regexp.MustCompile(`(?s)<div class="bar">(.*?)\n  </div>`)
	reButton      = regexp.MustCompile(`<button[^>]*\bid="([a-z-]+)"`)
	reComments    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reCSSComments = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reOpen        = regexp.MustCompile(`<div class="(side|side end|detectors)"`)
	reClose       = regexp.MustCompile(`^\s*</div>`)
	reHoverMedia  = regexp.MustCompile(`@media[^{]*\(\s*hover\s*:\s*hover\s*\)`)
)

// TestEveryCommandOfTheBarSitsInASide demands that no button of the bar be a
// direct child of the bar.
func TestEveryCommandOfTheBarSitsInASide(t *testing.T) {
	page := readAsset(t, "index.html")
	m := reBar.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the bar is not found: the test is looking in the wrong place")
	}
	// The comments name the buttons and the classes on purpose, to tell why
	// they sit where they sit: they are removed before reading the structure.
	bar := reComments.ReplaceAllString(m[1], "")

	inside := 0
	var orphans []string
	for line := range strings.SplitSeq(bar, "\n") {
		if reOpen.MatchString(line) {
			inside++
		}
		for _, b := range reButton.FindAllStringSubmatch(line, -1) {
			if inside == 0 {
				orphans = append(orphans, b[1])
			}
		}
		if reClose.MatchString(line) && inside > 0 {
			inside--
		}
	}
	for _, o := range orphans {
		t.Errorf("the button %q is a direct child of the bar: on the single row "+
			"it becomes a fourth column in a grid of three, and on the phone "+
			"`.side > button` does not tighten it", o)
	}

	// The other direction: without the sides the test above would pass only
	// because there is nothing left to match.
	for _, class := range []string{`<div class="side">`, `<div class="side end">`} {
		if !strings.Contains(bar, class) {
			t.Errorf("%s is missing: the single row has three columns and wants three children", class)
		}
	}
}

// **The selector has to name the side, not the bar.**
//
// It is the other half: the structure can stay right and the sheet go back to
// `.bar > button` in a reshuffle, which is exactly what happened. A child
// combinator does not reach through a `display: contents`.
func TestTheNarrowRulesReachThroughTheSides(t *testing.T) {
	// **The comments are removed before searching**, and it is the same care as
	// the tray's key guard: the comment beside the rule tells this story, so it
	// names on purpose the selector that must not be there. Without this line
	// the test accuses the remedy.
	css := reCSSComments.ReplaceAllString(readAsset(t, "style.css"), "")
	if strings.Contains(css, ".bar > button") {
		t.Error(".bar > button no longer matches anything: the commands sit inside " +
			"`.side`, and a selector that stops matching gives no error at all")
	}
	// And the rules have to be there. They are two and they are not the same
	// kind of thing, so they are asked for one by one: a count alone stays at
	// two while one of them turns into something else, which is what happened
	// when the 400 px padding rule went and the grow factor arrived.
	if !strings.Contains(css, ".side > button { padding:") {
		t.Error("the commands' narrow padding is gone: it is what decides how " +
			"early a label breaks out of its own pill on equal parts")
	}
	if !strings.Contains(css, ".viewer:not(.fs) .side > button { flex: 1 1 0; min-width: 0; }") {
		t.Error("the commands no longer share the row in equal parts, or they " +
			"share it in full screen too, where `.side` is a column and a grow " +
			"factor stretches the pills down the height of the picture")
	}
}

// **A tap leaves the element hovered, and a touch screen has no way to take it
// back.**
//
// Reported from a phone: "Details" stayed lit after being pressed, the colour
// Windows gives the control under the mouse — and that is what it was, the same
// `:hover` rule on a device with no pointer to move away. Every hover in these
// sheets therefore sits inside `@media (hover: hover)`, which describes the
// **primary** pointer: a laptop with a touchscreen keeps its hover, a phone
// never gets one.
//
// The guard reads the sheets because there is no value to look at: the rule is
// correct, the device is the thing that differs, and no test on a colour
// separates them. `:active` is deliberately not in scope — it lasts as long as
// the finger, which is the feedback a touch screen does want.
func TestNoHoverIsDeclaredOutsideAPointer(t *testing.T) {
	// **The list of sheets is read from the embedded folder, not written
	// here.** A hand-written list watches what somebody remembered: the fifth
	// stylesheet is exactly the one that would be added and not added here,
	// and the guard would go on passing over it.
	sheets, err := fs.Glob(webFS, "web/*.css")
	if err != nil || len(sheets) == 0 {
		t.Fatalf("no stylesheet found: %v", err)
	}
	guarded := 0
	for _, sheet := range sheets {
		// The comments tell this story and name `:hover` while telling it.
		css := reCSSComments.ReplaceAllString(readAsset(t, strings.TrimPrefix(sheet, "web/")), "")
		out, inside := hoverSelectorsOutsideAPointer(css)
		guarded += inside
		for _, sel := range out {
			// The picker's own menu is the one exception, and it is an
			// exception by name and not by prefix: those rows are `option`s of
			// an open `<select>`, the menu closes on the choice, so nothing is
			// left lit behind it — and the hover half shares its declaration
			// with the `:focus` half, which a touch screen does need.
			if strings.Contains(sel, "option") {
				continue
			}
			t.Errorf("%s: %q is outside `@media (hover: hover)`: on a phone it "+
				"stays on after the tap that switched it on", sheet, sel)
		}
	}
	// **And a floor, because this guard reads lines.** A reformat, a build
	// that joins them, a media query written in a form the pattern misses:
	// the parser stops matching, finds nothing, and passes green covering
	// nothing. It is the shape `TestNoCOMErrorIsWrappedUndescribed` already
	// carries — a guard that matches nothing must fail, not pass.
	if guarded < 8 {
		t.Errorf("only %d hover rules found inside a pointer block: the guard "+
			"has stopped reading the sheets, and green here means nothing", guarded)
	}
}

// hoverSelectorsOutsideAPointer returns the selectors carrying `:hover` that do
// not sit inside a `hover: hover` block, and how many do sit inside one. The
// second number is what lets the caller tell "nothing is wrong" from "nothing
// was read". It counts braces rather than parsing: what is needed is which
// block a line is in, and the sheets are hand-written with one selector per
// line.
func hoverSelectorsOutsideAPointer(css string) ([]string, int) {
	var out []string
	inside := 0
	depthOfPointer := -1
	depth := 0
	for line := range strings.SplitSeq(css, "\n") {
		if depthOfPointer < 0 && reHoverMedia.MatchString(line) {
			depthOfPointer = depth
		}
		if strings.Contains(line, ":hover") {
			if depthOfPointer < 0 {
				out = append(out, strings.TrimSpace(line))
			} else {
				inside++
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depthOfPointer >= 0 && depth <= depthOfPointer {
			depthOfPointer = -1
		}
	}
	return out, inside
}
