---
paths:
  - "internal/audio/**"
  - "internal/audiocodec/**"
  - "internal/wincom/**"
  - "internal/resample/**"
  - "internal/pipeline/**"
  - "internal/devices/**"
  - "internal/guard/**"
  - "cmd/pat-monitor/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## Invariants not to break

### Audio is captured in WASAPI raw mode

On many OEM machines a noise-suppression Audio Processing Object zeroes the
signal before applications see it — on the development laptop, Elevoc explicitly
filters a child crying. The bypass is `SetClientProperties` with
`AUDCLNT_STREAMOPTIONS_RAW` **before** `Initialize`. Measured gain: 64 dB.

The fallback without raw is not an equivalent alternative: it is the filter back
in the way. So it is retried three times a quarter of a second apart, with a new
client each attempt — the refusal can be transient when the microphone is
reopened right after being closed. And the reason for the refusal **is kept and
reported**: the most dangerous degradation this program can suffer must not go
unexplained.

### When raw is refused, exclusive mode remains

`AUDCLNT_E_RAW_MODE_UNSUPPORTED` (0x88890027) is not the end of the story. **In
exclusive mode the Windows audio engine does not take part at all**, so no APO
does either: the same signal as raw, by another road. Measured where raw is
refused and the shared path delivers exact zeros 100% of the time: in exclusive,
RMS -70 dBFS and 5.8% zeros, that is, the room.

It is not free — while the monitor runs the microphone is its — which is why it
is a fallback and not the first choice. But the alternative is a mute baby
monitor.

**Whether raw is granted is a state of the endpoint, and it moves under Windows
Update.** The same laptop gave `modo=raw` in `baselines/capture-intel.txt` and
refused it later with the **same** Intel driver and the **same** Elevoc APO
build: what changed in between is that the endpoint and the four APO components
were re-installed by a Windows update, which their `LastArrivalDate` shows and
nothing else does. And the condition written above did not hold either — raw has
been refused while the shared path measured RMS -38.7 dBFS with a floor of 380
LSB, that is, no zeroing at all. So neither the machine nor the zeroing is the
premise: **the premise is the refusal, whenever it comes**, and a comment saying
"on this machine raw is refused" ages into a false sentence without anybody
touching a line.

**The road is therefore announced even when it worked**, and the line that says
so was missing for as long as the fallback existed. Raw refused plus exclusive
granted means `RawMode` is true and nothing is wrong, so the warning below fires
for neither — and its `reason`, which carries the HRESULT, is the only place the
refusal is written down. The log said `mode=exclusive` and never why; finding
that it was `AUDCLNT_E_RAW_MODE_UNSUPPORTED` took a throwaway program to read
`Stream.RawError`, on a machine that was sitting right there. It is `Info` and
not `Warn` — the signal is clean — and what it names is the price: the
microphone is nobody else's while the monitor runs. It follows the same "only if
it changed" guard as the open, because the road is a **state** and the recheck
would write it every two minutes all night.

Three traps, all paid for:

- **The format is dictated by the device, not by the engine.** The mix format is
  the engine's result and in exclusive mode is refused with
  `AUDCLNT_E_UNSUPPORTED_FORMAT`. The good one is in
  `PKEY_AudioEngine_DeviceFormat`, a BLOB inside a PROPVARIANT to be read by
  hand because go-ole does not expose BLOBs.
- **The device format is integer PCM, the mix format is float.** Reading one as
  the other produces denormals, that is, "non-zero" samples at -760 dBFS: they
  look like signal if you count zeros and like silence if you look at amplitude.
  **A signal is judged by amplitude, never by the count of non-zero samples.**
- **In exclusive mode there is no volume either.** Measured with `pat-wasapi`:
  between raw and exclusive there are **-30.3 dB**, and the endpoint declares it
  applies **+30.0** — two independent roads that agree, which is why the
  declaration can be trusted instead of tuning by ear. Slider and mute are
  applied by the audio engine, which here does not take part: without reapplying
  them the audio arrives clean and six times quieter, enough to make a level
  meter look stuck. They are read back from `IAudioEndpointVolume` downstream,
  so the user's slider is followed instead of inventing a constant; mute is
  respected, and is a silence to be declared.

The fault is silent: green page, still meter, zero signal.

### Audio and video must not be able to switch each other off

It happens: on a laptop with the lid closed the microphone array disappears
while the camera stays. A baby monitor that shows nothing because it cannot hear
is worse than a mute one.

The converse holds too: **a camera that will not open must not switch off the
audio.** On a machine whose webcam offered no usable format the monitor was
blind *and* mute, because the audio lived inside the video's restart loop and
stayed closed for the whole backoff, up to half a minute. Measured before: 39
packets in 25 seconds; after: 100 every 2 seconds, indifferent to restarts.

The two lifetimes are therefore separate in `Run`, not in `runOnce`: each has
its own loop, and neither touches the other's context. The audio reports the
fault **once** instead of at every attempt, and the page declares `microphone:
absent`, because silence must not be left to the watcher to interpret.

### A panic is a fault of the part it happened in, and the camera does not go off for it

The chapter above is an invariant of our code, and **one floor below it there
was nothing holding it up**: a panic in any goroutine ends the process, and this
program has nineteen of them. A bug in the recogniser, in the recordings, in the
talk-back or in the notification-area icon switched the camera off — at night,
with nobody there to start it again. That is the separate lives of audio and
video broken by the runtime, where a rule written in Go cannot reach.

**No machinery for restarting was invented, and that is the whole design.** The
monitor already knows how to come back from a fault: `Pipeline.Run` restarts the
video with a backoff and a healthy-run reset, `superviseAudio` restarts the
microphone, and both write the line saying what happened. What they did not know
is that a panic **is** a fault. `guard.Run` turns it into an error, and
machinery that has been running for months does the rest.

Measured live, with `-simulate-panic`, on a binary with no console:

| | |
|---|---|
| panic on the 100th video frame | `capture interrupted, restarting  ran_for=4.951s` |
| camera open again | **1.4 s** later, `capture started` |
| panic on the 100th audio packet | `audio interrupted, retrying` |
| audio back | **1.15 s** later, and the camera never noticed |
| panic in an accessory | one line, and the capture started **after** it |

**Which parts the monitor can outlive is written at the call site**, as `aside`
in `main`: an errgroup takes everything down together as soon as one member
answers with an error, which is right for the pieces the monitor *is* and wrong
for the ones beside it. Written inside each of them it would be a property one
has to go and check; written in the list, whatever is **not** wrapped is
something the monitor cannot do without — the pipeline, the HTTP server.

**A recover catches its own goroutine and nobody else's**, and that is the
property that decides whether any of this protects anything. Guarding a
supervisor covers the call chain under it and **not** the goroutines that chain
starts: those are separate stacks, and a panic in one of them ends the process
exactly as before. A monitor with its boundaries guarded and fifteen bare `go`
statements left over is protected in the cases somebody thought of and not in
the others, which is worse than no protection, because it reads as complete.
So the rule is absolute and mechanical — **in the monitor, a goroutine is
started with `guard.Go`** — and it is checked rather than remembered, by a test
that reads the syntax tree of every non-test file under `internal` and
`cmd/pat-monitor`. It found eighteen the first time it ran. The instruments in
`cmd/pat-*` are outside it deliberately: they are run by hand in front of a
console, where a panic is the answer one wants, printed where one is looking.

