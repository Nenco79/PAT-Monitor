package icon

import (
	"bytes"
	"fmt"
	"image/png"
)

// Logo is one of the images a package manifest asks for, and the name it asks
// for it by.
//
// **The name is part of the contract and travels with the size.** The manifest
// writes `Square44x44Logo="Assets\Square44x44Logo.png"`, so a file named
// otherwise is a package that builds, installs, and shows nothing where the
// icon should be — the format gives no error for a logo it cannot find, it
// draws the fallback. Keeping the two halves in one value is what stops a
// rename touching one of them.
type Logo struct {
	Name string
	Side int
	// TargetSizes are the sizes shipped beside it as
	// `<Name>.targetsize-<n>.png`, for the shell to pick among the way it picks
	// among Sides.
	TargetSizes []int
}

// PackageLogos are the images the package manifest names.
//
// **Not one of the three sizes is in Sides, and none of them can be had from
// it.** The icon inside the executable is drawn at the sizes the shell picks
// among; these are the sizes a manifest names, and the two lists were decided
// by different people for different purposes. Interpolating them from the
// largest of the others is the obvious shortcut and the wrong one: the drawing
// simplifies as it shrinks — at small sizes the mouth and the glint come out as
// dirt and are left out on purpose — so a 44 scaled down from 256 carries
// exactly the detail that was meant to disappear, softened. Drawn at 44 it is
// the drawing made for 44.
//
// Three names and not more: 44 and 150 are what `uap:VisualElements` requires,
// and 50 is the `Properties/Logo`.
//
// **The 44 carries target sizes, and leaving them out was the same mistake one
// floor down.** The taskbar and the app list ask for 16, 24 and 32, and a
// package that ships only the 44 has Windows shrink it — which is the rescale
// the icon is drawn rather than scaled to avoid. Measured against a package
// without them, on the image the shell hands back: at 16 px the distance from
// the drawing made at 16 goes from 13.37 to 9.48, and at 32 px from 9.35 to
// 5.02. What is left over is the shell's own treatment, which is the same for
// both and is why neither reaches zero.
//
// **And they are inert without a resource index.** Measured: a package carrying
// the variants and no `resources.pri` gives back an image identical, pixel for
// pixel, to one that does not carry them at all — 0.00. So `makepri` is part of
// producing the package and not an improvement to it: without that step these
// files travel and nothing reads them.
//
// The sizes are the ones the shell asks for and we draw, which is not the same
// list as Sides: 128 is in Sides and is not a target size the shell knows, and
// the drawing is happy at any size anyway.
//
// The `.scale-*` family is deliberately not produced: those are for a manifest
// declaring a scale-aware asset, and the target sizes are what the small icon
// is actually chosen from.
var PackageLogos = []Logo{
	{
		Name:        "Square44x44Logo",
		Side:        44,
		TargetSizes: []int{16, 20, 24, 32, 40, 48, 64, 256},
	},
	{Name: "Square150x150Logo", Side: 150},
	{Name: "StoreLogo", Side: 50},
}

// StoreTile is the listing's own icon, the "1:1 App tile icon" Partner Center
// asks for at 300x300.
//
// **It is not a package logo, and that is why it is not in PackageLogos.** The
// manifest does not name it and the package does not carry it: it is uploaded
// by hand to the Store listing. Without it the Store falls back to the image in
// the package, which is StoreLogo at 50, and shows it six times larger — the
// rescale the icon is drawn rather than scaled to avoid, done by somebody
// else's code where nobody here can see it.
//
// It is drawn, like the others, rather than made in an editor: a hand-made copy
// is a second drawing, and it goes stale the first time this one changes.
var StoreTile = File{Name: "StoreTile300x300", Side: 300}

// File is one image to write, already named the way the package wants it.
type File struct {
	Name string // without the extension
	Side int
}

// Files is every image a package carries, the bases and their target sizes.
//
// It exists so that whoever writes them does not compose the variant's name: a
// name composed at the call site is the second spelling of a contract this file
// owns, and the one that ends up differing from what the resource index was
// built against.
func Files() []File {
	var out []File
	for _, l := range PackageLogos {
		out = append(out, File{Name: l.Name, Side: l.Side})
		for _, s := range l.TargetSizes {
			out = append(out, File{Name: fmt.Sprintf("%s.targetsize-%d", l.Name, s), Side: s})
		}
	}
	return out
}

// PNG draws the icon at that size and encodes it.
//
// It answers with the bytes alone for `Images`' reason: the write goes to
// memory and the image is always valid, so the error cannot arrive, and
// propagating one no caller could handle differently only invites it to be
// ignored somewhere less visible.
func PNG(side int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, Draw(side))
	return b.Bytes()
}
