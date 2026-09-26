package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The DLL search is narrowed before anything else in main**, in the monitor
// and in the two tools that answer for it. A statement put in front of the call
// is a statement that may load a DLL through the old search order, which starts
// from the executable's folder; and a tool that opened an encoder with the old
// order would be diagnosing a different program.
//
// **The defect was put back and this test fails with it**: with the call moved
// below flag.Parse, it names the statement in front.
func TestTheDLLSearchIsNarrowedFirst(t *testing.T) {
	for _, path := range []string{"main.go", "../pat-diag/main.go", "../pat-capture/main.go", "../pat-wasapi/main.go"} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "main" || fn.Recv != nil {
				continue
			}
			found = true
			first := fn.Body.List[0]
			if !narrows(first) {
				t.Errorf("%s: main starts with something other than narrowing the DLL search", fset.Position(first.Pos()))
			}
		}
		if !found {
			t.Errorf("%s: no func main", path)
		}
	}
}

// narrows says whether a statement calls narrowDLLSearch or
// wincom.NarrowDLLSearch.
func narrows(s ast.Stmt) bool {
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			found = found || fun.Name == "narrowDLLSearch"
		case *ast.SelectorExpr:
			found = found || selector(fun) == "wincom.NarrowDLLSearch"
		}
		return true
	})
	return found
}
