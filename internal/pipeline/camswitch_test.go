package pipeline

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"patmonitor/internal/devices"
)

func quietPipeline() *Pipeline {
	return New(Config{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

// **Choosing a camera closes the capture, and the close is a decision.**
//
// It is the hinge of the whole feature: the choice is applied by **reopening**,
// so if SetCamera did not reach as far as the close, the box would save a line
// in the file and move nothing — and a knob that moves nothing is worse than an
// absent knob.
func TestChoosingACameraClosesTheCaptureInProgress(t *testing.T) {
	p := quietPipeline()

	closed := false
	p.camCancel.Store(context.CancelFunc(func() { closed = true }))

	p.SetCamera("another camera")

	if !closed {
		t.Error("the capture in progress was not closed: the choice moves nothing")
	}
	if got := p.camWantedLink(); got != "another camera" {
		t.Errorf("the camera to open is %q: the reopen would use the old one", got)
	}
	if !p.camReopen.Load() {
		t.Error("the close is not marked as ours: the supervisor would report a fault")
	}
	if !p.camChosen.Load() {
		t.Error("the choice is not marked: a camera chosen and absent would say nothing")
	}
}

// **Closing the capture is not enough to make it reopen.** With the open
// failing there is no capture to cancel and the supervisor sleeps up to thirty
// seconds: the choice would sit written and without effect for half a minute,
// while the page has already taken it as done.
func TestAChoiceWakesTheSupervisorWithNothingToClose(t *testing.T) {
	p := quietPipeline()

	select {
	case <-p.camWake:
		t.Fatal("somebody was woken before any choice was made")
	default:
	}

	p.SetCamera("a camera")

	select {
	case <-p.camWake:
	default:
		t.Fatal("with nothing to close, whoever waits to try again was not woken")
	}

	// And two choices close together are worth one reopen: the wake-up is a
	// fact, not a queue, so whoever sends it never waits.
	p.SetCamera("one")
	p.SetCamera("two")
	<-p.camWake
	select {
	case <-p.camWake:
		t.Error("two choices left two wake-ups: the second reopen has nothing to do")
	default:
	}
}

// **The supervisor has to recognise the close we decided on.** With any other
// error, changing camera would write "capture interrupted" in the log, count as
// a fault and wait out the backoff — up to thirty seconds of still picture
// after a command, which reads as a command that did not work.
//
// It is read from the syntax tree because the defect is an **absence**: the
// branch removed, everything still compiles and every value stays right.
func TestTheVideoSupervisorRecognisesTheDecidedClose(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "pipeline.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	found := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name != "Run" && fn.Name.Name != "runOnce" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				switch id.Name {
				case "errCamSwitch":
					found[fn.Name.Name+":sentinel"] = true
				case "errCamRecheck":
					found[fn.Name.Name+":recheck"] = true
				case "camWake":
					found["Run:wake"] = true
				}
			}
			return true
		})
	}
	// The vacuity control: if the two functions were not found at all, the
	// checks below would pass by asking nothing.
	if len(found) == 0 {
		t.Fatal("this test is looking in the wrong place: neither Run nor runOnce was read")
	}
	if !found["runOnce:sentinel"] {
		t.Error("nobody turns a camera swap into errCamSwitch: it would be reported as a fault")
	}
	if !found["Run:sentinel"] {
		t.Error("the supervisor does not recognise errCamSwitch: a swap costs a log line and a backoff")
	}
	if !found["Run:wake"] {
		t.Error("the supervisor does not listen on camWake: a choice made while the open fails waits half a minute")
	}
	// **The two closures we decide on stay two.** Folded into one, the log
	// would call the chosen camera's return a choice, and whoever reads it
	// would go looking for a command where there was a precaution.
	if !found["runOnce:recheck"] || !found["Run:recheck"] {
		t.Error("the recheck's close is not told apart from a choice: one log line for two pieces of news")
	}
}

