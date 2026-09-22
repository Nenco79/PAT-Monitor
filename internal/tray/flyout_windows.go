//go:build windows

package tray

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"patmonitor/internal/qr"
)

// The tray's panel: what the right button opens in place of the menu.
//
// **The five questions that could not be settled by reading all answered yes**,
// on the real machine, and they are why this file exists rather than having been
// deleted:
//
//   - `Shell_NotifyIconGetRect` answers `S_OK` with a real rectangle, and
//     `CalculatePopupWindowPosition` flips the panel upwards so that it lands
//     exactly above the icon. Measured: icon at 2340,1704 of 64x96, panel 480x636
//     placed at 2132,1068;
//   - it always closes, on Esc and on a click outside. Nine openings, nine
//     closings;
//   - the code can be scanned;
//   - `IsDialogMessage` gives the keyboard on a window of a class of ours: Tab
//     goes round;
//   - **Narrator reads the `BS_OWNERDRAW` buttons.** That is the answer that
//     authorises removing the system menu, because it removes the one thing the
//     menu gave that the panel could not.
//
// And all three DWM attributes were accepted — corners, dark frame and acrylic —
// so **there is not one undocumented ordinal in this file**: the road that
// looked as though it needed the undocumented API is the one that avoids it.
//
// What is not in doubt and has been here from the start: the window is
// **opaque**, and the glass is put behind it by DWM. With per-pixel alpha the
// GDI text would come out fringed, because ClearType antialiases against a
// background it presumes to be black and knows nothing of the alpha channel. It
// is the constraint that, got wrong, is paid for with a rewrite.

// The sizes are in points at 96 dpi and scale with the screen.
const (
	flyWidth   = 260
	flyPadding = 12
	flyQRSide  = 150
	// The same radius as the QR panel in the pages, where the code sits in a
	// 168 px square with `border-radius: 8px`.
	flyQRRadius = 8
	flyGap      = 10
	flyLine     = 20

	// The two button sizes. The viewer asks for 48 because they are pressed
	// with a finger, in the dark, one-handed; here they are pressed with the
	// mouse, and 48 would make a panel twice as tall as it needs to be.
	//
	// **One height for every button.** There were two, tall for the frequent
	// commands and short for the rare ones, and from outside that reads as a
	// crooked grid rather than as a hierarchy: the difference between one
	// command and another is already said by the colour and the weight of the
	// text.
	flyButton    = 40
	flyButtonGap = 6
	// The address is not a button and does not have its height, but it **is a
	// control**: it is pressed to copy. See styleText.
	flyAddress = 26

	// The size of the glyph inside a button that carries no words.
	//
	// In the pages `.ico` is 16 beside a word; here there is no word, and a
	// 16-pixel glyph in the middle of a 40-tall pill reads as a mark left there
	// by mistake. Twenty is the size at which the glyph **is** the command, and
	// it stays inside the flat centre of the pill — which is the condition that
	// allows it to be engraved on its background rather than over it.
	flyIcon = 20

	// The text sizes are the stylesheets' tokens — `--t-corpo`, `--t-ui`,
	// `--t-small` — and the weights are those of the buttons' rules: 600 on the
	// pill, 500 on the secondary button. GDI does not read CSS, but the numbers
	// are those and no others.
	tBody  = 16
	tUI    = 14
	tSmall = 13

	idFlyBase = 200
)

// palette is the panel's colours, in the same two sets as the pages': dark like
// the viewer, light like the onboarding.
//
// **The Windows theme chooses, not us.** Having both faces already is what makes
// "the style is ours" compatible with "the theme is decided by whoever uses the
// PC": the right one is pulled out rather than one being imposed.
type palette struct {
	dark                                   bool
	ground, card, line, ink, muted, accent uint32
}

// The values are the stylesheets', converted to COLORREF.
var (
	palDark = palette{
		dark:   true,
		ground: rgb(0x17, 0x15, 0x12),
		card:   rgb(0x21, 0x1E, 0x1A),
		line:   rgb(0x32, 0x2D, 0x26),
		ink:    rgb(0xF0, 0xEB, 0xE2),
		muted:  rgb(0x94, 0x8B, 0x7C),
		accent: rgb(0x3F, 0xA8, 0xA4),
	}
	palLight = palette{
		ground: rgb(0xE5, 0xDF, 0xD3),
		card:   rgb(0xFB, 0xF8, 0xF2),
		line:   rgb(0xD3, 0xC9, 0xB6),
		ink:    rgb(0x2E, 0x2A, 0x24),
		muted:  rgb(0x6A, 0x62, 0x56),
		accent: rgb(0x17, 0x70, 0x6E),
	}
)

// rgb builds a COLORREF, which is BGR and not RGB: swapping them gives no
// error, it gives an interface with inverted colours.
func rgb(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

// flyStyle is how a command is drawn.
type flyStyle int

const (
	// styleGhost is `button.ghost`: transparent, a hairline of `--line` around
	// it, muted text, weight 500. It is the style of everything else, and it is
	// **the zero value** on purpose: a command added without saying anything
	// comes out discreet, not coloured. The other way round, the first one to
	// forget the style would take the filled pill, and there would be two.
	styleGhost flyStyle = iota
	// stylePill is the main command, weight 600. **There is one per panel**, as
	// in the pages, where every screen has one main action and only one — and
	// here it is there only when there really is something to do.
	//
	// **It paints nothing of its own, and what marks it is the focus.**
	// `paintButton` has no branch for it: a main command that is not selected
	// is drawn exactly like its neighbours, and `focusIndex` is what makes sure
	// it is selected. So this style says *prefer me*, not *fill me*.
	//
	// It used to wear a solid accent, under a comment here calling it "the
	// phase's colour" while the code took `pal.accent`, which the Windows theme
	// chooses and the phase does not — a sentence that could not fail,
	// describing something the code had never done. What the solid fill
	// produced is the report that found it: the same token reads dark green at
	// 22% over near-black and bright cyan at 100%, so the panel appeared to
	// hold two accents, one belonging to nothing. Then painting it *and*
	// painting the focus gave two marked buttons whenever both existed, which
	// is why the paint went and the preference stayed.
	stylePill
	// styleText has neither background nor border: it is the address, which is
	// pressed to copy it. **It stays a real control** even though it does not
	// look like a button, because otherwise neither Tab nor Narrator would
	// reach it.
	styleText
	// styleIcon is `button.ghost` without the word: the glyph is in its place.
	//
	// **The word does not vanish, it stops being drawn.** The control's text
	// remains, so Narrator reads it and the accelerator letter goes on
	// answering: it is the same distinction as the address, where what is shown
	// is not what is used. Taking it off the control would give two buttons
	// with no name from the keyboard, and this panel is also the interface of a
	// computer attached to a television.
	styleIcon
)

// flyCmd is a thing that can be done. It follows the menu's entries, including
// the two rules that govern them: the ones that would do nothing are not there,
// and the ones that cannot be undone ask for confirmation.
type flyCmd struct {
	label string
	do    func()
	// confirm, when filled, is what is asked before executing. The confirmation
	// sits **inside the panel**: see askInside.
	confirm [2]string
	// style is how it is drawn. **Only one is filled**, as in the pages, where
	// `button` is the coloured pill and `button.ghost` everything else: three
	// coloured pills in a row read as three selected things.
	style flyStyle
	// icon is the glyph drawn in place of the word. It applies with styleIcon
	// and with no other style.
	icon *glyph
	// share says this command divides the row with the one before it instead of
	// sitting below.
	//
	// **The row is decided by whoever composes, not by whoever lays out**: two
	// commands side by side are a choice of meaning — opening one folder or the
	// other is the same thing done to two places — and not a way of making
	// things fit. Whoever lays out merely divides the width.
	share bool
	// lead says this command is drawn between the status lines and the QR code
	// instead of in the column at the bottom.
	//
	// **It is for a command that answers the line above it**, and there is one:
	// the Windows settings page for a permission the notice has just declared
	// off. Down in the column it would be four rows away from the sentence it
	// belongs to, with the QR code and the address in between — measured by
	// looking, which is the only instrument for this.
	//
	// It is deliberately not a general slot: `layout` gives it a single row,
	// because a second one would be a second main answer and the panel has one.
	lead bool
	// stays says the panel does not close when this command runs.
	//
	// **There is one, and it is the confirmation's "No"**, which puts the panel
	// back as it was: without this distinction it was destroyed an instant
	// earlier and `cancel` rebuilt the buttons on a window that no longer
	// existed — seven `CreateWindowEx` with a null parent, and an
	// `InvalidateRect(NULL)` which by specification redraws **every window on
	// the desktop**. The defect showed in no error: Esc, which comes by another
	// road, did the right thing.
	stays bool
}

// pillKey is everything that tells one shape from another: two pills with the
// same key are the same image, and it is drawn once for all.
type pillKey struct {
	w, h         int32
	fill, border uint32
}

type flyout struct {
	t    *Tray
	hwnd windows.Handle
	pal  palette
	dpi  int32

	url    string
	anchor rect // where the icon is: needed at every repositioning, not only the first
	qrBm   windows.Handle
	qrPx   int32
	// The fonts, one per role, with the sizes and weights of the stylesheets:
	// the confirmation's question 600/16, the main command 600/14, the
	// secondary button 500/14, the lines 400/14, the address 400/13. See
	// makeFonts for why the first two are not one.
	fontTitle windows.Handle
	fontMain  windows.Handle
	fontGhost windows.Handle
	fontLine  windows.Handle
	fontSmall windows.Handle

	lines []string
	// body is the long text of the confirmation question. **It wraps**, and
	// that is why it is separate from the lines: those wrap over two rows at
	// most, this one over as many as it needs — and placed among them it was cut
	// in half without saying so, on the very sentence warning that the operation
	// cannot be undone.
	body  string
	bodyH int32
	// lineH is how tall each status line is, in pixels: one row, or two when the
	// sentence does not fit in one.
	//
	// **It is measured, in `rebuild`, and read by `layout`**, which has three
	// readers of its own and no device context to ask. Empty means nobody has
	// measured yet, and then every line is worth one row — which is the shape
	// the panel had before, so the fallback is the old behaviour and not a
	// guess.
	lineH []int32
	cmds  []flyCmd
	// buttons are the live controls, and they sit **outside cmds** on purpose.
	//
	// Keeping the handle inside `flyCmd`, whoever changes the content replaces
	// the slice and the handles with it: the loop that ought to have destroyed
	// the old buttons found none, and those stayed on the screen
	// **clickable**, with ids that by then indexed the new list. In the
	// confirmation question the old "Copy address" would have become the "Yes",
	// that is, it would have disconnected everybody without anybody having
	// confirmed anything. The lifetime of windows cannot live inside the data
	// that describes them, because that data gets replaced.
	buttons []windows.Handle

	// pending is the command that asked for confirmation. Until it is nil the
	// panel shows the question in place of its content.
	pending *flyCmd

	// pills are the shapes already composed, by appearance. See pill.
	pills map[pillKey]windows.Handle
	// glyphs are the engravings already made, by glyph, size and pair of
	// colours. Same reason as the pills, and same lifetime: release frees them.
	glyphs map[glyphKey]windows.Handle

	// hideAccel is the other half of the same state: the underlines on the
	// accelerator letters.
	//
	// **Asking Windows to hide them does not hide them**, and that is the trap:
	// the buttons' text is drawn by us, so the state is communicated to us but
	// applying it falls to us — `DT_HIDEPREFIX`. Without that, `O&pen` comes out
	// underlined whatever the state says, and the request looks refused when it
	// was accepted and ignored by whoever had to carry it out.
	hideAccel bool
}

var (
	current       *flyout
	flyClassReady bool

	// The background brush belongs to the **class**, not to the window: it
	// lives as long as the process and is remade only if the theme changes. See
	// classBackground.
	flyBackground      windows.Handle
	flyBackgroundColor uint32
)

// darkTheme asks Windows whether applications go dark.
//
// The system is asked rather than deduced from, as always. The key is the one
// the rest of Windows reads too; absent means light, which is the historical
// default.
func darkTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return v == 0
}

