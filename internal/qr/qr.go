// Package qr engraves a URL into a QR code and returns it as SVG.
//
// **It exists for one step of the configuration path**: whoever has the monitor
// on a computer and the phone in their hand has to be able to open Tailscale's
// authorisation address without transcribing it by hand. An address copied
// wrong is a dead end that looks like a fault of the program.
//
// **It is written here rather than pulled from a library**, for the same reason
// the tray icon is drawn rather than embedded: one executable, and no extra
// line in go.mod for a piece that fits in one file and will never change — the
// QR specification has stood still since 2000.
//
// **It deliberately covers one slice of the specification**, and that is what
// keeps the file readable: byte mode, error correction M, versions 1 to 6. That
// is 106 characters, far more than any URL this program produces, and in
// exchange the two most treacherous pieces disappear — the version information,
// which only exists from 7 up, and the table of alignment patterns, which from
// 2 to 6 is a single pattern in a known place. Asking for more than 106
// characters returns an error rather than producing a code that will not read.
package qr

import (
	"fmt"
	"strings"
)

// Tables from the specification, for correction level M and versions 1-6 only.
//
// The numbers check one another and the test verifies it: for every version,
// blocks x (data + correction) has to give the total codeword count. A typo in
// one of these rows would produce a code that looks right and cannot be read,
// which is exactly the kind of fault this package is shaped to avoid.
type version struct {
	blocks       int // data blocks, all the same size for versions 1-6
	dataPerBlock int // data codewords per block
	ecPerBlock   int // correction codewords per block
}

var versions = map[int]version{
	1: {blocks: 1, dataPerBlock: 16, ecPerBlock: 10},
	2: {blocks: 1, dataPerBlock: 28, ecPerBlock: 16},
	3: {blocks: 1, dataPerBlock: 44, ecPerBlock: 26},
	4: {blocks: 2, dataPerBlock: 32, ecPerBlock: 18},
	5: {blocks: 2, dataPerBlock: 43, ecPerBlock: 24},
	6: {blocks: 4, dataPerBlock: 27, ecPerBlock: 16},
}

// Centre of the single alignment pattern, per version. Version 1 has none.
var alignmentCentre = map[int]int{2: 18, 3: 22, 4: 26, 5: 30, 6: 34}

const maxVersion = 6

// Matrix is the engraved code: true means a dark module.
type Matrix struct {
	Size int
	mod  []bool
	used []bool // module already taken by a function pattern
}

func (m *Matrix) at(x, y int) bool     { return m.mod[y*m.Size+x] }
func (m *Matrix) set(x, y int, v bool) { m.mod[y*m.Size+x] = v; m.used[y*m.Size+x] = true }
func (m *Matrix) taken(x, y int) bool  { return m.used[y*m.Size+x] }

// Encode engraves text into a QR code.
func Encode(text string) (*Matrix, error) {
	data := []byte(text)

	v, err := pickVersion(len(data))
	if err != nil {
		return nil, err
	}
	spec := versions[v]
	totalData := spec.blocks * spec.dataPerBlock

	bits := encodeBytes(data, totalData)
	blocks, ec := splitAndCorrect(bits, spec)
	stream := interleave(blocks, ec, spec)

	// Every mask is tried and the one the specification's algorithm judges best
	// is kept. This is not a cosmetic detail: the penalties measure how much the
	// pattern resembles a finder pattern, and a wrong choice produces a code
	// some readers will not lock onto.
	var best *Matrix
	bestPenalty := -1
	for mask := 0; mask < 8; mask++ {
		m := newMatrix(v)
		m.drawFunctionPatterns(v)
		m.writeFormat(mask)
		m.writeData(stream, mask)
		if p := m.penalty(); bestPenalty < 0 || p < bestPenalty {
			bestPenalty, best = p, m
		}
	}
	return best, nil
}

func pickVersion(n int) (int, error) {
	for v := 1; v <= maxVersion; v++ {
		spec := versions[v]
		// Two codewords go to the header: 4 bits of mode and 8 of length. The
		// count is 8 bits up to version 9, so the 16-bit case is not here.
		if n <= spec.blocks*spec.dataPerBlock-2 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("qr: %d bytes do not fit a code up to version %d", n, maxVersion)
}

// ---------------------------------------------------------------- data

// encodeBytes produces the data codewords, padding included.
func encodeBytes(data []byte, totalData int) []byte {
	var b bitWriter
	b.write(0b0100, 4)    // byte mode
	b.write(len(data), 8) // count, 8 bits up to version 9
	for _, c := range data {
		b.write(int(c), 8)
	}
	// Terminator: up to four zeros, but never past the capacity.
	if left := totalData*8 - b.n; left > 0 {
		b.write(0, min(4, left))
	}
	b.pad()

	out := b.bytes()
	// The padding the specification prescribes: 0xEC and 0x11 alternating. It
	// is not arbitrary, and using zeros would produce a valid code with a flat
	// pattern, which readers lock onto less well.
	for i := 0; len(out) < totalData; i++ {
		if i%2 == 0 {
			out = append(out, 0xEC)
		} else {
			out = append(out, 0x11)
		}
	}
	return out
}

type bitWriter struct {
	buf []byte
	n   int
}

func (b *bitWriter) write(v, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.buf = append(b.buf, 0)
		}
		if v&(1<<i) != 0 {
			b.buf[b.n/8] |= 1 << (7 - b.n%8)
		}
		b.n++
	}
}