// **A failure in the enumeration stops nothing**: the chosen link goes to Media
// Foundation as it stands, which is how it was opened before that question was
// asked at all. Worse than opening a camera badly there is only not opening it.
func TestAnEnumerationThatFailsDoesNotStopTheOpen(t *testing.T) {
	var lines bytes.Buffer
	log := slog.New(slog.NewTextHandler(&lines, nil))

	cam, fellBack := resolveCamera("the chosen link", func() ([]devices.Device, error) {
		return nil, errors.New("no COM here")
	}, log)

	if cam.Link() != "the chosen link" {
		t.Errorf("the open would ask for %q instead of the chosen camera", cam.Link())
	}
	// And it is not a fallback: nothing was replaced, the question simply could
	// not be asked. Declaring one would raise an alert about a camera that may
	// well be the right one.
	if fellBack {
		t.Error("an enumeration that failed was reported as a camera swapped for another")
	}
	if lines.Len() == 0 {
		t.Error("the enumeration failed in silence")
	}
}

// The other half: the chosen camera is not connected, so another one is opened
// **and that is reported**, because the picture may be of another room.
func TestAChosenCameraThatIsGoneIsReplacedAndReported(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	list := func() ([]devices.Device, error) {
		return []devices.Device{devices.New("Integrated Camera", "the link that is there")}, nil
	}

	cam, fellBack := resolveCamera("a link that is gone", list, log)
	if !fellBack {
		t.Error("fell back on another camera without reporting it")
	}
	if cam.Link() != "the link that is there" {
		t.Errorf("opened %q instead of the camera that is connected", cam.Link())
	}

	// And with the chosen one connected, nothing is reported.
	if _, fellBack := resolveCamera("the link that is there", list, log); fellBack {
		t.Error("the camera that was asked for was reported as a replacement")
	}
}

// The wake-up must never block whoever sends it: a choice arrives from an HTTP
// handler, and a handler that waits on a channel is a page that hangs.
func TestAChoiceNeverWaits(t *testing.T) {
	p := quietPipeline()
	done := make(chan struct{})
	go func() {
		for range 50 {
			p.SetCamera("x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetCamera blocked: nobody was reading the wake-up channel")
	}
}

// **An interrupted probe gives the flag back.**
//
// `brProbed` is set by the caller before the goroutine starts, so an abort that
// left it set would mean the question "does this encoder obey?" is never asked
// again for the whole process — and what is left is `bitrateSeen`, which on
// Quick Sync stays silent by construction. It became reachable when the
// capture's context became per-session so that choosing a camera can close it:
// before that this goroutine held Run's context and outlived every restart.
func TestAnInterruptedProbeIsAskedAgain(t *testing.T) {
	p := quietPipeline()
	p.brMu.Lock()
	p.brProbed = true
	p.brMu.Unlock()

	inForce := p.currentBitrate()
	ctx, stop := context.WithCancel(context.Background())
	stop()
	p.probeBitrate(ctx, 2500)

	p.brMu.Lock()
	stillSpent := p.brProbed
	p.brMu.Unlock()
	if stillSpent {
		t.Error("the probe counted as done without measuring anything: this process will never ask again")
	}
	// And it left nothing behind. The value in force is what a rebuilt encoder
	// starts from, so a probe that aborted after halving it and before putting
	// it back would hand the next encoder half the preset with nobody having
	// asked.
	if got := p.currentBitrate(); got != inForce {
		t.Errorf("the bitrate in force went from %d to %d across a probe that measured nothing",
			inForce, got)
	}
}

// **A session does not inherit the other camera's step request.**
//
// `wantFormat` is deposited by the governor and consumed by the frame loop, and
// it outlives the session meant to consume it. Harmless while every restart came
// back to the same starting size; with a camera that can change, a pending step
// of a small camera's scale is smaller than anything the rebuilt scale knows —
// `applyFormat`'s cap only looks upwards, and `resync` invents no size that is
// not a step. What is left is a governor that believes it is at full size while
// a shrunken picture goes out.
//
// It is read from the syntax tree because the defect is an **absence**, and
// because reaching the line itself wants a camera.
func TestASessionDoesNotInheritTheOtherCamerasStepRequest(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "pipeline.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	// **The question is who *writes* it, and the first version of this guard
	// asked the wrong one**: it looked for the name `wantFormat` anywhere in
	// runVideo, and `applyFormat` — which lives inside runVideo — reads it on
	// every frame. So the guard passed with the line removed, that is, it
	// absolved exactly the defect it is named after. It now wants a `Store`.
	var read, cleared bool
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name != "runVideo" {
			continue
		}
		read = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			store, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || store.Sel.Name != "Store" {
				return true
			}
			if field, ok := store.X.(*ast.SelectorExpr); ok && field.Sel.Name == "wantFormat" {
				cleared = true
			}
			return true
		})
	}
	if !read {
		t.Fatal("this test is looking in the wrong place: runVideo was not found")
	}
	if !cleared {
		t.Error("runVideo never stores into wantFormat: a step asked for the previous " +
			"camera is applied to the new one, where it may match no step at all")
	}
}

