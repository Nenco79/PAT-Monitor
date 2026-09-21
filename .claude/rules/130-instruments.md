---
paths:
  - "internal/applog/**"
  - "internal/diag/**"
  - "cmd/pat-diag/**"
  - "cmd/pat-capture/**"
  - "cmd/pat-viewer/**"
  - "cmd/pat-wasapi/**"
  - "cmd/pat-opus/**"
  - "cmd/pat-sounds/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## An instrument is not believed until something else has checked it

In `cmd/`. The first to run on a new machine is **`pat-diag`**: it says whether
there is a Direct3D device, which H.264 encoders exist, whether the chosen one
really works on the GPU, **which formats the camera declares**, and whether the
microphone opens. It asks the questions the way the monitor asks them — encoders
are asked of the system instead of trying to encode, the microphone is opened
instead of being looked for in a list.

The format list is not for the curious: when the open fails with
`MF_E_INVALIDMEDIATYPE`, it is the only thing that says what should have been
asked for. It also shows the streams, which are more than one more often than
one thinks — webcams with Windows Hello also expose an infrared sensor.

**And it prints the camera's symbolic link whole**, because that string is the
value of `camera_device_id` and this is the only place it can be read. It used
to be cut at 88 characters and the real one is 94, so what came out looked
complete and matched nothing: the monitor refuses to start on an id it cannot
find and lists **names**, which for two cameras of the same model is the same
name twice — nothing to act on, in exactly the case that key exists for.
**Truncating a name costs a glance; truncating an identifier costs the choice it
identifies.**

Then **`pat-capture`**, which exercises the whole pipeline and measures the
delivery regularity of the four streams:

```
bin\pat-diag.exe
bin\pat-capture.exe -d 40s
```

Its report is the one in the two `baselines/capture-*.txt`, and the line to look
at first is **delivery regularity**, not the totals: a stream arriving in bursts
instead of at a cadence propagates intact to the browser's playback buffer,
where it becomes constant delay, while the totals would stay perfect.

**And on AMD that line says a third, and always has**: 32-36% of intervals in
bursts — `min 0s`, that is, two frames delivered in the same instant and then a
67 ms gap — against **0%** on Quick Sync. That encoder emits frames two at a
time. It is not a regression, and that is said by the only test that can
establish it: rebuilding `pat-capture` from before the changes and re-measuring
in the same session, the line said 37%, identical. Nobody knew because on that
machine no measurement had ever been taken, and that is why the reference is now
per machine: **a number never taken is not "good", it is unknown**, and the
first time one looks at it, it looks like a fault.

Latency is measured only from the browser, and lives in `baselines/latency.txt`
(raw data in `webrtc_internals_dump.txt`, **ignored by git** because the ICE
candidates are the real addresses of the home network: if it is not there, it is
not missing, it needs redoing). Two things to know before looking at it:
**`googCurrentDelayMs` no longer exists** — the equivalent is
`jitterBufferDelay/jitterBufferEmittedCount`, plus decode and render, so
comparisons with old measurements are indicative; and **the video delay grows,
and it is not our fault** — the minimum required by the network stays 11-14 ms
for the whole session while the target rises in steps from 12 to 65 ms, and it
is Chrome aligning video to audio, whose playback delay is dictated by the
watcher's sound card. Looking for that delay in our encoder means looking where
it is not.

Variability between identical runs is about ±30%: do not chase a single
measurement.

### The CPU profile on Windows measures waits, not work

`-pprof localhost:6060` opens `net/http/pprof`, and **refuses any address that
is not loopback**: those routes expose every goroutine's stack and let whoever
asks run a profile, which on a program with a public address are both a leak and
a way of making the machine work from outside.

**The route that really matters is not the profile: it is the goroutine dump**
(`/debug/pprof/goroutine?debug=2`). It answers with the stack of **every**
goroutine, function names and line numbers included, and — this is the point —
**even on a stripped binary**, because the runtime builds it from the `pclntab`.
It is the only tool that answers "what is it waiting for?" in front of a hung
monitor, and it is better than a debugger: nothing to install, and `dlv attach`
instead **kills the process when it exits**. A monitor has already stopped
responding for nine minutes with the cause undeterminable, because `-pprof` was
off.

