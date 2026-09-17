//go:build windows

package tray

import (
	"io"
	"log/slog"
	"math"
	"testing"

	"patmonitor/internal/i18n"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// **A glyph that cannot be read does not protest, and it would show up on
// somebody else's panel.**
//
// The `d` strings are constants: either they always read or they never do, so
// this is the test that decides — at run time all that is left is the fallback
// to the word, which exists so as not to leave a button empty and not to cover
// a defect.
//
// Where the stroke ends is watched too: a `d` with a number out of place reads
// perfectly well and draws outside the box, that is, it produces exactly the
// plausible, wrong glyph this project fears. The set's grid is 24, and no point
// can fall outside it.
func TestEveryGlyphOfThePanelCanBeRead(t *testing.T) {
	for _, g := range []*glyph{glyphVideo, glyphFileText} {
		lines, err := g.polylines()
		if err != nil {
			t.Errorf("%s: %v", g.name, err)
			continue
		}
		if len(lines) != len(g.d) {
			t.Errorf("%s: %d strokes from %d paths, one per path does not fit",
				g.name, len(lines), len(g.d))
		}
		for _, line := range lines {
			if len(line) < 2 {
				t.Errorf("%s: a stroke of %d points is not a stroke", g.name, len(line))
			}
			for _, p := range line {
				if p.X < 0 || p.X > glyphGrid || p.Y < 0 || p.Y > glyphGrid {
					t.Errorf("%s: the point %.3f,%.3f leaves the grid of %.0f",
						g.name, p.X, p.Y, glyphGrid)
				}
			}
		}
	}
}

// **A rounded corner is a quarter circle, and it has to be checked by
// position.**
//
// Converting an SVG arc to centre form has four ways of going wrong that give
// no error — the sign of the semi-axis, the two flags swapped, the sweep — and
// all four produce a curve: a corner gets drawn, only on the wrong side or
// bulging outwards. Here where it passes is measured: it is the body of
// `video`'s camera, the rectangle (3,6)-(15,18) with radius 2, and its top left
// vertex — so every point of the arc is at distance 2 from the centre (5,8) and
// inside the quadrant.
//
// **The path is the real one, taken from the glyph we ship**, not a copy
// written here: a test on a `d` of its own would prove that the reader
// understands the test's strings.
func TestARoundedCornerIsAQuarterCircleWhereItBelongs(t *testing.T) {
	const body = 1 // the second `<path>` of `video` is the body
	lines, err := parsePath(glyphVideo.d[body])
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("a closed path is one stroke, %d came out", len(lines))
	}
	line := lines[0]
	if first, last := line[0], line[len(line)-1]; first != last {
		t.Errorf("`z` did not close the stroke: it starts at %v and ends at %v", first, last)
	}

	// The points of the arc are those between (3,8) and (5,6): all in the top
	// left quadrant, all at distance 2 from the corner's centre.
	corner := 0
	for _, p := range line {
		if p.X > 5.001 || p.Y > 8.001 {
			continue
		}
		corner++
		if d := math.Hypot(p.X-5, p.Y-8); math.Abs(d-2) > 0.001 {
			t.Errorf("the point %.3f,%.3f is %.3f from the corner's centre rather than 2",
				p.X, p.Y, d)
		}
	}
	if corner < 3 {
		t.Errorf("the corner is made of %d points: it has not been flattened", corner)
	}
}

// **A command we cannot read is refused, not skipped.**
//
// Skipping it would give a glyph that draws and is not the right one: the most
// dangerous figure in this file, because nobody sees it until they look at the
// panel — and it is the same family as the GUID that does not protest.
func TestAnUnreadableGlyphIsRefusedInsteadOfGuessed(t *testing.T) {
	for _, bad := range []string{
		"M0 0C1 1 2 2 3 3",     // cubic: not needed by this family
		"M0 0a2 3 0 0 1 2 -2",  // ellipse
		"M0 0a2 2 45 0 1 2 -2", // rotated arc
		"4 4h16",               // no command
		"M4 4h",                // no number
		"M4 4z9",               // a number after the close
		"M4 4",                 // no stroke, only a move
		"",                     // nothing
		"M4 4Q6 6 8 8",         // quadratic
	} {
		if _, err := parsePath(bad); err == nil {
			t.Errorf("%q was read instead of being refused", bad)
		}
	}
}

