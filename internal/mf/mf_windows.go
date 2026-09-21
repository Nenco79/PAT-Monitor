//go:build windows

// Package mf talks to Media Foundation, the Windows multimedia engine.
//
// Video capture and H.264 encoding live here, and the reason the monitor talks
// straight to the encoder instead of delegating is two commands that are given
// **while it works**: changing the bitrate when the bandwidth drops, and
// producing a keyframe when the browser reports lost packets. An encoder whose
// parameters are set once, at start-up, can do neither — and without the second
// one, whoever loses the reference stays frozen until the periodic keyframe,
// that is, for up to two seconds.
//
// There is no Go binding for Media Foundation, so the interfaces are written by
// hand. It is not virgin ground: internal/audio already speaks COM from pure Go
// with go-ole for the WASAPI capture, and the same idiom is used here — a
// struct for the object, one for its vtable, and syscall to call.
package mf

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"

	"patmonitor/internal/wincom"
)

var (
	modmfplat      = windows.NewLazySystemDLL("mfplat.dll")
	modmf          = windows.NewLazySystemDLL("mf.dll")
	modmfreadwrite = windows.NewLazySystemDLL("mfreadwrite.dll")

	procMFStartup                        = modmfplat.NewProc("MFStartup")
	procMFShutdown                       = modmfplat.NewProc("MFShutdown")
	procMFCreateAttributes               = modmfplat.NewProc("MFCreateAttributes")
	procMFCreateMediaType                = modmfplat.NewProc("MFCreateMediaType")
	procMFTEnumEx                        = modmfplat.NewProc("MFTEnumEx")
	procMFCreateSample                   = modmfplat.NewProc("MFCreateSample")
	procMFCreateMemoryBuffer             = modmfplat.NewProc("MFCreateMemoryBuffer")
	procMFEnumDeviceSources              = modmf.NewProc("MFEnumDeviceSources")
	procMFCreateSourceReaderFromMediaSrc = modmfreadwrite.NewProc("MFCreateSourceReaderFromMediaSource")
)

// MF_VERSION combines the SDK and API versions, and it is the value MFStartup
// expects.
const mfVersion = (0x0002 << 16) | 0x0070

// hresult turns the COM return code into a readable error.
//
// Media Foundation's codes are not in the system table, so FormatMessage
// returns useless text: the hexadecimal value is reported, which is what you
// look up in the documentation, **and the name beside it where we have one**.
type hresult uintptr

func (h hresult) failed() bool { return h&0x80000000 != 0 }

func (h hresult) err(op string) error {
	if !h.failed() {
		return nil
	}
	// **E_ACCESSDENIED is not described here, it is wrapped**, and the
	// difference is the one mfName's own comment argues for E_FAIL: a note
	// belongs where the code was met, because this function is the single
	// funnel for every HRESULT in the package. What travels from here is the
	// bare fact that Windows refused — which that code means wherever it comes
	// from — and the diagnosis is added by whoever was opening a camera.
	if wincom.DeniedHRESULT(uintptr(h)) {
		return fmt.Errorf("%s: HRESULT 0x%08X: %w", op, uint32(h), wincom.ErrDenied)
	}
	if name := mfName(uint32(h)); name != "" {
		return fmt.Errorf("%s: HRESULT 0x%08X (%s)", op, uint32(h), name)
	}
	return fmt.Errorf("%s: HRESULT 0x%08X", op, uint32(h))
}

