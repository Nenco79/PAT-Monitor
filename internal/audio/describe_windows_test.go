package audio

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-ole/go-ole"

	"patmonitor/internal/wincom"
)

// TestTheCodeOfAVanishedDeviceIsNamed is the one this file was written for.
//
// A microphone unplugged while the monitor watches fails the capture loop, and
// what the log carries is that HRESULT. Left raw, go-ole renders it as "error
// 2290679812" followed by a complaint from FormatMessage — a decimal nobody can
// look up, next to a sentence saying the lookup failed. It is read at night,
// after the event, and it is the only thing that separates a device that went
// away from a driver that stopped answering.
func TestTheCodeOfAVanishedDeviceIsNamed(t *testing.T) {
	got := describeAudclnt(ole.NewError(0x88890004)).Error()
	if !strings.Contains(got, "AUDCLNT_E_DEVICE_INVALIDATED") {
		t.Errorf("the name is missing: %q", got)
	}
	if !strings.Contains(got, "0x88890004") {
		t.Errorf("the hexadecimal is missing, and it is what leads to the "+
			"documentation: %q", got)
	}
	if strings.Contains(got, "2290679812") {
		t.Errorf("the decimal survived, and it is the form nobody can look "+
			"up: %q", got)
	}
}

// TestEveryNamedCodeSaysItsNumber walks the table instead of picking an entry
// from it: a `case` added with the name and without the number, or with the
// number of its neighbour, is exactly the defect a single fixture absolves.
func TestEveryNamedCodeSaysItsNumber(t *testing.T) {
	// The codes the table claims to cover. Written here as the numbers, so the
	// test asserts the mapping rather than repeating whatever the code does.
	for _, code := range []uint32{
		0x88890004, 0x8889000A, 0x88890026, 0x88890027, 0x88890028, 0x88890029,
	} {
		name := audclntName(code)
		if name == "" {
			t.Errorf("0x%08X is not named", code)
			continue
		}
		if !strings.HasPrefix(name, "AUDCLNT_E_") {
			t.Errorf("0x%08X answers %q, which is not a code name", code, name)
		}
		if got := describeAudclnt(ole.NewError(uintptr(code))).Error(); !strings.Contains(got, fmt.Sprintf("0x%08X", code)) {
			t.Errorf("0x%08X: the rendering loses the number: %q", code, got)
		}
	}
}

// TestAnUnknownCodeStillLosesTheDecimal is the branch that has no name to give.
// It must still convert the base: an unknown code in hexadecimal can be
// searched for, the same code in decimal cannot.
func TestAnUnknownCodeStillLosesTheDecimal(t *testing.T) {
	got := describeAudclnt(ole.NewError(0x80070057)).Error()
	if !strings.Contains(got, "0x80070057") {
		t.Errorf("the hexadecimal is missing: %q", got)
	}
	if strings.Contains(got, "2147942487") {
		t.Errorf("the decimal survived: %q", got)
	}
}

// TestANonCOMErrorPassesThrough is what makes the function safe to put at every
// COM site: whatever is not an OleError comes back untouched, wrapping included,
// so nobody has to decide per site whether it applies.
func TestANonCOMErrorPassesThrough(t *testing.T) {
	sentinel := errors.New("the endpoint has delivered no data")
	wrapped := fmt.Errorf("context: %w", sentinel)
	got := describeAudclnt(wrapped)
	if got != wrapped {
		t.Errorf("the error was replaced: %v", got)
	}
	if !errors.Is(got, sentinel) {
		t.Error("the chain was broken, so errors.Is no longer reaches the cause")
	}
}

// **The refusal of a permission leaves this package as a value.**
//
// It is the one HRESULT here that whoever is upstream has to *decide* on rather
// than print: a microphone the user has taken away is not a microphone that has
// broken, and the two want different words on the page and different remedies.
// The reading of the number lives in `internal/wincom`, because the video path
// meets the same refusal through an unrelated call and a constant spelled at
// both sites is a constant that can be wrong at one of them.
//
// **Verified to catch**: with the branch removed from `describeAudclnt`, the
// first assertion fails — and what that failure describes is the monitor
// announcing "the microphone is missing" about a microphone that is plugged in
// and working.
func TestTheRefusalOfPermissionLeavesAsAValue(t *testing.T) {
	err := describeAudclnt(ole.NewError(0x80070005))
	if !wincom.Denied(err) {
		t.Errorf("E_ACCESSDENIED does not come out as a refusal: %v", err)
	}
	// This function's own rule, which the refusal does not get to break: the
	// hexadecimal is what whoever reads the log at night types into a search
	// box.
	if got := err.Error(); !strings.Contains(got, "0x80070005") {
		t.Errorf("the message %q has lost the code", got)
	}

	// And the codes this package meets every day are not refusals of anything:
	// a predicate that said yes to all of them would file an unplugged
	// microphone as a revoked permission.
	for _, code := range []uintptr{0x88890004, 0x88890027, 0x88890019} {
		if wincom.Denied(describeAudclnt(ole.NewError(code))) {
			t.Errorf("0x%08X reads as a refusal of permission", code)
		}
	}
}
