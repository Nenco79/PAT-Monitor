package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// **Every log handler the monitor builds hides the tailnet's name.** The log
// is the file people attach to an issue, and the tunnel is not the only one
// that writes the public name: the server logs the Host of a refused request,
// which from the Funnel is exactly that. So the root handler goes through
// tunnel.HideTailnet, and this reads the source for every slog handler built
// here, so that a second one added tomorrow is covered with nothing to
// remember.
//
// **The defect was put back and this test fails with it**: with the root
// handler built without tunnel.HideTailnet, it names the line.
func TestEveryLogHandlerHidesTheTailnet(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		wrapped := map[ast.Expr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector(call.Fun) == "tunnel.HideTailnet" && len(call.Args) == 1 {
				wrapped[call.Args[0]] = true
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch selector(call.Fun) {
			case "slog.NewTextHandler", "slog.NewJSONHandler":
				found++
				if !wrapped[call] {
					t.Errorf("%s: a log handler that does not hide the tailnet's name", fset.Position(call.Pos()))
				}
			}
			return true
		})
	}
	// A guard that has stopped finding the handler passes; this one says so.
	if found == 0 {
		t.Fatal("no slog handler found in the monitor: the guard is reading nothing")
	}
}

func selector(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return x.Name + "." + sel.Sel.Name
}
