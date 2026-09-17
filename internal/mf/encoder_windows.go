//go:build windows

package mf

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// Messages sent to a transform.
const (
	msgCommandFlush         = 0x00000000
	msgCommandDrain         = 0x00000001
	msgSetD3DManager        = 0x00000002
	msgNotifyBeginStreaming = 0x10000000
	msgNotifyEndStreaming   = 0x10000001
	msgNotifyEndOfStream    = 0x10000002
	msgNotifyStartOfStream  = 0x10000003
)

// Events an asynchronous transform generates.
const (
	evNeedInput     = 601
	evHaveOutput    = 602
	evDrainComplete = 603
)

// MFTEnumEx flags.
const (
	enumFlagSyncMFT       = 0x00000001
	enumFlagAsyncMFT      = 0x00000002
	enumFlagHardware      = 0x00000004
	enumFlagSortAndFilter = 0x00000040
)

// Flags of a transform's output stream.
const (
	outputStreamProvidesSamples   = 0x00000100
	outputStreamCanProvideSamples = 0x00000080
)

// seqHeaderTries is how many frames the declared parameter sets are asked for
// before giving up. Measured here they are there on the first; the margin is
// for an encoder that fills the attribute a little later, and the ceiling so
// that one which never fills it stops costing a call per frame.
const seqHeaderTries = 32

// H.264 profiles as Media Foundation names them.
const (
	H264ProfileBase            = 66
	H264ProfileMain            = 77
	H264ProfileHigh            = 100
	H264ProfileConstrainedBase = 256
)

// registerTypeInfo describes a type/subtype pair for the enumeration.
type registerTypeInfo struct {
	MajorType ole.GUID
	Subtype   ole.GUID
}

// outputDataBuffer is MFT_OUTPUT_DATA_BUFFER.
type outputDataBuffer struct {
	StreamID uint32
	_        uint32 // 8-byte alignment before the pointer
	Sample   *Sample
	Status   uint32
	_        uint32
	Events   uintptr
}

// outputStreamInfo is MFT_OUTPUT_STREAM_INFO.
type outputStreamInfo struct {
	Flags     uint32
	Size      uint32
	Alignment uint32
}

// ---------- IMFTransform ----------

type transform struct {
	ole.IUnknown
}

type transformVtbl struct {
	ole.IUnknownVtbl
	GetStreamLimits           uintptr
	GetStreamCount            uintptr
	GetStreamIDs              uintptr
	GetInputStreamInfo        uintptr
	GetOutputStreamInfo       uintptr
	GetAttributes             uintptr
	GetInputStreamAttributes  uintptr
	GetOutputStreamAttributes uintptr
	DeleteInputStream         uintptr
	AddInputStreams           uintptr
	// The order of these four is not interleaved as one would think: the two
	// enumerations come first, then the two assignments. Swapping them costs
	// dearly, because SetOutputType falls at the same index in both
	// arrangements and appears to work, while SetInputType lands on
	// GetOutputAvailableType and answers E_POINTER, which suggests a bad
	// pointer rather than a bad slot.
	GetInputAvailableType  uintptr
	GetOutputAvailableType uintptr
	SetInputType           uintptr
	SetOutputType          uintptr
	GetInputCurrentType    uintptr
	GetOutputCurrentType   uintptr
	GetInputStatus         uintptr
	GetOutputStatus        uintptr
	SetOutputBounds        uintptr
	ProcessEvent           uintptr
	ProcessMessage         uintptr
	ProcessInput           uintptr
	ProcessOutput          uintptr
}

func (t *transform) vtbl() *transformVtbl {
	return (*transformVtbl)(unsafe.Pointer(t.RawVTable))
}

func (t *transform) attributes() (*Attributes, error) {
	var a *Attributes
	r, _, _ := syscall.SyscallN(t.vtbl().GetAttributes,
		uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(&a)))
	if err := check("GetAttributes", r); err != nil {
		return nil, err
	}
	return a, nil
}

func (t *transform) setOutputType(mt *MediaType) error {
	r, _, _ := syscall.SyscallN(t.vtbl().SetOutputType,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(mt)), 0)
	return check("SetOutputType", r)
}

func (t *transform) setInputType(mt *MediaType) error {
	r, _, _ := syscall.SyscallN(t.vtbl().SetInputType,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(mt)), 0)
	return check("SetInputType", r)
}

func (t *transform) processMessage(msg uint32, param uintptr) error {
	r, _, _ := syscall.SyscallN(t.vtbl().ProcessMessage,
		uintptr(unsafe.Pointer(t)), uintptr(msg), param)
	return check("ProcessMessage", r)
}

func (t *transform) processInput(s *Sample) error {
	// MF_E_NOTACCEPTING
	const notAccepting = 0xC00D36B5
	r, _, _ := syscall.SyscallN(t.vtbl().ProcessInput,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(s)), 0)
	if uint32(r) == notAccepting {
		return ErrNotAccepting
	}
	return check("ProcessInput", r)
}

// availableInputTypes lists the formats the transform declares it accepts as
// input.
//
// It is for diagnosis: when SetInputType refuses, the difference between "this
// format does not suit me" and "I am calling the wrong method" only shows up by
// looking at whether the enumeration answers sensibly.
func (t *transform) availableInputTypes() []string {
	var out []string
	for i := uint32(0); i < 32; i++ {
		var mt *MediaType
		r, _, _ := syscall.SyscallN(t.vtbl().GetInputAvailableType,
			uintptr(unsafe.Pointer(t)), 0, uintptr(i), uintptr(unsafe.Pointer(&mt)))
		if hresult(r).failed() {
			if i == 0 {
				out = append(out, fmt.Sprintf("enumeration failed: HRESULT 0x%08X", uint32(r)))
			}
			break
		}
		if g, err := mt.Subtype(); err == nil {
			out = append(out, SubtypeName(g))
		}
		mt.Release()
	}
	if len(out) == 0 {
		out = append(out, "nothing")
	}
	return out
}

