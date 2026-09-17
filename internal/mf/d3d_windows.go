//go:build windows

package mf

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

// Today's hardware encoders are Direct3D objects first of all.
//
// This file exists because of something found by measuring: on this machine
// none of the three hardware encoders works without a Direct3D 11 device. The
// fault is mute and does not look like a fault — the transform configures
// itself without complaining, declares that it accepts data, and then never
// asks for the first frame. The only trace is the MF_SA_D3D11_AWARE attribute
// the encoder exposes about itself.
//
// The device is needed even when the frames travel in system memory, as ours
// do: it is the encoder that uploads them to the GPU. It is not a shortcut to
// avoid a copy, it is permission to work.

var (
	modd3d11 = windows.NewLazySystemDLL("d3d11.dll")

	procD3D11CreateDevice         = modd3d11.NewProc("D3D11CreateDevice")
	procMFCreateDXGIDeviceManager = modmfplat.NewProc("MFCreateDXGIDeviceManager")
)

const (
	d3dDriverTypeHardware = 1
	// Without VIDEO_SUPPORT the device is created just the same, but it is no
	// use: ResetDevice refuses it.
	d3d11CreateDeviceVideoSupport = 0x0800
	d3d11CreateDeviceBGRASupport  = 0x0020
	d3d11SDKVersion               = 7
)

var (
	iidID3D10Multithread = guid("{9b7e4e00-342c-4106-a19f-4f2704f689f0}")
)

// ---------- IMFDXGIDeviceManager ----------

type dxgiDeviceManager struct {
	ole.IUnknown
}

// The order of the methods is alphabetical. That is neither a COM convention
// nor a happy accident: it is how it is written in mfobjects.h, and it has to
// be copied, not deduced.
type dxgiDeviceManagerVtbl struct {
	ole.IUnknownVtbl
	CloseDeviceHandle uintptr
	GetVideoService   uintptr
	LockDevice        uintptr
	OpenDeviceHandle  uintptr
	ResetDevice       uintptr
	TestDevice        uintptr
	UnlockDevice      uintptr
}

func (m *dxgiDeviceManager) resetDevice(device *ole.IUnknown, token uint32) error {
	v := (*dxgiDeviceManagerVtbl)(unsafe.Pointer(m.RawVTable))
	r, _, _ := syscall.SyscallN(v.ResetDevice,
		uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(device)), uintptr(token))
	return check("ResetDevice", r)
}

// ---------- ID3D10Multithread ----------

// The name says 10 but the interface is obtained from a Direct3D 11 device too:
// it is the idiom Chromium and OBS use.
type multithread struct {
	ole.IUnknown
}

type multithreadVtbl struct {
	ole.IUnknownVtbl
	Enter                   uintptr
	Leave                   uintptr
	SetMultithreadProtected uintptr
	GetMultithreadProtected uintptr
}

func (m *multithread) setProtected(on bool) {
	v := (*multithreadVtbl)(unsafe.Pointer(m.RawVTable))
	var flag uintptr
	if on {
		flag = 1
	}
	// It returns the previous value, not an HRESULT: there is nothing to check.
	syscall.SyscallN(v.SetMultithreadProtected, uintptr(unsafe.Pointer(m)), flag)
}

// ---------- the device ----------

// D3DDevice holds together the Direct3D 11 device and the manager that shares
// it with Media Foundation.
type D3DDevice struct {
	device  *ole.IUnknown
	manager *dxgiDeviceManager
}

// NewD3DDevice creates a Direct3D 11 device usable for encoding.
func NewD3DDevice() (*D3DDevice, error) {
	var device *ole.IUnknown
	r, _, _ := procD3D11CreateDevice.Call(
		0, // default adapter
		d3dDriverTypeHardware,
		0, // no software rasteriser
		uintptr(d3d11CreateDeviceVideoSupport|d3d11CreateDeviceBGRASupport),
		0, 0, // feature levels: the default ones
		d3d11SDKVersion,
		uintptr(unsafe.Pointer(&device)),
		0, // the level obtained is of no interest
		0, // nor the immediate context: the drawing is the encoder's job
	)
	if err := check("D3D11CreateDevice", r); err != nil {
		return nil, err
	}

	// Media Foundation uses the device from its own threads while we hand it
	// frames from ours. Without this protection the behaviour is undefined, and
	// race faults inside a graphics driver cannot be diagnosed from our side.
	if mt, err := device.QueryInterface(iidID3D10Multithread); err == nil {
		(*multithread)(unsafe.Pointer(mt)).setProtected(true)
		mt.Release()
	}

	var token uint32
	var manager *dxgiDeviceManager
	r, _, _ = procMFCreateDXGIDeviceManager.Call(
		uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&manager)))
	if err := check("MFCreateDXGIDeviceManager", r); err != nil {
		device.Release()
		return nil, err
	}

	// The token ties this manager to this device: it is the manager that has
	// just issued it, and it is there to stop another piece of code replacing
	// the device underneath us.
	if err := manager.resetDevice(device, token); err != nil {
		manager.Release()
		device.Release()
		return nil, fmt.Errorf("the Direct3D device is not fit for encoding: %w", err)
	}

	return &D3DDevice{device: device, manager: manager}, nil
}

func (d *D3DDevice) Release() {
	if d == nil {
		return
	}
	if d.manager != nil {
		d.manager.Release()
		d.manager = nil
	}
	if d.device != nil {
		d.device.Release()
		d.device = nil
	}
}
