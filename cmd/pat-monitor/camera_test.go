package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"testing"

	"patmonitor/internal/devices"
)

// twoCameras is the ordinary case: two cameras with two names, which is the one
// the superseded key was written for.
func twoCameras() []devices.Device {
	return []devices.Device{
		devices.New("Integrated Camera", `\?\usb#aaa`),
		devices.New("Logitech StreamCam", `\?\usb#bbb`),
	}
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// The superseded key goes on opening the camera its owner chose: a
// configuration written before the change must not quietly move to another
// camera. **And it resolves to a link**, because a name is not what gets
// reopened.
func TestTheSupersededNameStillChoosesWhenThereIsNoID(t *testing.T) {
	got := chosenCamera(twoCameras(), "", "logitech streamcam", quietLog())
	if got != `\?\usb#bbb` {
		t.Errorf("chose %q instead of the link of the camera the name asked for", got)
	}
}

// **They are not two ways of choosing.** With both written, the id commands: the
// name is the migration of an older choice, and a migration that outvoted the
// current choice would be a knob nobody could turn off.
func TestTheIDCommandsOverTheSupersededName(t *testing.T) {
	got := chosenCamera(twoCameras(), `\?\usb#aaa`, "Logitech StreamCam", quietLog())
	if got != `\?\usb#aaa` {
		t.Errorf("the name won over the id: chose %q", got)
	}
}

// **The id is not looked up here at all**, and that is the property that
// matters: whether it is connected is a question for whoever opens, at every
// open. Answered here it would be answered once, about the webcam that happened
// to be plugged in at start-up.
func TestAnIDIsNotJudgedAgainstTheListAtStartUp(t *testing.T) {
	got := chosenCamera(twoCameras(), `\?\usb#gone`, "", quietLog())
	if got != `\?\usb#gone` {
		t.Errorf("the chosen id was changed at start-up: %q", got)
	}
	// And with no list at all — enumeration refused — the choice still stands.
	if got := chosenCamera(nil, `\?\usb#gone`, "", quietLog()); got != `\?\usb#gone` {
		t.Errorf("with no list the choice was dropped: %q", got)
	}
}

// A name that matches nothing is **not** carried on as a link: it would become a
// choice that can never be satisfied, declared as a fallback at every open, for
// a key that was only ever a name. It says so and stands aside.
func TestASupersededNameThatMatchesNothingSaysSoAndStandsAside(t *testing.T) {
	var lines bytes.Buffer
	got := chosenCamera(twoCameras(), "", "A Camera Nobody Has",
		slog.New(slog.NewTextHandler(&lines, nil)))
	if got != "" {
		t.Errorf("a name that matches nothing became the choice: %q", got)
	}
	if !strings.Contains(lines.String(), "Logitech StreamCam") {
		t.Errorf("the line does not say what there was to choose from: %s", lines.String())
	}
}

// **The chosen camera has two readers, and the migration reached one.**
//
// `chosenCamera` resolves the superseded `camera_name` into a link, and that
// link goes to the capture. It has to go to the configuration too, because the
// server composes `CameraChosen` from there: handed to the capture alone, the
// box in the details says "first available webcam" while the capture is pinned,
// and on unplugging that camera the alert says "the camera you chose is not
// connected" beside a box showing no choice.
//
// It is read from the syntax tree because the defect is an **absence** in a
// function that opens devices and cannot be called from a test: no value is
// wrong anywhere, one of two readers is simply never told.
func TestTheMigratedCameraLinkReachesTheConfiguration(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	// What `chosenCamera`'s answer was called, and what got assigned to the
	// configuration's field: the guard is that they are the same name.
	var answer, assigned string
	ast.Inspect(f, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range st.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(st.Lhs) {
					continue
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "chosenCamera" {
					if lhs, ok := st.Lhs[i].(*ast.Ident); ok {
						answer = lhs.Name
					}
				}
			}
			for i, lhs := range st.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "CameraDeviceID" || i >= len(st.Rhs) {
					continue
				}
				if id, ok := st.Rhs[i].(*ast.Ident); ok {
					assigned = id.Name
				}
			}
		}
		return true
	})

	if answer == "" {
		t.Fatal("this test is looking in the wrong place: nobody calls chosenCamera")
	}
	if assigned != answer {
		t.Errorf("chosenCamera's answer is %q and the configuration was given %q: "+
			"the capture and the server would disagree about which camera was chosen",
			answer, assigned)
	}
}
