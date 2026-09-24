//go:build windows

package mf

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
)

var iidIMFMediaSource = guid("{279a808d-aec7-40c8-9c6b-a6b492c78a66}")

// ---------- IMFActivate ----------

// activate is an object not yet built: the enumeration returns these proxies,
// and the real object comes into being only when it is needed.
type activate struct {
	Attributes
}

type activateVtbl struct {
	attributesVtbl
	ActivateObject uintptr
	ShutdownObject uintptr
	DetachObject   uintptr
}

func (a *activate) vtblActivate() *activateVtbl {
	return (*activateVtbl)(unsafe.Pointer(a.RawVTable))
}

func (a *activate) activateObject(iid *ole.GUID) (*ole.IUnknown, error) {
	var obj *ole.IUnknown
	r, _, _ := syscall.SyscallN(a.vtblActivate().ActivateObject,
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if err := check("ActivateObject", r); err != nil {
		return nil, err
	}
	return obj, nil
}

// ---------- camera enumeration ----------

// Device is a video source on the system.
type Device struct {
	Name string
	// Link is the symbolic path, which identifies the device stably across
	// restarts: the friendly name does not, because two cameras of the same
	// model share it.
	Link string
}

// ListVideoDevices lists the video capture sources.
//
// The answer comes from the system in structured form, with each one's symbolic
// path: that is what identifies a device stably across restarts, while the
// friendly name does not — two cameras of the same model share it.
func ListVideoDevices() ([]Device, error) {
	attrs, err := NewAttributes(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Release()

	if err := attrs.SetGUID(mfDevsourceAttributeSourceType, mfDevsourceAttributeSourceTypeVidcap); err != nil {
		return nil, err
	}

	var arr **activate
	var count uint32
	r, _, _ := procMFEnumDeviceSources.Call(
		uintptr(unsafe.Pointer(attrs)),
		uintptr(unsafe.Pointer(&arr)),
		uintptr(unsafe.Pointer(&count)))
	if err := check("MFEnumDeviceSources", r); err != nil {
		return nil, err
	}
	if arr == nil || count == 0 {
		return nil, nil
	}
	// The array is allocated by COM and has to be freed after releasing the
	// objects.
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(arr)))

	list := unsafe.Slice(arr, count)
	out := make([]Device, 0, count)
	for _, act := range list {
		if act == nil {
			continue
		}
		name, _ := act.GetAllocatedString(mfDevsourceAttributeFriendlyName)
		link, _ := act.GetAllocatedString(mfDevsourceAttributeSymbolicLink)
		out = append(out, Device{Name: name, Link: link})
		act.Release()
	}
	return out, nil
}

// ---------- IMFSourceReader ----------

// SourceReader reads frames from a capture source.
type SourceReader struct {
	ole.IUnknown
}

type sourceReaderVtbl struct {
	ole.IUnknownVtbl
	GetStreamSelection       uintptr
	SetStreamSelection       uintptr
	GetNativeMediaType       uintptr
	GetCurrentMediaType      uintptr
	SetCurrentMediaType      uintptr
	SetCurrentPosition       uintptr
	ReadSample               uintptr
	Flush                    uintptr
	GetServiceForStream      uintptr
	GetPresentationAttribute uintptr
}

func (r *SourceReader) vtbl() *sourceReaderVtbl {
	return (*sourceReaderVtbl)(unsafe.Pointer(r.RawVTable))
}

// OpenCamera opens the named camera and asks for the wanted format.
//
// An empty link means the first camera found.
//
// NV12 is asked for even when the camera does not expose it: with
// MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING the Source Reader inserts
// the conversion itself, so a webcam that speaks only MJPEG is decoded without
// us having to deal with it. It costs, and has to be measured, but it removes a
// whole branch of special cases.
//
// With dev set the frames stay on the GPU: the Source Reader allocates Direct3D
// textures instead of system-memory buffers, and that is the shape hardware
// encoders expect. Whoever wants to read their pixels — motion detection — has
// to bring them back down explicitly.
func OpenCamera(link string, width, height, fps int, dev *D3DDevice) (*SourceReader, error) {
	reader, err := newSourceReader(link, dev)
	if err != nil {
		return nil, err
	}
	if err := reader.selectOnlyVideo(); err != nil {
		reader.Release()
		return nil, err
	}
	if err := reader.requestFormat(width, height, fps); err != nil {
		reader.Release()
		return nil, err
	}
	return reader, nil
}

