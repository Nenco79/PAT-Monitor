//go:build windows

package tray

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"patmonitor/internal/i18n"
)

// panelRoom is the width everything full-width in this panel gets: the panel
// less its two paddings, at 96 dpi. The two guards below are the same question
// asked of the two things that live in it.
func panelRoom(f *flyout) int32 { return f.px(flyWidth) - 2*f.px(flyPadding) }

// measureBox is how tall a text is when wrapped into that width, in pixels.
func measureBox(hdc uintptr, font windows.Handle, s string, width int32, wrap bool) rect {
	if font != 0 {
		procSelectObject.Call(hdc, uintptr(font))
	}
	flags := uint32(dtCalcRect | dtNoPrefix)
	if wrap {
		flags |= dtWordBreak
	} else {
		flags |= dtSingleLine
	}
	r := rect{0, 0, width, 0}
	txt, _ := windows.UTF16FromString(s)
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
		uintptr(unsafe.Pointer(&r)), uintptr(flags))
	return r
}

// panelDC hands over a device context and the fonts the panel draws with, or
// skips: on a machine with no GDI there is nothing to measure and nothing to
// pretend.
func panelDC(t *testing.T) (*flyout, uintptr, func()) {
	t.Helper()
	// **The same fonts `create` uses, and they are the point.** A guard that
	// measures with a font the panel does not draw with is measuring something
	// else: this list was written by hand and had `fontGhost` at `tBody`
	// against the panel's `tUI`, two points larger, which put German's longest
	// command at exactly the room it has — one character from failing over
	// nothing. It errs strict and so it never went green over a defect; what it
	// did do is make the number this guard reports wrong, and that number was
	// written down. There is one list now, and it is `makeFonts`.
	f := &flyout{dpi: 96}
	f.makeFonts()
	if f.fontLine == 0 || f.fontGhost == 0 || f.fontMain == 0 || f.fontTitle == 0 {
		t.Skip("no fonts: there is nothing to measure with")
	}
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		t.Skip("no device context: there is nothing to measure with")
	}
	return f, hdc, func() {
		procReleaseDC.Call(0, hdc)
		for _, h := range []windows.Handle{f.fontTitle, f.fontMain, f.fontGhost, f.fontLine, f.fontSmall} {
			procDeleteObject.Call(uintptr(h))
		}
	}
}

// **Every status line fits in the rows the panel gives it.**
//
// These lines are the answer to the question the panel is opened for, and they
// sit above the QR code, so every row they take pushes everything below them
// down. `maxStatusRows` is the ceiling, and beyond it the text is cut with an
// ellipsis — a floor, not a place to land.
//
// It walks the catalogues rather than a list of languages, because **what
// overflows is a translation and not the base**: measured at 96 dpi,
// `remote-no-ingress` is 447 px in German against 432 in English and 338 in
// Italian, and `no-password` is over one row in all five.
//
// **Verified to catch**: with the ceiling put back to one row, it fails naming
// eight sentences across the five languages — which is the state this panel was
// in until somebody photographed it.
func TestEveryStatusLineFitsTheRowsItIsGiven(t *testing.T) {
	f, hdc, done := panelDC(t)
	defer done()

	room, row := panelRoom(f), f.px(flyLine)
	measured := 0

	for language := range i18n.Languages() {
		tr := &Tray{dictionary: i18n.Open([]string{language})}

		// What `lines` really puts on the panel: a fault when there is one, and
		// the two counters. The numbers are given their worst plausible width
		// rather than a 1, because a line that fits with one viewer and not with
		// a hundred is a line that fits by luck.
		var texts []string
		for _, fault := range AllFaults() {
			texts = append(texts, tr.t("tray.fault."+string(fault)))
		}
		texts = append(texts,
			tr.t("tray.line.watching", "viewers", "128", "devices", "128"),
			tr.t("tray.line.uptime", "since", "1234h56m78s"),
			// And the confirmation's title, which is `lines[0]` there.
			tr.t("tray.confirm.reset.title"),
			tr.t("tray.confirm.revoke.title"),
		)

		for i, text := range texts {
			font := f.fontLine
			// The last two are the confirmation titles, drawn in the pill's
			// font: measuring them with the lines' would say they fit when they
			// do not. It is the same trap as a palette read from a screenshot.
			if i >= len(texts)-2 {
				font = f.fontTitle
			}
			box := measureBox(hdc, font, text, room, true)
			rows := (box.Bottom + row - 1) / row
			measured++
			if rows > maxStatusRows {
				t.Errorf("%s: %q needs %d rows and the panel gives %d: it would be "+
					"cut", language, text, rows, maxStatusRows)
			}
			// **And a line that does not wrap at all counts as one row.**
			// `DT_CALCRECT` with `DT_WORDBREAK` returns the width of the widest
			// line, and it goes **past** the box it was given when nothing in
			// the text can be broken — so a sentence with no break opportunity
			// measures one row, passes the count above, and is drawn cut at
			// both ends, which is the worst way this panel fails and the case
			// this guard was written for.
			//
			// It costs nothing in the five languages here, where a space is
			// always available; it is the whole of the question in a script
			// with no spaces, where whether GDI breaks between characters is a
			// property of the flags and not of the sentence.
			if box.Right > room {
				t.Errorf("%s: %q measures %d px in a row of %d and did not wrap: "+
					"it would lose its first word and its last", language, text,
					box.Right, room)
			}
		}
	}
	if measured < 40 {
		t.Fatalf("%d lines measured: the guard has stopped finding them", measured)
	}
}

