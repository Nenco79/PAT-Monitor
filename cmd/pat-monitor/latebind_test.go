package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The pipeline is reached through a closure until it exists.**
//
// `p` is declared before the hub and assigned after it, so every callback the
// hub is handed has to read the variable when it is called, not when it is
// written. A method value — `p.SentFormat` where `func() { return
// p.SentFormat() }` was — binds the receiver on the spot, and on the spot it is
// nil: the quality loop's first turn panicked in `SentFormat`, and the guard
// that caught the panic stopped the loop for the life of the process. It
// compiles, it reads like a tidy-up, and no test that builds the hub by hand
// can see it, because there the pipeline is never nil.
//
// So this reads `run`: before the assignment of `p`, `p.X` may appear only as
// the function of a call, which inside a closure is what runs later. **The
// defect was put back and this test fails with it.**
func TestThePipelineIsNotBoundBeforeItExists(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}
	var run *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("no run in main.go: this test is looking in the wrong place")
	}

	// Where p stops being nil.
	born := token.NoPos
	ast.Inspect(run.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.ASSIGN || len(as.Lhs) != 1 || born != token.NoPos {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == "p" {
			born = as.Pos()
		}
		return true
	})
	if born == token.NoPos {
		t.Fatal("run never assigns p: this test is looking in the wrong place")
	}

	// Every selector on p that is the function of a call is fine; any other is
	// the receiver bound where it is written.
	called := map[*ast.SelectorExpr]bool{}
	seen := 0
	ast.Inspect(run.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				called[sel] = true
			}
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Pos() >= born {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "p" {
			return true
		}
		seen++
		if !called[sel] {
			t.Errorf("%s: p.%s is taken as a value before p is assigned, so it binds a "+
				"nil pipeline; call it from a closure instead",
				fset.Position(sel.Pos()), sel.Sel.Name)
		}
		return true
	})
	// The control: the hub's callbacks do reach p before it is assigned, so a
	// green result means they do it through calls rather than that there are
	// none.
	if seen == 0 {
		t.Fatal("no use of p before its assignment: this guard is watching nothing")
	}
}
