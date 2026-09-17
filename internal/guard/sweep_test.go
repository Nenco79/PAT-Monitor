package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// **A recover catches its own goroutine and nobody else's**, and that is the
// property that decides whether this package protects anything at all.
//
// Guarding a supervisor covers the call chain under it and **not** the
// goroutines that chain starts: those are separate stacks, and a panic in one
// of them ends the process exactly as before. So a monitor with the boundaries
// guarded and fifteen bare `go` statements left over is protected in the cases
// one thought of and not in the others — which is worse than no protection,
// because it reads as complete.
//
// The rule is therefore absolute and mechanical: **in the monitor, a goroutine
// is started with guard.Go.** It is checked rather than remembered, because the
// sixteenth is the one somebody adds without reading this.
//
// **And a `go` statement is not the only thing that starts a goroutine.** A
// review found `time.AfterFunc` sitting outside everything this test could
// see, and the finding was not the timer — whose body is two lines that cannot
// panic — it was that this test says "absolute and mechanical" while reading
// one construct out of three. A guard whose green covers more than it read is
// the thing this file exists to prevent, so it reads all three: the `go`
// statement, the timer (`guard.After`), and the errgroup in main, whose members
// are the monitor's largest parts and whose panic nothing catches either.
//
// The instruments in cmd/pat-* are outside it deliberately: they are run by
// hand in front of a console, where a panic is the answer one wants, printed
// where one is looking.
func TestEveryGoroutineOfTheMonitorIsGuarded(t *testing.T) {
	roots := []string{"..", "../../cmd/pat-monitor"}

	var bare []string
	guarded := 0

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// This package's own `go` is the guarded one: it is what every
			// other site delegates to, and it cannot delegate to itself.
			if filepath.Base(path) == "guard.go" && filepath.Base(filepath.Dir(path)) == "guard" {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					// **The wrapped ones are no longer `go` statements**, they
					// are calls: counting GoStmt alone, this floor read zero on
					// a tree that had just been swept clean, which is the shape
					// of a guard that has stopped looking at anything.
					if callsGuard(n, "Go") || callsGuard(n, "After") {
						guarded++
					}
					if isBareTimer(n) {
						bare = append(bare, fset.Position(n.Pos()).String()+
							" (time.AfterFunc: use guard.After)")
					}
				case *ast.GoStmt:
					// A `go` statement is the offence, unless somebody wrote
					// `go guard.Go(...)`, which is guarded twice over and
					// harmless.
					if !callsGuard(n.Call, "Go") {
						bare = append(bare, fset.Position(n.Pos()).String()+
							" (a `go` statement: use guard.Go)")
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, where := range bare {
		t.Errorf("a goroutine started outside guard at %s: a panic there ends "+
			"the process, and no recover anywhere else can catch it", where)
	}
	// A floor, because a guard that matches nothing passes: if this ever reads
	// no goroutines at all — a move, a rename, a walk that stops finding the
	// tree — it has to fail rather than go green over an empty sweep.
	if guarded < 12 {
		t.Fatalf("only %d guarded goroutines were found: this test is no longer "+
			"reading the monitor's source, so its silence means nothing", guarded)
	}

}

// callsGuard recognises the wrapper and nothing that merely resembles it: a
// local function called Go would otherwise let a whole file through.
func callsGuard(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "guard"
}

// isBareTimer catches the second way of starting a goroutine. context.AfterFunc
// is asked for by the same name and is the same hazard.
func isBareTimer(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "AfterFunc" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || (pkg.Name != "time" && pkg.Name != "context") {
		return false
	}
	return true
}

// The errgroup is the third way, and its members are the monitor's largest
// parts: a panic in one of them ends the process exactly as a bare `go` would,
// and `g.Wait` never sees it.
//
// The two shapes accepted are the two decisions this program makes about a
// subsystem, and they are meant to be read off the list: `aside(...)` says the
// monitor can outlive this one, and a body that catches for itself says it
// cannot. **It found two that carried neither** — the tunnel, whose protection
// was real but lived a package away and so said nothing at the call site, and
// the orderly shutdown, which had none at all.
func TestEveryMemberOfTheGroupDecidesWhatAPanicCosts(t *testing.T) {
	const path = "../../cmd/pat-monitor/main.go"

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Go" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "g" {
			return true
		}
		seen++
		if len(call.Args) != 1 {
			return true
		}
		if inner, ok := call.Args[0].(*ast.CallExpr); ok {
			if id, ok := inner.Fun.(*ast.Ident); ok && id.Name == "aside" {
				return true
			}
		}
		guarded := false
		ast.Inspect(call.Args[0], func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && callsGuard(c, "Run") {
				guarded = true
			}
			return true
		})
		if !guarded {
			t.Errorf("the group member at %s neither says `aside` nor catches for "+
				"itself: a panic in it ends the whole monitor, and which of the "+
				"two it wanted is not readable from the list",
				fset.Position(call.Pos()))
		}
		return true
	})

	if seen < 8 {
		t.Fatalf("only %d group members were read: this guard is no longer "+
			"looking at the list it was written for", seen)
	}
}
