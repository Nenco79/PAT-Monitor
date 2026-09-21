//go:build windows

// Package tray puts the monitor in the Windows notification area.
//
// It exists for a precise reason: since the log goes to a file, the monitor can
// live without a console — and without a console it has **no** visible presence
// at all. A program that watches a child all night and cannot be seen anywhere
// is a program that gets left running, or that is believed to be running when it
// has exited.
//
// Hence the two properties that matter more than the menu:
//
//   - **The icon is the state.** The colour says how the monitor is without
//     opening anything. With the console off it is the only thing that says so
//     at a glance.
//   - **This is the way out.** With -H=windowsgui there is no Ctrl+C to press,
//     and a baby monitor that can only be closed from the Task Manager keeps the
//     microphone and camera busy until somebody notices.
//
// The two commands that can live nowhere else are here as well: disconnecting
// every device and **resetting a forgotten password**. They are administrative
// commands and are given from in front of the machine, where physical presence
// is already the proof of who you are — not from a URL, where for the first the
// only proof would be the credential suspected of being compromised, and for the
// second it would be exactly the one nobody remembers.
package tray

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"patmonitor/internal/guard"
	"patmonitor/internal/i18n"
	"patmonitor/internal/version"
)

// productName is the name shown to the user.
//
// **It is not written here**: it comes from `internal/version`, which is the one
// place the product's identity lives. Repeating it would be exactly the sort of
// thing that gets changed in one place and forgotten in another — and here it is
// used by the window title, the icon's tooltip and the command line.
const productName = version.Product

// Phase is how the monitor is, reduced to what a colour can say.
//
// These are codes and nobody prints their value: the sentence is chosen by
// `verdict`, the colour by `phaseColor`.
type Phase string

const (
	PhaseStarting Phase = "starting"
	PhaseHome     Phase = "home"
	PhaseOutside  Phase = "outside"
	PhaseCheck    Phase = "check"
	PhaseFault    Phase = "fault"
)

// Status is what the tray shows. It is produced by whoever uses it, on every
// opening of the menu: a menu built once would show the state as of when the
// program started, which is exactly the least interesting moment.
type Status struct {
	Phase Phase
	// Fault is what is wrong, as a **code**.
	//
	// **The words are chosen by the tray**, which is the only one that knows
	// what language it is speaking: sentences composed by `cmd/pat-monitor`
	// used to pass through here in one language, and with the language taken
	// from Windows they would have stayed in it inside a German menu. It is the
	// usual rule — codes cross the API, the words live at the edges — applied
	// to the edge that was missing.
	//
	// It goes at the top of the menu **and** in the tooltip: when something is
	// broken, it is the answer to both questions.
	Fault Fault
	// Note is a condition worth saying that is not a fault: starting up, remote
	// access that is not getting through.
	//
	// **It is only in the tooltip**, and the difference is not a matter of
	// taste. The menu is opened in order to do something, and a line describing
	// a condition with no command beside it takes the place of a command; the
	// tooltip is brushed to find out whether one can go to bed, and there that
	// condition is exactly the answer.
	//
	// When there is a Fault, **Note** wins on the tooltip if present: it serves
	// the one case where the sentence to brush is wider than a menu row —
	// "no password" in the menu, "nobody can reach the monitor" under the
	// pointer.
	Note Note
	// Who is watching and how long it has been on. **These are numbers, not
	// lines already written**: the sentence is composed by the tray, which also
	// knows the plural of the language it is saying it in.
	Viewers int64
	Devices int64
	Uptime  string
	// The addresses. Empty means the entry does not appear — a panel with
	// commands that do nothing is worse than a short panel.
	//
	// **There are three and not two, and for a while the first two were one.**
	// The two jobs do not show until that field is `localhost`, which on this
	// machine is the right address for one and the only wrong one for the
	// other:
	//
	//   - OpenURL is what the click on the icon opens, **here**. Localhost is
	//     perfectly fine, better in fact: it works without a network, and it
	//     stays the same origin as `SetupURL` — that is, the same session,
	//     instead of asking for the password a second time because the arrival
	//     was from a different address;
	//   - HomeURL is the "at home" address, the one the panel **shows** and
	//     engraves in the QR code. There localhost is the one address that
	//     certainly does not work: only this computer sees it. The system
	//     chooses it, and it arrives already chosen — see cmd/pat-monitor;
	//   - PublicURL is the funnel's address, which when present wins over
	//     HomeURL because it works from outside the house too.
	OpenURL   string
	HomeURL   string
	PublicURL string
	// UpdateVersion and UpdateURL are the newer release, when there is one.
	//
	// **They are filled whenever one exists, and the Note is not.** The two
	// answer different questions and one field would have answered the wrong
	// one: the panel's command is worth having even while something is broken —
	// a monitor whose camera keeps stopping is exactly the one whose owner
	// wants the version with the fix — while the tooltip has room for one
	// sentence, and there a fault has to win. So the command follows these, and
	// the sentence follows Note, which whoever composes the state sets only
	// when there is nothing worse to say.
	//
	// **The URL is the release's own page and is not composed here.** Built
	// from a repository name and a tag it would be a second way of naming the
	// same page, and it would point at nothing the day a tag is renamed.
	UpdateVersion string
	UpdateURL     string
	// Todo and TodoURL are the step that falls to the user, when there is one.
	//
	// They are the same thing `tunnel.State` promises with Action and ActionURL,
	// and the tray is where that promise is worth most: the icon is yellow and
	// whoever opens it is already asking "what do I have to do?". Answering with
	// the diagnosis alone would send them looking for it elsewhere.
	Todo    string
	TodoURL string
}

