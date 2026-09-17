//go:build windows

package tray

import (
	"testing"
	"unsafe"
)

// The fault this test prevents is silent, and it is the nastiest of its kind in
// this sort of code: cbSize is computed with unsafe.Sizeof, so a wrong structure
// declares its own wrong size with precision. Windows accepts it, reads the
// fields at the offsets it expects, and out comes an icon with no tooltip, or a
// balloon with another field's title, or nothing at all — never an error.
//
// The numbers are those of the 64-bit Windows ABI, where pointers align to 8
// bytes. They can be checked by hand by adding up the fields of
// NOTIFYICONDATAW, and that is what to do if one day this test fails: it is not
// the number that has to change, it is the structure.
func TestTheStructSizes(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"NOTIFYICONDATAW", unsafe.Sizeof(notifyIconData{}), 976},
		{"WNDCLASSEXW", unsafe.Sizeof(wndClassEx{}), 80},
		{"ICONINFO", unsafe.Sizeof(iconInfo{}), 32},
		{"BITMAPINFOHEADER", unsafe.Sizeof(bitmapInfoHeader{}), 40},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %d bytes, wanted %d", c.name, c.got, c.want)
		}
	}
}

// The offsets matter as much as the total size, and are not implied by it: two
// structures of the same length with two fields swapped would pass the test
// above. What is checked is the three places where alignment inserts padding,
// which are the only ones where Go and C could diverge.
func TestTheOffsetsOfTheFieldsWeFill(t *testing.T) {
	var n notifyIconData
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		// hWnd is a pointer after a DWORD: four bytes of padding.
		{"hWnd", unsafe.Offsetof(n.HWnd), 8},
		// hIcon is a pointer after three DWORDs: another four.
		{"hIcon", unsafe.Offsetof(n.HIcon), 32},
		{"szTip", unsafe.Offsetof(n.SzTip), 40},
		{"szInfo", unsafe.Offsetof(n.SzInfo), 304},
		{"szInfoTitle", unsafe.Offsetof(n.SzInfoTitle), 820},
		{"guidItem", unsafe.Offsetof(n.GuidItem), 952},
		{"hBalloonIcon", unsafe.Offsetof(n.HBalloonIcon), 968},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("NOTIFYICONDATAW.%s at %d, wanted %d", c.name, c.got, c.want)
		}
	}
}

// The icon is drawn, so it can be tested: every phase has to have a drawing of
// its own, and at small sizes the opaque pixels have to be enough to be seen. A
// phase with no colour would give a grey icon identical to "starting", that is,
// a monitor declaring it does not know how it is exactly when it does.
func TestEveryPhaseHasItsOwnColour(t *testing.T) {
	phases := []Phase{PhaseStarting, PhaseHome, PhaseOutside, PhaseCheck, PhaseFault}
	seen := map[[3]float64]Phase{}
	for _, f := range phases {
		c, ok := phaseColor[f]
		if !ok {
			t.Errorf("phase %q has no colour", f)
			continue
		}
		if other, duplicate := seen[c]; duplicate {
			t.Errorf("phases %q and %q have the same colour: they cannot be told apart", f, other)
		}
		seen[c] = f
	}
}

// **A tooltip that is not asked to be shown is not seen.** With version 4 the
// shell suppresses `szTip`, and the defect is the absence of a thing: no error,
// no log line, just nothing under the pointer.
//
// The test sits on the constructor of the structure and not on the two places
// that fill it, because that is where the rule is written once.
func TestATooltipIsAlwaysAskedToBeShown(t *testing.T) {
	tr := &Tray{}

	if f := tr.notifyData(nifTip).UFlags; f&nifShowtip == 0 {
		t.Errorf("uFlags = %#x: with NIF_TIP there is no NIF_SHOWTIP, and with version 4 the tooltip vanishes", f)
	}
	if f := tr.notifyData(nifMessage | nifIcon | nifTip).UFlags; f&nifShowtip == 0 {
		t.Errorf("uFlags = %#x: NIF_SHOWTIP missing when the icon is added", f)
	}

	// And it is not added where there is no tooltip: `NIM_SETVERSION` and
	// `NIM_DELETE` come through here with zero flags, and declaring a field
	// that has not been filled is how Windows comes to read whatever it finds.
	if f := tr.notifyData(0).UFlags; f != 0 {
		t.Errorf("uFlags = %#x, wanted zero: without NIF_TIP there is nothing to show", f)
	}
}
