//go:build windows

package tray

import (
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"patmonitor/internal/i18n"
)

// **The three calls only this instrument makes are declared here**, and not
// beside the ones the panel itself uses: a procedure address the program never
// calls is dead weight in the binary that ships, and `deadcode` would be right
// to say so.
var (
	lookUser32                = windows.NewLazySystemDLL("user32.dll")
	procPeekMessageW          = lookUser32.NewProc("PeekMessageW")
	procGetWindowRect         = lookUser32.NewProc("GetWindowRect")
	procDwmGetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
)

// pmRemove takes the message out of the queue, which is what a loop that then
// dispatches it has to do.
const pmRemove = 0x0001

// **The panel is looked at, and this is what looks at it.** It is the pulse's
// rule one component up: a layout that reads well in the arithmetic can still
// put a command four rows from the sentence it answers, or cut a label at both
// ends, and no rereading of `layout` says so.
//
// **It has already earned its keep.** The command that opens the Windows
// settings page was moved, restyled and reworded from what the photographs
// showed; and the third label in this panel cut at both ends —
// `tray.menu.todo`, carrying Tailscale's own sentence — was found here and
// nowhere else, after two of the three had already been given an ellipsis by
// hand.
//
// It writes nothing during a normal `go test`. **A test that fills the disk of
// whoever runs it has already cost four megabytes of fake clips in the Videos
// folder**, so the path is given by whoever is looking:
//
//	PATMON_LOOK=<folder> go test ./internal/tray/ -run TestLookAtThePanel
//
// **What it cannot show is anything that depends on activation.** A panel
// opened like this never really becomes the active window, which is the same
// limitation the chapter on driving it from another process records — and the
// same reason it can be photographed at all, since the real one closes when
// anything takes the focus away.
func TestLookAtThePanel(t *testing.T) {
	out := os.Getenv("PATMON_LOOK")
	if out == "" {
		t.Skip("PATMON_LOOK not set: nothing to look at")
	}
	// Without this the system answers in virtualised coordinates and the
	// screenshot takes a different piece of screen. It is the first of the two
	// traps the chapter on looking at this panel records.
	procSetProcessDpiAwareness.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 is -4

	// The states worth a photograph: the refusal on its own, the refusal with
	// the tunnel also waiting — which is where two main actions would show — a
	// panel with nothing wrong, and the longest status line there is.
	for _, c := range []struct {
		name    string
		st      Status
		confirm bool
	}{
		{"denied", Status{
			Phase: PhaseCheck, Fault: FaultMicDenied,
			Viewers: 1, Devices: 2, Uptime: "2h14m",
			HomeURL: "http://192.168.1.42:8080/",
		}, false},
		{"denied-and-a-tunnel-step", Status{
			Phase: PhaseCheck, Fault: FaultMicDenied,
			Viewers: 1, Devices: 2, Uptime: "2h14m",
			HomeURL: "http://192.168.1.42:8080/",
			// The three go together: the sentence for the line, the code for
			// the button's label, the address for where it leads. A fixture
			// carrying two of the three draws a panel this program cannot
			// produce — which is how this one was caught, by the photograph
			// coming back without the command.
			Todo:       "approve this machine in the Tailscale admin console",
			TodoAction: "approve",
			TodoURL:    "https://login.tailscale.com/admin",
		}, false},
		{"nothing-wrong", Status{
			Phase: PhaseOutside, Viewers: 1, Devices: 2, Uptime: "2h14m",
			HomeURL:   "http://192.168.1.42:8080/",
			PublicURL: "https://patmon-1a2b3c.quercia-lieve.ts.net/",
		}, false},
		{"the-longest-line", Status{
			Phase: PhaseCheck, Fault: FaultRemoteNoIngress,
			Viewers: 0, Devices: 1, Uptime: "2h14m",
			HomeURL: "http://192.168.1.42:8080/",
		}, false},
		{"the-confirmation", Status{
			Phase: PhaseOutside, Viewers: 2, Devices: 3, Uptime: "2h14m",
			HomeURL: "http://192.168.1.42:8080/",
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := c.st
			tr := &Tray{
				cfg: Config{
					Log:      slog.New(slog.DiscardHandler),
					StatusFn: func() Status { return st },
					// The two folder glyphs are part of the shape: without them
					// the bottom of the panel is not the one that ships. The two
					// callbacks are there for the same reason — a command that
					// is not offered has no confirmation to photograph — and
					// they do nothing, because the panel is looked at and never
					// pressed.
					LogDir: lookLogDir, VideoDir: lookVideoDir,
					OnRevoke:        func() int { return 0 },
					OnResetPassword: func() error { return nil },
					SetupURL:        "http://localhost:8080/onboarding",
				},
				dictionary: i18n.Open([]string{lookLanguage()}),
			}
			f := &flyout{t: tr, dpi: 96, pills: map[pillKey]windows.Handle{}}
			if darkTheme() {
				f.pal = palDark
			} else {
				f.pal = palLight
			}
			f.url = st.PublicURL
			if f.url == "" {
				f.url = st.HomeURL
			}
			f.compose(st)
			// **The confirmation is a state of this panel and not another
			// window**, and its title was the worst truncation of the lot: the
			// question asked before cutting off everybody watching. It is
			// reached the way a press reaches it, through `askInside`, so what
			// is photographed is what the command really produces.
			if c.confirm {
				for i := range f.cmds {
					if f.cmds[i].confirm[0] != "" {
						f.pending = &f.cmds[i]
						f.lines = []string{f.cmds[i].confirm[0]}
						f.body = f.cmds[i].confirm[1]
						f.cmds = []flyCmd{
							{label: tr.t("tray.confirm.no"), stays: true},
							// **Both answers are `styleGhost`, which is what
							// `askInside` builds.** Giving "Yes" the main
							// command's style photographed a weight the
							// program never draws — 73 px against 62 in
							// Italian — that is, a picture of a panel this
							// code cannot produce, on the one instrument whose
							// whole value is that it shows what is really
							// there.
							{label: tr.t("tray.confirm.yes")},
						}
						break
					}
				}
			}
			current = f
			defer func() { current = nil }()
			if err := f.create(rect{600, 900, 640, 940}); err != nil {
				t.Fatal(err)
			}
			defer func() { f.dismiss(); pumpMessages(200 * time.Millisecond) }()
			pumpMessages(700 * time.Millisecond)

			// **`GetWindowRect` includes the invisible resize border**, so it
			// would crop what is behind the window too: the rectangle of the
			// real pixels is the extended frame bounds. It is the second of the
			// two traps the chapter records.
			var r rect
			const dwmwaExtendedFrameBounds = 9
			hr, _, _ := procDwmGetWindowAttribute.Call(uintptr(f.hwnd), dwmwaExtendedFrameBounds,
				uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))
			if int32(hr) < 0 {
				procGetWindowRect.Call(uintptr(f.hwnd), uintptr(unsafe.Pointer(&r)))
			}

			path := filepath.Join(out, "panel-"+c.name+".png")
			fh, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer fh.Close()
			if err := png.Encode(fh, screenshot(t, r)); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s  %dx%d  %s", c.name, r.width(), r.Bottom-r.Top, path)
		})
	}
}