// inputTypeFor returns the input type declared by the transform that matches
// the requested subtype. The caller completes it and releases it.
func (t *transform) inputTypeFor(subtype *ole.GUID) (*MediaType, error) {
	for i := uint32(0); i < 32; i++ {
		var mt *MediaType
		r, _, _ := syscall.SyscallN(t.vtbl().GetInputAvailableType,
			uintptr(unsafe.Pointer(t)), 0, uintptr(i), uintptr(unsafe.Pointer(&mt)))
		if hresult(r).failed() {
			break
		}
		g, err := mt.Subtype()
		if err == nil && ole.IsEqualGUID(&g, subtype) {
			return mt, nil
		}
		mt.Release()
	}
	return nil, fmt.Errorf("the encoder does not accept %s as input", SubtypeName(*subtype))
}

// outputTypeFor returns the output type declared by the transform that matches
// the requested subtype. The caller completes it and releases it.
//
// The same rule as for the input applies, and for the same reason: what the
// encoder enumerates carries attributes we do not know we are supposed to give
// it. Building one by hand with the same fields is not equivalent.
func (t *transform) outputTypeFor(subtype *ole.GUID) (*MediaType, error) {
	for i := uint32(0); i < 32; i++ {
		var mt *MediaType
		r, _, _ := syscall.SyscallN(t.vtbl().GetOutputAvailableType,
			uintptr(unsafe.Pointer(t)), 0, uintptr(i), uintptr(unsafe.Pointer(&mt)))
		if hresult(r).failed() {
			break
		}
		g, err := mt.Subtype()
		if err == nil && ole.IsEqualGUID(&g, subtype) {
			return mt, nil
		}
		mt.Release()
	}
	return nil, fmt.Errorf("the encoder declares no %s output", SubtypeName(*subtype))
}

func (t *transform) inputStatus() (uint32, error) {
	var flags uint32
	r, _, _ := syscall.SyscallN(t.vtbl().GetInputStatus,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(&flags)))
	return flags, check("GetInputStatus", r)
}

func (t *transform) outputStatus() (uint32, error) {
	var flags uint32
	r, _, _ := syscall.SyscallN(t.vtbl().GetOutputStatus,
		uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(&flags)))
	return flags, check("GetOutputStatus", r)
}

func (t *transform) outputStreamInfo() (outputStreamInfo, error) {
	var info outputStreamInfo
	r, _, _ := syscall.SyscallN(t.vtbl().GetOutputStreamInfo,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(&info)))
	return info, check("GetOutputStreamInfo", r)
}

func (t *transform) outputCurrentType() (*MediaType, error) {
	var mt *MediaType
	r, _, _ := syscall.SyscallN(t.vtbl().GetOutputCurrentType,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(&mt)))
	if err := check("GetOutputCurrentType", r); err != nil {
		return nil, err
	}
	return mt, nil
}

func (t *transform) inputCurrentType() (*MediaType, error) {
	var mt *MediaType
	r, _, _ := syscall.SyscallN(t.vtbl().GetInputCurrentType,
		uintptr(unsafe.Pointer(t)), 0, uintptr(unsafe.Pointer(&mt)))
	if err := check("GetInputCurrentType", r); err != nil {
		return nil, err
	}
	return mt, nil
}

// describeType sums up a format for the report. It is there to read back what
// the transform really adopted instead of trusting that SetInputType did not
// complain.
func describeType(mt *MediaType, err error) string {
	if err != nil {
		return err.Error()
	}
	defer mt.Release()
	var b strings.Builder
	if g, e := mt.Subtype(); e == nil {
		b.WriteString(SubtypeName(g))
	}
	if w, h, e := mt.FrameSize(); e == nil {
		fmt.Fprintf(&b, " %dx%d", w, h)
	}
	if n, d, e := mt.FrameRate(); e == nil && d > 0 {
		fmt.Fprintf(&b, " @%.2f fps", float64(n)/float64(d))
	}
	if bps, e := mt.GetUINT32(mfMTAvgBitrate); e == nil {
		fmt.Fprintf(&b, " %d kbit/s", bps/1000)
	}
	return b.String()
}

// ---------- IMFMediaEventGenerator ----------

var iidIMFMediaEventGenerator = guid("{2cd0bd52-bcd5-4b89-b62c-eadc0c031e7d}")

type eventGenerator struct {
	ole.IUnknown
}

type eventGeneratorVtbl struct {
	ole.IUnknownVtbl
	GetEvent      uintptr
	BeginGetEvent uintptr
	EndGetEvent   uintptr
	QueueEvent    uintptr
}

func (g *eventGenerator) vtbl() *eventGeneratorVtbl {
	return (*eventGeneratorVtbl)(unsafe.Pointer(g.RawVTable))
}

type mediaEvent struct {
	Attributes
}

type mediaEventVtbl struct {
	attributesVtbl
	GetType         uintptr
	GetExtendedType uintptr
	GetStatus       uintptr
	GetValue        uintptr
}

