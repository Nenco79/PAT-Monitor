package icon

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"unicode/utf16"
)

// **A wrong resource does not protest.** It is the same shape as the GUIDs
// written by hand: the linker accepts the object, the executable starts, and
// Windows shows the default icon with nothing anywhere saying why. There is no
// error to read, so the tests have to re-read what we wrote with the same
// method whoever consumes it uses — the tree walked from the start, not the
// fields recounted by the code that produced them.

func TestTheDrawingStaysInTheBox(t *testing.T) {
	// The box is written by hand and the shapes are not: if somebody lengthens
	// an ear the drawing runs off the disc, and at 256 pixels only a human
	// notices. A dense grid is sampled in SVG coordinates and every coloured
	// point has to fall inside what we declared.
	const step = 0.25
	for x := xMin - 8; x <= xMax+8; x += step {
		for y := yMin - 8; y <= yMax+8; y += step {
			if !painted(x, y) {
				continue
			}
			if x < xMin || x > xMax || y < yMin || y > yMax {
				t.Fatalf("the drawing touches %.2f,%.2f, outside the box %g,%g..%g,%g",
					x, y, xMin, yMin, xMax, yMax)
			}
		}
	}
}

// painted says whether there is any of the dog at that point, in SVG
// coordinates.
func painted(x, y float64) bool {
	return inEllipse(x, y, 45, 36, 20, 18) ||
		inPolygon(x, y, earShape) ||
		inEllipse(x, y, 28, 43, 13, 10) ||
		inEllipse(x, y, 18, 40, 5, 4) ||
		nearTo(x, y, mouthShape, 1.0) ||
		inEllipse(x, y, 40, 32, 3.4, 3.4)
}

func TestTheBoxIsTightOnEverySide(t *testing.T) {
	// The other half of the test above: a box that is too wide would pass it
	// anyway, and the drawing would come out shrunk with a margin nobody asked
	// for. On each side there has to be at least one painted point within half
	// a unit of the edge.
	cases := []struct {
		name    string
		touches func() bool
	}{
		{"left", func() bool { return scanColumn(xMin, xMin+0.5) }},
		{"right", func() bool { return scanColumn(xMax-0.5, xMax) }},
		{"top", func() bool { return scanRow(yMin, yMin+0.5) }},
		{"bottom", func() bool { return scanRow(yMax-0.5, yMax) }},
	}
	for _, c := range cases {
		if !c.touches() {
			t.Errorf("the %s side of the box is loose: no shape touches it", c.name)
		}
	}
}

func scanColumn(x0, x1 float64) bool {
	for x := x0; x <= x1; x += 0.05 {
		for y := yMin; y <= yMax; y += 0.05 {
			if painted(x, y) {
				return true
			}
		}
	}
	return false
}

func scanRow(y0, y1 float64) bool {
	for y := y0; y <= y1; y += 0.05 {
		for x := xMin; x <= xMax; x += 0.05 {
			if painted(x, y) {
				return true
			}
		}
	}
	return false
}

func TestEverySizeIsSquareAndOpaqueAtTheCentre(t *testing.T) {
	for _, l := range []int{16, 32, 256} {
		img := Draw(l)
		if b := img.Bounds(); b.Dx() != l || b.Dy() != l {
			t.Fatalf("%d: size %v", l, b)
		}
		// The centre is inside the disc by construction: if it is transparent,
		// the disc was not drawn at all.
		if a := img.NRGBAAt(l/2, l/2).A; a != 0xFF {
			t.Errorf("%d: the centre has alpha %d, the disc is not there", l, a)
		}
		// And the corners are outside: if they are opaque the disc has become a
		// square, that is, the clipping did not work.
		if a := img.NRGBAAt(0, 0).A; a != 0 {
			t.Errorf("%d: the corner has alpha %d, the disc overflows", l, a)
		}
	}
}

func TestTheSmallSizesDoNotCarryTheGlint(t *testing.T) {
	// At 16 pixels the glint in the eye measures two tenths of a pixel: drawn,
	// it is a light smudge in the middle of the snout. The test does not look
	// at the code but at the result: in the small icon there must be no pixel
	// of the glint's colour.
	small := Draw(16)
	for y := range 16 {
		for x := range 16 {
			if small.NRGBAAt(x, y) == eyeLight {
				t.Fatalf("the glint appears at 16 pixels, at %d,%d", x, y)
			}
		}
	}
}

