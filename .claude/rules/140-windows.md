---
paths:
  - "internal/tray/**"
  - "internal/icon/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## In front of the machine there is an icon, and behind it a panel

**The monitor has no window**, so everything whoever is at the machine can see
of it is here: an icon whose colour is the state, the panel that opens behind
it, and the sheet Explorer reads out of the executable when the program is not
running at all. Two things draw it — `internal/icon`, which computes the
pixels, and Windows, which owns the controls — and the chapters below are where
that boundary was found by getting it wrong.

### The notification-area icon is not an ornament: it is the only presence

With `-H=windowsgui` the monitor has no console, and without the tray it would
have **no** visible presence: nothing saying it is running, and no way of
closing it other than Task Manager, that is, microphone and camera occupied
until somebody notices. And it holds the two commands that cannot live anywhere
else: disconnect every device, and reset the password.

**The icon is drawn, not embedded.** An `.ico` would mean `rsrc` or
`goversioninfo` in the build chain, that is, a third-party binary for 16x16
pixels. GDI makes bitmaps, and we compute the pixels. The result gains too: the
icon **is** the state, and a file would have wanted five images to keep aligned
with five constants. The shades are the page's phase colours, so the icon and
the browser beside it look like the same program.

**A colour says one thing, and it must say the worst one.** With the webcam held
by another process and no default microphone, the tray declared "check this"
instead of "fault": it reported the lesser defect and hid the greater. **The
order of the cases in `trayStatus` is the severity scale.**

**The contract with the notification area is declared, otherwise it is 1996's.**
Without `NIM_SETVERSION` one stays at version 0: the shell sends raw mouse
messages, the menu position has to be guessed with `GetCursorPos`, and **from
the keyboard the icon cannot be commanded at all** — which on a computer
attached to a television is the only way to reach it. With version 4 come
`WM_CONTEXTMENU` with the screen coordinates already inside `wParam`, and
`NIN_SELECT`/`NIN_KEYSELECT` for keyboard invocation.

**The two forms cannot be told apart by looking at the values**, and that is the
trap: with version 4 the event is in `lParam`'s low word and the icon id in the
high one, with version 0 `lParam` **is** the event. Whoever reads the wrong form
gets no error — the menu simply stops opening. Same for the coordinates, which
are **signed**: on a screen placed to the left of the primary one they are
negative, and read unsigned they send the menu sixty-five thousand pixels away.
The two readings live in `messages_windows.go`, separate from the rest and under
test. A refused version stops nothing: one stays as it was.

**And version 4 switches off the tooltip, which must be asked for with
`NIF_SHOWTIP`.** The shell assumes a modern application draws its own little
window on hover, so whoever does not draw one and does not ask for that flag is
left with nothing under the pointer. Declaring the version alone **removes** the
tooltip: `szTip` goes on being copied, `NIM_MODIFY` goes on answering yes, and
the line the onboarding draws simply stops existing. The defect is **the absence
of a thing**, which is the hardest to notice, and it is visible from no line of
code. The flag is added inside `notifyData`, where whoever carries a tooltip
also asks for it to be shown, and not in the two places that compose it —
forgetting it in one alone would give an icon born with the little window and
losing it at the first update.

Four non-obvious things, all already paid for by whoever writes trays:

- **A real hidden window, not a message-only one.** `HWND_MESSAGE` looks like
  the right choice, but message-only windows **do not receive broadcast
  messages**, and among those is `TaskbarCreated`, which Explorer sends when it
  recreates the taskbar: the icon would vanish at the first Explorer restart and
  with it every way of closing the monitor.
- **The message loop must be pinned to its thread** (`runtime.LockOSThread`): a
  Win32 queue belongs to the thread that created the window, and if the runtime
  moved the goroutine the icon would sit there responding to nothing —
  indistinguishable from a hung program.
- **`TrackPopupMenu` wants `SetForegroundWindow` before and any message after**,
  otherwise the menu stays open when one clicks elsewhere. It is prescribed, but
  it is the kind of line that looks superfluous during a tidy-up.
- **`cbSize` computes itself, so a wrong struct declares its wrong size
  precisely.** Windows accepts it, reads the fields at the offsets it expects,
  and out comes an empty tooltip — never an error. `win32_windows_test.go`
  checks sizes **and offsets**: two structs of the same length with two fields
  swapped would pass a size check alone.

**The process declares its DPI, otherwise the system lies to it.** A *DPI
unaware* process receives virtualised numbers: `SM_CXSMICON` answers 16 even on
a screen at 150%, we draw 16 pixels and the system stretches them to 24 — which
is exactly where the blur shows. With
`SetProcessDpiAwarenessContext(PER_MONITOR_AWARE_V2)` called **before** any
window or metric, the same call answers
24. It is the usual rule one step earlier: **make sure the system is really
    answering.** It must be **V2**: the documented difference is precisely the
    automatic scaling of the non-client area **and of Win32 menus**. Above it is
    the second half: entries are added with `AppendMenuW` and `MFT_STRING`,
    without `MFT_OWNERDRAW` and without a font of ours, so the text is drawn by
    Windows with the font of the right DPI — **there is nothing of ours to
    scale** except the icon.

**And that is why the icon size is resampled**, not read once in `New`: if the
scale changes while the monitor runs, the icon stays the old size and Windows
starts stretching it again. Both `WM_DPICHANGED` **and** `WM_DISPLAYCHANGE` are
listened to, and **the measurement settled which of the two does the work**:
changing the scale twice, from 200% to 175% and back, delivered
`WM_DISPLAYCHANGE` both times and `WM_DPICHANGED` **not once**. That window is
created 0x0 at the origin and never moves, so its own DPI can change only if its
monitor's does — which is exactly the case the first message is documented not
to cover, and the second handler was written for a reason nobody had seen hold.

**But the size it compared was the wrong number, and the branch could never
fire.** `GetSystemMetrics(SM_CXSMICON)` has no argument for a DPI and answers
with the **session's**, which Windows fixes at sign-in: measured, twenty-five
seconds at another scale with `SM_CXSMICON` still 32, `GetDpiForSystem` still
192, and `GetDpiForWindow` on that window still 192. So `n != t.size` compared a
constant with itself.

**What decided that this is a defect and not a harmless inertia is the shell's
own answer.** `Shell_NotifyIconGetRect` reports the slot it gives the icon in
real pixels, and it went from 64x96 to **56x84** within a second of the change —
exactly 175/200. The notification area is per-monitor and rescales at once,
while our process does not: the icon stayed 32 pixels wide in a box that wanted
28, that is, resampled by the shell. **The blur DPI awareness was declared to
avoid, arriving by the one road the declaration does not cover.**

