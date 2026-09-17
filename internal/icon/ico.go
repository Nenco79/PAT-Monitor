package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
)

// Image is one of the sizes inside the icon, already encoded in the form it
// lives in inside a .ico and inside an RT_ICON resource — which are the same
// format, and that is why one struct serves both.
type Image struct {
	Side int
	PNG  bool // otherwise it is a DIB
	Data []byte
}

// Sides are the sizes the icon holds.
//
// They are not free choices: Windows picks among these and only rescales when
// it cannot find the one it needs, and a rescale arrives blurred exactly where
// the icon is small. 16 is the Explorer list and the title bar, 32 the desktop,
// 48 the large icons, 256 the tile view and the system dialogs.
var Sides = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

// Images draws and encodes every size.
//
// **256 goes in PNG, the others in DIB**, and that is not a preference: a
// 256-pixel DIB weighs 256 KB against the few KB of the PNG, and Windows has
// read PNG inside icons since Vista. Below that size the DIB is the road every
// version and every path of the shell has always known how to read, and there
// the saving does not exist anyway.
func Images() []Image {
	out := make([]Image, 0, len(Sides))
	for _, l := range Sides {
		img := Draw(l)
		if l >= 256 {
			var b bytes.Buffer
			// The error cannot arrive: the write goes to memory and the image
			// is always valid. Ignoring it here avoids propagating an error no
			// caller could handle any differently.
			_ = png.Encode(&b, img)
			out = append(out, Image{Side: l, PNG: true, Data: b.Bytes()})
			continue
		}
		out = append(out, Image{Side: l, Data: dib(img)})
	}
	return out
}

// dib encodes the image the way an icon wants it: BITMAPINFOHEADER, then the
// BGRA pixels bottom-up, then the AND mask.
//
// Two traps of the format, both compulsory:
//
//   - **the declared height is twice the real one.** The field describes the
//     colour bitmap plus the mask, which sit one above the other. Writing the
//     right height there produces an icon that loads and shows half of itself;
//   - **the AND mask has to be there at 32 bits too**, where it is useless
//     because the transparency lives in the alpha channel. It goes in as zeros,
//     that is, "all opaque", and leaving it out shortens the resource by just
//     enough to make whoever counts it read the wrong bytes.
func dib(img *image.NRGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	maskRow := ((w + 31) / 32) * 4

	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	put(uint32(40))   // biSize
	put(int32(w))     // biWidth
	put(int32(h * 2)) // biHeight: colours + mask
	put(uint16(1))    // biPlanes
	put(uint16(32))   // biBitCount
	put(uint32(0))    // biCompression: BI_RGB
	put(uint32(w*h*4 + maskRow*h))
	put(int32(0))  // biXPelsPerMeter
	put(int32(0))  // biYPelsPerMeter
	put(uint32(0)) // biClrUsed
	put(uint32(0)) // biClrImportant

	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			// Straight alpha out of an NRGBA into the BGRA an icon wants: a
			// copy, with no conversion, which is what the format asks for.
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	b.Write(make([]byte, maskRow*h))
	return b.Bytes()
}

// ICO packs the images into a .ico file.
//
// The monitor does not use it — what goes in the binary is the resource, not
// the file — but it is there for anyone who has to give an icon to a shortcut,
// an installer or a page, and it is also the only way to **look** at what we
// have drawn without compiling.
func ICO(im []Image) []byte {
	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	put(uint16(0))       // reserved
	put(uint16(1))       // type: icon
	put(uint16(len(im))) // how many
	off := 6 + 16*len(im)
	for _, i := range im {
		b.Write(dirEntry(i))
		put(uint32(len(i.Data)))
		put(uint32(off))
		off += len(i.Data)
	}
	for _, i := range im {
		b.Write(i.Data)
	}
	return b.Bytes()
}

// dirEntry is the first **eight** bytes common to ICONDIRENTRY and
// GRPICONDIRENTRY: only what comes after differs, the offset in the file or the
// resource identifier.
//
// Counted: bWidth, bHeight, bColorCount, bReserved, then wPlanes and wBitCount
// at two bytes each. The comment said six, and both callers already depended on
// eight — ICO strides 16 (8 + 4 + 4) and group strides 14 (8 + 4 + 2). Trusting
// the number and trimming the slice gives an .ico whose second entry starts two
// bytes early and a group whose identifiers are read out of the byte count,
// which is the very fault the note in `dib` three functions above warns about:
// short by just enough to make whoever counts read the wrong bytes.
//
// **256 is written as zero.** The field is one byte, so 256 does not fit, and
// the convention is that zero means 256. Writing 255 produces an icon that
// exists and gets chosen badly.
func dirEntry(i Image) []byte {
	side := byte(i.Side)
	if i.Side >= 256 {
		side = 0
	}
	return []byte{
		side, side, // width, height
		0, 0, // palette colours, reserved
		1, 0, // planes
		32, 0, // bits per pixel
	}
}