// newSourceReader opens the camera without yet asking it for any format.
//
// The separation is there so that what the camera offers can be enumerated
// before demanding anything: see CameraFormats.
func newSourceReader(link string, dev *D3DDevice) (*SourceReader, error) {
	act, err := findDevice(link)
	if err != nil {
		return nil, err
	}
	defer act.Release()

	src, err := act.activateObject(iidIMFMediaSource)
	if err != nil {
		// **A refusal comes out of here and not out of the enumeration**: the
		// device was in the list a line above, and the object behind it will
		// not be built. What that means for a baby monitor is said by
		// internal/pipeline, which is the one place that knows there is a
		// microphone in the same refusal — see deniedNote there. Here the fact
		// travels, wrapped by the HRESULT funnel.
		return nil, fmt.Errorf("camera open: %w", err)
	}
	defer src.Release()

	attrs, err := NewAttributes(3)
	if err != nil {
		return nil, err
	}
	defer attrs.Release()
	if err := attrs.SetUINT32(mfSourceReaderEnableAdvancedVideo, 1); err != nil {
		return nil, err
	}
	if err := attrs.SetUINT32(mfReadwriteEnableHardwareTransforms, 1); err != nil {
		return nil, err
	}
	if dev != nil {
		if err := attrs.SetUnknown(mfSourceReaderD3DManager, unsafe.Pointer(dev.manager)); err != nil {
			return nil, err
		}
	}

	var reader *SourceReader
	r, _, _ := procMFCreateSourceReaderFromMediaSrc.Call(
		uintptr(unsafe.Pointer(src)),
		uintptr(unsafe.Pointer(attrs)),
		uintptr(unsafe.Pointer(&reader)))
	if err := check("MFCreateSourceReaderFromMediaSource", r); err != nil {
		return nil, err
	}
	return reader, nil
}

// CameraFormat is a mode declared by the camera.
type CameraFormat struct {
	Stream        int
	Index         int
	Subtype       string
	Width, Height int
	FPSNum        int
	FPSDen        int
	Video         bool
}

func (f CameraFormat) String() string {
	fps := "?"
	if f.FPSDen > 0 {
		fps = fmt.Sprintf("%.2f", float64(f.FPSNum)/float64(f.FPSDen))
	}
	return fmt.Sprintf("%s %dx%d @%s fps", f.Subtype, f.Width, f.Height, fps)
}

// Codes that end the enumeration rather than reporting a fault.
const (
	hrNoMoreTypes = 0xC00D36B9 // MF_E_NO_MORE_TYPES
	// **It was 0xC00D36B4 under this name, which is the code one along**:
	// `MF_E_INVALIDMEDIATYPE`, not `MF_E_INVALIDSTREAMNUMBER`. So the early
	// return below never fired and the enumeration left by its fallback,
	// asking all eight streams — nothing wrong in the answer, seven questions
	// nobody needed. It was found by writing the names into `mfName`, that is,
	// by spelling a constant out twice and having the two disagree.
	hrInvalidStreamNum  = 0xC00D36B3 // MF_E_INVALIDSTREAMNUMBER
	cameraFormatStreams = 8          // how many streams to look for before stopping
)

// CameraFormats lists what the camera declares it can do, stream by stream.
//
// It is needed because "SetCurrentMediaType said no" does not say **what**
// should have been asked for, and on a machine that is not your own the only
// alternative is guessing one combination at a time.
//
// There is more than one stream more often than one would think: webcams with
// Windows Hello also expose an infrared sensor, which is a video stream to all
// intents and purposes but speaks in greyscale and is of no use here.
func CameraFormats(link string) ([]CameraFormat, error) {
	reader, err := newSourceReader(link, nil)
	if err != nil {
		return nil, err
	}
	defer reader.Release()

	var out []CameraFormat
	for stream := range cameraFormatStreams {
		for index := 0; ; index++ {
			mt, res := reader.nativeMediaType(stream, index)
			if mt == nil {
				if uint32(res) == hrInvalidStreamNum {
					return out, nil // no more streams: done
				}
				break // no more formats for this stream
			}

			f := CameraFormat{Stream: stream, Index: index}
			if major, e := mt.GetGUID(mfMTMajorType); e == nil {
				f.Video = ole.IsEqualGUID(&major, MFMediaTypeVideo)
			}
			if g, e := mt.Subtype(); e == nil {
				f.Subtype = SubtypeName(g)
			}
			f.Width, f.Height, _ = mt.FrameSize()
			f.FPSNum, f.FPSDen, _ = mt.FrameRate()
			mt.Release()

			out = append(out, f)
		}
	}
	return out, nil
}

