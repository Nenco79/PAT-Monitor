//go:build windows

package tray

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// **The pulse is looked at, not deduced.** The same rule holds as for the
// glyphs: at sixteen pixels an amplitude that looks reasonable on paper can be
// invisible or read as a flicker, and no rereading of the cosine says so.
//
// It writes nothing during a normal `go test`. **A test that interrogates or
// fills the disk of whoever runs it has already cost four megabytes of fake
// clips in the Videos folder**, so the path here has to be given by whoever is
// looking:
//
//	PATMON_LOOK=<folder> go test ./internal/tray/ -run TestLookAtThePulse
func TestLookAtThePulse(t *testing.T) {
	dir := os.Getenv("PATMON_LOOK")
	if dir == "" {
		t.Skip("PATMON_LOOK not set: nothing to look at")
	}

	const (
		size   = 32 // the real size at 200 dpi
		frames = 12 // a whole cycle: pulsePeriod / pulseStep
		zoom   = 5
		pad    = 6
	)

	// The background of the Windows taskbar in the dark theme, which is where
	// this icon lives: an icon judged on white is an icon judged somewhere else.
	const backR, backG, backB = 0x20, 0x20, 0x20

	width := frames*(size*zoom+pad) + pad
	out := image.NewNRGBA(image.Rect(0, 0, width, size*zoom+2*pad))
	for y := out.Rect.Min.Y; y < out.Rect.Max.Y; y++ {
		for x := out.Rect.Min.X; x < out.Rect.Max.X; x++ {
			out.Set(x, y, color.NRGBA{backR, backG, backB, 0xFF})
		}
	}

	for f := 0; f < frames; f++ {
		phase := float64(f) / frames
		dim := pulseDepth * (1 - math.Cos(2*math.Pi*phase)) / 2
		pix := iconPixels(PhaseOutside, size, dim)

		x0 := pad + f*(size*zoom+pad)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				i := (y*size + x) * 4
				b, g, r, a := int(pix[i]), int(pix[i+1]), int(pix[i+2]), int(pix[i+3])
				// **The icon is composed over the background, it does not
				// replace it.** The pixels are premultiplied, so the top is
				// already multiplied by the alpha and the background need only
				// be added for the uncovered part. Writing them as they were
				// would show the icon over nothing, while the question is how it
				// looks **on the taskbar**.
				comp := color.NRGBA{
					R: byte(r + backR*(255-a)/255),
					G: byte(g + backG*(255-a)/255),
					B: byte(b + backB*(255-a)/255),
					A: 0xFF,
				}
				for zy := 0; zy < zoom; zy++ {
					for zx := 0; zx < zoom; zx++ {
						out.Set(x0+x*zoom+zx, pad+y*zoom+zy, comp)
					}
				}
			}
		}
	}

	path := filepath.Join(dir, "pulse.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
