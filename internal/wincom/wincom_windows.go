//go:build windows

// Package wincom holds the readings of COM's answers that must not be made
// twice, starting with what CoInitializeEx returned.
//
// **A non-zero HRESULT is not automatically a fault**, and go-ole reports an
// error for every HRESULT that is not zero. Three outcomes come back and they
// want three different answers, which is why the reading is worth a package of
// its own: written in several places it drifts, and it drifts silently. The
// count is kept by TestNobodyReadsTheOutcomeOnTheirOwn, which reads the syntax
// tree rather than trusting a sentence written here.
//
// **The second reading is E_ACCESSDENIED**, and it is here for the same reason
// rather than out of any kinship with the first: video capture and audio
// capture meet that refusal through two unrelated calls, and a number spelled
// at both sites is a number that can be wrong at one of them. See ErrDenied.
package wincom

import (
	"errors"
	"fmt"
	"runtime"

	ole "github.com/go-ole/go-ole"

	"patmonitor/internal/guard"
)

// Apartment is the concurrency model asked for the thread.
//
// **The choice stays with the caller, and its reason with it.** The two paths
// make it differently — video in MTA, audio in STA — and why depends on what
// runs on top, not on this package: it is written where it is chosen. What is
// kept here is only the reading of the outcome.
type Apartment uint32

const (
	// MTA is the multithreaded apartment.
	MTA Apartment = ole.COINIT_MULTITHREADED
	// STA is the single-threaded apartment.
	STA Apartment = ole.COINIT_APARTMENTTHREADED
)

// String gives the apartment the name it is looked up by in the documentation.
func (a Apartment) String() string {
	switch a {
	case MTA:
		return "MTA"
	case STA:
		return "STA"
	default:
		return fmt.Sprintf("apartment %#x", uint32(a))
	}
}

// Sharing says what to do when the thread is **already** in an apartment other
// than the one asked for, that is, on the RPC_E_CHANGED_MODE outcome.
//
// **It is a parameter and not a fixed default because the right answer changes
// with who is asking, and getting it wrong costs in very different ways.**
// Calls do work inside somebody else's apartment — and nothing is balanced,
// because the reference is not ours — but "work" depends on what is put on top:
// runVideo wants MTA because asynchronous transforms deliver their events from
// an internal work queue, and in STA that queue would want a message loop that
// is not there. Carrying on there does not give an error, it gives **a silent
// hang with the camera on**.
//
// Hence which way to be wrong, and hence Required being the zero of the type:
// refusing costs a readable error and a restart with backoff, carrying on costs
// a hung monitor. Whoever has no reason to write Accepted is choosing well by
// omission.
type Sharing int

const (
	// Required: the apartment asked for is a requirement, and finding another
	// one is an error.
	Required Sharing = iota
	// Accepted: working inside somebody else's apartment is fine.
	Accepted
)

// ErrForeignApartment says the thread was already in an apartment other than
// the one asked for, and that the caller could not accept it.
var ErrForeignApartment = errors.New("wincom: the thread is already in a different apartment")

// Outcomes of CoInitializeEx that **are not faults**, and that go-ole reports
// as errors because it treats any non-zero HRESULT as one.
const (
	// sFalse: COM was already initialised on this thread, the same way. It is a
	// success, and it is balanced with CoUninitialize like any other.
	sFalse = 0x00000001
	// rpcChangedMode: the thread is already in a different apartment. Calls
	// work all the same, but **no** reference was added, so CoUninitialize must
	// not be called.
	rpcChangedMode = 0x80010106
)

// outcome is what to do after CoInitializeEx.
//
// **It is a pure function on purpose**: the decision this package exists to
// keep in one place is exactly this one, and without separating it from the
// thread and from COM there would be no way to test it.
type outcome int

const (
	// initialised: the reference is ours, balance it on the way out.
	initialised outcome = iota
	// foreign: the apartment is somebody else's, carry on without balancing.
	foreign
	// failed: a real fault.
	failed
)

// classify reads the outcome of CoInitializeEx.
func classify(err error) outcome {
	switch {
	case err == nil, Code(err) == sFalse:
		return initialised
	case Code(err) == rpcChangedMode:
		return foreign
	default:
		return failed
	}
}

// balances says whether the outcome added a reference of **ours**, that is,
// whether CoUninitialize is owed on the way out.
//
// **It is a one-line function and it has a name because it is the second way to
// get this wrong.** Balancing somebody else's apartment releases a reference we
// never took, and that mistake pairs with the first one so neatly that the two
// mask each other: the way not to have it is to have this decision be
// answerable on its own.
func (o outcome) balances() bool { return o == initialised }

