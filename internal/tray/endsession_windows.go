//go:build windows

package tray

// The end of the Windows session, read at the icon's window.
//
// **With `-H=windowsgui` that window is the only thing the system can talk
// to.** There is no console, so no Ctrl+C and no `CTRL_SHUTDOWN_EVENT`; there is
// no service, so nothing is asked to stop. When the computer goes off or
// somebody logs out, what arrives is a pair of messages sent to every top-level
// window — and the hidden window behind the notification-area icon is this
// program's only top-level window. Before this file both went to
// `DefWindowProc`, which consents and says nothing: the process was then
// terminated where it stood, with the orderly shutdown in `cmd/pat-monitor`
// never entered.
//
// **What is lost by being killed there is not the camera.** The system takes the
// camera, the handles and the memory back from any process that exits — the
// argument already written for the encoder that is not released at the end, and
// for the deadline in `shutdown.go`. What is lost is what only this program can
// do: the clip being written out, the viewers' connections closed cleanly, and
// the last lines of the log. **The third is the one that costs afterwards**,
// because without them every shutdown of the computer reads in the session log
// exactly like a crash — and the session log is the instrument the night test is
// judged on.

// The two flags of `lParam` that are read. `ENDSESSION_CLOSEAPP` arrives only to
// whoever registered for restart, which we do not; it is decoded all the same,
// because a reason that is not named reaches the log as a bare number.
const (
	endSessionCloseApp = 0x00000001
	endSessionCritical = 0x40000000
	endSessionLogoff   = 0x80000000
)

// sessionReallyEnding reads the `wParam` of `WM_ENDSESSION`.
//
// **The message arrives in both directions**: TRUE means the session is ending,
// FALSE that it was called off after everybody had already been asked. They are
// the same message and they differ by one word, so a handler that ignores
// `wParam` closes the monitor on a shutdown that never happened — a room left
// unwatched for a key somebody else pressed.
func sessionReallyEnding(wparam uintptr) bool { return wparam != 0 }

// sessionEndReason names what is ending, for the log.
//
// **No bit set is the shutdown**, and that is the documented reading rather than
// a default chosen here: `ENDSESSION_LOGOFF` says the user is logging out, and
// its absence says the machine is going off. The two are worth telling apart in
// a log, because one of them comes back in a minute and the other does not.
func sessionEndReason(lparam uintptr) string {
	bits := uint32(lparam)
	reason := "shutdown"
	if bits&endSessionLogoff != 0 {
		reason = "logoff"
	}
	if bits&endSessionCritical != 0 {
		reason += "+critical"
	}
	if bits&endSessionCloseApp != 0 {
		reason += "+close-app"
	}
	return reason
}