func (e *mediaEvent) eventType() (uint32, error) {
	v := (*mediaEventVtbl)(unsafe.Pointer(e.RawVTable))
	var t uint32
	r, _, _ := syscall.SyscallN(v.GetType, uintptr(unsafe.Pointer(e)), uintptr(unsafe.Pointer(&t)))
	return t, check("GetType", r)
}

// status is the outcome the event carries with it.
//
// An event we do not recognise is not noise: if the transform has something to
// say about what went wrong, this is where it says it.
func (e *mediaEvent) status() uint32 {
	v := (*mediaEventVtbl)(unsafe.Pointer(e.RawVTable))
	var hr uint32
	syscall.SyscallN(v.GetStatus, uintptr(unsafe.Pointer(e)), uintptr(unsafe.Pointer(&hr)))
	return hr
}

// eventName gives a name to the codes we expect to see.
func eventName(t uint32) string {
	switch t {
	case 1:
		return "MEError"
	case evNeedInput:
		return "METransformNeedInput"
	case evHaveOutput:
		return "METransformHaveOutput"
	case evDrainComplete:
		return "METransformDrainComplete"
	case 604:
		return "METransformMarker"
	case 605:
		return "METransformInputStreamStateChanged"
	default:
		return fmt.Sprintf("event %d", t)
	}
}

// mfEventFlagNoWait asks not to block when the queue is empty.
const mfEventFlagNoWait = 0x00000001

// mfENoEventsAvailable is the answer to an empty queue polled without waiting.
const mfENoEventsAvailable = 0xC00D3E80

// waitEvent waits for the next event, blocking until it arrives.
//
// It is the way the documentation prescribes for driving an asynchronous
// transform. It has no deadline: the caller has to have a way of unblocking it.
func (g *eventGenerator) waitEvent() (*mediaEvent, error) {
	var ev *mediaEvent
	r, _, _ := syscall.SyscallN(g.vtbl().GetEvent,
		uintptr(unsafe.Pointer(g)), 0, uintptr(unsafe.Pointer(&ev)))
	if err := check("GetEvent", r); err != nil {
		return nil, err
	}
	return ev, nil
}

// nextEvent collects an event if there is one, without blocking.
//
// The blocking version would be simpler, but it turns any hitch into a stalled
// program that says nothing. Polling without waiting, the caller can give
// itself a deadline and report what did not arrive.
func (g *eventGenerator) nextEvent() (*mediaEvent, error) {
	var ev *mediaEvent
	r, _, _ := syscall.SyscallN(g.vtbl().GetEvent,
		uintptr(unsafe.Pointer(g)), mfEventFlagNoWait, uintptr(unsafe.Pointer(&ev)))
	if uint32(r) == mfENoEventsAvailable {
		return nil, nil
	}
	if err := check("GetEvent", r); err != nil {
		return nil, err
	}
	return ev, nil
}

// ---------- H.264 encoder ----------

// EncoderInfo describes a transform found in the system.
type EncoderInfo struct {
	Name  string
	Async bool
	act   *activate
}

// ListH264Encoders lists the hardware H.264 encoders.
//
// The system is asked instead of trying to encode: the GPU declares directly
// what it can do, and probing it with a test encode would cost seconds at
// start-up to learn something already written in the MFT registry.
func ListH264Encoders() ([]EncoderInfo, error) {
	category := *mftCategoryVideoEnc
	outputType := registerTypeInfo{MajorType: *MFMediaTypeVideo, Subtype: *MFVideoFormatH264}

	var arr **activate
	var count uint32
	r, _, _ := procMFTEnumEx.Call(
		uintptr(unsafe.Pointer(&category)),
		uintptr(enumFlagHardware|enumFlagAsyncMFT|enumFlagSyncMFT|enumFlagSortAndFilter),
		0, // any input type
		uintptr(unsafe.Pointer(&outputType)),
		uintptr(unsafe.Pointer(&arr)),
		uintptr(unsafe.Pointer(&count)))
	if err := check("MFTEnumEx", r); err != nil {
		return nil, err
	}
	if arr == nil || count == 0 {
		return nil, nil
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(arr)))

	list := unsafe.Slice(arr, count)
	out := make([]EncoderInfo, 0, count)
	for _, act := range list {
		if act == nil {
			continue
		}
		name, _ := act.GetAllocatedString(mftFriendlyNameAttribute)
		async, _ := act.GetUINT32(mfTransformAsync)
		out = append(out, EncoderInfo{Name: name, Async: async == 1, act: act})
	}
	return out, nil
}

// ReleaseEncoders frees the proxies returned by ListH264Encoders.
func ReleaseEncoders(list []EncoderInfo) {
	for _, e := range list {
		if e.act != nil {
			e.act.Release()
		}
	}
}