// showFlyout opens the panel above the icon. The right button calls it, in place
// of the menu.
func (t *Tray) showFlyout(where spot) {
	t.cfg.Log.Debug("flyout: requested", "already_open", current != nil && current.hwnd != 0)
	// **This is not the normal road to closing**, and it is worth saying: a
	// click on the icon takes the activation off the panel before the shell
	// even sends us the notification, so we usually get here with the panel
	// already closed and what stops the reopening is the instant noted in
	// wmDestroy. This branch covers the opposite order, which nobody guarantees
	// us.
	if current != nil && current.hwnd != 0 {
		current.dismiss()
		return
	}

	st := t.status()
	f := &flyout{t: t, dpi: 96, pills: map[pillKey]windows.Handle{}}
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

	// **The panel anchors to the icon, not to the pointer.** From the menu the
	// position arrived in the event; here where the icon is **now** is asked
	// for, because the notification area reorders itself and on Windows 11 a
	// new icon is born in the overflow. Measured: it answers S_OK with the real
	// rectangle.
	nii := notifyIconIdentifier{HWnd: t.hwnd, UID: 1}
	nii.CbSize = uint32(unsafe.Sizeof(nii))
	var iconRect rect
	hr, _, _ := procShellNotifyIconGetRect.Call(
		uintptr(unsafe.Pointer(&nii)), uintptr(unsafe.Pointer(&iconRect)))
	if int32(hr) < 0 || iconRect.width() == 0 {
		// The fallback is the position the shell sends, and last of all the
		// pointer: it is the menu's own ladder.
		pt := point{where.X, where.Y}
		if !where.Known {
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		}
		iconRect = rect{pt.X, pt.Y, pt.X + 1, pt.Y + 1}
		t.cfg.Log.Debug("flyout: no icon rectangle, anchoring on the pointer",
			"hresult", fmt.Sprintf("0x%08X", uint32(hr)))
	}

	// **Registered before the window is created, not after.** The window
	// procedure finds the panel from here: if `current` were still nil during
	// creation, every message arriving while the window is born — activation
	// included — would fall on DefWindowProc, and the panel would behave one way
	// while being born and another immediately afterwards.
	current = f
	if err := f.create(iconRect); err != nil {
		t.cfg.Log.Error("flyout: not opened", "error", err)
		f.release()
		current = nil
		return
	}
}

// compose builds the lines and commands from the state, by the same rules as
// the menu: first how it is, then what is missing, then what can be done.
func (f *flyout) compose(st Status) {
	t := f.t
	f.lines = t.lines(st)
	f.body = ""
	f.cmds = nil

	// **A permission Windows is refusing is answered directly under the line
	// that states it**, and that is the whole reason `lead` exists: the notice
	// says *microphone: permission is off* and the command under it says what
	// pressing does. Anywhere else in the column it is a command about a
	// sentence four rows above it — the first version landed between the QR
	// code and the address, which reads as belonging to the address.
	//
	// **It is drawn like every other command**, which is the panel's own rule
	// about marks: the selected command already wears the accent, so anything
	// added on top of it would be two marks for one thing.
	//
	// **And it is no longer the one the focus falls on.** It is the `lead`,
	// drawn between the lines and the code; with a step also waiting the focus
	// goes to the step, because the mark follows the focus, the panel has one,
	// and the step is what the monitor cannot get past on its own. With nothing
	// waiting this is the first command and the focus is here. See focusIndex.
	//
	// It is the one case where this panel is the **only** place the thing can be
	// done: the switch is in this machine's Settings, so whoever watches from a
	// phone can be told about it and can do nothing whatever. It is the argument
	// that already keeps the password reset and the session revocation off the
	// HTTP routes.
	if url := settingsPage(st.Fault); url != "" {
		f.cmds = append(f.cmds, flyCmd{
			label: t.t("tray.menu.settings"),
			lead:  true,
			do:    func() { t.open(url) },
		})
	}
	// **What is missing comes first**: if there is a step to take, that is why
	// the panel was opened, and it is the only one that deserves the filled
	// pill. If nothing is missing the panel has no main action, and then none is
	// coloured.
	if key := todoCommand(st.TodoAction); key != "" && st.TodoURL != "" {
		url := st.TodoURL
		f.cmds = append(f.cmds, flyCmd{
			label: t.t(key),
			style: stylePill,
			do:    func() { t.open(url) },
		})
	}
	// **The address is copied by pressing the address**, not a button beside it
	// saying so. The text is already there, and one command fewer is one line
	// fewer to read: it is the same rule by which entries that do nothing are
	// not there.
	if f.url != "" {
		url := f.url
		f.cmds = append(f.cmds, flyCmd{label: forDisplay(url), style: styleText, do: func() {
			if err := setClipboard(url); err != nil {
				t.cfg.Log.Warn("address not copied", "error", err)
				return
			}
			t.Notify(t.t("tray.notify.copied"), url)
		}})
	}
	// **"Open the monitor" is not here, and it is not an oversight.** The left
	// button on the icon already does it, and that is the shortest gesture there
	// is: an entry repeating a simpler gesture takes the place of a command
	// without adding anything. It is the same rule by which entries that do
	// nothing are not there.

	// **A newer version is news, not a step to take**, so it sits after the
	// tunnel's pending action and takes no pill: the filled one is for the one
	// thing the monitor is waiting on somebody for, and there is at most one of
	// those. Here nothing is waiting — the monitor works, and this is something
	// its owner may want to know about while they are in front of it.
	//
	// **It appears even while something is wrong**, unlike the sentence in the
	// tooltip, which a fault wins. The case that settles it is the one that
	// matters most: a monitor whose camera keeps stopping is exactly the one
	// whose owner wants the release with the fix, and hiding the command behind
	// the fault would take it away in the hour it is worth having.
	//
	// It leads to the release page and nothing else. The monitor downloads
	// nothing: whoever is in front of the machine reads what changed and
	// decides, which is the same division as every other administrative command
	// here.
	if st.UpdateVersion != "" && st.UpdateURL != "" {
		url := st.UpdateURL
		f.cmds = append(f.cmds, flyCmd{
			label: t.t("tray.menu.update", "version", st.UpdateVersion),
			do:    func() { t.open(url) },
		})
	}
	if t.cfg.SetupURL != "" {
		url := t.cfg.SetupURL
		f.cmds = append(f.cmds, flyCmd{label: t.t("tray.menu.setup"),
			do: func() { t.open(url) }})
	}
	if t.cfg.OnResetPassword != nil {
		setup := t.cfg.SetupURL
		f.cmds = append(f.cmds, flyCmd{
			label:   t.t("tray.menu.reset"),
			confirm: [2]string{t.t("tray.confirm.reset.title"), t.t("tray.confirm.reset.body")},
			do: func() {
				if err := t.cfg.OnResetPassword(); err != nil {
					t.cfg.Log.Error("password not reset", "error", err)
					t.Notify(t.t("tray.notify.reset-failed"), err.Error())
					return
				}
				if setup != "" {
					t.open(setup)
				}
			},
		})
	}
	if t.cfg.OnRevoke != nil {
		f.cmds = append(f.cmds, flyCmd{
			label:   t.t("tray.menu.revoke"),
			confirm: [2]string{t.t("tray.confirm.revoke.title"), t.t("tray.confirm.revoke.body")},
			do: func() {
				n := t.cfg.OnRevoke()
				t.Notify(t.t("tray.notify.revoked"),
					t.t("tray.notify.revoked.body", "sessions", t.sessionsText(n)))
			},
		})
	}
	// **The two folders are two glyphs side by side, and they sit just above
	// "Quit".** They open nothing of the monitor: they lead to what the monitor
	// has **left behind** — the event clips and the log — so they go at the
	// bottom, after the commands that act on it and before the only one that
	// switches it off. Two whole words for two destinations that are rarely
	// opened lengthened the panel by eighty pixels to say "folder" twice: what
	// separates them is what is inside, and a glyph says that better than a
	// line.
	f.cmds = append(f.cmds, t.folders()...)

	f.cmds = append(f.cmds, flyCmd{label: t.t("tray.menu.quit"), do: func() {
		if t.cfg.OnQuit != nil {
			t.cfg.OnQuit()
		}
	}})
}

// folders gives the commands that open the two folders.
//
// **If a glyph could not be read those commands go back to being whole rows with
// their word.** An empty button says nothing, and it is exactly the sort of
// thing that does not protest: the `d` is in a constant, so either it always
// reads or it never does, and the second half is caught by
// `TestEveryGlyphOfThePanelCanBeRead` before the panel exists. The fallback
// stays because it costs one line and turns a mutilated panel into a wordy one.
func (t *Tray) folders() []flyCmd {
	var out []flyCmd
	if t.cfg.VideoDir != "" {
		dir := t.cfg.VideoDir
		out = append(out, flyCmd{label: t.t("tray.menu.videos"), icon: glyphVideo,
			do: func() { t.open(dir) }})
	}
	if t.cfg.LogDir != "" {
		dir := t.cfg.LogDir
		out = append(out, flyCmd{label: t.t("tray.menu.logs"), icon: glyphFileText,
			do: func() { t.open(dir) }})
	}
	for i := range out {
		if _, err := out[i].icon.polylines(); err != nil {
			t.cfg.Log.Warn("flyout: glyph not read, showing the word",
				"glyph", out[i].icon.name, "error", err)
			return words(out)
		}
	}
	for i := range out {
		out[i].style = styleIcon
		// The first opens the row, the second divides it with them. With one
		// folder there is nothing to divide and the glyph takes the whole row.
		out[i].share = i > 0
	}
	return out
}