func (b *bitWriter) pad() {
	for b.n%8 != 0 {
		b.write(0, 1)
	}
}

func (b *bitWriter) bytes() []byte { return b.buf }

// splitAndCorrect breaks the data into blocks and computes each one's
// correction.
func splitAndCorrect(data []byte, spec version) (blocks, ec [][]byte) {
	gen := generator(spec.ecPerBlock)
	for i := 0; i < spec.blocks; i++ {
		b := data[i*spec.dataPerBlock : (i+1)*spec.dataPerBlock]
		blocks = append(blocks, b)
		ec = append(ec, reedSolomon(b, gen))
	}
	return blocks, ec
}

// interleave puts the blocks together the way the specification wants: first
// the first codeword of every data block, then the second, and so on; then the
// same for the correction.
//
// **It is needed with a single block too**, where it does nothing, and that is
// exactly why it is easy to write wrong and not notice: versions 1-3 have one
// block and would work anyway. The fault would appear at the first version 4,
// that is, at the first slightly longer URL.
func interleave(blocks, ec [][]byte, spec version) []byte {
	out := make([]byte, 0, spec.blocks*(spec.dataPerBlock+spec.ecPerBlock))
	for i := 0; i < spec.dataPerBlock; i++ {
		for _, b := range blocks {
			out = append(out, b[i])
		}
	}
	for i := 0; i < spec.ecPerBlock; i++ {
		for _, b := range ec {
			out = append(out, b[i])
		}
	}
	return out
}

// ------------------------------------------------- Reed-Solomon over GF(256)

var (
	exp [512]byte
	log [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D // the QR primitive polynomial
		}
	}
	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}
}

func mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return exp[int(log[a])+int(log[b])]
}

// generator builds the generator polynomial of degree n.
//
// **The coefficients are in order of decreasing degree**, that is, `g[0]` is
// the one for x^n and is always 1. The order is not a free convention:
// reedSolomon reads `gen[i+1]` taking for granted that the leading coefficient
// comes first, and with the polynomial the other way round it produces a
// correction that is plausible and wrong — a code that prints beautifully and
// that no phone reads.
//
// Building it in the wrong order gives `[45 32 94 ...]` where the right answer
// is `[0 251 67 ...]`, and everything else — the serpentine walk, the mask, the
// encoding, the padding, the format — can be exact while it happens. That is
// what makes the fault invisible to any reading of the code: the matrix looks
// right in every one of its parts.
func generator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		// multiply by (x + alpha^i)
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= mul(c, exp[i])
		}
		g = next
	}
	return g
}

// reedSolomon computes the correction codewords: they are the remainder of the
// data divided by the generator.
func reedSolomon(data, gen []byte) []byte {
	n := len(gen) - 1
	rem := make([]byte, n)
	for _, d := range data {
		f := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		if f != 0 {
			for i := 0; i < n; i++ {
				rem[i] ^= mul(gen[i+1], f)
			}
		}
	}
	return rem
}

// ------------------------------------------------------------- matrix

func newMatrix(v int) *Matrix {
	size := 17 + 4*v
	return &Matrix{Size: size, mod: make([]bool, size*size), used: make([]bool, size*size)}
}

func (m *Matrix) drawFunctionPatterns(v int) {
	// Finder patterns at three corners, with their light separator.
	for _, p := range [][2]int{{0, 0}, {m.Size - 7, 0}, {0, m.Size - 7}} {
		m.finder(p[0], p[1])
	}
	// Timing: the alternating row and column that give the reader its scale.
	for i := 8; i < m.Size-8; i++ {
		dark := i%2 == 0
		m.set(i, 6, dark)
		m.set(6, i, dark)
	}
	if c, ok := alignmentCentre[v]; ok {
		m.alignment(c, c)
	}
	// The fixed dark module: it is always there, and it does not belong to the
	// format.
	m.set(8, m.Size-8, true)

	// The 31 format modules are reserved now and filled in later: were they not
	// taken, the data would write over them.
	for i := 0; i < 9; i++ {
		if i != 6 {
			m.reserve(i, 8)
			m.reserve(8, i)
		}
	}
	for i := 0; i < 8; i++ {
		m.reserve(m.Size-1-i, 8)
		m.reserve(8, m.Size-1-i)
	}
}

