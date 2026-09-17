//go:build windows

package wincom

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ole "github.com/go-ole/go-ole"
)

// The mistake this package exists to prevent is treating **every** non-zero
// HRESULT as a fault, when two of the three mean success. The test lives here
// and not on an actual COM open because what goes wrong is the reading, not the
// call: on a freshly pinned thread CoInitializeEx always answers S_OK, so a
// test that really opens COM **would let the wrong code through too**.
func TestTwoOfTheThreeOutcomesAreASuccess(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want outcome
	}{
		{"S_OK", nil, initialised},
		{"S_FALSE: it was already initialised here", ole.NewError(sFalse), initialised},
		{"RPC_E_CHANGED_MODE: somebody else's apartment", ole.NewError(rpcChangedMode), foreign},
		{"E_OUTOFMEMORY: a real fault", ole.NewError(0x8007000E), failed},
		{"an error that is not go-ole's", errors.New("boom"), failed},
	} {
		if got := classify(c.err); got != c.want {
			t.Errorf("%s: classify = %d, wanted %d", c.name, got, c.want)
		}
	}
}

// **Only the first of the two successes is balanced**, and this test opens COM
// for real because otherwise it would prove nothing.
//
// Calling classify with RPC_E_CHANGED_MODE would only repeat the test above
// under another name: putting ole.CoUninitialize back on the foreign-apartment
// branch — which is exactly the defect this test is named for — leaves that
// green. **A test that does not walk the path absolves the code that gets it
// wrong.**
//
// The path can be built: pin the thread, put it in STA, and from there every
// request for MTA finds an apartment that is not its own.
func TestOnlyOurOwnReferenceIsBalanced(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Somebody else's room, which this test pretends to be.
	if err := ole.CoInitializeEx(0, uint32(STA)); err != nil {
		t.Skipf("could not put the thread in STA: %v", err)
	}
	defer ole.CoUninitialize()

	release, err := Init(MTA, Accepted)
	if err != nil {
		t.Fatalf("with Accepted a foreign apartment is not an error: %v", err)
	}
	release()

	// Had release balanced, our host's reference would have gone to zero and
	// the apartment would be gone: a fresh CoInitializeEx would answer S_OK
	// instead of S_FALSE. That is the only way to observe one CoUninitialize
	// too many from the outside.
	again := ole.CoInitializeEx(0, uint32(STA))
	if Code(again) != sFalse {
		t.Fatalf("release let go of our host's apartment: the second CoInitializeEx gave %v, wanted S_FALSE", again)
	}
	ole.CoUninitialize() // hand back the reference the probe just added
}

// **Whoever needs that apartment has to be able to refuse**, and this is the
// half that a shared reading takes away without saying so. In STA the queue of
// Media Foundation's asynchronous transforms is left without the message loop
// it wants, and the result is not an error — it is a hang with the camera on.
func TestWhoNeedsTheApartmentCanRefuseIt(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, uint32(STA)); err != nil {
		t.Skipf("could not put the thread in STA: %v", err)
	}
	defer ole.CoUninitialize()

	release, err := Init(MTA, Required)
	if release == nil {
		t.Fatal("release is nil: the documentation promises it never is")
	}
	release()
	if !errors.Is(err, ErrForeignApartment) {
		t.Fatalf("Init(MTA, Required) in STA gave %v, wanted ErrForeignApartment", err)
	}
	if !strings.Contains(err.Error(), "MTA") {
		t.Errorf("the error does not say which apartment was needed: %q", err)
	}
}

// A real fault has to stay readable: the code that caused it is the only thing
// to trace back from, and %w keeps it reachable.
func TestARealFailureKeepsItsCode(t *testing.T) {
	err := fmt.Errorf("CoInitializeEx: %w", ole.NewError(0x8007000E))
	if got := Code(err); got != 0x8007000E {
		t.Errorf("Code = %#x, wanted 0x8007000E", got)
	}
	if got := Code(errors.New("boom")); got != 0 {
		t.Errorf("Code on a foreign error = %#x, wanted 0", got)
	}
}

// A sentence in a comment claiming how many places read this outcome notices
// nothing when a new one appears. This test reads the tree instead: whoever
// initialises COM on their own, or decides for themselves whether to balance,
// is doing it outside the one place that knows how to read those three
// outcomes.
//
// **It watches CoUninitialize too**, because the decision is a double one:
// which outcome is a success, and which of the two successes added a reference
// of ours.
func TestNobodyReadsTheOutcomeOnTheirOwn(t *testing.T) {
	banned := []string{"CoInitializeEx", "CoUninitialize"}

	var offenders []string
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "licenses", "bin", "wincom":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Comments name these calls on purpose, to explain the rule. What is
		// searched for is code, not prose.
		bare := withoutComments(string(src))
		for _, call := range banned {
			if strings.Contains(bare, call) {
				offenders = append(offenders, path+" -> "+call)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("COM is initialised in internal/wincom only, and instead:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// withoutComments strips line and block comments. It is not a Go parser and
// does not need to be: here it is enough not to mistake a sentence for a call.
func withoutComments(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			j := strings.IndexByte(src[i:], 10)
			if j < 0 {
				return b.String()
			}
			i += j
		case strings.HasPrefix(src[i:], "/*"):
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return b.String()
			}
			i += j + 4
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}