// mfName translates the codes this repository has actually met. The values come
// from mferror.h, not from memory, and every one of them is already named
// somewhere in this package as a constant or in a comment: the list is a second
// spelling of what we know, not a survey of Media Foundation.
//
// **The hexadecimal stays first and the name is added after it**, which is the
// order that matters: a code we have never met still arrives whole and
// searchable, and nothing here can make one look understood. It is the shape
// `audclntName` already has for WASAPI, and for the same reason — the note is
// the part that saves the evening, not the identifier.
//
// **The list is short on purpose.** Filling it from the header would give three
// hundred entries of which we have seen eight, and a table nobody has met is a
// table nobody has checked.
func mfName(code uint32) string {
	switch code {
	case 0xC00D3704:
		// The case it names: an older copy of the monitor stuck in its shutdown
		// with the camera still open, and every capture of the copy started
		// after it dying with this — about 900 ms after `camera opened`, so the
		// open succeeds and the streaming does not. The note is the diagnosis,
		// and it travels with the code because the code alone sends the reader
		// to the wrong place.
		return "MF_E_HW_MFT_FAILED_START_STREAMING: the hardware pipeline could not start; " +
			"another program — or another copy of this one — may be holding the camera"
	case 0xC00D36B3:
		return "MF_E_INVALIDSTREAMNUMBER: there is no stream with that index"
	case 0xC00D36B4:
		// **Writing the names down found one of ours.** This value was carried
		// in `source_windows.go` as `hrInvalidStreamNum`, that is, under the
		// name of the code one **before** it, and the enumeration's "no more
		// streams" exit was therefore never taken: it left by the fallback
		// below it and asked every one of the eight streams. Nothing failed,
		// and nothing could have — a sentinel that never matches costs seven
		// more questions and no wrong answer.
		return "MF_E_INVALIDMEDIATYPE: the media type is not supported by this object"
	case 0xC00D36B5:
		return "MF_E_NOTACCEPTING: the transform is not taking input just now"
	case 0xC00D36B9:
		return "MF_E_NO_MORE_TYPES"
	case 0xC00D3E80:
		return "MF_E_NO_EVENTS_AVAILABLE"
	case 0xC00D6D61:
		return "MF_E_TRANSFORM_STREAM_CHANGE"
	case 0xC00D6D72:
		return "MF_E_TRANSFORM_NEED_MORE_INPUT"
	}
	// **`E_FAIL` is deliberately not in the list.** It has been met here — five
	// times out of five on a Logitech Brio 105, on every output size the device
	// does not declare — but this function is the single funnel for every
	// HRESULT in the package, and 0x80004005 arriving from `MFTEnumEx` or
	// `D3D11CreateDevice` would carry a Source Reader's diagnosis that cannot
	// apply. A generic code cannot be given a meaning by the place one of its
	// instances was met; where that one is worth explaining is where it
	// happens, and `PinNativeFormat` explains it.
	return ""
}

func check(op string, r uintptr) error { return hresult(r).err(op) }

// Startup initialises Media Foundation for the current thread.
//
// It has to be called inside a thread with COM initialised and pinned, as for
// WASAPI: the objects are bound to the thread that created them, and goroutines
// migrate.
func Startup() error {
	r, _, _ := procMFStartup.Call(uintptr(mfVersion), 0)
	return check("MFStartup", r)
}

func Shutdown() {
	procMFShutdown.Call()
}

// ---------- GUIDs ----------

func guid(s string) *ole.GUID { return ole.NewGUID(s) }

