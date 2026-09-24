//go:build windows

package mf

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// A COM object implemented by us, because the hardware encoder delivers its
// events in no other way.
//
// The IMFMediaEventGenerator queue can be polled with GetEvent, and the
// documentation presents the two routes as equivalent. On this machine's Intel
// encoder they are not: GetEvent, blocking or otherwise, never returns
// anything, while the rest of the transform answers according to the contract —
// ProcessInput with MF_E_NOTACCEPTING and ProcessOutput with E_UNEXPECTED,
// which are the prescribed answers to whoever has not yet received an event.
// The events come out of BeginGetEvent only.
//
// Building a vtable by hand from Go is less exotic than it looks: a COM object
// is a pointer to a table of functions, and syscall.NewCallback turns a Go
// function into something a C caller knows how to invoke.
//
// The trap is not the vtable, it is lifetime: Media Foundation keeps our
// pointer between one call and the next, while Go's garbage collector has no
// reason to know that object is alive. Hence runtime.Pinner.

var iidIMFAsyncCallback = guid("{a27003cf-2354-4f2a-8d6a-ab7cff15437e}")

type asyncCallbackVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	GetParameters  uintptr
	Invoke         uintptr
}

// asyncCallback is the COM object. The first field has to be the pointer to the
// vtable: it is the only thing the caller knows about our structure.
type asyncCallback struct {
	vtbl   *asyncCallbackVtbl
	events *eventGenerator
	// ch receives the translated events. It is buffered because Invoke runs on
	// a Media Foundation thread and must not stop to wait for us.
	ch     chan Event
	pinner runtime.Pinner
	// state is the closed flag in bit 0 and the number of Invokes currently
	// inside the object in the bits above it. See enter and close.
	state atomic.Int64
	// invocations and lastErr make the worst case visible: Invoke being called
	// and us throwing away what it carries. Without these two, an event lost
	// and an event that never arrived look exactly the same.
	//
	// lastErr is written from a Media Foundation thread and read by Diagnose
	// from whichever thread asked, so it lives under mu with seen — an
	// interface is two words, and the reader of a torn one gets a type and
	// somebody else's pointer. invocations is atomic for the same reason and
	// was already.
	invocations atomic.Int64
	lastErr     error
	// seen records every event with its outcome. An event our code does not
	// recognise is to be looked at, not discarded: it is the place where the
	// transform says what it does not like.
	mu   sync.Mutex
	seen []string
}

func (cb *asyncCallback) record(s string) {
	cb.mu.Lock()
	if len(cb.seen) < 32 {
		cb.seen = append(cb.seen, s)
	}
	cb.mu.Unlock()
}

func (cb *asyncCallback) fail(err error) {
	cb.mu.Lock()
	cb.lastErr = err
	cb.mu.Unlock()
}

func (cb *asyncCallback) lastError() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.lastErr
}

// stateClosed is bit 0 of state; every Invoke inside the object counts two.
const stateClosed = 1

// enter says whether this Invoke may touch the event generator, and counts
// itself in until leave.
//
// **It exists because closing did not wait for anybody.** close() used to shut a
// channel and return, and the caller then released the event generator and the
// transform — while an Invoke on a Media Foundation thread was in the middle of
// calling EndGetEvent through the vtable of what was being freed. The window is
// small and it is walked constantly: the resolution scale rebuilds the encoder,
// and this file's own close() already states that at that moment "a request is
// nearly always in flight". That sentence was the argument for leaking the pin,
// and the Go object was the only thing it protected.
//
// **A counter rather than a lock, and that is the whole reason for the
// arithmetic.** The obvious shape is an RWMutex held for reading here and for
// writing in close. It is wrong here: Invoke re-arms the subscription before it
// returns, and were Media Foundation ever to deliver the next event on this same
// thread, from inside that call, the read side would be taken twice with a
// writer waiting — which in Go is a deadlock, that is, a hung monitor instead of
// a crashed one. A counter is reentrant by construction and costs an atomic.
func (cb *asyncCallback) enter() bool {
	for {
		v := cb.state.Load()
		if v&stateClosed != 0 {
			return false
		}
		if cb.state.CompareAndSwap(v, v+2) {
			return true
		}
	}
}

