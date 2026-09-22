# PAT Monitor

A pet and baby monitor — PAT is *Pet And Toddler*. It captures a webcam and a
microphone, encodes with whatever hardware encoder the machine has, serves the
stream to a browser over WebRTC, and reaches the Internet without opening a port
on the router. One executable, no external dependencies, no service to install.
It runs on Windows.

## What this file is

**Every heading is a sentence that asserts something**, not a label. Read in
sequence, the table of contents **is the list of rules**, and that is what it is
for before it is navigation: the question "am I about to break something?" is
usually answered there, without opening anything.

Each entry is a constraint that a measurement put there. They are
counter-intuitive: changing one "for cleanliness" or "to be safe" puts the
defect back. Where a number appears, it was measured on real hardware, and the
chapter says on which — **the numbers are the part of this file that cannot be
re-derived by reading the code.**

**The index is here and the chapters are in `.claude/rules/`**, sliced by the
code they govern so that each one arrives when that code is opened instead of
all of them at every session. The document is ninety thousand words, and loading
it whole takes two thirds of a context window before anything has been typed —
which is not a matter of tidiness: **an instruction nobody can hold is an
instruction nobody follows.** Nothing is summarised to make it fit. The slices
are plain markdown, they read in order, and the index below says which chapter
is in which.

**Both lists are written by hand, so both are watched.** `internal/doc` fails if
a chapter is missing from the index, listed without being written, filed out of
order or at the wrong depth, written into two slices at once, or sitting in a
slice that declares no code — it had already lost one chapter, and an incomplete
index looks exactly like an index.

**What is not here is what has not been demonstrated.** The milestones are
closed and the plan has finished its cycle; what is left is measurements nobody
has taken, and they are **open issues on GitHub**, one each, with how the
measurement is done and what it costs not to know. Three of the five want
hardware or a place that is not here — a webcam that cannot reach 720p, a phone
on a mobile network far from the house, a second machine — which is why they are
in a tracker somebody else can reach rather than in a file only the author
edits.

The one chapter here that is a specification rather than a constraint is **"The
loop: what the monitor decides, and in what order"**: whoever touches bitrate,
resolution or cadence should read that page first, and the chapters after it
explain where each number came from.

## If you have just cloned this

**The headings are rules and the numbers are measurements**, and that is the one
thing to know before changing anything. A constant that looks wrong here is
usually one somebody has already tried the obvious value for; the chapter beside
it says on which machine it was measured and with which tool. **Re-measure
before changing**, and where the code and the prose disagree, the prose is not
what gets corrected: several claims in here outlived the code they described,
and every time the remedy was a new measurement rather than a better sentence.

**The rules for the code you are in arrive on their own**, because each slice
declares the packages it governs. If your tool does not read `.claude/rules/`,
or you are reading this on the web, open the slice whose name matches the area
you are in. Everything about building and running is two chapters below, and
**it is `.\build.ps1` and not `go build`**, for a reason written there.

**It runs on Windows, and that is where it was written and measured rather than
a decision about where it ends up.** The capture is Media Foundation and WASAPI
and most of the tests carry `//go:build windows`, so elsewhere the tree builds
almost nothing and proves less — which is a fact about the code there is, not a
position on portability. Whoever wants it somewhere else is not arguing with a
rule: they are looking at how much of the capture would have to be written
again, and at the fact that none of the numbers in here were taken there.

**What fails if you break it**, none of it style: the index guard above; the
README's configuration table, which must name every key; the catalogues, where a
code with no word in some language reaches the interface as the bare code; the
sweep that refuses a bare `go` statement, because a panic in an unguarded
goroutine ends the process with the camera on; and the licence tree, which is
generated from what is linked in and committed, so it goes stale without a word.
Each of them is a defect that happened here once.

Three lines are worth an **IMPORTANT**, and nothing else is, because emphasis
everywhere is emphasis nowhere:

- **IMPORTANT: no real tailnet name, no address under `100.64.0.0/10` and no
  auth key goes into this repository** — not once, not in a test, not in a
  pasted log. A tailnet name is publicly resolvable DNS tied to an account, and
  a correction leaves it in the history. The examples use
  `quercia-lieve.ts.net`, which does not exist.