// nativeMediaType returns the format, or nil plus the code explaining why there
// is none. The raw code matters: the end of the list is announced with an error
// HRESULT, and treating it as one would break off the enumeration and make it
// look like a fault.
func (r *SourceReader) nativeMediaType(stream, index int) (*MediaType, uintptr) {
	var mt *MediaType
	res, _, _ := syscall.SyscallN(r.vtbl().GetNativeMediaType,
		uintptr(unsafe.Pointer(r)), uintptr(uint32(stream)), uintptr(uint32(index)),
		uintptr(unsafe.Pointer(&mt)))
	if res != 0 {
		return nil, res
	}
	return mt, 0
}

func findDevice(link string) (*activate, error) {
	attrs, err := NewAttributes(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Release()
	if err := attrs.SetGUID(mfDevsourceAttributeSourceType, mfDevsourceAttributeSourceTypeVidcap); err != nil {
		return nil, err
	}

	var arr **activate
	var count uint32
	r, _, _ := procMFEnumDeviceSources.Call(
		uintptr(unsafe.Pointer(attrs)),
		uintptr(unsafe.Pointer(&arr)),
		uintptr(unsafe.Pointer(&count)))
	if err := check("MFEnumDeviceSources", r); err != nil {
		return nil, err
	}
	if arr == nil || count == 0 {
		return nil, fmt.Errorf("no camera found")
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(arr)))

	list := unsafe.Slice(arr, count)
	var found *activate
	for _, act := range list {
		if act == nil {
			continue
		}
		if found != nil {
			act.Release()
			continue
		}
		if link == "" {
			found = act
			continue
		}
		if l, _ := act.GetAllocatedString(mfDevsourceAttributeSymbolicLink); l == link {
			found = act
			continue
		}
		act.Release()
	}
	if found == nil {
		return nil, fmt.Errorf("camera %q not found", link)
	}
	return found, nil
}

// selectOnlyVideo turns off every stream and turns the video one back on.
//
// A webcam with a built-in microphone exposes audio as well: reading it here
// would be wasted, because we take the audio from WASAPI in raw mode.
func (r *SourceReader) selectOnlyVideo() error {
	if err := r.setStreamSelection(streamAllStreams, false); err != nil {
		return err
	}
	return r.setStreamSelection(streamFirstVideo, true)
}

func (r *SourceReader) setStreamSelection(stream uint32, selected bool) error {
	var sel uintptr
	if selected {
		sel = 1
	}
	res, _, _ := syscall.SyscallN(r.vtbl().SetStreamSelection,
		uintptr(unsafe.Pointer(r)), uintptr(stream), sel)
	return check("SetStreamSelection", res)
}

func (r *SourceReader) requestFormat(width, height, fps int) error {
	mt, err := NewMediaType()
	if err != nil {
		return err
	}
	defer mt.Release()

	if err := mt.SetGUID(mfMTMajorType, MFMediaTypeVideo); err != nil {
		return err
	}
	if err := mt.SetGUID(mfMTSubtype, MFVideoFormatNV12); err != nil {
		return err
	}
	if err := mt.SetFrameSize(width, height); err != nil {
		return err
	}
	// **A frame rate here is an order, not a description.** The video processor
	// the reader inserts is documented to convert the cadence "to match the
	// output media type", so a camera delivering 14 against a 30 written here
	// has the difference **made up by repetition** — measured, 29% of the frames
	// under 500 bytes after a size change on two different cameras. A
	// non-positive fps therefore leaves the attribute out altogether, which is
	// one of the two roads out and the one that needs no second API.
	if fps > 0 {
		if err := mt.SetFrameRate(fps, 1); err != nil {
			return err
		}
	}
	if err := mt.SetUINT32(mfMTInterlaceMode, interlaceModeProgressive); err != nil {
		return err
	}

	res, _, _ := syscall.SyscallN(r.vtbl().SetCurrentMediaType,
		uintptr(unsafe.Pointer(r)), uintptr(streamFirstVideo), 0, uintptr(unsafe.Pointer(mt)))
	return check("SetCurrentMediaType", res)
}

