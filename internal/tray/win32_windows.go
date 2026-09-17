//go:build windows

package tray

import "golang.org/x/sys/windows"

// The Win32 calls the tray uses, declared by hand as in the rest of the
// project: `go-ole` is for COM, and there is no COM here.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	// GetDpiForMonitor lives in shcore, not in user32.
	shcore = windows.NewLazySystemDLL("shcore.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procSetTimer               = user32.NewProc("SetTimer")
	procKillTimer              = user32.NewProc("KillTimer")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	// **The size of a tray icon has no answer without a DPI.** GetSystemMetrics
	// has no argument for one and answers with the **session's** DPI, which
	// Windows fixes at sign-in: measured, twenty-five seconds at another scale
	// and SM_CXSMICON never moved. GetSystemMetricsForDpi is the same question
	// with the missing half, and the DPI to give it is that of the monitor the
	// icon is on -- which is why MonitorFromRect and GetDpiForMonitor are here.
	procGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	procMonitorFromRect        = user32.NewProc("MonitorFromRect")
	procGetDpiForMonitor       = shcore.NewProc("GetDpiForMonitor")
	procSetProcessDpiAwareness = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware     = user32.NewProc("SetProcessDPIAware")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procGetDoubleClickTime     = user32.NewProc("GetDoubleClickTime")
	procCreateIconIndirect     = user32.NewProc("CreateIconIndirect")

	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")

	procCreateDIBSection = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap     = gdi32.NewProc("CreateBitmap")
	procDeleteObject     = gdi32.NewProc("DeleteObject")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
)

// Window messages.
const (
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmCommand = 0x0111
	wmTimer   = 0x0113
	// wmDisplayChange arrives on a change of resolution or of monitor;
	// wmDpiChanged only to a per-monitor aware process, when the window ends up
	// on a screen with a different scale factor. Both are wanted: the second
	// does not cover the case where what changes is the scale of the screen we
	// are already on.
	smCXSmIcon              = 49
	monitorDefaultToNearest = 2
	mdtEffectiveDPI         = 0

	wmDisplayChange = 0x007E
	wmDpiChanged    = 0x02E0
	wmLButtonDblClk = 0x0203
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmUser          = 0x0400
	wmApp           = 0x8000
)

// Shell_NotifyIcon.
const (
	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	// **With version 4 the standard tooltip is off**, and has to be asked for.
	// The shell assumes a modern application draws its own little window on
	// hover, and sends it `NIN_POPUPOPEN` instead of showing `szTip`: whoever
	// does not draw one and does not ask for this flag is left with nothing
	// under the pointer.
	//
	// It is not a mistake visible in the code — `szTip` goes on being copied,
	// `NIM_MODIFY` goes on saying yes — and the symptom is the absence of a
	// thing, which is the hardest kind to notice.
	nifShowtip = 0x00000080

	niifInfo = 0x00000001

	// The version of the contract between us and the notification area.
	// **Without `NIM_SETVERSION` it stays at zero**, that is, at the behaviour
	// of 1996: the shell sends the raw mouse messages, the position has to be
	// guessed with `GetCursorPos`, and from the keyboard the icon cannot be
	// driven at all.
	//
	// With version 4 come `WM_CONTEXTMENU` with the **screen coordinates
	// already inside `wParam`**, and `NIN_SELECT`/`NIN_KEYSELECT` for keyboard
	// invocation — which on a computer attached to a television is the only way
	// of getting there.
	notifyIconVersion4 = 4

	// The version 4 events. `NIN_SELECT` replaces the left click and
	// `NIN_KEYSELECT` comes from Enter or Space: the shell sends the latter
	// **twice** for a single Enter, and that is documented — for us it changes
	// nothing, because opening the monitor twice opens one tab.
	//
	// They sit on `WM_USER` and not on `WM_APP`: that is the space the shell
	// has reserved for itself, and our `msgTrayIcon` sits on `WM_APP` precisely
	// so as not to land inside it.
	ninSelect    = wmUser + 0
	ninKeySelect = wmUser + 1
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// notifyIconData is NOTIFYICONDATAW in its full form (Vista onwards).
//
// The order of the fields **is the contract**: cbSize is computed with
// unsafe.Sizeof, so a field added or moved changes the size declared and Windows
// refuses the structure or, worse, reads the wrong part of it. Go's alignment
// rules and C's coincide here, and that is the only reason this declaration
// works.
type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}
