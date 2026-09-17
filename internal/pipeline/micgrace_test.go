package pipeline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// **A reopen we decided on is not a missing microphone.**
//
// The path recheck arms itself on every open that does not obtain raw mode —
// exclusive included, which is where a machine whose APO filters the microphone
// ends up — that is, every two minutes all night long. For the length of the
// reopen `AudioActive` would answer no, and that no feeds `MicrophoneActive` and
// so the `mic-missing` alert: a status round inside that window produces a
// banner, a chime and two transitions in the log, hundreds of times a night. An
// alarm that always sounds is one nobody reads any more.
func TestAPlannedReopenIsNotAMissingMicrophone(t *testing.T) {
	p := New(Config{Log: slog.New(slog.NewTextHandler(nil, nil))})

	// Microphone closed and no grace: absent, and rightly so.
	if p.AudioActive() {
		t.Fatal("with no microphone and no grace it declares itself active")
	}

	// The supervisor opens the window of the planned reopen.
	p.micGraceUntil.Store(time.Now().Add(micRecheckGrace).UnixNano())
	if !p.AudioActive() {
		t.Error("during a planned reopen the microphone declares itself absent")
	}
	if !p.inMicGrace() {
		t.Error("inMicGrace does not recognise its own window")
	}

	// **And the grace expires by itself.** If the device does not come back the
	// absence is declared as always: a grace somebody has to switch off stays on
	// for ever the day an open wedges inside the driver.
	p.micGraceUntil.Store(time.Now().Add(-time.Millisecond).UnixNano())
	if p.AudioActive() {
		t.Error("an expired grace goes on covering a closed microphone")
	}
	if p.inMicGrace() {
		t.Error("inMicGrace does not see its own expiry")
	}
}

// **The audio flag is cleared by the supervisor, and by nobody else.**
//
// With a second writer, whichever runs first wins and the supervisor's
// `CompareAndSwap(true, false)` always finds `false`: the line "audio
// interrupted, retrying" never comes out and `lastErr` never clears — so after a
// recovery, a second interruption carrying the same message goes to `Debug`,
// which in the binary that ships means nowhere.
//
// This is not a test on a value, and it could not be: `runAudio` wants WASAPI.
// It is a test on the **shape**, which is where the defect lives — two writers
// and the wrong order — and that is why it reads the source.
func TestOnlyTheSupervisorClearsTheAudioFlag(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pipeline.go", nil, 0)
	if err != nil {
		t.Fatalf("pipeline.go cannot be parsed: %v", err)
	}

	// `Run` clears it on the way out, when the capture ends for good;
	// `superviseAudio` on every turn, and it is the only one that needs to know
	// whether the audio was there.
	allowed := map[string]bool{"Run": true, "superviseAudio": true}

	found := 0
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasSuffix(exprName(sel.X), "audioOK") {
				return true
			}
			// Only the writes that **clear**: `Store(true)` and
			// `CompareAndSwap(false, true)` set, and setting is rightly the job
			// of whoever opens the device.
			clears := false
			switch sel.Sel.Name {
			case "Store", "Swap":
				clears = len(call.Args) == 1 && literal(call.Args[0]) == "false"
			case "CompareAndSwap":
				clears = len(call.Args) == 2 && literal(call.Args[1]) == "false"
			}
			if !clears {
				return true
			}
			found++
			if !allowed[fn.Name.Name] {
				t.Errorf("%s: %s clears audioOK. The supervisor does that, otherwise by "+
					"the time it is its turn the flag is already false and the "+
					"interruption goes unannounced",
					fset.Position(call.Pos()), fn.Name.Name)
			}
			return true
		})
	}

	if found == 0 {
		t.Fatal("no write of audioOK found: the test looked at nothing")
	}
}

func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprName(v.X) + "." + v.Sel.Name
	}
	return ""
}

func literal(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
