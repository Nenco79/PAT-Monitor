package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// **The configuration is one value, and no closure may keep a second.**
//
// `run` loads it and hands it to closures that four different goroutines run:
// the tunnel records the name the tailnet granted, the HTTP routes change
// settings, the status round reads the three detection switches, the talk-back
// reads the output device, and the tray's thread reads the lot once a second.
// It used to be a plain local, so that was a data race; and `internal/server`
// held a **second** copy taken by value, from which every route rebuilt the
// whole struct and wrote it back, so a field written by anybody else — the node
// name, concretely — was undone by the next save from any page.
//
// Those were one defect seen twice, and `config.Store` is the one owner that
// closes both. What keeps it closed is that nothing goes round it: the moment a
// closure captures the loaded value again there are two copies, and the second
// is stale the first time somebody saves.
//
// **No test on a value catches that**, and the race detector cannot run here —
// it wants cgo, and there is no C compiler on this machine. So the guard reads
// the syntax tree and asks about shape: no function literal in this package
// mentions `cfg`, which is the name of the value handed to `NewStore` and spent
// there. Readers take `configNow()`, writers go through the store.
func TestNoClosureKeepsASecondConfiguration(t *testing.T) {
	// Every file of the package, not just main.go: `run` lives there today, but
	// a closure moved into any of the other three would be watched by nobody
	// while this went on passing green.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	// mentions looks for the **variable**, and a field spelled the same way is
	// not it. `p.cfg.Log` reaches a field of somebody else's struct, and a naive
	// walk counts the `cfg` in the middle of it: only the left-hand side of a
	// selector is an identifier in its own right.
	mentions := func(n ast.Node, name string) bool {
		found := false
		var walk func(ast.Node) bool
		walk = func(n ast.Node) bool {
			if found || n == nil {
				return false
			}
			if sel, ok := n.(*ast.SelectorExpr); ok {
				ast.Inspect(sel.X, walk)
				return false
			}
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return true
		}
		ast.Inspect(n, walk)
		return found
	}

	// A guard that recognises nothing passes. `run` really does hand the
	// configuration to several closures, so finding none that reach it the
	// permitted way means the shape has moved and this is absolving whatever
	// replaced it.
	reached := 0
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s cannot be parsed: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.FuncLit)
			if !ok {
				return true
			}
			if mentions(lit, "configNow") || mentions(lit, "store") {
				reached++
			}
			if mentions(lit, "cfg") {
				t.Errorf("%s: this closure captures cfg, the value the store was "+
					"built from. That is a second copy of the configuration, and it "+
					"is stale from the first save: read with configNow(), write with "+
					"store.Set or store.Update", fset.Position(lit.Pos()))
			}
			return true
		})
	}

	if reached == 0 {
		t.Fatal("no closure reaches the configuration through the store: either the " +
			"one owner has gone or this guard is looking at a shape that no longer exists")
	}
}