- **IMPORTANT: do not add a dependency.** The answer is nearly always to ask the
  system instead, and it comes back structured rather than as text to parse: the
  videos folder, the local address, the preferred languages, the encoders the
  machine has. `internal/opuswasm` is the one artefact we could not produce
  ourselves, and its chapter says what that costs.
- **IMPORTANT: when you write a test for a defect you have just fixed, put the
  defect back and watch it fail.** Several tests here passed with the defect in
  place, and each is named in the chapter that records it — **a passing test and
  a test that asks nothing look identical.**

**The documents beside this one have their own readers.** `README.md` is for
whoever installs the program and says what it does rather than why; the open
issues are what has not been demonstrated; `baselines/` is evidence, so it is
re-measured and never rewritten — two of those files carry a verbatim Italian
transcript, because that is the output the build of that date produced.

## The headings are the rules

A minute of reading against ninety thousand words.

The chapters themselves are in `.claude/rules/`, and this is which is where.
The `paths` at the top of each slice are what actually decides when it arrives,
so they are the authority and this table is the map; to go the other way, from
a heading to its file, `grep -rn` the heading.

| slice | what it carries |
|---|---|
| `10-hardware.md` | what each encoder vendor was measured doing |
| `20-capture.md` | the microphone, the camera, panics, and the two lives that must not switch each other off |
| `30-codes.md` | codes across the API, the catalogues, the alert set |
| `40-detect.md` | motion, crying and barking as shapes |
| `50-stream.md` | the media clock and the SDP constraints |
| `60-loop.md` | the quality loop: bitrate, quantiser, resolution, cadence |
| `70-network.md` | routes, viewers, authentication, the Funnel |
| `80-encoder.md` | what Media Foundation does at the end of a session |
| `90-talkback.md` | talk-back, the audio output, libopus |
| `100-recordings.md` | the clips and the page that lists them |
| `110-recogniser.md` | CED, its front end, and where the thresholds came from |
| `120-releases.md` | the update check, the signing key, the licence tree |
| `130-instruments.md` | the measuring tools and the two logs |
| `140-windows.md` | the notification area, its panel, the executable's icon |
| `150-pages.md` | the palettes, the viewer, the guided path, the meters |
| `160-craft.md` | how to edit this codebase, how guards fail, the method |

- **What this file is**
- **If you have just cloned this**
- **Language**
- **The names**
- **Building and running**
- **Before predicting what the hardware will do, look here**
- **Invariants not to break**
  - Audio is captured in WASAPI raw mode
  - When raw is refused, exclusive mode remains
  - Audio and video must not be able to switch each other off
  - A panic is a fault of the part it happened in, and the camera does not
    go off for it
    - The runtime's last words do not pass through the log, and are made to
    - Nothing restarts the monitor, and Windows cannot be asked to
  - COM's `BOOL` is four bytes, Go's `bool` is one
  - "No default microphone" does not mean no microphone
  - From Remote Desktop the microphone does not exist, and the webcam does
  - The analysis stream is downsampled by averaging, not by dropping
  - The microphone path is re-examined, but only if it is the worst one
  - The microphone is chosen, and the box says what is being captured
  - The camera is chosen while the monitor watches, and choosing is reopening
  - A chosen camera that is gone does not stop the monitor
  - A permission Windows has taken away is a state, not a fault
  - A permission Windows is still asking about is not an absence
  - A microphone muted in Windows is a cause, and it was only in the log
  - The picture stopping is a fault, and `Ready` cannot say so
  - The machine must not fall asleep, and who is holding it awake is
    readable
  - A shutdown that does not finish keeps the camera
  - Codes cross the API, words stay at the edges
    - The tunnel phases are codes, and the pages hold the dictionary
    - The catalogue: English is the base, the others overlay it
      - German: the decisions, because nobody here can check the sentences
      - French: the decisions, and two guards that had been written by hand
      - Spanish: the decisions, and the bar's threshold moving
  - Alerts are the set of what is wrong now, not a list of events
  - Motion is measured on the merged frame, with the camera's breathing removed
  - Crying and barking are recognised by shape, and the floor is measured by the
    room
  - The media clock chases the real cadence, not the nominal one
  - SDP constraints, all discovered as "codec is not supported by remote"
