//go:build windows

package tray

import (
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
func TestTheFallbackIsNeverAClosedWindow(t *testing.T) {
	if doubleClickTime() <= 0 {
		t.Errorf("double-click window not positive: %v", doubleClickTime())
	}
}
