//go:build windows

package tray

import (
	"log/slog"
	"testing"
	"time"
)

// A double click on the icon used to open three pages: in legacy mode the shell
// forwards WM_LBUTTONUP, WM_LBUTTONDBLCLK, WM_LBUTTONUP, and we opened on all
// three. The test is the real sequence, with the intervals it really has.
func TestADoubleClickOpensOnlyOnePage(t *testing.T) {
	var tr Tray
	const within = 500 * time.Millisecond

	start := time.Now()
	// The three messages of a double click arrive within a few tens of
	// milliseconds.
	moments := []time.Duration{0, 90 * time.Millisecond, 95 * time.Millisecond}

	opened := 0
	for _, d := range moments {
		if tr.activatedAt(start.Add(d), within) {
			opened++
		}
	}
	if opened != 1 {
		t.Errorf("one double click opened %d pages, it is worth one", opened)
	}
}

// And a single click has to go on opening: the remedy cannot cost the natural
// gesture.
func TestASingleClickOpens(t *testing.T) {
	var tr Tray
	if !tr.activatedAt(time.Now(), 500*time.Millisecond) {
		t.Error("the first click opened nothing")
	}
}

// Two distinct gestures stay two gestures: past the double-click window,
// whoever clicks again really does want another page.
func TestTwoDistinctGesturesOpenTwice(t *testing.T) {
	var tr Tray
	const within = 500 * time.Millisecond
	start := time.Now()

	opened := 0
	for _, d := range []time.Duration{0, 2 * time.Second} {
		if tr.activatedAt(start.Add(d), within) {
			opened++
		}
	}
	if opened != 2 {
		t.Errorf("two clicks two seconds apart opened %d pages, they are worth 2", opened)
	}
}

// The window is dictated by the system, not by a constant of ours: whoever has
// slowed the double click down for accessibility is exactly the person for whom
// half a second written here would open two pages.
func TestTheWindowFollowsTheSystemSetting(t *testing.T) {
	var tr Tray
	start := time.Now()
	slow := 1500 * time.Millisecond

	tr.activatedAt(start, slow)
	if tr.activatedAt(start.Add(900*time.Millisecond), slow) {
		t.Error("with the slow double click at 1.5s, two clicks 0.9s apart opened two pages")
	}
}

// The value asked of the system is never zero, but if it were the window would
// vanish and the three pages would come back: that is what the fallback is for.
//
// **It is handed the answer**, because asked of the system it never met the
// zero and passed with the fallback deleted. Put back and watched failing.
func TestTheFallbackIsNeverAClosedWindow(t *testing.T) {
	if got := doubleClickFrom(0); got <= 0 {
		t.Errorf("a zero from the system gave a window of %v", got)
	}
	if got := doubleClickFrom(700); got != 700*time.Millisecond {
		t.Errorf("the system's 700 ms became %v", got)
	}
}

// **A WM_CLOSE from outside quits the monitor, the one Run posts does not.**
// The window is top-level, so `taskkill` without /F closes it: the icon went and
// the monitor went on with the camera on and nothing on the screen.
//
// **The defect was put back and this test fails with it**: with the close
// destroying the window alone, OnQuit is never called.
func TestAnOutsideCloseQuitsTheMonitor(t *testing.T) {
	quits := 0
	tr := &Tray{cfg: Config{OnQuit: func() { quits++ }, Log: slog.New(slog.DiscardHandler)}}
	instanceMu.Lock()
	instance = tr
	instanceMu.Unlock()
	t.Cleanup(func() {
		instanceMu.Lock()
		instance = nil
		instanceMu.Unlock()
	})

	wndProc(0, wmClose, 0, 0)
	if quits != 1 {
		t.Errorf("a close from outside called OnQuit %d times, wanted once", quits)
	}

	tr.closingOurselves.Store(true)
	wndProc(0, wmClose, 0, 0)
	if quits != 1 {
		t.Error("the close Run posts on its way out asked to quit again")
	}
}
