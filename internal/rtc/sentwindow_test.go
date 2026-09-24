package rtc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// realProduction is the throughput sequence recorded live, with the quality
// discount in force on a still room. The GOP is two seconds: one sample in two
// contains the keyframe, and between the two there is a factor of seven.
var realProduction = []int{789, 75, 560, 113, 558, 116, 543, 116, 864, 70, 651}

// The last high sample of the sequence, 864, is the one the scale came down on:
// the estimate was worth 338 kbit/s of video, and 338 sits below 40% of 864. On
// the low samples the same estimate was — rightly — an echo.
const (
	estimateAtTheTime = 338
	highSample        = 864
)

// The defect: a single sample, and the one with the keyframe at that, made a
// network collapse be declared while the quantiser sat at 29, the losses at zero
// and the real bandwidth at 2696 kbit/s. Observed cost, twice in five minutes: a
// minute of 640x352 in place of 720p.
func TestTheKeyframeSecondNoLongerLooksLikeACollapse(t *testing.T) {
	if !believableDrop(estimateAtTheTime, highSample, 0) {
		t.Fatal("the sample with the keyframe no longer produces the false collapse: " +
			"the test is no longer looking at the defect it exists to catch")
	}

	var w sentWindow
	var mean int
	for _, p := range realProduction {
		mean = w.add(p)
	}
	if mean == 0 {
		t.Fatal("the window did not fill")
	}
	if believableDrop(estimateAtTheTime, mean, 0) {
		t.Errorf("an echo was taken for a collapse: estimate %d, window mean %d",
			estimateAtTheTime, mean)
	}
	if estimateIsCredible(estimateAtTheTime, mean) &&
		believableDrop(estimateAtTheTime, mean, 0) {
		t.Error("the estimate is still credible, so the scale would come down anyway")
	}
}

// The half not to lose: the window must not make it deaf to a real collapse.
// Measured on a cellular network, the estimate came down to 300 while the
// encoder was producing 2500 — and there the network really had refused.
func TestARealCollapseIsStillBelieved(t *testing.T) {
	var w sentWindow
	var mean int
	for range sentWindowSamples {
		mean = w.add(2500)
	}
	if !believableDrop(300, mean, 0) {
		t.Error("a real collapse was not believed")
	}
}

// The losses stay direct proof and go through no window: they say what happened
// to our packets, not what the network might do.
func TestLossesDoNotNeedTheWindow(t *testing.T) {
	if !believableDrop(2000, 0, 0.05) {
		t.Error("a declared loss was not enough to make the drop believed")
	}
}

// Zero means "I do not know", and it is the harmless direction: with an unknown
// throughput no drop is credible, so it does not come down. The silence lasts one
// turn only, though: with a GOP of two seconds two consecutive samples are enough
// to contain exactly one keyframe, that is to remove the bias, and every extra
// turn of silence would be a turn of blindness to a collapse without losses.
func TestTheWindowSpeaksAfterOneGOP(t *testing.T) {
	var w sentWindow
	if v := w.add(500); v != 0 {
		t.Errorf("with a single sample the window answered %d", v)
	}
	if v := w.add(500); v != 500 {
		t.Errorf("with two samples the window answers %d instead of 500", v)
	}
	if believableDrop(100, 0, 0) {
		t.Error("with the throughput unknown a drop was believed")
	}
}

// A missing reading ages the window. A zero is not "it produced little" — that is
// a capture restart — but keeping the previous value is worse than keeping quiet:
// for the whole restart, which lasts up to half a minute, the window would answer
// with the earlier throughput, and an estimate that decayed in the meantime would
// look like a collapse to it.
func TestAMissingReadingAgesTheWindow(t *testing.T) {
	var w sentWindow
	for range sentWindowSamples {
		w.add(2400)
	}

	// Every missing reading removes the oldest sample, and below the minimum the
	// window goes back to "I do not know" instead of answering with what it held.
	for range sentWindowSamples - sentWindowMinSamples + 1 {
		w.add(0)
	}
	if v := w.add(0); v != 0 {
		t.Errorf("after a restart the window still answers %d", v)
	}
	if believableDrop(500, w.add(0), 0) {
		t.Error("the throughput from before the restart fabricated a collapse")
	}

	// And new samples fill it again, with no trace of the old ones.
	for range sentWindowSamples {
		w.add(600)
	}
	if v := w.add(600); v != 600 {
		t.Errorf("the window answers %d instead of 600: old samples are left", v)
	}
}