// **Outside the stroke there is the button's background, exactly.**
//
// The engraving is delivered with `BitBlt`, which copies and nothing else: if
// the bitmap's background were not **the same** colour as the one it is laid
// on, a square would appear around every glyph — and on a button that has the
// focus, where the background is another colour, it would appear only there. It
// is the case that live shows up only by tabbing as far as that button, that is,
// late.
//
// The corners are looked at, being the points furthest from the stroke in both
// glyphs, and with **the three real pairs of colours**: normal, focused,
// pressed.
func TestTheEngravingCarriesTheButtonBackground(t *testing.T) {
	for _, pal := range []palette{palDark, palLight} {
		fills := []uint32{
			pal.ground,
			blend(pal.ground, pal.accent, 0.22),
			blend(blend(pal.ground, pal.accent, 0.22), pal.ink, 0.12),
		}
		for _, g := range []*glyph{glyphVideo, glyphFileText} {
			lines, err := g.polylines()
			if err != nil {
				t.Fatal(err)
			}
			for _, fill := range fills {
				const side = 20
				pix := make([]byte, side*side*4)
				engrave(pix, side, lines, pal.ink, fill)

				want := []byte{byte(fill >> 16), byte(fill >> 8), byte(fill), 0xFF}
				for _, corner := range [][2]int{{0, 0}, {side - 1, 0}, {0, side - 1}, {side - 1, side - 1}} {
					i := (corner[1]*side + corner[0]) * 4
					got := pix[i : i+4]
					if string(got) != string(want) {
						t.Errorf("%s, background %06X: the corner %v carries %v instead of %v",
							g.name, fill, corner, got, want)
					}
				}
			}
		}
	}
}

// The two folder commands sit on a single row, and each carries its own glyph.
// **The row is decided by whoever composes it**, so that is where to look: if
// `share` were lost, the panel would give no error — it would grow by a row,
// which is what this arrangement exists to remove.
func TestTheTwoFoldersShareOneRow(t *testing.T) {
	tr := &Tray{dictionary: i18n.Open([]string{"it"})}
	tr.cfg.Log = quiet()
	tr.cfg.VideoDir, tr.cfg.LogDir = `C:\video`, `C:\log`

	cmds := tr.folders()
	if len(cmds) != 2 {
		t.Fatalf("two folders configured, %d commands", len(cmds))
	}
	for i, c := range cmds {
		if c.style != styleIcon || c.icon == nil {
			t.Errorf("command %d: style %v, glyph %v", i, c.style, c.icon)
		}
		// The word stays on the control even when it is not drawn: it is the
		// name Narrator reads and the letter that answers from the keyboard.
		if c.label == "" {
			t.Errorf("command %d: with no label it has no name to read", i)
		}
	}
	if cmds[0].share || !cmds[1].share {
		t.Errorf("the row is wrong: share = %v, %v", cmds[0].share, cmds[1].share)
	}

	f := &flyout{t: tr, dpi: 96, cmds: cmds}
	if rows := f.rows(); len(rows) != 1 || len(rows[0]) != 2 {
		t.Errorf("the two commands have to sit on one row, %v came out", rows)
	}

	// With one folder there is nothing to divide: the glyph takes the whole row
	// rather than leaving half of it empty.
	tr.cfg.LogDir = ""
	if cmds := tr.folders(); len(cmds) != 1 || cmds[0].share {
		t.Errorf("one folder divides nothing: %+v", cmds)
	}
}

// **Commands side by side reach the right edge like all the others.**
//
// Integer division leaves up to one pixel behind per command, and a pixel of
// uncovered background at the end of the row shows: the panel is a column, and
// everything else ends exactly at `w - pad`.
func TestASharedRowFillsTheWidth(t *testing.T) {
	tr := &Tray{dictionary: i18n.Open([]string{"it"})}
	tr.cfg.Log = quiet()

	for _, dpi := range []int32{96, 120, 144, 192} {
		f := &flyout{t: tr, dpi: dpi}
		pad, gap := f.px(flyPadding), f.px(flyButtonGap)
		// An odd width is the case that goes wrong: it does not divide in two.
		for _, w := range []int32{f.px(flyWidth), f.px(flyWidth) + 1} {
			for _, n := range []int{1, 2, 3} {
				spans := f.rowSpans(w, n)
				if len(spans) != n {
					t.Fatalf("dpi %d: %d commands, %d places", dpi, n, len(spans))
				}
				if spans[0][0] != pad {
					t.Errorf("dpi %d width %d: the row begins at %d rather than %d",
						dpi, w, spans[0][0], pad)
				}
				last := spans[n-1]
				if right := last[0] + last[1]; right != w-pad {
					t.Errorf("dpi %d width %d, %d commands: the row ends at %d rather than %d",
						dpi, w, n, right, w-pad)
				}
				for k := 1; k < n; k++ {
					if got := spans[k][0] - (spans[k-1][0] + spans[k-1][1]); got != gap {
						t.Errorf("dpi %d width %d: between command %d and %d there are %d instead of %d",
							dpi, w, k-1, k, got, gap)
					}
					// The widths may differ by one pixel because of integer
					// division, not by more: two parallel commands visibly
					// different would read as a hierarchy.
					if d := spans[k][1] - spans[k-1][1]; d < -1 || d > 1 {
						t.Errorf("dpi %d width %d: commands %d and %d wide",
							dpi, w, spans[k-1][1], spans[k][1])
					}
				}
			}
		}
	}
}
