//go:build windows

package tray

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The panel's glyphs: the same icons as the pages', engraved by us.
//
// **The family is that one, and the `d` strings are verbatim.** They are Tabler
// Icons v3.46.0, MIT, like the viewer's and the onboarding's glyphs: the licence
// is already in `licenses/manually-added/tabler-icons/`, and this file is the
// second place from which somebody else's work enters the binary. The strings
// below are copied from the `d` attribute of the `<svg>`s without touching them,
// which is the only way they could one day be compared against the set again:
// rewriting them into a structure of ours would mean never being able to check
// them.
//
// **The outline is dictated by the set, not by us**: a grid of 24, stroke 2,
// round caps and joins — the same three numbers as `icons.css`, and for the same
// reason, that touching them mashes the glyphs together at exactly the small
// size. Hence the way they are engraved, which is the non-obvious part: a round
// stroke is **the set of points less than half a stroke from the line**, so by
// measuring that distance the round caps and joins come out by themselves,
// without a special case for the corners.
//
// GDI is not involved: `Polyline` draws in steps and antialiases nothing, as
// does `RoundRect`. The surface is composed by hand in the same shape as the
// pills are, and delivered with `BitBlt`.
const (
	// The set's grid, and the stroke the glyphs were drawn with.
	glyphGrid   = 24.0
	glyphStroke = 2.0
)

type glyphPoint struct{ X, Y float64 }

// glyph is one icon of the set: its name and the `d` of its `<path>`s.
//
// The flattening — arcs included — is computed **once per process**: it is pure
// arithmetic on the same constant strings, so redoing it on every opening of the
// panel would be redoing the same sum.
type glyph struct {
	name string
	d    []string

	once  sync.Once
	lines [][]glyphPoint
	err   error
}

// The two glyphs the panel draws, verbatim from Tabler Icons v3.46.0.
//
// **They are two kinds of content, not two folders.** The set has a folder, and
// only one: two folder glyphs side by side would be the same drawing twice, that
// is, two commands indistinguishable from outside. What separates them is what
// is inside — a camera and a written sheet — which is also what one is looking
// for when opening one or the other.
//
// **`video` and not `movie`, and it is not a tie.** The film reel says "film",
// and at the panel's size its eight holes mash into a band that reads as a
// ticket; and above all it is **symmetrical**, so beside the sheet the two
// shapes resemble each other. The camera has the lens sticking out on one side —
// at a glance, before the drawing is even recognised, one sees they are two
// different things. Also tried: `folder`, which says "folder" and not "video"
// and beside a document makes the document read as a file, and `camera`, which
// says "photograph".
var (
	glyphVideo = &glyph{name: "video", d: []string{
		"M15 10l4.553 -2.276a1 1 0 0 1 1.447 .894v6.764a1 1 0 0 1 -1.447 .894l-4.553 -2.276v-4z",
		"M3 6m0 2a2 2 0 0 1 2 -2h8a2 2 0 0 1 2 2v8a2 2 0 0 1 -2 2h-8a2 2 0 0 1 -2 -2z",
	}}
	glyphFileText = &glyph{name: "file-text", d: []string{
		"M14 3v4a1 1 0 0 0 1 1h4",
		"M17 21h-10a2 2 0 0 1 -2 -2v-14a2 2 0 0 1 2 -2h7l5 5v11a2 2 0 0 1 -2 2z",
		"M9 9l1 0",
		"M9 13l6 0",
		"M9 17l6 0",
	}}
)

// polylines gives the glyph flattened, in the coordinates of the grid of 24.
func (g *glyph) polylines() ([][]glyphPoint, error) {
	g.once.Do(func() {
		for _, d := range g.d {
			lines, err := parsePath(d)
			if err != nil {
				g.lines, g.err = nil, fmt.Errorf("glyph %q, path %q: %w", g.name, d, err)
				return
			}
			g.lines = append(g.lines, lines...)
		}
		if len(g.lines) == 0 {
			g.err = fmt.Errorf("glyph %q has no strokes", g.name)
		}
	})
	return g.lines, g.err
}

