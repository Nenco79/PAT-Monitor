// Package icon draws the executable's icon and packs it into the resource the
// linker expects.
//
// **The tray icon is drawn at runtime, this one cannot be.** That one is the
// state and GDI makes it while the program runs; this one has to be read by
// Explorer when the program is **not** running, so it has to sit inside the PE,
// and into the PE it goes only as a resource. The beaten path would be rsrc or
// goversioninfo, that is, a third-party binary in the build chain: the same
// question already asked for the tray, with the same answer. The format is
// small, settled since the nineties and documented, and writing it here costs
// less than depending on it — it is the road of internal/qr.
//
// The drawing is the dog's head from the onboarding. The coordinates are its
// own, those of the `0 0 130 100` viewBox: whoever retouches the dog there
// knows where to look here, and the two cannot diverge quietly without the test
// noticing.
package icon

import (
	"image"
	"image/color"
	"math"
)

// The tints, taken from the SVG. The disc is --c-home, the same one the tray
// uses for "monitor running at home" and the page for the first stretch: the
// icon in the bar and the page open next to it have to look like the same
// program.
var (
	disc     = rgb(0x17, 0x70, 0x6E)
	fur      = rgb(0xCF, 0xA4, 0x6C)
	ear      = rgb(0x9E, 0x70, 0x38)
	snout    = rgb(0xE7, 0xCF, 0xA5)
	dark     = rgb(0x44, 0x36, 0x28)
	eye      = rgb(0x33, 0x29, 0x1D)
	eyeLight = rgb(0xFB, 0xF8, 0xF2)
)

// The palette is straight-alpha like the surface it is drawn onto. Every tint is
// opaque, so the two conventions give the same numbers here — what the type buys
// is that the drawing and the pixels it writes are spoken of in one vocabulary.
func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{r, g, b, 0xFF} }

// The drawing's bounding box in SVG coordinates.
//
// It is measured on the shapes, not estimated, and TestTheDrawingStaysInTheBox
// checks it by sampling them: if somebody lengthens an ear, the test says so
// instead of letting the drawing run off the disc at 256 pixels with nobody
// noticing until the icon is already out there.
const (
	xMin, yMin = 13.0, 18.0
	xMax, yMax = 66.2, 55.2

	// How much of the side the drawing takes. The rest is the disc around it.
	fill = 0.72
)

// Draw produces the square icon of `side` pixels, in RGBA with straight alpha.
//
// **Simplifying as it shrinks is not an affectation.** At 16 pixels the mouth
// is six tenths of a pixel long and the glint in the eye two tenths: drawn,
// they become two grey smudges in the middle of the snout, that is, dirt. Above
// a certain size they are exactly what makes the face a face. The threshold
// lives here and not in the eye of the beholder.
func Draw(side int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))

	l := float64(side)
	c := l / 2
	rDisc := c - l*0.02

	// From pixels to SVG coordinates: the sampling happens in the drawing's
	// space rather than scaling the shapes, so the numbers below stay the SVG's
	// own.
	scale := fill * l / (xMax - xMin)
	if s := fill * l / (yMax - yMin); s < scale {
		scale = s
	}
	offX := c - scale*(xMin+xMax)/2
	offY := c - scale*(yMin+yMax)/2

	mouth := side >= 24
	glint := side >= 32

	// Four samples a side are enough to take the staircase off the curved
	// edges, and it is the same density as the tray icon: there is no reason
	// for two drawings of the same program to be antialiased differently.
	const ss = 4
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			var sr, sg, sb, sa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if math.Hypot(px-c, py-c) > rDisc {
						continue
					}
					ux := (px - offX) / scale
					uy := (py - offY) / scale

					col := disc
					if inEllipse(ux, uy, 45, 36, 20, 18) {
						col = fur
					}
					if inPolygon(ux, uy, earShape) {
						col = ear
					}
					if inEllipse(ux, uy, 28, 43, 13, 10) {
						col = snout
					}
					if inEllipse(ux, uy, 18, 40, 5, 4) {
						col = dark
					}
					if mouth && nearTo(ux, uy, mouthShape, 1.0) {
						col = dark
					}
					if inEllipse(ux, uy, 40, 32, 3.4, 3.4) {
						col = eye
					}
					if glint && inEllipse(ux, uy, 41.3, 30.8, 1.1, 1.1) {
						col = eyeLight
					}

					sr += float64(col.R)
					sg += float64(col.G)
					sb += float64(col.B)
					sa++
				}
			}
			if sa == 0 {
				continue
			}
			const total = ss * ss
			// **The colour is the average over the samples that were covered
			// and the alpha is the coverage** — that is, straight alpha, not
			// premultiplied: on the rim a component regularly exceeds its own
			// alpha (measured, G=112 and B=110 against A=63).
			//
			// It used to be stored in an `image.RGBA`, which Go defines as
			// alpha-premultiplied. The two paths out of here then disagreed
			// about the same bytes. `dib` copies them verbatim into BGRA, which
			// wants straight, so the .ico was right; `png.Encode` believed the
			// type and un-premultiplied them, so the 256 entry and every icon
			// the web manifest draws came out with the rim lightened —
			// measured, that pixel written to PNG as 93/199/190 instead of
			// 23/112/110. `image.NRGBA` is Go's name for what these values
			// already are, and it makes both readers right without either of
			// them changing.
			img.SetNRGBA(x, y, color.NRGBA{
				R: byte(sr / sa),
				G: byte(sg / sa),
				B: byte(sb / sa),
				A: byte(sa / total * 255),
			})
		}
	}
	return img
}

