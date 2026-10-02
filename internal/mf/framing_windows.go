//go:build windows

package mf

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// The interfaces and the property, from the 10.0.26100.0 headers:
// `MIDL_INTERFACE` in mfidl.h for the IIDs, the enumeration in ksmedia.h for
// the property, whose comment numbers it 43.
var (
	iidGetService               = ole.NewGUID("{FA993888-4383-415A-A930-DD472A8CF6F7}")
	iidExtendedCameraController = ole.NewGUID("{B91EBFEE-CA03-4AF4-8A82-A31752F4A0FC}")
)

type getService struct{ ole.IUnknown }

type getServiceVtbl struct {
	ole.IUnknownVtbl
	GetService uintptr
}

const (
	// KSPROPERTY_CAMERACONTROL_EXTENDED_DIGITALWINDOW.
	propDigitalWindow = 43
	// KSCAMERA_EXTENDEDPROP_DIGITALWINDOW_AUTOFACEFRAMING; MANUAL is zero.
	digitalWindowAuto = 0x1
	// MF_CAPTURE_ENGINE_MEDIASOURCE, which is the filter scope the control is
	// declared at (KSCAMERA_EXTENDEDPROP_FILTERSCOPE, the same 0xFFFFFFFF), and
	// MF_SOURCE_READER_MEDIASOURCE, the reader's name for the source itself.
	filterScope = 0xFFFFFFFF
	// 1.0 in Q24, the whole view.
	q24One = 1 << 24
	// KSCAMERA_EXTENDEDPROP_HEADER, which a payload may or may not carry in
	// front of the window: the documentation of LockPayload does not say.
	extendedPropHeader = 32
	// KSCAMERA_EXTENDEDPROP_DIGITALWINDOW_SETTING: three Q24 LONGs and a
	// reserved ULONG.
	digitalWindowSetting = 16
)

type cameraController struct{ ole.IUnknown }

type cameraControllerVtbl struct {
	ole.IUnknownVtbl
	GetExtendedCameraControl uintptr
}

type cameraControl struct{ ole.IUnknown }

type cameraControlVtbl struct {
	ole.IUnknownVtbl
	GetCapabilities uintptr
	SetFlags        uintptr
	GetFlags        uintptr
	LockPayload     uintptr
	UnlockPayload   uintptr
	CommitSettings  uintptr
}

func (c *cameraControl) vtbl() *cameraControlVtbl {
	return (*cameraControlVtbl)(unsafe.Pointer(c.RawVTable))
}

// Framing is the camera's digital window as the driver declares it: whether
// it can follow a face by itself, whether it is doing so, and the part of the
// view it says it is sending.
type Framing struct {
	// CanFollow: the driver offers automatic face framing. It is what the
	// "Automatic framing" switch in Windows' camera settings turns on and off.
	CanFollow bool
	// Following: it is on now. This is the field to believe.
	Following bool
	// The window the driver declares, as fractions of the whole view: Size 1
	// is all of it. **It is not a witness**: on an ACER HD User Facing it read
	// 1.000 while the picture was plainly a crop around a face.
	OriginX, OriginY, Size float64
	// Payload is the raw buffer, for the instrument that has to say what it
	// read, and PayloadErr is why there is none: the two flags above are read
	// without it, so a payload that will not lock does not hide them.
	Payload    []byte
	PayloadErr error
}

// ErrNoFraming says the camera has no digital window: a driver without the
// control, or a Windows before 11, where the interface does not exist.
var ErrNoFraming = fmt.Errorf("the camera has no digital window control")

// absent says whether a refusal means "there is no such thing here", as
// opposed to "not now". **Only these codes are an absence**: the interface or
// the service not being there (E_NOINTERFACE, which is what the reader's own
// lookup answered, E_NOTIMPL, MF_E_UNSUPPORTED_SERVICE) and the property not
// being in the driver's set (ERROR_NOT_FOUND, ERROR_SET_NOT_FOUND, which is how
// a KS property a driver does not have comes back). Anything else — a driver
// not yet streaming, a device that has gone — is a fault and is reported as
// one, because filed as an absence it would be dropped without a trace.
func absent(res uintptr) bool {
	switch uint32(res) {
	case 0x80004002, 0x80004001, 0xC00D36BA, 0x80070490, 0x80070492:
		return true
	}
	return false
}

// refused wraps a failed lookup, as an absence or as a fault.
func refused(op string, res uintptr) error {
	err := check(op, res)
	if err == nil {
		// S_OK with no object: nothing to talk to, which is an absence.
		return fmt.Errorf("%w: %s answered nothing", ErrNoFraming, op)
	}
	if absent(res) {
		return fmt.Errorf("%w: %w", ErrNoFraming, err)
	}
	return err
}

