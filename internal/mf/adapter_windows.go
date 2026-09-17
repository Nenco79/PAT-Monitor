package mf

import (
	"fmt"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// The identity of the graphics card and of its driver.
//
// It is diagnostic: pat-diag reports it, and it is the first thing to compare
// when the same code behaves differently on two machines. The encoder's name
// alone is not enough — it stays identical across a driver update, which is
// exactly the case where the behaviour can change.

// IDXGIDevice only: the adapter is returned by GetAdapter, which does not ask
// for an IID. IDXGIAdapter's had stayed here without anyone using it, and that
// is the most dangerous category of dead code in this package — **a wrong GUID
// does not complain**, so one that is never used has never been verified, and
// whoever found it here would take it for good.
var iidIDXGIDevice = guid("{54ec77fa-1377-44e6-8c32-88fd5f44c84c}")

// IDXGIDevice and IDXGIAdapter both derive from IDXGIObject, which brings four
// methods with it: without those in the middle the indices shift and you end up
// calling the wrong method with somebody else's arguments — which gives no
// error, it does anything at all.
type dxgiObjectVtbl struct {
	ole.IUnknownVtbl
	SetPrivateData          uintptr
	SetPrivateDataInterface uintptr
	GetPrivateData          uintptr
	GetParent               uintptr
}

type dxgiDeviceVtbl struct {
	dxgiObjectVtbl
	GetAdapter uintptr
	// The other methods exist but we do not need them, and they only have to be
	// declared if one of them is used later: here GetAdapter is the first, so
	// this is enough.
}

type dxgiAdapterVtbl struct {
	dxgiObjectVtbl
	EnumOutputs           uintptr
	GetDesc               uintptr
	CheckInterfaceSupport uintptr
}

// dxgiAdapterDesc is DXGI_ADAPTER_DESC.
//
// The layout is copied from the header, not deduced: 304 bytes on x64, with
// Description being 128 wide characters and not a pointer. Getting it wrong
// means reading the vendor at the offset of a piece of video memory, and
// Windows does not notice because we are the ones handing it the structure.
type dxgiAdapterDesc struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLUID           struct {
		LowPart  uint32
		HighPart int32
	}
}

// AdapterInfo describes the graphics card the encoder is working on.
//
// The driver version is asked for with CheckInterfaceSupport passing
// **IID_IDXGIDevice**, which is the prescribed one: the documentation says it
// returns the version of the driver binary, and that since WDDM 2.3 all the
// components of a driver package share a single number — so whichever API is in
// use will do. With IID_ID3D11Device it answers DXGI_ERROR_UNSUPPORTED instead,
// and it is documented that it does. It is also the GUID Chromium passes.
func (d *D3DDevice) AdapterInfo() (name, driver string, err error) {
	if d == nil || d.device == nil {
		return "", "", fmt.Errorf("no Direct3D device")
	}
	dev, err := d.device.QueryInterface(iidIDXGIDevice)
	if err != nil {
		return "", "", fmt.Errorf("IDXGIDevice: %w", err)
	}
	defer dev.Release()

	var adapter *ole.IUnknown
	dv := (*dxgiDeviceVtbl)(unsafe.Pointer(dev.RawVTable))
	r, _, _ := syscall.SyscallN(dv.GetAdapter,
		uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(&adapter)))
	if err := check("GetAdapter", r); err != nil {
		return "", "", err
	}
	defer adapter.Release()

	av := (*dxgiAdapterVtbl)(unsafe.Pointer(adapter.RawVTable))

	var desc dxgiAdapterDesc
	r, _, _ = syscall.SyscallN(av.GetDesc,
		uintptr(unsafe.Pointer(adapter)), uintptr(unsafe.Pointer(&desc)))
	if err := check("GetDesc", r); err != nil {
		return "", "", err
	}
	name = syscall.UTF16ToString(desc.Description[:])

	// The driver version is a bonus: if it is missing, the card's identity is
	// there anyway and the caller decides what to do with it. Refusing
	// everything because a piece is missing would mean giving up what was
	// obtained as well.
	var umd int64
	r, _, _ = syscall.SyscallN(av.CheckInterfaceSupport,
		uintptr(unsafe.Pointer(adapter)),
		uintptr(unsafe.Pointer(iidIDXGIDevice)),
		uintptr(unsafe.Pointer(&umd)))
	if check("CheckInterfaceSupport", r) == nil {
		driver = umdVersion(umd)
	}
	return name, driver, nil
}

// umdVersion breaks the driver version down into its four numbers.
//
// It is a 64-bit integer carrying four 16-bit fields, from the most significant
// down: the same decoding Chromium does, and the same shape Device Manager
// shows — so whoever reads our log can compare it with what they see on their
// own PC without having to convert it.
func umdVersion(v int64) string {
	u := uint64(v)
	return fmt.Sprintf("%d.%d.%d.%d",
		(u>>48)&0xffff, (u>>32)&0xffff, (u>>16)&0xffff, u&0xffff)
}