// The window does not survive a size change: crossing one it would carry the
// large picture's samples into the small picture's judgement, and one descent
// would authorise another.
func TestTheWindowDoesNotSurviveASizeChange(t *testing.T) {
	var w sentWindow
	for range sentWindowSamples {
		w.add(2500)
	}
	w.reset()
	if v := w.add(400); v != 0 {
		t.Errorf("after a size change the window still answers %d", v)
	}
}

// The proof that stays when this story is forgotten.
//
// The defect was not a wrong value: it was **which number was passed**, and no
// test on a value catches that — `believableDrop` was doing exactly its job on
// the sample it was given. So the guardian reads the syntax tree and demands
// that whoever judges an estimate does not receive one second's throughput.
func TestNoEstimateIsJudgedOnASingleSample(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bitrate.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Who judges an estimate, and in which argument they receive the throughput.
	judges := map[string]int{"estimateIsCredible": 1, "believableDrop": 1}
	// And the floor has to be there: it is inline in a loop, so no test on a
	// value would notice its absence.
	floor := false
	// And the scale has to be released when the last viewer leaves: the governor
	// knows how to do it, but `target` is not called with no viewers, so if
	// nobody calls `release` the old window survives and no test on the governor
	// notices.
	released := false
	seen := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == "atLeastWhatWeDelivered" {
			floor = true
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "release" {
			if r, ok := sel.X.(*ast.Ident); ok && r.Name == "scale" {
				released = true
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		arg, ok := judges[id.Name]
		if !ok || arg >= len(call.Args) {
			return true
		}
		seen++
		name, ok := call.Args[arg].(*ast.Ident)
		if !ok {
			return true
		}
		if name.Name == "measured" {
			t.Errorf("%s: %s receives one second's throughput instead of the window; "+
				"the sample with the keyframe is worth seven times the others",
				fset.Position(name.Pos()), id.Name)
		}
		return true
	})
	if seen == 0 {
		t.Fatal("no call found: the guardian is looking at the wrong file")
	}
	if !released {
		t.Error("nobody releases the scale when the last viewer leaves: " +
			"whoever arrives afterwards inherits the judgement of a scene from ten minutes ago")
	}
	if !floor {
		t.Error("nobody raises the estimate up to what was delivered: " +
			"the cap can come back down below the traffic that was getting through")
	}
}

// **What arrived, the network carried.** It is the remedy for the trap that broke
// the picture in the moment that matters: the estimate echoed our reduced
// traffic, the cap came down to 350 kbit/s with losses at 0.0%, and at the first
// real movement the quantiser went to 49 without the loop being able to raise
// anything — `current` was already the cap, and there was no discount left to
// give back.
func TestTheEstimateNeverGoesBelowWhatWeDelivered(t *testing.T) {
	// The real sequence: ~795 kbit/s of video delivered, estimate 302, no losses.
	if v := atLeastWhatWeDelivered(302, 795, 0); v != 795 {
		t.Errorf("the estimate stayed at %d below the 795 kbit/s that had arrived", v)
	}
	// An estimate sitting above is not touched: the floor is not a target.
	if v := atLeastWhatWeDelivered(2500, 795, 0); v != 2500 {
		t.Errorf("the floor lowered a good estimate to %d", v)
	}
}

// The half not to lose: with losses the network really did refuse, and what we
// believed delivered was not. Measured on a cellular network, 300 estimated while
// we were sending 2500 with 33% of the packets lost.
func TestLossesRemoveTheFloor(t *testing.T) {
	if v := atLeastWhatWeDelivered(300, 2500, 0.33); v != 300 {
		t.Errorf("with 33%% losses the floor held the estimate at %d", v)
	}
	// And a loss just above the noise is enough to remove it.
	if v := atLeastWhatWeDelivered(300, 2500, bitrateLossQuiet); v != 300 {
		t.Errorf("on the loss threshold the floor held: %d", v)
	}
	// Below the noise it does not: an isolated loss happens on any link.
	if v := atLeastWhatWeDelivered(300, 2500, bitrateLossQuiet/2); v != 2500 {
		t.Errorf("a noise loss removed the floor: %d", v)
	}
}

// "I do not know" is not proof: with no known throughput nothing is raised.
func TestAnUnknownThroughputProvesNothing(t *testing.T) {
	if v := atLeastWhatWeDelivered(300, 0, 0); v != 300 {
		t.Errorf("with the throughput unknown the floor raised the estimate to %d", v)
	}
}

// **"I do not know" is not "zero", and that holds for the estimate too.** Zero is
// the vocabulary the governors use to tell each other the congestion control does
// not have enough feedback yet — the eight seconds of warm-up of every new viewer
// — and raising it fabricated a real bandwidth equal to our own throughput, that
// is a cap of six hundred kbit/s on a link nothing was known about yet.
func TestAnAbsentEstimateIsNotRaised(t *testing.T) {
	if v := atLeastWhatWeDelivered(0, 600, 0); v != 0 {
		t.Errorf("an absent estimate became %d, that is a network limit", v)
	}
	// And a negative value, which is the same "I do not know" written worse.
	if v := atLeastWhatWeDelivered(-1, 600, 0); v != -1 {
		t.Errorf("a negative estimate became %d", v)
	}
}

// **The window does not survive a change of size, and a camera swap is one.**
//
// Its four samples are an assertion about a stretch — how much we managed to
// send — and three readers take it: estimateIsCredible, believableDrop and
// atLeastWhatWeDelivered. When the camera changes, the scale is rebuilt on the
// new size and what is being sent stops being what those samples measured; the
// reopen takes about a second, so only one or two samples age out on their own
// and the mean goes on describing a size nobody is sending any more.
//
// The other three commands that change what goes out already reset it. This
// reads the source because **the line is inside a loop**: no test on a value
// would notice its absence, exactly as with the floor of what we delivered.
func TestARebuiltScaleResetsTheSentWindow(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bitrate.go", nil, 0)
	if err != nil {
		t.Fatalf("bitrate.go cannot be parsed: %v", err)
	}

	// The loop's position: before it sits the first build, on a window that is
	// empty by construction and has nothing to forget.
	var loopAt token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "RunBitrateControl" {
			return true
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			if loopAt == token.NoPos {
				if l, ok := m.(*ast.ForStmt); ok {
					loopAt = l.Pos()
				}
			}
			return true
		})
		return false
	})
	if loopAt == token.NoPos {
		t.Fatal("no loop found in RunBitrateControl: this test is looking in the wrong place")
	}

	rebuilds := 0
	ast.Inspect(f, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok || block.Pos() < loopAt {
			return true
		}
		builds := false
		for _, stmt := range block.List {
			as, ok := stmt.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				continue
			}
			if id, ok := as.Lhs[0].(*ast.Ident); !ok || id.Name != "scale" {
				continue
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "newScaleGovernor" {
				builds = true
			}
		}
		if !builds {
			return true
		}
		rebuilds++

		resets := false
		ast.Inspect(block, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "reset" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sent" {
				resets = true
			}
			return true
		})
		if !resets {
			t.Errorf("the scale is rebuilt at %s without resetting the sent window: "+
				"its samples were produced by the previous camera, and three readers "+
				"take them for what is being sent now", fset.Position(block.Pos()))
		}
		return true
	})

	// **The positive control**: with no rebuild found, every assertion above is
	// satisfied by a guard that asked nothing.
	if rebuilds == 0 {
		t.Fatal("no scale rebuild found inside the loop: this test is looking in the wrong place")
	}
}