// --- the reader of the `d` strings ------------------------------------------
//
// **It covers what this family uses, and refuses the rest.** An unexpected
// command is not skipped and not guessed at: an error is returned, and the
// caller shows the word instead of the glyph. A tolerant reader would draw a
// **plausible, wrong** glyph, which is the form of fault this project pays most
// dearly for — the same as the GUID that does not protest.

type pathScanner struct {
	s string
	i int
}

func (p *pathScanner) skip() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', ',', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

// command reads a command letter, if one is there.
func (p *pathScanner) command() (byte, bool) {
	p.skip()
	if p.i >= len(p.s) {
		return 0, false
	}
	c := p.s[p.i]
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		p.i++
		return c, true
	}
	return 0, false
}

func (p *pathScanner) number() (float64, error) {
	p.skip()
	start := p.i
	if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
		p.i++
	}
	digits := func() {
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	digits()
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		digits()
	}
	if p.i == start {
		return 0, fmt.Errorf("a number was expected at %d", start)
	}
	v, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number: %w", p.s[start:p.i], err)
	}
	return v, nil
}

// parsePath flattens a `d` into polylines.
func parsePath(d string) ([][]glyphPoint, error) {
	sc := &pathScanner{s: d}
	var out [][]glyphPoint
	var cur []glyphPoint
	var at, first glyphPoint
	var cmd byte

	// A polyline of a single point is not a stroke: a `z` leaves one behind,
	// starting again from the closing point without going anywhere.
	flush := func() {
		if len(cur) > 1 {
			out = append(out, cur)
		}
		cur = nil
	}
	// **A repeated command does not repeat its letter**, and after an `M` the
	// numbers that follow are lines: that is the specification's rule, and it is
	// what makes a `d` like `M9 9l1 0` readable.
	next := func() error {
		if c, ok := sc.command(); ok {
			cmd = c
			return nil
		}
		switch cmd {
		case 0:
			return fmt.Errorf("a command was expected at %d", sc.i)
		case 'M':
			cmd = 'L'
		case 'm':
			cmd = 'l'
		}
		return nil
	}

	for {
		sc.skip()
		if sc.i >= len(sc.s) {
			break
		}
		if err := next(); err != nil {
			return nil, err
		}
		switch cmd {
		case 'M', 'm':
			x, err := sc.number()
			if err != nil {
				return nil, err
			}
			y, err := sc.number()
			if err != nil {
				return nil, err
			}
			if cmd == 'm' {
				x, y = x+at.X, y+at.Y
			}
			flush()
			at = glyphPoint{x, y}
			first = at
			cur = []glyphPoint{at}
		case 'L', 'l', 'H', 'h', 'V', 'v':
			p, err := lineTo(sc, cmd, at)
			if err != nil {
				return nil, err
			}
			at = p
			cur = append(cur, at)
		case 'A', 'a':
			p, arc, err := arcTo(sc, cmd == 'a', at)
			if err != nil {
				return nil, err
			}
			at = p
			cur = append(cur, arc...)
		case 'Z', 'z':
			// Only a subpath that has gone somewhere is closed: closing a move
			// and nothing else would give a stroke of zero length, that is, a
			// dot in the drawing.
			if len(cur) > 1 {
				cur = append(cur, first)
			}
			flush()
			at = first
			cur = []glyphPoint{first}
			// After a close there is no command to repeat: a number here is a
			// `d` we do not understand, and that has to be said rather than
			// spun over.
			cmd = 0
		default:
			return nil, fmt.Errorf("command %q is not supported", string(cmd))
		}
	}
	flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("no stroke came out of %q", d)
	}
	return out, nil
}