func inEllipse(x, y, cx, cy, rx, ry float64) bool {
	dx := (x - cx) / rx
	dy := (y - cy) / ry
	return dx*dx+dy*dy <= 1
}

type point struct{ X, Y float64 }

// inPolygon uses the crossing count: the shape is closed and has no holes, so
// even-odd and non-zero give the same answer.
func inPolygon(x, y float64, p []point) bool {
	inside := false
	for i, j := 0, len(p)-1; i < len(p); j, i = i, i+1 {
		if (p[i].Y > y) != (p[j].Y > y) &&
			x < (p[j].X-p[i].X)*(y-p[i].Y)/(p[j].Y-p[i].Y)+p[i].X {
			inside = !inside
		}
	}
	return inside
}

// nearTo is the stroke: the distance from a polyline, which with round caps is
// exactly what a stroke-linecap="round" draws.
func nearTo(x, y float64, p []point, halfStroke float64) bool {
	for i := 0; i+1 < len(p); i++ {
		if distanceFromSegment(x, y, p[i], p[i+1]) <= halfStroke {
			return true
		}
	}
	return false
}

func distanceFromSegment(x, y float64, a, b point) float64 {
	vx, vy := b.X-a.X, b.Y-a.Y
	wx, wy := x-a.X, y-a.Y
	den := vx*vx + vy*vy
	t := 0.0
	if den > 0 {
		t = (wx*vx + wy*vy) / den
		t = math.Max(0, math.Min(1, t))
	}
	return math.Hypot(x-(a.X+t*vx), y-(a.Y+t*vy))
}

// cubic flattens a cubic bezier into a polyline. Sixteen segments are ample:
// the largest shape is the ear, and at 256 pixels one of its segments measures
// less than a pixel.
func cubic(p0, c1, c2, p1 point, n int) []point {
	out := make([]point, 0, n+1)
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		out = append(out, point{
			X: u*u*u*p0.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*p1.X,
			Y: u*u*u*p0.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*p1.Y,
		})
	}
	return out
}

// The two shapes that in the SVG are paths rather than ellipses.
//
//	ear:   M52 25 c9-1 15 8 14 19  c-1 8 -6 12 -12 11  z
//	mouth: M22 47 c0 4 3 6 6 6
var (
	earShape = append(
		cubic(point{52, 25}, point{61, 24}, point{67, 33}, point{66, 44}, 16),
		cubic(point{66, 44}, point{65, 52}, point{60, 56}, point{54, 55}, 16)...,
	)
	mouthShape = cubic(point{22, 47}, point{22, 51}, point{25, 53}, point{28, 53}, 16)
)