**But the CPU profile, here, does not measure the CPU.** On a process using 13%
of a core, `go tool pprof` reports **277% total CPU** and 98.7% of samples in
`runtime.cgocall`: Go's profiler counts as samples the threads parked inside a
system call, and this program has three that sleep nearly always — the WASAPI
event wait, `ReadSample`, and the tray's `GetMessage`, 34% + 29% + 34%. **What
one is looking for is among the discarded nodes**, and in the real case they
were all below 1.4% of a core each.

**The right tool for this question is differential measurement**: change one
variable at a time and look at the process's CPU time. That is how it emerged
that the cost does not depend on the pixels (a quarter of the image costs the
same), depends little on the frames (2.9 points by removing two thirds), and for
the rest sits inside Media Foundation and WASAPI, not in our code. **A wrong
measurement always accuses somebody else**: here it would have accused the tray,
which is the most idle thread in the program.

### A tool that opens differently proves nothing

`pat-diag`'s resolution-scale check opened the camera **with the Direct3D
device**, and the monitor opens it without. It is not a detail: with a D3D
manager the Source Reader converts in DXVA, without it converts in software, and
those are two different paths inside Media Foundation. The monitor passes `nil`
deliberately — it needs the frames in system memory, because motion detection
has to read their luma plane.

The result is a tool that can **declare green a machine on which the monitor
delivers no frame**, and that is what happened: the report said `1280x720
delivered` on a webcam whose maximum is 640x480, and for half an hour that line
made the right hypothesis be discarded.

Two corrections, and the second is worth more than the first. `probeScaling`
opens as the monitor opens. And when the requested size exceeds the declared
one, the section says so: **an upscale succeeds**, and a line saying only
"delivered" reads as "the camera can do it". It is the rule already written for
the encoder — the check must be taken as close as possible to whoever consumes
the data — applied to whoever writes the check.

**And the same rule, broken the other way round, in `pat-capture`: it opened a
camera the monitor would never open.** `devices.ListCameras` hands back
everything Media Foundation enumerates, infrared sensors included; the monitor
goes through `devices.Pick`, which drops them first, because an infrared sensor
read as ordinary video gives a picture nobody can watch. The tool took the first
of the raw list — under a comment that called it *the monitor's rule*, a claim
that was true when it was written and had stopped being.

On a laptop whose Windows Hello sensor enumerates first, this tool therefore
measured the infrared camera while the monitor filmed the room: **same binary,
same flags, a different device**, and nothing in the report saying so. Every
number in `baselines/` comes out of this tool.

The correction is the asymmetry rather than the filter: **with nothing asked
for it opens what the monitor would open, and `-cam` still reaches anything,
including an infrared one, because there somebody has chosen** — and when the
choice lands on one, the header says so. It is `KeepRequestedSize` one field
across: the instrument declares, and the thing it must not do is quietly
measure something else.

**Nobody found this by reading.** It came out of a review that was killed by a
session limit before it could write anything down, and what survived on disk was
one sentence — *confirmed a real instrument-parity defect in pat-capture* —
with no detail at all. What made it recoverable was the **trail of tool calls**:
`cmd/pat-capture/main.go:118-160`, `internal/devices/mf_windows.go`, the callers
of `ListCameras`, `IsLikelyIR`. A conclusion that was never written is gone; the
reading that led to it is not, and here it was enough to find the defect again
in four greps. **An agent's transcript is an instrument too**, and it survives
the agent.

**`pat-wasapi`** measures the microphone's three paths one after another —
shared, raw and exclusive — and compares them. It answers "why is it so quiet":
the RMS difference between raw and exclusive **is** the endpoint gain that one
of the two roads does not receive.