// Config is the things the tray cannot know by itself.
type Config struct {
	StatusFn func() Status
	// OnQuit closes the monitor. The tray does not decide how: it merely says
	// so.
	//
	// **Two things call it, and the second is not a gesture**: the panel's Quit,
	// and Windows ending the session. It must therefore be callable from the
	// message loop's thread and do nothing that waits for that loop — see the
	// WM_ENDSESSION case in wndProc.
	OnQuit func()
	// OnRevoke invalidates every session and says how many it closed.
	OnRevoke func() int
	// OnResetPassword clears the password.
	//
	// It is here and not behind a URL because whoever has forgotten it cannot
	// prove ownership with a credential: the only proof left is being in front
	// of the machine, and that is exactly what it takes to click this entry.
	OnResetPassword func() error
	// SetupURL is the guided configuration path, to be reopened when something
	// the menu does not cover has to change — switching remote access on, for
	// instance.
	SetupURL string
	// LogDir is the log's folder, to be opened in Explorer.
	LogDir string
	// VideoDir is the recordings' folder: the events and the clips asked for by
	// hand.
	//
	// It sits beside LogDir and for the same reason: **whoever is in front of
	// the machine must not have to go through a password to open a folder of
	// their own.** The clips can also be watched from `/clips`, which is the
	// road for whoever arrives from a phone; this is the one for whoever has
	// the screen in front of them.
	//
	// **The tray does not decide it**, and since the clips live in `Videos\PAT
	// Monitor` that counts: it is passed by `cmd/pat-monitor`, which takes it
	// from the store that writes them (`clips.Dir()`). Recomposing it here
	// would give two different folders for the same thing, and the panel's
	// would be the empty one — the defect already paid for with `LocalURL`,
	// recomputed by the tray while the state was already carrying it.
	VideoDir string
	Log      *slog.Logger
	// Dictionary is the words, in the language of whoever is in front of the
	// machine.
	//
	// **Whoever builds the tray passes it, rather than the tray opening it**,
	// because it is not the tray's alone: the same sentences serve
	// `cmd/pat-monitor` for the notifications it sends from outside the menu,
	// and two dictionaries opened separately could pick two different languages
	// in the same process — something that would not happen today, but that
	// nobody would go looking for when it did.
	//
	// Nil is fine: the system one is opened. It serves the tests, which have no
	// reason to know it exists.
	Dictionary *i18n.Dictionary
}

// Tray is the live icon. It is built with New and started with Run.
type Tray struct {
	cfg Config

	hwnd windows.Handle
	icon windows.Handle
	size int

	// The phase of the icon currently drawn: it is redrawn only when it
	// changes.
	shown Phase

	// The icon's pulse while remote access is being verified.
	//
	// **It is not a new field of the state, and must not become one**: what
	// says whether to beat is `NoteVerifying`, that is, the same value that
	// picks the tooltip's sentence. With two separate fields one could have the
	// icon breathing and the sentence saying something else, and that is the
	// class of divergence this program has always paid for.
	//
	// `dim` is the last drawing's dimming: it also serves for switching off,
	// because when the pulse ends the icon has to be brought back to the full
	// colour.
	pulsing   bool
	pulseFrom time.Time
	dim       float64

	// The tooltip currently shown. **Together with `shown` it is the memory
	// that makes it possible not to speak**: as long as the icon and the
	// sentence are the previous ones, there is nothing to say to the
	// notification area, which lives in another process. It was already written
	// here that "a NIM_MODIFY a second is wasted work", and only the icon's
	// drawing was skipped: the call went out anyway, every time.
	tipShown string

	// endingSession says that Windows has declared the session over.
	//
	// **It is not a copy of "we are closing": it says who else is closing.**
	// The monitor is shut down by the panel's Quit too, and there the shell is
	// alive and the icon has to come off. Here the shell is going down with us,
	// and the difference decides whether it is still worth speaking to it. It
	// is written and read on the message loop's thread only — wndProc and Run's
	// own defer — so it carries no lock.
	endingSession bool

	// v4 says whether the notification area accepted the modern contract. It
	// decides **how a message is read**, not what is done: with version 4 the
	// event is in the low word of lParam and the icon in the high one, with
	// version zero lParam is the event and nothing else. Reading one shape for
	// the other gives no error, it simply makes the menu stop working.
	v4 bool

	// Queue of balloon notifications. Shell_NotifyIcon has to be called from the
	// thread that owns the window, and whoever wants to notify is not that
	// thread: it queues and wakes the message loop.
	mu      sync.Mutex
	pending []balloon

	// lastPanel is the instant of the last opening of the panel. See panelAt.
	lastPanel time.Time

	// The instant of the last opening asked for by the icon. See activatedAt.
	lastOpen time.Time

	// The words, in the language of whoever is in front of the machine.
	dictionary *i18n.Dictionary
}

type balloon struct{ title, text string }