var (
	// Main categories and types.
	MFMediaTypeVideo    = guid("{73646976-0000-0010-8000-00AA00389B71}")
	MFVideoFormatNV12   = guid("{3231564E-0000-0010-8000-00AA00389B71}")
	MFVideoFormatH264   = guid("{34363248-0000-0010-8000-00AA00389B71}")
	MFVideoFormatMJPG   = guid("{47504A4D-0000-0010-8000-00AA00389B71}")
	MFVideoFormatYUY2   = guid("{32595559-0000-0010-8000-00AA00389B71}")
	mftCategoryVideoEnc = guid("{f79eac7d-e545-4387-bdee-d647d7bde42a}")

	// Media type attributes.
	mfMTMajorType        = guid("{48eba18e-f8c9-4687-bf11-0a74c9f96a8f}")
	mfMTSubtype          = guid("{f7e34c9a-42e8-4714-b74b-cb29d72c35e5}")
	mfMTFrameSize        = guid("{1652c33d-d6b2-4012-b834-72030849a37d}")
	mfMTFrameRate        = guid("{c459a2e8-3d2c-4e44-b132-fee5156c7bb0}")
	mfMTInterlaceMode    = guid("{e2724bb8-e676-4806-b4b2-a8d6efb44ccd}")
	mfMTAvgBitrate       = guid("{20332624-fb0d-4d9e-bd0d-cbf6786c102e}")
	mfMTPixelAspectRatio = guid("{c6376a1e-8d0a-4027-be45-6d9a0ad39bb6}")
	mfMTMPEG2Profile     = guid("{ad76a80b-2d5c-4e0b-b375-64e520137036}")
	// The SPS and the PPS as the encoder declares them, outside the stream.
	// Cross-checked between the mingw-w64 headers and a Windows SDK mfapi.h,
	// which agree byte for byte. The documentation says what is inside for
	// H.264: "concatenated NAL units in Annex B format, along with their start
	// codes ... they are SPS & PPS NAL units" — that is, exactly what
	// media.IterateAnnexB already reads.
	mfMTMPEGSequenceHeader = guid("{3c036de7-3ad0-4c9e-9216-ee6d6ac21cb3}")

	// Capture device enumeration.
	mfDevsourceAttributeSourceType       = guid("{c60ac5fe-252a-478f-a0ef-bc8fa5f7cad3}")
	mfDevsourceAttributeSourceTypeVidcap = guid("{8ac3587a-4ae7-42d8-99e0-0a6013eef90f}")
	mfDevsourceAttributeFriendlyName     = guid("{60d0e559-52f8-4fa2-bbce-acdb34a8ec01}")
	mfDevsourceAttributeSymbolicLink     = guid("{58f0aad8-22bf-4f8a-bb3d-d2c4978c6e2f}")

	// Source Reader and transform attributes.
	mfReadwriteEnableHardwareTransforms = guid("{a634a91c-822b-41b9-a494-4de4643612b0}")
	// Mind the first block: it is 0f81da2c, with the leading zero. Written
	// f81da2c7 — the same GUID with the digits shifted — nothing complains: an
	// unknown attribute is accepted in silence by IMFAttributes, and the format
	// conversion simply does not happen. Invisible as long as the webcam already
	// offers NV12; on one that exposes YUY2 only, the camera does not open at
	// all.
	mfSourceReaderEnableAdvancedVideo = guid("{0f81da2c-b537-4672-a8b2-a681b17307a3}")
	mftFriendlyNameAttribute          = guid("{314ffbae-5b41-4c95-9c19-4e7d586face3}")
	mfSourceReaderD3DManager          = guid("{ec822da2-e1e9-4b29-a0d8-563c719f5269}")
	// MF_LOW_LATENCY shares its GUID with CODECAPI_AVLowLatencyMode: they are
	// the same setting reachable from two different interfaces.
	mfLowLatency           = guid("{9c27891a-ed7a-40e1-88e8-b22727a024ee}")
	mfTransformAsync       = guid("{f81a699a-649a-497d-8c73-29f8fed6ad7a}")
	mfTransformAsyncUnlock = guid("{e5666d6b-3422-4eb6-a421-da7db1f8e207}")

	iidIMFTransform = guid("{bf94c121-5b05-4e6f-8000-ba598961414d}")
)

// Source Reader stream indices.
const (
	streamFirstVideo = 0xFFFFFFFC
	streamAllStreams = 0xFFFFFFFE
)

// interlaceModeProgressive is MFVideoInterlace_Progressive.
const interlaceModeProgressive = 2

// ---------- IMFAttributes ----------

// Attributes is the key-value store the whole of Media Foundation rests on:
// media types, devices and transforms are all, underneath, sets of attributes.
type Attributes struct {
	ole.IUnknown
}

type attributesVtbl struct {
	ole.IUnknownVtbl
	GetItem            uintptr
	GetItemType        uintptr
	CompareItem        uintptr
	Compare            uintptr
	GetUINT32          uintptr
	GetUINT64          uintptr
	GetDouble          uintptr
	GetGUID            uintptr
	GetStringLength    uintptr
	GetString          uintptr
	GetAllocatedString uintptr
	GetBlobSize        uintptr
	GetBlob            uintptr
	GetAllocatedBlob   uintptr
	GetUnknown         uintptr
	SetItem            uintptr
	DeleteItem         uintptr
	DeleteAllItems     uintptr
	SetUINT32          uintptr
	SetUINT64          uintptr
	SetDouble          uintptr
	SetGUID            uintptr
	SetString          uintptr
	SetBlob            uintptr
	SetUnknown         uintptr
	LockStore          uintptr
	UnlockStore        uintptr
	GetCount           uintptr
	GetItemByIndex     uintptr
	CopyAllItems       uintptr
}