**`pat-opus`** exercises the audio encoder: it encodes with libopus and decodes
with `pion/opus`, which is an **independent** implementation — two roads that
check each other. `-rate` tries the other four rates. Measured on all five:
correlation ≥0.9998, S/N between 34 and 42.6 dB, codec delay 6.5 ms.

The test sweep stops **below Nyquist**, and it took an odd number to notice: at
8 kHz a sweep up to 8 kHz is half above the limit, enters already distorted and
comes out worse. The resulting 21 dB looked like a codec unsuited to low rates;
with the test corrected, that is the rate that does **best**. **A wrong
measurement always accuses somebody else.**

**`pat-viewer`** is a synthetic Pion viewer, and **its cadence is counted from
the RTP timestamps** after having lied in two opposite ways: it counted slice
NALs — but Quick Sync puts **three** per picture, and a real 9.8 fps became
"29.8" — and its buffer stopped at 8 MB while the count did not, so a minute of
pictures was divided by two minutes of test. The two errors have opposite signs,
masked each other for a whole session, and made the **monitor's** counter, which
was right, look suspect. What dismantled them was whoever was watching the page
— "I see 29.7-29.9" — not because the page could arbitrate, but because it said
the image was fluid, and 15 fps is not. **A tool that stops measuring and does
not say so makes what it measures look broken**: the truncation is now declared.

**`pat-viewer -pli 1s`** sends keyframe requests on command and measures how
long one takes to arrive — the only way to test the PLI response without really
losing packets. A wait around one second, that is, half the GOP, means nobody
listened to the request; measured working, 116 ms over 19 requests.

**`pat-sounds`** runs the recogniser over sets of recordings labelled by others,
and from there come the classes and the thresholds. Four steps, and the first is
the only expensive one: `score` writes a CSV with one row per clip and one
column per candidate class, `judge` reads it and costs nothing, `dilute`
measures what happens to a short event inside a whole window — which is the
condition the monitor works in and which no dataset reproduces — and `gate`
measures the shape detector, **which is judged the other way round**: of the
model one cares that it does not get it wrong, of the gate that it does not lose
anything. The sets are not in the repository: where to get them and what came
out is in `baselines/sounds.txt`.

**`-audio-test-tone`** (in `pat-capture` it is `-tone`) replaces the microphone
with a generated tone that enters the chain exactly where the microphone would.
It does not test WASAPI capture, which is precisely where the APO bypass is: the
tone one hears says everything else works, not that the microphone will be
audible. Whoever enables it declares it in the log and on the status page,
because a baby monitor transmitting a tone in place of the room must not be able
to go unnoticed.

**`pat-capture` grew three flags the day a machine nobody had run appeared**,
and each exists because a question could not otherwise be asked. **`-cam`**
chooses which camera to measure on: without it the instrument takes the first of
the enumeration, which on a machine where the monitor is already running is the
one the monitor is holding — and with two webcams of the same model the name
alone cannot choose, so it matches the link too. **`-w2`** gives the width to go
with `-h2`, because the resolution scale's steps are **not 16:9**: three quarters
of 1280x720 is 960x528, while a width derived from the height gives 928, that is,
a tool asking a size the monitor never asks for. And **`-pli`** requests
keyframes and times them, which is the one road to `AVEncVideoForceKeyFrame`
without a viewer: if the encoder ignores the request, a viewer that loses the
reference waits for the periodic keyframe instead of a frame, and the property is
declared per vendor while the declaration is worth nothing — AMD answers
`E_NOTIMPL` to `IsModifiable` and honours it perfectly. Measured on AMD from this
road: **24 asked, 24 answered within five frames, median 27 ms**, which agrees
with the 48 ms measured through `pat-viewer` on the same chip.