func (m *Matrix) reserve(x, y int) {
	if !m.taken(x, y) {
		m.set(x, y, false)
	}
}

func (m *Matrix) finder(ox, oy int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := ox+dx, oy+dy
			if x < 0 || y < 0 || x >= m.Size || y >= m.Size {
				continue
			}
			border := dx == 0 || dx == 6 || dy == 0 || dy == 6
			centre := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
			inside := dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6
			m.set(x, y, inside && (border || centre))
		}
	}
}

func (m *Matrix) alignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			d := max(abs(dx), abs(dy))
			m.set(cx+dx, cy+dy, d != 1)
		}
	}
}

// writeFormat engraves the 15 format bits into the two prescribed copies.
//
// The code is a BCH(15,5): five data bits — two of correction level and three
// of mask — plus ten of check, the whole thing XORed with a fixed mask. That
// last one exists because without it the format "level M, mask 0" would be all
// zeros, that is, a light band the reader cannot tell from the quiet zone.
func (m *Matrix) writeFormat(mask int) {
	const levelM = 0b00
	data := levelM<<3 | mask

	rem := data << 10
	for i := 14; i >= 10; i-- {
		if rem&(1<<i) != 0 {
			rem ^= 0b10100110111 << (i - 10)
		}
	}
	format := (data<<10 | rem) ^ 0b101010000010010

	bit := func(i int) bool { return format&(1<<i) != 0 }

	// The two copies sit in different places and both are compulsory: a reader
	// that cannot make out the first — a shadow, a finger on the corner — has
	// to be able to take the second.
	for i := 0; i <= 5; i++ {
		m.set(8, i, bit(i))
	}
	m.set(8, 7, bit(6))
	m.set(8, 8, bit(7))
	m.set(7, 8, bit(8))
	for i := 9; i <= 14; i++ {
		m.set(14-i, 8, bit(i))
	}

	for i := 0; i <= 7; i++ {
		m.set(m.Size-1-i, 8, bit(i))
	}
	for i := 8; i <= 14; i++ {
		m.set(8, m.Size-15+i, bit(i))
	}
}

// writeData walks the matrix in a serpentine from the bottom right, skipping
// the modules already taken, and applies the mask as it writes.
func (m *Matrix) writeData(stream []byte, mask int) {
	bit := 0
	up := true
	for dx := m.Size - 1; dx > 0; dx -= 2 {
		if dx == 6 {
			dx-- // the timing column carries no data
		}
		for i := 0; i < m.Size; i++ {
			y := i
			if up {
				y = m.Size - 1 - i
			}
			for j := 0; j < 2; j++ {
				x := dx - j
				if m.taken(x, y) {
					continue
				}
				v := false
				if bit < len(stream)*8 {
					v = stream[bit/8]&(1<<(7-bit%8)) != 0
				}
				bit++
				if maskAt(mask, x, y) {
					v = !v
				}
				m.mod[y*m.Size+x] = v
			}
		}
		up = !up
	}
}

func maskAt(n, x, y int) bool {
	switch n {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (x*y)%2+(x*y)%3 == 0
	case 6:
		return ((x*y)%2+(x*y)%3)%2 == 0
	default:
		return ((x+y)%2+(x*y)%3)%2 == 0
	}
}

// penalty applies the specification's four rules: lower is better.
//
// The four live in separate functions rather than one block because that is the
// only way to look at them one at a time when the chosen mask is not
// convincing: a total that does not add up says nothing about which rule is
// wrong, and the four measure different things.
func (m *Matrix) penalty() int {
	return m.r1() + m.r2() + m.r3() + m.r4()
}

// 1: runs of five or more equal modules, across and down.
func (m *Matrix) r1() int {
	p := 0
	for i := 0; i < m.Size; i++ {
		p += m.runs(func(k int) bool { return m.at(k, i) })
		p += m.runs(func(k int) bool { return m.at(i, k) })
	}
	return p
}

// 2: 2x2 blocks of the same colour.
func (m *Matrix) r2() int {
	p := 0
	for y := 0; y < m.Size-1; y++ {
		for x := 0; x < m.Size-1; x++ {
			c := m.at(x, y)
			if m.at(x+1, y) == c && m.at(x, y+1) == c && m.at(x+1, y+1) == c {
				p += 3
			}
		}
	}
	return p
}

