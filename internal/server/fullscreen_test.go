package server

import (
	"regexp"
	"strings"
	"testing"
)

// The full-screen rail, and the two ways it breaks without a word from anyone.
//
// **The rail is the bar, restyled.** There is no second set of buttons — the
// same nodes, the same ids, the same listeners — so the mode inherits every
// command the bar gains, including the ones nobody thought about it for. On the
// rail the word is clipped and the glyph is all there is, and that is where the
// two silent defects live: a command with no glyph becomes an empty circle, and
// a word removed instead of clipped becomes a button a screen reader cannot
// name. Neither shows in normal mode, and neither gives an error.

var (
	reFSHidden = regexp.MustCompile(`\.viewer\.fs\s+#([a-z-]+)`)
	reCSSRule  = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
)

// TestEveryCommandOnTheRailHasAGlyph requires each of the bar's buttons either
// to carry an `<svg>` or to be hidden in full screen — and **both halves are
// read from the source**, so adding a command forces the choice instead of
// leaving a blank pill on the rail. A list written here would be the second
// list that diverges.
func TestEveryCommandOnTheRailHasAGlyph(t *testing.T) {
	page := readAsset(t, "index.html")
	css := readAsset(t, "style.css")

	hidden := hiddenInFullScreen(css)
	if len(hidden) == 0 {
		t.Fatal("no button is hidden in full screen: either the rule has gone, " +
			"or this test has stopped recognising it — and then it would absolve " +
			"every button below")
	}

	m := reBar.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the bar is not found: the test is looking in the wrong place")
	}
	bar := reComments.ReplaceAllString(m[1], "")

	// One button at a time, from its `<button` to the matching `</button>`.
	checked := 0
	for _, chunk := range strings.Split(bar, "<button")[1:] {
		id := reButton.FindStringSubmatch("<button" + chunk)
		if id == nil {
			continue
		}
		end := strings.Index(chunk, "</button>")
		if end < 0 {
			t.Errorf("the button %q is not closed in the bar", id[1])
			continue
		}
		body := chunk[:end]
		checked++
		if hidden[id[1]] {
			continue
		}
		if !strings.Contains(body, "<svg") {
			t.Errorf("the command %q reaches the full-screen rail with no glyph: "+
				"there the word is clipped, so it would be an empty pill. Give it "+
				"an icon, or hide it in full screen as `details` and `logout` are",
				id[1])
		}
	}
	if checked < 7 {
		t.Fatalf("only %d buttons were examined: the guard has stopped finding "+
			"them, and a guard that matches nothing passes", checked)
	}

	// **And the one that floats on the picture is examined too.** It is not in
	// the bar — it sits on the stage, bottom right — so the loop above cannot
	// see it, and it is the button with the least room of all for a word.
	onVideo := 0
	for _, chunk := range strings.Split(page, "<button")[1:] {
		if !strings.Contains(strings.SplitN(chunk, ">", 2)[0], "on-video") {
			continue
		}
		onVideo++
		end := strings.Index(chunk, "</button>")
		if end < 0 || !strings.Contains(chunk[:end], "<svg") {
			t.Error("a control floating on the picture has no glyph: there the " +
				"word is clipped, so it would be an empty pill over the video")
		}
	}
	if onVideo == 0 {
		t.Error("no control floats on the picture: full screen is reached from " +
			"there, and if that button has gone the mode cannot be entered at all")
	}
}

// TestTheRailClipsTheWordAndDoesNotRemoveIt is the other half. `display: none`
// on those spans is shorter, looks identical on screen, and takes the accessible
// name with it: seven buttons a screen reader cannot tell apart, on the one
// surface of this page that is nothing but glyphs. `title` is no substitute —
// on a touch screen it never appears at all.
func TestTheRailClipsTheWordAndDoesNotRemoveIt(t *testing.T) {
	css := readAsset(t, "style.css")
	found := false
	for _, r := range reCSSRule.FindAllStringSubmatch(stripCSSComments(css), -1) {
		sel, body := r[1], r[2]
		if !strings.Contains(sel, ".viewer.fs .bar button > span") {
			continue
		}
		found = true
		if strings.Contains(body, "display: none") {
			t.Error("the rail's words are removed instead of clipped: the buttons " +
				"lose their accessible name, and the screen looks exactly the same")
		}
		if !strings.Contains(body, "clip-path") {
			t.Error("the rail's words are not clipped: without it they are either " +
				"visible or gone, and there is no third state")
		}
	}
	if !found {
		t.Error("the rule that hides the rail's words is not there: the rail " +
			"would carry the bar's labels, which do not fit a 48 px pill")
	}
}

// hiddenInFullScreen reads which ids the sheet takes out of the mode.
func hiddenInFullScreen(css string) map[string]bool {
	out := map[string]bool{}
	for _, r := range reCSSRule.FindAllStringSubmatch(stripCSSComments(css), -1) {
		sel, body := r[1], r[2]
		if !strings.Contains(body, "display: none") {
			continue
		}
		for _, id := range reFSHidden.FindAllStringSubmatch(sel, -1) {
			out[id[1]] = true
		}
	}
	return out
}

func stripCSSComments(css string) string {
	return reCSSComments.ReplaceAllString(css, "")
}