// **The stride the two containers are built on.** `dirEntry` is shared by
// ICONDIRENTRY and GRPICONDIRENTRY, and each caller advances by that length plus
// its own tail: 16 in the .ico, 14 in the group resource. The doc above it used
// to say six, and nothing compared the sentence with the slice — a wrong stride
// does not fail, it produces a well-formed container whose entries are read from
// the wrong offset, which is the family this package is a monument to.
func TestTheDirectoryEntryIsEightBytes(t *testing.T) {
	if n := len(dirEntry(Image{Side: 16})); n != 8 {
		t.Errorf("dirEntry is %d bytes: the .ico strides 16 and the group 14, both "+
			"of which count on 8, so every entry after the first is read from the "+
			"wrong offset — and nothing about that looks like an error", n)
	}
}

func TestTheICOContainerReadsBack(t *testing.T) {
	im := Images()
	f := ICO(im)

	if got := binary.LittleEndian.Uint16(f[2:]); got != 1 {
		t.Fatalf("type %d, wanted 1 (icon)", got)
	}
	n := int(binary.LittleEndian.Uint16(f[4:]))
	if n != len(im) {
		t.Fatalf("%d entries for %d images", n, len(im))
	}
	for k := range n {
		v := f[6+16*k:]
		size := int(binary.LittleEndian.Uint32(v[8:]))
		off := int(binary.LittleEndian.Uint32(v[12:]))
		if off+size > len(f) {
			t.Fatalf("entry %d: the data runs off the file", k)
		}
		if !bytes.Equal(f[off:off+size], im[k].Data) {
			t.Fatalf("entry %d: the data is not the image's", k)
		}
		// 256 is written as zero: that is the convention, and getting it wrong
		// produces an icon that exists and gets chosen badly.
		want := byte(im[k].Side)
		if im[k].Side >= 256 {
			want = 0
		}
		if v[0] != want || v[1] != want {
			t.Errorf("entry %d: declared side %dx%d, wanted %d", k, v[0], v[1], want)
		}
	}
}

func TestTheDIBDeclaresTwiceTheHeight(t *testing.T) {
	// The format's trap: the height in the header counts the colours plus the
	// mask. Writing the real height gives an icon that loads and shows half of
	// itself, which is the kind of fault noticed late.
	d := dib(Draw(32))
	if w := int32(binary.LittleEndian.Uint32(d[4:])); w != 32 {
		t.Fatalf("width %d", w)
	}
	if h := int32(binary.LittleEndian.Uint32(d[8:])); h != 64 {
		t.Fatalf("declared height %d, wanted 64", h)
	}
	maskRow := ((32 + 31) / 32) * 4
	if want := 40 + 32*32*4 + maskRow*32; len(d) != want {
		t.Fatalf("the DIB is %d bytes, wanted %d: the AND mask is missing", len(d), want)
	}
}

// **The rim is straight alpha, and the two ways out of the drawing have to
// agree about that.**
//
// Draw averages the colour over the samples that were covered and sets the alpha
// to the coverage, so on the antialiased rim a component regularly exceeds its
// own alpha - measured, 112 and 110 against 63. Those are straight values, and
// the surface used to be an `image.RGBA`, which Go defines as premultiplied. The
// .ico was right, because `dib` copies the bytes verbatim into BGRA, which wants
// straight; `png.Encode` believed the type and un-premultiplied, so the 256
// entry and every size the web manifest draws came out with a lightened rim -
// the same pixel written as 93/199/190. Nothing looked broken, which is the
// family this package is a monument to.
//
// The test asks the property rather than the type: whatever is decoded back out
// of the PNG has to be the pixel that was drawn.
func TestThePNGCarriesThePixelsThatWereDrawn(t *testing.T) {
	img := Draw(256)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	dec, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// The rim is where the two conventions differ; the interior is opaque, where
	// they agree, so a test that never looks at a partial pixel absolves the
	// defect.
	partial := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			want := img.NRGBAAt(x, y)
			if want.A > 0 && want.A < 255 {
				partial++
			}
			got := color.NRGBAModel.Convert(dec.At(x, y)).(color.NRGBA)
			if got != want {
				t.Fatalf("at %d,%d the drawing holds %v and the PNG gives back %v: "+
					"the two ends disagree about whether the alpha is premultiplied",
					x, y, want, got)
			}
		}
	}
	if partial == 0 {
		t.Fatal("no partially transparent pixel was compared: the test looked only " +
			"where the two conventions agree, which absolves the defect it exists for")
	}
}