// **The chosen camera coming back reopens the capture**, and until it does the
// monitor is showing the wrong room while the `camera-other` alert asserts the
// chosen camera is not connected. Without this the assertion becomes false the
// moment somebody plugs the webcam back in, and stays false all night: an alarm
// that rings for no reason, in a program that has one alarm.
func TestTheChosenCameraComingBackReopensTheCapture(t *testing.T) {
	p := quietPipeline()
	p.camWanted.Store("the chosen link")
	p.camFellBack.Store(true)

	closed := make(chan struct{}, 1)
	p.camCancel.Store(context.CancelFunc(func() { closed <- struct{}{} }))

	var back atomic.Bool
	var asked atomic.Int64
	list := func() ([]devices.Device, error) {
		asked.Add(1)
		if back.Load() {
			return []devices.Device{
				devices.New("Another Camera", "another link"),
				devices.New("The Chosen One", "the chosen link"),
			}, nil
		}
		return []devices.Device{devices.New("Another Camera", "another link")}, nil
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go p.watchForTheChosenCamera(ctx, list, 5*time.Millisecond)

	// **While it is still absent nothing is interrupted.** A precaution that
	// reopened on every look would cost a second and a third of picture every
	// two minutes, all night, for a camera that is not coming back.
	select {
	case <-closed:
		t.Fatal("the capture was closed with the chosen camera still absent")
	case <-time.After(60 * time.Millisecond):
	}
	if asked.Load() == 0 {
		t.Fatal("this test is looking in the wrong place: nobody was asked for the list")
	}

	back.Store(true)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the chosen camera came back and nothing reopened")
	}
	if !p.camReopen.Load() {
		t.Error("the close is not marked as ours: the supervisor would report a fault")
	}
	if !p.camRecheck.Load() {
		t.Error("the close is not marked as a recheck: the log would call it a choice")
	}
	// And it is not a choice: that flag makes the open announce a fallback, and
	// nobody chose anything here.
	if p.camChosen.Load() {
		t.Error("a recheck announced itself as somebody's choice")
	}
}

// **It does not look while the chosen camera is the one open.** There is nothing
// to find, so an enumeration every two minutes would be a cost paid all night
// for the normal case — the microphone's "only if it is the worse path".
func TestNothingIsLookedForWhileTheChosenCameraIsOpen(t *testing.T) {
	p := quietPipeline()
	p.camWanted.Store("the chosen link")
	p.camFellBack.Store(false)

	var asked atomic.Int64
	list := func() ([]devices.Device, error) {
		asked.Add(1)
		return nil, nil
	}
	ctx, stop := context.WithCancel(context.Background())
	go p.watchForTheChosenCamera(ctx, list, 5*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	stop()

	if got := asked.Load(); got != 0 {
		t.Errorf("the list was asked for %d times with the chosen camera already open", got)
	}
}
