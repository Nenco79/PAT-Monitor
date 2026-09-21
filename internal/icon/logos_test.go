package icon

import (
	"bytes"
	"image/png"
	"testing"
)

// **Each logo is drawn at its own size and decodes at it.**
//
// The shortcut this refuses is scaling them from the largest image the icon
// already holds: the sizes a manifest names are in no version of `Sides`, so
// whoever reaches for one has to resize, and the drawing simplifies as it
// shrinks — what comes out then carries, softened, exactly the detail that was
// meant to disappear.
//
// **Verified to catch**: with `PNG` given a fixed side, the sizes stop matching
// and every logo but one fails.
func TestEveryPackageLogoIsDrawnAtItsOwnSize(t *testing.T) {
	if len(PackageLogos) == 0 {
		t.Fatal("no logo is produced, and a manifest that names one gets nothing")
	}
	for _, l := range PackageLogos {
		data := PNG(l.Side)
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s: does not decode: %v", l.Name, err)
			continue
		}
		b := img.Bounds()
		if b.Dx() != l.Side || b.Dy() != l.Side {
			t.Errorf("%s: %dx%d, wanted %dx%d", l.Name, b.Dx(), b.Dy(), l.Side, l.Side)
		}
	}
}

// **And each of them has something on it.** A logo that encodes, decodes and is
// entirely transparent passes every check made on its shape, installs, and
// leaves the shell drawing the fallback — the failure the format gives no error
// for.
func TestNoPackageLogoIsBlank(t *testing.T) {
	for _, l := range PackageLogos {
		img, err := png.Decode(bytes.NewReader(PNG(l.Side)))
		if err != nil {
			t.Errorf("%s: does not decode: %v", l.Name, err)
			continue
		}
		opaque := 0
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
					opaque++
				}
			}
		}
		// A tenth of the square is far below what the drawing covers and far
		// above anything an accident produces.
		if min := b.Dx() * b.Dy() / 10; opaque < min {
			t.Errorf("%s: %d pixels drawn out of %d, wanted at least %d",
				l.Name, opaque, b.Dx()*b.Dy(), min)
		}
	}
}

// **The name travels with the size**, because the manifest writes the file name
// and the format answers a name it cannot find by drawing the fallback rather
// than by failing. Two logos of the same name would be one file, and a name
// with an extension in it would become `Square44x44Logo.png.png` where the tool
// adds one.
func TestTheLogoNamesAreDistinctAndCarryNoExtension(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range PackageLogos {
		if l.Name == "" {
			t.Error("a logo with no name is a file the manifest cannot ask for")
		}
		if seen[l.Name] {
			t.Errorf("%s: named twice, so one of the two is not written", l.Name)
		}
		seen[l.Name] = true
		if len(l.Name) > 4 && l.Name[len(l.Name)-4] == '.' {
			t.Errorf("%s: carries its own extension, and the tool adds one", l.Name)
		}
	}
}