func (a *Attributes) vtbl() *attributesVtbl {
	return (*attributesVtbl)(unsafe.Pointer(a.RawVTable))
}

func (a *Attributes) SetGUID(key *ole.GUID, value *ole.GUID) error {
	r, _, _ := syscall.SyscallN(a.vtbl().SetGUID,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(value)))
	return check("SetGUID", r)
}

func (a *Attributes) SetUINT32(key *ole.GUID, value uint32) error {
	r, _, _ := syscall.SyscallN(a.vtbl().SetUINT32,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(value))
	return check("SetUINT32", r)
}

// SetUINT64 is for the attributes that pack two 32-bit values into one, such as
// the frame size (width in the top half, height in the bottom) and the cadence
// expressed as a fraction.
func (a *Attributes) SetUINT64(key *ole.GUID, value uint64) error {
	r, _, _ := syscall.SyscallN(a.vtbl().SetUINT64,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(value))
	return check("SetUINT64", r)
}

// SetUnknown records a COM object among the attributes. obj is the pointer to
// the interface, not to the Go struct wrapping it.
func (a *Attributes) SetUnknown(key *ole.GUID, obj unsafe.Pointer) error {
	r, _, _ := syscall.SyscallN(a.vtbl().SetUnknown,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(obj))
	return check("SetUnknown", r)
}

// Keys lists the keys present in this attribute store.
//
// **It is there to tell "the encoder does not declare it" from "we do not know
// how to ask for it"**, which from outside are the same thing: a missing key
// and a key read with the wrong GUID both give an error, and this project has
// already paid months for a badly written GUID that did not complain.
// Enumerating reads what is really there, instead of guessing what is missing.
//
// The keys are GUIDs and not names: only whoever has the headers knows those.
// Whoever reads the report compares them against the list of ours.
func (a *Attributes) Keys() ([]ole.GUID, error) {
	var n uint32
	r, _, _ := syscall.SyscallN(a.vtbl().GetCount,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(&n)))
	if err := check("GetCount", r); err != nil {
		return nil, err
	}
	keys := make([]ole.GUID, 0, n)
	for i := uint32(0); i < n; i++ {
		var k ole.GUID
		// The value is of no interest: this is an inventory, not a read.
		// Passing nil in place of the PROPVARIANT still returns the key, and it
		// is the prescribed way of walking the store.
		r, _, _ := syscall.SyscallN(a.vtbl().GetItemByIndex,
			uintptr(unsafe.Pointer(a)), uintptr(i), uintptr(unsafe.Pointer(&k)), 0)
		if err := check("GetItemByIndex", r); err != nil {
			return keys, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (a *Attributes) GetUINT32(key *ole.GUID) (uint32, error) {
	var v uint32
	r, _, _ := syscall.SyscallN(a.vtbl().GetUINT32,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return v, check("GetUINT32", r)
}

func (a *Attributes) GetUINT64(key *ole.GUID) (uint64, error) {
	var v uint64
	r, _, _ := syscall.SyscallN(a.vtbl().GetUINT64,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return v, check("GetUINT64", r)
}

func (a *Attributes) GetGUID(key *ole.GUID) (ole.GUID, error) {
	var v ole.GUID
	r, _, _ := syscall.SyscallN(a.vtbl().GetGUID,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return v, check("GetGUID", r)
}

// GetBlob returns a byte-array attribute.
//
// The size is asked for first because the buffer is the caller's:
// MF_MT_MPEG_SEQUENCE_HEADER is a few dozen bytes today and nothing says it
// stays that way. The count written back is used rather than the size asked
// for, so a store that writes less does not leave us reading its zeros.
func (a *Attributes) GetBlob(key *ole.GUID) ([]byte, error) {
	var size uint32
	r, _, _ := syscall.SyscallN(a.vtbl().GetBlobSize,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&size)))
	if err := check("GetBlobSize", r); err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, fmt.Errorf("GetBlob: the attribute is there and empty")
	}
	buf := make([]byte, size)
	var written uint32
	r, _, _ = syscall.SyscallN(a.vtbl().GetBlob,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&written)))
	if err := check("GetBlob", r); err != nil {
		return nil, err
	}
	// The count comes back from the store, and slicing past the buffer would
	// panic in the capture goroutine rather than fail here.
	if int(written) > len(buf) {
		return nil, fmt.Errorf("GetBlob: %d bytes reported into a buffer of %d", written, len(buf))
	}
	return buf[:written], nil
}