// Identifiers of the messages and of the fixed entries.
const (
	msgTrayIcon = wmApp + 1 // click on the icon
	msgBalloon  = wmApp + 2 // there is a notification queued

)

// New prepares the tray without showing it yet.
func New(cfg Config) *Tray {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	d := cfg.Dictionary
	if d == nil {
		d = i18n.Open(i18n.FromSystem())
	}
	// Before reading any metric: from an unaware process the system answers
	// with virtualised numbers and the icon ends up stretched.
	declareDPI()
	return &Tray{cfg: cfg, size: smallIconSize(), dictionary: d}
}

// Run shows the icon and serves its message loop for as long as the context
// lives.
//
// **It blocks the thread it runs on, and it has to.** A Win32 message queue
// belongs to the thread that created the window: if Go's runtime moved this
// goroutine elsewhere, GetMessage would stop seeing our window's messages and
// the icon would sit there answering nothing — a fault that looks in every way
// like a stuck program.
func (t *Tray) Run(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := t.createWindow(); err != nil {
		return err
	}
	defer t.destroy()

	if err := t.addIcon(); err != nil {
		return fmt.Errorf("notification area icon: %w", err)
	}
	t.resizeIcon("icon added")

	// Declaring that it is here.
	//
	// It is not noise: without a console the log is the only witness, and "the
	// icon is not there" has two opposite causes — it did not start, or it
	// started and Windows is keeping it hidden in the notification area's
	// overflow menu, which is the default for new icons. Without this line the
	// two can only be told apart by guessing.
	t.cfg.Log.Info("notification area icon active",
		"size", t.size, "state", string(t.shown))

	// Whoever closes the monitor from outside — Ctrl+C, or a fault that ends
	// the group — cannot unblock GetMessage: that function waits for a message,
	// not for a context. So it is sent one.
	stop := make(chan struct{})
	defer close(stop)
	guard.Go(t.cfg.Log, "the stop of the message loop", func() {
		select {
		case <-ctx.Done():
			procPostMessageW.Call(uintptr(t.hwnd), wmClose, 0, 0)
		case <-stop:
		}
	})

	// A one-second period: the icon follows the state without anybody having to
	// tell it, which means there is no way of forgetting about it when a new
	// fault is added.
	procSetTimer.Call(uintptr(t.hwnd), 1, 1000, 0)

	var msg msgReceived
	for {
		r, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("tray message loop: %w", err)
		}
		if r == 0 { // WM_QUIT
			return nil
		}
		// **The panel's keyboard is all in this line.** `IsDialogMessage` can
		// be used "with any window that contains controls", and from it come
		// Tab, Shift+Tab, the arrows, Enter on the default button, Esc as
		// IDCANCEL and the accelerators. If the message was for it, it has
		// already been consumed.
		if flyoutWantsMessage(&msg) {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// Notify shows a balloon over the icon. It can be called from any goroutine.
//
// It is for things that happen **once** and that whoever is watching the screen
// wants to know now: remote access opening, sessions disconnected. Not for
// continuous state, which is the icon's colour.
func (t *Tray) Notify(title, text string) {
	t.mu.Lock()
	t.pending = append(t.pending, balloon{title, text})
	t.mu.Unlock()
	if t.hwnd != 0 {
		procPostMessageW.Call(uintptr(t.hwnd), msgBalloon, 0, 0)
	}
}

// NotifyPublicAddress announces that the monitor can be reached from outside.
//
// **The text is written by the tray, not by whoever decides when to say it**,
// and that is why this method exists instead of a `Notify` with two strings. The
// words on this side are chosen by the Windows language, and a sentence composed
// outside the package would be the only one not passing through here — that is,
// the only one that would stay in one language inside a German menu, and the
// only one the key guard would not see.
//
// **Only the address.** "You still need the password" used to be added here: it
// is already known to whoever chose it — without one the tunnel would not have
// come up — and a notification that hurries to reassure raises the suspicion
// that there is something to worry about.
func (t *Tray) NotifyPublicAddress(url string) {
	t.Notify(t.t("tray.notify.remote-on"), url)
}

// ---------- window ----------

// There is one instance per process, so the callback finds it in a variable
// rather than in the window's data. If two were ever needed, this is the point
// to change — and it would be visible at once, not silent.
var (
	instance   *Tray
	instanceMu sync.Mutex

	// wmTaskbarCreated is the message Explorer broadcasts when it recreates the
	// taskbar. It has to be registered, it is not a constant.
	wmTaskbarCreated uint32
)

func (t *Tray) createWindow() error {
	instanceMu.Lock()
	if instance != nil {
		instanceMu.Unlock()
		return fmt.Errorf("the tray is already running")
	}
	instance = t
	instanceMu.Unlock()

	className, err := windows.UTF16PtrFromString("PATMonitorTray")
	if err != nil {
		return err
	}
	hinst, _, _ := procGetModuleHandleW.Call(0)

	wc := wndClassEx{
		Style:     0,
		WndProc:   windows.NewCallback(wndProc),
		Instance:  windows.Handle(hinst),
		ClassName: className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("window class registration: %w", err)
	}

	title, _ := windows.UTF16PtrFromString(productName)
	// A real hidden window, not a message-only one (HWND_MESSAGE): message-only
	// windows **do not receive broadcast messages**, and TaskbarCreated is one
	// of those. With a message-only window the icon would vanish for ever at the
	// first Explorer restart, which on Windows happens by itself.
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0, // no WS_VISIBLE: it must not appear
		0, 0, 0, 0,
		0, 0, hinst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("creating the hidden window: %w", err)
	}
	t.hwnd = windows.Handle(hwnd)

	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(name)))
	wmTaskbarCreated = uint32(r)
	return nil
}

