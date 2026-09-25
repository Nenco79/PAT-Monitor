//go:build windows

package tray

import (
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The icon is drawn, not embedded.
//
// This is the notification-area icon, not the executable's: that one has to live
// in the PE because Explorer reads it with the program not running, and
// `internal/icon` builds it. Here the program is running, so it can be computed.
//
// The normal route would be an .ico file turned into a .syso by `rsrc` or
// `goversioninfo` and linked into the binary. That would mean a third-party
// binary in the build chain to produce 32x32 pixels, and the project's rule is
// that before adding one you ask whether the thing can be asked of the system.
// Here it can: GDI creates bitmaps and icons, and we compute the pixels.
//
// The result gains by it too. An .ico is a fixed image, while this icon **is the
// state**: the colour says how the monitor is, and with the console off it is
// the only thing that says so without opening a page. A file would have wanted
// five images kept in step with five constants.
//
// The size is dictated by the system (SM_CXSMICON) rather than fixed at 16: on a
// high-density screen a 16-pixel icon resized by the notification area's manager
// comes out blurred, and blurring shows precisely where the icon is small.

// The colours are those of the onboarding prototype's phases, and they are the
// same ones the pages use: it is the only way for the notification-area icon and
// the page open beside it to look like the same program.
var phaseColor = map[Phase][3]float64{
	PhaseStarting: {0x7A, 0x72, 0x64}, // stone: nothing is known yet
	PhaseHome:     {0x17, 0x70, 0x6E}, // teal: --c-home
	PhaseOutside:  {0x4F, 0x7F, 0x31}, // leaf green: --c-ready
	PhaseCheck:    {0xA6, 0x6A, 0x12}, // amber: --c-note
	PhaseFault:    {0xB3, 0x45, 0x2F}, // brick: --c-stop
}

// The crescent's colour, that is, the cream of the cards: --card.
var moonColor = [3]float64{0xFB, 0xF8, 0xF2}

// makeIcon draws the phase's icon and returns an HICON to be destroyed with
// destroyIcon.
//
// The drawing is a waning moon inside a filled disc. A crescent is recognisable
// at 16 pixels — that is why it is an ancient symbol — and it says by itself
// what this is about: this program is watched at night.
func makeIcon(p Phase, size int, dim float64) (windows.Handle, error) {
	if size <= 0 {
		size = 16
	}
	return iconFromBGRA(iconPixels(p, size, dim), size)
}

// pulseTowards is the colour the pulse carries the disc towards.
//
// **It is black, and it was arrived at by looking.** The first draft faded
// towards the stone of `PhaseStarting`, on the argument that the pulse would
// then swing between two hues of the palette that already mean "ready" and "I do
// not know". On paper it held; on the taskbar the report was "I think I saw the
// blinking but it is between two identical colours", and a movement that has to
// be looked for is not a signal. **Between an argument and a glance, on a
// twenty-four pixel glyph the glance decides.**
var pulseTowards = [3]float64{0, 0, 0}

// iconPixels computes the icon's pixels, in premultiplied top-down BGRA.
//
// It is separate from makeIcon for one reason: so that the drawing can be
// **looked at** without going through GDI and without a notification area. An
// icon that is computed rather than loaded has the advantage of being the state,
// and the disadvantage that nobody sees it until it is running.
//
// dim carries the disc towards pulseTowards, and is in [0,1].
//
// **The crescent is never dimmed**, and that is what makes an excursion all the
// way to black bearable: the disc can go out entirely and the icon stays
// readable, because the cream moon draws its shape over it. If that were dimmed
// too, halfway through the cycle on a dark taskbar there would be a hole where
// the monitor was — that is, the icon would stop being a presence exactly while
// it is trying to say something.
func iconPixels(p Phase, size int, dim float64) []byte {
	fg, ok := phaseColor[p]
	if !ok {
		fg = phaseColor[PhaseStarting]
	}
	if dim > 0 {
		if dim > 1 {
			dim = 1
		}
		for i := range fg {
			fg[i] = fg[i]*(1-dim) + pulseTowards[i]*dim
		}
	}

	// Premultiplied top-down BGRA: it is the format CreateIconIndirect expects
	// from a 32-bit DIB, and the only one in which the alpha channel is really
	// used rather than ignored in favour of the mask.
	pix := make([]byte, size*size*4)

	c := float64(size) / 2
	// **The disc touches the edge.** The size of the box is decided by the
	// notification area's manager (SM_CXSMICON) and cannot be exceeded: giving a
	// bigger one means having it shrunk, that is, going back to blurred. The
	// only room to be gained was the margin we were leaving ourselves, and by
	// eye the icon looked smaller than its neighbours because of it.
	rDisc := c

	// The crescent: a circle minus a second circle offset up and to the right.
	//
	// **The proportions are all here and derive from rMoon**, not from size: so
	// enlarging the crescent is one number, and the shape does not change. They
	// are also the same proportions the onboarding draws it with — there it is
	// an SVG, and two drawings of the same thing diverge at the first touch
	// unless they have a ratio written down somewhere.
	rMoon := float64(size) * 0.34
	moonX, moonY := c-rMoon*0.067, c
	cutR := rMoon * 0.90
	cutX, cutY := c+rMoon*0.367, c-rMoon*0.333

	// Four samples a side: curved edges at 16 pixels without antialiasing look
	// like a staircase, and a staircase on a disc shows at once.
	const ss = 4
	for y := range size {
		for x := range size {
			var covDisc, covMoon float64
			for sy := range ss {
				for sx := range ss {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if math.Hypot(px-c, py-c) <= rDisc {
						covDisc++
						// The crescent lives only inside the disc: that way
						// there is no chance of it spilling out if one day the
						// radii move.
						if math.Hypot(px-moonX, py-moonY) <= rMoon &&
							math.Hypot(px-cutX, py-cutY) > cutR {
							covMoon++
						}
					}
				}
			}
			const tot = ss * ss
			a := covDisc / tot
			if a == 0 {
				continue
			}
			m := covMoon / tot

			// The pixel's colour is the phase, lightened towards the cream
			// where the crescent passes.
			r := fg[0]*(1-m) + moonColor[0]*m
			g := fg[1]*(1-m) + moonColor[1]*m
			b := fg[2]*(1-m) + moonColor[2]*m

			i := (y*size + x) * 4
			pix[i+0] = byte(b * a) // premultiplied
			pix[i+1] = byte(g * a)
			pix[i+2] = byte(r * a)
			pix[i+3] = byte(a * 255)
		}
	}

	return pix
}

// iconFromBGRA builds an HICON from premultiplied BGRA pixels.
func iconFromBGRA(pix []byte, size int) (windows.Handle, error) {
	hdr := bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(size),
		Height:   -int32(size), // negative = first row at the top
		Planes:   1,
		BitCount: 32,
		// BI_RGB, not BI_BITFIELDS: with 32 bits the order is already BGRA.
		Compression: 0,
	}

	var bits unsafe.Pointer
	color, _, err := procCreateDIBSection.Call(
		0, uintptr(unsafe.Pointer(&hdr)), 0 /* DIB_RGB_COLORS */, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 {
		return 0, err
	}
	defer procDeleteObject.Call(color)
	copy(unsafe.Slice((*byte)(bits), len(pix)), pix)

	// The monochrome mask is wanted anyway, even though with 32 bits it is the
	// alpha channel that decides: CreateIconIndirect demands it and takes the
	// size from it. All zero means "no pixel excluded".
	mask, _, err := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	if mask == 0 {
		return 0, err
	}
	defer procDeleteObject.Call(mask)

	info := iconInfo{FIcon: 1, HbmMask: windows.Handle(mask), HbmColor: windows.Handle(color)}
	h, _, err := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if h == 0 {
		return 0, err
	}
	return windows.Handle(h), nil
}

func destroyIcon(h windows.Handle) {
	if h != 0 {
		procDestroyIcon.Call(uintptr(h))
	}
}

// declareDPI tells Windows that we count the pixels ourselves.
//
// **Without it the icon arrives blurred on any scaled screen**, and it is the
// defect that shows: a process that declares nothing is "DPI unaware", and then
// the system lies to it for compatibility — GetSystemMetrics answers 16 even at
// 150%, we draw 16 pixels, and Windows stretches them to 24 automatically.
// Stretching a bitmap that small is exactly where blurring is noticed.
//
// Declaring it, the same call answers with the real size and we draw the icon at
// that size already: nothing scales anything. It is the same rule as the rest of
// the project — ask the system rather than work it out — applied one step
// earlier, that is, making sure the system answers truthfully.
//
// **The executable now declares it in its manifest too** (icon.AppManifest,
// written by pat-icon), which takes effect before any code runs and is what the
// App Certification Kit can read: it reported the package as not DPI aware,
// because this call goes through a DLL loaded at run time and the kit reads the
// import table. This call stays for a binary built by hand with `go build`,
// which has no resource file and so no manifest.
//
// **With the manifest present, both calls below fail harmlessly.** Measured on a
// process the manifest had made per-monitor v2: SetProcessDpiAwarenessContext
// answers access denied, SetProcessDPIAware then answers success, and the
// awareness is still per-monitor v2 after both. Windows does not lower a
// declaration already made, so the fallback cannot downgrade it. The fallback
// exists for Windows versions before 1703, where the newer function does not
// exist: there per-monitor awareness is lost and system awareness remains,
// which is still better than nothing.
func declareDPI() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	const perMonitorV2 = ^uintptr(3) // -4
	if procSetProcessDpiAwareness.Find() == nil {
		if r, _, _ := procSetProcessDpiAwareness.Call(perMonitorV2); r != 0 {
			return
		}
	}
	if procSetProcessDPIAware.Find() == nil {
		procSetProcessDPIAware.Call()
	}
}

