package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"patmonitor/internal/devices"
)

// **The instrument opens the camera the monitor opens, and it did not.**
//
// `devices.ListCameras()` hands back everything Media Foundation enumerates,
// infrared sensors included; the monitor never touches that list directly — it
// goes through `devices.Pick`, which drops them first. This tool took
// `cams[0]` off the raw list, under a comment calling that "the monitor's
// rule", which it had stopped being.
//
// On a laptop whose Windows Hello sensor enumerates first, the consequence is
// that `pat-capture` measured the infrared camera while the monitor filmed the
// room: same binary, same flags, a different device, and nothing in the report
// saying so. Every number in `baselines/` comes out of this tool, and the file
// beside this one already carries the same rule for the frame size.
//
// **It reads the source because there is nothing to run**: reproducing it wants
// a machine with an infrared camera enumerating first, and on every machine here
// both spellings pick the same device — which is precisely how it survived.
func TestTheInstrumentPicksTheCameraTheMonitorWould(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}

	var picks, raw int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name(call.Fun) {
		case "devices.Pick":
			picks++
		case "devices.Cameras":
			picks++
		}
		return true
	})
	// Indexing the enumeration is the defect itself: `cams[0]`, or `all[0]`,
	// or any other spelling of "the first one Windows named".
	ast.Inspect(file, func(n ast.Node) bool {
		idx, ok := n.(*ast.IndexExpr)
		if !ok {
			return true
		}
		if lit, ok := idx.Index.(*ast.BasicLit); ok && lit.Value == "0" {
			if id, ok := idx.X.(*ast.Ident); ok && (id.Name == "cams" || id.Name == "all") {
				raw++
				t.Errorf("the camera list is indexed directly at %s: the monitor "+
					"never does that, and the first of the enumeration can be the "+
					"infrared sensor", fset.Position(idx.Pos()))
			}
		}
		return true
	})
	if picks == 0 {
		t.Error("nothing in this tool goes through devices.Pick or devices.Cameras: " +
			"whatever it opens, it is not what the monitor opens")
	}
}

// And the filter really is the thing that separates the two, which is what
// makes the guard above worth having: with an infrared sensor first in the
// enumeration, the raw list and the monitor's rule name different cameras.
func TestTheFilterIsWhatSeparatesTheTwoRules(t *testing.T) {
	all := []devices.Device{
		devices.New("HP IR Camera", "link-ir"),
		devices.New("HP Wide Vision HD Camera", "link-rgb"),
	}
	if !all[0].IsLikelyIR() {
		t.Fatal("the fixture's first camera is not recognised as infrared: this " +
			"test is asserting nothing")
	}
	cam, fellBack, err := devices.Pick(all, "")
	if err != nil {
		t.Fatal(err)
	}
	if fellBack {
		t.Error("nothing was chosen, so nothing is missing: fallback must be false")
	}
	if cam.Link() == all[0].Link() {
		t.Error("Pick returned the infrared sensor")
	}
	if cam.Link() != "link-rgb" {
		t.Errorf("Pick returned %q, wanted the ordinary camera", cam.Link())
	}
}
