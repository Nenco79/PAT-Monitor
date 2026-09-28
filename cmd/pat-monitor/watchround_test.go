package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The watching round runs on its own ticker, not inside the status.**
//
// The alerts, the event clips, the recogniser's verdict and the clip heartbeat
// used to be computed by `statusFn`, whose only callers are the page's
// heartbeat and the tray's timer: with the page closed and the tray not
// started nothing ran, and the night left an empty log and an empty folder
// with nothing saying why. Called from both, it also applied snapshots out of
// order.
//
// No test on a value can see where a call sits, so this reads the syntax tree:
// `statusFn` may call none of the round's verbs, and `watch` must be called
// from a goroutine started with guard.Go. **The defect was put back and this
// test fails with it**: with the round inlined into statusFn again, it names
// each verb.
func TestTheWatchingRoundIsNotDrivenByWhoeverAsksForTheStatus(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}

	verbs := map[string]bool{
		"registry.Update": true, "rec.Trigger": true, "rec.Tick": true,
		"recog.Wanted": true, "recog.Verdict": true, "watch": true,
	}
	var status *ast.FuncLit
	fromGoroutine := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == 1 && len(n.Rhs) == 1 {
				if id, ok := n.Lhs[0].(*ast.Ident); ok && id.Name == "statusFn" {
					if lit, ok := n.Rhs[0].(*ast.FuncLit); ok {
						status = lit
					}
				}
			}
		case *ast.CallExpr:
			if typeName(n.Fun) != "guard.Go" {
				return true
			}
			for _, arg := range n.Args {
				ast.Inspect(arg, func(m ast.Node) bool {
					if c, ok := m.(*ast.CallExpr); ok && typeName(c.Fun) == "watch" {
						fromGoroutine = true
					}
					return true
				})
			}
		}
		return true
	})

	if status == nil {
		t.Fatal("no statusFn found in main.go: this test is looking in the wrong place")
	}
	ast.Inspect(status, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && verbs[typeName(c.Fun)] {
			t.Errorf("%s: statusFn calls %s, so the round runs only when somebody asks",
				fset.Position(c.Pos()), typeName(c.Fun))
		}
		return true
	})
	if !fromGoroutine {
		t.Error("watch is not called from a goroutine of its own: nothing drives the round")
	}
}