So the size is asked of the screen the icon is on, by the road the panel already
uses to anchor itself: `Shell_NotifyIconGetRect`, `MonitorFromRect`,
`GetDpiForMonitor`, and `GetSystemMetricsForDpi` — the same question as before
**with the half that was missing**. Verified on the same gesture:
`session_says=32 screen_says=28` and the icon remade **16 ms** after the
message, then back to 32 on the way up. The fallback is the old road, for the
two cases that have no rectangle to ask about: at `New` the icon does not exist
yet, and on a Windows without `GetSystemMetricsForDpi` there is nothing better.

**And `New` is why `resizeIcon` runs on the icon's arrival too**, not only on a
scale change: the first size is computed before there is an icon, so it can
only be the session's. **The case it was written for is not reachable on this
shell**, and the measurement says why. On Windows 11 the notification area
lives on the primary screen, and the session's answer **is** the primary's DPI
for a process that starts now: with the 125% screen made primary and the
monitor started afterwards, a fresh process read `GetDpiForSystem` 120 and
`SM_CXSMICON` 20, and the icon was born at 20. What makes that a measurement
and not a coincidence is **who answered**: `Shell_NotifyIconGetRect` returned
`S_OK` with a 40x60 slot on that screen, so it was the screen's road and not
the fallback — which would have said 20 as well. **A fallback that answers the
same number hides whether anybody is there.**

**The session's answer does two things, and they do not contradict each other:
it is frozen for a running process and current for a new one.** The twenty-five
seconds with `SM_CXSMICON` still 32 above are the first, these are the second,
and together they close the question — at `New` the two roads cannot disagree,
because both resolve to the primary, and disagreement needs the primary to
change **after** the process started, which is a change and has a message.

**And that road was travelled, on the same two screens.** With the monitor
running, making the 150% screen primary again delivered `WM_DISPLAYCHANGE` with
`session_says=20 screen_says=24 drawn=20` — the frozen answer against the
screen's — and the icon remade **65 ms** later. It is the same defect as the
scale gesture arriving by a third road: not a screen changing its scale, but the
icon changing screen. The slot confirms the arithmetic at three scales, twice as
wide and three times as tall as the icon each time: 40x60 at 20, 48x72 at 24,
64x96 at 32.

**So the arrival call stays, declared as a road to degrade by and not as a
correction for anything reachable here**: it is the right answer for a shell
that puts the notification area anywhere but the primary, which this one does
not do and nothing promises the next one will not. It costs one call at
start-up, and removing it would leave that case with nothing at all.

**The box is decided by the manager, the drawing is decided by us.** Giving an
icon larger than `SM_CXSMICON` means making it shrink it, that is, going back to
blurry. **The proportions derive from `rMoon` and not from `size`**, so
enlarging it stays one number and the shape does not change. And **the icon
drawn in the onboarding is the same one**, otherwise whoever finishes the path
learns to look beside the clock for a thing that does not exist.

**An HICON is not a kernel handle.** Looking for a resource leak, `HandleCount`
grew by ~1.5 a second and looked guilty: those were the capture restarts. Icons
are **USER** objects, and they are counted with `GetGuiResources`, which
answered `GDI=7 USER=11`, steady. **The wrong tool always accuses the wrong
component.**

### The end of the session arrives at that window, and nowhere else

**The window that exists to carry an icon is also the only thing the operating
system can talk to.** With `-H=windowsgui` there is no console, so no Ctrl+C and
no `CTRL_SHUTDOWN_EVENT`; there is no service, so nothing is asked to stop. When
the computer goes off or somebody logs out, what arrives is `WM_QUERYENDSESSION`
and then `WM_ENDSESSION`, sent to every top-level window — and the hidden window
behind the notification-area icon is this program's only one. Until they were
handled, both went to `DefWindowProc`, which consents and says nothing: the
process was terminated where it stood and the orderly shutdown in
`cmd/pat-monitor` was never entered.

**What the kill costs is not the camera.** The system takes the camera, the
handles and the memory back from any process that exits — the argument already
written for the encoder that is not released at the end, and for the deadline in
`shutdown.go`. What it costs is the clip being written out, the viewers'
connections closed cleanly, and the last lines of the log. **The third is the
one that is paid later**: without them every shutdown of the computer reads in
the session log exactly like a crash, and the session log is the instrument the
night test is judged on.

Three decisions, and each has an obvious opposite:

- **The monitor closes on the verdict, not on the question.** The two messages
  are `WM_ENDSESSION` and `WM_QUERYENDSESSION`, and the second is put to
  everybody first: any one of them — another program, not us — can still answer
  no, and then the session goes on. Acting on the question would close the
  monitor on a shutdown that was called off, which is a room left unwatched for
  a key somebody else pressed. What waiting costs is the few seconds between the
  two messages, and those seconds were never ours.
- **`WM_ENDSESSION` arrives in both directions, and the direction is in
  `wParam`.** TRUE is the session ending, FALSE is the same message saying it
  was called off after everybody had been asked. A handler that reads the
  message and not the word closes the monitor in the second case too, and it
  looks identical in a diff.
- **The question is never refused.** Returning FALSE vetoes the shutdown: a baby
  monitor that stops the computer going off, at night, behind a dialogue nobody
  is in front of, would be worse than anything it is guarding. `DefWindowProc`
  consents by itself, so the case is written out only to put the promise in the
  file instead of in what nobody wrote — and to get the line that tells a log
  where the sequence started from one where the message never came.

**Nothing waits for the shutdown to finish, and the two obvious ways of making
it wait are both wrong.** Blocking inside the window procedure blocks the
message loop, and the message loop is a member of the same errgroup the shutdown
is waiting on: that is a deadlock with a ten-second timeout in front of it.
`ShutdownBlockReasonCreate`, which is the API that really buys time, buys it by
putting a screen in front of whoever is turning the computer off, naming us as
the reason it will not go. So the grace period is Windows's, not ours, and what
is written is the **start** of the shutdown with its reason: a log that ends
there says the process was killed inside that period, which is a different fact
from never having been told.

**And the console build needs a second word, for the same event.** Go's runtime
maps `CTRL_C` and `CTRL_BREAK` onto SIGINT and the other three — the console
window closed, the logoff, the shutdown — onto **SIGTERM**, so a
`signal.NotifyContext` asking for `os.Interrupt` alone leaves a `-Console`
monitor killed where it stands when the machine goes off. Both roads end at the
same cancel.

