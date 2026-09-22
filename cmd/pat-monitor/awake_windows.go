//go:build windows

package main

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"patmonitor/internal/version"
)

// The monitor asks Windows not to let the machine fall asleep.
//
// **It is not a setting.** A monitor that lets the computer go to sleep at the
// idle timeout is a monitor that stops at two in the morning without anybody
// having done anything wrong, and whoever is watching from a phone sees a
// frozen picture and cannot tell the child from the network from the machine.
// There is no reading of the configuration here for the same reason the
// packaged build does not ask GitHub: it is not a preference somebody might
// want the other way round. The program only runs while it is watching.
//
// **The display is deliberately not held on.** PowerRequestDisplayRequired
// exists and would light a bedroom all night; what has to stay awake is the
// system, not the screen.

const (
	// POWER_REQUEST_CONTEXT_VERSION, and it is zero rather than one: the
	// constant names a version of the structure, not a first version number.
	powerRequestContextVersion = 0
	// POWER_REQUEST_CONTEXT_SIMPLE_STRING: the reason is a plain string of
	// ours rather than a resource in a module Windows would localise.
	powerRequestContextSimpleString = 0x00000001
	// PowerRequestSystemRequired, the second of the PowerRequestType enum.
	powerRequestSystemRequired = 1
)

// reasonContext is REASON_CONTEXT. The third field is the union, of which only
// the simple-string arm is used; the two words before it leave the pointer
// exactly where the C union puts it, which awake_windows_test.go checks rather
// than trusts.
type reasonContext struct {
	Version uint32
	Flags   uint32
	Reason  *uint16
}

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procPowerCreateRequest = kernel32.NewProc("PowerCreateRequest")
	procPowerSetRequest    = kernel32.NewProc("PowerSetRequest")
	procPowerClearRequest  = kernel32.NewProc("PowerClearRequest")
)

// awake is a power request being held.
//
// **The reason string is kept here, and that is not tidiness.**
// PowerCreateRequest does not copy it: it keeps the pointer for as long as the
// handle lives, so a buffer built inside the call and left to the collector is
// memory Windows goes on reading. Somebody else's program shipped exactly that
// and it was found by a reader, not by a crash — which is what this kind of
// defect does, because the freed bytes usually still say what they said.
type awake struct {
	h      windows.Handle
	reason []uint16
	once   sync.Once
}

// awakeReason is what the machine's owner reads.
//
// **It is the half of the modern call that the old one cannot do**, and the
// reason for preferring it: `powercfg /requests` prints this sentence under our
// process, so whoever notices the computer no longer sleeping can ask Windows
// who is holding it and get an answer instead of a hunt.
// SetThreadExecutionState holds the machine awake anonymously.
func awakeReason() string { return version.Product + " is watching the room" }

// keepAwake holds the system awake until release is called.
func keepAwake(reason string) (*awake, error) {
	r, err := windows.UTF16FromString(reason)
	if err != nil {
		return nil, fmt.Errorf("the reason for staying awake: %w", err)
	}
	a := &awake{reason: r}
	ctx := reasonContext{
		Version: powerRequestContextVersion,
		Flags:   powerRequestContextSimpleString,
		Reason:  &a.reason[0],
	}
	// **The failure is INVALID_HANDLE_VALUE and not zero**, which is the trap
	// of this one call: read as "nil means it went wrong" it reports success
	// for every failure, and the machine then sleeps with the log saying it
	// will not.
	h, _, callErr := procPowerCreateRequest.Call(uintptr(unsafe.Pointer(&ctx)))
	if windows.Handle(h) == windows.InvalidHandle {
		return nil, fmt.Errorf("PowerCreateRequest: %w", callErr)
	}
	a.h = windows.Handle(h)
	if ok, _, callErr := procPowerSetRequest.Call(h, powerRequestSystemRequired); ok == 0 {
		windows.CloseHandle(a.h)
		return nil, fmt.Errorf("PowerSetRequest: %w", callErr)
	}
	return a, nil
}

// release gives the machine back.
//
// **It is nil-safe and happens once**, because both are real here: the caller
// holds whatever keepAwake returned including nothing, and the release sits in
// a defer that the end of the Windows session can reach from the other side.
// Clearing a request twice closes a handle twice, and the second close lands on
// whatever has been given that number since.
func (a *awake) release() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		procPowerClearRequest.Call(uintptr(a.h), powerRequestSystemRequired)
		windows.CloseHandle(a.h)
	})
}