func (t *Tray) destroy() {
	t.removeIcon()
	destroyIcon(t.icon)
	if t.hwnd != 0 {
		procDestroyWindow.Call(uintptr(t.hwnd))
		t.hwnd = 0
	}
	instanceMu.Lock()
	instance = nil
	instanceMu.Unlock()
}

// activatedAt says whether this message is a **new** activation of the icon, and
// not the echo of one an instant ago.
//
// **A double click on the icon used to open three pages.** In legacy mode — that
// is, without NIM_SETVERSION — the shell forwards the raw mouse sequence, and
// for a double click that sequence is `WM_LBUTTONUP`, `WM_LBUTTONDBLCLK`,
// `WM_LBUTTONUP`: three of the messages we treat as an opening, three browser
// windows.
//
// **It is not solved by picking one.** Keeping only the double click loses the
// single click, which is the natural gesture; keeping only `WM_LBUTTONUP` leaves
// **two** openings, because in a double click that message arrives twice. The
// right question is not which message to listen to but **when a gesture ends**,
// and Windows answers that by itself: `GetDoubleClickTime` is by definition the
// interval within which two clicks are the same gesture. So everything arriving
// inside that window is collapsed, and single click and double click both open
// one page.
//
// The value is asked of the system rather than written down: whoever slowed the
// double click in the accessibility settings did so because half a second is not
// enough for them, and they are exactly the person for whom a constant written
// here would open two pages.
//
// The instant is a parameter for the same reason as `measuredFPSAt`: it is what
// makes the rule checkable without waiting half a second per test.
func (t *Tray) activatedAt(now time.Time, within time.Duration) bool {
	return newGesture(&t.lastOpen, now, within)
}

// panelAt is the same question for the right button, and the panel needs it
// where the menu did not.
//
// **A right click sends two messages we both accept** — `WM_CONTEXTMENU` with
// version 4 and `WM_RBUTTONUP` with version zero — and as long as it opened a
// menu it cost nothing: `TrackPopupMenu` is modal, runs a loop of its own, and
// eats the second message. The panel, though, is a toggle — open if closed and
// close if open — so two messages opened and closed it again in the same
// millisecond. Measured: two "flyout: requested" for a single click, the second
// with `already_open=true`.
//
// The instant is **separate** from the left button's: a left click and a right
// click are two different gestures, and collapsing them together would make the
// second of a pair be ignored.
func (t *Tray) panelAt(now time.Time, within time.Duration) bool {
	return newGesture(&t.lastPanel, now, within)
}

// newGesture collapses everything arriving inside the double-click window.
// Written once for both buttons: two copies would diverge at the first touch,
// and here the touch would be the duration itself.
func newGesture(when *time.Time, now time.Time, within time.Duration) bool {
	if !when.IsZero() && now.Sub(*when) < within {
		return false
	}
	*when = now
	return true
}