// **A full-row command label is cut at both ends, so it has to fit in one.**
//
// `paintButton` draws every label centred, and a button has one height: unlike
// a status line it cannot be given a second row without making the column of
// commands crooked. The ellipsis is there underneath — it has to be, because
// `tray.menu.todo` carries Tailscale's own sentence and that is not ours to
// shorten — but **an ellipsis on a word somebody chose is a word chosen badly**,
// and these are all ours.
//
// **Verified to catch**: with the room halved it fails naming the widest label
// in each language.
//
// **And the two font defects it was carrying could *not* be shown to catch**,
// which is said rather than glossed. Put back, neither makes this test fail:
// the German "Beenden und Monitor ausschalten" measured 236 px against 236 of
// room, and `>` lets equality through by one pixel; and `tray.confirm.yes` is
// 79 to 107 px wide in the pill's font against a room of 236, so measuring it
// 21 to 25 px small changed no verdict. **They were latent, not harmless** —
// one was a pixel from a failure nobody could have explained, the other a
// measurement that would have absolved a label two points too wide — and what
// found them is a review reading the fonts against `create`, because **a guard
// cannot check the instrument it is made of.**
func TestEveryCommandLabelFitsThePanel(t *testing.T) {
	f, hdc, done := panelDC(t)
	defer done()

	room := panelRoom(f)
	measured := 0

	// **The font goes with the key, because the style does.** `fontFor` gives
	// the main command `fontMain` and everything else `fontGhost` — the same 14
	// points at two weights, so `tray.confirm.yes` is a little wider than its
	// neighbour "No" and no longer two points larger. Measured with the ghost's
	// font it would still be let off, which is why the distinction stays.
	pill := map[string]bool{"tray.confirm.yes": true}

	for language := range i18n.Languages() {
		tr := &Tray{dictionary: i18n.Open([]string{language})}
		for _, key := range []string{
			"tray.menu.settings", "tray.menu.setup", "tray.menu.reset",
			"tray.menu.revoke", "tray.menu.quit", "tray.menu.videos", "tray.menu.logs",
			"tray.menu.todo.authorise", "tray.menu.todo.approve",
			"tray.menu.todo.enable-funnel",
			"tray.confirm.yes", "tray.confirm.no",
		} {
			// The accelerator marker is not drawn. **The step's three labels
			// are measured now, and that is the change**: they used to be one
			// key carrying Tailscale's sentence, whose width this catalogue
			// does not decide and which was therefore let off — and what it
			// produced on the panel was that sentence cut mid-word.
			// `tray.menu.update` stays out: its width belongs to a version
			// number.
			label := strings.ReplaceAll(tr.t(key), "&", "")
			font := f.fontGhost
			if pill[key] {
				font = f.fontMain
			}
			measured++
			if w := measureBox(hdc, font, label, room, false).Right; w > room {
				t.Errorf("%s: %q is %d px wide and the row has %d: centred, it is "+
					"cut at both ends", language, label, w, room)
			}
		}
	}
	if measured < 40 {
		t.Fatalf("%d labels measured: the guard has stopped finding them", measured)
	}
}

// The two counters are composed here and shown there, so the number of viewers
// is the one thing in those lines whose width nobody chose. This nails what the
// guard above assumed: that a three-digit count is the worst it can be asked to
// hold.
func TestTheCountersAreMeasuredWithPlausibleNumbers(t *testing.T) {
	tr := &Tray{dictionary: i18n.Open([]string{"en"})}
	line := tr.t("tray.line.watching", "viewers", strconv.Itoa(128), "devices", "128")
	if !strings.Contains(line, "128") {
		t.Fatalf("the count does not reach the line: %q", line)
	}
}