// ---------- IMFSourceReaderEx ----------

// iidSourceReaderEx is IMFSourceReaderEx.
//
// The Windows SDK is not installed on the development machine, so this value was
// cross-checked between **two independent headers**, as the wrong-GUID chapter
// requires: mingw-w64's `mfreadwrite.h`, which declares it twice, in
// `DEFINE_GUID` and in `__CRT_UUID_DECL`, and the IDL-recompiled SDK headers
// Microsoft publishes in `win32metadata`. Both give
// 7b981cf0-560e-4116-9875-b099895f23d7, and both list the four extra methods in
// the same order.
var iidSourceReaderEx = ole.NewGUID("{7B981CF0-560E-4116-9875-B099895F23D7}")

// sourceReaderExVtbl is IMFSourceReader's table with four methods after it.
//
// The interface **derives** from IMFSourceReader, so the ten methods above come
// first and at the same offsets: an embedded struct says that once instead of
// repeating the list, which is the second list that diverges.
type sourceReaderExVtbl struct {
	sourceReaderVtbl
	SetNativeMediaType           uintptr
	AddTransformForStream        uintptr
	RemoveAllTransformsForStream uintptr
	GetTransformForStream        uintptr
}

// mfSourceReaderCurrentTypeIndex asks for the type a stream is producing now,
// rather than one of the ones it could.
const mfSourceReaderCurrentTypeIndex = 0xFFFFFFFF

// PinNativeFormat fixes what the **device** produces, so that a later change of
// output size cannot be satisfied by renegotiating the camera.
//
// **It exists because the chapter this file implements is only half true.**
// `SetCurrentMediaType` on the reader's output is satisfied in one of two ways:
// by finding a native type on the device that matches, or by putting a Video
// Processor in front of one that does not — and only the second scales. Which
// one happens is the camera's accident: a webcam that speaks YUY2 alone forces
// the converter into the chain and then any size works, while one that offers
// NV12 has the reader take the device's own type, leaving nothing to scale
// with. On a Logitech Brio 105 that is measured and fatal: every size the
// device declares is delivered, every size it does not gives
// `ReadSample: HRESULT 0x80004005`, five for five.
//
// Pinning the native type is the documented shape of "the camera is fixed, the
// reader converts". **Whether it works is a measurement and not a deduction**,
// which is why it is a switch and not the default: the caller runs the same
// binary with it and without it, on the camera that fails.
//
// The type pinned is the one the device is producing **now**, read back rather
// than composed, so nothing about the camera's format is asserted by us.
func (r *SourceReader) PinNativeFormat() error {
	obj, err := r.QueryInterface(iidSourceReaderEx)
	if err != nil {
		return fmt.Errorf("IMFSourceReaderEx unavailable: %w", err)
	}
	ex := (*SourceReader)(unsafe.Pointer(obj))
	defer ex.Release()

	mt, code := r.nativeMediaType(streamFirstVideo, mfSourceReaderCurrentTypeIndex)
	if mt == nil {
		return check("GetNativeMediaType(current)", code)
	}
	defer mt.Release()

	// The flags say what the reader did about it, and they are read rather than
	// discarded: this whole function exists because an accepted call is not an
	// applied one, and the one thing the API offers here is that word.
	var flags uint32
	vt := (*sourceReaderExVtbl)(unsafe.Pointer(ex.RawVTable))
	res, _, _ := syscall.SyscallN(vt.SetNativeMediaType,
		uintptr(unsafe.Pointer(ex)), uintptr(streamFirstVideo),
		uintptr(unsafe.Pointer(mt)), uintptr(unsafe.Pointer(&flags)))
	if err := check("SetNativeMediaType", res); err != nil {
		return err
	}
	return nil
}

// mfXVPDisableFRC switches off the video processor's frame-rate conversion.
//
// The SDK is not installed here, so the value was cross-checked between two
// independent sources, as the wrong-GUID chapter requires: the IDL-recompiled
// SDK headers Microsoft publishes in `win32metadata`, and Wine's `mfidl.idl`,
// which is a clean-room reimplementation and owes Microsoft's headers nothing.
// Both give 2c0afa19-7a97-4d5a-9ee8-16d4fc518d8c.
var mfXVPDisableFRC = ole.NewGUID("{2C0AFA19-7A97-4D5A-9EE8-16D4FC518D8C}")

