//go:build windows

package tray

import "testing"

// Anything above 32 is a success and <= 32 is a failure, and **the value is not
// an HRESULT**: both mistakes are the same mistake — unpacked as an HRESULT, or
// replaced by `if err != nil`, which the call invites because `Proc.Call`'s
// third return is an `Errno` and non-nil even when it is zero — a browser that
// opened is reported as a browser that did not.
//
// 0x80004005 is here to say so: it is the shape an HRESULT failure has, it is
// above 32, and to this call that makes it a success.
func TestTheShellsFailureRangeEndsAtThirtyTwo(t *testing.T) {
	for _, c := range []struct {
		name string
		code uintptr
		fail bool
	}{
		{"out of memory", 0, true},
		{"access refused", 5, true},
		{"nothing can open it", 31, true},
		{"the last code of the range", 32, true},
		{"the first code past it", 33, false},
		{"a successful open", 42, false},
		{"an HRESULT that means failure, read by somebody who takes this for one", 0x80004005, false},
	} {
		err := shellResult(c.code)
		if got := err != nil; got != c.fail {
			t.Errorf("%s: code %d read as a failure = %v, wanted %v (%v)",
				c.name, c.code, got, c.fail, err)
		}
	}
}

// A command with no address, or a state read before the address is known, would
// otherwise hand an empty string to the shell and take whatever it answers for
// one. The refusal is the first line of Open, so nothing reaches the shell and
// no browser opens for whoever runs the tests.
func TestNothingToOpenIsRefusedBeforeTheShellIsAsked(t *testing.T) {
	if err := Open(""); err == nil {
		t.Error("Open with no target reported that it opened something")
	}
}