func (cb *asyncCallback) leave() { cb.state.Add(-2) }

func (cb *asyncCallback) log() []string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return append([]string(nil), cb.seen...)
}

var (
	asyncVtblOnce sync.Once
	asyncVtbl     asyncCallbackVtbl
)

func initAsyncVtbl() {
	asyncVtbl = asyncCallbackVtbl{
		QueryInterface: syscall.NewCallback(cbQueryInterface),
		AddRef:         syscall.NewCallback(cbAddRef),
		Release:        syscall.NewCallback(cbRelease),
		GetParameters:  syscall.NewCallback(cbGetParameters),
		Invoke:         syscall.NewCallback(cbInvoke),
	}
}

const (
	sOK          = 0
	eNoInterface = 0x80004002
	eNotImpl     = 0x80004001
	ePointer     = 0x80004003
)

// The parameters are unsafe.Pointer and not uintptr for a practical reason:
// they are pointers arriving from C, and declaring them as integers would force
// a conversion back, which go vet rightly flags as suspicious.
func cbQueryInterface(this, riid, ppv unsafe.Pointer) uintptr {
	if ppv == nil {
		return ePointer
	}
	out := (*unsafe.Pointer)(ppv)
	g := (*ole.GUID)(riid)
	if ole.IsEqualGUID(g, ole.IID_IUnknown) || ole.IsEqualGUID(g, iidIMFAsyncCallback) {
		*out = this
		return sOK
	}
	*out = nil
	return eNoInterface
}

// Reference counting is not needed: the object lives as long as the encoder
// that owns it, and what keeps it alive is the Go code, not COM. Plausible
// values are returned because the caller reads them.
func cbAddRef(this unsafe.Pointer) uintptr  { return 2 }
func cbRelease(this unsafe.Pointer) uintptr { return 1 }

// GetParameters may answer E_NOTIMPL: it means no preference about the work
// queue, that is, the default one.
func cbGetParameters(this, flags, queue unsafe.Pointer) uintptr { return eNotImpl }

func cbInvoke(this, result unsafe.Pointer) uintptr {
	cb := (*asyncCallback)(this)
	cb.invocations.Add(1)

	// **Nothing below may run once the encoder has been closed**, because from
	// that moment the event generator and the transform this reaches through
	// have been released. A request armed before the close still arrives here —
	// it cannot be cancelled — and the right answer to it is to do nothing. The
	// IMFAsyncResult is the caller's and it releases it when we return.
	if !cb.enter() {
		return sOK
	}
	defer cb.leave()

	ev, err := cb.events.endGetEvent(result)
	if err != nil {
		cb.fail(err)
	}
	if err == nil && ev != nil {
		t, err := ev.eventType()
		status := ev.status()
		ev.Release()
		if err != nil {
			cb.fail(err)
		}
		if err == nil {
			cb.record(fmt.Sprintf("%s (status 0x%08X)", eventName(t), status))
			select {
			case cb.ch <- translateEvent(t):
			default:
				// The queue is full: whoever consumes it has stopped, and
				// piling up here does not help them. Losing a notification is
				// better than blocking a Media Foundation thread.
			}
		}
	}

	// We go straight back to listening: the subscription is good for one event
	// only, and without this line the encoder's second request never arrives.
	// The silence that follows looks in every way like that of a broken
	// encoder.
	//
	// No check on the closed flag here: enter() passed, so close() has not
	// begun, and if it begins now it waits for this call to leave before
	// anything is released.
	_ = cb.events.beginGetEvent(this)
	return sOK
}

func translateEvent(t uint32) Event {
	switch t {
	case evNeedInput:
		return EventNeedInput
	case evHaveOutput:
		return EventHaveOutput
	default:
		return EventOther
	}
}