// Init initialises COM on the current thread and returns what to call on the
// way out.
//
// The thread **must already be pinned** by the caller: COM state belongs to the
// thread and not to the goroutine, so without LockOSThread the Go runtime can
// move it between calls. This form is for whoever keeps a thread of their own
// for the whole life of a loop — Thread is the convenience for everyone else.
//
// The returned function is **never nil, not even alongside an error**: where
// there is nothing to balance it does nothing. That way callers write a defer
// and stop thinking about which case they are in — and the line has to stay
// true, because promising it and returning nil is a trap laid precisely for
// whoever trusts the documentation.
func Init(a Apartment, s Sharing) (release func(), err error) {
	nothing := func() {}

	e := ole.CoInitializeEx(0, uint32(a))
	o := classify(e)

	release = nothing
	if o.balances() {
		release = ole.CoUninitialize
	}

	switch {
	case o == failed:
		return nothing, fmt.Errorf("CoInitializeEx: %w", e)
	case o == foreign && s == Required:
		return nothing, fmt.Errorf("%w: wanted %s", ErrForeignApartment, a)
	default:
		return release, nil
	}
}

// Thread runs fn on a thread of its own, with COM initialised.
//
// The outcome that matters here is S_FALSE — "COM was already initialised on
// this thread", that is, success. Read as an error, **fn is not called at
// all**: whoever waits for an answer produced inside it waits forever, and for
// the talk-back that means an audio output that never opens while holding a
// lock everything else passes through.
//
// It is the mirror of weighing an encoder command by its effect rather than by
// its answer: here an answer that said yes gets read as a refusal.
func Thread(a Apartment, s Sharing, fn func() error) error {
	done := make(chan error, 1)
	// **An outcome is sent whatever happens, a panic included**, and this is
	// the one goroutine where swallowing one quietly would be worse than
	// letting it through: whoever called this is waiting on the channel with
	// **no timeout**, so a goroutine that dies without writing turns a crash
	// into a monitor hung with the camera on — and a hang is the harder of the
	// two to diagnose, because there is nothing to read.
	//
	// So the catching is done by the inner guard, whose error goes into the
	// send like any other; the outer one carries the rule that every goroutine
	// of the monitor is started this way.
	//
	// **And the send is deferred, which is what makes the promise true.**
	// Written as the last statement it holds only while nothing above it can
	// panic — and something can: the inner guard writes a log line before it
	// returns. A panic there would be caught by the outer guard, tidily, and
	// leave this channel empty for ever. A defer sends whatever the state of
	// the world is, so the caller is answered even when the answer is that
	// there is no answer.
	guard.Go(nil, "a COM thread", func() {
		outcome := errors.New("the COM thread ended without an outcome")
		defer func() { done <- outcome }()

		outcome = guard.Run(nil, "a COM thread", func() error {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			release, err := Init(a, s)
			defer release()
			if err != nil {
				return err
			}

			return fn()
		})
	})
	return <-done
}

// Code pulls the HRESULT out of a go-ole error. Zero if it is not one of theirs.
func Code(err error) uintptr {
	var oe *ole.OleError
	if errors.As(err, &oe) {
		return oe.Code()
	}
	return 0
}

// eAccessDenied is E_ACCESSDENIED, the HRESULT Windows answers with when a
// capability is switched off.
//
// **The number lives here once**, and the two capture paths ask rather than
// spell it: internal/mf meets it coming out of IMFActivate::ActivateObject and
// internal/audio coming out of IMMDevice::Activate, which are two calls into
// two different subsystems answering the same refusal. Written at both sites it
// would be one constant with two spellings, and the day one of them is wrong
// the symptom is a monitor that reports a broken camera instead of a revoked
// permission.
const eAccessDenied = 0x80070005

// ErrDenied is Windows refusing the device because permission is off.
//
// **It is a state, not a fault**, and that is the whole reason it exists as a
// value one can test for rather than as a sentence in a log. Camera and
// microphone are consented to and the consent can be withdrawn at any moment —
// from Settings, by whoever administers the machine, or by a Windows update
// resetting *"let desktop apps access your camera"* — so the refusal is
// something the monitor has to be able to say out loud and point at a remedy
// for. Everything downstream of it hangs off this: the alert code, the word in
// the catalogues, and the one command in the notification area that opens the
// page where the switch is.
//
// Whoever produces it wraps it, so the HRESULT and the call that met it stay in
// the message: the sentinel says what kind of refusal it is, not where it came
// from.
var ErrDenied = errors.New("Windows refused access to the device (E_ACCESSDENIED)")

// Denied reports whether an error is that refusal.
//
// It answers for both shapes, and it has to: the HRESULT as go-ole hands it
// over, and the wrapped sentinel, which is what travels once a capture path has
// turned the COM error into one of its own. A caller that tested only the first
// would stop recognising the refusal the moment somebody added an fmt.Errorf in
// between — that is, it would go green and blind on the same edit.
func Denied(err error) bool {
	return errors.Is(err, ErrDenied) || DeniedHRESULT(Code(err))
}

// DeniedHRESULT reports whether a bare return code is that refusal. It is for
// whoever holds the HRESULT itself and has not made an error out of it yet.
func DeniedHRESULT(code uintptr) bool { return uint32(code) == eAccessDenied }