- **The loop: what the monitor decides, and in what order**
  - The sensors
  - The levers, in order
  - The rules that always hold
  - The asymmetries
  - The two things chosen by measurement, not by brand
  - The two quality numbers, and why they are two
  - The cadence is declared, not assumed
  - Quality drops only if the network forces it
  - Of a command to the encoder, weigh the effect, not the answer
    - The probe exists because the evidence does not arrive on its own
    - The watchdog's signature is "does not follow", not "sits above"
  - Constant quality is done inside the encoder, not around it
  - The bitrate chases a quality, and it is the only loop
    - The discount only exists at full size, and the QP window is two GOPs
    - Four numbers that spoke of a moment already over
  - A room that moves revokes the discount, in one go
  - Capped CRF and the two-mode switch — removed, and what remains
  - The quantiser: the attribute where it exists, the stream where it does not
    - The reserve for the parameter sets is not where it looks
    - On Quick Sync the slice quantiser is a constant
    - The net underneath: a reading enters only after being seen to change
  - The quantiser floor, and the auto-tuning that was removed
  - The requested bitrate is not the produced bitrate, not even in CBR
    - The "seconds above the ceiling" column measures the GOP, not the encoder
    - The byte ceiling is judged over a window, not over a sample
      - And the estimate's credibility was still judged on a sample
  - Do not ask the camera for more pixels than it declares
  - The resolution is scaled by the Source Reader, not by the camera
  - To change size the encoder is rebuilt, and only the SPS says so
  - The quantiser is the only number that anticipates blocking
  - The resolution scale is commanded by the quantiser, not by the bitrate
    - The quantiser has a veto over the bandwidth descent
    - What arrived, the network carried
  - Below the last size, frames are removed, not more pixels
  - An asset with no route does not give a 404, and nobody says so
  - The STUN servers must be handed to the browser too
  - A relay is not shipped, and the reason is what it would carry
    - When no path is found, the log names the cause
  - From the Funnel, `RemoteAddr` is the ingress node, not the visitor
  - Signalling that drops must not switch off the media
  - An encoder is judged only if the request was the constraint
  - The port is taken before the camera
  - Whoever retries decides how often, not how much gets written
  - Access is slowed down, never blocked
  - Administrative commands are given from in front of the machine
  - The Funnel does not come up without a password
  - Outside access declares itself active only after passing through it
  - The Tailscale node version is stamped by the linker
  - Do not go below `tailscale.com v1.102.2`
  - When the process is ending, the encoder is not released
  - An asynchronous encoder's request is worth one time only
  - A wrong GUID does not complain
  - An `ICodecAPI` VARIANT's type is not deduced from its meaning
  - Verifying the keyframe
  - A code we have met is named, and the number stays first
- **Notifications with the page closed — written, tested and removed**
- **Talk-back is half duplex, and while somebody speaks the room is silent**
  - The place for the voice is laid at negotiation time
  - A box with several sources is not written, it is composed
  - On the way out the audio engine takes part, on the way in it does not
  - We run libopus ourselves, one module per codec
    - The `.wasm` is a blob, and it belongs to others
    - A pure-Go codec: measured, and not yet
  - A lock is not held across opening a device
  - An output that will not open is not requested fifty times a second
  - An HRESULT other than zero is not automatically a fault
  - Only what one has is written, never filler silence
  - A ceiling the next packet can get round is not a ceiling
  - "Written into the buffer" is not "it was heard"
- **Recordings are the watcher's files, not the application's data**
  - The folder is proved by writing in it, and the panel is told where it
    really is
  - A clip asked for by hand is born kept, and the lock works both ways
  - The red that means something is in the glyph, not in the pill
  - A row of the list is one area
  - A test that asks the system writes on the disk of whoever runs it
- **The sound recogniser runs here, and parity is demonstrated once**
  - The model file carries its front end with it, and must be believed
  - Layout defects do not complain
  - The signal's scale matters, because there are two floors
  - Above the training window there is a hole, and it is not patched
  - Thresholds are measured on other people's datasets, not on the developer's
    house
    - A short event inside a long window: the bark holds, the cry does not
    - The gate is tuned the other way round, and no longer decides
    - The model decides, and the chain has been walked by the air
- **A program in somebody else's house cannot be reached, so it asks**
  - The endpoint is the policy, and the belt underneath costs two comparisons
  - The key ships before anything reads it