// 3: the pattern that resembles a finder pattern, across and down.
//
// **The two passes have different bounds.** Giving the vertical scan the
// horizontal one's bound makes it look at the first columns only and miss the
// last eleven rows. A penalty computed halfway does not produce a wrong code —
// it produces the wrong mask, which is worse to find, because the code still
// reads, only worse.
func (m *Matrix) r3() int {
	pattern := []bool{true, false, true, true, true, false, true, false, false, false, false}
	reversed := []bool{false, false, false, false, true, false, true, true, true, false, true}
	p := 0
	for y := 0; y < m.Size; y++ {
		for x := 0; x+11 <= m.Size; x++ {
			if m.matchesAcross(x, y, pattern) || m.matchesAcross(x, y, reversed) {
				p += 40
			}
		}
	}
	for x := 0; x < m.Size; x++ {
		for y := 0; y+11 <= m.Size; y++ {
			if m.matchesDown(x, y, pattern) || m.matchesDown(x, y, reversed) {
				p += 40
			}
		}
	}
	return p
}

// 4: imbalance between dark and light.
//
// The specification does not simply divide the percentage: it takes the
// multiple of five **below and above**, measures how far each is from 50, and
// keeps the smaller.
func (m *Matrix) r4() int {
	dark := 0
	for _, v := range m.mod {
		if v {
			dark++
		}
	}
	pct := dark * 100 / len(m.mod)
	down := pct / 5 * 5
	return min(abs(down-50)/5, abs(down+5-50)/5) * 10
}

func (m *Matrix) runs(get func(int) bool) int {
	p, run, prev := 0, 1, get(0)
	for k := 1; k < m.Size; k++ {
		c := get(k)
		if c == prev {
			run++
			continue
		}
		if run >= 5 {
			p += 3 + (run - 5)
		}
		run, prev = 1, c
	}
	if run >= 5 {
		p += 3 + (run - 5)
	}
	return p
}

func (m *Matrix) matchesAcross(x, y int, pat []bool) bool {
	for i, want := range pat {
		if m.at(x+i, y) != want {
			return false
		}
	}
	return true
}

func (m *Matrix) matchesDown(x, y int, pat []bool) bool {
	for i, want := range pat {
		if m.at(x, y+i) != want {
			return false
		}
	}
	return true
}

// Colour is one of the code's two tints, in components rather than in hex: the
// SVG wants them as text, whoever engraves a bitmap wants them as numbers.
type Colour struct{ R, G, B uint8 }

// Hex is the form CSS wants.
func (c Colour) Hex() string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// Paper and Ink are the code's two colours, and they live **here** because two
// different technologies use them: the SVG of the pages and the tray's GDI
// bitmap. Written twice they would diverge, and the way they would diverge is
// the worst one — a code that reads on one screen and not on the other.
//
// **They are not pure black and white**, and that is not an oversight: they are
// the paper and ink of the light palette, already proven against real phones.
// The contrast stays ample for a camera, and the code belongs to the product
// instead of being a cut-out.
var (
	Paper = Colour{0xFB, 0xF8, 0xF2}
	Ink   = Colour{0x2E, 0x2A, 0x24}
)

// QuietZone is how many light modules are needed around the code.
//
// **It is not decoration**: the specification prescribes it, and without it
// many readers do not lock onto the code — with a fault of the worst kind,
// which depends on the phone and therefore works on the phone of whoever wrote
// it.
const QuietZone = 4

// At says whether the module at (x, y) is dark. Outside the matrix it is light,
// that is, quiet zone: whoever draws the code has to be able to ask without
// keeping two different counts of the borders.
//
// It is for whoever engraves the code in another technology — today the tray,
// into a GDI bitmap — because the SVG below is not the only way to draw it.
func (m *Matrix) At(x, y int) bool {
	if x < 0 || y < 0 || x >= m.Size || y >= m.Size {
		return false
	}
	return m.at(x, y)
}

// ------------------------------------------------------------------ SVG

// SVG returns the code as a vector image.
//
// **The light margin around it is not decoration**: the specification calls it
// the quiet zone and prescribes four modules. Without it many readers do not
// lock onto the code, and the fault is of the worst kind — it depends on the
// phone, so it works on the phone of whoever wrote it.
func (m *Matrix) SVG(px int) string {
	side := m.Size + QuietZone*2

	var b strings.Builder
	// **The accessible name does not go here, and that is not an omission.**
	// Whoever engraves the code does not know what language its viewer reads,
	// and an aria-label written here would be the language of whoever wrote the
	// program — inside a multilingual product. Both pages show this SVG inside
	// an <img>, which is an atomic element: its accessible name is its alt, and
	// that comes from the catalogue. The words belong at the edges.
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`viewBox="0 0 %d %d" shape-rendering="crispEdges">`, px, px, side, side)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, side, side, Paper.Hex())

	// One path instead of a rect per module: there are a few thousand of them,
	// and the browser draws every one.
	fmt.Fprintf(&b, `<path fill="%s" d="`, Ink.Hex())
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			if m.at(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+QuietZone, y+QuietZone)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