// words takes the glyphs off: the commands go back to being buttons with their
// word.
func words(cmds []flyCmd) []flyCmd {
	for i := range cmds {
		cmds[i].icon, cmds[i].share = nil, false
	}
	return cmds
}

// forDisplay shortens an address for showing, not for using.
//
// **The scheme is taken off, not the name.** The panel is 260 points wide and
// the part that identifies the address is the last: with a tailnet longer than
// ours, `https://` eats eight characters at exactly its expense, and the cut
// with the ellipsis would fall where reading matters. Whoever looks at the panel
// is about to scan the code or to copy, and in both cases the scheme is no use
// to them — what gets copied stays the whole address.
func forDisplay(url string) string {
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(url, scheme) {
			url = url[len(scheme):]
			break
		}
	}
	return strings.TrimSuffix(url, "/")
}

// askInside replaces the panel's content with the question.
//
// **The confirmation cannot be a MessageBox, and that is not a preference: it is
// the mechanism.** The panel closes when it loses activation, and any modal
// window takes it away — so a system confirmation would make the panel vanish
// under the question. Asking inside also removes the reason the menu's MessageBox
// needed `MB_SETFOREGROUND`.
//
// **The focus starts on "No"**, as in the menu: whoever opens the panel, clicks
// by mistake and presses Enter by reflex must not have just thrown out everybody
// who was watching. Here that is achieved by putting "No" first, which is where
// `IsDialogMessage` takes the initial focus.
func (f *flyout) askInside(c *flyCmd) {
	f.pending = c
	f.lines = []string{c.confirm[0]}
	f.body = c.confirm[1]
	action := c.do
	f.cmds = []flyCmd{
		{label: f.t.t("tray.confirm.no"), stays: true, do: f.cancel},
		// The "Yes" does not close itself: `command` closes it, like every other
		// command that leads away from the panel. Closing from inside its own
		// `do` was the road by which the "No" ended up doing the same.
		{label: f.t.t("tray.confirm.yes"), do: action},
	}
	f.rebuild()
}

func (f *flyout) cancel() {
	f.pending = nil
	f.compose(f.t.status())
	f.rebuild()
}

// create builds the window, positions it and shows it.
func (f *flyout) create(iconRect rect) error {
	if err := registerFlyoutClass(); err != nil {
		return err
	}
	hinst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString("PATMonitorFlyout")
	title, _ := windows.UTF16PtrFromString(productName)

	// It is created small and off screen: the real size depends on the DPI of
	// the screen it will end up on, and that is known only after creating it.
	hwnd, _, err := procCreateWindowExW.Call(
		wsExToolWindow|wsExTopMost,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsPopup|wsClipChild|wsThickFrame,
		0, 0, 10, 10,
		0, 0, hinst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("creating the flyout window: %w", err)
	}
	f.hwnd = windows.Handle(hwnd)
	f.anchor = iconRect

	// **The scale is that of the screen it will end up on, not of the main
	// one.** `GetDpiForWindow` answers for the monitor the window is on, and
	// freshly created it is at 0,0 — that is, on the main one. With two screens
	// at different factors and the taskbar on the scaled one, the whole panel —
	// fonts, padding, code, buttons — would be computed with the wrong scale,
	// and no line would say so. So it is first moved onto the icon's rectangle,
	// which is by definition on the right screen.
	procSetWindowPos.Call(hwnd, 0, uintptr(iconRect.Left), uintptr(iconRect.Top),
		10, 10, swpNoZOrder|swpNoActivate)
	if d, _, _ := procGetDpiForWindow.Call(hwnd); d > 0 {
		f.dpi = int32(d)
	}

	// --- corners, dark frame and glass: all documented, all DWM's ------------
	//
	// On this machine none of the three was refused. On Windows 10 they do not
	// exist and answer with an error without changing anything, which is the
	// right direction: it degrades to an opaque rectangle.
	f.dwm(dwmwaWindowCornerPref, dwmwcpRound)
	if f.pal.dark {
		f.dwm(dwmwaUseImmersiveDarkMode, 1)
	}
	f.dwm(dwmwaSystemBackdropType, dwmsbtTransientWindow)
	f.dwm(dwmwaBorderColor, f.pal.line)

	f.makeFonts()
	if err := f.createQR(); err != nil {
		f.t.cfg.Log.Warn("flyout: QR not drawn", "error", err)
	}
	f.rebuild()

	// **The order is show, activate, give the focus**, and it is the Microsoft
	// sample's order. Inverting it — giving the focus before showing — activates
	// a window that is not there yet, and on the right button the activation is
	// still the taskbar's, which takes it back: the panel gets created,
	// positioned and closed without ever appearing.
	//
	// **The window's first image is not drawn by WM_PAINT.** That message is
	// taken when the queue is empty, and between the moment the window appears
	// and the moment the loop fishes it out there are the activation, the focus
	// and the indicator state: enough for DWM to compose a frame of a
	// not-yet-painted window, which shows as a white flash before the dark
	// panel.
	//
	// It is remedied from both sides: the **class brush** tells the system what
	// colour to fill it with, so that even what appears before us is the right
	// colour; and the WM_PAINT is sent **at once**, jumping the queue.
	//
	// **And the children have to be redrawn with it**, which is the half that
	// was missing: `WS_CLIPCHILDREN` takes the buttons' rectangles away from the
	// parent — they paint themselves — and `UpdateWindow` updates one window.
	// So the background came out dark already with **seven rectangles** still to
	// paint, which is exactly the white glimpsed under the buttons before they
	// coloured in. `RDW_ALLCHILDREN` takes them all, `RDW_UPDATENOW` makes them
	// paint now rather than on the next round of the queue.
	//
	// **The panel opens with the mouse, so the underlines on the accelerator
	// letters are born hidden — and born, that is, asked for before it shows.**
	// Asking after `ShowWindow` means the first image has already been painted
	// with the other state, and nobody repaints it because nothing changed on
	// screen: an underline would be seen appearing at the opening and vanishing
	// afterwards. Alt brings them back, which is the Windows convention.
	procSendMessageW.Call(uintptr(f.hwnd), wmChangeUIState,
		uintptr(uisSet)|uintptr(uisfHideAccel)<<16, 0)
	s, _, _ := procSendMessageW.Call(uintptr(f.hwnd), wmQueryUIState, 0, 0)
	f.hideAccel = s&uisfHideAccel != 0

	f.classBackground()
	// **The brush and the immediate WM_PAINT were not enough, and the part left
	// over was the buttons.** They are `BS_OWNERDRAW` and born `WS_VISIBLE`, so
	// they become visible with the parent while nothing has drawn them yet —
	// nothing can, before WM_DRAWITEM — and `WS_CLIPCHILDREN` takes their
	// rectangles away from the parent's erase, which is the one thing the class
	// brush cannot cover. Reported from in front of the machine as "a bit of
	// white on opening, worse on the dark one", and that asymmetry is the
	// diagnosis: an undrawn rectangle is conspicuous on near-black and invisible
	// on cream, while the ground itself is the same colour in both.
	//
	// So the frame is not composed at all until the painting is done.
	// DWMWA_CLOAK hides the window from DWM while leaving it visible to USER, so
	// the RedrawWindow below still paints everything; uncloaking publishes one
	// finished image. **Refused, it changes nothing** — `f.dwm` says so and the
	// panel opens as it did before, which is the direction to degrade in.
	//
	// The uncloak must not sit behind anything that can return: between here and
	// the end of this function there is no early exit, and a window left cloaked
	// is a panel that never appears.
	f.dwm(dwmwaCloak, 1)
	start := time.Now()
	procShowWindow.Call(uintptr(f.hwnd), swShow)
	procRedrawWindow.Call(uintptr(f.hwnd), 0, 0,
		rdwInvalidate|rdwErase|rdwAllChildren|rdwUpdateNow)
	f.dwm(dwmwaCloak, 0)
	// **The time of the first paint is measured**, and cloaking changes what the
	// number means rather than making it idle: it used to be the length of the
	// unfinished image — while the pills were recomposed one at a time the panel
	// arrived in pieces and the colour looked as though it were "loading" — and
	// it is now the wait before anything appears at all. Both are the thing that
	// is seen, and a number says whether it has come back better than any
	// impression can.
	painted := time.Since(start)
	fg, _, _ := procSetForegroundWindow.Call(uintptr(f.hwnd))
	f.initialFocus()
	f.t.cfg.Log.Debug("flyout: shown", "foreground", fg != 0,
		"first_paint_ms", painted.Milliseconds(), "shapes", len(f.pills))
	return nil
}

