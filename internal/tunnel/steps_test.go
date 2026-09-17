package tunnel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// AllSteps is a hand-written list, and this keeps it from being one in the way
// that matters.
//
// What reads it is the catalogue's check, which requires a word for every step
// in **every** language. A step that never reaches AllSteps is therefore a step
// nobody has to translate, and the failure it names comes out on the page as a
// bare key — with the whole suite green, because each link in the chain is
// doing what it was asked. It is the first failure mode of "Guards, and how
// they fail": a hand-written list protects exactly what somebody remembered.
//
// **And it was not a hypothesis.** StepPanic was added to this file, neither
// catalogue was touched, and `go test ./...` passed.
//
// The list cannot be derived at run time — Go has no reflection over constants
// — so it is derived here instead, from the source, which is the same answer
// the pages' guards give when they read the markup rather than a list of their
// own.
func TestEveryStepReachesTheListTheCatalogueReads(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	declared := map[string]token.Pos{}
	var listed map[string]bool

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				// `StepX FailedStep = "x"`, and inside a const block the type
				// is written once: only the spec that carries it is looked at,
				// which is how these are written here.
				if id, ok := n.Type.(*ast.Ident); ok && id.Name == "FailedStep" {
					for _, nm := range n.Names {
						declared[nm.Name] = nm.Pos()
					}
				}
			case *ast.FuncDecl:
				if n.Name.Name != "AllSteps" {
					return true
				}
				listed = map[string]bool{}
				ast.Inspect(n.Body, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						listed[id.Name] = true
					}
					return true
				})
			}
			return true
		})
	}

	if listed == nil {
		t.Fatal("AllSteps was not found: this guard was reading nothing at all, " +
			"which looks exactly like a guard that is satisfied")
	}
	// A floor, because a guard that matches nothing passes, and that is the
	// shape a rename turns this into silently.
	if len(declared) < 7 {
		t.Fatalf("only %d step constants were read (%v): the source no longer has "+
			"the shape this guard knows, so it is covering nothing", len(declared), declared)
	}

	for name := range declared {
		// StepNone is the absence of a step and names no failure: it is
		// deliberately outside the list and outside the catalogue.
		if name == "StepNone" {
			continue
		}
		if !listed[name] {
			t.Errorf("%s is a step and AllSteps does not carry it: nothing will "+
				"ask for a word for it, and the page will show the bare key\n\t%s",
				name, fset.Position(declared[name]))
		}
	}
}
