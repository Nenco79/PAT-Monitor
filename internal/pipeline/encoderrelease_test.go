package pipeline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The encoder's release asks whether the process is ending, not whether the
// session is.** runVideo's context is per session, so that a camera choice and
// the chosen camera's recheck can close it; the defer that decides whether to
// leave the encoder to the operating system read that context, and every swap
// left a hardware encoder session open for the life of the process.
//
// Reaching the branch wants a real encoder and a swap, and what it leaks is
// visible only to the driver, so this reads the syntax tree: inside runVideo,
// the defer that calls `last.Close()` must decide on a context that is not the
// shadowed `ctx`. **The defect was put back and this test fails with it.**
func TestTheEncoderIsReleasedUnlessTheProcessIsEnding(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pipeline.go", nil, 0)
	if err != nil {
		t.Fatalf("pipeline.go cannot be parsed: %v", err)
	}
	var run *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "runVideo" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("no runVideo in pipeline.go: this test is looking in the wrong place")
	}

	found := false
	ast.Inspect(run.Body, func(n ast.Node) bool {
		def, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		lit, ok := def.Call.Fun.(*ast.FuncLit)
		if !ok || !callsClose(lit, "last") {
			return true
		}
		found = true
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Err" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "ctx" {
				t.Errorf("%s: the release is decided on the session's ctx, which a camera "+
					"choice cancels too", fset.Position(call.Pos()))
			}
			return true
		})
		return true
	})
	if !found {
		t.Fatal("no defer in runVideo calls last.Close(): this test is looking in the wrong place")
	}
}

// callsClose says whether lit calls name.Close().
func callsClose(lit *ast.FuncLit, name string) bool {
	calls := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
					calls = true
				}
			}
		}
		return true
	})
	return calls
}
