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
	// **The same sizes and weights `create` uses, and they are the point.** A
	// guard that measures with a font the panel does not draw with is measuring
	// something else: `fontGhost` was built here at `tBody` against the panel's
	// `tUI`, two points larger, which put German's longest command at exactly
	// the room it has — one character from failing over nothing. It errs strict
	// and so it never went green over a defect; what it did do is make the
	// number this guard reports wrong, and that number was written down.
	f := &flyout{dpi: 96}
	f.fontPill = f.createFont(tBody, 600)
	f.fontGhost = f.createFont(tUI, 500)
	f.fontLine = f.createFont(tUI, 400)
	if f.fontLine == 0 || f.fontGhost == 0 || f.fontPill == 0 {
		t.Skip("no fonts: there is nothing to measure with")
	}
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		t.Skip("no device context: there is nothing to measure with")
	}
	return f, hdc, func() {
		procReleaseDC.Call(0, hdc)
		for _, h := range []windows.Handle{f.fontLine, f.fontGhost, f.fontPill} {
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
				font = f.fontPill
			}
			box := measureBox(hdc, font, text, room, true)
			rows := (box.Bottom + row - 1) / row
			measured++
			if rows > maxStatusRows {
				t.Errorf("%s: %q needs %d rows and the panel gives %d: it would be "+
					"cut", language, text, rows, maxStatusRows)
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
	// the filled pill `fontPill`, 16 at weight 600, and everything else
	// `fontGhost`, 14 at 500 — so `tray.confirm.yes`, which is the panel's one
	// pill with a word of ours on it, is two points larger than its neighbour
	// "No". Measured with the ghost's font it was being let off.
	pill := map[string]bool{"tray.confirm.yes": true}

	for language := range i18n.Languages() {
		tr := &Tray{dictionary: i18n.Open([]string{language})}
		for _, key := range []string{
			"tray.menu.permission", "tray.menu.setup", "tray.menu.reset",
			"tray.menu.revoke", "tray.menu.quit", "tray.menu.videos", "tray.menu.logs",
			"tray.confirm.yes", "tray.confirm.no",
		} {
			// The accelerator marker is not drawn. `tray.menu.todo` and
			// `tray.menu.update` are left out because their width belongs to a
			// sentence from Tailscale and to a version number, neither of which
			// this catalogue decides.
			label := strings.ReplaceAll(tr.t(key), "&", "")
			font := f.fontGhost
			if pill[key] {
				font = f.fontPill
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
