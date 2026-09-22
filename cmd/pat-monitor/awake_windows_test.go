//go:build windows

package main

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"patmonitor/internal/version"
)

// **A structure handed to Windows is a layout, and Go will not check it.** The
// fields of REASON_CONTEXT are read by offset on the other side: a field
// inserted, reordered or widened here compiles, runs, and has Windows read the
// reason pointer out of the middle of two integers. Nothing complains, and what
// comes back is a request that was never made or a crash somewhere else.
//
// Eight is the offset on both word sizes, because a pointer follows two
// four-byte fields and aligns to itself.
func TestTheReasonContextHasTheLayoutWindowsReads(t *testing.T) {
	var c reasonContext
	for _, f := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Version", unsafe.Offsetof(c.Version), 0},
		{"Flags", unsafe.Offsetof(c.Flags), 4},
		{"Reason", unsafe.Offsetof(c.Reason), 8},
	} {
		if f.got != f.want {
			t.Errorf("%s sits at %d, and Windows reads it at %d", f.name, f.got, f.want)
		}
	}
	if size := unsafe.Sizeof(c); size != 8+unsafe.Sizeof(uintptr(0)) {
		t.Errorf("REASON_CONTEXT is %d bytes, and the two words plus a pointer are %d",
			size, 8+unsafe.Sizeof(uintptr(0)))
	}
}

// **The request is made and given back**, which is the one thing that cannot be
// asked of anything but the machine: the call is there, the flags are the ones
// it accepts, and the handle is the failure value or it is not.
//
// It asks the system, and it is allowed to: there is nothing to hand in as a
// parameter here, no disk is written, nothing outlives the test. What it would
// catch is a constant misread from the header, which is the mistake this whole
// file is one long transcription of.
func TestTheMachineCanBeHeldAwakeAndGivenBack(t *testing.T) {
	a, err := keepAwake("PAT Monitor test")
	if err != nil {
		t.Fatalf("the machine could not be held awake: %v", err)
	}
	defer a.release()

	if a.h == windows.InvalidHandle || a.h == 0 {
		t.Errorf("the request came back as handle %v, which is not a request", a.h)
	}

	// **The reason buffer is held by the request and not by the call that made
	// it.** Windows keeps the pointer for the life of the handle, so a buffer
	// left behind in keepAwake is memory somebody else owns being read by the
	// power service — and it reads as correct nearly every time, which is why
	// it is worth a test rather than a comment.
	//
	// **Verified to catch**: building the buffer as a local and dropping the
	// field, this fails.
	if len(a.reason) == 0 {
		t.Error("the request holds no reason string: Windows is left pointing at whatever freed it")
	} else if got := windows.UTF16PtrToString(&a.reason[0]); got != "PAT Monitor test" {
		t.Errorf("the reason Windows was given reads %q", got)
	}
}

// **Releasing twice would close somebody else's handle.** The second call to
// CloseHandle lands on whatever number Windows has handed out since, and the
// program then breaks somewhere with no connection to here. Both roads out of
// the monitor can reach the release — the deferred one and the end of the
// Windows session — so it is not a hypothesis.
//
// And nil is one of the things the caller holds, because a refused request is
// still something to defer.
func TestGivingTheMachineBackTwiceIsOnce(t *testing.T) {
	a, err := keepAwake("PAT Monitor test")
	if err != nil {
		t.Fatalf("the machine could not be held awake: %v", err)
	}
	a.release()
	a.release()

	var none *awake
	none.release()
}

// The sentence whoever asks Windows who is holding the machine awake will read.
// It has to name the program: `powercfg /requests` prints it under a process
// path, and a reason that does not say what is running says nothing the path
// did not already.
func TestTheReasonNamesTheProgram(t *testing.T) {
	if r := awakeReason(); !strings.Contains(r, version.Product) {
		t.Errorf("the reason reads %q, which does not name the program", r)
	}
}