// doubleClickTime is the system interval, with a fallback if the call fails.
func doubleClickTime() time.Duration {
	ms, _, _ := procGetDoubleClickTime.Call()
	if ms == 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

func wndProc(hwnd windows.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	instanceMu.Lock()
	t := instance
	instanceMu.Unlock()
	if t == nil {
		return defWindowProc(hwnd, msg, wparam, lparam)
	}

	switch {
	case msg == msgTrayIcon:
		t.iconEvent(eventFromLParam(lparam, t.v4), pointFromWParam(wparam, t.v4))
		return 0

	case msg == msgBalloon:
		t.drainBalloons()
		return 0

	// **The timers are two and are told apart by their identifier**, which is
	// in wParam: the one-second one rereads the state, the pulse's one merely
	// redraws. Treating them together would compose the monitor's whole state
	// five times a second to animate sixteen pixels.
	case msg == wmTimer:
		if wparam == idTimerPulse {
			t.pulse()
			return 0
		}
		t.refresh()
		return 0

	case msg == wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case msg == wmDestroy:
		procPostQuitMessage.Call(0)
		return 0

	// **The question, which is never refused.** Returning FALSE here vetoes the
	// shutdown: a baby monitor that stops the computer going off — at night,
	// behind a dialogue nobody is in front of — would be worse than anything it
	// is guarding. `DefWindowProc` would consent on its own; the case is
	// written out so that the promise is in the file rather than in what nobody
	// wrote. What it adds is the line: without it the log cannot tell the
	// sequence having started from the message never having come.
	case msg == wmQueryEndSession:
		t.cfg.Log.Info("Windows is asking to end the session",
			"reason", sessionEndReason(lparam))
		return 1

	// **The verdict, which is what the monitor acts on.** The question above is
	// put to everybody first and any one of them — another program, not us —
	// can still answer no, and then the session goes on. Closing on the question
	// would leave the room unwatched for a shutdown that was called off, and
	// what it would buy is the few seconds between the two messages.
	//
	// **Nothing here waits for the shutdown to finish, and that is deliberate
	// twice over.** This thread is the message loop, and the message loop is a
	// member of the same group the shutdown waits for: blocking would be a
	// deadlock with a timeout in front of it. And the API that really buys time,
	// `ShutdownBlockReasonCreate`, buys it by putting a screen in front of
	// whoever is turning the computer off, with our name on it as the reason it
	// will not go. So what is bounded is Windows's grace period and not ours,
	// and what is written is the start: a log that ends here says the process
	// was killed inside that period, which is a different fact from never having
	// been told.
	case msg == wmEndSession:
		if !sessionReallyEnding(wparam) {
			t.cfg.Log.Info("the end of the session was called off")
			return 0
		}
		t.cfg.Log.Info("the Windows session is ending, the monitor is closing",
			"reason", sessionEndReason(lparam))
		t.endingSession = true
		if t.cfg.OnQuit != nil {
			t.cfg.OnQuit()
		}
		return 0

	// **The icon's size is resampled, not taken once.** `t.size` is born in
	// `New`, with the scale of that moment, and the drawing is tuned to it: if
	// the scale changes while the monitor is running — the display settings, or
	// a laptop docked to a monitor with a different factor — the icon stays the
	// old size and Windows starts stretching it again. It is exactly the defect
	// DPI awareness was declared for, deferred in time: the menu's text goes on
	// scaling because Windows draws it, the icon does not because we do.
	//
	// On a program meant to stay on for days, and on a laptop, the case is not
	// theoretical. Clearing `shown` is the same gesture as the Explorer
	// restart: it forces the redraw on the next round rather than duplicating
	// its code.
	// **The arrival is written down, not only the consequence.** Logging just the
	// change leaves three cases looking the same from outside: the message never
	// came, it came and the metric had not moved, it came and the icon was
	// remade. Only the first is a defect, and it is the one a test of this
	// cannot otherwise tell from the second — the family of the `Invoke`
	// counter. **Which** of the two messages arrived is written for the same
	// reason: the stated ground for handling both is that one does not cover a
	// case, and without that word one of the two can be dead with nothing
	// saying so.
	case msg == wmDpiChanged || msg == wmDisplayChange:
		which := "WM_DISPLAYCHANGE"
		if msg == wmDpiChanged {
			which = "WM_DPICHANGED"
		}
		// **Both numbers are written, because their disagreement is the
		// point.** Measured on a scale change here: the session says 32 and
		// goes on saying it, the screen says 28 within a second. And only
		// WM_DISPLAYCHANGE ever arrived -- two changes, two messages, and
		// WM_DPICHANGED not once -- which is what the second handler was
		// written for and what nobody had seen.
		t.cfg.Log.Debug("scale message", "message", which,
			"session_says", smallIconSize(), "screen_says", iconSizeFor(t.hwnd),
			"drawn", t.size)
		t.resizeIcon(which)
		return 0

	// Explorer has been restarted and its notification area is empty: whoever
	// was there has to put itself back. Without this the icon vanishes and does
	// not return, and with -H=windowsgui every way of closing the monitor
	// vanishes with it.
	case wmTaskbarCreated != 0 && msg == wmTaskbarCreated:
		t.shown = ""
		if err := t.addIcon(); err != nil {
			t.cfg.Log.Warn("icon not restored after the Explorer restart", "error", err)
		}
		t.resizeIcon("explorer restarted")
		return 0
	}
	return defWindowProc(hwnd, msg, wparam, lparam)
}

func defWindowProc(hwnd windows.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return r
}

// ---------- icon ----------

func (t *Tray) status() Status {
	if t.cfg.StatusFn == nil {
		return Status{Phase: PhaseStarting}
	}
	return t.cfg.StatusFn()
}

func (t *Tray) notifyData(flags uint32) *notifyIconData {
	// **Whoever carries a tooltip also asks for it to be shown.** It is here
	// and not in the two places that compose it because forgetting it in only
	// one of them gives no error: the icon would be added with the little
	// window and stop having it at the first update, a second later.
	if flags&nifTip != 0 {
		flags |= nifShowtip
	}
	nid := &notifyIconData{
		HWnd:             t.hwnd,
		UID:              1,
		UFlags:           flags,
		UCallbackMessage: msgTrayIcon,
		HIcon:            t.icon,
	}
	nid.CbSize = uint32(unsafe.Sizeof(*nid))
	return nid
}

func (t *Tray) addIcon() error {
	st := t.status()
	if err := t.setIcon(st.Phase); err != nil {
		return err
	}
	tip := t.tooltip(st)
	nid := t.notifyData(nifMessage | nifIcon | nifTip)
	copyTip(&nid.SzTip, tip)
	if r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(nid))); r == 0 {
		return err
	}

	// **The version is declared after the add, and the icon was added first**:
	// `NIM_SETVERSION` talks about an icon that has to be there already.
	//
	// A refusal stops nothing. There is no Windows without version 4 among
	// those this program runs on, but if one day there were, the right answer
	// is the notification area as it used to be, not a monitor that does not
	// start: the keyboard and the exact coordinates are lost, and the rest
	// carries on because we handle the raw mouse messages anyway.
	nid = t.notifyData(0)
	nid.UVersion = notifyIconVersion4
	if r, _, err := procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(nid))); r == 0 {
		t.cfg.Log.Warn("notification area kept at the legacy contract", "error", err)
	} else {
		t.v4 = true
	}

	t.shown = st.Phase
	t.tipShown = tip
	return nil
}