**A goroutine somebody is waiting on must not die quietly**, and there is one:
`wincom.Thread` sends its outcome on a channel that the caller reads with **no
timeout**. Swallowing a panic there would turn a crash into a monitor hung with
the camera on — and a hang is the harder of the two to diagnose, because there
is nothing to read. So the catching is done inside, and the error goes into the
send like any other. It is not a hypothesis: the audio capture runs on a COM
thread, so the live test's panic came out as `audio capture: panic in a COM
thread`, the supervisor retried, and the microphone was back a second later.

**The innermost boundary wins, and that is right.** That same panic never
reached the `the audio capture` guard one floor up: whoever is closest knows
best what to do with it, and here what wincom did was hand it to the supervisor
in the shape the supervisor already understands.

**A subsystem declares its own fault in its own vocabulary rather than letting
the panic out.** `tunnel.Run` answers `nil` on every one of its failure paths —
outside access is half of what was asked for and the monitor at home is the
other half — so a panic there is declared the same way, with `StepPanic`: left
to propagate it would switch off the camera because the tunnel broke; swallowed
silently it would leave the badge and the icon saying "active" about something
that is no longer running. The quality loop does the mirror image: on a panic
the encoder is **put back at the ceiling**, which is the answer this program
gives everywhere to "I do not know", because that loop is the only thing that
lowers the bitrate and its last command can have been the floor of 300 kbit/s,
asked for a room that was still an hour ago.

**The trade is real and is declared**, because it is the argument against all of
this: recovering hides bugs, and a program that swallows its own panics runs on
in a state nobody designed. Three things hold it down. The stack is **always
written**, by whoever catches and not by whoever receives the error, so a caller
that decides to carry on cannot make the diagnosis disappear. Nothing is caught
in the middle of the work: the catch sits at the top of a subsystem, where the
state below it is about to be thrown away and rebuilt anyway. And what cannot be
recovered from at all — a concurrent map write, a stack overflow — is caught by
nobody and never will be, which is what the chapter below is for.

**The site is put in front of the stack, and the stack is unreadable.** A text
handler quotes a value containing newlines, so a stack arrives as one escaped
run of three thousand characters: first in the line it buries the three things
that answer the question — which subsystem, what happened, where — under a wall
nobody reads at seven in the morning. `guard.site` pulls out the frame under the
runtime's own and **refuses rather than guesses**: a shape it does not recognise
gives nothing, because a plausible wrong frame sends the reader to the wrong
function and the whole stack is on the same line for exactly that case.

**And `AllSteps` was a hand-written list, which is how this was found.** Adding
`StepPanic` to the tunnel should have made the catalogue's check ask for a word
in both languages; it asked for nothing, because the list the check reads is
written by hand and the new code never reached it. The whole suite stayed green
over a failure that would have come out on the page as a bare key. It is the
first entry of "Guards, and how they fail" — *a hand-written list protects
exactly what somebody remembered* — met by walking into it, and there is now a
test that derives the list from the source instead.

**Wrapping a `go` statement moves when its arguments are evaluated**, and that
is the one way this sweep can change behaviour rather than only protect it. A
`go f(x, y)` evaluates `x` and `y` **at once** and runs `f` later; turned into
`guard.Go(log, "…", func() { f(x, y) })` both are read whenever that goroutine
gets round to it. Fifteen of the sixteen sites carried locals or constants and
were unmoved; the sixteenth read `r.Header.Get("User-Agent")` from an HTTP
request inside an observer built with `WithoutCancel` **precisely so that it
outlives the handler**, that is, a request that is no longer ours to read. It
is hoisted to a local now. Whoever wraps the next one looks at the arguments
before the body.

**Only a panic is filed as a panic**, and `guard.ErrPanic` is what makes that
possible: `guard.Run` hands back whatever the function returned as well, so a
caller that answers a panic differently — the tunnel files one under a step
whose sentence reaches the page and the notification area — would put "an
unexpected fault inside the monitor" over an ordinary error the day that
function grows a real `return err`. Today those functions answer nil, which is
exactly when a confusion costs nothing to remove.

**A goroutine is started in three ways, and the guard reads all three.** The
`go` statement is the obvious one; `time.AfterFunc` runs its function in a
goroutine of its own, where the chain that armed the timer is long gone and can
catch nothing (`guard.After`); and a member of the errgroup in `main` is a
goroutine whose panic `g.Wait` never sees. The third is checked by a different
rule, because there the interesting thing is not that it is caught but **which
of the two decisions was taken**: `aside(…)` says the monitor can outlive this
part, and a body that catches for itself says it cannot. It found two that
carried neither — the orderly shutdown, which had no catch at all, and remote
access, whose protection was real and lived a package away, so the list said
nothing about it.

**And that was a review's finding about the guard rather than about the code.**
The timer's body is two lines that cannot panic; what was wrong is that the
test asserted the rule as *absolute and mechanical* while reading one construct
out of three. **A guard whose green covers more than it read is worse than a
missing guard**, because the next person reads the claim and not the code — the
same shape as the list of known words this file already refuses elsewhere.

**A library's callback is a fourth, and a security audit found it where it
mattered.** Pion calls what is registered with `pc.OnTrack` and its siblings on
goroutines of its own, which no `guard.Go` started: the talk-back's receive loop
ran there, fed by a viewer's packets through the decoder and the resampler, and
a panic in it would have ended the process with the camera on — the accessory
this chapter names by name. The bodies are ours, so each catches for itself with
`guard.Run`, and `TestEveryCallbackAPeerConnectionRunsIsGuarded` reads them; it
named all three with the wrapping taken out. What the chapter below says about
a panic in a library's own code is unchanged: that one is still nobody's to
catch.

**`-simulate-panic` is the instrument**, and it exists for the same reason
`-simulate-fault` does: a fault does not happen on command, and checking what
the monitor does about one on the night it happens is not checking. Its four
places are the four different answers — `video` restarts the capture, `audio`
reopens the microphone, `accessory` writes a line and changes nothing else, and
`now` is caught by nobody, which is the only way to exercise the chapter below.

#### The runtime's last words do not pass through the log, and are made to

A panic's trace is written by the runtime **straight to file descriptor 2**. It
does not go through `slog`, through `Fanout`, or through anything else of ours,
and with `-H=windowsgui` the monitor has no standard error at all. So the one
event this log was invented for — a process that dies in the night — was the one
event it could not record, which is exactly the episode the chapter above
records as already having happened once.

Measured with a throwaway probe built both ways:

| | `GetStdHandle(STD_ERROR)` | writing to `os.Stderr` |
|---|---|---|
| launched from a shell | `0x228` | works |
| launched detached, as the shell's icon does | **`0x0`** | `The handle is invalid` |

and in **both** cases, after pointing that handle at a file, the trace of a
panic raised in another goroutine was written into the file, appended after the
lines already there.

The second row is the monitor as it really runs, and the first is a console
somebody is watching. Hence the rule: **we stand in for a standard error that is
not there, and never take away one that is** — whoever runs
`pat-monitor 2>somewhere` has asked for the traces to go there and gets them
there. What the redirection does **not** cost is the console, and that was not
obvious: `os.Stderr` is built once at start-up from the handle of that moment,
so it goes on writing where it always did. The log's two destinations stay two.

**The handle is given up before the file behind it is closed**, at every
rotation and on a write that fails. A closed handle's value is handed out again
to the next thing this process opens, and a trace written there would end up
inside somebody else's bytes — with nothing to read anywhere, which is the shape
of fault all of this exists to remove. The first test of that ordering **passed
with the defect put back**, because two recorded names say nothing about a third
event neither of them names: it now asks the file whether it is still open.

**And the log is deliberately never closed.** This was measured and not
reasoned: with `defer logFile.Close()` in `main`, a process that panicked left
`crash traces=this file` in the log and then **no trace at all**. A deferred
close runs while the panic is unwinding, that is, an instant before the runtime
writes — the destination was taken away by the very line that had announced it.
Nothing is lost by not closing, because this writer buffers nothing and the
handle goes back to the system when the process ends; what it buys is that the
log is the **last** thing alive, which is what a witness has to be. **No unit
test can see this**, and none does: it took `-simulate-panic now` on a binary
with no console, which is why that flag has a value that nothing catches.

**A protection that lapses says so in the file.** The re-take after a rotation
used to discard its error, so a failure left the standard error pointing
nowhere while the flag went on claiming otherwise — and the start-up line,
`crash traces: this file`, would have asserted it for the rest of the night.
`CaptureCrashes` respected that same error and declined, so the two paths
disagreed about the same fact. It now gives up the flag and writes one line
into the log itself, which is the only destination this object is sure of: for
news about the log, there is nowhere else to put it.

**Whether it is in force is said in the log**, on the line that already names
the file. Reading it after a night that ended with the camera off, "there is no
trace" and "traces do not come to this file" are the same silence, and only that
word separates them.

#### Nothing restarts the monitor, and Windows cannot be asked to

The chapter above makes a panic survivable; **it does not make the process
survivable**. What is left — a runtime throw that no recover can catch, a panic
in a goroutine started by a library rather than by us, memory exhausted — still
ends it, and then the camera is off until somebody notices in the morning. The
obvious answer is to have Windows put it back, and the obvious answer does not
work.

`RegisterApplicationRestart` is the right shape for this program: it writes no
registry key, creates no scheduled task, installs no service and spawns no
child — none of the things that **are** persistence mechanisms, which is what an
antivirus heuristic looks for. It is Windows restarting us rather than us
resurrecting ourselves, and for a binary that opens a camera, a microphone and a
network tunnel **without a signature**, that distinction is the whole budget.
Every alternative spends it.

**It was measured, and it never fires.** Four ways of dying, each in its own
process registered for restart, on a machine where Windows Error Reporting is
enabled and `WerSvc` is running:

| how it died | event in the log | restarted |
|---|---|---|
| an ordinary panic | none | no |
| a panic with `debug.SetTraceback("crash")` | none | no |
| a concurrent map write, that is, a runtime throw | none | no |
| a real Win32 exception raised past the runtime | none | no |

**The reason is in the fourth row, and the console says it outright.** Run with
a console so that its output can be read, that process prints:

	Exception 0xe0000042 0x0 0x0 0x7ffc0caa3cfa
	PC=0x7ffc0caa3cfa
	runtime.cgocall(...)
	...
	exit status 2

The Go runtime installs a handler of its own that catches what nothing else
handled, prints its diagnostics, and ends the process with `exit(2)`. **An exit
code is not a crash**: Windows Error Reporting never engages, nothing is written
to the Application log, and the registration waits for an event that cannot
happen. It is the same shape as an accepted `ICodecAPI` property that is never
applied — the call answers `S_OK`, and nothing of what it promised occurs.

**And the roads that would work are the ones we will not take.** A watchdog
process, a scheduled task and a `Run` key are persistence mechanisms, that is,
exactly what makes an unsigned binary with these permissions look like what it
is not. A Windows **service** would work — the service manager restarts on a
non-zero exit, which `exit(2)` is — and it is excluded by something already
measured here rather than by taste: **audio endpoints are per session**, which
is why a monitor started inside a Remote Desktop session is deaf for its whole
life, and a service in session 0 is on the wrong side of the same boundary. It
would also have no notification area, which is this program's only presence.

So the monitor is not restarted, and that is a decision with a measurement under
it rather than an omission. What is left in its place is the diagnosis: after
the chapter above, a death that used to leave a switched-off webcam and nothing
else now leaves the trace that caused it, in the log, where whoever comes back
in the morning is already looking.

**And the reason this is written down is that it is invisible from the code.**
`RegisterApplicationRestart` returns `S_OK`, so an implementation of it would
look finished, would be reviewed as finished, and would be discovered to do
nothing on the one night it was needed. Whoever wants to try again should start
by making a Go program die in a way Windows recognises, and the console output
above is where that has to begin.

### COM's `BOOL` is four bytes, Go's `bool` is one

Passing the address of a `bool` to a COM method that writes a `BOOL` writes
**four** bytes where there is one, and the next three are whatever the compiler
put beside it.

`vol.GetMute(&info.Muted)` zeroed the two following struct fields — precisely
the ones saying whether the volume control existed — and the result was a
correctly interrogated endpoint declaring itself to have no controls: the
symptom looks exactly like an unsupported interface, and the right value was
there, erased by the next line. Pass an `int32` and convert, as `comBool` does.

**And the same trust in a C structure, one layer out: a blob is believed about
its own length.** `PKEY_AudioEngine_DeviceFormat` hands back a `WAVEFORMATEX`,
eighteen packed bytes that **declare how many more follow**, and the copy taken
of it was checked against eighteen and no further. `decodeFormat` then reads the
`SubFormat` GUID at offset 24, which needs forty, and what authorises that read
is `cbSize` — a number inside the blob. Eighteen bytes declaring twenty-two more
therefore sent the read twenty-two bytes past the end of a Go slice, into
whatever the allocator had put there; and the same truncated pointer goes to
`IsFormatSupported`, where it is WASAPI doing the reading.

Nobody crafts this one — it comes from the audio driver — and that is the
argument **for** the check rather than against it: it is a structure from
outside the program whose length is asserted by its own contents, and this
program runs on machines whose drivers nobody here has seen. It is the exclusive
path that reaches it, which is the path this laptop takes every time, raw mode
being refused. **The check takes the bytes and not the device**, so both
directions can be tested: a function that interrogates the operating system can
only be run, never tested, and it would run on the disk of whoever runs the
tests.

### "No default microphone" does not mean no microphone

Windows assigns the default role, and that role can be empty: it is enough that
the default was a Bluetooth headset and that it left. Measured: `Microphone
Array` active, `GetDefaultAudioEndpoint` answering "element not found", and the
monitor declaring itself deaf with a working microphone under its nose.

So it falls back to the first active capture endpoint. The choice is arbitrary
and rightly so — the alternative is hearing nothing — and whoever wants to
decide sets `mic_device_id`.

### From Remote Desktop the microphone does not exist, and the webcam does

Windows audio endpoints are **per session**. In an RDP session the physical
devices are not exposed: in their place are the "Remote Audio" endpoints,
microphone redirection is off by default, and when enabled it exposes the
microphone of **whoever connected**. Video capture does not follow that road, so
the webcam keeps working: that asymmetry is what makes the fault baffling —
perfect camera, non-existent microphone, hardware in order. Measured,
`Win32_SoundDevice` lists the three Intel Smart Sound devices normally — that is
the enumeration of the **hardware**, not of the session's endpoints — while
`EnumAudioEndpoints(eCapture, DEVICE_STATE_ACTIVE)` returns nothing.

**The consequence matters more than the cause: a monitor started inside the RDP
session stays deaf for its whole life**, even after disconnecting, because the
process does not change session. It must be launched from the console — and that
is one of the reasons **auto-start with Windows is not done**: a shortcut
launched from an arbitrary session would produce this fault silently, every
night.

**The output is missing too**: inside RDP `IAudioClient` on the render endpoint
answers `E_INVALIDARG` (0x80070057), so the monitor cannot hear **and** cannot
speak while the camera carries on.

It is detected with `GetSystemMetrics(SM_REMOTESESSION)`, which describes the
session of the **process asking** — the right question, since that is what
decides which endpoints exist. The error message says so instead of leaving it
to be deduced: blaming a missing microphone sends someone to look for a hardware
fault while the hardware is fine.

### The analysis stream is downsampled by averaging, not by dropping

The 16 kHz for the detector is obtained by averaging three consecutive samples.
Taking one in three would alias exactly the high frequencies a sound classifier
cares about.

For the same reason **the main stream is never resampled**: a resampler written
in a hurry shifts the pitch of sounds with no visible clue, and a cry played a
tone higher has no way of being noticed.

The rate is dictated by the microphone, provided it is one of the **five** Opus
compresses natively — 8, 12, 16, 24, 48 kHz. The list lives in
`audiocodec.SupportedRates` and **nowhere else**. 44.1 kHz is **not** there, and
it is the likeliest of the excluded rates: a microphone arriving at that rate is
refused, saying so at open time, not converted behind the scenes.

**The analysis stream always comes out at 16 kHz.** Averaging is exact, has no
filter and nothing to tune, so it is the first road wherever it exists — from 24
kHz it does not, and there `internal/resample` takes over, verified with a tone
above Nyquist, which is the only case in which a broken resampler differs from a
correct one. What that bought is one case fewer, and not just any case: at 24
kHz the recogniser could not run at all, that is, a machine on which the monitor
watched less than the others with nothing saying so. Covered by
`analysis_test.go`, which checks the plan for every rate we accept **and** that
the two roads give the same level where both are possible.

### The microphone path is re-examined, but only if it is the worst one

Falling back to the shared path can be caused by another application holding the
microphone — a video call, a voice assistant — and those end: a meeting lasts an
hour, not a night. Without a re-examination the monitor would stay on the
processed path until morning, which on the development laptop means mute, for a
call that ended at nine.

It retries every two minutes and **only while already on the worse path**: when
the good road is open there is no timer and the audio is never interrupted.
**And there are two fallbacks**: raw mode, and capture that ended up on a
microphone nobody asked for — the second was never re-examined, so on a machine
that does get raw, plugging the USB stick back in left it on the lid microphone
until reboot. `micRecheckWanted` says it in five lines, and its table is also
the explanation: the default role is not a fallback whatever it opens, and the
test tone has nothing to re-examine.

The reopen does not go through the error path — announcing an interruption every
two minutes would turn a precaution into an alarm — and the "microphone opened"
line comes out only when the mode **changes**: a log that repeats the same thing
all night hides what happened once.

**But the precaution rang anyway**, because the re-examination is also armed in
exclusive mode, that is, every two minutes all night on that machine:

- **`AudioActive` answered no** for the duration of the reopen, and that no
  feeds `MicrophoneActive` and the `mic-missing` alert. The state is read every
  second: with ~720 reopens a night, a banner, a chime and two transitions in
  the log are not a rare case, they are a certainty. **An alarm that rings every
  night for no reason stops being read**, and this is the only alarm the program
  has. `micRecheckGrace` is a two-second window in which a closed microphone is
  not an absent microphone, and it **expires by itself** instead of being
  switched off by someone — an open that wedges inside the driver cannot hold it
  on forever.
- **`raw mode not obtained` and `audio active` were rewritten every time**,
  ~1440 identical lines a night: inside the grace they are `Debug`.
- **The line announcing the real interruption did not come out at all.** The
  flag was cleared by two writers, `runAudio` and the supervisor, and
  `runAudio`'s came first: the supervisor's `CompareAndSwap(true, false)` always
  found `false`, so "audio interrupted, retrying" was **dead code**. Now the
  supervisor clears it, being the only one that needs to know whether the audio
  was there, and `TestOnlyTheSupervisorClearsTheAudioFlag` refuses a second
  writer: the defect was not a value but **the order of two writes**.

### The microphone is chosen, and the box says what is being captured

The viewer's details carry a box listing the machine's microphones, and choosing
one changes where the monitor listens **without restarting anything**. The real
case is mundane: the laptop has an array in its lid, the cot is across the room,
and the USB stick plugged in for the purpose is the second endpoint of a list
nobody could ever see.

**The choice is applied by reopening, and there is no second road.**
`SetMicDevice` writes the id and closes the capture; the supervisor reopens it,
that is, the same code as the periodic re-examination, grace included. Inventing
a second way to open a microphone would mean two opens that diverge, and the new
one would be the less tested.

**The two closures we decide must be distinguished in the log.** `errMicRecheck`
and `errMicSwitch` end in the same branch and remain two errors: whoever reads
has to find **why** the audio was interrupted, and "path re-examination" in
place of "someone changed it" sends them looking for a fault where there was a
command. For the same reason a choice **is announced** while the re-examination
stays quiet: it is the only moment at which "this microphone is filtered" is
useful, because another one can still be chosen.

**The box carries the choice, and a line beside it says what is being
captured**: the two diverge in the case that matters. If the chosen microphone
is unplugged, WASAPI falls back to the default instead of refusing to open — by
refusing, the monitor would be mute all night with a working microphone under
its nose — but the fallback **is declared** in two places, a log line and the
line "capturing *name*". Hence the two separate state fields, `microphoneId` and
`microphoneChosen`: one field would have lied exactly there — and the one
carrying the real name is `Microphone()`, the endpoint the capture opened.
`paintMic` set the box from `microphoneChosen` without reading `microphoneId`,
so the two fields left the server separate and were rejoined into a lie on
screen. **The defect was not a wrong value: it was a missing reader**, and no
test on the value catches that —
`TestThePageReadsWhatIsCapturingAndNotOnlyWhatWasChosen` checks that somebody
reads the field.

**The list does not travel with the state.** Enumerating costs a COM thread and
a round of property stores, and the state is requested by the heartbeat every
three seconds from every open page. It is requested when the details open, and
at **every** open, because a stick gets plugged in while the monitor runs; the
declared price is that a microphone attached with the box already open shows up
by reopening the details.

Four decisions that are not visible from the code:

- **The empty id is a valid choice** and means "follow the role Windows calls
  default". Without it, after pinning a device there would be no way back — and
  it is also why "Windows default" is a **list entry**: it is the line
  `mic_device_id: ""`, and which device that is is said by the hint.
- **An id that is not among the active ones is refused by the route.** Passing
  it on, the capture would fall back — right for a device that unplugs, wrong
  for a choice just made — and the file would say something that does not
  happen. Whoever cannot enumerate does not ask the question and trusts: better
  an unverified choice than an inert box.
- **It is saved before being applied**, as with the tunnel: a choice applied and
  not persisted would come back by itself at the next start with nothing saying
  so.
- **It reopens even when the id is the same as before**, but **that is not the
  way to say "retry now"**: from a `<select>` that gesture does not exist,
  because an already-selected entry emits no `change`. Retrying is the monitor's
  re-examination, which asks nobody to remember to press something at three in
  the morning.

**A choice that arrives while the open is in progress is not lost.** The command
comes from another goroutine and its close can precede the pointer that makes it
reachable: reading the id once, that choice would stay written and applied by
nobody. At the end of the open the wanted value is read again, and if it changed
the device is closed again.

**Five defects were one family: a command repeated more than anyone asked for.**
The fallback declared itself at every reopen, ~720 lines a night, and now has
the "only if it changed" guard. Arrow keys on a closed `<select>` emit one
`change` per press, and each one here means rewriting `config.yaml` and
reopening the capture: scrolling the list from the keyboard interrupted the
audio once per key, so the choice is sent after half a second of quiet — **the
cadence of an operation is not dictated by whoever wants it**. Closing the
capture was not enough to make it reopen, because if the open is failing the
supervisor sleeps up to thirty seconds: `micWake` is the other half of the
command. The guard against repainting was on **focus**, which after a mouse
choice stays on the control, while the right question is whether the **menu is
open**: `:open` commands where it exists, and where it does not the class laid
down on `mousedown` **expires** after fifteen seconds — much longer than an open
menu and much shorter than it takes a stale box to become a lie. And the command
read the box when the half second expired, not at the moment of choice: inside
that window the heartbeat can repaint and `paintMic` puts back the **previous**
choice. The id is taken now and travels with the timer.

**And a choice that no longer exists is shown, not hidden.** Setting the box to
the default, the watcher would see a state that is not in the file and cannot
even really be chosen, while on the device's return the capture would go back to
it with nobody having asked: there is an entry for it, "microphone chosen · not
connected".

**At rest it took the font, weight and colour of the value it replaced, and
the arrow said it could be pressed — and that stopped holding the day the rows
became lines.** With the name of a reading on the left and its value flush
right, a `<select>` cannot join the column, and **the reason is that the two
engines size it differently**. Measured in Chrome, the box follows the
**chosen** entry — picking the long microphone takes it from 158 px to 348 —
so there it is always snug. On the phone it follows the **longest**, and that is
the only thing separating the two boxes of the same panel: "Prima webcam
disponibile" is the longest entry of its own list, so it looked snug there too,
while "Predefinito di Windows" sat against a device name 2.7 times its width and
left the gap that was reported, in those words, as a value centred in its
field. (The half about the phone is an inference from the photograph: there is
no WebKit on this machine to ask. What is measured is Chrome and the widths of
the two lists.) `text-align: right` cures it in
Chrome and **not** on the phone, whose engine does not honour it inside the
control, and the half that cannot be verified from this machine is the half the
monitor is watched on.

So the name stays where the control puts it, the control has the frame it never
had — a hairline, a radius, a ground — and **its width is ours, not the
control's**. The frame alone was not enough, and it was reported again in one
line: one long and one short, no improvement. It could not be otherwise while
the width came from the control, because what the control measures is the list,
and the two lists are different. A width the page decides is a number the
engines do not get a vote on: the two fields start and end at the same place as
each other whatever is chosen and whatever the list holds, and choosing the long
microphone no longer moves anything.

**It was three fifths of the row, and it is now what the row has left**, which
is the same change and the same reason as the readings beside it: the name
column is fixed, and everything after it belongs to the value. A share was the
right answer while the readings ended at the right edge, because the box had to
end there too; with the readings starting at a fixed offset the box starts
there as well, so the two boxes go on matching each other by construction. What
must go with it is `min-width: 0`, or a long entry sizes the control and pushes
it onto a line of its own.

**A field drawn as a field costs no agreement between engines**, and it says the
true thing the panel wanted to say anyway — two of those rows are pressed and
twelve are read. The typography is still the value's, and the arrow is still the
same glyph as "Details". Five things that design required:

- **The menu is drawn by the page, and it stays a real `<select>`.**
  `appearance: base-select` is the only road that removes the blue without
  removing the control: with a normal `<select>` the entries accept a background
  but **the highlight of the chosen row does not** — it stays the Windows
  accent, unreachable from any sheet — and dressing only the entries gives a
  menu half ours and half theirs, stranger than one left whole to the browser. A
  list drawn by us would cost keyboard and screen reader. Where the base
  appearance does not exist, the declaration is invalid and the system menu
  remains.
- **Two marks for two different things, and order is not enough.** Teal says
  "this is the chosen one", light grey "this is under the finger"; written in
  the obvious order, the chosen entry under the finger lost its teal. The case
  of both together is declared.
- **The `<button>` with `<selectedcontent>` inside must be written in the
  markup**, otherwise there is no box for `text-overflow` to act on and the name
  leaves the control, passing under the arrow. They are empty, so where that
  specification does not exist the parser ignores them; and the page replaces
  the **options**, not the children — `replaceChildren` would carry the button
  away on the first state round.
- **The target is large and the row does not grow**: vertical padding plus an
  equal and opposite margin, pressed on 30 px and laid out on 18. The wrapper
  wants `display: flow-root`, otherwise that margin **collapses** with its own
  and the focus ring ends up over the row's label; and that is where the ring
  sits, on the wrapper and not on the control.
- **The menu width is our decision**, and must be declared: `max-content` up to
  420 px with the name **wrapping** beyond that ceiling. Without it an anchored
  menu shrinks to whatever fits between the box and the window edge — the same
  page gave 420 px in the first column and **158 in the fifth**, that is, four
  lines for a name, and with Windows's eighty-eight-character names it carried
  on for half a page. In the box it is cut with an ellipsis, because there is a
  grid row to respect and whoever opens the list opens it to read. **Shortening
  the names was the wrong road**: it removed information from everyone for a
  defect that was elsewhere.

The font is 13 px like the rest, and below 16 iOS zooms the page by itself on
the first focus: that is the price of consistency with the grid, and it is a
**choice**.

Verified from a real browser driven over the DevTools protocol on the box's four
branches. **What is worth the effort is the first open with nothing open and
nothing in the configuration**: the box settles on the device Windows gives the
role to, and if even that is missing on the first of the list — an empty box
would say nothing to anyone. Where enumeration fails only the entry for what is
being captured remains and it cannot be pressed: offering a choice the capture
ignores would be a knob that moves nothing.

**Done, on a machine with two: a USB stick and the laptop's array.** The USB
one was chosen, and unplugging it while the monitor watched, the capture does
not learn it from an enumeration — the WASAPI loop fails, `GetNextPacketSize`
answering `AUDCLNT_E_DEVICE_INVALIDATED` (0x88890004) — and the supervisor
reopens on `Microphone Array` **1.2 seconds** later, in raw mode. **Both
declarations are there**: the log's `the chosen microphone is not available:
opened another one`, carrying the chosen id beside the opened one, and on the
page the box's entry `Chosen microphone · not connected` **selected** and still
holding the chosen id, with `capturing Microphone Array …` beside it in the
`notice` tone and not the fault one — read from a real browser driven over the
DevTools protocol, which is the only thing that says the two fields were not
reunited into a lie on the screen.

**And the alert rang for one second, which is the side of the grace that had
never been travelled.** `mic-missing` appeared at +0.8 s and cleared at +1.8 s,
because this was not a planned reopen: the microphone really had gone, and
`micRecheckGrace` is written to silence a precaution and not a fact. The two
halves of that distinction are now both measured, the quiet one by a night of
reopens and the loud one here.

**The return is not immediate, and two minutes is the figure.** The examination
is armed by the fallback and not only by a worse mode — raw was obtained
throughout — so the chosen microphone comes back at the **next** examination
and not on plugging in: measured, the stick back in Windows's list at 14:52:49
and the capture on it at 14:53:03, announced at `INFO` because the device
changed and **without** the fallback warning. The examination that fell while it
was still out wrote the `Debug` line alone, which is that guard doing its job:
two minutes of a still log instead of an interruption announced every two.

**What this run does not separate is which of the two fallback roads answered.**
With two endpoints and the chosen one gone, the default Windows reassigns and
the first active one are the same device, so `GetDefaultAudioEndpoint` and
`firstActiveCapture` predict identical results — and the chosen one was itself
the default. Telling them apart wants a third microphone, and nothing depends on
knowing.

### The camera is chosen while the monitor watches, and choosing is reopening

`SetCamera` writes the wish and closes the capture; the supervisor that already
restarts video reopens it on the new camera. **There is no second road**, which
is the microphone's rule and the same argument: a second way of opening a camera
would be two opens that diverge, and the new one would be the less tested of the
two.

The pieces are the microphone's, one for one, and each of them was paid for
there:

**And the flag that says so is given back before anything that can fail.** It
is read by `runOnce` after `runVideo` returns, and the supervisor's branch for a
closure we decided on **does not wait at all** — no fault written, no backoff,
straight round again. That is right for a camera somebody chose and wrong for a
session that never started: cleared thirty lines into `runVideo`, after
`wincom.Init` and `mf.Startup`, a failure in either of those left it set, so
every turn reported a switch nobody had asked for and came back at once. A
failure that repeats — Media Foundation refusing to start — is then a loop at
full speed writing `camera changed on request, reopening` as fast as the machine
allows, where what was wanted is one line and a wait doubling to thirty seconds.

**The microphone's twin already had it right**, which is what makes this legible
as an oversight rather than a decision: `runAudio` clears `micReopen` on its
sixth line, with nothing above it that can return. Clearing it early loses
nothing, because the **wish** lives in `camWanted` and is not touched: what this
flag carries is only whether the closure just past was ours, and about a session
that never started the honest answer is no.

- **The wish is not the configuration.** `camWanted` is read at every open;
  `Config.CameraLink` only says where the capture starts from. Rereading the
  file at every open would mean reopening the camera the user has just
  discarded.
- **Closing the capture is not enough to make it reopen.** With the open failing
  — no camera at all, one held by another process — the supervisor waits up to
  thirty seconds and there is no capture there to cancel: the choice would sit
  written and without effect for half a minute, while the page has already taken
  it as done. `camWake` is the other half of the command.
- **The close we decided on must not look like a fault.** It comes back as
  `errCamSwitch`, and the supervisor reopens at once, with no backoff and with a
  line of its own — `camera changed on request` — because this is the one
  closure of the video capture that is a command, and an interruption with no
  cause sends whoever reads the log looking for a fault where there was a
  choice.
- **The decision is taken in `runOnce` and not at `runVideo`'s exits**, of which
  there are several and two of which come back as a plain `nil`: a decision
  taken at each exit is one that an exit will end up not taking.
- **A choice arriving while the open is in progress is not lost.** The command
  comes from another goroutine and its close can precede the pointer that makes
  it reachable, so the wish is read again at the end of the open.
- **And a choice is announced even when nothing changed**, which is exactly the
  case an "only if it changed" guard silences: a camera chosen and not connected
  falls back on the one already open, so the picture is identical and the
  command went nowhere. That is the one moment when saying so is any use,
  because another one can still be chosen. Hence `camChosen` beside
  `camReopen`, as the microphone has two flags for two questions.
- **There is no grace here, and that is a measurement rather than a difference
  of principle.** What a closed microphone makes false is `AudioActive`, read
  every second, so a two-second reopen produced ~720 false alarms a night; what
  a closed camera makes false is `capture-stopped`, which asks for half a minute
  of no frames. A reopen lasting a second cannot reach it.
- **The chosen camera coming back is looked for, and that half was missing.**
  Media Foundation announces nothing, so without a look the monitor would show
  the wrong room until the next restart — and the `camera-other` alert would go
  on asserting that the chosen camera is not available **after somebody plugged
  it back in.** That is worse than the microphone's version of the same gap: an
  alarm that has become false and rings all night, in a program that has one
  alarm. `camRecheckInterval` is the microphone's two minutes, and the look
  happens **only while we are on another camera** — the same "only if it is the
  worse path", which is what makes the cadence free in the normal case. The
  reopen happens only when the chosen camera is really there, because the check
  is cheap and the cure costs a second and a third of picture.
- **And that makes two closures we decide on, so they stay two errors.**
  `errCamSwitch` and `errCamRecheck` end in the same branch of the supervisor
  and do not share a log line: "somebody chose" and "the camera came back" send
  whoever reads to two different places, which is the rule the microphone's pair
  already carries. The recheck deliberately does **not** set `camChosen`: that
  flag exists to make the open announce a choice that went nowhere, and nobody
  chose here.
- **A step deposited for the previous camera is thrown away.** `wantFormat` is a
  request the governor leaves for the frame loop, and it outlives the session
  meant to consume it. That was harmless while every restart came back to the
  same starting size — every step of the old scale is a step of the identical
  new one — and it stopped being harmless the moment the camera can change:
  going from a small webcam to a large one, a pending step of the small scale is
  smaller than anything the rebuilt scale knows, `applyFormat`'s cap does not
  catch it because the cap only looks **upwards**, and `resync` invents no size
  that is not a step. What is left is a governor that believes it is at full
  size while a shrunken picture goes out. It is the `sent` window's defect one
  field across, and the same remedy: **a window, or a request, is an assertion
  about a stretch, and it holds only while the stretch is the same.**

**Measured on real hardware**, driving the pipeline with the real webcam and
`SetCamera` called three times: **757 ms** from the command to the close, the
new picture at **1.1 s**, and 110 frames delivered in a five-second window
against 150 without a swap — that is, **a swap costs about a second and a third
of picture** and nothing else. Choosing a camera that is not connected produced
the fallback line **with the picture unchanged**, which is the `camChosen` half
above and the only way to see it; choosing the real one back cleared the
fallback.

`/api/cameras` and `/api/camera` are the microphone's routes twice over —
deliberately the same shape, the same order, the same refusals — with one
asymmetry: **an empty choice means "the first usable one"**, which is a rule of
ours and not a role Windows keeps, so the entry that carries it is marked by
`devices.Pick` itself rather than by the page assuming the list's order.

**And that asymmetry cannot be tidied away, which is why it is measured here.**
Reading the two boxes side by side the obvious question is why one says
"Windows default" and the other "first available webcam", and the obvious
correction — one wording for both — would put on screen a sentence claiming a
thing the system does not do. Asked of Windows rather than deduced:

	Windows.Media.Devices.MediaDevice, static methods
	  GetDefaultAudioCaptureId     <- the role exists
	  GetDefaultAudioRenderId      <- and for output too
	  GetAudioCaptureSelector
	  GetAudioRenderSelector
	  GetVideoCaptureSelector      <- how to enumerate them, and nothing else

**There is no `GetDefaultVideoCaptureId`, and there is nothing for it to
return**: Windows keeps device roles for audio only. Media Foundation's video
enumeration says the same thing from the other side — on this machine each
device carries **six** attributes, of which the ones we can name are the
friendly name, the symbolic link and the source type, plus a category and a
hardware flag; there is no role among them and nowhere for one to be.

**And the two subsystems disagree again, on the name.** Asked in the same
instant, through the same two routes, with two Brio 105 and an ACER connected:

	/api/microphones   Microphone Array (AMD Audio Device)
	                   Microphone (Brio 105)
	                   Microphone (2- Brio 105)
	/api/cameras       ACER HD User Facing
	                   Brio 105
	                   Brio 105

**Windows disambiguates two identical audio endpoints itself, with a `2- `
prefix, and Media Foundation does not.** It is not a defect of ours and it is
inherited in silence: the microphone's box can never show one word twice, and the
camera's does it the first time somebody buys a spare of the same webcam — which
is the ordinary way to end up with two, and the case `camera_device_id` exists
for. From the page the watcher cannot tell which is which, and the state does not
close the gap afterwards either: it publishes the name of what is capturing, and
that name is `Brio 105` whichever of the two was opened. **The remedy is a decision and not a fix**: numbering
the entries would be the obvious road and is the wrong one, because the
enumeration's order is documented to mean nothing, so the number a watcher
learned yesterday can belong to the other camera today — a label that changes
identity is worse than one that does not distinguish. What is stable is the
link's own instance field, measured above to survive even a change of socket.

The consequence is a real difference and not a difference of wording: **the
microphone's empty choice delegates to something the user chose in Windows,
while the camera's delegates to a rule of ours.** So the microphone's default
**can be empty** — measured, `GetDefaultAudioEndpoint` answering "element not
found" with a working microphone array — a state the camera's rule cannot have,
because it answers whenever there is any camera at all. And in the other
direction, ours is the weaker of the two: the enumeration's order is not
documented as meaning anything, so on a machine with two webcams "the first
available" is not a promise about **which**. What that costs is covered by the
box existing: whoever cares pins one, and the tooltip names the one it is right
now.

**And choosing from the box clears `camera_name`.** That key is read only when
the id is empty, so a choice of "the first usable one" made from the page would
be overruled at the next start by a name written years ago: a command that moves
nothing until a restart, and then moves something nobody asked for.

**The link that key migrates to becomes the choice, in memory.** Handed to the
capture alone it pins a camera the state says nobody chose: the box says "first
available webcam" while the capture is pinned, and on unplugging that camera the
alert says "the camera you chose is not available" beside a box showing no
choice. It is the codes chapter's question again — at every change of shape, not
who writes it but **who reads it** — and a chosen camera has two readers, the
capture and the server. It is **not** written to the file at start-up, because
rewriting somebody's configuration is their decision: the migration completes by
itself the first time a camera is chosen from the box.

**The box in the details is the microphone's box, not a second one.** One
`makePicker` builds both, and that is a decision rather than tidiness: the
microphone's cost half a dozen defects — the half-second debounce, the arrow
that follows a state a `<select>` does not publish, the guard on the open menu
with its expiring class, the id taken at the moment of choice and not when the
timer fires — and every one of them applies unchanged to a camera. Copied, the
second box would have started from the first one's fixes and then drifted from
its next one, which on this project is not a hypothesis: it is what `--t-h1` did
in two stylesheets.

What is **not** shared is what really differs, and it is three things: which
fields of the state the box reads, the line beside it, and the words for the two
entries that are not devices.

- **The microphone derives its fallback and the camera is handed it.** WASAPI
  falls back on its own and the capture can only report the endpoint it got, so
  two ids that differ mean the chosen one was absent; for the camera the chosen
  link comes out of a file and the open one out of the enumeration, and the same
  comparison would declare another camera about the right one. See the chapter
  below.
- **There is no "nobody is capturing" for the camera.** A camera that delivers
  nothing is `capture-stopped` — an alert at the top of the page and a picture
  that has visibly stopped — while a microphone that is not there is silence,
  which looks exactly like a quiet room. The line says the thing that cannot be
  seen by looking, and for the camera that is only "this is another room".
- **The tone belongs to the message, not to the box.** It used to be the class
  of the element, that is one colour per box, and the camera's arrival made that
  false: "the microphone is missing" is a fault and "another one is being used"
  is a thing to know — the alerts' own two levels — so the identical sentence
  would have come out brick beside one box and amber beside the other.
  `.pick-state.fault` and `.pick-state.notice`, in the banner's own colours.
- **The camera's entries carry no `Default` field, and the microphone's is the
  reason to look twice at that.** It is serialised, it is documented, and **no
  page reads it**: the entry for the rule is drawn by the page from its own
  catalogue, and its tooltip names the device in use — which, when that entry is
  the selected one, is the device the rule opens, by construction. The camera's
  was written with a sentence claiming it kept the page from assuming the list's
  order, that is, a claim about a field with no consumer: **the field cannot
  break and the comment cannot fail**, which is the pair already paid for by the
  dead SDP field — and `deadcode` does not see it, because it is JSON and not
  code.

**And generalising switched two guards off, silently.** The ids moved out of
`el('...')` into a configuration object, and the panel's source out of
`showWarning('...')`: both guards read those literal forms and nothing else, so
they went on passing while covering nothing. **A generalisation that moves a
value out of the shape a guard reads switches that guard off, greenly** — the
remedy is that what a guard reads stays at the call site and what reaches the
factory is the result: `sel: el('s-cam')` and `warn: (t) => showWarning(
'cam-choice', t)`. The second of the two had no guard at all until then, and a
source missing from `warningOrder` is not an error: `showWarning` files the text
and the panel never picks it, so the message simply never appears.

**Verified from a real browser** driven over the DevTools protocol, with a
`camera_device_id` naming a webcam that does not exist:

| | |
|---|---|
| the box's entries | first available · `Webcam scelta · non collegata` · `Integrated Camera` |
| the line beside it | `sta riprendendo Integrated Camera`, `pick-state notice`, `rgb(224,163,64)` |
| choosing the real camera | chosen → `camera changed on request` → opened; entry gone, line hidden |
| choosing "the first available" | reopened again, and `id=""` in the file |
| a link nobody has | 409 `no-such-camera` |
| the microphone's box after it | still chooses, tooltip still names the default |
| the video through three swaps | **513 frames, 2 dropped, never paused** |

The last row is the one worth keeping: three encoder rebuilds and three camera
opens inside one session, and the browser went on decoding — which is what the
announced level being the preset's buys, and the reason that piece had to be
done first.

### A chosen camera that is gone does not stop the monitor

`camera_device_id` names one webcam, and until the day it could be read it named
none: the key was there, the identifier `pat-diag` printed was cut short, and
the branch "the chosen camera is not connected" was code nobody could reach.
With the link readable it takes unplugging a USB webcam to reach it — and what
it did was **refuse to start**, that is, a baby monitor that does not switch on
with a working camera and a working microphone inside the machine.

**The precedent is the microphone and the conclusion is the same**: the first
usable one is opened. The argument is stronger here, because there is no second
road at all — WASAPI opens the default when the chosen endpoint goes, Media
Foundation opens nothing, so a refusal is the whole product stopped by a cable.

**What video risks and audio does not is the wrong room.** The fallback may be
looking at the kitchen while the chosen camera watched the cot, and at three in
the morning, on a phone, a dark room looks like a dark room. So the fallback is
**declared, and as an alert** — not as the line in the details the microphone's
gets, because that line is read by whoever goes looking, and this is a thing
that is wrong now.

- **It is a `Notice` and not a `Fault`**, and the boundary is the level's own
  definition: there is a live picture, so the monitor is doing its job and there
  is something to know about it. A fault would also turn the tray icon brick,
  that is, teach whoever walks past the machine to read a working monitor as a
  broken one. It sits beside `remote-down`, which is the same shape — half of
  what was asked for is missing and the rest works.
- **It is in the tray as well, because the remedy is physical.** Whoever watches
  from a phone can be told there is another room on screen and can do nothing
  about it; whoever is in front of the machine is standing beside the socket the
  webcam goes into.
- **The fallback is published, not recomputed.** The obvious form — chosen link
  different from open link — is wrong in a way that does not show: Windows gives
  the same symbolic link back in different cases depending on who is asked, so
  it would declare another camera about the right one. `devices.Pick` matches
  the two once and case-insensitively, and the capture publishes the fact.
- **And publishing it was not enough, because the box compares links of its
  own.** To decide whether to draw the "chosen · not available" entry it asks
  whether the chosen link is in the list, and that comparison respected case:
  with a link pasted from `pat-diag` in another spelling, the box drew "not
  connected" over the camera that was on screen, and the value assigned to the
  control matched no entry at all so it fell back on "the first available one".
  **A comparison the deciding layer makes one way must not be made another way
  by the layer that draws it** — the page matches without case too, and takes
  the control's value from the entry rather than from the file. Verified live
  with the real link upper-cased in the configuration: two entries, no fallback
  line, and the pinned camera selected.
- **And the link handed to Media Foundation is the enumerated one**, which is
  the same trap from the other side: `findDevice` compares byte for byte, so a
  link written in another case would be "camera not found" on a camera that is
  sitting there.
- **The question is asked at every open**, which is what makes the answer follow
  the camera. It is the move already made for the size, and the defect is the
  same if it is not made: an answer taken at start-up describes the webcam that
  was plugged in then.
- **The line is written when it changes, not at every open.** A camera that will
  not open at all is retried every thirty seconds, and a state rewritten every
  thirty seconds is a log that hides what happened once.

  **That held for this line and for no other, and the package found it.** With
  the only webcam disabled in Device Manager, each retry wrote three: the list
  having nothing usable, the formats not readable, and `capture interrupted,
  restarting`. At the thirty-second cap that is ~360 lines an hour for as long
  as the monitor runs, in a log of four rotating files, so on a machine with no
  camera the lines that matter are pushed out in a couple of weeks. The refused
  camera did the same with two. It was measured on the MSIX because the
  certification's tester may have no webcam, which is the case nobody runs here.

  The audio supervisor already had the rule, `audio still unavailable` at
  Debug, and the video now has it too: `retryLevel` writes a failure at Error the
  first time and at Debug when it repeats, and starts again after an attempt in
  which the camera came open, since then the monitor worked and what failed
  afterwards is a new episode. The two warnings of the open are written before
  the attempt's outcome is known, so they are quietened by the attempt before:
  when it failed without the camera opening, the open logs through `demoted`,
  which writes at Debug and so is dropped at the normal level and kept by
  `-verbose`.

  **A review found the two holes in that, and both were about the attempt that
  is not a repeat.** A choice of camera closes the capture through the branch
  that reopens at once, and that branch skipped the bookkeeping, so a failure
  remembered from before the choice quietened the open of the camera just
  picked: a command is news, and the branch now clears both. And the attempt
  that finally opens was quietened too, because the open's warnings are written
  before anyone knows it will succeed — so a camera back whose formats cannot be
  read would be asked for the preset with nothing saying why. `demoted` holds
  what it lowers, and `replay` writes it again at its own level the moment the
  camera comes open; when the open fails, what was held was a repeat and is
  dropped.
- **The name in the details is the one that is open**, and it used to be the
  device `main` chose, frozen for the life of the process: with a fallback it
  would have named the camera that is not there while showing the picture of the
  one that is. It is the microphone's rule, and the encoder's — the governor is
  not the authority on what is going out.
- **Start-up no longer refuses either.** `main` used to end the process when
  there was no usable webcam at all, which breaks the invariant on separate
  lifetimes one step before the place it is written: no camera meant no audio
  either. It warns, and the capture retries with its backoff, so a webcam
  plugged in a minute later works.
- **A superseded `camera_name` that matches nothing does not become a link.** It
  would be a choice nothing can ever satisfy — a fallback at every open and an
  alert all night — for a key that was only ever a name.

**Verified live** with a `camera_device_id` matching no device: the fallback
line 466 ms after start-up, `camera opened NV12 1280x720@30`, capture and WebRTC
tracks up as usual, and the `camera-other` alert at **thirty seconds exactly**,
which is the start-up grace and not a coincidence.

**And then with two real cameras and a hand on the cable**, which is the half a
missing id cannot show: a Logitech Brio 105 chosen from the box while an ACER
HD User Facing sat beside it, and the Brio pulled out of its socket.

| | |
|---|---|
| chosen from the box | `camera changed on request, reopening`, `camera = Brio 105` |
| unplugged | `ReadSample: HRESULT 0xC00D3EA2`, then the fallback line naming both |
| the picture back | on the ACER, **two seconds** after the cable |
| plugged back in | found by the recheck at **2m1.7s**, which is its two minutes |
| the picture back again | on the Brio, one second later |
| the alert | `camera-other` raised on the fallback and **cleared by itself** |

**The cable took the microphone with it, and that was not planned.** The Brio
carries one and it was the endpoint in use, so a single physical event hit both
halves of the capture at once — which is the invariant on separate lives put to
the test by the thing that really breaks a baby monitor. They recovered
independently and neither waited for the other: the video fell back to the other
camera in two seconds, and the audio reopened on `Microphone Array` in raw mode
with `mic-missing` raised and **cleared one second later**. That one second is
the loud side of `micRecheckGrace`, and this is the second device it has been
seen on.

**And then with two of the same model, which is the case the link exists for.**
Two Logitech Brio 105 on one machine: same friendly name, same VID and PID, and
Media Foundation hands back two links differing in one field, the USB instance —
`7&1f2dbae&0&0000` against `7&23f38cb&0&0000`. A `camera_name` could not have
chosen between them **in principle**, which is the half a pair of different
models could only assert.

Each was pinned from the box in turn: reopened in **286 ms** and **209 ms**, with
the fallback flag staying false both times and the whole link written to the
file. **No software measurement separates them after that** — same name, same
`1280x720@30`, same cadence — so the test is physical and its two outcomes are
exclusive: with the second one chosen, unplugging it must produce a fallback, and
unplugging its twin must produce nothing at all. The chosen one went out.

| | |
|---|---|
| the microphone notices | `AUDCLNT_E_DEVICE_INVALIDATED`, at the cable |
| `mic-missing` | +295 ms |
| `the chosen camera is not connected: opened another one` | **+470 ms** |
| `camera-other` | +971 ms |
| the microphone reopened, in raw, on the twin | +1.2 s |
| `mic-missing` cleared by itself | +1.98 s |
| plugged back, found by the recheck | **2m1.674s** |
| reopened on it, `camera-other` cleared | +1.4 s after that |

Two of those numbers are worth more than the rest. The recheck's **2m1.674s**
against the other pair's 2m1.7s is the same interval measured twice on two
different devices, that is, a constant of ours rather than a property of a
webcam. And **the fallback went to the ACER, not to the twin**: `Pick` takes the
first of the enumeration, and the twin being the same model buys it nothing —
which is right, because the enumeration's order promises nothing, and it is worth
knowing that an identical spare does not inherit the chosen one's place.

**The cable was put back in a different socket, and the link did not change.**
Measured, byte for byte: `7&23f38cb&0&0000` before and after, while Windows's own
`DEVPKEY_Device_LocationInfo` moved from `…0004.002…` to `…0003.001.004…`. So on
this device a pinned camera survives being moved, which is the property a watcher
would assume and nobody had checked. **It does not generalise from one device**:
that instance field is a serial number where the device has one and a hub path
where it has not, and only the second kind would move.

**One half of that open question is still not travelled.** The swap has to survive
a **change of size**, and these three cameras cannot force one: the two Brios
reach 1920x1080 and the ACER 1280x720, so all three open at the preset's 720p and
the rebuilt resolution scale and reset quantiser window stay unexercised. It
wants a webcam that cannot do 720p — the 640x480 kind this file already names.

**And the test found a defect in the box rather than in the choice**, which is
recorded where the two boxes are argued against each other: "The camera is chosen
while the monitor watches, and choosing is reopening", under what Windows names
and what it does not.

### A permission Windows has taken away is a state, not a fault

Camera and microphone are **consented to** on Windows 11, and the consent can be
withdrawn while the monitor runs: from Settings, by whoever administers the
machine, or by an update putting *"let desktop apps access your camera"* back to
off. There is a switch per device and a second one for desktop programs, so a
machine can grant the camera and refuse the microphone — a monitor showing a
room it cannot hear.

**The state existed and nothing in here could name it.** `grep` over
`internal/mf`, `internal/audio` and `internal/devices` found no `0x80070005`
anywhere: a revoked camera arrived as *the capture is not starting*, retried
every thirty seconds for as long as the monitor ran, and reached the page as
`capture-stopped` — *No images from the camera*, which is **true, says nothing
about why, and sends whoever reads it to look at a cable.** The remedy is two
clicks and nothing could say so.

**It is a defect now and not only under packaging**, which is the part worth
writing down: Windows 11 has the desktop-app switch on every installation, so
this is reachable today, on an unpackaged binary, by one toggle in Settings.
What MSIX changes is the frequency — there camera and microphone are
**capabilities**, declared in the manifest and revocable per package from the
same page — not whether the case exists.

The shape is the one this file uses everywhere else, and each piece is at the
layer that can answer for it:

- **The number lives once, in `internal/wincom`.** Video capture meets the
  refusal coming out of `IMFActivate::ActivateObject` and audio capture coming
  out of `IMMDevice::Activate` — two calls into two unrelated subsystems
  answering the same `E_ACCESSDENIED` — and a constant spelled at both sites is
  a constant that can be wrong at one of them. `wincom.Denied` answers for the
  HRESULT as go-ole hands it over **and** for the wrapped sentinel, because
  between the call and the loop that decides sit two or three `fmt.Errorf` and
  nothing enforces that every one of them uses `%w`.
- **The two HRESULT funnels wrap and do not diagnose.** `mf.check` and
  `describeAudclnt` attach `wincom.ErrDenied` and keep the hexadecimal first, by
  their own rule; what the refusal *means for a baby monitor* is said by
  `internal/pipeline`, which is the one layer that knows there is a microphone
  in the same refusal. It is `mfName`'s own argument about `E_FAIL` — **a
  generic code cannot be given a meaning by the place one of its instances was
  met** — and the reason the note is not written at the call site in
  `internal/audio` is narrower and worth knowing: the guard that refuses a COM
  error wrapped without `describeAudclnt` reads `%w` wraps and bare returns, so
  a branch that calls a helper instead is a branch it judges **not at all**.
  Putting the sentence there would have walked through a hole rather than found
  one.
- **The flag follows the evidence and holds no history.** `camDenied` and
  `micDenied` are stored from the error the last attempt ended with — stored
  even when it is false — and cleared **at the open** and not at the end of a
  session: a latch would leave the banner on the phone all night over a
  permission granted back in a second.
- **The line comes out when the state changes, not on every retry.** A refused
  camera is retried every thirty seconds for as long as the monitor runs, and a
  sentence repeated all night is a log in which nothing that happened once can
  be found. It is the rule the camera's own open and the microphone's fallback
  already follow.
- **The refusal is announced instead of its consequence.** With the camera off
  there are no frames, so `capture-stopped` is true as well, and the page has
  one banner: `camera-denied` takes its place, exactly as `mic-missing` already
  takes `mic-silent`'s. Both are `Fault` and not `Notice`, unlike
  `camera-other`: there is no picture at all, and that it is somebody's decision
  rather than a breakage does not change what the room gets.

**And the remedy is in the notification area, because that is the only place it
exists.** Whoever watches from a phone can be told the camera permission is off
and can do nothing whatever about it; the switch is in this machine's Settings.
`tray.settingsPage` answers with the address — `ms-settings:privacy-webcam`
or `ms-settings:privacy-microphone` — and the word is not there, because there
is one word for both.

**The first version of that panel was wrong in three ways at once, and all
three were about the panel and not about the permission.** They are worth
listing, because the shape they share is *a piece added to a composition
designed as a whole*:

- **The command carried the device's name**, `Microphone permission`, sitting
  directly under a line that already says *microphone: permission is off* — the
  same word twice on two adjacent rows. The line names the device and the
  command says what pressing does, which is the division the recordings page's
  lock already makes; so there is one entry, and it is shorter.
- **It wore the filled pill.** The selected command already wears the accent,
  and the focus falls on the first command, which is this one: a pill on top of
  that is **two marks for one thing**, which is the rule the panel states in as
  many words. It is drawn like every other command, and what makes it stand out
  is the selection it already had. The pill stays on the tunnel's step, which is
  a different question.
- **It sat in the column at the bottom**, four rows from the sentence it
  answers, with the QR code and the address in between — where it reads as
  belonging to the address. `flyCmd.lead` gives it the one row between the
  status lines and the code, which is where an answer to a line goes.

**And the line above it was cut at both ends, which was the panel's defect and
not this chapter's** — eight existing sentences were already being truncated,
silently, the worst of them the question asked before disconnecting everybody.
The measurement and the rule that came out of it are in `140-windows.md`, where
the panel is: the status lines wrap now, into as many rows as they need.

What belongs here is that **these two sentences were shortened anyway**. As
first written they were 241 to 288 px against 236 of room, and they are 119 to
201 now — one row, in every language. A notice that fits its row is worth
more than one that is allowed to take two: the rows above the QR code are the
first thing read, and the second row is there for the sentences that cannot do
without it. It is why the notice says *microphone: permission is off* and not
where the switch is. **The button under it says that.**

**The guided path tells the two apart too**, and that is the screen where it
matters most: whoever is there is setting the monitor up for the first time, in
front of this machine. `onb.s1.cam-denied-detail` names the page in words —
Settings › Privacy & security › Camera — **and offers the link beside it, but
only where the link can work**: `ms-settings:` is a Windows shell address, so
from a phone it leads nowhere at all, and a dead affordance on the screen that
is diagnosing a fault is worse than none — the reader cannot tell a link that
does nothing from a permission that did not take. The test is the origin, and
it is sound because this page is served by the monitor: a loopback host means
the browser is on the machine the permission belongs to. Whoever opened the path
by its home address gets the sentence, which is true everywhere; and that is why
the sentence **names** the page instead of leaving the address to carry it.

**What is not covered, and it is not an oversight.** When the machine-wide
camera switch is off, Windows can hide the devices from the enumeration
altogether, and then what arrives here is *no usable webcam* — which is what
this program already says, and which is honest: nothing in that answer
distinguishes a revoked machine from one with no camera in it. The per-program
switch is the case that produces `E_ACCESSDENIED`, and it is the case this
chapter covers.

**It was tried on a revoked machine, and that is where three of its defects came
from.** The switch was thrown on a running monitor and thrown back: the refusal
reached the notification area and the guided path, and granting the permission
again cleared it. What that half hour found, none of which any test had:

- **the notice was cut at both ends.** `microfono: permesso disattivato in
  Windows` is 271 px against the 236 a row has, and centred with no ellipsis a
  line loses its first word and its last. It led to the measurement that found
  three of the *existing* sentences doing the same in four languages.
- **the command was in the wrong place and wore the wrong mark**, between the QR
  code and the address, in a filled pill on top of the focus it already had.
- **the check row stayed green.** The button appeared under it and the detail
  beside it said why, while the badge went on saying the camera worked, because
  it was reading `ready` — a latch. Pressing *Check again* could not help.

**The lesson is the one this file keeps relearning.** Every piece of the chain
was covered by a guard that passed: the sentinel, the precedence, the catalogue,
the alert. All four were true, and none of them is about what a person sees —
the width of a row, where a command sits, whether a badge is telling the truth.
**Putting a defect back and watching a guard fail proves the guard, not the
feature**, and the only thing that proved the feature was a permission actually
switched off with the monitor watching.

**What is still not demonstrated is the package.** Nobody has run this inside an
MSIX, where camera and microphone are declared capabilities and `%APPDATA%` is
redirected — which is the other half of `provenDir`'s reason for existing, and
the half with no evidence under it.

### A permission Windows is still asking about is not an absence

The chapter above is about a refusal, and a refusal **comes back**. Packaged as
MSIX it comes back the same way: `E_ACCESSDENIED` from
`IMFActivate::ActivateObject` for the camera and from `IAudioClient::Initialize`
for the microphone, `camDenied` and `micDenied` go up, and the two codes with
their button reach the page. That half needs nothing.

**What had no name is the state before any answer exists.** Packaged, the
consent is asked **per package and at the first use** — it appears in the
consent store under the package family name rather than under *let desktop apps
access your camera* — and while that dialogue is on the screen the open call
simply **waits**.

Unpackaged the camera opens in about half a second, well inside `startupGrace`,
so that window is invisible. Packaged it outlasts the grace by as much as the
person takes, and past the grace `!Ready` meant broken: what went out was
`capture-stopped` and `mic-missing`, the second of which **asserts there is no
microphone** at the one moment when the whole question is whether this program
may use the one that is there. It is the same defect either way; packaging only
makes it long enough to read.

**The remedy is a state and not a longer timeout.** `camOpening` and
`micOpening` say a call into the device is in flight and has not come back: set
before the call, cleared the moment it returns or the endpoint opens.
`micMissing`, `micSilent` and the "never started" half of `captureStopped` stand
down while one is up, and the icon falls through to `PhaseStarting`, which
already existed and was only ever kept out by `mic-missing` taking precedence
over it.

**A predicate waived in a chain does not disappear: it hands over.**
`activeFaults` walks denied, missing, silent as an `else if`, so waiving
`micMissing` alone drops the chain one branch further down — a microphone that
never opened has no level, the health reads as digital silence, and out goes
*the microphone delivers zeros*, **the worst fault this product has**, in place
of one that was merely wrong. `micSilent` is in the list for that reason.

**A test that hands the predicate a value the real state cannot have proves
nothing.** `MicHealth: MicCodeOK` is exactly what a microphone that has not
opened does not have, and `CameraOpenedUnix: 0` is exactly what a camera that
has just reopened does not have: both left a subtest green over a defect it was
written to catch. Where a status field is being waived, the case has to carry
the values that field really takes.

Four things this deliberately does not do:

- **It invents no deadline for a human.** A grace with a number in it would be
  the same mistake one size larger, and there is nothing to measure: how long
  somebody takes to read a dialogue is not a property of this program. While the
  answer is pending the monitor says it is starting, which is true, and Windows'
  own window is on the screen saying why.
- **It does not cover the stall, and the anchor belongs to one branch.** The
  grace is counted a second time from the last successful open, because by the
  time a waited-for device comes open the first grace is spent and the second
  the first keyframe takes would read as a capture that had stopped. Applied to
  the whole predicate that anchor **hides the stall**: a picture stopped minutes
  ago with the camera reopened a moment ago answers *nothing is wrong*, and a
  reopen loop whose backoff starts at a second keeps answering it. It lives
  inside the `!Ready` branch and nowhere else.
- **It does not swallow the answer.** The flag is cleared before the error is
  wrapped, so a refusal that came back is the denied pair's business from that
  instant. It is the distinction `internal/update` already makes: *"I could not
  ask" must never be rendered as an answer.*
- **It does not stop at the alert set.** The icon had a case of its own —
  `!s.RawAudio` sits below `!s.Ready`, so with the camera consented to first the
  picture goes ready while the microphone's dialogue is still up and the audio
  was announced as **filtered** before anybody had said whether it may be
  captured. The pages had two more: the viewer's microphone row and the guided
  path's check row went on shouting while the banner had learnt to keep quiet —
  and the guided path is the screen the person is standing at while Windows asks
  them. Every surface that reads the device's state is part of the same claim.

**The flags are cleared by a `defer` as well as by the store that marks the
right instant.** `runVideo` and `runAudio` run under `guard.Run` precisely
because that stretch goes into COM, Media Foundation and WASAPI: a recovered
panic between the set and the clear leaves the flag standing, and a flag standing
is the alert waived for ever.

**What is not known is what happens if nobody ever answers.** The monitor then
says *starting* for as long as the dialogue stands. That is written here rather
than guarded against, because the guard would need the number this chapter has
just refused to invent, and because the thing that would tell the watcher is
already on their screen.

### A microphone muted in Windows is a cause, and it was only in the log

The endpoint's mute is read at every open — it has to be, because in exclusive
mode the audio engine does not take part and the gain and the mute it would have
applied come back to us. Respecting it was right and complete. **Saying so was
neither**: `gain = 0` and a `Warn` line, and from there on the monitor
reported *the microphone delivers zeros*, which is the same fault it reports for
a dead capture path. The program knew why and told the file.

What that costs is not the mute, it is where it sends whoever reads. `mic-silent`
is the worst fault this product has and it accuses the device: a cable, a driver,
an array that has gone. The thing that was actually wrong is a slider on the
machine the reader is standing in front of, and nothing on any surface said the
word.

**So it is a code of its own and not a detail hung off the old one.** `mic-muted`
sits between `mic-missing` and `mic-silent` in the same chain the refusal already
walks, which is the rule stated one chapter up: **the cause is announced instead
of the consequence**, because the page shows one banner and of the two only one
says what to do. Below `mic-missing`, because a microphone that is not there
cannot usefully be called muted; above `mic-silent`, because the silence is what
the mute produces.

**And the Sound page, not the microphone's own properties.**
`ms-settings:sound-defaultinputproperties` lands on the very slider and is right
only while the muted endpoint is the default one — the chosen microphone can be
another device, and a page showing a slider that is not down is worse than a page
showing a list. There is an address that is always right,
`ms-settings:sound-properties?endpointId=`, and it wants the ID of the endpoint
that is capturing: `internal/tray` does not hold it and `cmd/pat-monitor` does.
It is a field's worth of work and it waits on somebody measuring that the URI
takes the ID in the shape WASAPI hands it over.

**It describes the last open, and that is a limit with a road out.** Reading
the mute again means asking the endpoint from a thread that is not holding it,
and the capture loop is no place for a call that crosses into the audio service.
So a mute is read where every other property of the endpoint is read, at the
open — and **the re-examination is what makes that enough**: a muted open arms
the same two-minute timer as a raw-mode fallback, so the monitor comes back to
look on its own and disarms the timer the moment it finds the endpoint unmuted.
Without that the button in the panel was a command whose remedy never reached
the monitor, which is written up in `micRecheckWanted`.

**What it costs is that the news is late and never absent**: up to two minutes
between somebody unmuting and the room being heard again, which is the same
figure the chosen microphone's return already carries.

**Measured live, with the endpoint really muted**, on a monitor with its own
configuration, the Funnel off and nothing recording:

| | |
|---|---|
| muted before start-up, the open declares it | `raw mode refused` then `the microphone is muted in Windows` |
| `alert code=mic-muted level=fault` | **+30.5 s**, which is the start-up grace |
| `mic-silent` in the whole run | **zero lines** |
| unmuted from outside | 11:56:12 |
| `alert cleared code=mic-muted` | 11:57:36.8, that is **2m00.7s after the open** |
| the microphone reopened, gain back, no mute line | +0.6 s after that |

The clearing sits on the re-examination's two minutes to the tenth of a second,
which is what says the recovery is that timer and not a coincidence — the same
constant this file already records twice on other devices. And the alert clears
**during** the reopen rather than after it, because `MicrophoneOpening` waives
the predicate: the state is correct throughout and does not flicker back.

**What this run does not demonstrate is the repair itself**, and that is worth
more than the numbers. This machine refuses raw mode, so the re-examination was
armed by the raw fallback whatever the mute did; the case the repair exists for
is a machine that **obtains** raw with nothing to fall back on, where there was
no timer at all. Showing it wants such a machine, and the two muted rows of
`TestARecheckIsArmedForEveryFallback` are what stands in for it — which is a
guard proving a guard, and this file has a chapter about why that is not the
same thing.

**And the function that answers with the page stopped being about permissions.**
It was `privacySetting` while the only two answers were privacy switches, and a
mute is not a permission: left alone, the name would have described two thirds of
what it does. The button's word never said *permission* — it says *open Windows
settings* — so only the name and the catalogue key had to follow it.

### The picture stopping is a fault, and `Ready` cannot say so

A monitor that has stopped showing anything is the one thing this program exists
to notice, and for a while it was the one thing it could not: the predicate
behind `capture-stopped` was `!s.Ready`, and `Ready` is a **latch** —
a channel closed by the first keyframe and never opened again. It answers "did
it ever start". It was read by three consumers as "is it working now".

What that cost, measured in one night's log:

| | |
|---|---|
| frames stop, inside a rebuild of the encoder | 14:58:38 |
| the cadence falls, 6.9 → 3.6 → **2.0** fps, quantiser unreadable | to 14:59:12 |
| viewers connect, `last_video_kbps=0` | three of them |
| alerts raised in that time | **none** |
| the page, the notification area | green throughout |
| noticed by | a person, 3m33s later |

**Every instrument it needed was already there and nobody was loading it.**
`Stats.LastVideoUnix` is written on every access unit, and its comment says it
exists "to notice that one stream has stopped while everything else stays
alive" — a sentence that had been true and unread for as long as it had been
written. The same holds one floor up: `SetCamera` argues it needs no grace of
its own because `capture-stopped` "asks for half a minute of no frames", and no
code asked for anything of the sort. **Two pieces of prose describing a feature
nobody had built**, each plausible beside the other.

So the predicate is now a **freshness**: no frame for `videoStall`, which is the
half minute the prose already promised. It keeps the old half as well, because
they are two faults with one name and one remedy — a capture that never started,
and one that has stopped — and what must not happen again is the second being
invisible because the first is false.

**The grace and the stall are not the same number and must not be merged.**
Thirty seconds of startup grace is "the camera is still opening"; thirty seconds
of no frames is "the picture has stopped". They are equal today by coincidence
of the hardware and they answer different questions: the first is about the
process's age, the second about the last frame.

**One predicate, three consumers, and that is why it was worth repairing rather
than adding to.** `captureStopped` is read by the tray's colour, by the banner
on the page and by the alert written to the log. A second test written beside it
for the stall would have been a second list of what "not working" means — the
failure this file already names — and the three surfaces would have diverged at
the first touch.

**There were four, and the fourth was in JavaScript.** The sentence above counts
the consumers of `captureStopped` — the tray, the banner, the log — and every one
of them is in Go, which is why repairing the predicate reached them all. The
guided path's check row is the fourth, it asks the same question, and it asked it
of `ready` **directly**: `!!s.ready && s.resolution !== ''`. A repair made in Go
cannot reach a fourth copy written on a page, and nothing counted it, because the
count was taken by reading the callers of a Go function.

What it cost is the shape the latch always costs: **the row went green at the
first frame and stayed green.** Reported from in front of the machine — grant the
permission and it goes green, revoke it and *the button appears and it stays
green, even pressing "Check again"*. That last clause is the diagnosis: the
button and the detail line beside it were reading `cameraDenied`, live and
correct, while the badge two centimetres away was reading a field whose whole
meaning is *it started once*. Re-checking could not help, because re-reading a
latch gives the latch.

It reads `measuredFps` now, which is the live half and needed no new state — its
window is charged against **real time**, so with the frames stopped it decays to
zero on its own — and `cameraDenied`, which is the immediate half and the only
one of the two that says why. `TestTheCheckRowDoesNotDecideOnALatch` refuses
`ready` in that condition, reading the declaration rather than the prose around
it and **stripping the comments first**, because the paragraph explaining the
repair says `ready` four times.

**The rule the count should have followed is the one this file states
elsewhere**: at every change of shape the question is not who writes a field but
**who reads it** — and `ready` is read by whoever can fetch `/api/status`, which
is three Go call sites and two pages.

**The fault raises a goroutine dump, once.** A picture that has stopped after
having started is the only fault here that can leave nothing else to read: no
error, no restart, nothing in the log but silence. `runtime.Stack` over every
goroutine says where the capture is, and it is what a stripped binary can still
answer — the profiler is behind a flag nobody runs at night, and `dlv attach`
wants a build that is not the one that ships.

**It does not separate that silence from an unplugged camera**, which reaches
the same alert with the log already full of the capture's errors, and it does
not pretend to: telling them apart would take a second reading of state, that
is, a second idea of what "not working" means. One dump per process is the
price of covering the silent one. A capture that never started is excluded, and
that one really is different — the last frame is zero, the stacks would show a
camera being opened, and the error is on the line above.

**And the test instrument could spend it, which is what "once per process"
costs.** The dump is raised while walking the alerts that **appeared**, and that
set is `presentAlerts` **plus** `simulatedFaults` — so `-simulate-fault
capture-stopped` raises the code on a monitor whose capture is running, where
`Ready` is true and the last frame arrived a moment ago, that is, where both
conditions guarding the dump hold by construction. Sixty kilobytes of stacks of
a healthy pipeline, and `dumpOnce` gone: a real stall the same night would have
written nothing, which is the silence all of this exists to break. The remedy is
to ask the predicate rather than read the loop — `stalledAfterStarting` calls
`captureStopped` on the status, where no flag can reach — and it is the "one
predicate, three consumers" rule again, met from the side where a **fourth**
reader had quietly written a fourth idea of the same question. Verified live:
the flag raises the alert, clears it, and the log stays 3.3 kB.

**And the rebuild that hung is timed, which is a smaller claim than it looks.**
Between "video format changed" from the governor and the same line from the
capture there are two calls into Media Foundation — building the new transform
and releasing the old — and on that night one of them did not return, so the
pair of lines came out as one. Timing them names a rebuild that takes seconds
**and comes back**; it can say nothing about one that does not, which is what
the dump above is for. The threshold is `slowRebuild`, two seconds, against 107
to 330 ms measured across every size change in a day of that log: a warning that
fires on an ordinary night is one nobody reads on the night it means something.

**There are three intervals and not two, because a rebuild can be turned down
first.** An encoder that refuses the new cadence is asked again with the old
one, and the first attempt costs real time in the driver with the frame loop
stopped for it — so a refusal of 1.8 s, a build of 1.0 and a close of 0.1 is a
**2.9 second** stall. Weighing the build alone reported 1.1, under the
threshold, and the log said nothing about the very case the timing was added
for. The fields stay separate, because they accuse different people — asking for
a session, asking for a cadence, letting the old one go — and what is compared
against the threshold is their **sum**, which is what the picture waited. It is
the same distinction as everywhere else here: **attribution is one question and
the stall is another**, and a number that answers the first is not automatically
the one to threshold.

### The machine must not fall asleep, and who is holding it awake is readable

Nothing in this program had ever asked Windows to stay awake, and nothing in
these chapters had noticed. The idle timeout is the operating system's, it is on
by default, and what it produces is the one failure with no symptom to diagnose:
a monitor that was watching at midnight and is not there at two, with a log that
ends mid-sentence and no fault anywhere, because nothing broke.

**It is not a configuration key**, for the reason the packaged build's update
check is not one: it is not a preference somebody might want the other way
round. The program only runs while it is watching, and a monitor that lets the
computer sleep is not a monitor. A key would be a way of switching the product
off that reads like a setting.

**`PowerCreateRequest` and not `SetThreadExecutionState`, and the difference is
a sentence.** Both hold the machine awake; only the first carries a reason, and
`powercfg /requests` prints it under our process. So whoever notices the computer
has stopped sleeping can ask Windows who is responsible and **read the answer**,
instead of going through startup entries. The old call holds the machine awake
anonymously, which on somebody else's computer is the difference between a
program and a nuisance.

**Measured, from an elevated prompt with the monitor running**, which is the
only thing that separates a request that was made from one that is held:

	SYSTEM:
	[PROCESS] \Device\HarddiskVolume3\...\bin\pat-monitor.exe
	PAT Monitor is watching the room

	DISPLAY:
	Nessuna.

	ESECUZIONE:
	Nessuna.

**Three things at once, and the two empty sections are worth as much as the full
one.** The request is live and it is ours, named by the path and by the sentence
whoever reads that list actually needs. `DISPLAY` is empty, which is the decision
about the bedroom, held by nothing but the absence of a second call. And
`ESECUZIONE` is empty, which is `PowerRequestExecutionRequired` not being taken:
the two really are separate rows of that list, so the argument for not taking it
is checkable from outside rather than only from the documentation.

**The display is deliberately left to go dark.** `PowerRequestDisplayRequired`
exists and would light a bedroom all night. What has to stay awake is the system.

Three things the call does that nothing about it looks like:

- **It fails with `INVALID_HANDLE_VALUE` and not with zero.** Read as *nil means
  it went wrong* — which is what every other handle-returning call here teaches —
  it reports success for every failure, and then the machine sleeps with the log
  saying it will not.
- **It keeps the reason string rather than copying it.** The pointer is read for
  the life of the handle, so a buffer built inside the call and left to the
  collector is memory Windows goes on reading; another project shipped exactly
  that and it was found by a reader rather than by a crash, because freed bytes
  usually still say what they said. The buffer is a field of the request.
- **`REASON_CONTEXT` is a layout Go will not check.** A field inserted or
  widened compiles and has Windows read the pointer out of the middle of two
  integers, with no error on either side. `awake_windows_test.go` asserts the
  three offsets rather than trusting them, and the release is nil-safe and
  happens once — both roads out of the monitor can reach it, the deferred one
  and the end of the Windows session, and clearing a request twice closes a
  handle twice, which lands on whatever has been given that number since.

**A refusal is worth a line and not a stop.** What is lost is the machine
possibly sleeping, which is the state everything was in before this existed;
refusing to monitor over it would trade a maybe for a certainty.

**What is not measured here is the one case where the request is not enough.**
On a modern standby machine running on battery, Windows terminates system
requests five minutes after the sleep timeout expires — documented, not measured
on this hardware — so a laptop left unplugged still stops. That is an argument
for reading the power source, not for holding the display on, and nobody has
read it yet. It is also why the log line says the request was **made** rather
than that the machine is awake: the claim can expire in the night, in the file
somebody opens to find out why it stopped.

**And `PowerRequestExecutionRequired` is not the answer to it**, which is
written down because it is the obvious next proposal and a review has already
made it. That request is about the **process** — *"the calling process continues
to run instead of being suspended or terminated by process lifetime management
mechanisms"* — not about the system, and the same sentence that terminates
system requests on battery terminates *"system and execution required"* ones
together. On traditional S3 it implies `PowerRequestSystemRequired`, which is
the direction that needs no help. So it would buy nothing on the case it is
proposed for, and on a packaged full-trust desktop program there is no lifetime
management to be suspended by in the first place.

### A shutdown that does not finish keeps the camera

The same stuck call, one floor up. The quit was asked for at 15:02:11: the
tunnel went off, the sessions were revoked, the HTTP server closed and **the
port was released** — and then `g.Wait()` sat on the capture goroutine, which
was inside the driver call that never returned. The process stayed alive for
**seven minutes** with the webcam lit, `shutdown complete` never written, and it
ended because somebody killed it rather than by itself.

**The cost was not one monitor, it was two.** The single-instance check in this
program is the listener — "whoever does not get the port is not this machine's
monitor" — and the hung process had already given the port up. So the copy
started to put things right came up cleanly, took the port, opened the camera,
and lost it about 900 ms later, eight times over, to
`MF_E_HW_MFT_FAILED_START_STREAMING`: the first process was still holding it.
**A guard that covers the running program and not the one that is ending covers
the case that never happens.**

So the wait has a deadline: `shutdownLimit`, ten seconds, started at the request
to stop and not at start-up. What it does is **stop waiting, not unblock
anything** — the thread is inside a driver call and nothing in this program can
reach it. What the operating system does on any process that exits is take the
camera, the handles and the memory back, which is the argument already written
for the encoder that is not released at the end, applied one floor up: **a
resource the system is about to reclaim is not worth waiting for.**

Three decisions that are not visible from the code:

- **The exit code is its own**, `exitShutdownStuck`, and not the 1 of an error:
  a monitor that failed and a monitor that did everything asked and was still
  holding something are two different things to whoever finds the process gone.
  On this platform the exit code is often all there is — see "Nothing restarts
  the monitor, and Windows cannot be asked to".
- **The stacks go into the log before the exit**, for the same reason the stall
  dumps them: this is the one moment at which what the process is waiting for is
  still there to be read, and a minute later it is a fact nobody can recover.
- **A named mutex was considered and not taken.** It would close the window
  completely, and it would also forbid what this repository already contemplates
  — a second instance started from another folder with its own configuration,
  its own port and its own log. The hole the deadline leaves is ten seconds
  wide; a machine-wide lock to close it would break a working setup, and one
  keyed to the configuration would be a second single-instance rule beside the
  listener. **The measured fault was a wait with no end, and that is what was
  repaired.**
