//go:build windows

package mf

import (
	"errors"
	"testing"
)

// **"Not here" and "not now" are two answers.** Every refusal used to be filed
// as a camera with no control, which is dropped without a line: a driver that
// is not yet streaming, or a device that has gone, would then leave the
// framing on all night with nothing in the log.
func TestOnlyAnAbsenceIsFiledAsNoFraming(t *testing.T) {
	for _, res := range []uintptr{0x80004002, 0x80004001, 0xC00D36BA, 0x80070490, 0x80070492} {
		if err := refused("probe", res); !errors.Is(err, ErrNoFraming) {
			t.Errorf("%#x is an absence and came back as %v", res, err)
		}
	}
	// STATUS_INVALID_DEVICE_STATE as a Win32 HRESULT, E_FAIL, and a device gone.
	for _, res := range []uintptr{0x8007139F, 0x80004005, 0xC00D3EA2} {
		if err := refused("probe", res); err == nil || errors.Is(err, ErrNoFraming) {
			t.Errorf("%#x is a fault and came back as %v", res, err)
		}
	}
	if err := refused("probe", 0); !errors.Is(err, ErrNoFraming) {
		t.Errorf("S_OK with no object came back as %v", err)
	}
}

// **The window is found in both shapes a payload may have.** The documentation
// of LockPayload does not say whether the extended-property header comes with
// it; an ACER HD User Facing hands over the sixteen bytes of the setting alone,
// and a driver that hands over the header too must not have the window read
// out of the header's flags.
func TestTheWindowIsFoundWithAndWithoutTheHeader(t *testing.T) {
	setting := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0} // as measured
	if off, ok := window(setting); !ok || off != 0 {
		t.Fatalf("the bare setting: off=%d ok=%v", off, ok)
	}
	withHeader := make([]byte, extendedPropHeader+digitalWindowSetting)
	withHeader[0] = 1                     // Version
	withHeader[8] = byte(len(withHeader)) // Size, which names the whole
	copy(withHeader[extendedPropHeader:], setting)
	if off, ok := window(withHeader); !ok || off != extendedPropHeader {
		t.Fatalf("with the header: off=%d ok=%v", off, ok)
	}
	// A header whose Size does not match is not believed: writing the window
	// at a guessed offset would write it into somebody else's bytes.
	withHeader[8] = 99
	if _, ok := window(withHeader); ok {
		t.Fatal("a payload whose header names another size was taken as a window")
	}
	if _, ok := window(setting[:12]); ok {
		t.Fatal("a payload too short for a window was taken as one")
	}
}