// VideoEncoderConfig describes what is wanted on the output.
type VideoEncoderConfig struct {
	Width, Height int
	FPS           int
	BitrateKbps   int
	// Profile is one of the H264Profile* values.
	Profile uint32
	// InputSubtype is the format of the incoming frames; nil means NV12.
	InputSubtype *ole.GUID
	// NameFilter, when set, narrows the choice to encoders whose name contains
	// it. It is for diagnosis, to compare two encoders on the same machine.
	NameFilter string
	// Poll drives asynchronous transforms by polling too, without listening to
	// their events. It is there to work out whether an encoder's silence is
	// about the event queue or about the transform itself.
	Poll bool
	// Device is the Direct3D device to hand to the transform. Hardware encoders
	// do not work without one; software ones ignore it, answering E_NOTIMPL.
	//
	// The caller owns it, because it has to be shared with the camera: if the
	// two had different devices the textures produced by one would be useless
	// to the other.
	Device *D3DDevice
	// Skip discards the first usable candidates. The system lists the same
	// encoder more than once and the duplicates are not necessarily equivalent:
	// it is there to try the second when the first configures but does not
	// work.
	Skip int
	// Block waits for events by blocking instead of polling the queue.
	Block bool
	// GOPFrames is the distance between keyframes. Zero leaves the choice to
	// the encoder.
	GOPFrames int
	// RateControl is the criterion for spending bits; empty means CBR.
	RateControl RateControl
	// Quality is the level to hold in RateQuality, from 1 to 100. Zero uses
	// DefaultQuality.
	Quality int
	// MinQP is the quantiser floor in RateCapped, from 1 to 51. Zero uses
	// DefaultMinQP.
	//
	// Higher means spending less and settling sooner; lower means chasing a
	// sharpness that beyond a certain point is sensor noise. It is not the same
	// scale as Quality and must not be confused with it: here the number **is**
	// the quantiser, there it is a level from 0 to 100 that the encoder
	// converts on its own.
	MinQP int
	// MaxQP is the quantiser ceiling: beyond it the encoder must not degrade
	// further. Zero leaves it free.
	//
	// **It serves the opposite purpose to MinQP**: not preventing waste, but
	// permitting degradation. An encoder that refuses to approximate enough
	// overshoots the bitrate instead of coming back inside it, and on WebRTC an
	// overshot bitrate is paid for in lost packets. Documented as **static**:
	// set before starting the session, not while it runs.
	MaxQP int
	// QualityVsSpeed goes from 1 to 100. Zero leaves the default.
	QualityVsSpeed int
}

// VideoEncoder wraps the transform and its event protocol.
//
// Hardware encoders are asynchronous transforms: you do not call them, you
// listen to them. They ask for a frame when they are ready to receive one and
// give notice when they have produced one, and the order of those two is not
// alternating. It is the most awkward point of Media Foundation, and it is also
// what saves us from having to guess when to push.
type VideoEncoder struct {
	t            *transform
	events       *eventGenerator
	cb           *asyncCallback
	codec        *codecAPI
	codecNotes   []string
	keyFrameNote string
	bitrateNote  string
	provides     bool
	outSize      uint32
	// seqHeader is the parameter sets as the encoder declares them, taken after
	// the first frame. See readSequenceHeader.
	seqTries  atomic.Int32
	seqHeader atomic.Pointer[[]byte]
	async     bool
	poll      bool
	block     bool
	dev       *D3DDevice
	// d3dResult keeps how the transform took the Direct3D device: it is the
	// kind of answer one discards out of habit and then misses exactly when it
	// is needed.
	d3dResult error
	// cfg is kept because the encoder can ask to renegotiate the output while
	// it works, and at that moment we need to know what was asked of it.
	cfg VideoEncoderConfig
	// lastQP and qpSeen hold the last frame's quantiser. They are separate
	// because "it does not declare it" and "it declares zero" are two different
	// things, and confusing them would give a mute encoder that looks perfect.
	lastQP atomic.Uint32
	qpSeen atomic.Bool
	// The inventory of the first output sample's attributes, for diagnosis: it
	// says what the encoder really delivers instead of leaving what is missing
	// to be deduced.
	attrSeen    atomic.Bool
	attrError   atomic.Value
	sampleAttrs atomic.Pointer[[]ole.GUID]
	Name        string
}

// NewVideoEncoder picks the first hardware encoder and configures it.
func NewVideoEncoder(cfg VideoEncoderConfig) (*VideoEncoder, error) {
	list, err := ListH264Encoders()
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no hardware H.264 encoder in the system")
	}
	defer ReleaseEncoders(list)

	// Candidates are tried one after another instead of trusting the first: the
	// system lists more than one under the same name, and not all of them agree
	// to work in system memory. Here the declaration is not enough and it has
	// to be tried: the list says what exists, not which instance will accept
	// our configuration.
	var errs []string
	skip := cfg.Skip
	for _, cand := range list {
		if cfg.NameFilter != "" && !strings.Contains(cand.Name, cfg.NameFilter) {
			continue
		}
		obj, err := cand.act.activateObject(iidIMFTransform)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: activation: %v", cand.Name, err))
			continue
		}
		t := (*transform)(unsafe.Pointer(obj))
		enc := &VideoEncoder{t: t, Name: cand.Name, async: cand.Async, poll: cfg.Poll, block: cfg.Block, dev: cfg.Device}
		if err := enc.configure(cfg); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cand.Name, err))
			// **The whole encoder is closed, not the transform alone.**
			// configure walks a fixed order and can fail at any point along it,
			// and by the last two steps — starting the stream, asking for the
			// output stream info — it has already taken an ICodecAPI, a second
			// reference through IMFMediaEventGenerator, and armed the async
			// callback. Releasing t alone left all three, and the worst of them is
			// the callback: that extra reference keeps the transform alive, so a
			// candidate we have just rejected goes on being handed events for the
			// life of the process with nobody reading them. Close does what the
			// skip branch below already did.
			enc.Close()
			continue
		}
		if skip > 0 {
			skip--
			enc.Close()
			continue
		}
		return enc, nil
	}
	return nil, fmt.Errorf("no usable encoder:\n  %s", strings.Join(errs, "\n  "))
}

