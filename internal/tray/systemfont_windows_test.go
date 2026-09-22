//go:build windows

package tray

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// **A structure handed to Windows is a layout, and `cbSize` makes a wrong one
// look right.** The call is refused unless the size matches, and the size is
// computed from the declaration — so a field left out gives a structure that
// declares its own wrong size precisely, and what comes back is the block
// Windows filled read at offsets nobody checked. It is the trap
// `NOTIFYICONDATAW` already sprang in this package, which is why the numbers
// below are written out rather than derived.
//
// 92 and 504 are the specification's, and the message font sits at 408: five
// `LOGFONTW` before it and three pairs of integers between them.
func TestTheNonClientMetricsHaveTheLayoutWindowsFills(t *testing.T) {
	if got := unsafe.Sizeof(logFontW{}); got != 92 {
		t.Errorf("LOGFONTW is %d bytes, and Windows writes 92", got)
	}
	var m nonClientMetricsW
	if got := unsafe.Sizeof(m); got != 504 {
		t.Errorf("NONCLIENTMETRICSW is %d bytes, and cbSize will declare that "+
			"number to a call that wants 504", got)
	}
	for _, f := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"cbSize", unsafe.Offsetof(m.Size), 0},
		{"lfCaptionFont", unsafe.Offsetof(m.CaptionFont), 24},
		{"lfMenuFont", unsafe.Offsetof(m.MenuFont), 224},
		{"lfStatusFont", unsafe.Offsetof(m.StatusFont), 316},
		{"lfMessageFont", unsafe.Offsetof(m.MessageFont), 408},
		{"iPaddedBorderWidth", unsafe.Offsetof(m.PaddedBorderWidth), 500},
	} {
		if f.got != f.want {
			t.Errorf("%s sits at %d, and Windows writes it at %d", f.name, f.got, f.want)
		}
	}
}

// **The face is the system's, the fallback is ours, and on this machine they
// are the same string.** An Italian Windows writes its messages in Segoe UI,
// which is exactly what the panel used to hard-code: a test that only checked
// the value would pass over a call that had never succeeded — the shape this
// package already records about a fallback answering the same number as the
// road it stands in for.
//
// So the decision is taken away from the system call and given the answer, and
// both directions are asked here.
//
// **Verified to catch**: with the refusal branch removed, the first case
// returns an empty face.
func TestTheFaceIsTakenFromWindowsAndTheFallbackIsOurs(t *testing.T) {
	var chinese logFontW
	copy(chinese.FaceName[:], windows.StringToUTF16("Microsoft YaHei UI"))
	chinese.CharSet = 134 // GB2312_CHARSET

	for _, c := range []struct {
		name     string
		lf       logFontW
		answered bool
		face     string
		charSet  byte
	}{
		{"the call was refused", chinese, false, "Segoe UI", 1},
		{"it answered with nothing", logFontW{}, true, "Segoe UI", 1},
		{"a system that writes in another script", chinese, true, "Microsoft YaHei UI", 134},
	} {
		face, charSet := messageFaceFrom(c.lf, c.answered)
		if face != c.face || charSet != c.charSet {
			t.Errorf("%s: face %q charset %d, wanted %q and %d",
				c.name, face, charSet, c.face, c.charSet)
		}
	}
}

// **And the call really answers here**, which is the half no table can assert:
// the two cases above are about what is done with the answer, and this is
// about there being one. Without it the panel could be running on the fallback
// for ever on every machine and nothing would say so, because the fallback is
// the right face on the machine it was written on.
//
// It asks the system, and it is allowed to: there is nothing to hand in, no
// disk is written, and what it would catch is the structure or the constant
// being wrong — which is the whole of this file.
func TestWindowsReallyAnswersWithAFace(t *testing.T) {
	f := &flyout{dpi: 96}
	face, charSet := f.messageFace()
	if face == "" {
		t.Fatal("no face at all: the panel would draw with whatever GDI picks")
	}
	t.Logf("this machine writes its messages in %q, charset %d", face, charSet)

	// The refusal and the answer are told apart by asking the call directly:
	// on a Windows that writes in Segoe UI the returned face cannot say which
	// road it came by.
	var m nonClientMetricsW
	m.Size = uint32(unsafe.Sizeof(m))
	ok, _, err := procSystemParametersInfoForDpi.Call(
		spiGetNonClientMetrics, uintptr(m.Size), uintptr(unsafe.Pointer(&m)), 0, 96)
	if ok == 0 {
		t.Fatalf("SystemParametersInfoForDpi refused the structure: %v — "+
			"the panel is on the fallback, and on this machine that is invisible", err)
	}
	if windows.UTF16ToString(m.MessageFont.FaceName[:]) != face {
		t.Error("the face read here and the face the panel takes are not the same: " +
			"one of the two is reading the block at the wrong offset")
	}
}