**And on the way out the notification area is not written to at all.**
`Shell_NotifyIcon` is a call into Explorer, and the one that takes the icon off
sits in `Run`'s defer — that is, on the message loop's thread, which is the
errgroup member `g.Wait()` is waiting for. At that moment Explorer is being torn
down as well, and a call that waits there would spend the grace period on
exactly the thing this handler exists to protect. **The work is pointless
anyway**: the notification area goes with the session. So the session's end sets
a flag and `removeIcon` stands down — it is "a resource the system is about to
reclaim is not worth waiting for", applied to somebody else's process. The other
writes to the shell — the refresh, the pulse, the balloons — are **not** gated,
because their window is the gap between the cancel and the message loop reading
its own `WM_CLOSE`, microseconds against a timer that fires once a second, while
this one is on the way out every time. Five more branches to cover a gap nobody
has measured would be five branches nobody can test.

**What has to fit inside Windows's grace period is under half a second.** With
the camera open and no viewer connected, the stretch from the line announcing
the session's end to `shutdown complete` measures in the high hundreds of
milliseconds, and the exit code is 0 rather than `exitShutdownStuck`; a run taken
straight after a rebuild reached a second and a fraction, which is the width to
expect rather than an anomaly. The grace period is measured in seconds, so not
blocking is affordable.

**And the measurement is re-taken without one**, which is the part worth
writing down: nothing is kept in the tree for it, and nothing needs to be. From
a second process, enumerate the top-level windows with `EnumWindows`, keep the
one whose process is the monitor's and whose class is `PATMonitorTray` — **not**
`FindWindow`, which answered 0 for that same class while `EnumWindows` was
returning the window a line later — then `SendMessage` it `0x0011`, and `0x0016`
with `wParam` 0 and then 1. The log says the rest. It exercises the real
procedure on the real window, which is everything here except how long Windows
waits.

### The executable's icon lives in the PE, and we write the resource

**They are two different icons: the tray's is drawn by the program while it
runs, this one has to be read by Explorer when the program is not running.** The
first can be the state, and indeed is; the second has to be inside the file, and
in the file it can only be a PE resource. Whoever reads the chapter above and
concludes "icons are not embedded" has half the rule.

The beaten road is `rsrc` or `goversioninfo`, that is, a third-party binary in
the build chain. Same question as the tray, same answer: the format is small,
unchanged since the nineties and documented, so `internal/icon` writes it.

Five non-obvious things, all paid for by reading the specification rather than
by trying:

- **The height declared in the DIB is twice the real one**, because the field
  counts the colour bitmap plus the mask, which sit one above the other. Writing
  the true height gives an icon shown half.
- **The AND mask must be there even at 32 bits**, where it is useless because
  transparency is in the alpha. It goes full of zeros, and omitting it shortens
  the resource by just enough to make whoever counts it read the wrong bytes.
- **256 is written as zero.** The side field is one byte, so 256 does not fit
  and the convention is that zero means it; writing 255 gives an icon that
  exists and is chosen badly. That size goes in PNG and not in DIB: 10 KB
  against 256.
- **The group with the lowest identifier wins**: the icon Windows shows is that
  of the lowest-numbered `RT_GROUP_ICON`, not the first one found.
- **The tree's leaves contain an RVA, and in an object file an RVA is not yet
  known.** Each one wants a relocation, otherwise the linker produces an
  executable whose icon points at the start of the file — and Windows does not
  complain, it shows the default, and there is nothing to read that explains it.

**A wrong resource does not complain**, and it is the family of hand-written
GUIDs. The test therefore rereads the tree by **walking it from the start**, as
whoever consumes it does, instead of recounting the fields with the code that
wrote them. And the one that counts is the last, done by hand:
`PrivateExtractIcons` on the compiled executable answers 16, 48 and 256 — that
is, it was read by Windows, not by us.

The `.syso` **is not in the repository**: `build.ps1` regenerates it with `go
run ./cmd/pat-icon`. It is derived entirely from the drawing, which is code, and
it is deterministic — the date in the COFF header is zero deliberately — so
there is no second copy that can diverge. Without `build.ps1` the binary still
compiles, and stays without an icon.

### The build number is the fourth field, not the third digit

`internal/icon/version.go`, in the same `.syso` as the icon — and that is not
convenience: an executable has **one** resource section, and two `.syso` in the
same directory do not add up. It is needed because whoever has the executable in
hand but is not running it — a file downloaded months ago, a copy on a stick —
has nowhere else to read which code it is.

**The third digit is not the commit counter**, however much `r` grows by itself:
`VS_FIXEDFILEINFO` has **four** fields, the first three say what the product is
— the third, by semver, being the fixes within the same milestone — and the
fourth says which build. Mashing them together would jump the patch from 0 to
127 with nothing having happened to the product. With four fields there is no
need to choose: `0.5.0.127`.

**The string is composed by `version.Full`, not by the generator.** The values
the linker stamps at runtime are assigned by hand in `pat-icon` before calling
it: that way the line Explorer shows and the one the monitor writes in the log
are the **same function**.

Three fields of the format say things that would otherwise have to be deduced:
`VS_FF_PRERELEASE` when the number carries a label, `VS_FF_PRIVATEBUILD` when
the tree is dirty, and the translation `040904B0` — **English**, like `LICENSE`
and `NOTICE`, because this sheet is read by whoever redistributes and by tools
that scan executables.

**The first of the three was derived from a consequence.** The rule was "on
while the major is zero", which on a 0.x says the right thing for the wrong
reason: at `1.0.0-beta.1` it switches itself off, that is, the executable would
declare itself **finished** at the exact instant the number says the opposite.
It is now decided by `version.Prerelease()`, which looks at what the number
**asserts** — semver's `-` — instead of at a consequence, and `icon.VersionInfo`
carries it as a field of its own. **A 0.x fixture does not distinguish the two
rules**, so the test that counts uses the form where they disagree, major 1 with
a label, in both directions: a flag lit wrongly is worse, because nobody opens
an `.exe`'s sheet and the falsehood would be read by whoever redistributes.

**Except `FileDescription`, which has a reader nobody had foreseen: whoever uses
the monitor.** Windows puts it **at the top of tray notifications**, where the
program name goes, and there sat a marketing sentence — that is, the alert
saying outside access had opened arrived under the heading of a brochure. It now
begins with the name, `version.Product + " - webcam over WebRTC"`, and the name
**is taken**: a second copy of "PAT Monitor" in that file would diverge at the
first rename. What was learned is not about language: **a field intended for a
sheet nobody opens can end up in an interface**, and then the first word
matters. `TestTheDescriptionLeadsWithTheProductName` looks only at that, because
it is the only part that second reader sees.

