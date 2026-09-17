package audio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCOMErrorIsWrappedUndescribed is the guard the behavioural tests beside
// it cannot be.
//
// `describeAudclnt` was right from the start and had no test; what was missing
// was **calling it**, and it was missing in exactly the places whose errors are
// read at night — the capture loop, where a microphone unplugged while the
// monitor watches arrives as `AUDCLNT_E_DEVICE_INVALIDATED`. Thirty-one of the
// forty-five wraps in this package skipped it, so the readable form was on the
// errors met while setting up, in front of a console, and absent from the ones
// met after. **A test on the function's output absolves every site that never
// calls it**, which is why this one reads the source instead.
//
// **That pair is a count of the state before the sweep, and it is the number
// `CLAUDE.md` carries.** The two documents used to disagree — twenty-nine of
// forty-three here against thirty-one of forty-five there — while agreeing on
// the fourteen that did go through, which is what said one of them was simply
// stale rather than counting something else. Re-counted: forty-five is the
// figure the tree supports. It is left as a historical measurement and does not
// track the file, because a measurement belongs to the configuration it was
// taken in; what tracks the file is the floor below.
//
// The rule it enforces needs no list of codes: an error that comes straight out
// of a method call on a COM object goes through `describeAudclnt`. The one
// exception is a category and not an entry — the `windows` syscall package,
// whose errors carry their own text, and for which a function named after
// AUDCLNT codes would say something false.
func TestNoCOMErrorIsWrappedUndescribed(t *testing.T) {
	files, err := filepath.Glob("*_windows.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources to read: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			stmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}
			recv, called := comCallInInit(stmt.Init)
			if !called {
				return true
			}
			checked++
			for _, wrap := range wrapsIn(stmt.Body) {
				if !describes(wrap) {
					t.Errorf("%s: the error of %s is wrapped without "+
						"describeAudclnt, so its HRESULT reaches the log in "+
						"decimal", fset.Position(wrap.Pos()), recv)
				}
			}
			// **And the second shape, which is how the defect actually left.**
			// The loop above judges the wraps it finds, so a branch that wraps
			// nothing is judged not at all: `return info, err` hands the raw
			// *ole.OleError to the caller and this guard never looked at it. That
			// is exactly how the endpoint volume's failure reached the one line
			// that explains a 30 dB loss as a bare decimal — the thing
			// describeAudclnt exists to remove, leaving by the door the guard
			// written for it does not watch.
			//
			// The rule is the same either way: an error that leaves this branch
			// has been described somewhere inside it. Asked of the branch rather
			// than of each wrap, it covers both shapes, and it says nothing about
			// branches that return some other error or do not return at all.
			if name, bare := returnsBare(stmt.Init, stmt.Cond, stmt.Body); bare && !mentionsDescribe(stmt.Body) {
				t.Errorf("%s: the error of %s leaves as %q without going through "+
					"describeAudclnt, so its HRESULT reaches the caller in decimal",
					fset.Position(stmt.Body.Pos()), recv, name)
			}
			return true
		})
	}
	// Without this the guard could pass by matching nothing at all, which is
	// the shape a refactor turns it into silently.
	if checked < 20 {
		t.Fatalf("only %d COM call sites were examined: the guard has stopped "+
			"recognising them", checked)
	}
}

// comCallInInit reports whether the statement is `if err := x.M(...); ...` on
// something that is not the syscall package, and names the receiver.
func comCallInInit(init ast.Stmt) (string, bool) {
	assign, ok := init.(*ast.AssignStmt)
	if !ok || len(assign.Rhs) != 1 {
		return "", false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		// A method on something like `p.client` — still a COM object.
		var b strings.Builder
		if !renderRecv(&b, sel.X) {
			return "", false
		}
		return b.String() + "." + sel.Sel.Name, true
	}
	// The syscall package's errors are already readable, and running one
	// through a function named for AUDCLNT codes would misdescribe it.
	if recv.Name == "windows" || recv.Name == "ole" {
		return "", false
	}
	return recv.Name + "." + sel.Sel.Name, true
}

func renderRecv(b *strings.Builder, e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		b.WriteString(v.Name)
		return true
	case *ast.SelectorExpr:
		if !renderRecv(b, v.X) {
			return false
		}
		b.WriteString("." + v.Sel.Name)
		return true
	}
	return false
}

// wrapsIn collects the arguments a %w wrap consumes inside a block.
func wrapsIn(body *ast.BlockStmt) []ast.Expr {
	var out []ast.Expr
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Errorf" {
			return true
		}
		if len(call.Args) < 2 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || !strings.Contains(lit.Value, "%w") {
			return true
		}
		out = append(out, call.Args[len(call.Args)-1])
		return true
	})
	return out
}

// describes reports whether the wrapped value went through describeAudclnt.
func describes(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "describeAudclnt" {
			found = true
		}
		return !found
	})
	return found
}

// returnsBare reports whether the branch hands the error out untouched, and
// under what name. It is the shape `if err := x.M(); err != nil { return v, err }`
// — no wrap for wrapsIn to find, and so nothing for the loop above to judge.
func returnsBare(init ast.Stmt, cond ast.Expr, body *ast.BlockStmt) (string, bool) {
	assign, ok := init.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) == 0 {
		return "", false
	}
	// The error is the last thing assigned: `err := x.M()` or `v, err := x.M()`.
	name, ok := assign.Lhs[len(assign.Lhs)-1].(*ast.Ident)
	if !ok {
		return "", false
	}
	// **And the condition has to be the one the rule is about.** Without this the
	// walk reads `if s := pv.String(); s != ""` as an error branch and reports
	// the device name as an undescribed HRESULT — measured, on the first run. The
	// shape this guard names is `err != nil`, so that is what it asks for.
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return "", false
	}
	lhs, ok := bin.X.(*ast.Ident)
	if !ok || lhs.Name != name.Name {
		return "", false
	}
	if rhs, ok := bin.Y.(*ast.Ident); !ok || rhs.Name != "nil" {
		return "", false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, r := range ret.Results {
			if id, ok := r.(*ast.Ident); ok && id.Name == name.Name {
				found = true
			}
		}
		return true
	})
	return name.Name, found
}

// mentionsDescribe reports whether the branch put the error through
// describeAudclnt at all — by wrapping it, by reassigning it, or on its way into
// a field that is read later.
func mentionsDescribe(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "describeAudclnt" {
			found = true
		}
		return !found
	})
	return found
}
