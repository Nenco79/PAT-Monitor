package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The DLL search is narrowed before anything else in main.** A statement put
// in front of it is a statement that may load a DLL through the old search
// order, which starts from the executable's folder; flag.Parse is harmless
// today, and the next line somebody adds there need not be.
//
// **The defect was put back and this test fails with it**: with the call moved
// below flag.Parse, it names the statement in front.
func TestTheDLLSearchIsNarrowedFirst(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			continue
		}
		first := fn.Body.List[0]
		found := false
		ast.Inspect(first, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "narrowDLLSearch" {
					found = true
				}
			}
			return true
		})
		if !found {
			t.Errorf("%s: main starts with something other than narrowDLLSearch", fset.Position(first.Pos()))
		}
		return
	}
	t.Fatal("no func main in main.go")
}