func (t *Tray) removeIcon() {
	if !t.worthTellingTheShell() {
		return
	}
	nid := t.notifyData(0)
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(nid)))
}

// worthTellingTheShell says whether there is still somebody on the other end.
//
// **`Shell_NotifyIcon` is a call into Explorer, not a local one**, and this one
// runs in `Run`'s defer — that is, on the message loop's thread, which is the
// errgroup member `g.Wait()` is waiting for. At the session's end Explorer is
// being torn down at the same moment we are asking it to take an icon off a bar
// that is disappearing: if that call waits, the orderly shutdown never finishes
// inside the grace period, which is the exact symptom the session-end handler
// was written to remove. **And the work is pointless anyway**: the notification
// area goes with the session, so the icon is removed by the thing removing
// everything else — it is "a resource the system is about to reclaim is not
// worth waiting for", applied to somebody else's process.
//
// **The other writes to the shell are not gated, and that is argued rather than
// overlooked.** `refresh`, the pulse and the balloons run on the same thread and
// would block in the same way, but only inside the gap between the cancel and
// the message loop reading the `WM_CLOSE` posted for it — microseconds, against
// a timer that fires once a second. This one is not a gap: it is on the way out,
// every time. If a hang is ever measured in the timer path, that is when the
// gate widens; guessing five more branches now would be five branches nobody can
// test.
func (t *Tray) worthTellingTheShell() bool { return t.hwnd != 0 && !t.endingSession }

// refresh brings the icon and the tooltip back in line with the state of now.
//
// **The shell is written to only if something changed.** This function runs once
// a second all night, and the tooltip talks about nothing that changes every
// second: it says how the monitor is, where it can be seen and who is watching.
// Rewriting the same bytes is a call to the notification area — which is in
// another process — thirty-six thousand times in ten hours, to change nothing.
//
// The comparison is on the **text about to be shown**, not on the state: the
// state also carries things that change constantly, like how long the monitor
// has been on, which do not appear in the tooltip. Comparing `Status` would
// never skip a round. The time goes in the panel, where whoever reads it asked
// for it by opening it.
// resizeIcon redraws the icon when the screen it sits on wants another size.
//
// **It is called on the icon's arrival as well, not only on a scale change**:
// `New` computes the first size before the icon exists, so it can only have the
// session's answer -- and on a machine whose taskbar lives on a second screen
// at another scale that answer is the wrong one from the start, with no change
// of anything to correct it later.
func (t *Tray) resizeIcon(reason string) {
	n := iconSizeFor(t.hwnd)
	if n == t.size {
		return
	}
	t.cfg.Log.Info("icon size changed", "reason", reason, "was", t.size, "now", n)
	t.size = n
	t.shown = ""
	t.refresh()
}

func (t *Tray) refresh() {
	st := t.status()
	tip := t.tooltip(st)
	t.setPulse(st.Note == NoteVerifying)
	if st.Phase == t.shown && tip == t.tipShown {
		return
	}
	flags := uint32(nifTip)
	if st.Phase != t.shown {
		if err := t.setIcon(st.Phase); err != nil {
			t.cfg.Log.Warn("tray icon not updated", "error", err)
		} else {
			flags |= nifIcon
			t.shown = st.Phase
		}
	}
	nid := t.notifyData(flags)
	copyTip(&nid.SzTip, tip)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(nid)))
	t.tipShown = tip
}

// The icon's pulse: period, step and how far the dimming goes.
//
// **The step is not the smoothness one would like, it is the one that can be
// afforded.** Every frame is a `NIM_MODIFY`, that is, a message to another
// process: at twenty a second that would be twelve thousand messages to Explorer
// for a ten-minute window, for a sixteen-pixel icon.
//
// **The period is slow on purpose.** A fast blink in the notification area is
// the vocabulary of "look here now", and this is not an alarm: it is a wait that
// resolves itself, and in the normal case lasts eleven seconds.
//
// **Amplitude and cadence are two different knobs, and they were swapped
// twice.** The first draft oscillated between two nearby greens at five frames a
// second: the report was "I think I saw the blinking but it is between two
// identical colours", that is, **amplitude** was missing — and the temptation,
// faced with a movement that goes unnoticed, is always to add frames. With the
// excursion taken to the bottom, the defect became its opposite: it was seen,
// and seen **in steps**.
//
// **How many frames actually arrive is not a matter of opinion, and it is not
// obvious either**: the icon is redrawn by another process, and if the shell
// coalesced them, sending more would be wasted work. Measured by sampling the
// icon's pixels on screen at ~117 Hz while the pulse ran at twenty frames a
// second:
//
//	sent        every 50 ms
//	on screen   every 57-68 ms, that is, ~15-17 a second
//	excursion   G-B from 0 to 78, that is, black and full green for real
//
// So the taskbar redraws every 57-68 ms and coalesces above that. **The step is
// 60 ms, that is, what the taskbar delivers**: it is the most that can be shown.
//
// Choosing ten a second "to stay under" the ceiling is the wrong knob again, in
// the opposite direction: it optimises the messages to Explorer instead of the
// smoothness, which is the only thing this animation exists for. Staying under a
// ceiling is not a virtue when the ceiling **is** the target.
//
// The minimum of twelve frames per cycle stays written in the test: the wider
// the run, the more steps it takes to cover it without jumps.
const (
	pulsePeriod = 3600 * time.Millisecond
	pulseStep   = 60 * time.Millisecond
	pulseDepth  = 1.0
)

