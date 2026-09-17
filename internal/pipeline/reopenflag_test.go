package pipeline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The "we closed this on purpose" flag is given back before anything that can
// fail.**
//
// `runOnce` reads `camReopen` after `runVideo` returns and, finding it, reports
// `errCamSwitch` — which the supervisor treats as a closure it asked for: no
// fault logged, and **no wait at all** before going round again. That is right
// for a camera somebody chose and wrong for a session that never started.
//
// It used to be cleared thirty lines into `runVideo`, after `wincom.Init` and
// `mf.Startup`, both of which return an error. Left set through one of those,
// every turn of the supervisor reports a switch that nobody asked for and comes
// straight back — a loop at full speed, writing `camera changed on request,
// reopening` as fast as the machine allows, where the right behaviour is one
// line and a backoff doubling to thirty seconds.
//
// **The microphone's twin already had it right**, and that is what makes this
// legible as an oversight rather than a decision: `runAudio` clears `micReopen`
// on its sixth line, with nothing above it that can return.
//
// The guard is positional and needs no failure to reproduce, which is the point:
// making `mf.Startup` fail on demand is not something this package can do, and
// that is exactly why the defect survived.
func TestTheReopenFlagIsGivenBackBeforeAnythingCanFail(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pipeline.go", nil, 0)
	if err != nil {
		t.Fatalf("pipeline.go cannot be parsed: %v", err)
	}

	for _, c := range []struct{ fn, field string }{
		{"runVideo", "camReopen"},
		{"runAudio", "micReopen"},
	} {
		var body *ast.BlockStmt
		ast.Inspect(file, func(n ast.Node) bool {
			if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == c.fn {
				body = d.Body
				return false
			}
			return true
		})
		if body == nil {
			t.Fatalf("%s was not found: this guard is watching nothing", c.fn)
		}

		cleared := token.NoPos
		ast.Inspect(body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Store" {
				return true
			}
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == c.field {
				if cleared == token.NoPos || call.Pos() < cleared {
					cleared = call.Pos()
				}
			}
			return true
		})
		if cleared == token.NoPos {
			t.Errorf("%s never clears %s: every session would inherit the last one's "+
				"decision", c.fn, c.field)
			continue
		}

		var early int
		ast.Inspect(body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok || ret.Pos() >= cleared {
				return true
			}
			early++
			t.Errorf("%s can return at %s before clearing %s: the supervisor then reads a "+
				"closure it never asked for, and its branch for those does not wait",
				c.fn, fset.Position(ret.Pos()), c.field)
			return true
		})
		// The control: there is code above the clearing, so a green result means
		// that code cannot return rather than that there is none.
		if early == 0 && cleared == body.Pos() {
			t.Errorf("%s clears %s as its very first token: the guard is asserting "+
				"something the shape of the function makes trivially true", c.fn, c.field)
		}
	}
}