// rebuild remakes the buttons, recomputes the size and repositions.
//
// It is needed on every change of content — the opening and the switch to the
// confirmation question — and it repositions **always**: a panel that changes
// height while staying anchored at the top would run off the screen downwards,
// which above the taskbar is exactly where there is no room.
func (f *flyout) rebuild() {
	for _, h := range f.buttons {
		if h != 0 {
			procDestroyWindow.Call(uintptr(h))
		}
	}
	f.buttons = nil
	hinst, _, _ := procGetModuleHandleW.Call(0)
	class, _ := windows.UTF16PtrFromString("BUTTON")
	for i := range f.cmds {
		txt, _ := windows.UTF16PtrFromString(f.cmds[i].label)
		h, _, _ := procCreateWindowExW.Call(0,
			uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(txt)),
			wsChild|wsVisible|wsTabStop|bsOwnerDraw,
			0, 0, 10, 10, uintptr(f.hwnd), uintptr(idFlyBase+i), hinst, 0)
		// **An empty place is still a place**: `buttons` and `cmds` are indexed
		// by the same number — `place`, the initial focus and the redraws all do
		// it — and skipping a control that failed to be created would shift
		// everyone after it by a row, leaving the last at 10x10 in the corner. A
		// null handle is skipped by the readers, which is the only place the case
		// can be handled without shifting anything.
		f.buttons = append(f.buttons, windows.Handle(h))
		if h == 0 {
			f.t.cfg.Log.Warn("flyout: button not created", "label", f.cmds[i].label)
			continue
		}
		if fo := f.fontFor(f.cmds[i]); fo != 0 {
			procSendMessageW.Call(h, wmSetFont, uintptr(fo), 1)
		}
	}

	// The body's height is **measured**, not estimated by counting characters:
	// the line's width depends on the font and the DPI, and DT_CALCRECT is the
	// right question put to whoever will actually draw it.
	w, _ := f.measure()
	f.bodyH = f.bodyHeight(w - 2*f.px(flyPadding))
	// The status lines are measured for the body's reason and in the body's
	// place: what wraps and what does not depends on the font, the DPI and the
	// language, and DT_CALCRECT is the question put to whoever will draw it.
	f.measureLines(w - 2*f.px(flyPadding))
	w, h := f.measure()

	// **The position is not computed by us.** `CalculatePopupWindowPosition` is
	// the same routine as `TrackPopupMenu`'s: it flips at the edges, keeps
	// inside the work area and shifts the panel so as not to cover the icon.
	rc := f.anchor
	anchor := point{X: (rc.Left + rc.Right) / 2, Y: (rc.Top + rc.Bottom) / 2}
	sz := size{CX: w, CY: h}
	var out rect
	ok, _, _ := procCalculatePopupWindowPosition.Call(
		uintptr(unsafe.Pointer(&anchor)), uintptr(unsafe.Pointer(&sz)),
		tpmVertical|tpmVCenterAlign|tpmCenterAlign|tpmWorkArea,
		uintptr(unsafe.Pointer(&rc)), uintptr(unsafe.Pointer(&out)))
	if ok == 0 {
		out = rect{anchor.X - w, anchor.Y - h, anchor.X, anchor.Y}
		f.t.cfg.Log.Warn("flyout: CalculatePopupWindowPosition refused, placing it by hand")
	}
	f.t.cfg.Log.Debug("flyout: placed",
		"dpi", f.dpi, "size", fmt.Sprintf("%dx%d", w, h),
		"at", fmt.Sprintf("%d,%d", out.Left, out.Top),
		"buttons", len(f.cmds))

	procSetWindowPos.Call(uintptr(f.hwnd), 0,
		uintptr(out.Left), uintptr(out.Top), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
	f.place(w)
	procInvalidateRect.Call(uintptr(f.hwnd), 0, 1)
	// **The focus is given only to an already visible window.** `SetFocus`
	// activates its top-level ancestor: calling it on a still-hidden window
	// activates it before it has been shown, and the taskbar's manager — which
	// on the right button still holds the activation — takes it back an instant
	// later. From outside one sees a panel that does not appear: it gets
	// created, positioned, and closed by `WA_INACTIVE` before painting itself.
	if v, _, _ := procIsWindowVisible.Call(uintptr(f.hwnd)); v != 0 {
		f.initialFocus()
	}
}

// focusIndex is the command the panel opens with selected, or -1.
//
// **It is a function because the mark follows the focus**, so which command
// this picks *is* which command the panel marks — and a rule that decides the
// loudest thing on the screen should be answerable without a window. The
// drawing needs handles; this needs only the composed commands.
func (f *flyout) focusIndex() int {
	for _, want := range []flyStyle{stylePill, styleGhost} {
		for i, c := range f.cmds {
			if c.style == styleText {
				continue
			}
			if want == stylePill && c.style != stylePill {
				continue
			}
			return i
		}
	}
	return -1
}

// initialFocus puts the focus where focusIndex says, and falls back to the
// first control there is when that command has no button.
func (f *flyout) initialFocus() {
	// **The focus starts on the main command, and where there is none, on the
	// first.** The address is not a command: copying is done by pressing it, so
	// it is the panel's first control, and opening from the keyboard and
	// pressing Enter would copy an address instead of opening the monitor. In
	// the confirmation question the rule changes nothing, because there the
	// first one is "No" and there is no main command to prefer.
	//
	// **The preference is what makes one mark enough.** The panel says *this
	// one* in a single way — the accent of the selected command — so the main
	// action has to be the selected one, or the loudest thing on the panel
	// would be sitting on something that is not the main action. It used to
	// paint itself as well, and then a panel with both a refused permission and
	// a step waiting carried two marked buttons, which is the thing this rule
	// exists to prevent.
	if i := f.focusIndex(); i >= 0 && i < len(f.buttons) && f.buttons[i] != 0 {
		procSetFocus.Call(uintptr(f.buttons[i]))
		return
	}
	// **The fallback skips the address, and the first version of it did not.**
	// Walking the handles alone reaches `styleText`, which is the address —
	// the one control this panel must never open with selected, because Enter
	// would then copy an address instead of opening the monitor. The old loop
	// carried that test inside itself; splitting the choice out of it left the
	// fallback with nothing but handles to look at.
	for i, c := range f.cmds {
		if c.style == styleText || i >= len(f.buttons) || f.buttons[i] == 0 {
			continue
		}
		procSetFocus.Call(uintptr(f.buttons[i]))
		return
	}
}