// idTimerPulse is the second timer, and it needs one of its own.
//
// Speeding up the one-second one would mean calling `status()` five times a
// second — that is, composing the monitor's whole state, locks included, to
// redraw sixteen pixels. **An animation's cadence cannot dictate a
// measurement's.**
const idTimerPulse = 2

// setPulse switches the pulse on or off, and writes only when it changes.
func (t *Tray) setPulse(want bool) {
	if want == t.pulsing {
		return
	}
	t.pulsing = want
	if want {
		t.pulseFrom = time.Now()
		procSetTimer.Call(uintptr(t.hwnd), idTimerPulse,
			uintptr(pulseStep/time.Millisecond), 0)
		return
	}
	procKillTimer.Call(uintptr(t.hwnd), idTimerPulse)
	// **Switching the pulse off is not enough: the icon has to be brought back
	// to the full colour.** Without that, the last frame drawn remains — a
	// green dimmed by chance, that is, a colour that means nothing and never
	// goes away.
	if t.dim != 0 {
		t.dim = 0
		t.repaintIcon()
	}
}

// pulse draws one frame of the beat.
//
// The curve is a raised cosine: it starts and returns to zero without corners,
// which is the difference between a breath and a blink. The time is read from
// the clock and not from a frame counter, so a timer that skips a beat does not
// shift the phase.
func (t *Tray) pulse() {
	if !t.pulsing {
		return
	}
	t.dim = pulseDim(time.Since(t.pulseFrom))
	t.repaintIcon()
}

// pulseDim is the dimming at a given point of the cycle.
//
// **It starts and ends at zero**, which is the property not to lose: the cycle
// passes through the full colour, so the viewer sees the true green on every
// round instead of a dimmed green oscillating. It is also why the curve is a
// raised cosine and not a ramp — a ramp would have a corner at every period, and
// a corner reads as a blink.
func pulseDim(elapsed time.Duration) float64 {
	phase := math.Mod(float64(elapsed), float64(pulsePeriod)) / float64(pulsePeriod)
	return pulseDepth * (1 - math.Cos(2*math.Pi*phase)) / 2
}

// redrawFlags are the fields every update of the icon has to carry.
//
// **`nifTip` is there even when the tooltip does not change**, and that is not
// redundancy: with the version 4 contract the standard little window is off and
// is asked for with `NIF_SHOWTIP`, which `notifyData` adds **only** to whoever
// carries `nifTip`. An icon-only `NIM_MODIFY` therefore does not leave the
// tooltip as it was: **it takes it away**.
//
// It is the defect this file already described — "an icon born with the little
// window and losing it at the first update" — reintroduced by the pulse, which
// updates sixteen times a second: the report was "while it blinks no tooltip
// appears". **A rule written next to the code does not protect the code written
// afterwards**, and that is why the flags now live in a constant with a test
// over them.
const redrawFlags = nifIcon | nifTip

// repaintIcon redraws the icon with the current dimming and delivers it to the
// notification area, bringing back the tooltip that was already in force.
func (t *Tray) repaintIcon() {
	if err := t.setIcon(t.shown); err != nil {
		t.cfg.Log.Warn("tray icon not updated", "error", err)
		return
	}
	nid := t.notifyData(redrawFlags)
	copyTip(&nid.SzTip, t.tipShown)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(nid)))
}

// setIcon replaces the icon, destroying the previous one **after** the new one
// exists: an HICON destroyed while it is still the notification area's leaves an
// empty rectangle.
func (t *Tray) setIcon(p Phase) error {
	h, err := makeIcon(p, t.size, t.dim)
	if err != nil {
		return err
	}
	old := t.icon
	t.icon = h
	destroyIcon(old)
	return nil
}

func (t *Tray) tooltip(st Status) string {
	// **The tooltip is what the onboarding draws**, and for a while it was not:
	// the configuration path shows an illustration of the tray reading "All
	// well" and "At home and from outside · 1 viewer", and whoever got to the
	// end then found "viewers: 0 · devices connected: 1". It is the same family
	// as the fake QR code: a drawing promising something the product does not
	// do.
	//
	// Three lines, in this order: **how it is**, **where it can be seen and who
	// is watching**, and finally who it is — name and version, which live here
	// and not in the menu because this is the only place reachable without
	// clicking anything and without knowing the password, and it is the first
	// thing needed by whoever is helping somebody else over the phone.
	//
	// **All in 127 characters**: `szTip` is a fixed buffer and `copyTip`
	// truncates without saying anything. The limit holds for **every language**,
	// and that is why the test runs over all the catalogues rather than one: a
	// German sentence is on average a third longer, and whoever overflows finds
	// out with their own name cut in half.
	return strings.Join([]string{
		t.verdict(st.Phase),
		t.summary(st),
		productName + " " + version.Short(),
	}, "\n")
}