// digitalWindow reaches the control through the reader's source.
//
// **It answers only while the camera is sending.** Of this property the driver
// documentation says that it "only applies while the camera is actively
// streaming", and a SET before that returns STATUS_INVALID_DEVICE_STATE: so it
// is asked for after the first frame, not at open.
//
// **Through the source's IMFGetService, and not the reader's own lookup.**
// Asked of the reader with GUID_NULL, GetServiceForStream only queries the
// source for the interface, and the source does not expose the controller
// that way: measured, E_NOINTERFACE on a camera Windows offers the switch
// for. Microsoft's own example takes the source's IMFGetService and asks it,
// and that is what is done here: the reader hands over the first, and the
// first answers the second.
func (r *SourceReader) digitalWindow() (*cameraControl, error) {
	var gs *getService
	res, _, _ := syscall.SyscallN(r.vtbl().GetServiceForStream,
		uintptr(unsafe.Pointer(r)), uintptr(filterScope),
		uintptr(unsafe.Pointer(&ole.GUID{})), uintptr(unsafe.Pointer(iidGetService)),
		uintptr(unsafe.Pointer(&gs)))
	if res != 0 || gs == nil {
		return nil, refused("the source's IMFGetService", res)
	}
	defer gs.Release()
	var obj *cameraController
	res, _, _ = syscall.SyscallN((*getServiceVtbl)(unsafe.Pointer(gs.RawVTable)).GetService,
		uintptr(unsafe.Pointer(gs)), uintptr(unsafe.Pointer(&ole.GUID{})),
		uintptr(unsafe.Pointer(iidExtendedCameraController)), uintptr(unsafe.Pointer(&obj)))
	if res != 0 || obj == nil {
		return nil, refused("GetService(IMFExtendedCameraController)", res)
	}
	defer obj.Release()
	var ctl *cameraControl
	vt := (*cameraControllerVtbl)(unsafe.Pointer(obj.RawVTable))
	res, _, _ = syscall.SyscallN(vt.GetExtendedCameraControl,
		uintptr(unsafe.Pointer(obj)), uintptr(filterScope), uintptr(propDigitalWindow),
		uintptr(unsafe.Pointer(&ctl)))
	if res != 0 || ctl == nil {
		return nil, refused("GetExtendedCameraControl(DIGITALWINDOW)", res)
	}
	return ctl, nil
}

// window finds the setting inside a payload, which may or may not carry the
// extended-property header in front of it: a buffer of exactly one setting is
// the setting, and a longer one is a header whose Size names the whole.
func window(p []byte) (off int, ok bool) {
	switch {
	case len(p) == digitalWindowSetting:
		return 0, true
	case len(p) >= extendedPropHeader+digitalWindowSetting &&
		binary.LittleEndian.Uint32(p[8:]) == uint32(len(p)):
		return extendedPropHeader, true
	}
	return 0, false
}

// withPayload lends the control's locked buffer to fn, and unlocks it
// whatever fn does: one place for the pairing, for the read and the write.
func (c *cameraControl) withPayload(fn func([]byte) error) error {
	var p *byte
	var n uint32
	res, _, _ := syscall.SyscallN(c.vtbl().LockPayload,
		uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))
	if err := check("LockPayload", res); err != nil {
		return err
	}
	defer syscall.SyscallN(c.vtbl().UnlockPayload, uintptr(unsafe.Pointer(c)))
	if p == nil || n == 0 {
		return fmt.Errorf("LockPayload handed back no buffer")
	}
	return fn(unsafe.Slice(p, n))
}

// Framing reads the digital window.
func (r *SourceReader) Framing() (Framing, error) {
	ctl, err := r.digitalWindow()
	if err != nil {
		return Framing{}, err
	}
	defer ctl.Release()
	caps, _, _ := syscall.SyscallN(ctl.vtbl().GetCapabilities, uintptr(unsafe.Pointer(ctl)))
	flags, _, _ := syscall.SyscallN(ctl.vtbl().GetFlags, uintptr(unsafe.Pointer(ctl)))
	f := Framing{CanFollow: caps&digitalWindowAuto != 0, Following: flags&digitalWindowAuto != 0}
	f.PayloadErr = ctl.withPayload(func(p []byte) error {
		f.Payload = append([]byte(nil), p...)
		if off, ok := window(p); ok {
			q := func(i int) float64 { return float64(int32(binary.LittleEndian.Uint32(p[off+i:]))) / q24One }
			f.OriginX, f.OriginY, f.Size = q(0), q(4), q(8)
		}
		return nil
	})
	return f, nil
}

// StopFollowing turns automatic face framing off and gives the whole view
// back: the window is set to the origin and to 1.0, which the driver
// documentation names as the defaults.
//
// **Manual with the window left where it was would keep the crop**: the
// window the camera had chosen around a face is what the driver holds while
// it follows, and switching to manual with that payload freezes the zoom
// instead of removing it. So a payload in which the window cannot be found is
// a refusal, and nothing is changed: the flags are set only once the window
// has been written.
func (r *SourceReader) StopFollowing() error {
	ctl, err := r.digitalWindow()
	if err != nil {
		return err
	}
	defer ctl.Release()
	err = ctl.withPayload(func(buf []byte) error {
		off, ok := window(buf)
		if !ok {
			return fmt.Errorf("a payload of %d bytes with no window in a shape known here: nothing changed", len(buf))
		}
		binary.LittleEndian.PutUint32(buf[off:], 0)
		binary.LittleEndian.PutUint32(buf[off+4:], 0)
		binary.LittleEndian.PutUint32(buf[off+8:], q24One)
		binary.LittleEndian.PutUint32(buf[off+12:], 0)
		return nil
	})
	if err != nil {
		return err
	}
	res, _, _ := syscall.SyscallN(ctl.vtbl().SetFlags, uintptr(unsafe.Pointer(ctl)), 0)
	if err := check("SetFlags(MANUAL)", res); err != nil {
		return err
	}
	res, _, _ = syscall.SyscallN(ctl.vtbl().CommitSettings, uintptr(unsafe.Pointer(ctl)))
	return check("CommitSettings", res)
}
