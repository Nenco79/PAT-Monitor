package server

import (
	"regexp"
	"strings"
	"testing"
)

// stripJSComments removes `//` lines and `/* */` blocks.
//
// **A guard that reads comments finds its own subject written down beside the
// defect**: the remedy to a fault tells its story, and the story names the very
// field the test is refusing. Here the comment above `camOk` says the word
// `ready` four times, which would have made the check below pass over any code
// at all.
func stripJSComments(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// **The check screen must not decide on a latch, and this is the second time.**
//
// `Ready` closes on the first keyframe and never opens again: it answers *did it
// ever start*, and it was being read as *does it work now*. The alert built on
// the same field cost a stream that stopped at 14:58 and was noticed by a person
// three and a half minutes later, with the page green throughout; here it cost a
// row that went green at the first frame and stayed green with the camera
// permission revoked under it — the detail line beside it saying so, the button
// to the Windows page offered underneath, and "Check again" powerless, because
// re-reading a latch gives the latch.
//
// The live half is `measuredFps`, whose window is charged against real time and
// so decays to zero on its own, and `cameraDenied`, which is immediate and is
// the only one of the two that says why.
//
// **Verified to catch**: with `!!s.ready &&` put back at the front of `camOk`
// the test fails naming it; with `measuredFps` taken out, likewise.
func TestTheCheckRowDoesNotDecideOnALatch(t *testing.T) {
	js := stripJSComments(readAsset(t, "onboarding.js"))

	// The declaration, not the prose around it: a search for a sentence passes
	// vacuously the day somebody rewords it.
	decl := regexp.MustCompile(`(?s)const camOk\s*=(.*?);`).FindStringSubmatch(js)
	if decl == nil {
		t.Fatal("no `const camOk = …;` in onboarding.js: this guard is reading " +
			"nothing, and a renamed condition is exactly how it would look")
	}
	cond := decl[1]

	if strings.Contains(cond, "ready") {
		t.Errorf("the camera row decides on `ready`, which is a latch: %q", strings.TrimSpace(cond))
	}
	for _, live := range []string{"measuredFps", "cameraDenied"} {
		if !strings.Contains(cond, live) {
			t.Errorf("the camera row does not read `%s`: %q", live, strings.TrimSpace(cond))
		}
	}

	// The microphone's row is live already — `microphoneActive` is the capture's
	// own answer — and it has to carry the refusal for the same reason the
	// camera's does: the two permissions are two switches, so one can be refused
	// while the other is granted.
	mic := regexp.MustCompile(`(?s)const micOk\s*=(.*?);`).FindStringSubmatch(js)
	if mic == nil {
		t.Fatal("no `const micOk = …;` in onboarding.js")
	}
	for _, live := range []string{"microphoneActive", "microphoneDenied"} {
		if !strings.Contains(mic[1], live) {
			t.Errorf("the microphone row does not read `%s`: %q", live, strings.TrimSpace(mic[1]))
		}
	}
}