func (f *flyout) dwm(attr, value uint32) {
	v := value
	r, _, _ := procDwmSetWindowAttribute.Call(uintptr(f.hwnd), uintptr(attr),
		uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	// **An unsupported attribute has to answer with an error and do nothing**:
	// that is the right direction for Windows 10, where none of the three
	// exists. What it answered is written down rather than taken as done.
	if r != 0 {
		f.t.cfg.Log.Debug("flyout: DWM attribute refused",
			"attribute", attr, "hresult", fmt.Sprintf("0x%08X", uint32(r)))
	}
}

// classBackground gives the class the colour of the panel of now.
//
// **It cannot be fixed at registration time**: the Windows theme changes while
// the monitor is running, and a brush chosen once would be the wrong colour in
// exactly the frame this exists to cover. The old one is deleted after being
// replaced, otherwise an object the system is still using gets destroyed.
func (f *flyout) classBackground() {
	// The class is one per process, so the brush **is already the right one** at
	// every opening except the first and those after a theme change: remaking it
	// to put back the same colour means creating and destroying a shared GDI
	// object for nothing.
	if flyBackground != 0 && flyBackgroundColor == f.pal.ground {
		return
	}
	br, _, _ := procCreateSolidBrush.Call(uintptr(f.pal.ground))
	if br == 0 {
		return
	}
	previous, _, _ := procSetClassLongPtrW.Call(uintptr(f.hwnd), gclpHbrBackground, br)
	flyBackground, flyBackgroundColor = windows.Handle(br), f.pal.ground
	if previous != 0 {
		procDeleteObject.Call(previous)
	}
}

func (f *flyout) px(dip int32) int32 { return dip * f.dpi / 96 }

func (f *flyout) cmdHeight(c flyCmd) int32 {
	if c.style == styleText {
		return f.px(flyAddress)
	}
	return f.px(flyButton)
}

// layout says at what height each piece sits, and how tall the panel is.
//
// **It lives in one function because there are three readers** — whoever
// measures, whoever lays out the buttons and whoever paints — and the same
// arithmetic written three times is how the drawing comes unstuck from the
// controls without anything saying so.
//
// **The order is the menu's: first how it is, then what can be done.** The
// status lines are the answer to the question the panel is opened for, and go
// where the eye falls; the QR code is already a command — it serves to carry the
// monitor to a phone — and sits with the others.
type layout struct {
	linesY, bodyY, qrY, cmdY, total int32
	// leadY is where the lead command goes, and -1 when there is none. It is a
	// sentinel and not a zero because zero is a legal y inside this panel.
	leadY int32
}

func (f *flyout) layout() layout {
	var p layout
	y := f.px(flyPadding)

	p.linesY = y
	for i := range f.lines {
		y += f.lineHeight(i)
	}
	// **The lead command belongs to the line above it**, so it is the only
	// thing that comes between the status and the code. See flyCmd.lead.
	p.leadY = -1
	if i := f.leadIndex(); i >= 0 {
		y += f.px(flyGap)
		p.leadY = y
		y += f.cmdHeight(f.cmds[i])
	}
	if f.body != "" && f.bodyH > 0 {
		p.bodyY = y
		y += f.bodyH
	}
	// In the confirmation question the code is not there: they would be two
	// things to look at while one has to be decided. Nor is it there when it
	// could not be engraved — an address over 106 characters — and then not even
	// its space is kept: a gap in the middle of the panel reads as an image that
	// has not finished loading.
	if f.pending == nil && f.qrBm != 0 {
		y += f.px(flyGap)
		p.qrY = y
		y += f.qrPx
	}
	y += f.px(flyGap)

	p.cmdY = y
	for _, row := range f.rows() {
		y += f.rowHeight(row) + f.px(flyButtonGap)
	}
	p.total = y - f.px(flyButtonGap) + f.px(flyPadding)
	return p
}

// rows groups the commands by row: whoever carries `share` sits beside the
// previous one rather than below it.
//
// **It sits next to layout for the reason layout exists**: there are three
// readers, and the same arithmetic written three times is how the drawing comes
// unstuck from the controls without anything saying so. A `share` on the first
// command has no predecessor to divide the row with, and then it opens its own:
// an empty row at the top of the panel would be a gap nobody explains.
func (f *flyout) rows() [][]int {
	var out [][]int
	for i, c := range f.cmds {
		if c.lead {
			continue // it has a row of its own, above the code
		}
		if c.share && len(out) > 0 {
			out[len(out)-1] = append(out[len(out)-1], i)
			continue
		}
		out = append(out, []int{i})
	}
	return out
}

// leadIndex is the command drawn above the code, or -1.
//
// **It is derived from the commands and not kept beside them**, which is the
// same rule `rows` follows: a second field saying which one leads is a second
// list, and the day it disagrees the panel lays out a button nobody painted.
// There is at most one, and the first wins — `layout` gives it one row.
func (f *flyout) leadIndex() int {
	for i, c := range f.cmds {
		if c.lead {
			return i
		}
	}
	return -1
}

// rowHeight is the height of the tallest command in the row.
func (f *flyout) rowHeight(row []int) int32 {
	var h int32
	for _, i := range row {
		if v := f.cmdHeight(f.cmds[i]); v > h {
			h = v
		}
	}
	return h
}

func (f *flyout) measure() (w, h int32) {
	return f.px(flyWidth), f.layout().total
}

func (f *flyout) place(w int32) {
	gap := f.px(flyButtonGap)
	p := f.layout()
	if i := f.leadIndex(); i >= 0 && p.leadY >= 0 && i < len(f.buttons) && f.buttons[i] != 0 {
		span := f.rowSpans(w, 1)[0]
		procSetWindowPos.Call(uintptr(f.buttons[i]), 0, uintptr(span[0]), uintptr(p.leadY),
			uintptr(span[1]), uintptr(f.cmdHeight(f.cmds[i])), swpNoZOrder|swpNoActivate)
	}
	y := p.cmdY
	for _, row := range f.rows() {
		for k, span := range f.rowSpans(w, len(row)) {
			i := row[k]
			if i < len(f.buttons) && f.buttons[i] != 0 {
				procSetWindowPos.Call(uintptr(f.buttons[i]), 0, uintptr(span[0]), uintptr(y),
					uintptr(span[1]), uintptr(f.cmdHeight(f.cmds[i])), swpNoZOrder|swpNoActivate)
			}
		}
		y += f.rowHeight(row) + gap
	}
}

// rowSpans divides the width among the commands of a row: for each one, where it
// begins and how wide it is.
//
// **The remainder of the division is spread, not dumped on the last one.** The
// panel is a column and everything reaches exactly `w - pad`, so those pixels
// have to go to somebody: giving them all to the end makes the last command
// n-1 pixels wider, and two parallel commands visibly different read as a
// hierarchy that is not there. One each to the first few makes them
// indistinguishable.
func (f *flyout) rowSpans(w int32, n int) [][2]int32 {
	pad, gap := f.px(flyPadding), f.px(flyButtonGap)
	if n <= 0 {
		return nil
	}
	room := w - 2*pad - int32(n-1)*gap
	each, extra := room/int32(n), room%int32(n)
	out := make([][2]int32, 0, n)
	x := pad
	for k := int32(0); k < int32(n); k++ {
		width := each
		if k < extra {
			width++
		}
		out = append(out, [2]int32{x, width})
		x += width + gap
	}
	return out
}

// createQR engraves the code into a bitmap already of the right size.
//
// **Dark on light always, even with the dark panel.** It is not an oversight: it
// is the same rule already paid for on the night page — a transparent code, or a
// light one on dark, is unreadable by exactly the camera that has to read it. On
// the dark background it reads like a sticker, and that is right.
func (f *flyout) createQR() error {
	if f.url == "" {
		return nil
	}
	m, err := qr.Encode(f.url)
	if err != nil {
		return err
	}
	modules := int32(m.Size + 2*qr.QuietZone)
	side := f.px(flyQRSide)
	scale := side / modules
	if scale < 2 {
		scale = 2
	}
	// Rounded to the whole module: half a pixel per module does not read.
	side = modules * scale
	f.qrPx = side

	hdr := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: side, Height: -side,
		Planes: 1, BitCount: 32, Compression: 0,
	}
	var bits unsafe.Pointer
	bm, _, err := procCreateDIBSection.Call(
		0, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bm == 0 {
		return err
	}
	pix := unsafe.Slice((*byte)(bits), int(side)*int(side)*4)

	// **The panel is rounded as in the pages**, and the rounding is done in
	// here rather than with a GDI region: the bitmap is delivered with
	// `BitBlt`, which copies and nothing else, so the corners outside the curve
	// already carry the panel's colour and blend with the background. A region
	// would clip the same shape with more objects to free.
	radius := f.px(flyQRRadius)
	fill := f.pal.ground
	for y := int32(0); y < side; y++ {
		for x := int32(0); x < side; x++ {
			c := fill
			if insideRounded(x, y, side, radius) {
				c = colorOf(qr.Paper)
			}
			writePixel(pix, int(y*side+x)*4, c)
		}
	}
	for my := int32(0); my < modules; my++ {
		for mx := int32(0); mx < modules; mx++ {
			if !m.At(int(mx)-qr.QuietZone, int(my)-qr.QuietZone) {
				continue
			}
			for y := my * scale; y < (my+1)*scale; y++ {
				for x := mx * scale; x < (mx+1)*scale; x++ {
					writePixel(pix, int(y*side+x)*4, colorOf(qr.Ink))
				}
			}
		}
	}
	f.qrBm = windows.Handle(bm)
	return nil
}

// spiGetNonClientMetrics is SPI_GETNONCLIENTMETRICS.
const spiGetNonClientMetrics = 0x0029

// messageFace is the typeface Windows writes its own message text in, and the
// character set it writes it with.
//
// **The typeface is the system's and the sizes are ours, and they are different
// questions.** The scale -- `tBody`, `tUI`, `tSmall`, the two weights -- is the
// stylesheets', measured on this panel, and the panel's own chapter is a record
// of what happens when one of those numbers is taken from somewhere else. What
// is not ours to choose is the *face*: on a Chinese, Japanese or Korean Windows
// "Segoe UI" has no glyph for most of what it would be asked to draw, and GDI
// answers with the box every user of those systems knows. `lfMessageFont` is
// the face that system writes with, so asking is the difference between a panel
// and a row of tofu -- and it is the same rule as the videos folder, the local
// address and the preferred languages.
//
// **A refusal is not a fault**: Segoe UI is what this panel has always used and
// is right on the machines this program has been measured on. The face is taken
// and the height is deliberately not, because `lfMessageFont.Height` is the
// system's text size and would throw the scale away.
func (f *flyout) messageFace() (string, byte) {
	var m nonClientMetricsW
	m.Size = uint32(unsafe.Sizeof(m))
	ok, _, _ := procSystemParametersInfoForDpi.Call(
		spiGetNonClientMetrics, uintptr(m.Size), uintptr(unsafe.Pointer(&m)),
		0, uintptr(f.dpi))
	return messageFaceFrom(m.MessageFont, ok != 0)
}

// messageFaceFrom takes the answer instead of asking for it, which is what
// makes both directions testable: a function that interrogates the operating
// system can be run and not tested, and on this machine the refusal and the
// answer are the **same string** -- an Italian Windows writes with Segoe UI,
// so a test on the value alone would pass over a call that never succeeded.
func messageFaceFrom(lf logFontW, answered bool) (string, byte) {
	const fallback = "Segoe UI"
	const defaultCharSet = 1
	if !answered {
		return fallback, defaultCharSet
	}
	name := windows.UTF16ToString(lf.FaceName[:])
	if name == "" {
		return fallback, defaultCharSet
	}
	// **The character set comes with the face and is not guessed.**
	// DEFAULT_CHARSET makes GDI choose from the system locale, which is nearly
	// always the same answer and is an answer arrived at by a second road; the
	// face and the set Windows paired are the pair it drew with.
	return name, lf.CharSet
}

// makeFonts builds the five the panel draws with.
//
// **It is a function because the guards build them too, and a second list
// diverges.** One was already written by hand in `panelDC` and had `fontGhost`
// at `tBody` against the panel's `tUI` — two points larger, which put German's
// longest command at exactly the room it has and made a number this project
// wrote down wrong. The sizes and the weights are the stylesheets' and they are
// decided here, once.
//
// **The title of a question is a title, and a command is a command.** One font
// served both: the confirmation's question at 16/600, which is right, and the
// main command at 16/600 beside its neighbours at 14/500 — two buttons of the
// same shape side by side with their words at two sizes, `Sì, procedi` two
// points larger than `No, lascia stare`. It was defensible while the main
// command was a solid block, that is, a different object; once both wear the
// same mark the size is the only loud thing left, and it reads as a mistake,
// which is how it was reported.
func (f *flyout) makeFonts() {
	f.fontTitle = f.createFont(tBody, 600)
	f.fontMain = f.createFont(tUI, 600)
	f.fontGhost = f.createFont(tUI, 500)
	f.fontLine = f.createFont(tUI, 400)
	f.fontSmall = f.createFont(tSmall, 400)
}

func (f *flyout) createFont(measure, weight int32) windows.Handle {
	face, charSet := f.messageFace()
	name, err := windows.UTF16PtrFromString(face)
	if err != nil {
		name, _ = windows.UTF16PtrFromString("Segoe UI")
	}
	h, _, _ := procCreateFontW.Call(
		uintptr(-f.px(measure)), 0, 0, 0, uintptr(weight), 0, 0, 0,
		uintptr(charSet), 0, 0, 5 /* CLEARTYPE_QUALITY */, 0,
		uintptr(unsafe.Pointer(name)))
	return windows.Handle(h)
}

func (f *flyout) dismiss() {
	if f == nil || f.hwnd == 0 {
		return
	}
	procDestroyWindow.Call(uintptr(f.hwnd))
}

func (f *flyout) release() {
	if f.qrBm != 0 {
		procDeleteObject.Call(uintptr(f.qrBm))
		f.qrBm = 0
	}
	for _, h := range []windows.Handle{f.fontTitle, f.fontMain, f.fontGhost, f.fontLine, f.fontSmall} {
		if h != 0 {
			procDeleteObject.Call(uintptr(h))
		}
	}
	f.fontTitle, f.fontMain, f.fontGhost, f.fontLine, f.fontSmall = 0, 0, 0, 0, 0
	for key, bm := range f.pills {
		procDeleteObject.Call(uintptr(bm))
		delete(f.pills, key)
	}
	for key, bm := range f.glyphs {
		procDeleteObject.Call(uintptr(bm))
		delete(f.glyphs, key)
	}
	f.hwnd = 0
}

func registerFlyoutClass() error {
	if flyClassReady {
		return nil
	}
	className, _ := windows.UTF16PtrFromString("PATMonitorFlyout")
	hinst, _, _ := procGetModuleHandleW.Call(0)
	cur, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{
		WndProc:   windows.NewCallback(flyoutProc),
		Instance:  windows.Handle(hinst),
		Cursor:    windows.Handle(cur),
		ClassName: className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("flyout class registration: %w", err)
	}
	flyClassReady = true
	return nil
}

// drawItemFrom reads the DRAWITEMSTRUCT Windows passes in lParam.
//
// **`go vet` flags `unsafe.Pointer(uintptr)`, and in general it is right**: a
// uintptr keeps nothing alive, so the collector could move or free what it
// points at exactly while it is being converted. Here that cannot happen, and
// the reasons are two and both are needed:
//
//   - **the memory is not Go's.** The structure is allocated by Windows and
//     lives in its stack, where the collector does not reach and moves nothing;
//   - **we do not keep it.** It is valid for the duration of this message, and
//     the pointer does not leave `paintButton`.
//
// The conversion lives in a function of its own rather than in the middle of the
// switch, to keep the reasoning next to the point where it applies: silencing
// the checker over the whole file would remove the warning where it would be
// right too.
func drawItemFrom(lparam uintptr) *drawItemStruct {
	return (*drawItemStruct)(*(*unsafe.Pointer)(unsafe.Pointer(&lparam)))
}

func flyoutProc(hwnd windows.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	f := current
	if f == nil || f.hwnd != hwnd {
		return defWindowProc(hwnd, msg, wparam, lparam)
	}

	switch msg {
	// **The client area takes the whole window.** `WS_THICKFRAME` is only there
	// to give DWM a frame to round; the resize border that would come with it is
	// not wanted, and answering zero without touching the proposed rectangle
	// removes it. The rounded corner and the shadow remain, because those are
	// drawn by DWM outside the window and not in its non-client area.
	case wmNcCalcSize:
		if wparam != 0 {
			return 0
		}

	// **Saying the non-client area is empty is not enough: somebody repaints it
	// anyway.** WM_NCCALCSIZE decides the rectangle, but these two work the
	// frame out from the style — and `WS_THICKFRAME` is there, because DWM needs
	// it to round the corners — and draw the resize border **over** our content.
	//
	// Measured on the screen's pixels, opening the panel with a real click: two
	// pixels of the colour we declare and then **sixteen of grey 0xB4B4B4 on all
	// four sides** — which at 192 dpi are the eight of `SM_CXSIZEFRAME` plus
	// `SM_CXPADDEDBORDER`.
	//
	// **It appears on activation, and that is why it could not be seen from
	// here:** a panel opened by sending the icon's message from another process
	// never becomes the active window, so the frame is never repainted. The
	// defect showed only with a real click, and only from the second opening on
	// — that is, from the first in which the activation **changes** instead of
	// already being there.
	//
	// `lParam` at -1 is how one tells DefWindowProc "change state, do not touch
	// the pixels"; WM_NCPAINT is answered with zero because there is nothing to
	// paint.
	case wmNcPaint:
		return 0
	case wmNcActivate:
		return defWindowProc(hwnd, msg, wparam, ^uintptr(0))

	// **The erasing is done by the system, with our brush.** This used to answer
	// 1 so as not to see a flicker, and the flicker came from the brush: without
	// one, the window stayed the colour it is born — white — until the first
	// WM_PAINT. With a brush of the panel's colour the two passes paint the same
	// thing and there is nothing to see.
	case wmEraseBkgnd:
		return defWindowProc(hwnd, msg, wparam, lparam)

	case wmPaint:
		f.paint()
		return 0

	case wmDrawItem:
		f.paintButton(drawItemFrom(lparam))
		return 1

	// **It closes on losing activation**, which is also why nothing modal can be
	// opened from here: child controls do not deactivate the parent, a MessageBox
	// does. See askInside.
	case wmActivate:
		if wparam&0xFFFF == waInactive {
			// It is written down, and that is not verbosity: a panel that
			// vanishes without appearing and one that is never created look the
			// same from outside, and only this line tells them apart.
			//
			// **Who takes over is said by lParam**, and without that name
			// "loses activation" is not a diagnosis: it is a statement of the
			// symptom. Two wrong guesses in a row were enough to learn that here
			// one looks rather than deduces.
			f.t.cfg.Log.Debug("flyout: closing, lost activation",
				"to", classNameOf(windows.Handle(lparam)))
			procDestroyWindow.Call(uintptr(hwnd))
		}
		return 0

	case wmCommand:
		f.command(uint32(wparam & 0xFFFF))
		return 0

	// Windows has changed its mind about the keyboard indicators — usually
	// because somebody pressed Tab. It is reread and repainted: it holds the
	// state, we follow it.
	case wmUpdateUIState:
		// **First the state is let update, then it is asked for.** Windows holds
		// the state and it is written by `DefWindowProc` **while it handles this
		// message**: asking before answers with the previous value, that is,
		// with the one the message is announcing has changed — and what comes of
		// it never changes.
		outcome := defWindowProc(hwnd, msg, wparam, lparam)
		s, _, _ := procSendMessageW.Call(uintptr(hwnd), wmQueryUIState, 0, 0)
		before := f.hideAccel
		f.hideAccel = s&uisfHideAccel != 0
		// The underline is on **every** label, so everything is repainted here —
		// but it only happens on Alt, not on every arrow key.
		if before != f.hideAccel {
			for _, h := range f.buttons {
				if h != 0 {
					procInvalidateRect.Call(uintptr(h), 0, 1)
				}
			}
		}
		return outcome

	case wmDestroy:
		// **Closing counts as a gesture.** Clicking the icon with the panel
		// open, the click first takes its activation away — so we get here — and
		// only afterwards does the shell send us the icon's notification:
		// without this instant, that notification would find the panel already
		// closed and would reopen it. Clicking the icon would close it and
		// reopen it, which from outside looks like a flicker.
		f.t.lastPanel = time.Now()
		f.release()
		current = nil
		return 0
	}
	return defWindowProc(hwnd, msg, wparam, lparam)
}

func (f *flyout) command(id uint32) {
	// Esc arrives as IDCANCEL, sent by IsDialogMessage. Inside the question it
	// cancels the question, outside it closes the panel: which is what one
	// expects of Esc in both cases.
	if id == idCancel {
		if f.pending != nil {
			f.cancel()
			return
		}
		procDestroyWindow.Call(uintptr(f.hwnd))
		return
	}
	i := int(id) - idFlyBase
	if i < 0 || i >= len(f.cmds) {
		return
	}
	c := f.cmds[i]
	if c.confirm[0] != "" {
		f.askInside(&c)
		return
	}
	// **Whoever stays, stays first**: a command that puts the panel back as it
	// was cannot find it destroyed. See flyCmd.stays.
	if c.stays {
		if c.do != nil {
			c.do()
		}
		return
	}
	// **The panel closes before acting.** Opening a page or copying an address
	// with the panel still on top would leave a window nobody closed lying
	// around, and the command that opens the browser would take its activation
	// away in any case.
	procDestroyWindow.Call(uintptr(f.hwnd))
	if c.do != nil {
		c.do()
	}
}

func (f *flyout) paint() {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(f.hwnd), uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(uintptr(f.hwnd), uintptr(unsafe.Pointer(&ps)))

	var rc rect
	procGetClientRect.Call(uintptr(f.hwnd), uintptr(unsafe.Pointer(&rc)))
	f.fillRect(hdc, rc, f.pal.ground)

	pad := f.px(flyPadding)
	p := f.layout()

	// The status lines: the same as the menu's, from the same function. The
	// first of the question is the title, and goes in bold.
	//
	// **Centred**, because everything else in the panel is — the code, the
	// address, the commands' labels — and two left-aligned lines above a column
	// of centred things read as a piece of another page. The question's body
	// stays left: that one wraps, and a centred paragraph reads worse with every
	// extra line.
	//
	// **A line too long for its row gets a second row, and the first answer was
	// to cut it.** Centred and on one row it was cut at *both* ends — the
	// failure the panel's own icon labels already name, *in the way where one
	// cannot even tell something is missing* — and it was reachable by three of
	// the existing fault sentences and not only by a new one: measured at 96
	// dpi against the 236 px of a row, `remote-no-ingress` is 447 px in German
	// and 432 in English, and `no-password` is over in all five languages.
	// Nothing had said so, because a line missing its first word and its last
	// reads as a line.
	//
	// An ellipsis was put there first and it was the wrong repair: **it makes
	// the cut visible, and what was wanted was the sentence.** These are the
	// answer to the question the panel is opened for, and half of one is not an
	// answer. So the rectangle is the one `measureLines` measured, up to
	// `maxStatusRows`, and the ellipsis stays underneath as the floor for what
	// does not fit even there.
	y := p.linesY
	for i, text := range f.lines {
		font, color := f.fontLine, f.pal.muted
		if f.pending != nil && i == 0 {
			font, color = f.fontTitle, f.pal.ink
		}
		h := f.lineHeight(i)
		box := rect{pad, y, rc.Right - pad, y + h}
		if h <= f.px(flyLine) {
			f.drawText(hdc, text, box, color, font, dtCenter|dtEndEllipsis)
		} else {
			// **`DT_VCENTER` is only honoured on a single line**, so a wrapped
			// one has to be centred by hand: without it the text sits at the
			// top of its two rows and the gap below reads as a missing line.
			f.drawWrapped(hdc, text, box, color, font)
		}
		y += h
	}

	// The QR code, below the status: it is a command like the others.
	if f.pending == nil && f.qrBm != 0 {
		x := (rc.width() - f.qrPx) / 2
		f.blit(hdc, f.qrBm, rect{x, p.qrY, x + f.qrPx, p.qrY + f.qrPx})
	}

	// The question's body, wrapping. The rectangle is the one `bodyHeight`
	// measured, so it all fits by construction.
	if f.body != "" && f.bodyH > 0 {
		if f.fontLine != 0 {
			procSelectObject.Call(hdc, uintptr(f.fontLine))
		}
		procSetBkMode.Call(hdc, transparent)
		procSetTextColor.Call(hdc, uintptr(f.pal.muted))
		r := rect{pad, p.bodyY, rc.Right - pad, p.bodyY + f.bodyH}
		txt, _ := windows.UTF16FromString(f.body)
		procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
			uintptr(unsafe.Pointer(&r)), dtWordBreak|dtNoPrefix)
	}
}

