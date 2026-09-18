---
paths:
  - "internal/record/**"
  - "internal/server/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## Recordings are the watcher's files, not the application's data

Clips live in **`Video\PAT Monitor`** of whoever uses the PC. They used to live
in `%APPDATA%\PAT Monitor\video`, beside the configuration and the log, and that
is the right place for those two: nobody opens them by hand. For films it is
wrong — `%APPDATA%` is hidden, appears in no library, and whoever wants to watch
a clip outside the browser has to know that a folder Explorer does not show
exists.

**The folder is told by the system**, not `%USERPROFILE%\Video`: that name
changes with the Windows language — on this machine the displayed name is
"Video" and the folder on disk is `Videos` — and it can be moved, because
Windows lets it be redirected and OneDrive does so by itself.
`SHGetKnownFolderPath` answers with the real one, redirections included, and
with `KF_FLAG_CREATE` it answers with one that exists. **Ask the system instead
of deducing.**

**The tray button is not touched, and that is the point.** The "Video folder"
entry opens `t.cfg.VideoDir`, which `cmd/pat-monitor` fills with the store's
directory: changing where clips live, the tray follows with not a line.
Recomposing the path there would give two folders for the same thing, and the
panel's would be the empty one.

**Whoever upgrades is told, and nobody moves their files.** Old clips stay where
they are and `/clips` no longer lists them: from outside that is
indistinguishable from lost clips, so at start-up one line declares how many
there are and where — once, and only if there really are any. Moving them
ourselves would be the kind choice and is not the right one: they are somebody's
files, the copy can fail half way, and a program that moves films between two
folders at night with nobody having asked is worse than a log line.

**The fallback is the old folder.** If the system does not answer, we go back
beside the configuration, which we know is writable. And there the line about
remaining clips does **not** come out: the old folder is the current one, and
announcing them as lost would be a lie about files that are being served.

### A clip asked for by hand is born kept, and the lock works both ways

The "Clip" button in the bar asks for the current clip without waiting for an
event. **It does not add a piece**: the pre-roll is already running —
`internal/record` keeps two whole GOPs in memory — so the command delivers the
seconds the ring holds and collects the next ones, exactly as a movement in the
room would. Same post-roll, same duration, same file.

**It is born kept**, that is, exempt from retention, and the reason is an
asymmetry: a clip of an event is written by the monitor on its own account and
there are dozens a night, so if nobody watches it it must be able to expire; a
clip asked for by hand exists **because** somebody wanted it. The direction is
this way because **freeing is always possible, getting an expired clip back is
not.**

**Hence the half that was missing.** The lock was one-way — `Keep` renamed
`clip-` to `keep-` and there was no way back — and that was enough while it was
applied by hand. With hand-made clips born kept, that single road becomes a
leak: every press would leave a few megabytes on disk that no rule touches
again, that is, the promise about the disk of whoever hosts us would stop
holding precisely for the kind of clip the user produces on command. There is
now `Release`, and on the recordings page the lock is **the bar's toggle**:
pill, `aria-pressed`, a label that does not move, and a tooltip saying what
pressing does. The "Kept" badge went with the one-way street — a badge is not
pressed.

Four decisions not visible from the code:

- **The prefix is decided by `Save`, not by a rename right after.** Between
  `Save` and a `Keep` sits the **pruning**, which runs at the end of `Save`:
  with a tight quota the just-requested clip would be the only candidate to
  disappear, that is, pressing it would give a file written and deleted in the
  same instant.
- **Pressing "Clip" during an event does not open a second clip: it gives it the
  lock.** The code stays the event's — what happened in the room is that — but
  the request to keep it is not lost. Between whoever asks for it and whoever
  does not mention it, whoever asks commands, and not the other way round.
- **`manual` is a code, and it goes through `UsableCode`.** It becomes part of
  the file name, and a malformed code produces a clip that listing, pruning and
  deletion **all skip**: an invisible file taking up space and counting in
  neither ceiling. The word the page shows lives in `clips.manual`, so the
  string is written in two languages, Go and JavaScript, and
  `TestThePageKnowsTheManualClipCode` holds them together.
- **There is one refusal, and it is told to whoever pressed.** In the first
  seconds after start-up, and after every capture restart, the ring is empty
  because the first keyframe has not arrived: `Trigger` answers `false`, the
  route answers 409 with the code `nothing-to-record`, and the page shows it for
  six seconds. A command that does nothing and does not say so is a knob that
  moves nothing.

**The "I am recording" state belongs to the room, not to the browser**, which is
why it arrives from `/api/status` like the three switches: the clip lasts ten
seconds, survives the page closing, and whoever opens a second phone with the
clip already running must see it. The successful response colours immediately —
waiting for the heartbeat would mean up to three seconds of a dark button on a
clip that is running — and what turns the pill off is the heartbeat, because the
clip ends by itself and nobody will press anything.

**Pressing again while recording extends the clip, it does not stop it.** That
is what the tooltip says, and the consequence is that the button is a toggle
with no off: the price is declared, and it is paid so as not to have a second
way of deciding how long a clip lasts.

