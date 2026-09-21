//go:build windows

package wincom

import (
	"errors"
	"fmt"
	"testing"

	ole "github.com/go-ole/go-ole"
)

// **The refusal has to be recognisable after somebody has wrapped it**, and
// that is the half a test on the HRESULT alone would miss.
//
// Between the call that meets E_ACCESSDENIED and the loop that decides what to
// say about it there are two or three `fmt.Errorf`, and go-ole's error survives
// them only as long as every one of those uses `%w` — which nothing enforces.
// Both capture paths therefore wrap the sentinel themselves, at the funnel, and
// what travels from there is a value: `Denied` has to answer for that shape as
// well as for the raw one.
//
// **Verified to catch**: with `errors.Is` taken out of `Denied`, the third case
// fails saying the refusal stopped being recognised — which is the monitor
// reporting a broken camera in place of a revoked permission.
func TestTheRefusalIsRecognisedInBothShapes(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"the HRESULT as go-ole hands it over", ole.NewError(0x80070005), true},
		{"the same one wrapped", fmt.Errorf("Activate: %w", ole.NewError(0x80070005)), true},
		{"the sentinel, which is what the capture paths produce", fmt.Errorf("camera open: %w", ErrDenied), true},
		{"another HRESULT", ole.NewError(0x80070057), false},
		{"an error that is not COM's", errors.New("boom"), false},
		{"no error at all", nil, false},
	} {
		if got := Denied(c.err); got != c.want {
			t.Errorf("%s: Denied = %v, wanted %v", c.name, got, c.want)
		}
	}
}

// And the bare code, which is what whoever holds the return value asks.
//
// **S_OK is in the list on purpose**: this repository has already paid once for
// an HRESULT read as a fault when two of its three values mean success, and a
// predicate that answered yes to zero would file every successful open as a
// refused permission.
func TestOnlyOneCodeIsTheRefusal(t *testing.T) {
	if !DeniedHRESULT(0x80070005) {
		t.Error("E_ACCESSDENIED is not recognised: it is the one value this reading exists for")
	}
	for _, code := range []uintptr{0, 1, 0x80070057, 0x88890027, 0xC00D3704} {
		if DeniedHRESULT(code) {
			t.Errorf("0x%08X reads as a refusal of permission", uint32(code))
		}
	}
}