// drawWrapped draws a status line over more than one row, centred in its box.
//
// **It is a second function and not a flag on the first**, because the two
// cannot share their flags: `DT_SINGLELINE` and `DT_VCENTER` go together and
// `DT_WORDBREAK` excludes both, so one call with a switch inside it would be
// two calls wearing one name.
//
// The vertical centring is done by measuring and shifting, which is what
// `DT_VCENTER` would have done: the rows are `flyLine` apart and the text is
// shorter than that, so without it the sentence sits against the top of its box
// and the slack collects underneath, where it reads as a line that failed to
// draw.
func (f *flyout) drawWrapped(hdc uintptr, s string, r rect, color uint32, font windows.Handle) {
	if font != 0 {
		procSelectObject.Call(hdc, uintptr(font))
	}
	procSetBkMode.Call(hdc, transparent)
	procSetTextColor.Call(hdc, uintptr(color))
	txt, _ := windows.UTF16FromString(s)

	const flags = dtCenter | dtWordBreak | dtNoPrefix | dtEndEllipsis
	measure := rect{r.Left, r.Top, r.Right, r.Bottom}
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
		uintptr(unsafe.Pointer(&measure)), flags|dtCalcRect)
	box := r
	if slack := (r.Bottom - r.Top) - (measure.Bottom - measure.Top); slack > 0 {
		box.Top += slack / 2
	}
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
		uintptr(unsafe.Pointer(&box)), flags)
}

