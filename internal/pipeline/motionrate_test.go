package pipeline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// **Detection gets five frames a second, and it used to get five only in
// daylight.**
//
// `internal/detect` opens by saying it receives the analysis frames "five a
// second", and its blend filter is a per-frame time constant — half a second of
// response at that rate. The pipeline delivered them with `frameNo % (fps /
// MotionFPS)`, a divisor of the cadence the camera **declares**, and this
// repository has a whole file about the gap between that number and the one the
// camera hands over: in the dark the automatic exposure takes it to 9.8-20 fps
// while the format still says 30.
//
// So the contract held at 30 and broke at 9.8, where a divisor of 6 gives **1.6
// analyses a second** — a third of what the detector is built for, in the one
// condition this program exists for, and the same condition in which the
// exposure hunts and the detector most needs its frames.
//
// The table below is the measurement rather than an assertion about it: the
// left column is what the old rule delivered, the right what the gate does.
func TestDetectionKeepsItsCadenceWhateverTheCameraDelivers(t *testing.T) {
	const declared = 30 // what the camera's format says, and what the divisor used
	for _, real := range []float64{9.8, 13.7, 18.4, 27.0, 30.0} {
		gate := newMotionGate(declared)
		start := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
		now := start
		step := time.Duration(float64(time.Second) / real)

		passed, seen := 0, 0
		for now.Sub(start) < 10*time.Second {
			now = now.Add(step)
			seen++
			if gate.due(now) {
				passed++
			}
		}
		got := float64(passed) / now.Sub(start).Seconds()

		// What the old rule would have produced, computed here rather than
		// remembered: the divisor is the one that was in the code.
		divisor := max(declared/MotionFPS, 1)
		old := real / float64(divisor)

		t.Logf("camera %.1f fps: divisor gave %.1f/s, the gate gives %.1f/s", real, old, got)
		if got < MotionFPS-0.6 || got > MotionFPS+0.6 {
			t.Errorf("camera at %.1f fps: detection gets %.1f frames a second, and it is "+
				"built for %d", real, got, MotionFPS)
		}
		// And the frames really are being thinned when there are more than
		// enough, or the gate would be an expensive way of passing everything.
		if real > MotionFPS+1 && passed >= seen {
			t.Errorf("camera at %.1f fps: %d of %d frames passed, nothing was thinned",
				real, passed, seen)
		}
	}
}

// Below MotionFPS the camera is the limit, and everything passes: a gate that
// dropped frames there would be taking away the few there are.
func TestBelowItsOwnCadenceTheGatePassesEverything(t *testing.T) {
	gate := newMotionGate(30)
	start := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
	now := start
	step := time.Second / 3 // three frames a second, a very dark room

	passed, seen := 0, 0
	for now.Sub(start) < 10*time.Second {
		now = now.Add(step)
		seen++
		if gate.due(now) {
			passed++
		}
	}
	if passed != seen {
		t.Errorf("%d of %d frames reached detection: below its own cadence the gate "+
			"has nothing to thin", passed, seen)
	}
}

// **And the loop really uses it**, which the two tests above cannot say: they
// exercise the gate, and the defect was in how the frame loop called it.
//
// The guard asks the syntax tree for the condition that admits a frame to
// detection, and refuses a remainder — `frameNo % motionEvery` is the shape that
// was there, and any counter-based revival of it has the same shape. The defect
// was put back and this fails with it.
func TestTheFrameLoopGatesDetectionByTime(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pipeline.go", nil, 0)
	if err != nil {
		t.Fatalf("pipeline.go cannot be parsed: %v", err)
	}

	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runVideo" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ifs, ok := n.(*ast.IfStmt)
			if !ok || ifs.Cond == nil {
				return true
			}
			// The branch that hands a frame to detection.
			var callsMotion bool
			ast.Inspect(ifs.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Motion" {
					callsMotion = true
				}
				return true
			})
			if !callsMotion {
				return true
			}
			found = true
			var byTime, byRemainder bool
			ast.Inspect(ifs.Cond, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "due" {
						byTime = true
					}
				}
				if bin, ok := n.(*ast.BinaryExpr); ok && bin.Op == token.REM {
					byRemainder = true
				}
				return true
			})
			if byRemainder {
				t.Errorf("detection is admitted by a remainder at %s: that counts frames, "+
					"and the number it divides is the cadence the camera declares rather "+
					"than the one it delivers", fset.Position(ifs.Pos()))
			}
			if !byTime {
				t.Errorf("the branch that feeds detection at %s is not gated by time",
					fset.Position(ifs.Pos()))
			}
			return true
		})
		return false
	})
	if !found {
		t.Fatal("no branch in runVideo hands a frame to detection: this guard is " +
			"watching nothing")
	}
}