// configure takes the transform from the state the enumeration hands it over in
// to the one in which it works.
//
// The order is what it is, and every step is there for a measured reason: the
// unlock because otherwise nearly every method refuses; the device before the
// formats because it changes which formats are offered; the subscription before
// the start because the first events cannot be recovered; the formats before
// the start because starting without them answers E_FAIL.
func (e *VideoEncoder) configure(cfg VideoEncoderConfig) error {
	e.cfg = cfg
	if e.async {
		if err := e.unlock(); err != nil {
			return err
		}
	}
	e.attachDevice()
	if err := e.negotiate(cfg); err != nil {
		return err
	}
	if err := e.applyCodecSettings(cfg); err != nil {
		return err
	}
	if e.async {
		if err := e.subscribe(); err != nil {
			return err
		}
	}
	if err := e.startStreaming(); err != nil {
		return err
	}

	info, err := e.t.outputStreamInfo()
	if err != nil {
		return err
	}
	e.provides = info.Flags&(outputStreamProvidesSamples|outputStreamCanProvideSamples) != 0
	e.outSize = info.Size
	return nil
}

// readSequenceHeader takes the SPS and the PPS from the output media type.
//
// **It is read after the first frame and not when the encoder is built**, and
// that is a measurement and not a preference: on Quick Sync the attribute is
// **absent** at build time and 48 bytes long by the first frame. Read where the
// obvious place is, this reserve would have been permanently empty on the one
// machine available to test it — which is the worst way for a fallback to fail,
// because everything else keeps working.
//
// It is taken **once** per encoder, and the scale rebuilds the encoder rather
// than reconfiguring it, so a change of size comes back through here. That
// matters because this file documents the output type as a liar: reconfigured
// while running it answers with the size that was asked for and not the one
// being produced.
//
// An absence is not a fault. Every encoder seen here puts the parameter sets in
// the stream, where they are authoritative; this is the reserve for one that
// does not, and there the alternative is no quantiser at all.
func (e *VideoEncoder) readSequenceHeader() []byte {
	mt, err := e.t.outputCurrentType()
	if err != nil {
		return nil
	}
	defer mt.Release()
	b, err := mt.GetBlob(mfMTMPEGSequenceHeader)
	if err != nil {
		return nil
	}
	return b
}

// SequenceHeader is the SPS and PPS the encoder declares, in Annex-B form, or
// nil if it declares none or has not produced a frame yet.
func (e *VideoEncoder) SequenceHeader() []byte {
	if p := e.seqHeader.Load(); p != nil {
		return *p
	}
	return nil
}

// unlock declares that we know the event protocol. Until it is done, nearly
// every method answers MF_E_TRANSFORM_ASYNC_LOCKED.
func (e *VideoEncoder) unlock() error {
	attrs, err := e.t.attributes()
	if err != nil {
		return err
	}
	err = attrs.SetUINT32(mfTransformAsyncUnlock, 1)
	// Low latency is declared here too, on the transform's attributes, and not
	// only through ICodecAPI: they are two distinct switches that some encoders
	// read from different places.
	_ = attrs.SetUINT32(mfLowLatency, 1)
	attrs.Release()
	if err != nil {
		return fmt.Errorf("unlocking the async transform: %w", err)
	}
	return nil
}

// attachDevice hands over the Direct3D device. Software encoders answer
// E_NOTIMPL, which here is not a problem.
func (e *VideoEncoder) attachDevice() {
	if e.dev != nil {
		e.d3dResult = e.t.processMessage(msgSetD3DManager, uintptr(unsafe.Pointer(e.dev.manager)))
	}
}

// subscribe opens the listening for events. It has to be done before telling
// the transform to begin, or the first events are lost.
func (e *VideoEncoder) subscribe() error {
	gen, err := e.t.QueryInterface(iidIMFMediaEventGenerator)
	if err != nil {
		return fmt.Errorf("IMFMediaEventGenerator: %w", err)
	}
	e.events = (*eventGenerator)(unsafe.Pointer(gen))
	if e.poll || e.block {
		return nil
	}
	if e.cb, err = newAsyncCallback(e.events); err != nil {
		return fmt.Errorf("event subscription: %w", err)
	}
	return nil
}

func (e *VideoEncoder) startStreaming() error {
	// The initial flush has nothing to flush. Chromium sends it too, and it is
	// the way to bring back to a known state a transform we do not know how it
	// was left in.
	if e.async {
		_ = e.t.processMessage(msgCommandFlush, 0)
	}
	if err := e.t.processMessage(msgNotifyBeginStreaming, 0); err != nil {
		return err
	}
	return e.t.processMessage(msgNotifyStartOfStream, 0)
}

func (e *VideoEncoder) negotiate(cfg VideoEncoderConfig) error {
	// The order matters: output first, then input. The other way round the
	// encoder refuses the input format, because it does not yet know what it
	// has to produce.
	if err := e.negotiateOutput(cfg); err != nil {
		return err
	}
	return e.negotiateInput(cfg)
}

