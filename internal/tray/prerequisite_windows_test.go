//go:build windows

package tray

import (
	"strings"
	"testing"
)

// The prerequisite text comes from Tailscale, and it is not written to sit in
// this panel: it can have several lines and it can be long. What it has to
// become is one run of words, because its destination is a box that wraps.
//
// **The line break is the half that still matters.** `DrawTextW` with
// `DT_WORDBREAK` honours an embedded newline, so one left in would break the
// row where Tailscale's paragraph breaks and not where the panel's width says
// — that is, it would spend a row of three on a break nobody here chose.
func TestThePrerequisiteBecomesOneParagraph(t *testing.T) {
	in := "HTTPS is not enabled for this network.\nYou can enable it from the panel."
	got := oneParagraph(in)
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("a line break survived into a wrapped row: %q", got)
	}
	// **And the second half is kept**, which is the change: it used to take the
	// first line and drop the rest, and in Tailscale's longest message the
	// sentence that says what to do is the second one.
	if got != "HTTPS is not enabled for this network. You can enable it from the panel." {
		t.Errorf("oneParagraph = %q", got)
	}
}

// Windows breaks lines with CRLF, and a lone carriage return left behind shows
// as a rectangle.
func TestThePrerequisiteCopesWithCRLF(t *testing.T) {
	if got := oneParagraph("first line\r\nsecond line"); got != "first line second line" {
		t.Errorf("oneParagraph with CRLF = %q", got)
	}
}

// **The French non-breaking space survives, and `strings.Fields` was what
// destroyed it.** `unicode.IsSpace` answers true for U+00A0, so the obvious
// spelling turned every one of them into an ordinary space — undoing, at the
// last step before drawing, exactly what the catalogues are guarded for, and
// leaving the panel free to end a row on a bare colon. Three of the six French
// sentences that reach this row carry one.
//
// **Verified to catch**: with `strings.Join(strings.Fields(s), " ")` put back,
// this fails.
func TestTheNonBreakingSpaceSurvivesTheFlattening(t *testing.T) {
	const in = "Aucune action requise : cela peut prendre\nune minute."
	got := oneParagraph(in)
	if !strings.ContainsRune(got, ' ') {
		t.Errorf("the non-breaking space was collapsed: %q", got)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("a line break survived: %q", got)
	}
	if got != "Aucune action requise : cela peut prendre une minute." {
		t.Errorf("oneParagraph = %q", got)
	}
}

// **Nothing is cut here, and that is the point of where this now lives.**
//
// Sixty characters was the width of a menu item, and the text is drawn in rows
// that wrap: the cut arrived before the room did, so the sentence reached the
// panel already truncated with two of its three rows unused. What bounds it is
// the panel — `maxStatusRows` and `DT_END_ELLIPSIS` — which cuts at the end of
// the last row it can give.
//
// **Verified to catch**: with the sixty-rune cap put back, this fails.
func TestThePrerequisiteIsNotShortenedHere(t *testing.T) {
	in := strings.Repeat("parola ", 60)
	got := oneParagraph(in)
	if strings.HasSuffix(got, "…") {
		t.Error("shortened with an ellipsis: the panel decides what does not fit, " +
			"and it has three rows to say it in")
	}
	if n := len(strings.Fields(got)); n != 60 {
		t.Errorf("%d words of 60 survived: something is still cutting here", n)
	}
}

// A text that is already one line must come back untouched: a program that
// rewrites what did not need rewriting is one whose output nobody can compare.
func TestThePrerequisiteLeavesOneLineAlone(t *testing.T) {
	in := "Authorise this computer"
	if got := oneParagraph(in); got != in {
		t.Errorf("oneParagraph(%q) = %q, it should have stayed as it was", in, got)
	}
}
