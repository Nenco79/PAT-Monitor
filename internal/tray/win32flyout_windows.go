//go:build windows

package tray

import "golang.org/x/sys/windows"

// The Win32 calls the panel needs, kept apart from the icon's because they are
// a block of their own: if the panel goes, this file goes whole.
//
// **Four of these do the work that looked like ours**, and they are the reason
// the panel costs what it costs rather than twice that:
//
//   - `Shell_NotifyIconGetRect` says where our icon is **now**, which with a
//     notification area that reorders itself cannot be guessed;
//   - `CalculatePopupWindowPosition` says where the window goes, **already
//     flipped at the edges and clipped to the work area**. It is the same
//     routine `TrackPopupMenu` uses inside itself, and it takes the same flags:
//     a panel positioned this way behaves like a real menu;
//   - `DwmSetWindowAttribute` gives corners, acrylic and a dark frame, all
//     documented — and it is why no undocumented `uxtheme` ordinal appears
//     here;
//   - `IsDialogMessageW` gives the keyboard: Tab, Enter, Esc and the
//     accelerators on **any** window containing controls, not only on a real
//     dialog.
var (
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procShellNotifyIconGetRect       = shell32.NewProc("Shell_NotifyIconGetRect")
	procCalculatePopupWindowPosition = user32.NewProc("CalculatePopupWindowPosition")
	procDwmSetWindowAttribute        = dwmapi.NewProc("DwmSetWindowAttribute")
	procIsDialogMessageW             = user32.NewProc("IsDialogMessageW")
	procShowWindow                   = user32.NewProc("ShowWindow")
	procSetWindowPos                 = user32.NewProc("SetWindowPos")
	procGetClientRect                = user32.NewProc("GetClientRect")
	procBeginPaint                   = user32.NewProc("BeginPaint")
	procEndPaint                     = user32.NewProc("EndPaint")
	procFillRect                     = user32.NewProc("FillRect")
	procDrawTextW                    = user32.NewProc("DrawTextW")
	procLoadCursorW                  = user32.NewProc("LoadCursorW")
	procGetDpiForWindow              = user32.NewProc("GetDpiForWindow")
	procIsWindowVisible              = user32.NewProc("IsWindowVisible")
	procSendMessageW                 = user32.NewProc("SendMessageW")
	procSetFocus                     = user32.NewProc("SetFocus")
	procGetClassNameW                = user32.NewProc("GetClassNameW")
	procGetDC                        = user32.NewProc("GetDC")
	procReleaseDC                    = user32.NewProc("ReleaseDC")
	procInvalidateRect               = user32.NewProc("InvalidateRect")
	procRedrawWindow                 = user32.NewProc("RedrawWindow")
	procSetClassLongPtrW             = user32.NewProc("SetClassLongPtrW")

	procCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procSetBkMode          = gdi32.NewProc("SetBkMode")
	procSetTextColor       = gdi32.NewProc("SetTextColor")
	procCreateFontW        = gdi32.NewProc("CreateFontW")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procBitBlt             = gdi32.NewProc("BitBlt")
)