The traps are all in the lengths, and none of the three gives an error:
**`wValueLength` for text counts characters, not bytes**, terminator included —
doubling it is the step that gets forgotten; **`wLength` includes internal
padding** but not the padding after the last child; and **the padding between
the value and the first child gets forgotten**, because where the value is
already aligned it adds nothing — the error would have gone unnoticed until the
first odd-length value.

**And the resource tree was written for two types only**, with the offsets
computed by hand one after another: adding a third the same way is the place
where a wrong offset does not complain, because the tree stays well formed and
points elsewhere. It is built from a list, and the arithmetic is done once for
all. The test rereads the sheet by **walking it**, with a reader written from
the specification instead of from the code that produced it; and the one that
counts is the last, done by hand: `(Get-Item …).VersionInfo` answers with every
field, and `PrivateExtractIcons` goes on answering 16, 48 and 256 — the third
type did not move the first two.

### The tray menu: first what is happening, then what can be done

Whoever opens that menu does one of two things — check how the monitor is, or
open it. The state therefore sits at the top, where the eye lands, and the first
clickable entry is the one the double click also does, in bold, because that is
how Windows declares the default action.

- **What cannot be undone stands alone and asks for confirmation.**
  Disconnecting everybody interrupts whoever is watching, and in a short menu
  the entry above and the one below are a few pixels away.
- **Entries that do nothing are not there**, and neither are those that lead
  nowhere: an entry describing a problem with no address to open does what a log
  line does, while occupying a command's place.
- **The ellipsis says a question will follow**, the ampersand gives the keyboard
  accelerator, and "Exit" says what happens — exiting frees camera and
  microphone and stops watching, and the word alone does not say so.

Three lines, in this order: **how it is**, **where it can be seen and who is
watching**, and who it is. The third — name and version — stays because this is
the only place reachable without clicking anything and without knowing the
password, and it is the first thing needed by whoever is helping somebody else
over the phone. The numbers stay in the menu, and they are two different
questions: the menu is opened to do something, the tooltip is hovered to find
out whether one can go to bed. Zero viewers is not written as a figure — "0
viewers" reads as a broken counter, while the question has an answer in words.

**All in 127 characters**, because `szTip` is a fixed buffer and `copyTip`
truncates **silently** — and the ceiling holds **in every language**, not in the
writer's: a German sentence is on average a third longer than an Italian one, so
the test runs over every catalogue and over each one's worst case. **A fault
always beats the sentence**: that is what the tooltip shows in place of the
counters, otherwise whoever hovers the icon with the camera stopped would read
"At home · nobody is watching" — misplaced confidence in the worst place.

**The drawing at the end of the guided path shows this panel, and it carries the
panel's own labels.** It used to be a pale box reading "Copy address · Quit",
that is, the system menu that no longer exists — and before that "Copy address ·
Settings", where a "Settings" entry had never existed at all. Hand-copied labels
in a drawing are a second list and they drift, so the illustration's text comes
from the same `tray.menu.…` entries the panel renders, through
`data-i18n-plain`, which is the sentence without the accelerator marker Windows
underlines under Alt. **A rename now rewrites the drawing too.**

What follows about that figure is decisions, not detail.

**Its colours are read from the source and not from a screenshot.** The panel's
ground is what `fillRect` paints all of it with, and the buttons are that
**same** ground with a hairline of `line` around them — only their border tells
them apart. Two drafts had it the other way round, the panel in `card` and the
buttons in `ground`, and then the panel came out the same shade as the figure
holding it: a border around nothing. The second draft cured that by inventing a
third shade, which is worse — **the right colour was never the problem to design
around, it was the one to go and read.** Everything in it is centred, as it is
there, for the reason written there.

**And so are its weights, which the first version did not read.** Side by side
with a photograph the drawing looked heavier than the panel, and every number
that made it so was in the same file as the colours. The hairline is `thick :=
f.px(1)`, one pixel — and the figure being life-size, a stroke of 2 was
literally twice the panel's. The status lines are `fontLine`, 14 at weight
400, and the address `fontSmall`, 13 at 400: in the panel the address is the
**smaller** of the two, and the drawing had the pair the other way round. **A
palette read from the source and the rest taken by eye is the defect the
palette rule exists to prevent**, one level down; what hid it is that the
geometry was faithful — 260, 12, 150, 20, 40 and 6, all of them right — so
nothing looked wrong until the two were put beside each other. The one stroke
that stays 2 is the glyph's, because that 2 is the set's in its own grid of 24.

**It is life-size.** Stretched to the card it filled half the figure with nothing
while showing the shape at twice the size it has on screen; `.art.life-size`
caps it at its own width, so the buttons are the 40 they really are.

**The code is a motif and carries no data**, and it is **the one element drawn in
a colour the real one never uses**. Twenty-one modules, three finders and a fixed
scatter, which is the condition for drawing one at all — a code that scans, on
the screen that teaches the gesture, would lead nowhere. The panel engraves in
`ink`, near-black at 13.4:1 on its paper; the drawing uses `--tray-wash` at
**1.66:1**. Everything else in that figure is faithful; **the one thing somebody
might point a camera at is deliberately not.** The low contrast is the property
and not a compromise, and the modules are 6 wide to carry it — what has to read
is the shape of a code, not its data, and there is no data. **A ratio meant for
text says this is illegible, and is measuring the wrong thing**: it was drawn and
looked at.

**And the colour is the panel's own.** A first attempt borrowed `--star-1`, the
night page's star teal — faint enough, at 1.96:1, and still a visitor, because
the tint belongs to another page. What the code wants is the wash that fills the
command with the focus, and that mixture exists in no palette, because the tray
computes it: `blend(ground, accent, 0.22)`. **A value the palette does not hold
is not a reason to reach outside the component: it is a reason to write the
mixture instead of its result.** `--tray-wash` is `color-mix(in srgb,
var(--c-home) 22%, var(--ground))`, the same formula in the page's language —
which the sheet already speaks, the `.note` boxes composing their ground and
border from the phase at 8% and 26% with no hex anywhere. A `#B8C7BD` would be
a copy of a number the panel keeps recomputing; written as the mix, moving
either ingredient moves the drawing with the panel. **And it is `--c-home` and
not `--phase`**: the last step is green, and the tray's accent is not. The
border of that same pill is the 60% mix and stays inline — it has one user, and
a name nobody looks up is a name that drifts.

