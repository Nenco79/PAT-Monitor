package qr

import (
	"strconv"
	"strings"
	"testing"
)

// **How this package was verified, and why the reference below is worth more
// than any amount of re-reading.**
//
// A QR encoder is built to fail quietly: the matrix looks right in every one of
// its parts even when the data inside is rubbish, and the only way to notice is
// to try to read it. So the package was compared module by module against
// github.com/skip2/go-qrcode, an implementation written by somebody else, added
// to the project for the length of the check and then removed. It is the same
// road as pat-opus, which tests libopus with an independent decoder instead of
// re-reading its own.
//
// Outcome: **identical module by module** from version 1 to 6, single block and
// multiple blocks, at equal mask. The check found a defect no re-reading would
// have found — the generator polynomial built backwards — and cleared
// everything else.
//
// The two implementations **choose different masks**, and that is not a defect:
// each of the eight produces a valid code, the choice is a robustness
// heuristic, and the two sets of penalties differ in rule 3. The reference is
// therefore frozen **at a fixed mask**, which is the only part the two agree on
// by construction.
//
// The check does not dodge that divergence, it turns it around: go-qrcode does
// not let the mask be imposed, so the code is engraved with **all eight** and
// exactly one has to match theirs. That is a stricter question, not an easier
// one — it says that under the mask they chose our data and our correction are
// the same bits, and that no other mask resembles it by accident.
//
// Where the text holds long runs of digits the two diverge for yet another
// reason: go-qrcode splits the text into segments and uses numeric mode for
// digits, which costs less. Those are two different encodings of the same text,
// both correct.

// reference is the code for "https://patmon.quercia-lieve.ts.net" at mask 0,
// which is the one go-qrcode chose, verified identical to it module by module.
var reference = []string{
	"#######...##..#..####.#######",
	"#.....#.#.#.##...####.#.....#",
	"#.###.#....#...#....#.#.###.#",
	"#.###.#..#.#.#....##..#.###.#",
	"#.###.#.##.###..####..#.###.#",
	"#.....#...###.##.####.#.....#",
	"#######.#.#.#.#.#.#.#.#######",
	"..........##...###.#.........",
	"#.#.#.#....#.....#.##...#..#.",
	"..####.#.#..##..#...###..#..#",
	"..###.##..#...#...#..#.##.###",
	"#.......#..########...#.#..#.",
	".######....##.#.##..###..#.##",
	"........###.#.#.##..###..#..#",
	"#...#.####..##..#...##..##.##",
	"..#.#....####...###..##..#.#.",
	"##..#.###...#..###....##.#.##",
	".#####.####..#..###.###..##.#",
	"#.#...###..##.#.##...#.##..##",
	".##.#........##.##.#####.#.#.",
	"#....##.##....####..#####....",
	"........#..#..#..#.##...#.###",
	"#######..#.#.#.....##.#.##.##",
	"#.....#...##.....##.#...##..#",
	"#.###.#.#...#....##.#####...#",
	"#.###.#..#.#....#......##.###",
	"#.###.#.##.#..#.##.###.###..#",
	"#.....#...#..#####..##.#...#.",
	"#######.#..#.##..#.####.##.##",
}

// The frozen reference is the test that holds all the others up: if the encoder
// changes behaviour, it shows here.
func TestTheFrozenReference(t *testing.T) {
	const s = "https://patmon.quercia-lieve.ts.net"
	const mask = 0

	m := engrave(t, s, mask)
	if m.Size != len(reference) {
		t.Fatalf("size %d, the reference is %d", m.Size, len(reference))
	}
	for y, row := range reference {
		if len(row) != m.Size {
			t.Fatalf("row %d of the reference is %d long", y, len(row))
		}
		for x := 0; x < m.Size; x++ {
			if want := row[x] == '#'; m.at(x, y) != want {
				t.Fatalf("module (%d,%d) differs from the reference", x, y)
			}
		}
	}
}

// engrave builds the matrix at an imposed mask, skipping the choice.
func engrave(t *testing.T, s string, mask int) *Matrix {
	t.Helper()
	v, err := pickVersion(len(s))
	if err != nil {
		t.Fatal(err)
	}
	spec := versions[v]
	bits := encodeBytes([]byte(s), spec.blocks*spec.dataPerBlock)
	bl, ec := splitAndCorrect(bits, spec)
	m := newMatrix(v)
	m.drawFunctionPatterns(v)
	m.writeFormat(mask)
	m.writeData(interleave(bl, ec, spec), mask)
	return m
}