- **A package is a second shape of the same binary, and it updates itself elsewhere**
- **Licences are collected from what is shipped, not from go.mod**
- **Dead code is found with a tool, not by eye**
- **An instrument is not believed until something else has checked it**
  - The CPU profile on Windows measures waits, not work
  - A tool that opens differently proves nothing
  - The log goes to a file, and it is not an accessory
    - Two instances write one file, and the line says which
  - The session log is the only instrument for the test that counts
- **In front of the machine there is an icon, and behind it a panel**
  - The notification-area icon is not an ornament: it is the only presence
  - The end of the session arrives at that window, and nowhere else
  - The executable's icon lives in the PE, and we write the resource
  - The build number is the fourth field, not the third digit
  - The tray menu: first what is happening, then what can be done
  - The tray panel: the pixels are ours, the controls are Windows's
    - Removing the non-client area does not remove whoever paints it
    - The first image is not drawn by WM_PAINT
    - `DefDlgProc` is not there, and its omissions give no error
    - A shape is composed once, and the centre is not sampled
    - One mark for one thing
    - What is shown is not what is used
    - A notice gets the rows it needs, and an answer sits under its question
    - Two folders are two glyphs, and the word stays on the control
    - How to look at a panel that closes when you look at it
- **The pages state what is not normal, and their drawings promise nothing**
  - Two palettes, and the viewer does not get lighter
  - The night page states, the onboarding explains
  - On the night page, "it works" is not news
    - The bar has one shape, and that is the property that counts
    - Full screen: the controls float on the picture
    - The Home Screen is the only way to lose the browser's bars on iOS
    - The glyphs are other people's, the semantics are ours
  - The configuration path, and the rules it must preserve
    - "You have not signed in yet" and "you are no longer in" are the same 401
    - The total is not promised before the fork
    - A step is a stretch that asks something of whoever takes it
    - "At home" is not localhost, and the interface is chosen by the system
    - Arrival is declared, not merely reached
    - The explainer is a box, not a step
    - The box promises, the row beside it explains
    - `hidden` hides nothing if a sheet writes a `display`
    - A class nobody looks up any more does not complain
    - Where the real thing is on the page, its drawing does not explain: it
      imitates
  - The QR code is engraved, not imported
  - Only the stars one can see pulse, and the price is per element
  - What only the server knows cannot be filled in by a route
  - Two roles with one name lie, and they do not collide
  - A drawing that follows the measurement is a second instrument in disguise
  - A high level and any level were the same colour
  - A meter that cannot measure does not draw silence
    - The line that explained it was a prediction, and the case was not rare
  - The codec warning was a prediction, and predictions err both ways
  - A property of the stream is not stated when there is no stream
- **Editing this codebase**
  - A doc comment on the wrong declaration does not complain
  - A text substitution is not a rename
  - A grouped selector is not a line
  - Two classic scripts on one page share one lexical scope
  - The same character is harmless in a comment and fatal in a string
- **Guards, and how they fail**
- **Method, as it turned out to be necessary**

## Language

**Code, comments, identifiers, the log, commit messages and reports are in
English.** The rule under it is that the language is chosen by whoever reads: a
comment is read by whoever opens that file, an identifier by people who may not
speak yours.

**The interface is the exception, and it is multilingual**: keys in the pages, a
catalogue per language, English as the fallback. The browser picks it and a
selector overrides the browser. `internal/i18n/catalogs/it.json` is a
translation of the interface, on a par with the others — it is a catalogue, not
a leftover.

**A report is evidence, and evidence is not rewritten.**
`baselines/capture-intel.txt` and `baselines/capture-amd.txt` carry a verbatim
Italian transcript, because that is the output the build of that date produced.
Rewriting one means writing lines the program never emitted. What is ours — the
header, where the verdict lives — is English, and the header says so. The
numbers below it do not depend on a language.

## The names

