//go:build windows

package tray

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The panel's palette and the icon's are **a third and a fourth copy** of the
// colours that live in the stylesheets, and the comment beside each one says
// exactly which token it is copying.
//
// A comment, though, is not a constraint: change a hue in the sheet and nothing
// happens here, and the result is an icon of one colour and the page open beside
// it of another — that is, the very thing those colours exist to avoid, "looking
// like the same program".
//
// So the question is put to the sheet, which is the original. It is not read
// from the embedded assets because those live in another package: the file is
// read, and if one day it moves the test says so instead of passing.
const paletteSheet = "../server/web/style.css"

var (
	reTokenP = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*(#[0-9A-Fa-f]{6})\s*;`)
	reAliasP = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*var\(\s*(--[a-z0-9-]+)\s*\)\s*;`)
)

// cssPalette reads the colours of one block of the sheet, aliases resolved.
func cssPalette(t *testing.T, selector string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(paletteSheet)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", paletteSheet, err)
	}
	re := regexp.MustCompile(`(?s)(?m)^` + regexp.QuoteMeta(selector) + `\s*\{(.*?)\n\}`)
	m := re.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("the block %q is not in %s", selector, paletteSheet)
	}
	out := map[string]string{}
	for _, d := range reTokenP.FindAllStringSubmatch(m[1], -1) {
		out[d[1]] = strings.ToUpper(d[2])
	}
	// Aliases are resolved afterwards, so that `--accent: var(--c-home)`
	// carries a colour here too.
	for range 4 {
		for _, d := range reAliasP.FindAllStringSubmatch(m[1], -1) {
			if v, ok := out[d[2]]; ok {
				out[d[1]] = v
			}
		}
	}
	return out
}

func hexOf(c uint32) string {
	return strings.ToUpper(fmt.Sprintf("#%02X%02X%02X", c&0xFF, (c>>8)&0xFF, (c>>16)&0xFF))
}

func compare(t *testing.T, where string, css map[string]string, pairs map[string]string) {
	t.Helper()
	for token, inGo := range pairs {
		want, ok := css[token]
		if !ok {
			t.Errorf("%s: the sheet no longer declares %s", where, token)
			continue
		}
		if inGo != want {
			t.Errorf("%s: %s is %s in the sheet and %s in the code", where, token, want, inGo)
		}
	}
}

// The tray panel, in both its faces.
func TestTheFlyoutPaletteMatchesTheStylesheet(t *testing.T) {
	dark := cssPalette(t, ":root")
	compare(t, "palDark", dark, map[string]string{
		"--ground": hexOf(palDark.ground),
		"--card":   hexOf(palDark.card),
		"--line":   hexOf(palDark.line),
		"--ink":    hexOf(palDark.ink),
		"--muted":  hexOf(palDark.muted),
		"--c-home": hexOf(palDark.accent),
	})

	day := cssPalette(t, "body.day")
	compare(t, "palLight", day, map[string]string{
		"--ground": hexOf(palLight.ground),
		"--card":   hexOf(palLight.card),
		"--line":   hexOf(palLight.line),
		"--ink":    hexOf(palLight.ink),
		"--muted":  hexOf(palLight.muted),
		"--c-home": hexOf(palLight.accent),
	})
}

// And the icon, which takes the deeper hues — those of the light palette —
// because it has to hold up on a light taskbar too.
//
// `PhaseStarting` stays out: the stone is a colour of its own, it is not in the
// sheets, and demanding it here would mean inventing one to make it pass.
func TestTheTrayIconUsesThePaletteHues(t *testing.T) {
	day := cssPalette(t, "body.day")
	pairs := map[Phase]string{
		PhaseHome:    "--c-home",
		PhaseOutside: "--c-ready",
		PhaseCheck:   "--c-note",
		PhaseFault:   "--c-stop",
	}
	for phase, token := range pairs {
		c := phaseColor[phase]
		got := strings.ToUpper(fmt.Sprintf("#%02X%02X%02X", int(c[0]), int(c[1]), int(c[2])))
		if want := day[token]; got != want {
			t.Errorf("phase %v is %s and %s is %s", phase, got, token, want)
		}
	}

	// The crescent is the cream of the cards, and the comment beside it says so.
	moon := strings.ToUpper(fmt.Sprintf("#%02X%02X%02X", int(moonColor[0]), int(moonColor[1]), int(moonColor[2])))
	if want := day["--card"]; moon != want {
		t.Errorf("the crescent is %s and --card is %s", moon, want)
	}
}
