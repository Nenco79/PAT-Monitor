package icon

import (
	"bytes"
	"fmt"
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
	files := Files()
	if len(files) == 0 {
		t.Fatal("no logo is produced, and a manifest that names one gets nothing")
	}
	for _, f := range files {
		img, err := png.Decode(bytes.NewReader(PNG(f.Side)))
		if err != nil {
			t.Errorf("%s: does not decode: %v", f.Name, err)
			continue
		}
		b := img.Bounds()
		if b.Dx() != f.Side || b.Dy() != f.Side {
			t.Errorf("%s: %dx%d, wanted %dx%d", f.Name, b.Dx(), b.Dy(), f.Side, f.Side)
		}
	}
}

// **The target sizes are named the way the resource index was built against**,
// and nowhere else: `<base>.targetsize-<n>`. A variant spelled at the call site
// is a second spelling of the contract, and it is inert rather than wrong —
// the shell falls back on the base and the only sign is an icon that is
// slightly soft at small sizes.
func TestTheTargetSizesAreNamedAfterTheirBase(t *testing.T) {
	for _, l := range PackageLogos {
		for _, s := range l.TargetSizes {
			want := fmt.Sprintf("%s.targetsize-%d", l.Name, s)
			found := false
			for _, f := range Files() {
				if f.Name == want {
					if f.Side != s {
						t.Errorf("%s: drawn at %d", want, f.Side)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("%s: declared and not produced", want)
			}
		}
	}
}

// **And each of them has something on it.** A logo that encodes, decodes and is
// entirely transparent passes every check made on its shape, installs, and
// leaves the shell drawing the fallback — the failure the format gives no error
// for.
func TestNoPackageLogoIsBlank(t *testing.T) {
	for _, l := range Files() {
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
	for _, l := range Files() {
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