// GetAllocatedString returns a string COM allocates: it has to be freed with
// CoTaskMemFree, otherwise every enumeration leaks memory.
func (a *Attributes) GetAllocatedString(key *ole.GUID) (string, error) {
	var ptr *uint16
	var length uint32
	r, _, _ := syscall.SyscallN(a.vtbl().GetAllocatedString,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&ptr)), uintptr(unsafe.Pointer(&length)))
	if err := check("GetAllocatedString", r); err != nil {
		return "", err
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(ptr)))
	return windows.UTF16PtrToString(ptr), nil
}

// NewAttributes creates an empty store of the given capacity.
func NewAttributes(size uint32) (*Attributes, error) {
	var a *Attributes
	r, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&a)), uintptr(size))
	if err := check("MFCreateAttributes", r); err != nil {
		return nil, err
	}
	return a, nil
}

// ---------- IMFMediaType ----------

// MediaType describes a format. It is an attribute store with a few extra
// methods, and in this code it is used almost only as a store.
type MediaType struct {
	Attributes
}

// NewMediaType creates an empty media type, to be filled with attributes.
func NewMediaType() (*MediaType, error) {
	var mt *MediaType
	r, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&mt)))
	if err := check("MFCreateMediaType", r); err != nil {
		return nil, err
	}
	return mt, nil
}

// pack2x32 puts two 32-bit values into a 64-bit attribute, with the first in
// the top half. It is Media Foundation's convention for sizes and cadences.
func pack2x32(hi, lo uint32) uint64 { return uint64(hi)<<32 | uint64(lo) }

func unpack2x32(v uint64) (hi, lo uint32) { return uint32(v >> 32), uint32(v) }

// SetFrameSize and SetFrameRate exist because the packed convention is the
// first place one gets it wrong, and getting it wrong the error that comes back
// says nothing useful.
func (mt *MediaType) SetFrameSize(w, h int) error {
	return mt.SetUINT64(mfMTFrameSize, pack2x32(uint32(w), uint32(h)))
}

func (mt *MediaType) SetFrameRate(num, den int) error {
	return mt.SetUINT64(mfMTFrameRate, pack2x32(uint32(num), uint32(den)))
}

func (mt *MediaType) FrameSize() (w, h int, err error) {
	v, err := mt.GetUINT64(mfMTFrameSize)
	if err != nil {
		return 0, 0, err
	}
	hi, lo := unpack2x32(v)
	return int(hi), int(lo), nil
}

func (mt *MediaType) FrameRate() (num, den int, err error) {
	v, err := mt.GetUINT64(mfMTFrameRate)
	if err != nil {
		return 0, 0, err
	}
	hi, lo := unpack2x32(v)
	return int(hi), int(lo), nil
}

func (mt *MediaType) Subtype() (ole.GUID, error) { return mt.GetGUID(mfMTSubtype) }

// SubtypeName translates the GUID into the four-letter name inside it.
//
// Media Foundation's video subtypes have the shape of a FourCC followed by a
// fixed suffix, so the name is read from the first four bytes instead of
// keeping a table.
func SubtypeName(g ole.GUID) string {
	b := []byte{byte(g.Data1), byte(g.Data1 >> 8), byte(g.Data1 >> 16), byte(g.Data1 >> 24)}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return g.String()
		}
	}
	return string(b)
}