**A request counts as answered inside the pipeline's own window, five frames**,
and that is not a detail: beyond it what arrives is the periodic keyframe, so a
wider window would score an encoder that ignores the command as one that obeys
slowly. It also has to **expire**. The first version cleared the pending request
only when a keyframe arrived, so on an encoder that ignored the command the first
request would have stayed pending for ever and the report would have said "1
asked" instead of showing the silence of forty — a measurement that stops
measuring and does not say so, which is the defect `pat-viewer`'s truncated
buffer already cost once. And `-pli` inflates the keyframe count on purpose: the
line above it will read more than the GOP predicts, and that is the requests
being answered.

`internal/diag.Delivery` measures a stream's regularity with a sliding window:
whoever needs the report over the whole test sets `Window`.

### The log goes to a file, and it is not an accessory

It lives in `%APPDATA%\PAT Monitor\log\`, rotated by size (`internal/applog`).
It goes **to file and console together**: the console is the most direct way to
watch while it exists, but with `-H=windowsgui` it does not exist. The cost of
not having it has already been paid: a process that exited at night left only a
switched-off webcam, and the cause — a race of ours — had to be **deduced
instead of read**. No record in the Windows event log: a Go panic exits cleanly.

- **The two destinations must not be able to silence each other.** It was
  `io.MultiWriter(os.Stderr, logFile)`, which writes in order and **returns at
  the first error**: launching a binary with no inherited console,
  `os.Stderr.Write` answers "The handle is invalid" and with that ordering **the
  file receives zero bytes**. That is, the file log died exactly in the
  configuration it was invented for, the one where it is the only witness: the
  monitor ran, the folder stayed at the previous day's timestamp, and a fault
  lasting nine minutes left not a line. There is now `applog.Fanout`, which
  writes to every live destination and declares one dead only for itself. **The
  ordering was the whole difference between a log that exists and one that does
  not**, and `fanout_test.go` tests both directions — and first of all
  demonstrates `io.MultiWriter`'s defect, so whoever wants to simplify back to
  it finds written why it cannot be done.
- **A log that cannot be written stops nothing.** The monitor starts anyway and
  declares it: refusing to watch a child because there is nowhere to write would
  be absurd. And after the first error it stops retrying, otherwise every line
  would generate as much noise as the log itself.
- **Reopening continues the file, it does not truncate it.** The lines
  explaining why there was a restart are **before** the restart.

**A code the reader cannot look up is not a diagnosis.** A microphone unplugged
while the monitor watches fails the capture loop, and what reached the log was
`error 2290679812 (FormatMessage failed with: The system cannot find message
text …)` — a decimal nobody can search for, beside a sentence saying the lookup
failed. The AUDCLNT codes are not in the system message table, so go-ole can
only print the number. `describeAudclnt` converts the base and names what it
knows, and it existed all along: **what was missing was calling it.** Of the
forty-five wraps in this package, thirty-one skipped it — and among them all
three COM calls of the capture loop, which is the one path that reports a
device vanishing while the monitor watches. The fourteen that did go through it
are nearly all opens, met in front of a console. **The half that gets described
is the half somebody was looking at when they wrote it.**

**And no test on that function could have caught it.** `describeAudclnt` was
correct; the defect was thirty-one call sites that never reached it, and a test
on the output absolves every one of them — it is the family of "a test that
does not cross the branch absolves the branch".
`TestNoCOMErrorIsWrappedUndescribed` therefore reads the syntax tree: an error
coming straight out of a method call on a COM object goes through it. **The
rule needs no list of codes**, and its one exception is a category rather than
an entry — the `windows` syscall package, whose errors carry their own text and
for which a function named after AUDCLNT codes would say something false. It
counts the sites it recognised, 57, and fails below twenty: a guard that
matches nothing passes, and that is the shape a refactor turns it into
silently. Verified to catch, on the very site that produced the line above.

#### Two instances write one file, and the line says which

The log follows the configuration, so a second copy started from the same folder
appends to the same `monitor.log`. The case that costs is one instance stuck in
its shutdown with the camera open and a second retrying the capture every thirty
seconds: the two streams of lines interleave and are **indistinguishable**, half
of reading such a file goes on deciding which of the two wrote each line, and the
answer is not otherwise in it.

Every line now carries `pid`, which costs nine characters. It goes on the
handler and not on the `version` line, because the question is not "was there a
second instance" — two `version` lines an hour apart already say so — but
"whose line is this one", and that is asked of every line.

**The alternative was to keep them apart**, and it is the one already taken for
the folder: `-config` elsewhere means a log elsewhere. That covers the second
instance somebody starts deliberately and not the one this fault produced, where
both processes are the same binary reading the same configuration.

### The session log is the only instrument for the test that counts

This program's decisive test — phone on cellular, Tailscale off, ten minutes —
is done **away from the PC**. There one watches no status page and opens no
`webrtc-internals`; on return there are only log lines, and they have to be
enough to tell a network losing packets from an encoder not responding. Those
are two faults with two different remedies, and an impression does not separate
them.

**And the line it all ends in could carry a number no link has ever produced.**
The report subtracts two snapshots of cumulative counters, and the snapshot was
marked valid **before anything had been asked** of the statistics interceptor —
which answers nothing once it has unbound a stream, that is, while the session
is being torn down, which is when the watcher's ticker is most likely to fire
one last time. Zeros minus a large total, in `uint64`, does not go negative: it
wraps, and `video_kbps` came out at about **9.8e15**.

It does not stay in a debug line either. The loop keeps the last report and
hands it to `logSessionEnd`, so it lands in the closing line of the session —
the one this chapter calls the only report there is.

**A difference needs two real readings, and the first repair only gave it one.**
Marking the unread snapshot invalid protects the **next** report, because it is
the baseline then; this one was still subtracting, because the test was on the
previous snapshot alone. The test written for the defect caught the half-repair,
which is the one thing a test written afterwards can do that rereading the diff
cannot. It is `internal/rtc`'s own rule — **a missing reading is not a reading
of zero** — which three windows in that package already keep and which nobody
had applied to a pair of counters.

It lives in `internal/server/viewerlog.go`. Three non-obvious things:

- **Provenance is classified, not printed.** From the Funnel `RemoteAddr` is the
  ingress node and is the same for everybody: the real address comes from
  `tunnel.SourceAddr`. `requestOrigin` distinguishes Funnel, tailnet, local
  network and this PC, and it is covered by tests precisely because getting it
  wrong would attribute every visit from the Internet to the same person.
- **The numbers are differences, not totals.** A growing total says something
  happened but not when, and after ten minutes a loss concentrated in the first
  minute is indistinguishable from one spread over all ten.
- **The session is announced when ICE has chosen a path**, not when the
  WebSocket opens: the path (`host`, `srflx`, `relay`) is the datum that says
  whether the connection from outside worked. If within twenty seconds there is
  none, that is the outcome and it is written as such — a disconnection with no
  media is a fault, and reporting it as a visit would hide it among the others.

The periodic lines are at `-v`; opening, closing, path change and loss above 2%
come out anyway, because otherwise finding them would require having switched
debug on **before** the fault happened.

Two traps found by the log **on itself**, at the first real session:

- **`PeerConnection.GetStats()` produces nothing for a sender.** The type
  `webrtc.OutboundRTPStreamStats` exists, is read from the report without errors
  and stays at zero forever: in Pion only receivers populate it. The report was
  zeros on a session that was meanwhile delivering audio and video perfectly.
  The real numbers are in the stats interceptor, interrogated **per SSRC**;
  `RegisterDefaultInterceptors` already installs one but keeps its Getter in a
  private map, so one of ours is needed.
- **The stats report is a map, and Go iterates it at random.** Looking there for
  the candidate pair "in state succeeded" works while there is only one; when
  both the host and the srflx stay valid — the normal case — the path "changes"
  at every sample, back and forth, for a whole night of false lines. Ask the ICE
  transport, which knows which one it is using: `GetSelectedCandidatePair`. How
  wrong the other way is, is said by the `webrtc-internals` dump: **six**
  `succeeded` pairs, and **one** with `nominated=true`.

