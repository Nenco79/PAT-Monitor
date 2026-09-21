//go:build windows

package tray

import (
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// asTheInstance puts a tray where the window procedure looks for it.
//
// The callback is registered with the window class and cannot carry a pointer,
// so `wndProc` finds its tray in a package variable. Driving the procedure
// directly is what makes these tests worth writing: a table over
// `sessionReallyEnding` would go on passing with the whole case gone from the
// switch, which is exactly the defect being guarded against.
func asTheInstance(t *testing.T, tr *Tray) {
	t.Helper()
	instanceMu.Lock()
	previous := instance
	instance = tr
	instanceMu.Unlock()
	t.Cleanup(func() {
		instanceMu.Lock()
		instance = previous
		instanceMu.Unlock()
	})
}

// counted is a tray whose only job is to say whether it was asked to close.
func counted(t *testing.T) (*Tray, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	tr := &Tray{cfg: Config{
		Log:    quiet(),
		OnQuit: func() { mu.Lock(); calls++; mu.Unlock() },
	}}
	asTheInstance(t, tr)
	return tr, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// The case this was written for: before it, both messages fell through to
// DefWindowProc, the process was terminated where it stood, and the orderly
// shutdown never ran. **Put back by deleting the WM_ENDSESSION case and this
// test fails**, which is the only thing that tells it apart from a test that
// asks nothing.
func TestTheSessionEndingClosesTheMonitor(t *testing.T) {
	_, calls := counted(t)

	wndProc(windows.Handle(0), wmEndSession, 1, endSessionLogoff)

	if got := calls(); got != 1 {
		t.Errorf("the monitor was asked to close %d times, wanted once", got)
	}
}

// The same message in the other direction. Somebody else refused the shutdown
// after everybody had been asked, the session goes on, and a handler that reads
// only the message would have left the room unwatched for a key we did not
// press.
func TestAnEndOfSessionCalledOffDoesNotCloseTheMonitor(t *testing.T) {
	_, calls := counted(t)

	wndProc(windows.Handle(0), wmEndSession, 0, endSessionLogoff)

	if got := calls(); got != 0 {
		t.Errorf("the monitor was closed %d times on a shutdown that was called off", got)
	}
}

// The question is not the verdict, and it must never be refused: FALSE here
// stops the computer going off, at night, behind a dialogue nobody is in front
// of.
func TestTheQuestionIsConsentedToAndConcludesNothing(t *testing.T) {
	_, calls := counted(t)

	answer := wndProc(windows.Handle(0), wmQueryEndSession, 0, endSessionLogoff)

	if answer == 0 {
		t.Error("the shutdown was vetoed")
	}
	if got := calls(); got != 0 {
		t.Errorf("the monitor closed on the question, %d times", got)
	}
}

// The notification area is not written to on the way out of a session.
//
// `Shell_NotifyIcon` is a call into Explorer, which at that moment is being torn
// down too, and it sits in `Run`'s defer — that is, in front of the orderly
// shutdown. What is checked here is the decision, because the call itself
// cannot be observed from a test: **put the defect back by dropping
// `t.endingSession` from `worthTellingTheShell` and this fails.**
func TestOnTheWayOutTheNotificationAreaIsNotWrittenTo(t *testing.T) {
	tr, _ := counted(t)
	// A handle that is never dereferenced: what it stands for is "the icon is
	// still up", which is the other half of the condition.
	tr.hwnd = windows.Handle(1)

	if !tr.worthTellingTheShell() {
		t.Fatal("with the session running the icon has to come off normally")
	}

	wndProc(windows.Handle(0), wmEndSession, 1, endSessionLogoff)

	if tr.worthTellingTheShell() {
		t.Error("the shell is still being written to while the session ends")
	}
}

// And the two cases that are not the end leave it alone: a shutdown called off
// must not leave a monitor that has stopped talking to the notification area
// for the rest of the night.
func TestAQuestionAndACancellationLeaveTheIconAlone(t *testing.T) {
	tr, _ := counted(t)
	tr.hwnd = windows.Handle(1)

	wndProc(windows.Handle(0), wmQueryEndSession, 0, endSessionLogoff)
	wndProc(windows.Handle(0), wmEndSession, 0, endSessionLogoff)

	if !tr.worthTellingTheShell() {
		t.Error("the icon was given up on a session that is not ending")
	}
}

// The reason is what tells a log where the computer went off from one where
// somebody logged out, and no bit set is the shutdown rather than a gap.
func TestTheReasonIsNamedAndNoBitIsTheShutdown(t *testing.T) {
	for _, c := range []struct {
		lparam uintptr
		want   string
	}{
		{0, "shutdown"},
		{endSessionLogoff, "logoff"},
		{endSessionLogoff | endSessionCritical, "logoff+critical"},
		{endSessionCritical, "shutdown+critical"},
		{endSessionLogoff | endSessionCloseApp, "logoff+close-app"},
	} {
		if got := sessionEndReason(c.lparam); got != c.want {
			t.Errorf("reason(%#x) = %q, wanted %q", c.lparam, got, c.want)
		}
	}
}