func TestThe256IsPNG(t *testing.T) {
	// A 256-pixel DIB weighs 256 KB, the PNG a few thousand bytes. If the
	// condition ever inverted by mistake, the resource would grow twentyfold
	// with nothing saying so.
	for _, i := range Images() {
		if i.Side == 256 {
			if !i.PNG {
				t.Fatal("the 256 is not in PNG")
			}
			if _, err := png.Decode(bytes.NewReader(i.Data)); err != nil {
				t.Fatalf("the 256 does not decode: %v", err)
			}
			return
		}
	}
	t.Fatal("there is no 256")
}

// --------------------------------------------------------- the COFF object

func TestTheResourceIsWalkedFromTheStart(t *testing.T) {
	im := Images()
	obj, err := Syso(im, testVersion(), "amd64")
	if err != nil {
		t.Fatal(err)
	}

	// The header, read the way the linker reads it.
	if m := binary.LittleEndian.Uint16(obj[0:]); m != machineAMD64 {
		t.Fatalf("machine %#x", m)
	}
	if n := binary.LittleEndian.Uint16(obj[2:]); n != 1 {
		t.Fatalf("%d sections, wanted one", n)
	}
	sec := obj[20:60]
	if string(bytes.TrimRight(sec[:8], "\x00")) != ".rsrc" {
		t.Fatalf("section %q", sec[:8])
	}
	size := int(binary.LittleEndian.Uint32(sec[16:]))
	start := int(binary.LittleEndian.Uint32(sec[20:]))
	nReloc := int(binary.LittleEndian.Uint16(sec[32:]))
	rsrc := obj[start : start+size]

	// **One relocation per leaf, and not one more.** If one were missing, that
	// icon would point at the start of the file: it is the fault the linker
	// cannot report.
	if want := len(im) + 2; nReloc != want {
		t.Fatalf("%d relocations for %d leaves", nReloc, want)
	}

	kinds := readDirectory(t, rsrc, 0)
	icons, ok := kinds[rtIcon]
	if !ok {
		t.Fatal("RT_ICON is missing")
	}
	groups, ok := kinds[rtGroupIcon]
	if !ok {
		t.Fatal("RT_GROUP_ICON is missing")
	}

	iconIDs := readDirectory(t, rsrc, icons)
	if len(iconIDs) != len(im) {
		t.Fatalf("%d icons for %d images", len(iconIDs), len(im))
	}
	for k, i := range im {
		id := uint32(k + 1)
		sub, ok := iconIDs[id]
		if !ok {
			t.Fatalf("icon %d is missing", id)
		}
		langs := readDirectory(t, rsrc, sub)
		leaf, ok := langs[neutralLanguage]
		if !ok {
			t.Fatalf("icon %d: the neutral language is missing", id)
		}
		if got := readLeaf(t, rsrc, leaf); !bytes.Equal(got, i.Data) {
			t.Fatalf("icon %d: %d bytes instead of %d", id, len(got), len(i.Data))
		}
	}

	groupIDs := readDirectory(t, rsrc, groups)
	sub, ok := groupIDs[groupID]
	if !ok {
		t.Fatalf("the group does not have identifier %d", groupID)
	}
	langs := readDirectory(t, rsrc, sub)
	g := readLeaf(t, rsrc, langs[neutralLanguage])

	// The group has to list every size and point at identifiers that really
	// exist: a list naming an icon that is not there is how Windows falls back
	// to the default icon.
	if n := int(binary.LittleEndian.Uint16(g[4:])); n != len(im) {
		t.Fatalf("the group lists %d sizes for %d images", n, len(im))
	}
	for k := range im {
		v := g[6+14*k:]
		id := uint32(binary.LittleEndian.Uint16(v[12:]))
		if _, ok := iconIDs[id]; !ok {
			t.Errorf("the group names icon %d, which does not exist", id)
		}
		if size := int(binary.LittleEndian.Uint32(v[8:])); size != len(im[k].Data) {
			t.Errorf("entry %d: the group declares %d bytes, the icon has %d",
				k, size, len(im[k].Data))
		}
	}
}