**And the address is a placeholder, not a plausible name.** The documentation's
examples use a tailnet that does not exist, which is right in prose and wrong
here: on a screen where the real address sits a few lines above, a name that
looks real is read as one. The half that is ours stays concrete — `patmon-` and
six hex digits, which is what the monitor generates — and the half that is
theirs says so. It goes through the catalogue, because the path has taught its
reader "Tailscale" and never "tailnet".

**The two status lines carry example numbers, deliberately not the real ones**:
the heartbeat knows how many are watching, and using it would make the
illustration follow the measurement, which is how a drawing turns into a second
instrument.

The "to do" text comes from Tailscale, so it can be long and multi-line:
`firstLine` shortens it and **declares** the truncation, because a sentence cut
without an ellipsis reads as a finished sentence.

**The system menu is gone**: the right button opens a panel of ours, and this
chapter remains for its rules — which the panel has inherited, all of them — and
for a wrong estimate. It said that controlling the frame meant abandoning the
`HMENU` and redoing keyboard navigation, click-outside dismissal, DPI and
accessibility by hand: **those four are not redone by hand**, and how that was
possible is in the chapter below.

### The tray panel: the pixels are ours, the controls are Windows's

`internal/tray/flyout_windows.go`. The right button on the icon opens a panel
drawn by us — QR code, state, commands — with the pages' two palettes and the
theme chosen by Windows. The left button goes on opening the monitor, and **that
is why the panel has no entry that opens it**: repeating a shorter gesture
occupies a command's place without adding anything.

**What authorises removing the menu is that the controls stay the system's.**
The buttons are real `BUTTON`s with `BS_OWNERDRAW`: **only who paints** changes,
so focus, keyboard and accessibility come free — verified that Narrator reads
them, and that is the answer that decided it. A window class of ours would have
lost all three. The keyboard is given by `IsDialogMessage`, which the
documentation says can be used "with any window that contains controls".

**Four calls do the work that looked like ours**, and that is why the panel
costs what it costs: `Shell_NotifyIconGetRect` says where the icon is **now**
(the notification area reorders itself, and on Windows 11 a new icon is born in
the overflow); `CalculatePopupWindowPosition` — the same routine as
`TrackPopupMenu`, with the same flags — positions it already flipped at the
edges and clipped to the work area; `DwmSetWindowAttribute` gives corners,
acrylic, dark frame and border colour; `IsDialogMessageW` gives the keyboard.
There is not one undocumented ordinal in this file.

**The confirmation lives inside the panel, and that is not a preference: it is
the mechanism.** The panel closes when it loses activation, and any modal window
takes it away — a `MessageBox` would make it vanish under its own question.
Asking inside also removes the need for a defaulted "No": "No" is the first
command, and that is where `IsDialogMessage` puts the focus.

**The window is opaque, and the glass is put behind by DWM.** With per-pixel
alpha the GDI text would come out fringed: ClearType antialiases against a
background it assumes is black and knows nothing of the alpha channel. It is the
constraint that, got wrong, is paid for with a rewrite.

#### The typeface is the system's, the sizes are ours

The panel hard-coded `"Segoe UI"`, and its own comment named the road out for as
long as it stood there. **The two halves of a font are not the same question.**
The scale — `tBody`, `tUI`, `tSmall`, the two weights — is the stylesheets', it
was measured on this panel, and the rows above are a record of what it costs to
take one of those numbers from somewhere else. The **face** is not ours to
choose at all: on a Chinese, Japanese or Korean Windows, Segoe UI has no glyph
for most of what this panel would ask it to draw, and GDI answers with the box
every user of those systems recognises. So the face and the character set come
from `SystemParametersInfoForDpi(SPI_GETNONCLIENTMETRICS)`, `lfMessageFont` —
the face Windows writes its own message text in — and the height is deliberately
left behind, because that is the system's text size and taking it would throw
the scale away.

It is **ask the system rather than choose**, in the place where the answer
cannot be guessed from here: the videos folder, the local address and the
preferred languages are the same rule, and this is the one where the right
answer depends on a script nobody here can name in advance.

**The DPI variant, for the reason the icon's size already cost this file.**
`SystemParametersInfo` without it answers with the **session's** DPI, which
Windows fixes at sign-in; the panel knows the DPI of the monitor it is opening
on and hands it over.

Three things that give no error when they are wrong:

- **`cbSize` makes a wrong structure look right.** The call is refused unless
  the size matches, and the size is computed from the declaration — so a field
  left out produces a structure that declares its own wrong size **precisely**,
  which Windows then fills while our reads come out of the wrong offsets. It is
  `NOTIFYICONDATAW`'s trap one structure across, so the whole of
  `NONCLIENTMETRICSW` is declared for one field of it, and the offsets are
  checked rather than trusted: 92 bytes for `LOGFONTW`, 504 for the whole, the
  message font at 408. Leaving `iPaddedBorderWidth` off gives 500 and the call
  fails on every Windows this program supports.
- **The refusal and the answer are the same string here.** An Italian Windows
  writes its messages in Segoe UI, which is exactly what the panel used to
  hard-code — so a test on the value would pass over a call that had never
  succeeded. The decision is therefore handed the answer rather than asking for
  it (`messageFaceFrom`), both directions are tested, and a third test asks the
  real call and fails if it refuses. **A fallback that answers the same thing as
  the road it stands in for hides whether anybody is there.**
- **The character set travels with the face.** `DEFAULT_CHARSET` makes GDI
  choose from the system locale, which is nearly always the same answer arrived
  at by a second road; the pair Windows drew with is the pair to draw with.

**Measured on this machine**: the call answers `Segoe UI`, charset 1, and the
five photographs of the panel are unchanged. That is the whole of what can be
shown from here — the road changed and the pixels did not — and what it buys is
on a machine nobody here has.

#### Removing the non-client area does not remove whoever paints it

`WS_THICKFRAME` is there for one reason: **DWM rounds the frame**, and a bare
`WS_POPUP` has none — `DWMWA_WINDOW_CORNER_PREFERENCE` is accepted and has
nothing to act on. The client rectangle takes the whole window by answering zero
to `WM_NCCALCSIZE`, and measured it works: client and window are 520x1068 in all
six openings.

But **`WM_NCPAINT` and `WM_NCACTIVATE` derive the frame from the style**, not
from the rectangle, and draw it over the content. Measured by reading the
screen's pixels: two pixels of the colour we declare and then **sixteen of
0xB4B4B4 on all four sides**, which at 192 dpi are the eight of `SM_CXSIZEFRAME`
plus `SM_CXPADDEDBORDER`. Answer zero to the first and pass `lParam` as -1 to
the second, which is how one tells `DefWindowProc` "change state, do not touch
the pixels".

