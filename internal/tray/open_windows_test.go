//go:build windows

package tray

import "testing"

// A command with no address, or a state read before the address is known, would
// otherwise hand an empty string to the shell and take whatever it answers for
// one. The refusal is the first line of Open, so nothing reaches the shell and
// no browser opens for whoever runs the tests.
func TestNothingToOpenIsRefusedBeforeTheShellIsAsked(t *testing.T) {
	if err := Open(""); err == nil {
		t.Error("Open with no target reported that it opened something")
	}
}