func (f *flyout) drawText(hdc uintptr, s string, r rect, color uint32, font windows.Handle, flags uint32) {
	if font != 0 {
		procSelectObject.Call(hdc, uintptr(font))
	}
	procSetBkMode.Call(hdc, transparent)
	procSetTextColor.Call(hdc, uintptr(color))
	txt, _ := windows.UTF16FromString(s)
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
		uintptr(unsafe.Pointer(&r)), uintptr(dtSingleLine|dtVCenter|dtNoPrefix|flags))
}

// marked is how this panel says *this one*: the viewer's switched-on toggle,
// `border-color` accent at 60%, `background` accent at 22%, text `--ink`.
//
// **It is one function because there is one mark.** The main command and the
// command with the focus are two questions with one answer here, and written
// twice they would drift — which is what the solid accent was, a second mark
// that had stopped meaning anything the day `pal.accent` stopped being the
// phase.
func (f *flyout) marked() (fill, border, ink uint32) {
	return blend(f.pal.ground, f.pal.accent, 0.22),
		blend(f.pal.ground, f.pal.accent, 0.60),
		f.pal.ink
}

// paintButton is the half we pay for having keyboard and accessibility free:
// the control is the system's, the pixels are ours.
func (f *flyout) paintButton(di *drawItemStruct) {
	i := int(di.CtlID) - idFlyBase
	var c flyCmd
	if i >= 0 && i < len(f.cmds) {
		c = f.cmds[i]
	}

	// **The shapes are the stylesheet's**, not an invention here: the filled
	// button is `background: var(--phase); color: #FBF8F2`, the secondary one is
	// `button.ghost` — transparent, a hairline of `--line` around it, `--muted`
	// text. The radius is `border-radius: 999px`, that is, half the height: a
	// pill, not a value to choose.
	fill, border, ink := f.pal.ground, f.pal.line, f.pal.muted
	if c.style == styleText {
		border = 0
	}
	// **The command that has the focus is visible, even to whoever has not
	// touched the keyboard.** At the opening the focus is already there — we
	// give it to the first command — and a selected button that does not declare
	// it is a selection that exists only for whoever presses Enter blind.
	//
	// The form is that of the viewer's switched-on toggles, taken from the same
	// rule, and it is `marked` — the same function the main command draws with,
	// so the panel cannot end up with two ways of saying the same thing. On the
	// main command it assigns what is already there.
	if di.ItemState&odsFocus != 0 {
		fill, border, ink = f.marked()
	}
	if di.ItemState&odsSelected != 0 {
		// Pressed: the sheet lightens by 12%. Here it is mixed towards the ink,
		// which on the dark background and on the light one goes the right way
		// in both cases.
		fill = blend(fill, f.pal.ink, 0.12)
		ink = f.pal.ink
	}

	// **There is no focus ring, and it is the coloured background that made it
	// useless.** They were two marks for the same thing: the background already
	// says "this is the selected command", and over it a light hairline becomes
	// a second outline, thicker than the one underneath. The focus stays visible
	// — that is the condition that matters for whoever navigates by keyboard —
	// only the colour says it instead of an outline. With it went the whole
	// `UISF_HIDEFOCUS` state: that was kept because a ring must not appear to
	// whoever uses the mouse, while a selected command **has** to be visible
	// always.
	f.pill(uintptr(di.Hdc), di.RcItem, fill, border)

	// **The glyph is engraved on the button's background, not over it.** The
	// bitmap arrives with `BitBlt`, which copies and nothing else, so what lies
	// outside the stroke has to carry the right colour already: it can, because
	// the glyph falls in the flat centre of the pill, where that colour is
	// exactly `fill`. A different background — the focus, the pressed state — is
	// a different key in the cache, not a case to handle here.
	if c.icon != nil {
		side := f.px(flyIcon)
		if bm := f.glyphBitmap(c.icon, side, ink, fill); bm != 0 {
			r := di.RcItem
			x := r.Left + (r.width()-side)/2
			y := r.Top + (r.Bottom-r.Top-side)/2
			f.blit(uintptr(di.Hdc), bm, rect{x, y, x + side, y + side})
			return
		}
		// If the engraving did not work the word is written: it is the
		// control's text, it is always there, and an empty button says nothing.
	}

	// **We have the label**, and it is the one we gave the control: asking for
	// it back with `GetWindowTextW` is a system call and a 128-character buffer
	// on every redraw, to get back a string that is two lines above. It also
	// carried a way of being wrong — beyond 128 characters that answer is
	// truncated.
	buf, _ := windows.UTF16FromString(c.label)
	n := uintptr(len(buf) - 1)
	if fo := f.fontFor(c); fo != 0 {
		procSelectObject.Call(uintptr(di.Hdc), uintptr(fo))
	}
	procSetBkMode.Call(uintptr(di.Hdc), transparent)
	procSetTextColor.Call(uintptr(di.Hdc), uintptr(ink))
	// **The underline on the accelerator letter is removed by us.**
	// `DT_HIDEPREFIX` draws `O&pen` without the line under the p; the state
	// saying whether to show it is held by Windows, but the text is drawn here,
	// so nobody applies it in our place — and asking and nothing else leaves
	// seven underlined buttons with the request accepted.
	flags := uint32(dtSingleLine | dtCenter | dtVCenter)
	if f.hideAccel {
		flags |= dtHidePrefix
	}
	// **What does not fit is cut and said so**, and there are two cases.
	//
	// **Every label is cut with an ellipsis, and it used to be two of them.**
	//
	// The address comes from outside and can be as long as the installer's
	// tailnet wants; a `styleIcon` button takes half a row, and the word on it
	// is the fallback for when the engraving fails — "Log folder" does not fit
	// in a hundred and fifteen points. Both were given the ellipsis because
	// **centred, a label that does not fit is cut at both ends**, that is, in
	// the way where one cannot even tell something is missing.
	//
	// **The third was in front of us the whole time and was found by looking at
	// a photograph of the panel**: `tray.menu.todo` carried Tailscale's own
	// sentence, which is the longest thing in this window and was not ours to
	// shorten — it took one line of it, and a line is not a width. Measured, it
	// drew `ve this machine in the Tailscale`, missing its first word and its
	// last. The assumption underneath was the one the icon label's note had
	// already retired in one case and left standing in general: that the
	// commands' labels fit by construction.
	//
	// **That key no longer exists**, and the repair went further than an
	// ellipsis: the sentence is a status row now and the button carries a short
	// label of ours per action. What this paragraph records is the measurement
	// and the assumption, both of which still hold for whatever is put on a
	// button next.
	//
	// So the rule is unconditional now, because the exception cost more than it
	// saved: on a label that fits, the ellipsis changes nothing whatever, and on
	// one that does not it turns an unreadable line into a readable one that
	// declares itself cut. What the catalogue's own labels must still do is fit
	// — `TestEveryCommandLabelFitsThePanel` measures them — because an
	// ellipsised label is a repair, not a design.
	flags |= dtEndEllipsis
	r := di.RcItem
	procDrawTextW.Call(uintptr(di.Hdc), uintptr(unsafe.Pointer(&buf[0])), n,
		uintptr(unsafe.Pointer(&r)), uintptr(flags))

}

func (f *flyout) fillRect(hdc uintptr, r rect, c uint32) {
	br, _, _ := procCreateSolidBrush.Call(uintptr(c))
	if br == 0 {
		return
	}
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), br)
	procDeleteObject.Call(br)
}

// flyoutWantsMessage gives the keyboard to the panel.
//
// **It is all the keyboard navigation we need**, and it is one call: the
// documentation says `IsDialogMessage` can be used "with any window that
// contains controls". From it come Tab, Shift+Tab, the arrows, Enter on the
// button that has the focus, Esc as IDCANCEL and the accelerators. Tried: Tab
// goes round, and Narrator reads the buttons.
func flyoutWantsMessage(msg *msgReceived) bool {
	f := current
	if f == nil || f.hwnd == 0 {
		return false
	}
	// **Only the keyboard messages**, which are the only ones
	// `IsDialogMessage` does anything with. Without this line every movement of
	// the mouse over the panel — a hundred a second — pays two system calls to
	// be told no.
	if msg.Message < wmKeyFirst || msg.Message > wmKeyLast {
		return false
	}
	if v, _, _ := procIsWindowVisible.Call(uintptr(f.hwnd)); v == 0 {
		return false
	}
	f.keyboardInUse(msg)
	r, _, _ := procIsDialogMessageW.Call(uintptr(f.hwnd), uintptr(unsafe.Pointer(msg)))
	return r != 0
}

// keyboardInUse makes the underlines appear when Alt is pressed.
//
// **This is the half `IsDialogMessage` does not bring with it.** The
// "underlines invisible" state is held by Windows, but what **removes** it is
// `DefDlgProc`, that is, the procedure of real dialogs — and this window is of a
// class of ours, which goes through `DefWindowProc`: without this line the panel
// is born with the underlines off, as it should be, and never turns them back
// on.
//
// The focus does not come through here: **it is always visible**, because the
// button's colour says it and not a ring — see paintButton.
func (f *flyout) keyboardInUse(msg *msgReceived) {
	if msg.Message != wmKeyDown && msg.Message != wmSysKeyDown {
		return
	}
	if msg.WParam != vkMenu {
		return
	}
	procSendMessageW.Call(uintptr(f.hwnd), wmChangeUIState,
		uintptr(uisClear)|uintptr(uisfHideAccel)<<16, 0)
}

// classNameOf gives a window's class name, for the log.
//
// It serves to answer "who took my activation away?", which is the only useful
// question when a panel vanishes: `Shell_TrayWnd` is the taskbar,
// `NotifyIconOverflowWindow` the hidden-icons panel, and a class of ours would
// mean we are taking it away from ourselves.
func classNameOf(h windows.Handle) string {
	if h == 0 {
		return "(none)"
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return "(unknown)"
	}
	return windows.UTF16ToString(buf[:n])
}

// maxStatusRows is how many rows a status line may take.
//
// **Three, and it is measured rather than chosen.** Two was the guess, and it
// was made by dividing a width by a width — which is not how text wraps. Asked
// of `DT_CALCRECT` in all five languages, four sentences need a third row, and
// the widest is not the one the arithmetic accused: English's
// `remote-no-ingress` breaks into *access from outside: open, but / nothing
// gets through from the / Internet*, while German's, 447 px against English's
// 432, happens to break into two.
//
// **The other three are the confirmation titles**, which is the find that
// matters: *¿Desconectar todos los aparatos?* in Spanish, French and Italian
// is the question asked before cutting off everybody watching, and on one row
// it was cut at both ends. It is drawn in the title's font, 16 at weight 600,
// so it needs a third row where the same sentence in 14 would not.
//
// **What the cap is for is the panel and not the sentence.** These lines sit
// above the QR code, so every row they take pushes the code, the address and
// every command down: a notice free to grow makes a panel that no longer fits
// beside the icon, and `CalculatePopupWindowPosition` would then put it
// somewhere else entirely. Beyond this the text is cut, with an ellipsis that
// says so — the old behaviour kept as a floor, not as a place to land.
// `TestEveryStatusLineFitsTheRowsItIsGiven` is what keeps the two in step.
const maxStatusRows = 3