**It appears on activation**, and that is why it could not be seen from here. A
panel opened by sending the icon's message from another process never becomes
the active window, so that frame is never repainted: the defect showed only with
a real click, and only **from the second opening on** — the first in which
activation *changes* instead of already being there. "It only does it if I open
it" was the measurement, not an impression, and it is what narrowed the field to
what depends on activation.

#### The first image is not drawn by WM_PAINT

That message is taken **when the queue is empty**, and between the window
appearing and the loop picking it up sit activation, focus and the state of the
underline indicators: enough for DWM to compose a frame of a not-yet-painted
window, which shows as a light flash. It is remedied from two sides: a **class
brush** of the panel's colour, so that even what appears before us is the right
colour, and drawing **immediately**, outside the queue.

**And the children must be redrawn with it**, which is the forgotten half:
`WS_CLIPCHILDREN` takes the buttons' rectangles away from the parent — they
paint themselves — and `UpdateWindow` updates **one window only**. So the
background came out already dark with seven rectangles still to paint, that is,
the white showing through exactly in the shape of the buttons. `RedrawWindow`
with `RDW_ALLCHILDREN | RDW_UPDATENOW` takes them all.

**And those three were still not enough: the frame must not be composed at
all.** Reported from in front of the machine — "a bit of white on opening, worse
on the dark one" — and **that asymmetry is the diagnosis**: the ground is the
same colour in both palettes, so a defect in the ground would show equally,
while an **undrawn** rectangle is conspicuous on near-black and invisible on
cream. What is undrawn is the buttons: `BS_OWNERDRAW` means nothing can paint
them before `WM_DRAWITEM`, they are born `WS_VISIBLE` so they appear with the
parent, and `WS_CLIPCHILDREN` takes their rectangles away from the parent's
erase — which is the one thing the class brush cannot reach. The three earlier
remedies all make the painting happen sooner, and none of them can make it
happen before the window is on screen.

`DWMWA_CLOAK` hides the window from DWM while leaving it visible to USER, so
`RedrawWindow` still paints everything and uncloaking publishes **one finished
image**. It changes what `first_paint_ms` means rather than making it idle: it
used to be the length of the unfinished image, and it is now the wait before
anything appears — a delayed correct panel against an immediate wrong one, which
is this program's direction everywhere. **Refused, it changes nothing**, and the
uncloak sits where nothing can return before it: a window left cloaked is a
panel that never appears.

**It was confirmed by looking, and could not have been confirmed otherwise.** A
throwaway probe was written first, precisely because `DwmSetWindowAttribute`
answering `S_OK` is the kind of answer this file does not accept — a class with
a dark brush, one owner-draw child with a deliberately slow `WM_DRAWITEM`, and
the screen's pixels sampled through a 1x1 DIB with and without the attribute.
**It measured nothing**: neither variant ever showed the finished colour, so it
could not compare them, and neither ever showed a light pixel. That is the
chapter above again — a panel driven from another process never really becomes
active, and what depends on activation cannot be seen from here. The probe was
deleted rather than have a conclusion drawn from it, and the two gestures that
settled it were somebody opening the panel three times.

#### `DefDlgProc` is not there, and its omissions give no error

`IsDialogMessage` brings navigation, not the whole dialog handler. The "keyboard
indicators visible" state is kept by Windows, but **removing** it at the first
Tab is `DefDlgProc`, that is, the real dialog procedure — and this window is of
a class of ours: the panel was born with the underlines off, as it should, and
never turned them back on. **Alt is asked for by us.**

And the state is read **after** letting `DefWindowProc` work: it is written by
it *while handling* `WM_UPDATEUISTATE`, so interrogating it before answers with
the value that message is announcing has changed — and what one gets back never
changes. **A request accepted is not a request executed**, and it is the family
of the GUID that does not complain: Windows agrees to hide the underlines and
does not hide them, because the buttons' text is drawn by us and applying it is
`DT_HIDEPREFIX`'s job.

#### A shape is composed once, and the centre is not sampled

Every pill is antialiased at 4x4 samples per pixel: at 192 dpi that is six
hundred thousand points per button, and composing seven in a row **shows** —
from outside it is a colour running down the panel.

Two remedies, and the first is worth double: **what distinguishes two pills is
all in one key** (size, background, border), so it is composed once and
delivered with a `BitBlt` — the five secondary commands are the identical image.
And **the centre has nothing to sample**: beyond the radius from the ends and
away from the edges, every shape contains the whole pixel, so the colour is the
background — exactly, not by approximation. Measured on the same panel: **51 ms
→ 15 ms** of first draw. It follows also that **what does not change is not
repainted**: a change of underlines concerns every label, focus concerns one
button.

#### One mark for one thing

The command with focus is declared with the colour of the viewer's lit switches
— accent border at 60%, background at 22%, text `--ink` — and **the focus ring
is gone**. They were two marks for the same thing, and the second read as a
system outline that had wandered in.

**With the ring went the machinery that existed to hide it**, and that is the
part that matters: `UISF_HIDEFOCUS` existed because a ring must not appear to
somebody opening with the mouse, while a **selected command must always be
visible** — at opening, focus is already there, we give it. The premise having
changed, the state became dead weight and must be removed, not kept "just in
case". What remains is the real half of the same mechanism: the underlines,
which show only on pressing Alt.

**And the main command wears that same mark, which it did not.** It was filled
with `pal.accent` solid, under a comment calling it *the phase's colour* — and
`pal` is `palDark` or `palLight`, chosen by the Windows theme, so that fill had
never been the phase's anything. **A comment that cannot fail, describing
something the code had never done.**

What it produced on screen is the report that found it: the same token reads
dark green at 22% over near-black and **bright cyan at 100%**, so a panel
holding one accent appeared to hold two, and the louder of the two belonged to
nothing. Both now go through `marked()`, one function because there is one mark;
what still separates the main command from the one with the focus is the weight,
600 against 500, which is the difference that was meant to be carried all along.

**And then two marks at once turned out to be the price nobody wanted**, which
is what the next photograph showed: with a permission refused *and* a step
waiting on Tailscale, the panel carried two. The rule is therefore narrower than
it was — **the mark belongs to the focus and to nothing else**, and the focus
goes to the main command when there is one. The main action is the selected one
by construction, so there is one accent on the panel whatever state it is in,
and it is never sitting on something that is not the main action.

It costs the command answering the notice its mark in that one state. That is
the right way round: the notice above it already names the device, and the step
is the thing the monitor cannot get past on its own.