func (t *Tray) drainBalloons() {
	t.mu.Lock()
	queue := t.pending
	t.pending = nil
	t.mu.Unlock()

	for _, b := range queue {
		nid := t.notifyData(nifInfo)
		copyInfo(&nid.SzInfoTitle, b.title)
		copyBig(&nid.SzInfo, b.text)
		nid.DwInfoFlags = niifInfo
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(nid)))
	}
}

// ---------- actions ----------

// Open opens an address or a folder with the default program.
//
// **It is exported because two packages want the same act, and it is the one
// road out to the shell**: the panel's commands and the icon's click, here, and
// the guided setup, which `cmd/pat-monitor` opens at the first start. The tray
// is only where the other caller happens to live — what is kept here is the act,
// not the icon.
//
// An opening that fails **stops nothing**: the failure is returned, and each
// caller decides whether it is worth a line of its log — the guided setup is
// not refused because a browser did not open.
func Open(target string) error {
	if target == "" {
		return errors.New("nothing to open")
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return fmt.Errorf(`the "open" verb cannot be given to the shell: %w`, err)
	}
	p, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("%q cannot be given to the shell: %w", target, err)
	}
	const swShowNormal = 1
	code, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(p)), 0, 0, swShowNormal)
	return shellResult(code)
}

// shellResult reads what ShellExecute returned.
//
// **The documentation has it compared to 32, and says not to read it as
// anything else**: the return is an HINSTANCE only for backward compatibility
// with 16-bit Windows, and what it really carries is "greater than 32" for a
// success and one of the SE_ERR_* codes — access denied, no association, and the
// rest — for a failure. It is a reading of its own, and it is written down
// because a tidy-up that replaced it with `if err != nil` would report a browser
// that opened as a failure: `Proc.Call` returns what `syscall.SyscallN` gave it,
// an `Errno` that is non-nil even when it is zero.
func shellResult(code uintptr) error {
	if code <= 32 {
		return fmt.Errorf("the shell returned %d, which is its failure range", code)
	}
	return nil
}

// open opens an address or a folder with the default program, and writes the
// failure down.
func (t *Tray) open(target string) {
	if err := Open(target); err != nil {
		t.cfg.Log.Warn("open failed", "what", target, "error", err)
	}
}

// setClipboard puts some text on the clipboard.
//
// It serves the one menu entry with a real reason to exist: the public address
// has to be transferred to a phone, and transcribing it by hand from a screen is
// the quickest way of getting it wrong.
func setClipboard(s string) error {
	utf16, err := windows.UTF16FromString(s)
	if err != nil {
		return err
	}
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return err
	}
	defer procCloseClipboard.Call()

	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		return err
	}

	n := uintptr(len(utf16) * 2)
	const gmemMoveable = 0x0002
	h, _, err := procGlobalAlloc.Call(gmemMoveable, n)
	if h == 0 {
		return err
	}
	url, _, _ := procGlobalLock.Call(h)
	if url == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("could not lock the clipboard memory")
	}

	// GlobalLock returns an address, and to Go an address is a uintptr.
	//
	// `go vet` flags every uintptr -> unsafe.Pointer conversion, and in the
	// general case it is right: a uintptr keeps nothing alive, so between the
	// conversion and the use the collector could have moved or freed the object
	// pointed at. Here no Go object is involved — the memory is GlobalAlloc's,
	// that is, the system's, and the collector neither knows nor touches it. So
	// the bits are reinterpreted rather than converted, and this is the only
	// line in the project where that happens: if a second one appeared
	// elsewhere, it would deserve the same suspicion vet applies to this one.
	dst := *(*unsafe.Pointer)(unsafe.Pointer(&url))
	copy(unsafe.Slice((*byte)(dst), n),
		unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), n))
	procGlobalUnlock.Call(h)

	const cfUnicodeText = 13
	// From here on the memory belongs to the system: it must not be freed, and
	// freeing it would be a use after release inside Windows.
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return err
	}
	return nil
}

func copyTip(dst *[128]uint16, s string) { copyUTF16(dst[:], s) }
func copyInfo(dst *[64]uint16, s string) { copyUTF16(dst[:], s) }
func copyBig(dst *[256]uint16, s string) { copyUTF16(dst[:], s) }

// copyUTF16 fills a fixed-size buffer, always leaving the trailing zero.
func copyUTF16(dst []uint16, s string) {
	src, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(src) > len(dst) {
		src = src[:len(dst)]
	}
	copy(dst, src)
	dst[len(dst)-1] = 0
}

// NotifyUpdate announces that a newer release exists, once.
//
// **The text is written here for the reason NotifyPublicAddress states**: the
// words on this side are chosen by the Windows language, and a sentence
// composed by whoever decides when to say it would be the only one not passing
// through the key guard.
//
// **It says the number and nothing else.** There is no download to wait for and
// no button to hurry towards: the version is what tells its reader whether this
// matters to them, and the rest — what changed, whether to bother — is on the
// page the panel's command opens. A notification that explains is a
// notification read after the thing it explains has scrolled away.
func (t *Tray) NotifyUpdate(version string) {
	t.Notify(t.t("tray.notify.update"), t.t("tray.notify.update.body", "version", version))
}
