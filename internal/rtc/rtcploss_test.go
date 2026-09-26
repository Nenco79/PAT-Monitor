package rtc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// A report block about the audio is not video loss; with no SSRC to compare,
// every block still counts, because ignoring losses for want of a number would
// switch off the one piece of evidence that brings the bitrate down on its own.
func TestOnlyTheVideosBlocksAreVideoLoss(t *testing.T) {
	const video, audio = 0x1111, 0x2222
	if !aboutTheVideo(video, video) {
		t.Error("the video's own block was not counted")
	}
	if aboutTheVideo(audio, video) {
		t.Error("the audio's block was counted as video loss")
	}
	if !aboutTheVideo(audio, 0) {
		t.Error("with the video's SSRC unknown a block was dropped: losses would go unheard")
	}
}

// **The filter has to stand where the loss is recorded**, and a test on the
// predicate cannot see it go: removing the call leaves aboutTheVideo correct
// and unused. So the source is read — in drainRTCP, the loop that records a
// loss must ask whose block it is.
//
// **The defect was put back and this test fails with it**: with the call
// taken out of the loop, every block is video loss again.
func TestTheLossIsRecordedOnlyAfterAskingWhoseBlockItIs(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "hub.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var drain *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "drainRTCP" {
			drain = fn
		}
	}
	if drain == nil {
		t.Fatal("drainRTCP is not in hub.go: this guard reads nothing")
	}

	loops := 0
	ast.Inspect(drain.Body, func(n ast.Node) bool {
		loop, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		records, asks := false, false
		ast.Inspect(loop.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				asks = asks || fun.Name == "aboutTheVideo"
			case *ast.SelectorExpr:
				records = records || fun.Sel.Name == "recordLoss"
			}
			return true
		})
		if records {
			loops++
			if !asks {
				t.Errorf("%s: a loss is recorded for every report block without "+
					"asking whether the block is the video's", fset.Position(loop.Pos()))
			}
		}
		return true
	})
	if loops == 0 {
		t.Fatal("no loop in drainRTCP records a loss: this guard reads nothing")
	}
}