**Verified live**, with the real monitor and the real webcam, driving a browser
with the DevTools protocol: click on the button, `recording=true` in the state,
`keep-20260903-235511-manual.mp4` on disk ten seconds later — 17.2 s of video
with audio, `kept=true` — pill off when the clip ends, and the release renaming
it to `clip-…-manual.mp4`. In the same session the `motion` and `bark` clips
were born prunable, which is the other half.

### The red that means something is in the glyph, not in the pill

`icons.css` says a glyph is `currentColor`, so it lights up with its pill
without a second rule to keep aligned. The exceptions are the recording dot and
the "Delete" bin. There the red **is** the meaning — the mark with which any
device declares it is recording, and the colour with which everywhere one says
"this does not come back" — and following the button's colour those two commands
would be indistinguishable from their neighbours.

The criterion is what counts: **`currentColor` is broken when red carries the
meaning, never for emphasis.**

**And the half one gets wrong is the other: the red does not go on the pill.**
"Delete" had a red border and red text, that is, it was the only different pill
of the three in a row where nothing is wrong — and a pill is the **resting**
state of a command that can perfectly well be pressed. On the glyph the same
shade says one thing and says it at the right moment; on the frame it declares
the row is an alarm. **What protects against pressing by mistake was never the
colour: it is the confirmation.**

**The two exceptions do not take the same red, and why is the whole criterion.**
The bin takes `--c-stop`, which is the brick of what is wrong **and of what does
not come back**. The recording dot takes `--c-rec`, and it is neither of those
things: recording is not a fault and is not irreversible, and dressing it in
brick would teach people to read as an alarm something that is perfectly fine —
the same error the VU meter avoids by not painting a loud cry brick. It is a
full red, declared in both palettes with its reason beside it, and absent from
`onboarding.css`, where there is nothing to record.

**The circle is ours and not the set's**: taking it from Tabler would mean an
attribution for a geometry anybody would draw the same.

### A row of the list is one area

- **The highlight sits on the row, not on the button.** `.clip-open:hover` lit
  the data alone and left the three commands dark, that is, showed two adjacent
  zones instead of one. The background is now on `.clip-row`, and the buttons
  keep their own, which is lighter: "this row, and inside it, this command".
  **And it no longer promises more than there is**: `.clip-open` is `flex: 1`,
  so the gap in the middle — 750 px out of 1100 — really is pressable and opens
  the clip. The case of both together is declared: `.clip-row.on` and
  `.clip-row:hover` are both 0,2,0, so without a rule for it the winner would be
  whichever is written last — either the clip that is playing loses its mark
  under the mouse, or the mouse does nothing on precisely the row being looked
  at.
- **Three parallel commands carried three different widths.** A hierarchy drawn
  by a spelling accident instead of by a decision. **Equal widths where things
  are parallel**, and measured: 93, 93, 93.
- **A title takes a title step.** "Recordings" sat on `--t-lead`, which in the
  scale is the opening line of a text — a body role, not a heading — and was the
  only `h1` in the product not using one: beside the 40 px pill it read as the
  label of the command to its left. It is now `--t-h2`. **Not `--t-h1`**: 28 px
  is the opening of a screen with nothing above it, while this is a heading as
  wide as a forty-pixel-tall command.

**One more command is one more label, and the single-row threshold is
re-measured.** The threshold is not where the buttons fit: it is **where the
middle group stops moving**. With `1fr auto 1fr` the two columns have a floor —
their content — and below it the left one, which has one button more, breaks out
of its track and pushes the centre: the offset appears **when "Talk" appears**,
that is, it is exactly the control that slides under the finger. **The number and
the measurements that bought it are written in `150-pages.md` and nowhere else**
— it stands at 1020 since Spanish, and this paragraph had stayed at 980 while the
other moved. Short labels bought the rest: **on a phone the five commands fit on
one row** — 106 px at 390, the same height with and without "Talk".

### A test that asks the system writes on the disk of whoever runs it

`clipStore` asked Windows for the videos folder **from inside**, and the tests
asked it too: `TestTheRetentionKeysReachTheStore` wrote **four megabytes of fake
clips into the developer's Video folder**, with the right names — that is,
things the monitor would then have listed among the recordings — and passed
green three times in a row. **It was found by looking at the disk**, not at the
code nor at the tests: there was nothing to read, because the test did exactly
what it had been asked to do.

The remedy has two halves. **The question to the system is asked by whoever
starts**, and the folder reaches `clipStore` as a parameter, `clipsDir` taking
the folder instead of asking for it: that way the choice can be tested in both
directions without a rigged machine. And **`insideTheTest`** requires the store
to be inside the test's temporary directory, verified to catch.

The rule beyond the case: **a function that interrogates the system cannot be
tested, it can only be run.** Whoever puts one in the middle of a constructor
makes it run for whoever wanted to test only the rest — and on the disk of
whoever runs the tests, which is a place a program must not write to by
accident.

