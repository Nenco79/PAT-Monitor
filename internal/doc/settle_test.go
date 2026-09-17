package doc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// **A relation asserted three times in prose and compared by nobody.**
//
// The quality loop must speak more slowly than the encoder watch can listen.
// `internal/rtc.qualitySettle` is how long that loop stays still after moving
// the bitrate; `internal/pipeline.bitrateVerifyAfter` is how long the watch
// waits before judging a command. If the first drops below the second, every
// window the watch measures contains a change, and it ends up comparing a new
// request against bytes the previous one produced.
//
// **That is not a hypothesis: it is the defect that already happened.** Coming
// down 631 → 372 → 300, the watch compared 300 with the 553 produced while 631
// was being asked for, concluded the encoder does not obey, and moved to
// rebuilding it — which on AMD answers `ProcessOutput: 0x8000FFFF` and
// interrupts the capture: four restarts in eighty seconds.
//
// The relation is written in the comment beside `qualitySettle` and twice more
// in `CLAUDE.md`, and until this file nothing compared the two numbers. Lower
// one or raise the other and the whole suite stayed green while that returned —
// which is the shape the document calls a measurement written next to a constant
// that contradicts it, one step before the contradiction.
//
// It is read from the source because **neither package can import the other**,
// and a copy of either number here would be the second list that diverges.
func TestTheQualityLoopSpeaksSlowerThanTheWatchListens(t *testing.T) {
	settle, ok := constDuration(t, "../rtc/quality.go", "qualitySettle")
	if !ok {
		t.Fatal("qualitySettle was not found in internal/rtc/quality.go: it has been " +
			"renamed or moved, and this guard is comparing nothing")
	}
	verify, ok := constDuration(t, "../pipeline/pipeline.go", "bitrateVerifyAfter")
	if !ok {
		t.Fatal("bitrateVerifyAfter was not found in internal/pipeline/pipeline.go: " +
			"it has been renamed or moved, and this guard is comparing nothing")
	}

	// Strictly above, not equal: at equal values a command lands exactly on the
	// boundary of the window that judges it, which is the case the wait exists
	// to keep out.
	if settle <= verify {
		t.Errorf("qualitySettle is %v and bitrateVerifyAfter is %v: the quality loop "+
			"now commands at least as often as the encoder watch judges, so every "+
			"window it measures contains a change and it compares a new request with "+
			"the bytes the previous one produced. That verdict moves the machine onto "+
			"the road that rebuilds the encoder, which on AMD interrupts the capture.",
			settle, verify)
	}
}

// constDuration reads one named duration constant out of a file.
func constDuration(t *testing.T, path, name string) (time.Duration, bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s cannot be parsed: %v", path, err)
	}
	var out time.Duration
	var found bool
	ast.Inspect(f, func(n ast.Node) bool {
		gen, ok := n.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			return true
		}
		for _, sp := range gen.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if id.Name != name || i >= len(vs.Values) {
					continue
				}
				d, ok := durationOf(vs.Values[i])
				if !ok {
					t.Fatalf("%s: %s is written in a shape this guard cannot read, "+
						"so the relation it is half of is not being checked",
						fset.Position(id.Pos()), name)
				}
				out, found = d, true
			}
		}
		return true
	})
	return out, found
}