// Styles and messages of the panel's window.
const (
	wsPopup     = 0x80000000
	wsChild     = 0x40000000
	wsVisible   = 0x10000000
	wsTabStop   = 0x00010000
	wsClipChild = 0x02000000 // WS_CLIPCHILDREN: the background does not repaint the buttons
	// **`WS_THICKFRAME` is for the corners, not for resizing.** DWM rounds a
	// window's **frame**, and a bare `WS_POPUP` has none:
	// `DWMWA_WINDOW_CORNER_PREFERENCE` is accepted — no refusal appears in the
	// log — and has nothing to act on. It is also why the Microsoft sample
	// creates its flyout with `WS_POPUP | WS_THICKFRAME`, which read without
	// this context looks like a leftover.
	//
	// The border that would come with it is removed by answering
	// `WM_NCCALCSIZE`: see flyoutProc.
	wsThickFrame = 0x00040000

	wsExToolWindow = 0x00000080
	wsExTopMost    = 0x00000008
	// **`WS_EX_NOACTIVATE` is absent, deliberately.** Without activation there
	// is no focus, so there is no keyboard — nor closing, which rests on the
	// same mechanism (`WM_ACTIVATE` with `WA_INACTIVE`). The panel **has** to be
	// able to become the foreground window.

	wmPaint      = 0x000F
	wmActivate   = 0x0006
	wmDrawItem   = 0x002B
	wmSetFont    = 0x0030
	wmEraseBkgnd = 0x0014
	wmNcCalcSize = 0x0083
	// **Removing the non-client area does not remove whoever paints it.** The
	// rectangle is decided by WM_NCCALCSIZE, but these two work the frame out
	// from the window's **style** and draw it anyway: see flyoutProc.
	wmNcPaint    = 0x0085
	wmNcActivate = 0x0086

	// The "show or hide the keyboard indicators" state. **Windows holds it**,
	// and it is driven with WM_CHANGEUISTATE: opening with the mouse asks for
	// them to be hidden, and whoever changes it announces so with
	// WM_UPDATEUISTATE.
	wmChangeUIState = 0x0127
	wmUpdateUIState = 0x0128
	uisSet          = 1

	// The class brush: it is what paints the window the **first** time, before
	// WM_PAINT has had its turn. See create.
	gclpHbrBackground = ^uintptr(9) // GCLP_HBRBACKGROUND = -10

	// Redraw **now**, and the children with it: see create.
	rdwInvalidate  = 0x0001
	rdwErase       = 0x0004
	rdwAllChildren = 0x0080
	rdwUpdateNow   = 0x0100

	waInactive = 0

	swShow = 5

	swpNoActivate = 0x0010
	swpNoZOrder   = 0x0004

	// Buttons drawn by us. **They stay real `BUTTON` controls**: only who
	// paints changes, so keyboard, focus and accessibility come free. A window
	// class of our own would lose all three.
	bsOwnerDraw = 0x0000000B

	odsSelected = 0x0001
	odsFocus    = 0x0010

	wmQueryUIState = 0x0129
	uisClear       = 2

	// **The underlines on the accelerator letters are a state, and Windows
	// holds it.** Hiding them until somebody presses Alt is the system's
	// convention; leaving them on means a line under a letter of **every**
	// label, in a panel that opens with the mouse.
	//
	// Asking is not enough, though: the buttons' text is drawn by us, and what
	// applies it is `DT_HIDEPREFIX`. See paintButton.
	uisfHideAccel = 0x0002

	// The key that makes them appear. In a real dialog `DefDlgProc` intercepts
	// it; here it falls to us, see keyboardInUse.
	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104

	// The range of keyboard messages: it is what `IsDialogMessage` looks at,
	// and everything else can be spared it.
	wmKeyFirst = 0x0100
	wmKeyLast  = 0x0109

	vkMenu = 0x12 // Alt

	idcArrow = 32512

	transparent = 1

	dtSingleLine  = 0x0020
	dtCenter      = 0x0001
	dtVCenter     = 0x0004
	dtEndEllipsis = 0x00008000
	dtNoPrefix    = 0x00000800
	// dtHidePrefix draws the accelerator letter **without** the line under it:
	// it is how UISF_HIDEACCEL is obeyed when the text is drawn by hand.
	dtHidePrefix = 0x00100000
	dtWordBreak  = 0x00000010
	// dtCalcRect measures without drawing: it is how the height of wrapping
	// text is known, rather than guessed at by counting characters.
	dtCalcRect = 0x00000400

	srcCopy = 0x00CC0020

	idCancel = 2

	// The DWM attributes, all documented.
	// DWMWA_CLOAK hides the window from composition while leaving it visible to
	// USER and GDI: WM_PAINT and WM_DRAWITEM are still delivered, so the panel
	// can be painted whole before DWM is allowed to compose a frame of it.
	dwmwaCloak                = 13
	dwmwaUseImmersiveDarkMode = 20
	dwmwaWindowCornerPref     = 33
	// **The hairline around the window is drawn by DWM, and by default it is
	// not ours.** On Windows 11 every window has a one-pixel border, which for
	// an active window takes the system colour: on an almost black panel it
	// reads as a white line. Ours is declared, and then it stops being a
	// leftover and becomes the panel's border.
	dwmwaBorderColor        = 34
	dwmwaSystemBackdropType = 38

	// **The large radius, not the small one.** Microsoft's guidance recommends
	// `DWMWCP_ROUNDSMALL` for menus, "because menus are auxiliary UI" — but
	// this is not a menu, and the transient surfaces Windows opens from its own
	// notification area (volume, network) have the full corner. What the system
	// does is watched, not only what the guidance says for another kind of
	// surface.
	dwmwcpRound = 2
	// Acrylic: the material Windows uses for transient surfaces.
	dwmsbtTransientWindow = 3

	tpmVertical     = 0x0040
	tpmVCenterAlign = 0x0010
	tpmCenterAlign  = 0x0004
	tpmWorkArea     = 0x10000
)

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) width() int32 { return r.Right - r.Left }

type point struct{ X, Y int32 }

type size struct{ CX, CY int32 }

// msgReceived is the MSG of the message loop.
//
// It has a name because there are two readers: the loop, which passes it to
// `GetMessage`, and the panel, which looks inside it to know whether a
// navigation key has arrived. Anonymous, the second would have had to declare it
// again, and two declarations of the same structure diverge without giving an
// error.
type msgReceived struct {
	HWnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// notifyIconIdentifier identifies our icon for Shell_NotifyIconGetRect. The same
// hWnd and uID we added it with: see notifyData.
type notifyIconIdentifier struct {
	CbSize   uint32
	HWnd     windows.Handle
	UID      uint32
	GuidItem windows.GUID
}

type paintStruct struct {
	Hdc         windows.Handle
	FErase      int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

// drawItemStruct is DRAWITEMSTRUCT: it arrives with WM_DRAWITEM for every button
// to be painted.
type drawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   windows.Handle
	Hdc        windows.Handle
	RcItem     rect
	ItemData   uintptr
}