// The two folders exist only so that their row is drawn. Nothing is opened:
// the panel is photographed, never pressed.
const (
	lookLogDir   = `C:\log`
	lookVideoDir = `C:\video`
)

// lookLanguage is the language to draw in: PATMON_LOOK_LANG, or Italian, which
// is the widest of the five in this panel.
func lookLanguage() string {
	if v := os.Getenv("PATMON_LOOK_LANG"); v != "" {
		return v
	}
	return "it"
}

// pumpMessages runs the window's own message loop for a while. The panel paints
// itself from `WM_PAINT`, so without a loop the photograph is of an empty
// window — which is not a mistake one makes twice.
func pumpMessages(d time.Duration) {
	deadline := time.Now().Add(d)
	var msg struct {
		HWnd    windows.Handle
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      point
	}
	for time.Now().Before(deadline) {
		for {
			got, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
			if got == 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// screenshot reads the pixels the screen really carries in that rectangle.
//
// **`PrintWindow` is no use here**: it answers black, because this window does
// not handle `WM_PRINTCLIENT`. The screen is the only thing that has been
// through DWM, which is where the corners, the glass and the frame come from.
func screenshot(t *testing.T, r rect) image.Image {
	t.Helper()
	w, h := int(r.width()), int(r.Bottom-r.Top)
	if w <= 0 || h <= 0 {
		t.Fatalf("the window measures %dx%d", w, h)
	}
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDC.Call(mem)

	var bi bitmapInfoHeader
	bi.Size = uint32(unsafe.Sizeof(bi))
	// A negative height is a top-down DIB: without it the rows arrive upside
	// down and the picture is a mirror nobody notices until they read a word.
	bi.Width, bi.Height = int32(w), int32(-h)
	bi.Planes, bi.BitCount = 1, 32

	var bits unsafe.Pointer
	bm, _, _ := procCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bm == 0 {
		t.Fatal("the bitmap to read into could not be created")
	}
	defer procDeleteObject.Call(bm)
	procSelectObject.Call(mem, bm)
	procBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h), screen,
		uintptr(r.Left), uintptr(r.Top), srcCopy)

	// A COLORREF is BGR, as it is everywhere else in this package.
	pix := unsafe.Slice((*byte)(bits), w*h*4)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4+0] = pix[i*4+2]
		img.Pix[i*4+1] = pix[i*4+1]
		img.Pix[i*4+2] = pix[i*4+0]
		img.Pix[i*4+3] = 0xFF
	}
	return img
}