// **The correction is checked by its property, not by recomputing it.**
//
// A complete Reed-Solomon codeword — data plus correction — is by construction
// divisible by the generator polynomial: evaluated at the generator's roots it
// has to give zero. Redoing the sum with the same code that produced it would
// prove nothing; this is a different question, and it is exactly the one a real
// decoder asks.
func TestTheSyndromesAreZero(t *testing.T) {
	cases := []string{
		"a",
		"https://patmon.quercia-lieve.ts.net",
		"http://192.168.1.50:8080/",
		"https://login.tailscale.com/admin/settings/features/enable",
		"https://login.tailscale.com/admin/settings/features/enable-https-now",
		"https://login.tailscale.com/admin/settings/features/enable-https-and-funnel-for-tailnet",
	}
	for _, s := range cases {
		v, err := pickVersion(len(s))
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		spec := versions[v]
		bits := encodeBytes([]byte(s), spec.blocks*spec.dataPerBlock)
		bl, ec := splitAndCorrect(bits, spec)

		for b := range bl {
			whole := append(append([]byte{}, bl[b]...), ec[b]...)
			for k := 0; k < spec.ecPerBlock; k++ {
				var acc byte
				for _, c := range whole {
					acc = mul(acc, exp[k]) ^ c
				}
				if acc != 0 {
					t.Errorf("v%d %q block %d: syndrome %d = %#02x, wanted 0", v, s, b, k, acc)
					break
				}
			}
		}
	}
}

// The degree-10 generator is published by the specification. A reversed order
// produces a code that prints beautifully and that no phone reads: it is the
// defect the independent cross-check found.
func TestThePublishedGenerator(t *testing.T) {
	want := []int{0, 251, 67, 46, 61, 118, 70, 64, 94, 32, 45}
	g := generator(10)
	if len(g) != len(want) {
		t.Fatalf("degree %d, wanted %d", len(g)-1, len(want)-1)
	}
	for i, c := range g {
		if int(log[c]) != want[i] {
			t.Errorf("coefficient %d: alpha^%d, wanted alpha^%d", i, log[c], want[i])
		}
	}
	if g[0] != 1 {
		t.Errorf("the leading coefficient is %d, it has to be 1", g[0])
	}
}

// The tables check one another: a typo would produce a code that looks right
// and cannot be read.
func TestTheTablesAgree(t *testing.T) {
	totals := map[int]int{1: 26, 2: 44, 3: 70, 4: 100, 5: 134, 6: 172}
	for v, spec := range versions {
		got := spec.blocks * (spec.dataPerBlock + spec.ecPerBlock)
		if got != totals[v] {
			t.Errorf("v%d: %d codewords, the specification wants %d", v, got, totals[v])
		}
		side := 17 + 4*v
		if c, ok := alignmentCentre[v]; ok && (c < 6 || c > side-7) {
			t.Errorf("v%d: alignment at %d, outside a %dx%d matrix", v, c, side, side)
		}
	}
	if _, ok := alignmentCentre[1]; ok {
		t.Error("version 1 has no alignment pattern")
	}
}

// The function patterns sit where the reader looks for them. They are the only
// part that does not depend on the data, so a mistake here breaks **every**
// code.
func TestTheFunctionPatternsAreInPlace(t *testing.T) {
	for v := 1; v <= maxVersion; v++ {
		m := newMatrix(v)
		m.drawFunctionPatterns(v)
		side := 17 + 4*v

		for _, o := range [][2]int{{0, 0}, {side - 7, 0}, {0, side - 7}} {
			if !m.at(o[0], o[1]) || !m.at(o[0]+3, o[1]+3) {
				t.Errorf("v%d: finder pattern missing at (%d,%d)", v, o[0], o[1])
			}
			if o[0]+7 < side && m.at(o[0]+7, o[1]) {
				t.Errorf("v%d: dark separator next to (%d,%d)", v, o[0], o[1])
			}
		}
		for i := 8; i < side-8; i++ {
			if m.at(i, 6) != (i%2 == 0) || m.at(6, i) != (i%2 == 0) {
				t.Errorf("v%d: wrong timing at %d", v, i)
				break
			}
		}
		if !m.at(8, side-8) {
			t.Errorf("v%d: the fixed dark module is missing", v)
		}
	}
}

// **Asking for more than fits has to give an error**, not a truncated code: a
// QR that reads and leads to a chopped address is worse than no QR at all,
// because it looks like it works.
func TestTooLongIsRefusedNotTruncated(t *testing.T) {
	if _, err := Encode(strings.Repeat("x", 106)); err != nil {
		t.Errorf("106 bytes ought to fit: %v", err)
	}
	if _, err := Encode(strings.Repeat("x", 107)); err == nil {
		t.Error("107 bytes accepted: the code would be silently truncated")
	}
}

// The light margin is prescribed: four modules a side. Without it many readers
// do not lock on, and the fault depends on the phone — so it works on the phone
// of whoever wrote it.
func TestTheSVGHasItsMarginAndOnePath(t *testing.T) {
	m, err := Encode("https://patmon.quercia-lieve.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	svg := m.SVG(240)
	side := strconv.Itoa(m.Size + 8)
	if !strings.Contains(svg, `viewBox="0 0 `+side+" "+side+`"`) {
		t.Errorf("the viewBox does not account for the margin: %.120s", svg)
	}
	if n := strings.Count(svg, "<path"); n != 1 {
		t.Errorf("%d paths: the modules have to live in one only", n)
	}
	if !strings.Contains(svg, `shape-rendering="crispEdges"`) {
		t.Error("without crispEdges the modules arrive blurred")
	}
}
