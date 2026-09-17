//go:build windows

package applog

import (
	"os"

	"golang.org/x/sys/windows"
)

// The Go runtime does not write through this package, and that is the whole
// reason this file exists.
//
// A panic's trace, and the fatal errors no recover can catch — a concurrent map
// write, a stack overflow — are written by the runtime straight to **file
// descriptor 2**, which never passes through slog, through Fanout or through
// anything else of ours. So the one event the log was invented for, a process
// that dies during the night, was the one event it could not record: with
// `-H=windowsgui` the monitor has no standard error at all, and the trace went
// nowhere.
//
// Measured, on this machine, with a throwaway probe built both ways:
//
//	launched from a shell     GetStdHandle(STD_ERROR) = 0x228, os.Stderr writes
//	launched detached         GetStdHandle(STD_ERROR) = 0x0,   "handle is invalid"
//
// and in **both** cases, after pointing that handle at a file, the trace of a
// panic raised in another goroutine was written into the file, appended after
// the lines already there.
//
// The second row is the monitor as it really runs — from the notification area,
// or from a double click — and the first is a console the developer is looking
// at. Hence the rule: **we stand in for a standard error that is not there, and
// never take away one that is.** Whoever runs `pat-monitor 2>somewhere` has
// asked for the traces to go somewhere, and gets them there.
//
// What the redirection does **not** cost is the console, and that was not
// obvious: `os.Stderr` is built once at start-up from the handle of that
// moment, so it keeps writing where it always did — measured above, both
// before and after. The log's two destinations stay two.

// standardErrorIsMissing says whether this process has a standard error of its
// own.
//
// Zero is what a process launched with no console inherits; InvalidHandle is
// what the call answers when it fails. Neither is somewhere a trace could be
// read from.
func standardErrorIsMissing() bool {
	h, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	return err != nil || h == 0 || h == windows.InvalidHandle
}

// pointStandardErrorAt makes the runtime's last words land in f.
func pointStandardErrorAt(f *os.File) error {
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
}

// releaseStandardError gives the handle up before the file behind it is closed.
//
// **A closed handle's value is handed out again**, to the next socket or file
// this process opens, and a trace written there would end up inside somebody
// else's bytes — with nothing to read anywhere, which is the shape of fault
// this file exists to remove. Zero is the value a process with no console
// carries, and writing to it fails without taking anything with it.
func releaseStandardError() error {
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, 0)
}