// DisableFrameRateConversion tells the inserted video processor not to
// manufacture frames.
//
// **It is the vendor's own switch for the defect above**: of `MF_XVP_DISABLE_FRC`
// the documentation says "if this attribute is TRUE, the video processor will
// not perform frame-rate conversion. By default, the video processor will
// convert the frame rate to match the output media type." The second sentence
// is the defect, stated by whoever wrote it.
//
// **The caveat is in the same page and is the reason this is a switch**: "set
// the attribute before streaming begins", while the processor this reaches
// appears in the middle of a session, the first time the scale moves. So it is
// asked for after every size change, and whether it takes is read from the
// frames, not from the HRESULT.
//
// A stream can have more than one transform in front of it, and which index the
// processor is at is not promised: they are walked until the enumeration ends,
// and the attribute is set on every one that will take it. Setting it on
// something that is not a video processor costs an attribute nobody reads —
// the same as a wrong GUID, and harmless for the same reason.
func (r *SourceReader) DisableFrameRateConversion() (int, error) {
	obj, err := r.QueryInterface(iidSourceReaderEx)
	if err != nil {
		return 0, fmt.Errorf("IMFSourceReaderEx unavailable: %w", err)
	}
	ex := (*SourceReader)(unsafe.Pointer(obj))
	defer ex.Release()
	vt := (*sourceReaderExVtbl)(unsafe.Pointer(ex.RawVTable))

	set := 0
	for i := range 8 {
		var category ole.GUID
		var t *transform
		res, _, _ := syscall.SyscallN(vt.GetTransformForStream,
			uintptr(unsafe.Pointer(ex)), uintptr(streamFirstVideo), uintptr(uint32(i)),
			uintptr(unsafe.Pointer(&category)), uintptr(unsafe.Pointer(&t)))
		if res != 0 || t == nil {
			break
		}
		attrs, err := t.attributes()
		if err == nil {
			if err := attrs.SetUINT32(mfXVPDisableFRC, 1); err == nil {
				set++
			}
			attrs.Release()
		}
		t.Release()
	}
	if set == 0 {
		return 0, fmt.Errorf("no transform in front of the stream accepted MF_XVP_DISABLE_FRC")
	}
	return set, nil
}

// SetOutputSize asks the Source Reader to deliver frames of a different size,
// without touching the camera.
//
// It is the route to the resolution scale: when the bandwidth will not carry
// 720p the right answer is not to keep taking bits away from the same pixels —
// below a certain threshold all that gives is a broken picture — but to send
// fewer pixels.
//
// It scales the Source Reader, not the camera, and the difference matters: the
// camera stays open on its best format, so the change does not interrupt the
// video and the motion detector goes on seeing the same scene. It can do this
// because we switched on MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING —
// the GUID whose digits are easy to shift, and which does not complain when
// they are.
//
// As always, what the reader answers is not what it does: the caller has to
// look at the size of the frames that arrive.
func (r *SourceReader) SetOutputSize(width, height, fps int) error {
	return r.requestFormat(width, height, fps)
}

// CurrentFormat reports what the Source Reader has been configured to deliver.
//
// **What it answers is the type last accepted, and that is less than it looks.**
// The subtype and the cadence really can differ from what was asked: the camera
// can refuse one and have a nearby one negotiated in its place. The size cannot
// — with advanced video processing switched on the reader takes the size it is
// given and puts a scaler in front of it, so the only thing reading it back
// proves is that SetCurrentMediaType took. It says nothing about the pixels: a
// camera too small to fill the frame is upscaled, and the size read back agrees
// all the same. What the pixels really are is read from the frame bytes and
// from the SPS; see media.SPSSize.
func (r *SourceReader) CurrentFormat() (w, h, fpsNum, fpsDen int, subtype string, err error) {
	var mt *MediaType
	res, _, _ := syscall.SyscallN(r.vtbl().GetCurrentMediaType,
		uintptr(unsafe.Pointer(r)), uintptr(streamFirstVideo), uintptr(unsafe.Pointer(&mt)))
	if err = check("GetCurrentMediaType", res); err != nil {
		return
	}
	defer mt.Release()

	if w, h, err = mt.FrameSize(); err != nil {
		return
	}
	fpsNum, fpsDen, _ = mt.FrameRate()
	if g, e := mt.Subtype(); e == nil {
		subtype = SubtypeName(g)
	}
	return
}