func lineTo(sc *pathScanner, cmd byte, at glyphPoint) (glyphPoint, error) {
	switch cmd {
	case 'H', 'h':
		x, err := sc.number()
		if err != nil {
			return at, err
		}
		if cmd == 'h' {
			x += at.X
		}
		return glyphPoint{x, at.Y}, nil
	case 'V', 'v':
		y, err := sc.number()
		if err != nil {
			return at, err
		}
		if cmd == 'v' {
			y += at.Y
		}
		return glyphPoint{at.X, y}, nil
	}
	x, err := sc.number()
	if err != nil {
		return at, err
	}
	y, err := sc.number()
	if err != nil {
		return at, err
	}
	if cmd == 'l' {
		x, y = x+at.X, y+at.Y
	}
	return glyphPoint{x, y}, nil
}

// arcTo reads an arc and flattens it.
//
// **Circular and unrotated only**, which is all this family writes: the glyphs'
// rounded corners are quarter circles, `a2 2 0 0 1` and `a1 1 0 0 0`. A rotated
// ellipse is refused rather than approximated with a circle — that would be a
// glyph that draws and is not the right one.
func arcTo(sc *pathScanner, relative bool, from glyphPoint) (glyphPoint, []glyphPoint, error) {
	var v [7]float64
	for i := range v {
		n, err := sc.number()
		if err != nil {
			return from, nil, err
		}
		v[i] = n
	}
	rx, ry, rotation, large, sweep := v[0], v[1], v[2], v[3] != 0, v[4] != 0
	to := glyphPoint{v[5], v[6]}
	if relative {
		to = glyphPoint{to.X + from.X, to.Y + from.Y}
	}
	if rx != ry || rotation != 0 {
		return from, nil, fmt.Errorf(
			"only circular arcs without rotation are supported, got rx=%g ry=%g rotation=%g",
			rx, ry, rotation)
	}
	r := math.Abs(rx)
	if r == 0 || (from.X == to.X && from.Y == to.Y) {
		return to, []glyphPoint{to}, nil
	}

	// The conversion to centre form, for the circular case: the centre lies on
	// the perpendicular bisector of the segment, and which side is said by the
	// two flags.
	dx, dy := (from.X-to.X)/2, (from.Y-to.Y)/2
	span := dx*dx + dy*dy
	if span > r*r {
		// Radius too small to join the two points: the specification says to
		// widen it to the smallest that will do, not to refuse.
		r = math.Sqrt(span)
	}
	k := math.Sqrt(math.Max(0, (r*r-span)/span))
	if large == sweep {
		k = -k
	}
	cx := k*dy + (from.X+to.X)/2
	cy := -k*dx + (from.Y+to.Y)/2

	a0 := math.Atan2(from.Y-cy, from.X-cx)
	a1 := math.Atan2(to.Y-cy, to.X-cx)
	da := a1 - a0
	if sweep && da < 0 {
		da += 2 * math.Pi
	}
	if !sweep && da > 0 {
		da -= 2 * math.Pi
	}

	// One segment per fraction of a unit of arc: at the size the panel draws at
	// these are tenths of a pixel, that is, below what antialiasing can tell
	// apart.
	n := int(math.Ceil(math.Abs(da) * r / 0.15))
	n = min(max(n, 2), 48)
	pts := make([]glyphPoint, 0, n)
	for i := 1; i <= n; i++ {
		a := a0 + da*float64(i)/float64(n)
		pts = append(pts, glyphPoint{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return to, pts, nil
}

// --- the engraving ----------------------------------------------------------

// glyphKey is everything that tells one engraving from another: two glyphs with
// the same key are the same image, and are composed once. It is the pills' rule,
// and here it counts double — a button that has the focus and one that does not
// differ only by the two colours.
type glyphKey struct {
	name      string
	side      int32
	ink, fill uint32
}

// glyphBitmap engraves a glyph into a bitmap already of the right size, on the
// background it will be laid on.
//
// **The background is a parameter and not a case to handle**: the bitmap is
// delivered with `BitBlt`, which copies and nothing else, so what lies outside
// the stroke has to carry the button's colour already. That can be done because
// the glyph sits in the flat centre of the pill, where the colour is exactly
// `fill` — the same property for which `pill` does not sample its own centre.
func (f *flyout) glyphBitmap(g *glyph, side int32, ink, fill uint32) windows.Handle {
	if side <= 0 {
		return 0
	}
	if f.glyphs == nil {
		f.glyphs = map[glyphKey]windows.Handle{}
	}
	key := glyphKey{g.name, side, ink, fill}
	if bm, ready := f.glyphs[key]; ready {
		return bm
	}
	lines, err := g.polylines()
	if err != nil {
		return 0
	}

	hdr := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: side, Height: -side,
		Planes: 1, BitCount: 32, Compression: 0,
	}
	var bits unsafe.Pointer
	bm, _, _ := procCreateDIBSection.Call(
		0, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bm == 0 {
		return 0
	}
	// The bitmap stays alive in the cache: `release` frees it, with the pills.
	engrave(unsafe.Slice((*byte)(bits), int(side)*int(side)*4), side, lines, ink, fill)

	f.glyphs[key] = windows.Handle(bm)
	return windows.Handle(bm)
}

// engrave fills the pixels: the stroke in `ink`, everything else in `fill`.
//
// **It is separate from glyphBitmap for `iconPixels`' reason**: so that the
// drawing can be looked at and measured without going through GDI and without an
// open panel — and the property that matters, that outside the stroke there is
// exactly the button's background, can be demanded rather than hoped for.
func engrave(pix []byte, side int32, lines [][]glyphPoint, ink, fill uint32) {
	// The segments already in pixels: the distance is computed once per sample
	// and per segment, and rescaling inside there would be the same
	// multiplication a hundred thousand times.
	scale := float64(side) / glyphGrid
	half := glyphStroke / 2 * scale
	var segs [][4]float64
	for _, line := range lines {
		for i := 1; i < len(line); i++ {
			segs = append(segs, [4]float64{
				line[i-1].X * scale, line[i-1].Y * scale,
				line[i].X * scale, line[i].Y * scale,
			})
		}
	}
	dist := func(px, py float64) float64 {
		best := math.MaxFloat64
		for _, s := range segs {
			if d := distToSegment(px, py, s[0], s[1], s[2], s[3]); d < best {
				best = d
			}
		}
		return best
	}

	// **Only the band at the edge is sampled.** Beyond half a pixel diagonal
	// from the stroke the outcome is already decided — inside or outside — and
	// that is nine tenths of the pixels: sampling them sixteen times to get back
	// zero or one costs the same wait one sees when a panel arrives in pieces.
	const ss = 4
	const halfDiagonal = 0.7072
	for y := range side {
		for x := range side {
			var cov float64
			switch d := dist(float64(x)+0.5, float64(y)+0.5); {
			case d <= half-halfDiagonal:
				cov = 1
			case d >= half+halfDiagonal:
				cov = 0
			default:
				var inside float64
				for sy := range ss {
					for sx := range ss {
						if dist(float64(x)+(float64(sx)+0.5)/ss,
							float64(y)+(float64(sy)+0.5)/ss) <= half {
							inside++
						}
					}
				}
				cov = inside / (ss * ss)
			}
			writePixel(pix, int(y*side+x)*4, blend(fill, ink, cov))
		}
	}
}

// distToSegment is the distance from the segment, not from the line: it is what
// gives the round caps, because beyond the two ends the nearest point is the
// end. On the joins it does the same by itself — the stroke before and the one
// after overlap around the vertex — and that is why there is no case for the
// corners.
func distToSegment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	length := dx*dx + dy*dy
	if length == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / length
	t = math.Min(math.Max(t, 0), 1)
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}
