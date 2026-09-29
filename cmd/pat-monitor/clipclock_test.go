package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **The recorder is handed the instant each piece was captured, not the
// instant the sink ran.** Both used to be `time.Now()`, and the sink runs when
// the encoder lets a frame go — measured, 41-121 ms after the camera saw it,
// against some 10 ms for a packet of audio. The clip puts both tracks on that
// one clock, so the sound led the picture by the difference, and nobody sees
// that in the file: it is heard watching the clip.
//
// The defect was which number was passed, not a wrong value, so this reads the
// syntax tree: the second argument of WriteVideo and WriteAudio must not be a
// call — `time.Now()` is one, `au.At` and `at` are not. Put back, both calls
// fail here.
func TestTheRecorderIsGivenTheCaptureInstant(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}

	seen := map[string]int{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		name := typeName(call.Fun)
		if name != "rec.WriteVideo" && name != "rec.WriteAudio" {
			return true
		}
		seen[name]++
		if when, ok := call.Args[1].(*ast.CallExpr); ok {
			t.Errorf("%s: %s is dated with %s(): that is when the sink ran, not when the "+
				"piece was captured, and the two tracks are late by different amounts",
				fset.Position(call.Pos()), name, typeName(when.Fun))
		}
		return true
	})

	for _, name := range []string{"rec.WriteVideo", "rec.WriteAudio"} {
		if seen[name] == 0 {
			t.Errorf("no call to %s in main.go: the test looked at nothing", name)
		}
	}
}
