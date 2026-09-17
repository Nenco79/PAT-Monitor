package server

import (
	"strings"
	"testing"

	"patmonitor/internal/tunnel"
)

// **The phases are a contract between Go and the two pages, and no compiler
// checks it.**
//
// `tunnel.Phase` crosses the JSON and the JavaScript compares it: nine places
// between `app.js` and `onboarding.js` decide what to show from that value.
// While they were sentences the defect was latent — a translation would have
// switched them off silently, leaving the remote-access panel grey and the
// onboarding stuck at step 3. They became codes, and this test exists because
// **the real defect was not the language: it was that the two halves could
// diverge with nothing to say so.**
//
// Changing the value of a constant below and not touching the pages makes this
// test fail, which is the only place the thing is noticed before whoever
// installs the monitor notices it.
var expectedPhases = []tunnel.Phase{
	tunnel.PhaseOff,
	tunnel.PhaseStarting,
	tunnel.PhaseNeedsLogin,
	tunnel.PhaseNeedsFunnel,
	tunnel.PhaseNeedsApproval,
	tunnel.PhaseCertificate,
	tunnel.PhaseRunning,
	tunnel.PhaseError,
}

func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := webFS.ReadFile("web/" + name)
	if err != nil {
		t.Fatalf("asset %s: %v", name, err)
	}
	return string(b)
}

// Every phase has to be named by at least one of the two pages.
//
// **It is not enough that the JavaScript compiles**: a `case` left with the old
// value is valid code that never fires.
func TestEveryTunnelPhaseIsNamedByThePages(t *testing.T) {
	app := readAsset(t, "app.js")
	onb := readAsset(t, "onboarding.js")

	for _, f := range expectedPhases {
		q := "'" + string(f) + "'"
		if !strings.Contains(app, q) && !strings.Contains(onb, q) {
			t.Errorf("the phase %q appears in neither page", f)
		}
	}
}

// The viewer's badge prints a word, not the code, and what guarantees that now
// is the catalogue.
//
// There used to be a `TestEveryPhaseHasAWordInTheViewer` here, which looked
// inside `app.js`'s `FASI` map. That map no longer exists: the phase became a
// key (`viewer.phase.<code>`) and the word lives in the language catalogues. The
// same question is now asked by `TestEveryKeyIsInEveryCatalogue`, which expands
// that prefix with the authoritative list of phases and demands an entry **in
// every language** rather than in one map — that is, it asks for more.
//
// The test is removed rather than adapted: two tests of the same thing diverge,
// and the weaker one is the one that ends up being believed.

// And none of the old sentences must have survived as a comparison.
//
// It is the direction that gets forgotten: adding the new code without removing
// the old branch leaves two `case`s, one of them dead, and the dead one looks
// alive.
//
// **The `FASI` map is removed before looking**, and the first draft did not do
// that: those sentences belong in there, because they are the text being shown,
// not the value being compared. The test accused all five of them, which is the
// proof that it looks where it should — it only had to look **how**, as well.
func TestNoOldItalianPhaseSurvivesInThePages(t *testing.T) {
	// "attivo" and "errore" are there, and they are the two that count: they
	// are short, they look like ordinary words, and they are the ones a hand
	// edit would bring back without raising suspicion. Outside the map they must
	// appear nowhere.
	old := []string{
		"disattivato", "avvio in corso", "da collegare al tailnet",
		"da abilitare su Tailscale", "in attesa di approvazione",
		"collegamento sicuro in preparazione", "attivo", "errore",
	}
	for _, name := range []string{"app.js", "onboarding.js"} {
		src := withoutPhaseMap(readAsset(t, name))
		for _, v := range old {
			if strings.Contains(src, "'"+v+"'") {
				t.Errorf("%s still compares against the old phase %q", name, v)
			}
		}
	}
}

// withoutPhaseMap removes the code-to-word dictionary, which is the only place
// where the phases' Italian sentences are allowed to appear.
func withoutPhaseMap(src string) string {
	i := strings.Index(src, "const FASI = {")
	if i < 0 {
		return src
	}
	j := strings.Index(src[i:], "};")
	if j < 0 {
		return src
	}
	return src[:i] + src[i+j:]
}

// The signalling reasons have **three** consumers, and the first draft updated
// one.
//
// Changing `Message: err.Error()` into `Reason: <code>`, the viewer was brought
// into line and `onboarding.js` and `pat-viewer` were not: the first said
// "preview failed: monitor error" for any cause, the second printed "error from
// the server: " with nothing after it. Neither is a functional fault, and that
// is why they survived a rereading: **it only shows when the diagnosis is
// wanted**, that is, late.
//
// The pages have to name every code; `pat-viewer` does not, because it prints
// them bare — it is a tool, not an interface.
func TestBothPagesKnowEverySignallingReason(t *testing.T) {
	reasons := []string{reasonNotReady, reasonSDP}
	for _, name := range []string{"app.js", "onboarding.js"} {
		src := readAsset(t, name)
		for _, m := range reasons {
			// The key may be bare or quoted, as for the phases: in JavaScript
			// `sdp:` is valid and `'not-ready':` is not. Looking for one form
			// alone makes the test fail on correct code, which is the quickest
			// way of getting somebody to switch it off.
			if !strings.Contains(src, "'"+m+"'") && !strings.Contains(src, "\n        "+m+":") {
				t.Errorf("%s does not know what to say for the reason %q", name, m)
			}
		}
	}
}