// lineHeight is how tall the status line at that index is.
//
// **It answers one row when nothing has been measured**, which is the state
// `layout` can legitimately be in: it is called while the window is being built
// and from `WM_PAINT`, and only one of those comes after `rebuild`.
func (f *flyout) lineHeight(i int) int32 {
	if i < len(f.lineH) && f.lineH[i] > 0 {
		return f.lineH[i]
	}
	return f.px(flyLine)
}

// measureLines asks how many rows each status line really needs.
//
// **The font is the one that will draw it**, which matters for exactly one
// line: the first of a confirmation question is drawn in the pill's font, 16 at
// weight 600, and measuring it with the lines' 14/400 would say it fits when it
// does not. It is the same trap as a palette read from a screenshot — the
// drawing and the measurement have to come from the same source.
//
// The height is rounded **up to whole rows** rather than taken as the text's
// own: the panel's rhythm is `flyLine`, and a line 34 px tall among rows of 20
// puts everything below it half a row out of step with the padding it was
// designed with.
func (f *flyout) measureLines(width int32) {
	f.lineH = make([]int32, len(f.lines))
	if len(f.lines) == 0 || width <= 0 {
		return
	}
	row := f.px(flyLine)

	hdc, _, _ := procGetDC.Call(uintptr(f.hwnd))
	if hdc == 0 {
		// No device context is not a reason to lay the panel out differently:
		// one row each is what it has always done.
		for i := range f.lineH {
			f.lineH[i] = row
		}
		return
	}
	defer procReleaseDC.Call(uintptr(f.hwnd), hdc)

	for i, text := range f.lines {
		font := f.fontLine
		if f.pending != nil && i == 0 {
			font = f.fontTitle
		}
		if font != 0 {
			procSelectObject.Call(hdc, uintptr(font))
		}
		r := rect{0, 0, width, 0}
		txt, _ := windows.UTF16FromString(text)
		procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
			uintptr(unsafe.Pointer(&r)), dtCalcRect|dtWordBreak|dtNoPrefix)

		rows := (r.Bottom + row - 1) / row
		if rows < 1 {
			rows = 1
		}
		if rows > maxStatusRows {
			rows = maxStatusRows
		}
		f.lineH[i] = rows * row
	}
}

// bodyHeight measures how tall the wrapping text is, at the given width.
//
// **Whoever will draw it is asked.** Estimating it by counting characters would
// be wrong at every change of font or DPI, and the way it goes wrong is the
// worst one: the text leaves the rectangle and disappears, with nothing to say
// so.
func (f *flyout) bodyHeight(width int32) int32 {
	if f.body == "" {
		return 0
	}
	hdc, _, _ := procGetDC.Call(uintptr(f.hwnd))
	if hdc == 0 {
		return f.px(flyLine)
	}
	defer procReleaseDC.Call(uintptr(f.hwnd), hdc)
	if f.fontLine != 0 {
		procSelectObject.Call(hdc, uintptr(f.fontLine))
	}
	r := rect{0, 0, width, 0}
	txt, _ := windows.UTF16FromString(f.body)
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)-1),
		uintptr(unsafe.Pointer(&r)), dtCalcRect|dtWordBreak|dtNoPrefix)
	return r.Bottom
}

// colorOf converts a hue of the code to a COLORREF.
func colorOf(c qr.Colour) uint32 { return rgb(uint32(c.R), uint32(c.G), uint32(c.B)) }

// writePixel puts a COLORREF into a 32-bit DIB, which is **BGRA**.
//
// The order is the reverse of the COLORREF's, and swapping it gives no error: it
// gives a code with inverted colours, which a camera reads all the same as long
// as the contrast holds — that is, a defect that is not noticed here and is
// noticed on somebody else's phone.
func writePixel(pix []byte, i int, c uint32) {
	pix[i] = byte(c >> 16)  // blue
	pix[i+1] = byte(c >> 8) // green
	pix[i+2] = byte(c)      // red
	pix[i+3] = 0xFF
}

// insideRounded says whether the pixel is inside a square with rounded corners.
//
// The centre of the nearest circle is obtained by clamping the point into the
// inner rectangle: outside the corners the distance is zero and the test always
// passes, inside a corner it becomes the distance from that curve's centre. It
// is the usual way, and it is worth writing down because read in a hurry it
// looks as though it treats the sides as curves too.
func insideRounded(x, y, side, radius int32) bool {
	if radius <= 0 {
		return true
	}
	cx := min(max(x, radius), side-1-radius)
	cy := min(max(y, radius), side-1-radius)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

// fontFor picks the button's font: the main command carries 600/14, the
// secondary one 500/14. **They used to differ in size as well as weight**, and
// a button two points taller than the one above it was reported as the defect
// it is — the confirmation's question keeps the larger one, because a title is
// a title. It lives here because two callers ask for it — whoever creates the
// control and whoever paints it — and two separate choices would diverge.
func (f *flyout) fontFor(c flyCmd) windows.Handle {
	switch c.style {
	// The glyph does not use the font, but the control has one all the same:
	// it is the one the word would be written with if the glyph could not be
	// engraved, and it is the secondary button's because that is what it is.
	case styleGhost, styleIcon:
		return f.fontGhost
	case styleText:
		return f.fontSmall
	}
	return f.fontMain
}

// pill paints a pill-shaped button with antialiased edges.
//
// **GDI antialiases nothing**: `RoundRect` draws a curve in steps, and on a pill
// forty pixels tall the staircase shows. So the surface is composed by hand in a
// DIB — four samples a side, like the icon — and delivered with `BitBlt`.
// Outside the curve is the panel's colour, so the button blends with the
// background instead of leaving four corners.
//
// The radius is not a parameter: `border-radius: 999px` means half the height,
// and passing it would allow writing a pill that is not a pill.
func (f *flyout) pill(hdc uintptr, r rect, fill, border uint32) {
	w, h := r.width(), r.Bottom-r.Top
	if w <= 0 || h <= 0 {
		return
	}
	// **A shape is composed once.** Composing costs `w*h*16` points — at 192 dpi
	// six hundred thousand per button — and the buttons are all the same size
	// and almost all the same appearance: recomposing them one at a time shows,
	// and it shows exactly as one would describe it from outside, that is, a
	// colour that "arrives" down the panel. What tells two pills apart is all in
	// the key, so the cache cannot hand back another one's shape.
	if f.pills == nil {
		f.pills = map[pillKey]windows.Handle{}
	}
	key := pillKey{w, h, fill, border}
	if bm, ready := f.pills[key]; ready {
		f.blit(hdc, bm, r)
		return
	}
	hdr := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: w, Height: -h,
		Planes: 1, BitCount: 32, Compression: 0,
	}
	var bits unsafe.Pointer
	bm, _, _ := procCreateDIBSection.Call(
		0, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bm == 0 {
		return
	}
	// The bitmap stays alive in the cache: `release` frees it, with the others.
	pix := unsafe.Slice((*byte)(bits), int(w)*int(h)*4)

	radius := float64(h) / 2
	thick := float64(f.px(1))
	// **The centre has nothing to sample, and it is two thirds of the pixels.**
	// Beyond the radius from the two ends and away from the horizontal edges,
	// all four shapes contain the **whole** pixel: the colour is the fill,
	// exactly, and it is not an approximation — it is the value the sixteen
	// samples would arrive at. The hairline's vertical edges run at the ends,
	// which the margin on the radius already keeps outside.
	marginY := thick + 1

	const ss = 4
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			if float64(x) >= radius+1 && float64(x) <= float64(w)-radius-1 &&
				float64(y) >= marginY && float64(y) <= float64(h)-marginY {
				writePixel(pix, int(y*w+x)*4, fill)
				continue
			}
			var outside, inside float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if insidePill(px, py, 0, 0, float64(w), float64(h), radius) {
						outside++
					}
					if insidePill(px, py, thick, thick,
						float64(w)-thick, float64(h)-thick, radius-thick) {
						inside++
					}
				}
			}
			const tot = ss * ss
			c := f.pal.ground
			if border != 0 {
				c = blend(c, border, outside/tot)
			} else {
				c = blend(c, fill, outside/tot)
			}
			c = blend(c, fill, inside/tot)
			writePixel(pix, int(y*w+x)*4, c)
		}
	}

	f.pills[key] = windows.Handle(bm)
	f.blit(hdc, windows.Handle(bm), r)
}

// blit delivers an already composed bitmap.
func (f *flyout) blit(hdc uintptr, bm windows.Handle, r rect) {
	mem, _, _ := procCreateCompatibleDC.Call(hdc)
	previous, _, _ := procSelectObject.Call(mem, uintptr(bm))
	procBitBlt.Call(hdc, uintptr(r.Left), uintptr(r.Top),
		uintptr(r.width()), uintptr(r.Bottom-r.Top), mem, 0, 0, srcCopy)
	procSelectObject.Call(mem, previous)
	procDeleteDC.Call(mem)
}

// insidePill says whether the point is inside a rectangle with rounded corners.
//
// The centre of the nearest curve is obtained by clamping the point into the
// inner rectangle: on the sides the distance is zero and the test always passes,
// in a corner it becomes the distance from that curve's centre. Read in a hurry
// it looks as though it treats the sides as curves too, and that is worth
// saying.
func insidePill(px, py, x0, y0, x1, y1, radius float64) bool {
	if radius <= 0 {
		return px >= x0 && px <= x1 && py >= y0 && py <= y1
	}
	cx := math.Min(math.Max(px, x0+radius), x1-radius)
	cy := math.Min(math.Max(py, y0+radius), y1-radius)
	return math.Hypot(px-cx, py-cy) <= radius
}

// blend mixes two COLORREFs, component by component.
func blend(a, b uint32, t float64) uint32 {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	m := func(s int) uint32 {
		ca := float64((a >> s) & 0xFF)
		cb := float64((b >> s) & 0xFF)
		return uint32(ca+(cb-ca)*t+0.5) & 0xFF
	}
	return m(0) | m(8)<<8 | m(16)<<16
}
