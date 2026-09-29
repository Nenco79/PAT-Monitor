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
// So this reads `run`: before the assignment of `p`, the variable may be read
// only inside a function literal, which is what runs later, or have its
// address taken. Anything else reads it on the spot — the method value, but
// also `defer p.Close()`, whose receiver is evaluated at the defer, a direct
// call, and a copy that a closure then uses. **The defect was put back and this
// test fails with it**, and so do the other three shapes.
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

	// The pipeline variable, by its object, so that another `p` in run — a
	// path, a loop variable — is not taken for it.
	var pipe *ast.Object
	ast.Inspect(run.Body, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok && pipe == nil {
			for _, id := range vs.Names {
				if id.Name == "p" {
					pipe = id.Obj
				}
			}
		}
		return pipe == nil
	})
	if pipe == nil {
		t.Fatal("run declares no var p: this test is looking in the wrong place")
	}

	// Where p stops being nil, the function literals that run later, and the
	// places its address is taken.
	born := token.NoPos
	var closures []*ast.FuncLit
	addressed := map[*ast.Ident]bool{}
	ast.Inspect(run.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.ASSIGN && len(n.Lhs) == 1 && born == token.NoPos {
				if id, ok := n.Lhs[0].(*ast.Ident); ok && id.Obj == pipe {
					born = n.Pos()
				}
			}
		case *ast.FuncLit:
			closures = append(closures, n)
		case *ast.UnaryExpr:
			if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND {
				addressed[id] = true
			}
		}
		return true
	})
	if born == token.NoPos {
		t.Fatal("run never assigns p: this test is looking in the wrong place")
	}
	later := func(pos token.Pos) bool {
		for _, fl := range closures {
			if fl.Pos() <= pos && pos < fl.End() {
				return true
			}
		}
		return false
	}

	seen := 0
	ast.Inspect(run.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Obj != pipe || id.Pos() >= born || id.Pos() == pipe.Pos() {
			return true
		}
		if later(id.Pos()) {
			seen++
			return true
		}
		if !addressed[id] {
			t.Errorf("%s: p is read before it is assigned, outside a closure, so what it "+
				"reads is a nil pipeline; reach it from a closure instead",
				fset.Position(id.Pos()))
		}
		return true
	})
	// The control: the hub's callbacks do reach p before it is assigned, so a
	// green result means they do it through closures rather than that there
	// are none.
	if seen == 0 {
		t.Fatal("no use of p before its assignment: this guard is watching nothing")
	}
}