// smallIconSize is the size the system wants for the notification area.
//
// It has to be called **after** declareDPI, otherwise it answers with the
// virtualised size.
func smallIconSize() int {
	n, _, _ := procGetSystemMetrics.Call(smCXSmIcon)
	if n <= 0 {
		return 16
	}
	return int(n)
}

// iconSizeFor asks how big the icon should be **on the screen it is on**, and
// falls back to the session's answer when it cannot know.
//
// **The session's answer is not the screen's, and that is measured.** Changing
// the display scale from 200% to 175% with the monitor running: the shell's slot
// for the icon went from 64x96 pixels to 56x84 -- exactly 175/200 -- within a
// second, while `SM_CXSMICON` stayed 32 and `GetDpiForSystem` stayed 192 for
// the twenty-five seconds it was left there. Windows fixes the system DPI at
// sign-in; the notification area is per-monitor and rescales at once. So the
// icon stayed 32 pixels wide inside a box that wanted 28, that is, resampled by
// the shell -- exactly the blur DPI awareness was declared to avoid, and the
// branch meant to prevent it could never fire, because it compared one
// session-fixed number with another.
//
// The road is the one the flyout already uses to anchor itself:
// `Shell_NotifyIconGetRect` says where the icon is, `MonitorFromRect` which
// screen that is, `GetDpiForMonitor` its scale.
//
// **The fallback is not a formality.** At `New` the icon does not exist yet, so
// there is no rectangle to ask about and the session's answer is all there is;
// it is also what remains on a Windows without `GetSystemMetricsForDpi`. Both
// give what the code gave before, which is the direction to degrade in.
func iconSizeFor(hwnd windows.Handle) int {
	if hwnd == 0 {
		return smallIconSize()
	}
	nii := notifyIconIdentifier{HWnd: hwnd, UID: 1}
	nii.CbSize = uint32(unsafe.Sizeof(nii))
	var r rect
	hr, _, _ := procShellNotifyIconGetRect.Call(
		uintptr(unsafe.Pointer(&nii)), uintptr(unsafe.Pointer(&r)))
	if int32(hr) < 0 || r.width() == 0 {
		return smallIconSize()
	}
	mon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)),
		monitorDefaultToNearest)
	if mon == 0 {
		return smallIconSize()
	}
	var dpiX, dpiY uint32
	if h, _, _ := procGetDpiForMonitor.Call(mon, mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); h != 0 || dpiX == 0 {
		return smallIconSize()
	}
	n, _, _ := procGetSystemMetricsForDpi.Call(smCXSmIcon, uintptr(dpiX))
	if n <= 0 {
		return smallIconSize()
	}
	return int(n)
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  windows.Handle
	HbmColor windows.Handle
}