#### What is shown is not what is used

The address is a command: press it and it is copied. **It is shown without
`https://`**, because the panel is 260 points wide and the part that identifies
an address is the last — the scheme eats eight characters at its expense — with
a trailing ellipsis for when that is not enough. **What is copied stays whole.**

The QR code carries the public address if the tunnel is up, otherwise the home
one, which is the address chosen by the system and not `localhost`. Beyond 106
characters `internal/qr` does not engrave, and then not even the code's space is
kept: a gap in the middle of the panel reads as an image that has not finished
loading.

#### A notice gets the rows it needs, and an answer sits under its question

Everything in this panel is centred, which is argued where the drawing of it is;
the price is that **a line too long for its rectangle loses its first word and
its last**, and reads as a line. That is the worst way to be truncated — one
cannot even tell something is missing — and the panel knew it in one place only:
the note beside the icon buttons' fallback says exactly that, about a label
almost nobody ever sees.

**Measured at 96 dpi against the 236 px of a full-width row**, in all five
catalogues, it was reachable in eight places:

| | |
|---|---|
| `tray.fault.remote-no-ingress` | **447** px in German, 434 in English, 408 in French |
| `tray.fault.no-password` | over in **all five** languages, 259 to 351 px |
| `tray.fault.mic-filtered` | over in English (251) and Spanish (247) |
| the confirmation titles | over in Spanish, French and Italian |
| the widest command label | `Beenden und Monitor ausschalten`, **210** — 26 px of room |

**That last row said 236 — *exactly* the room — and it was wrong**, which is
worth more than the row. The guard that produced it built `fontGhost` at
`tBody`, two points above the `tUI` the panel draws it with, so it measured a
label nobody sees; and it is the very trap the guard beside it names in its own
comment, *measuring them with the lines' font would say they fit when they do
not*. It errs strict, so it never went green over a defect — **what it did was
publish a number**, and a number taken with the wrong instrument is the one kind
of error this file cannot absorb. The fonts are `create`'s now, and the same fix
found a second half: `tray.confirm.yes` is the panel's one pill with a word of
ours on it, so it is drawn two points *larger* than its neighbour "No" and was
being measured two points small — that half erred lax. Found by a review, not by
a test: **a guard cannot check the instrument it is made of.**

**That two-point difference is gone, and the same report removed it.** One font
served the confirmation's question and the main command: 16 at weight 600, which
is right for a title and wrong for a button standing beside others at 14 — `Sì,
procedi` two points larger than `No, lascia stare`, which is what somebody saw
and called a defect. It was defensible while the main command painted itself a
different colour, that is, was a different object; once the mark went to the
focus alone, the size was the only loud thing left. The question keeps 16/600
and the command takes 14/600, so what separates it from its neighbours is the
weight.

**And the guards built their own fonts by hand**, which is the list this
paragraph already records going wrong once. There is one list now, `makeFonts`,
and the panel and its guards both call it.

**The one that matters is the fourth row.** `tray.confirm.revoke.title` is the
question asked before cutting off everybody who is watching — *¿Desconectar
todos los aparatos?* — and on one row, in three of the five languages, it was
cut at both ends. A destructive command was asking its question in a sentence
nobody could read.

**An ellipsis was put there first, and it was the wrong repair.** It makes the
cut visible, and what was wanted was the sentence: these lines are the answer to
the question the panel is opened for, and half an answer is not one. So the
status lines **wrap**, into as many rows as `measureLines` measures for them,
and the ellipsis stays underneath as the floor for whatever does not fit even
there.

**`maxStatusRows` is three, and it is measured rather than chosen.** Two was the
guess, and it was made by dividing a width by a width — which is not how text
wraps. English's `remote-no-ingress` is *narrower* than German's and needs one
row more, because it breaks into *access from outside: open, but / nothing gets
through from the / Internet* while the German happens to break in two. The three
confirmation titles need the third row for a different reason: they are drawn in
the pill's font, 16 at weight 600. **A cap derived from an arithmetic that does
not model the mechanism is a cap that is wrong in the cases nobody predicted**,
and here the guard found all four within a second of being written.

What the cap is for is the panel and not the sentence: these lines sit above the
QR code, so every row they take pushes the code, the address and every command
down, and a notice free to grow makes a panel that no longer fits beside the
icon. `TestEveryStatusLineFitsTheRowsItIsGiven` keeps the two in step, in every
language, because **what overflows is a translation and not the base.**

**And wrapping brought a defect of its own, in the one line that is not a
sentence.** The counters are a pair of facts joined by a middot, and the wrap
broke *aparatos registrados:* from its *3*, leaving a row carrying a lone digit
— which reads as a fault rather than as a number. The space before each
placeholder is non-breaking now, in all five catalogues: **a figure is not
separable from the label that names it**, and that is a typographic fact rather
than a change of wording, so not one sentence moved.

**And the guard counted rows and never the width, which is the half that lets
the worst case through.** `DT_CALCRECT` with `DT_WORDBREAK` returns the width of
the widest line and goes **past** the box it was given when nothing in the text
can be broken — so a sentence with no break opportunity measures **one row**,
passes a count of rows, and is drawn cut at both ends, which is the failure this
whole section exists for. Measured with an unbreakable line put in: 391 px in a
row of 236, one row, green. It costs nothing in the five languages here, where a
space is always available; it is the whole of the question in a script that has
none, where whether GDI breaks between characters is a property of the flags and
not of the sentence.

**The commands do not wrap and must fit**, because a button has one height and a
taller one among the others is a crooked column. The ellipsis is there
underneath — it has to be, since `tray.menu.todo` carries Tailscale's own
sentence and **a line is not a width**: `firstLine` takes one line of it and it
had been drawing `ve this machine in the Tailscale` for as long as it had
existed. But **an ellipsis on a word somebody chose is a word chosen badly**, so
`TestEveryCommandLabelFitsThePanel` measures the ones that are ours.

**And then the sentence was taken off the button altogether**, which is what
that paragraph had been arguing for without noticing. The step Tailscale is
waiting on used to be one key, `tray.menu.todo`, carrying their prose: what
appeared was `Da fare: approve this machin…` — a paragraph cut mid-word, in
another language, on the one command the reader has to press. **The ellipsis was
there and it did not help**: a cut sentence does not read as a short one, it
reads as a defect, and it was reported as one.