// There used to be a second method here that asked the reader for the whole
// current media type, and it was removed: during the resolution scale it read
// back 960x540 while the stream still carried the SPS of 1280x720. Nothing
// downstream should reach for it again.

// Status flags returned by ReadSample.
const (
	streamFlagEndOfStream             = 0x02
	streamFlagCurrentMediaTypeChanged = 0x10
)

// ReadSample reads the next frame.
//
// It can return (nil, nil) without that being an error: when the source has no
// frame ready yet, Media Foundation answers with an empty sample rather than
// blocking.
func (r *SourceReader) ReadSample() (*Sample, time.Duration, error) {
	var (
		actualStream uint32
		flags        uint32
		timestamp    int64
		sample       *Sample
	)
	res, _, _ := syscall.SyscallN(r.vtbl().ReadSample,
		uintptr(unsafe.Pointer(r)), uintptr(streamFirstVideo), 0,
		uintptr(unsafe.Pointer(&actualStream)),
		uintptr(unsafe.Pointer(&flags)),
		uintptr(unsafe.Pointer(&timestamp)),
		uintptr(unsafe.Pointer(&sample)))
	if err := check("ReadSample", res); err != nil {
		return nil, 0, err
	}
	if flags&streamFlagEndOfStream != 0 {
		return nil, 0, fmt.Errorf("the camera closed the stream")
	}
	// The timestamp is in units of 100 ns.
	return sample, time.Duration(timestamp) * 100, nil
}

// ---------- IMFSample and IMFMediaBuffer ----------

// Sample is one sample: one or more buffers with an instant and a duration.
type Sample struct {
	Attributes
}

type sampleVtbl struct {
	attributesVtbl
	GetSampleFlags            uintptr
	SetSampleFlags            uintptr
	GetSampleTime             uintptr
	SetSampleTime             uintptr
	GetSampleDuration         uintptr
	SetSampleDuration         uintptr
	GetBufferCount            uintptr
	GetBufferByIndex          uintptr
	ConvertToContiguousBuffer uintptr
	AddBuffer                 uintptr
	RemoveBufferByIndex       uintptr
	RemoveAllBuffers          uintptr
	GetTotalLength            uintptr
	CopyToBuffer              uintptr
}

func (s *Sample) vtblSample() *sampleVtbl {
	return (*sampleVtbl)(unsafe.Pointer(s.RawVTable))
}

// Buffer returns the data as a single contiguous block.
//
// A sample can have several buffers, and the conversion joins them: it costs a
// copy only when there really is more than one.
func (s *Sample) Buffer() (*MediaBuffer, error) {
	var buf *MediaBuffer
	r, _, _ := syscall.SyscallN(s.vtblSample().ConvertToContiguousBuffer,
		uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(&buf)))
	if err := check("ConvertToContiguousBuffer", r); err != nil {
		return nil, err
	}
	return buf, nil
}

// MediaBuffer is a block of memory managed by Media Foundation.
type MediaBuffer struct {
	ole.IUnknown
}

type mediaBufferVtbl struct {
	ole.IUnknownVtbl
	Lock             uintptr
	Unlock           uintptr
	GetCurrentLength uintptr
	SetCurrentLength uintptr
	GetMaxLength     uintptr
}

func (b *MediaBuffer) vtbl() *mediaBufferVtbl {
	return (*mediaBufferVtbl)(unsafe.Pointer(b.RawVTable))
}

// WithBytes runs fn over the buffer's data.
//
// The bytes stay valid only inside fn: the buffer is locked for the duration of
// the call and unlocked straight afterwards, because keeping it locked stops the
// source reusing it and brings the capture to a halt.
func (b *MediaBuffer) WithBytes(fn func([]byte) error) error {
	var ptr *byte
	var maxLen, curLen uint32
	r, _, _ := syscall.SyscallN(b.vtbl().Lock,
		uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(&ptr)),
		uintptr(unsafe.Pointer(&maxLen)), uintptr(unsafe.Pointer(&curLen)))
	if err := check("Lock", r); err != nil {
		return err
	}
	defer syscall.SyscallN(b.vtbl().Unlock, uintptr(unsafe.Pointer(b)))

	if ptr == nil || curLen == 0 {
		return fn(nil)
	}
	return fn(unsafe.Slice(ptr, curLen))
}
