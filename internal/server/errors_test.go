package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// **`type errCode string` protects against nothing, and this test exists for
// that.**
//
// An untyped constant converts itself: `authError(…, "could not save the
// configuration")` compiles without a word, and the defect appears on somebody
// else's screen as "Operation failed" instead of the right sentence. It happened
// three times in the very commit that introduced the codes — the hand
// substitution caught the call sites on one line and missed those on two.
//
// The compiler cannot be the net because the type is a string; the net is
// looking at the source. The question is sharp: **no literal in the argument
// that carries the code.** An identifier is fine, a call too — it is
// `s.codeFor(err)` — a piece of prose is not.
//
// There were two roads that would close the class, and this is the one that can
// be written: making `errCode` a type that literals do not convert into is not
// possible in Go without giving up the constants.
func TestNoProseTravelsAsACode(t *testing.T) {
	// Where the code sits, in each function that carries it.
	position := map[string]int{
		"writeJSONError": 2,
		"writeJSONRetry": 2,
		"authError":      5,
		"authErrorRetry": 5,
	}

	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("sources cannot be parsed: %v", err)
	}

	seen := 0
	for _, p := range pkg {
		for fileName, file := range p.Files {
			if strings.HasSuffix(fileName, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := calleeName(call.Fun)
				where, carriesACode := position[name]
				if !carriesACode || where >= len(call.Args) {
					return true
				}
				seen++
				if lit, prose := call.Args[where].(*ast.BasicLit); prose {
					t.Errorf("%s: %s receives the literal %s instead of a code: "+
						"the type converts it silently and the page will show the "+
						"generic message",
						fset.Position(lit.Pos()), name, lit.Value)
				}
				return true
			})
		}
	}

	// **And the test has to have looked at something.** If one day those
	// functions were renamed, `position` would find nothing any more and this
	// test would pass happily without having checked a line.
	if seen == 0 {
		t.Fatal("no call found: the list in position no longer matches the code")
	}
}

func calleeName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}