// newAsyncCallback starts listening for gen's events.
func newAsyncCallback(gen *eventGenerator) (*asyncCallback, error) {
	asyncVtblOnce.Do(initAsyncVtbl)

	cb := &asyncCallback{
		vtbl:   &asyncVtbl,
		events: gen,
		ch:     make(chan Event, 64),
	}
	// Media Foundation keeps this pointer and uses it from threads of its own:
	// the garbage collector must neither move it nor free it.
	cb.pinner.Pin(cb)
	cb.pinner.Pin(&asyncVtbl)

	if err := gen.beginGetEvent(unsafe.Pointer(cb)); err != nil {
		cb.pinner.Unpin()
		return nil, err
	}
	return cb, nil
}

// close stops the subscription and **waits for an Invoke already inside the
// object to come out**.
//
// Whoever calls it is about to release the event generator and the transform,
// and that wait is the only thing that makes the release safe: without it the
// two Release calls race a Media Foundation thread that is at that moment
// calling EndGetEvent through the vtable being freed.
//
// The wait is a spin, and it is bounded by one Invoke — an EndGetEvent, two
// reads off the event and a non-blocking send, that is, microseconds — while an
// encoder is closed at every step of the resolution scale and not more often
// than that. A parked wait would need a channel that the entering side has to
// signal on, which is a second piece of state for a wait nobody will ever
// observe.
//
// The pin is still deliberately leaked: a request armed before this call cannot
// be cancelled, so Media Foundation may invoke us again afterwards, and
// freeing the object while it holds the pointer means a process crash instead
// of an error. What has changed is that such a late Invoke now returns without
// touching anything — see enter. The memory is one small struct.
func (cb *asyncCallback) close() {
	for {
		v := cb.state.Load()
		if v&stateClosed != 0 {
			break
		}
		if cb.state.CompareAndSwap(v, v|stateClosed) {
			break
		}
	}
	for cb.state.Load() != stateClosed {
		runtime.Gosched()
	}
}

func (g *eventGenerator) beginGetEvent(callback unsafe.Pointer) error {
	r, _, _ := syscall.SyscallN(g.vtbl().BeginGetEvent,
		uintptr(unsafe.Pointer(g)), uintptr(callback), 0)
	return check("BeginGetEvent", r)
}

// queueEvent pushes an event into the transform's queue.
//
// It serves one question, but a decisive one: when an encoder does not speak,
// it tells "our COM object is wrong" from "the encoder has nothing to say". If
// the fake event comes back, the subscription works and the silence is the
// encoder's own.
func (g *eventGenerator) queueEvent(met uint32) error {
	r, _, _ := syscall.SyscallN(g.vtbl().QueueEvent,
		uintptr(unsafe.Pointer(g)), uintptr(met),
		uintptr(unsafe.Pointer(ole.IID_NULL)), 0, 0)
	return check("QueueEvent", r)
}

func (g *eventGenerator) endGetEvent(result unsafe.Pointer) (*mediaEvent, error) {
	var ev *mediaEvent
	r, _, _ := syscall.SyscallN(g.vtbl().EndGetEvent,
		uintptr(unsafe.Pointer(g)), uintptr(result), uintptr(unsafe.Pointer(&ev)))
	if err := check("EndGetEvent", r); err != nil {
		return nil, err
	}
	return ev, nil
}

// drain throws away the events left in the queue, and says how many there were.
//
// **It is needed after stopping and restarting the transform.** The channel is
// buffered because Invoke runs on a Media Foundation thread and must not wait
// for us; the consequence is that after a reconfiguration it still holds events
// from the **previous** session. Consuming one means believing we have
// permission to call ProcessOutput when there is no frame behind it any more —
// and the prescribed answer to whoever does that is E_UNEXPECTED.
//
// Measured on AMD: without the drain, the first reconfiguration of the bitrate
// makes ProcessOutput answer HRESULT 0x8000FFFF and the capture restart, in a
// loop. It is the same invariant as always — **a request from the asynchronous
// encoder is good once only** — crossed by a restart rather than by a loop.
func (c *asyncCallback) drain() int {
	n := 0
	for {
		select {
		case <-c.ch:
			n++
		default:
			return n
		}
	}
}