func TestTwoGenerationsGiveTheSameBytes(t *testing.T) {
	// The .syso is regenerated on every build: were it not deterministic, every
	// build would change the binary and "has anything changed?" would stop
	// having an answer. The date in the header is zero on purpose.
	a, err := Syso(Images(), testVersion(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Syso(Images(), testVersion(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two generations give different bytes")
	}
}

func TestAnUnknownArchitectureIsRefused(t *testing.T) {
	if _, err := Syso(Images(), testVersion(), "riscv64"); err == nil {
		t.Fatal("accepted an architecture whose relocation type we do not know")
	}
}

// readDirectory walks a resource directory the way whoever consumes it does,
// and returns the entries by identifier. The value is the offset of the subtree
// or of the leaf, with the high bit already stripped.
func readDirectory(t *testing.T, rsrc []byte, off int) map[uint32]int {
	t.Helper()
	if off+dirSize > len(rsrc) {
		t.Fatalf("directory at %d outside the section", off)
	}
	named := int(binary.LittleEndian.Uint16(rsrc[off+12:]))
	numbered := int(binary.LittleEndian.Uint16(rsrc[off+14:]))
	if named != 0 {
		t.Fatalf("directory at %d: %d named entries, we use none", off, named)
	}
	out := make(map[uint32]int, numbered)
	previous := int64(-1)
	for k := range numbered {
		v := off + dirSize + k*entrySize
		id := binary.LittleEndian.Uint32(rsrc[v:])
		// The order is not decoration: whoever reads does a binary search.
		if int64(id) <= previous {
			t.Fatalf("directory at %d: identifiers not increasing (%d after %d)",
				off, id, previous)
		}
		previous = int64(id)
		out[id] = int(binary.LittleEndian.Uint32(rsrc[v+4:]) &^ 0x80000000)
	}
	return out
}

// readLeaf resolves an IMAGE_RESOURCE_DATA_ENTRY. The RVA it holds is relative
// to the section, because in the object the section sits at zero and the
// relocation has not been applied yet: it is exactly what the linker will find
// to fix up.
func readLeaf(t *testing.T, rsrc []byte, off int) []byte {
	t.Helper()
	where := int(binary.LittleEndian.Uint32(rsrc[off:]))
	howMuch := int(binary.LittleEndian.Uint32(rsrc[off+4:]))
	if where+howMuch > len(rsrc) {
		t.Fatalf("leaf at %d: %d bytes from %d run off the section",
			off, howMuch, where)
	}
	return rsrc[where : where+howMuch]
}

// ------------------------------------------------------ the file's details

func testVersion() *VersionInfo {
	return &VersionInfo{
		// Obviously fake numbers, not those of a real version: a fixture citing
		// a real commit goes stale, and after a change of history it cites a
		// commit that no longer exists.
		Major: 0, Minor: 9, Patch: 9, Build: 4567,
		Prerelease:   true,
		Product:      "PAT Monitor",
		Description:  "Pet and baby monitor: webcam and microphone over WebRTC",
		Version:      "0.9.9 r4567 (abc1234)",
		Copyright:    "Copyright 2026 Nenco79 — Apache-2.0",
		FileName:     "pat-monitor.exe",
		InternalName: "pat-monitor",
	}
}

// **The format is built to go wrong quietly**: the lengths are redundant,
// whoever reads trusts the first one and takes the bytes it finds. An empty
// sheet, or one with somebody else's field in it, produces no error anywhere —
// it is the family of the GUID written by hand.
//
// So it is read back by **walking**, the way whoever consumes it does, instead
// of recounting the fields with the code that wrote them.
func TestTheFileDetailsAreWalkedFromTheStart(t *testing.T) {
	blob, err := testVersion().Blob()
	if err != nil {
		t.Fatal(err)
	}
	if len(blob)%4 != 0 && len(blob) != int(binary.LittleEndian.Uint16(blob)) {
		t.Fatalf("declared length %d, bytes %d",
			binary.LittleEndian.Uint16(blob), len(blob))
	}

	root := readNode(t, blob)
	if root.key != "VS_VERSION_INFO" {
		t.Fatalf("root %q", root.key)
	}
	// VS_FIXEDFILEINFO: the signature is the first field, and it is the only
	// thing that tells whoever reads they are in the right place.
	if len(root.value) != 52 {
		t.Fatalf("VS_FIXEDFILEINFO of %d bytes, 52 are needed", len(root.value))
	}
	if f := binary.LittleEndian.Uint32(root.value); f != 0xFEEF04BD {
		t.Fatalf("signature %#x", f)
	}
	// The four digits sit in two DWORDs, high and low. **The fourth is the
	// commit count**: it is the field that exists for the purpose, and it is
	// the reason the third does not become one.
	ms := binary.LittleEndian.Uint32(root.value[8:])
	ls := binary.LittleEndian.Uint32(root.value[12:])
	if ms>>16 != 0 || ms&0xFFFF != 9 || ls>>16 != 9 || ls&0xFFFF != 4567 {
		t.Fatalf("version %d.%d.%d.%d", ms>>16, ms&0xFFFF, ls>>16, ls&0xFFFF)
	}

	// A pre-release declares itself in the flag, so whoever reads the file with
	// other tools does not have to infer it from the number. **The fixture says
	// so rather than letting it be derived from the digits**: `Major: 0` used
	// to be enough, and that link is precisely what broke at 1.0.0-beta.1.
	//
	// This check used to sit inside the version comparison above, after a
	// t.Fatalf — that is, it never ran at all.
	if flag := binary.LittleEndian.Uint32(root.value[28:]); flag&ffPrerelease == 0 {
		t.Errorf("a pre-release does not declare itself as one: flag %#x", flag)
	}

	fields := stringsOf(t, root)
	for key, want := range map[string]string{
		"ProductName":      "PAT Monitor",
		"FileVersion":      "0.9.9 r4567 (abc1234)",
		"ProductVersion":   "0.9.9 r4567 (abc1234)",
		"OriginalFilename": "pat-monitor.exe",
	} {
		if fields[key] != want {
			t.Errorf("%s = %q, wanted %q", key, fields[key], want)
		}
	}
	// A field present and empty takes up a row of the sheet to say nothing.
	for key, value := range fields {
		if value == "" {
			t.Errorf("the field %s is there and empty", key)
		}
	}
}

// A wrong tree does not protest: if the version resource were not there, or sat
// under a different type, Explorer would show a sheet with no version and
// nothing else would say so.
func TestTheVersionResourceIsInTheTree(t *testing.T) {
	obj, err := Syso(Images(), testVersion(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	sec := obj[20:60]
	rsrc := obj[binary.LittleEndian.Uint32(sec[20:]) : binary.LittleEndian.Uint32(sec[20:])+binary.LittleEndian.Uint32(sec[16:])]

	kinds := readDirectory(t, rsrc, 0)
	sub, ok := kinds[rtVersion]
	if !ok {
		t.Fatal("RT_VERSION is missing")
	}
	id := readDirectory(t, rsrc, sub)
	langs, ok := id[versionID]
	if !ok {
		t.Fatalf("the version resource does not have identifier 1: %v", id)
	}
	leaf, ok := readDirectory(t, rsrc, langs)[neutralLanguage]
	if !ok {
		t.Fatal("the language is missing")
	}
	size := binary.LittleEndian.Uint32(rsrc[leaf+4:])
	want, _ := testVersion().Blob()
	if int(size) != len(want) {
		t.Fatalf("leaf of %d bytes, the blob has %d", size, len(want))
	}
}

// Without a version the tree stays as it was: whoever builds by hand with `go
// build` has no numbers to stamp, and that is not a reason to go without the
// icon.
func TestWithoutAVersionTheIconsRemain(t *testing.T) {
	obj, err := Syso(Images(), nil, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	sec := obj[20:60]
	rsrc := obj[binary.LittleEndian.Uint32(sec[20:]) : binary.LittleEndian.Uint32(sec[20:])+binary.LittleEndian.Uint32(sec[16:])]
	kinds := readDirectory(t, rsrc, 0)
	if _, there := kinds[rtVersion]; there {
		t.Error("there is a version resource that was not asked for")
	}
	if _, there := kinds[rtGroupIcon]; !there {
		t.Error("the icon vanished along with the version")
	}
}

// ---- reading the format, written from the specification and not from our code

type parsedNode struct {
	key      string
	value    []byte
	children []byte
}

func readNode(t *testing.T, b []byte) parsedNode {
	t.Helper()
	if len(b) < 6 {
		t.Fatal("truncated node")
	}
	length := int(binary.LittleEndian.Uint16(b[0:]))
	valLen := int(binary.LittleEndian.Uint16(b[2:]))
	isText := binary.LittleEndian.Uint16(b[4:]) == 1
	if length > len(b) {
		t.Fatalf("node declaring %d bytes out of %d", length, len(b))
	}

	i := 6
	for i+1 < length && !(b[i] == 0 && b[i+1] == 0) {
		i += 2
	}
	key := fromUTF16(b[6:i])
	i = (i + 2 + 3) &^ 3 // terminator, then alignment

	// For text the length is in characters: doubling it is the step that gets
	// forgotten, and it produces a value cut in half with no errors.
	byteVal := valLen
	if isText {
		byteVal *= 2
	}
	if i+byteVal > length {
		t.Fatalf("value of %d bytes that does not fit in %d", byteVal, length)
	}
	value := b[i : i+byteVal]
	end := min((i+byteVal+3)&^3, length)
	return parsedNode{key: key, value: value, children: b[end:length]}
}

// stringsOf walks StringFileInfo and reports the pairs of its table.
func stringsOf(t *testing.T, root parsedNode) map[string]string {
	t.Helper()
	out := map[string]string{}
	var sfi *parsedNode
	for _, f := range childrenOf(t, root.children) {
		if f.key == "StringFileInfo" {
			c := f
			sfi = &c
		}
	}
	if sfi == nil {
		t.Fatal("StringFileInfo is missing")
	}
	tables := childrenOf(t, sfi.children)
	if len(tables) != 1 {
		t.Fatalf("%d tables, one is needed", len(tables))
	}
	// The table's key is language plus code page in hexadecimal, and it has to
	// match the DWORD in VarFileInfo: whoever reads looks for the block from
	// there.
	if tables[0].key != "040904B0" {
		t.Errorf("table %q", tables[0].key)
	}
	for _, s := range childrenOf(t, tables[0].children) {
		out[s.key] = strings.TrimRight(fromUTF16(s.value), "\x00")
	}
	return out
}

func childrenOf(t *testing.T, b []byte) []parsedNode {
	t.Helper()
	var out []parsedNode
	for len(b) >= 6 {
		n := readNode(t, b)
		out = append(out, n)
		step := (int(binary.LittleEndian.Uint16(b[0:])) + 3) &^ 3
		if step <= 0 || step > len(b) {
			break
		}
		b = b[step:]
	}
	return out
}

func fromUTF16(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return strings.TrimRight(string(utf16.Decode(u)), "\x00")
}

// The pre-release flag follows Prerelease, not the digits.
//
// **The two cases are chosen where the two rules disagree.** The earlier rule
// was "on while the major is zero", and on a 0.x it says the same thing as the
// new one — so a 0.x fixture distinguishes nothing, and the test above would
// pass with either. What separates them is exactly the shape we are about to
// ship: **major 1 and a label**, where the old rule switches the flag off while
// the number says the opposite.
//
// The other direction is the half that gets forgotten: a flag on by mistake is
// noticed by nobody — almost nobody opens the details of an .exe — and on the
// day the label falls away, a 1.0.0 still declaring itself a beta would tell a
// falsehood to whoever redistributes it, who is the audience that field exists
// for.
func TestThePreReleaseFlagFollowsTheLabelAndNotTheDigits(t *testing.T) {
	for _, c := range []struct {
		name  string
		label bool
	}{
		{"1.0.0-beta.1", true},
		{"1.0.0", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := testVersion()
			v.Major, v.Minor, v.Patch = 1, 0, 0
			v.Prerelease = c.label
			v.Version = c.name + " r4567 (abc1234)"

			b, err := v.Blob()
			if err != nil {
				t.Fatal(err)
			}
			flag := binary.LittleEndian.Uint32(readNode(t, b).value[28:])
			if on := flag&ffPrerelease != 0; on != c.label {
				t.Errorf("pre-release declared %v instead of %v: flag %#x", on, c.label, flag)
			}
		})
	}
}
