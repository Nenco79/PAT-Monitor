package icon

import (
	"bytes"
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
// Three and not more: 44 and 150 are what `uap:VisualElements` requires, and 50
// is the `Properties/Logo`. The scaled variants a manifest may also carry
// (`.scale-200` and the rest) are not produced, and that is a decision rather
// than an omission — Windows scales the one it has, and every extra file is
// another name that has to keep matching.
var PackageLogos = []Logo{
	{Name: "Square44x44Logo", Side: 44},
	{Name: "Square150x150Logo", Side: 150},
	{Name: "StoreLogo", Side: 50},
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