func (e *VideoEncoder) negotiateOutput(cfg VideoEncoderConfig) error {
	out, err := e.t.outputTypeFor(MFVideoFormatH264)
	if err != nil {
		return err
	}
	defer out.Release()

	if err := out.SetUINT32(mfMTAvgBitrate, uint32(cfg.BitrateKbps*1000)); err != nil {
		return err
	}
	if err := out.SetFrameSize(cfg.Width, cfg.Height); err != nil {
		return err
	}
	if err := out.SetFrameRate(cfg.FPS, 1); err != nil {
		return err
	}
	if err := out.SetUINT32(mfMTInterlaceMode, interlaceModeProgressive); err != nil {
		return err
	}
	if err := out.SetUINT64(mfMTPixelAspectRatio, pack2x32(1, 1)); err != nil {
		return err
	}
	if cfg.Profile != 0 {
		if err := out.SetUINT32(mfMTMPEG2Profile, cfg.Profile); err != nil {
			return err
		}
	}
	return e.t.setOutputType(out)
}

func (e *VideoEncoder) negotiateInput(cfg VideoEncoderConfig) error {
	// The input format is not built from scratch: it starts from one of those
	// the encoder declares it accepts, and size and cadence are added to it. A
	// type made by hand is refused even when it has the same attributes, and
	// the error received, E_POINTER, gives no hint of that.
	subtype := cfg.InputSubtype
	if subtype == nil {
		subtype = MFVideoFormatNV12
	}
	in, err := e.t.inputTypeFor(subtype)
	if err != nil {
		return fmt.Errorf("%w; the encoder declares it accepts: %s",
			err, strings.Join(e.t.availableInputTypes(), ", "))
	}
	defer in.Release()

	if err := in.SetFrameSize(cfg.Width, cfg.Height); err != nil {
		return err
	}
	if err := in.SetFrameRate(cfg.FPS, 1); err != nil {
		return err
	}
	if err := in.SetUINT32(mfMTInterlaceMode, interlaceModeProgressive); err != nil {
		return err
	}
	if err := in.SetUINT64(mfMTPixelAspectRatio, pack2x32(1, 1)); err != nil {
		return err
	}
	if err := e.t.setInputType(in); err != nil {
		return fmt.Errorf("input format refused: %w", err)
	}
	return nil
}

func (e *VideoEncoder) Close() {
	if e.cb != nil {
		e.cb.close()
		e.cb = nil
	}
	if e.codec != nil {
		e.codec.Release()
		e.codec = nil
	}
	if e.events != nil {
		e.events.Release()
		e.events = nil
	}
	if e.t != nil {
		_ = e.t.processMessage(msgNotifyEndOfStream, 0)
		_ = e.t.processMessage(msgNotifyEndStreaming, 0)
		e.t.Release()
		e.t = nil
	}
	// The Direct3D device is not released here: it belongs to whoever gave it
	// to us, and it is shared with the camera.
	e.dev = nil
}

// Async says whether this transform is driven by events.
//
// It matters to whoever governs it: on a synchronous one the output is
// collected after every frame fed in, on an asynchronous one only when
// METransformHaveOutput arrives, and asking earlier answers E_UNEXPECTED.
func (e *VideoEncoder) Async() bool { return e.async && !e.poll }

// UsesDevice says whether the transform accepted the Direct3D device, that is,
// whether the encoding is happening on the GPU.
//
// Having a device is not enough: software encoders answer E_NOTIMPL to the
// message and go on working on the CPU. The difference has to be reported to
// whoever is looking at the status page, otherwise the monitor declares
// "hardware" even when it is not.
func (e *VideoEncoder) UsesDevice() bool { return e.dev != nil && e.d3dResult == nil }

// Diagnose reports what the transform declares about itself after
// configuration.
//
// It is needed when an encoder configures without complaining and then does not
// work: the silence does not say whether it stayed locked, whether it never got
// the unlock, or whether it is simply waiting for something. These three
// answers tell them apart.
func (e *VideoEncoder) Diagnose() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  asynchronous       %v\n", e.async)
	fmt.Fprintf(&b, "  Direct3D           %v (SET_D3D_MANAGER: %v)\n", e.dev != nil, e.d3dResult)

	if attrs, err := e.t.attributes(); err == nil {
		unlock, errU := attrs.GetUINT32(mfTransformAsyncUnlock)
		async, errA := attrs.GetUINT32(mfTransformAsync)
		attrs.Release()
		fmt.Fprintf(&b, "  MF_TRANSFORM_ASYNC %v (%v)\n", async, errA)
		fmt.Fprintf(&b, "  unlock read back   %v (%v)\n", unlock, errU)
	} else {
		fmt.Fprintf(&b, "  attributes         %v\n", err)
	}

	// MFT_INPUT_STATUS_ACCEPT_DATA is 1. On an asynchronous transform the
	// expected answer is zero until METransformNeedInput arrives: if there is a
	// 1 here the problem is the event queue, not the transform.
	if st, err := e.t.inputStatus(); err == nil {
		fmt.Fprintf(&b, "  input accepts      0x%02X\n", st)
	} else {
		fmt.Fprintf(&b, "  input accepts      %v\n", err)
	}
	if st, err := e.t.outputStatus(); err == nil {
		fmt.Fprintf(&b, "  output ready       0x%02X\n", st)
	} else {
		fmt.Fprintf(&b, "  output ready       %v\n", err)
	}

	fmt.Fprintf(&b, "  allocates output   %v (buffer %d bytes)\n", e.provides, e.outSize)
	fmt.Fprintf(&b, "  settings           %s\n", e.CodecNotes())
	fmt.Fprintf(&b, "  keyframe on demand %s\n", e.KeyFrameNote())
	fmt.Fprintf(&b, "  hot bitrate change %s\n", e.BitrateNote())
	fmt.Fprintf(&b, "  input adopted      %s\n", describeType(e.t.inputCurrentType()))
	fmt.Fprintf(&b, "  output adopted     %s\n", describeType(e.t.outputCurrentType()))
	if e.cb != nil {
		fmt.Fprintf(&b, "  Invoke calls       %d (last error: %v)\n",
			atomic.LoadInt64(&e.cb.invocations), e.cb.lastError())
		for _, s := range e.cb.log() {
			fmt.Fprintf(&b, "    event            %s\n", s)
		}
	}
	return b.String()
}

