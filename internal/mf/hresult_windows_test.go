//go:build windows

package mf

import (
	"strings"
	"testing"
)

// **The hexadecimal is the part that must never go away.** The name is a
// convenience and the note is a diagnosis, but what whoever reads the log at
// seven in the morning types into a search box is the code — and a message that
// replaced it with a sentence of ours would have removed the one thing that
// travels outside this repository.
func TestTheCodeSurvivesTheName(t *testing.T) {
	err := check("ReadSample", 0xC00D3704)
	if err == nil {
		t.Fatal("a failing HRESULT answered nil")
	}
	got := err.Error()
	for _, want := range []string{"ReadSample", "0xC00D3704", "MF_E_HW_MFT_FAILED_START_STREAMING"} {
		if !strings.Contains(got, want) {
			t.Errorf("the error %q does not carry %q", got, want)
		}
	}
}

// A code nobody has met arrives whole and unadorned: the table absolves
// nothing, which is the difference between describing what we know and
// pretending to know Media Foundation.
func TestACodeWeHaveNotMetIsReportedAsItIs(t *testing.T) {
	got := check("SetOutputType", 0xC00D0BAD).Error()
	if want := "SetOutputType: HRESULT 0xC00D0BAD"; got != want {
		t.Errorf("got %q, wanted %q", got, want)
	}
}

// **`E_FAIL` stays out, and that is a decision rather than an omission.** This
// package has met it — on a size the camera does not declare — but `check` is
// the one funnel for every HRESULT here, so a note about the Source Reader
// would travel with an `E_FAIL` raised while enumerating transforms or building
// a D3D device. **A generic code cannot be given a meaning by the place one of
// its instances was met**, and the guard is here because the tempting edit is
// to add it back.
func TestTheGenericFailureIsNotGivenAMeaning(t *testing.T) {
	if name := mfName(0x80004005); name != "" {
		t.Errorf("E_FAIL is described as %q: it arrives from every call in this package", name)
	}
}

// Success is not an error, whatever the name table says. S_OK and S_FALSE both
// have the top bit clear, and this repository has already paid once for an
// HRESULT read as a fault when two of its three values mean success — see
// internal/wincom.
func TestSuccessIsNotDescribed(t *testing.T) {
	if err := check("MFStartup", 0); err != nil {
		t.Errorf("S_OK became the error %v", err)
	}
	if err := check("MFStartup", 1); err != nil {
		t.Errorf("S_FALSE became the error %v", err)
	}
}

// **The names are not free text**: each one is the identifier from mferror.h,
// so a note may follow after a colon but the first word is what somebody can
// look up. Written the other way round — "the camera is busy
// (MF_E_HW_MFT_FAILED_START_STREAMING)" — the message would read better and
// search worse.
func TestEveryNameOpensWithTheIdentifier(t *testing.T) {
	// The codes are the ones this package translates; the list is here and not
	// derived because a switch is not enumerable, and the floor below is what
	// makes a table that has stopped naming anything fail instead of pass.
	codes := []uint32{0xC00D3704, 0xC00D36B3, 0xC00D36B4, 0xC00D36B5,
		0xC00D36B9, 0xC00D3E80, 0xC00D6D61, 0xC00D6D72}

	named := 0
	for _, c := range codes {
		name := mfName(c)
		if name == "" {
			t.Errorf("0x%08X is in the switch and answers nothing", c)
			continue
		}
		named++
		first, _, _ := strings.Cut(name, ":")
		if strings.ContainsAny(first, " ") {
			t.Errorf("0x%08X is described as %q: the identifier has to come first", c, name)
		}
		if up := strings.ToUpper(first); up != first {
			t.Errorf("0x%08X opens with %q, which is not an identifier from the header", c, first)
		}
	}
	if named < len(codes) {
		t.Fatalf("%d codes of %d were named: this test is no longer reading the table", named, len(codes))
	}
}