The division is the panel's own, the one the permission's notice already makes:
**the line names the thing and the button says what pressing does.** The
explanation goes to the status rows, which wrap into as many as they are given —
theirs when their control server answers, since it composes it knowing the
tailnet and the reader's role, ours when it does not. The button carries a short
label of ours per action, so `TestEveryCommandLabelFitsThePanel` measures it like
every other, and the truncation is gone rather than decorated.

**Three of the six actions never produced a button and still do not**, and that
is a reading rather than a decision: `wait-certificate` says in as many words
that there is nothing to do, and `other-user` and `failed` carry no address.
They answer with an empty label, which is `settingsPage`'s shape, and a guard
walks `tunnel.AllActions` so that an action added over there and forgotten here
fails instead of quietly producing a panel with no command.

**The three labels share one accelerator, and the guard folds them into one
slot.** They are alternatives — only one can be on the panel at a time — so
demanding three distinct letters would be a rule stricter than the thing it
guards, and it would spend three of a language's free letters on one row.
Sharing is also what one wants from the keyboard: whatever the step is, it
answers to the same key.

**And a command that answers a line goes under that line.** `flyCmd.lead` is the
one row between the status and the code, and there is exactly one user: the
Windows settings page for a permission the notice has just declared off. In the
column at the bottom it sat four rows from the sentence it belongs to, with the
code and the address in between, where it reads as belonging to the address. It
is deliberately not a general slot — a second one would be a second main answer,
and the panel has one.

**It wears no pill**, and that is *One mark for one thing* met from the other
side: the focus falls on the first command, which is this one, and the selected
command already wears the accent. A filled pill on top of that is two marks for
the same thing. The pill stays with the tunnel's step, which is a different
question and is the one thing the monitor is waiting on somebody for.

**Every one of these was found by looking**, and most of them only by looking:
the arithmetic in `layout` is right in every one of those panels, and
`TestEveryTrayKeyIsInEveryCatalogue` is green over a line missing its first
word. `TestLookAtThePanel` is the instrument — `PATMON_LOOK=<folder>`,
`PATMON_LOOK_LANG=<tag>`, one PNG per state — and it is kept for the reason the
pulse's is: **what a photograph answers, no assertion about the same panel
does.** The guards came after it, and each one is what the photograph taught
written down so that nobody has to take it again.

#### Two folders are two glyphs, and the word stays on the control

The two commands that open the clips folder and the log folder are **two glyphs
side by side just above "Exit"**. They open nothing of the monitor's: they lead
to what the monitor has **left behind**, so they sit at the end, after the
commands that act on it and before the only one that switches it off. Two whole
rows for two destinations opened rarely lengthened the panel by eighty pixels in
order to write the word "folder" twice, which is the one half that does not
distinguish them.

**They are two kinds of content and not two folders**, and that is the decision
that counts: the set has a folder, and it has **one** — two folder glyphs side
by side would be the same drawing twice, that is, two commands indistinguishable
from outside. What separates them is what is inside, `video` and `file-text`,
which is also what one is looking for when opening one or the other.

**And among the glyphs that say "video" the silhouette won, not the subject.**
The first was `movie`, the film strip: it says "film" instead of "recordings",
at the panel's size its eight holes smudge into a band that reads as a ticket,
and above all it is **symmetric** — next to the sheet the two silhouettes
resemble each other, and two commands side by side either are distinguished at a
glance or are not. The camera has the lens sticking out on one side, which is a
difference visible before the drawing is recognised. Also tried and discarded
were `folder`, which says "folder" and not "video", and `camera`, which says
"photography". **All four were looked at** — as characters, on a grid of 34 —
rather than chosen by name.

**The word does not disappear, it stops being drawn.** The control's text
remains, so Narrator reads it and the accelerator letter goes on responding: it
is the same distinction as the address above. Removing it would give two buttons
with no name from the keyboard, and this panel is also the interface of a
computer attached to a television. **The price is declared instead of hidden:**
with no label and no tooltip, mouse users have to try them once to learn them —
a real tooltip is a `comctl32` control, and without a manifest it would be the
grey v5 one inside a panel drawn by us.

**The glyph is engraved onto the button's background, not over it.** The bitmap
arrives with `BitBlt`, which just copies, so what lies outside the stroke must
already carry the right colour — possible because the glyph falls in the pill's
**flat centre**, the same property for which `pill` does not sample its own
centre. A different background — focus, pressed — is a different key in the
cache and not a case to handle; if it were not, a square would appear around the
glyph **only on the button with focus**, that is, it would show only by tabbing
that far. `TestTheEngravingCarriesTheButtonBackground` nails it, on the three
real colour pairs.

**The `d` strings are verbatim, and a tolerant reader would be the defect.** The
strings in `internal/tray/glyph_windows.go` are copied from the `<svg>`
attribute untouched — the only way they can one day be re-compared with the set
— and the reader covers what this family uses and **refuses the rest**: a cubic,
a rotated ellipse, an unknown command return an error instead of being skipped.
Skipping them would give a glyph **plausible and wrong**, which nobody sees
until they look at somebody else's panel.

How the engraving works, which is the non-obvious part: **a round stroke is the
set of points within half a stroke of the line**, so by measuring that distance
the round caps and joins — the set's two `stroke-linecap` and `stroke-linejoin`
— come for free, with no special case for corners. Only the band at the edge is
sampled: beyond half a pixel diagonal from the stroke the outcome is already
decided, and that is nine tenths of the pixels. And **a glyph is looked at**, as
always: first as characters on a grid of 40, then on the real panel photographed
from outside.

#### How to look at a panel that closes when you look at it

This is the method part, and it is needed next time. The panel closes on losing
activation, so it cannot be inspected with anything that steals focus. It is
driven from a separate process: send it the **icon's message** (`WM_APP+1` with
the event in `lParam`'s low word, not a real `WM_CONTEXTMENU`, which reaches
nobody), attach the input queues with `AttachThreadInput` to read and move
focus, post a `WM_KEYDOWN` and count **how many steps one press makes** — one
button, measured, when it looked as though they all scrolled.

Two traps of the tool, which accused the code in the right place for the wrong
reason: **without declaring the DPI** the system answers in virtualised
coordinates and the screenshot takes a different piece of screen; and
**`GetWindowRect` includes the invisible resize border**, so it also crops what
is behind the window — the rectangle of the real pixels is given by
`DWMWA_EXTENDED_FRAME_BOUNDS`. `PrintWindow` is no use here: it answers black,
because we do not handle `WM_PRINTCLIENT`.

**And colours are read, not judged.** "There is a white border" became solvable
when it became `+0:262D32 +2:B4B4B4 +18:121517`: the first is ours, the third is
the ground, and the second belongs to neither.