// SelfTest checks the event subscription by sending itself one.
//
// It separates two silences that look alike: "our COM object is wrong" and "the
// encoder has nothing to say". It is the measurement that settles which of the
// two it is on an encoder that will not speak, and it stays here because that
// question comes back.
//
// It must not be called during encoding: it consumes a place in the queue and
// could take the turn of a real request.
func (e *VideoEncoder) SelfTest() string {
	if e.cb == nil {
		return "no subscription to test"
	}
	if err := e.events.queueEvent(evNeedInput); err != nil {
		return fmt.Sprintf("QueueEvent: %v", err)
	}
	select {
	case <-e.cb.ch:
		return "the fake event came back: the subscription works"
	case <-time.After(time.Second):
		return "not even the fake event comes back: the subscription is broken"
	}
}

// Event is what the encoder asks of the loop that governs it.
type Event int

const (
	// EventNeedInput: the encoder is ready to receive a frame.
	EventNeedInput Event = iota
	// EventHaveOutput: there is a coded frame to collect.
	EventHaveOutput
	// EventOther: something that does not concern us, carry on.
	EventOther
	// EventNone: the deadline passed without the encoder asking anything.
	EventNone
)

// NextEvent waits for the encoder's next request, for at most timeout.
//
// On synchronous transforms there is nothing to wait for: a NeedInput is
// feigned, and the caller collects the output after every ProcessInput.
func (e *VideoEncoder) NextEvent(timeout time.Duration) (Event, error) {
	if !e.async || e.poll {
		return EventNeedInput, nil
	}
	if e.cb != nil {
		select {
		case ev := <-e.cb.ch:
			return ev, nil
		case <-time.After(timeout):
			return EventNone, nil
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		var (
			ev  *mediaEvent
			err error
		)
		if e.block {
			ev, err = e.events.waitEvent()
		} else {
			ev, err = e.events.nextEvent()
		}
		if err != nil {
			return EventOther, err
		}
		if ev == nil {
			if time.Now().After(deadline) {
				return EventNone, nil
			}
			time.Sleep(time.Millisecond)
			continue
		}

		t, err := ev.eventType()
		ev.Release()
		if err != nil {
			return EventOther, err
		}
		switch t {
		case evNeedInput:
			return EventNeedInput, nil
		case evHaveOutput:
			return EventHaveOutput, nil
		default:
			return EventOther, nil
		}
	}
}

// ErrNotAccepting: the encoder does not want the frame being given to it.
//
// It is not a fault: it is the prescribed answer to whoever delivers without
// having received a request, and it happens legitimately after a
// reconfiguration, when a request issued before the flush reaches us after it.
// The remedy is to skip the frame — the restarted transform asks for another
// one straight away — while treating it as an error would kill the capture on
// every bitrate change.
var ErrNotAccepting = errors.New("the encoder does not accept the frame")

// MFSampleExtension_VideoEncodeQP is the quantiser the encoder coded the frame
// it delivers with. Type UINT64.
//
// It is the most honest number this encoder produces: it is not an answer to a
// question of ours but a property of the work done. It says how coarsely the
// encoder had to approximate to stay inside the bitrate, that is, it anticipates
// blockiness instead of observing it — a bitrate that is enough for a still room
// is not enough for the same room when someone moves, and no constant written by
// us can know that.
//
// The GUID does not come from headers on disk, which on this machine are **not
// there** (of Windows Kits\10 only UnionMetadata remains): it is cross-checked
// between the mingw-w64 headers and the ones Microsoft publishes in
// win32metadata, which are its own. Two independent sources that agree.
var mfSampleExtensionVideoEncodeQP = guid("{b2efe478-f979-4c66-b95e-ee2b82c82f36}")

// LastQP is the quantiser of the last frame collected, and whether the encoder
// declares it at all.
//
// The H.264 scale runs from 0 to 51: higher means more approximated. Below ~30
// the picture is clean, beyond ~38 the blocks show. The exact values belong to
// the encoder and have to be tuned here, not copied: libwebrtc uses 24 and 37
// with openh264 and 28 and 39 with VideoToolbox, for the same codec.
func (e *VideoEncoder) LastQP() (qp int, known bool) {
	return int(e.lastQP.Load()), e.qpSeen.Load()
}

// SampleAttributes lists the attributes the encoder put on the first frame it
// delivered, and whether the quantiser was among them.
//
// It answers the question that otherwise stays open when LastQP says "I do not
// know": is it the encoder that does not declare it, or are we asking for it
// wrongly? A missing attribute and a wrong GUID give the same silence, and
// only this inventory separates them.
func (e *VideoEncoder) SampleAttributes() (keys []string, withQP bool) {
	if s, _ := e.attrError.Load().(string); s != "" {
		return []string{"enumeration failed: " + s}, false
	}
	p := e.sampleAttrs.Load()
	if p == nil {
		return nil, false
	}
	for i := range *p {
		k := (*p)[i]
		if ole.IsEqualGUID(&k, mfSampleExtensionVideoEncodeQP) {
			withQP = true
		}
		keys = append(keys, k.String())
	}
	return keys, withQP
}

// Feed hands a frame to the encoder.
func (e *VideoEncoder) Feed(s *Sample) error { return e.t.processInput(s) }

// Take collects a coded frame and passes it to fn.
//
// The bytes are valid only inside fn: they belong to the encoder, which reuses
// them. It returns false when there was nothing to collect, which on
// synchronous transforms is normal.
func (e *VideoEncoder) Take(fn func(data []byte) error) (bool, error) {
	var buffer outputDataBuffer

	// If the encoder allocates the samples itself, which is what hardware
	// encoders do, a null pointer is passed and it fills it in. Otherwise it is
	// up to us to prepare the place to write.
	var allocated *Sample
	if !e.provides {
		var err error
		allocated, err = newSampleWithBuffer(e.outSize)
		if err != nil {
			return false, err
		}
		defer allocated.Release()
		buffer.Sample = allocated
	}

	var status uint32
	r, _, _ := syscall.SyscallN(e.t.vtbl().ProcessOutput,
		uintptr(unsafe.Pointer(e.t)), 0, 1,
		uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&status)))

	// MF_E_TRANSFORM_NEED_MORE_INPUT: there is nothing yet, and it is not an
	// error.
	const needMoreInput = 0xC00D6D72
	if uint32(r) == needMoreInput {
		return false, nil
	}

	// MF_E_TRANSFORM_STREAM_CHANGE: the encoder has changed its mind about the
	// output format and demands it be reassigned before it delivers. It is not
	// a fault and it is provided for by the contract: it happens when the
	// transform reconfigures itself, and ignoring it blocks the stream forever.
	const streamChange = 0xC00D6D61
	if uint32(r) == streamChange {
		if err := e.negotiateOutput(e.cfg); err != nil {
			return false, fmt.Errorf("the encoder changed output format and the new one cannot be set: %w", err)
		}
		return false, nil
	}
	if err := check("ProcessOutput", r); err != nil {
		return false, err
	}
	if buffer.Sample == nil {
		return false, nil
	}
	if buffer.Sample != allocated {
		defer buffer.Sample.Release()
	}

	// **The parameter sets the encoder declares.** They are read here rather
	// than when the encoder was built because at build time this one has none:
	// measured, zero bytes then and forty-eight by the first frame. See
	// readSequenceHeader.
	//
	// **What spends a try is a result, not an attempt.** One early read coming
	// back empty must not switch the reserve off for the session: this is a
	// fallback, so the encoders it exists for are exactly the ones nobody here
	// can test it on, and there it would fail the way faults in this program
	// are worst — silently, with everything else still working. It stops after
	// seqHeaderTries frames, so an encoder that simply never declares it does
	// not pay a COM call for ever.
	if e.seqHeader.Load() == nil && e.seqTries.Add(1) <= seqHeaderTries {
		if b := e.readSequenceHeader(); len(b) > 0 {
			e.seqHeader.Store(&b)
		}
	}

	// The quantiser is read from the sample, not asked of the encoder: it is an
	// attribute of the frame just produced. If it is missing the state is left
	// as it was, rather than zeroed — an encoder that declares it only now and
	// then would otherwise give an average skewed downwards, that is, towards
	// "everything is fine".
	if v, err := buffer.Sample.GetUINT64(mfSampleExtensionVideoEncodeQP); err == nil {
		e.lastQP.Store(uint32(v & 0xffff))
		e.qpSeen.Store(true)
	}

	// Of the first sample the inventory of attributes is kept.
	//
	// **It tells "the encoder does not declare it" from "we do not know how to
	// ask for it"**, which from outside are identical, because a badly written
	// GUID never complains. If the quantiser's key is not among them, the
	// absence belongs to the encoder; if it is there and we are not reading it,
	// it belongs to us.
	if !e.attrSeen.Swap(true) {
		keys, err := buffer.Sample.Keys()
		if err != nil {
			// **A failed enumeration is not an empty store**, and confusing the
			// two would bring back the very ambiguity this inventory removes.
			e.attrError.Store(err.Error())
		}
		e.sampleAttrs.Store(&keys)
	}

	mb, err := buffer.Sample.Buffer()
	if err != nil {
		return false, err
	}
	defer mb.Release()

	if err := mb.WithBytes(fn); err != nil {
		return true, err
	}
	return true, nil
}

func newSampleWithBuffer(size uint32) (*Sample, error) {
	if size == 0 {
		size = 1 << 20
	}
	var s *Sample
	r, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s)))
	if err := check("MFCreateSample", r); err != nil {
		return nil, err
	}
	var buf *MediaBuffer
	r, _, _ = procMFCreateMemoryBuffer.Call(uintptr(size), uintptr(unsafe.Pointer(&buf)))
	if err := check("MFCreateMemoryBuffer", r); err != nil {
		s.Release()
		return nil, err
	}
	defer buf.Release()

	v := (*sampleVtbl)(unsafe.Pointer(s.RawVTable))
	r, _, _ = syscall.SyscallN(v.AddBuffer, uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(buf)))
	if err := check("AddBuffer", r); err != nil {
		s.Release()
		return nil, err
	}
	return s, nil
}

// The counters for the quantiser attribute do not live here but on the
// pipeline: the report is read **after** the capture has stopped, and by then
// the encoder is gone. Asked of it, they answered zero on every machine — even
// where the attribute was being read perfectly well — and that zero reads as
// "this encoder does not declare it". See Pipeline.QPAttributoConteggi.
