package main

import (
	"strings"
	"testing"
)

// The prerequisite text comes from Tailscale, and it is not written to sit in a
// menu item: it can have several lines and it can be long. The two things that
// matter are that no line break ends up inside it, and that it does not exceed
// the width.
func TestFirstLineKeepsOnlyTheFirstLine(t *testing.T) {
	in := "HTTPS is not enabled for this network.\nYou can enable it from the panel."
	got := firstLine(in)
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("a line break ended up in a menu item: %q", got)
	}
	if got != "HTTPS is not enabled for this network." {
		t.Errorf("firstLine = %q", got)
	}
}

func TestFirstLineShortensAndSaysSo(t *testing.T) {
	in := strings.Repeat("a", 200)
	got := firstLine(in)
	if n := len([]rune(got)); n > 60 {
		t.Errorf("menu item %d characters long, the limit is 60", n)
	}
	// **The truncation declares itself.** A sentence cut without an ellipsis
	// reads as a finished sentence, and that is how a tool which stops saying
	// everything makes what it describes look broken.
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated without saying so: %q", got)
	}
}

// A text that fits must not be touched: adding an ellipsis to a whole sentence
// would say something is missing when nothing is.
func TestFirstLineLeavesWhatFitsAlone(t *testing.T) {
	in := "Authorise this computer"
	if got := firstLine(in); got != in {
		t.Errorf("firstLine(%q) = %q, it should have stayed as it was", in, got)
	}
}

// Windows breaks lines with CRLF: looking only for \n would leave a trailing
// \r, which in a menu item shows as a rectangle.
func TestFirstLineCopesWithCRLF(t *testing.T) {
	got := firstLine("first line\r\nsecond line")
	if got != "first line" {
		t.Errorf("firstLine with CRLF = %q", got)
	}
}
