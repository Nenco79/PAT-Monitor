package rtc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// **The bandwidth estimate goes into the hub's map only once nothing can fail
// after it.**
//
// It used to be registered as soon as the Viewer existed, and five things below
// that point return an error without ever handing the viewer to anybody: adding
// the video track, adding the audio track, the talk-back transceiver, creating
// the offer and SetLocalDescription. Each closes the PeerConnection and returns;
// **the only code that removes an entry is Viewer.Close**, which on those paths
// nobody can call, because the viewer is never returned.
//
// What leaks is not the memory. `worstEstimate` counts the map and its count is
// the control loop's `viewers`, so a single dead entry means `viewers == 0`
// never happens again — and the three `release` functions, each written for a
// measured defect and each carrying its measurement, stop being called for the
// life of the process. Every viewer afterwards inherits the discount, the
// quantiser window and the estimate of whoever left. The estimate is taken as a
// **minimum**, so a dead viewer's frozen one holds the encoder down for
// everybody who follows.
//
// **It reads the source, and that is the honest instrument here.** Reaching
// those branches wants `AddTrack` or `CreateOffer` to fail, which this package
// cannot make happen on demand — and that is exactly why the defect survived:
// the paths are real, rare, and leave the monitor permanently degraded rather
// than broken. The property is positional and needs no failure to check: **no
// error return may stand between the registration and the end of the
// function.**
//
// The defect was put back — the block moved above `pc.AddTrack` — and this test
// fails with it, naming all five.
func TestTheEstimateIsRegisteredOnlyWhenNothingCanFail(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "hub.go", nil, 0)
	if err != nil {
		t.Fatalf("hub.go cannot be parsed: %v", err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "NewViewer" {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatal("NewViewer was not found: this guard is watching nothing")
	}

	// Where the entry is written: an assignment whose left-hand side indexes
	// something called bwe.
	registered := token.NoPos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		idx, ok := as.Lhs[0].(*ast.IndexExpr)
		if !ok {
			return true
		}
		if sel, ok := idx.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "bwe" {
			registered = as.Pos()
		}
		return true
	})
	if registered == token.NoPos {
		t.Fatal("nothing in NewViewer registers an estimate: the guard no longer " +
			"describes this function")
	}

	// Every error return: three results, the last of them not the bare nil of
	// the successful one.
	var late int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 3 {
			return true
		}
		if id, ok := ret.Results[2].(*ast.Ident); ok && id.Name == "nil" {
			return true // the successful return
		}
		if ret.Pos() > registered {
			late++
			t.Errorf("an error return at %s stands after the estimate has been "+
				"registered: nobody removes that entry, and from then on the loop "+
				"never sees zero viewers again", fset.Position(ret.Pos()))
		}
		return true
	})
	// And the control: there really are error returns in this function, so a
	// green result means they are all before the registration rather than that
	// there are none to find.
	var total int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 3 {
			return true
		}
		if id, ok := ret.Results[2].(*ast.Ident); ok && id.Name == "nil" {
			return true
		}
		total++
		return true
	})
	if total-late < 5 {
		t.Errorf("only %d error returns found before the registration: this "+
			"function used to have five after it, so the shape has moved and this "+
			"guard is absolving whatever replaced it", total-late)
	}
}

// The control from the other side: a registered estimate really is what makes
// the loop see a viewer, and Close really is what takes it away. Without this,
// the guard above would be asserting the position of a line whose effect nobody
// had checked.
func TestAnEntryInTheMapIsAViewerForTheLoop(t *testing.T) {
	h := New(Config{FPS: 30, BitrateKbps: 2500})
	now := time.Now()

	if _, viewers := h.worstEstimate(now); viewers != 0 {
		t.Fatalf("a fresh hub already counts %d viewers", viewers)
	}

	v := &Viewer{hub: h, closed: make(chan struct{}), pc: nil}
	h.bweMu.Lock()
	h.bwe = map[*Viewer]viewerEstimate{v: {since: now}}
	h.bweMu.Unlock()

	if _, viewers := h.worstEstimate(now); viewers != 1 {
		t.Fatalf("an entry in the map counts as %d viewers, wanted 1", viewers)
	}

	h.bweMu.Lock()
	delete(h.bwe, v)
	h.bweMu.Unlock()
	if _, viewers := h.worstEstimate(now); viewers != 0 {
		t.Error("removing the entry does not take the viewer away: with no viewers " +
			"the three governors are never released")
	}
}