To whoever uses it the program is **PAT Monitor**, from *Pet And Toddler*: page
titles, `productName` in `internal/tray`, the binary `pat-monitor.exe`, data in
`%APPDATA%\PAT Monitor\` and recordings in `Video\PAT Monitor\` — **two folders
and the same name**, because application data is opened only by the program and
clips are opened by whoever lives there. Behind it: the Go module `patmonitor`,
`cmd/pat-monitor`, the tools `pat-diag`, `pat-capture`, `pat-viewer`,
`pat-opus`, `pat-wasapi`, `pat-sounds`, the two build generators `pat-icon`
and `pat-licenses`, and `pat-sign`, which holds the release signing key.

**The Tailscale hostname is not a name, it is an address**: it generates the
public URL, `patmon-1a2b3c.quercia-lieve.ts.net`. Changing it breaks the
bookmark on the watcher's phone and forces the TLS certificate to be reissued,
so it is a migration, not a tidy-up. It lives in `config.Default()` and in
`tunnel.New` — and **it does not reach anyone who already has a `config.yaml`**,
where the key is saved in full: changing the default and believing existing
installations have been renamed is the kind of knob that moves nothing.

**The default is `patmon-` plus six hex digits of a machine fingerprint**,
because a fixed name collides at the second installation and Tailscale renames
the node silently. The bare name remains as a fallback for a machine with no way
to distinguish itself. The two places that produce it call the same
`config.DefaultFunnelHostname()`: written twice it would give two public
addresses for one monitor.

**No real name goes into the repository.** A tailnet name is publicly resolvable
DNS tied to an account, and once committed it stays in the history after the
correction: the examples use `quercia-lieve.ts.net`, which does not exist. The
same holds for node addresses under `100.64.0.0/10` and for auth keys, which
must not appear even once.

**"baby monitor" in lower case is the category, not the product.** A rename goes
through whole identifiers, never through substring replacement.

## Building and running

Build with **`.\build.ps1`**, not `go build` by hand: the binary needs linker
stamps (see "The Tailscale node version is stamped by the linker").

**With no flags out comes what ships**: no console (`-H=windowsgui`), stripped,
the notification-area icon as its only presence, and the diagnostic tools not
rebuilt at all. **`.\build.ps1 -Console`** is the form for measuring: the
monitor with its console plus every tool — which ones is `build.ps1`'s business,
and they are not counted here, because a sentence that keeps a count is a second
list and diverges.

Go is **not on the PATH** of the tool shells; prepend it every time:

```powershell
$env:Path = "C:\Program Files\Go\bin;$env:Path"
$env:CGO_ENABLED = '0'          # one static binary, no runtime to install
.\build.ps1
go vet ./... ; go test ./...
```

**No external dependencies.** Video from Media Foundation, audio from WASAPI
with an embedded Opus encoder, outside access from Tailscale. If a third-party
binary ever seems necessary, the question is whether the thing can be asked of
the system instead — it almost always can, and the answer comes back structured
rather than as diagnostic text to be parsed.

**The binary that ships is stripped, the development one is not.** `-s -w` takes
49.1 MB to 36.4, 26% less, and costs nothing at runtime because those sections
**are not loaded at all**: measured, 36 ms of start-up against 37. Of the final
36.4, **six are the model weights** and ~3.6 the wazero compiler: what we write
is the smallest part. `-trimpath` gains no size but removes the build machine's
absolute paths.

**Panic traces stay intact**, and that must be re-verified whenever anyone
touches those flags: the runtime builds them from its `pclntab`, in `.rdata`,
not from DWARF.

**A stripped binary loses the `delve` attach, and only that: stacks are still
available.** `dlv attach` answers "could not find goroutine array", and the
obvious conclusion — "stripping has to go" — is wrong: pprof reads the
`pclntab`, and on the same binary `/debug/pprof/goroutine?debug=2` answers with
92 complete goroutines, names, files and lines. **The question that matters in
front of a hung process — "what is it waiting for?" — is answered fine by a
stripped binary**, and better than by a debugger, because `dlv attach` **kills
the process when it exits**. Hence the procedure: **`-pprof localhost:6060` and
a goroutine dump, before touching anything**; Delve only to inspect variables,
rebuilding with `-Console`.

**No UPX.** It would take the binary to ~12 MB but decompresses at start-up —
paying exactly the cost stripping avoids and keeping the whole image resident —
and UPX-packed binaries are a classic malware signature flagged by Defender and
SmartScreen: 18 MB traded for a red screen.

The capture reference measurements are in `baselines/capture-intel.txt` and
`baselines/capture-amd.txt`, and they answer "has it got worse?", which without
a number taken when it was fine has no answer. Redo them when the pipeline
changes, with three runs, because between identical runs there is about ±30%.
**They are two files and not one, and they are not compared with each other**:
different scene, light and encoder, so each machine is compared with itself.

