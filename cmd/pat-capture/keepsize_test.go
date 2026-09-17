package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// **This instrument measures the size it was asked for, and the pipeline would
// lower it.**
//
// The monitor asks the camera what it declares and comes down to it, because a
// larger request does not fail: with the advanced video processing on the Source
// Reader puts a scaler in the middle and enlarges. Here that protection is the
// defect — the report prints the requested size, adds a line saying that what
// follows measures an upscale, and then the measurement would quietly be of the
// camera instead. **An instrument that changes what it measures without saying
// so is worse than one that measures the wrong thing loudly**, and it is the
// same rule as pat-diag opening the camera the way the monitor opens it.
//
// It reads the source because the defect is not a wrong value: it is a value
// resolved somewhere else, and on a machine whose camera has the preset's pixels
// — which is every machine here — both spellings measure the same thing.
func TestThisInstrumentKeepsTheSizeItWasAsked(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}

	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || name(lit.Type) != "pipeline.Config" {
			return true
		}
		for _, e := range lit.Elts {
			field, ok := e.(*ast.KeyValueExpr)
			if !ok || name(field.Key) != "KeepRequestedSize" {
				continue
			}
			found = true
			if name(field.Value) != "true" {
				t.Errorf("KeepRequestedSize is %q: the pipeline would lower the size "+
					"to what the camera declares, and the report says it is measuring "+
					"an upscale", name(field.Value))
			}
		}
		return true
	})

	// **Without this the test passes by finding nothing**, which is exactly the
	// shape of a guard that absolves the defect it was written for: the field is
	// false by omission, so its absence is the defect and not a neutral state.
	if !found {
		t.Fatal("pipeline.Config does not set KeepRequestedSize: omitted, it is " +
			"false, and this instrument goes back to measuring the camera while " +
			"reporting an upscale")
	}
}

func name(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return name(v.X) + "." + v.Sel.Name
	}
	return ""
}
