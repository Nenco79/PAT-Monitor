package server

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// **iOS composites the Home Screen icon onto black and says nothing about it.**
// That is the whole reason the field is filled in here: transparency does not
// stay the page's warm near-black, it becomes #000000, and the only way to see
// the difference is to hold the phone. Nothing about the drawing changing back
// would fail — the route answers, the PNG is valid, the icon appears — so the
// guard is on the pixels.
//
// The favicon is the exception and it is checked in the other direction: a tab
// gives the icon whatever colour the tab is, so an opaque field there would be a
// small dark rectangle among the others. Both halves are asserted, because a
// test that only demanded opacity would be satisfied by filling all four.

func decodeIcon(t *testing.T, size int) image.Image {
	t.Helper()
	b, err := iconPNG(size)
	if err != nil {
		t.Fatalf("the %dpx icon was not drawn: %v", size, err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("the %dpx icon is not a readable PNG: %v", size, err)
	}
	return img
}

func TestTheHomeScreenIconsAreOpaque(t *testing.T) {
	checked := 0
	for _, size := range iconSizes {
		if size == faviconSize {
			continue
		}
		checked++
		img := decodeIcon(t, size)
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
					t.Fatalf("the %dpx icon is transparent at (%d,%d): iOS would composite it onto black", size, x, y)
				}
			}
		}
	}
	if checked < 2 {
		t.Fatalf("%d sizes read: this guard is looking at nothing", checked)
	}
}

// And the field is the page's ground and not some other dark: the corner is the
// one place the drawing certainly does not reach.
func TestTheFieldIsThePagesGround(t *testing.T) {
	img := decodeIcon(t, 180)
	r, g, b, _ := img.At(0, 0).RGBA()
	wantR, wantG, wantB, _ := ground.RGBA()
	if r != wantR || g != wantG || b != wantB {
		t.Errorf("the 180px icon's corner is %04x%04x%04x and the ground is %04x%04x%04x",
			r, g, b, wantR, wantG, wantB)
	}
}

// The other direction, which the test above cannot see: filling every size
// would satisfy it and would put a dark tile in the browser's tab strip.
func TestTheFaviconKeepsItsTransparency(t *testing.T) {
	img := decodeIcon(t, faviconSize)
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a == 0 {
				return
			}
		}
	}
	t.Error("the favicon has no transparent pixel left: in a tab it is a dark rectangle among the others")
}
