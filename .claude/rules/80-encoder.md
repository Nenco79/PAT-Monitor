---
paths:
  - "internal/mf/**"
  - "internal/media/**"
  - "internal/encoder/**"
---

Part of PAT Monitor's engineering record — chapters of **The loop: what
the monitor decides, and in what order**; the index that carries every
chapter, in order, is in `CLAUDE.md`.

### When the process is ending, the encoder is not released

Releasing the NVIDIA H.264 Encoder MFT takes the process out with a
**0xC0000005** about one time in five, and only at the end: every teardown in
the middle of a session — a capture restart, a change of size, a camera swapped
— goes through the identical code and never dies. What is different at the end
is that the whole process is unwinding around the release.

**It is the product's and not the instrument's**, and that took twenty
shutdowns to establish, because the first seven were clean in a row:

| | |
|---|---|
| `pat-capture`, NVIDIA encoder | **6 deaths of 14** |
| `pat-capture`, Microsoft software encoder | **0 of 14** |
| the monitor's orderly shutdown, before | **4 of 20**, none writing `shutdown complete` |
| the monitor's orderly shutdown, after | **0 of 40** |
| `pat-capture` with the same skip taken | **6 of 14** — unchanged |

**Windows names nothing.** No WER event for our binaries in seven days that
carry five crashes from other programs, and `LocalDumps` set for that binary
alone collected no dump across seven deaths: the process does not die through
the unhandled-exception path, so there is nothing to collect. The exit code is
the only witness there is.

**The remedy is to do nothing**, and the precedent is one layer down in this
same repository, written in the same words: the async callback's pin is
deliberately leaked, because *freeing the object while Media Foundation holds it
means a process crash instead of an error*. At the end the operating system is
about to reclaim every handle this process owns, so the release buys nothing —
and it costs the last lines of the log and whatever the recorder had open,
which on a baby monitor is the clip somebody wanted.

**Only the last release is skipped**, and the condition is the context: it is
cancelled only by a shutdown somebody asked for. A restart still closes, because
there the process goes on and an encoder per restart really would accumulate.

**And it cures the monitor and not the instrument, which says the release was
one path and not the family.** `pat-capture` takes the same branch — the log
says `the encoder is left to the operating system` — and still dies 6 times in
14, against 8 in 14 without it. So something else on that vendor faults while
the process unwinds, and what differs is what each program does **after** the
capture has stopped: the tool stays alive to compose a report, the monitor
does not. Chasing it further would want the vectored handler this file has
already named; what is worth writing down is that the instrument is still the
cheap way to reproduce the fault.

**And "it works" was nearly asserted on 19 runs.** At one in five, nineteen
clean shutdowns in a row happen 1.4% of the time — unlikely, but this is the
same file that records seven clean in a row at that rate. It is forty now, and
the reason the count was doubled is that the tool refused to be cured by the
same change: **a remedy that does not travel is a reason to re-count, not to
explain.**

**And this is a measurement, not a rule about Media Foundation.** One vendor,
one machine. What holds generally is the shape: **a resource the operating
system is about to reclaim is not worth a crash**, and the test for "did it
work" is the exit code over forty runs, not the absence of an error.

### An asynchronous encoder's request is worth one time only

Hardware encoders are **asynchronous** MFTs: they ask for a frame with
`METransformNeedInput` and keep **only one** in flight. If that request is
discarded — for example because `ReadSample` had no frame ready and the loop
goes back to the top — a second one never arrives and the encoder stays stopped
forever. Symmetric on the way out: every `METransformHaveOutput` authorises
**exactly one** `ProcessOutput`, and insisting answers `E_UNEXPECTED`.

The fault is indistinguishable from an encoder that will not start, and that is
what makes it expensive: the transform answers everything correctly —
`ProcessInput` with `MF_E_NOTACCEPTING`, `ProcessOutput` with `E_UNEXPECTED`,
which are the **prescribed** answers to somebody who has not received an event —
declares `GetInputStatus` as `ACCEPT_DATA`, accepts the Direct3D device, accepts
every `ICodecAPI` property, and stays silent.

What broke the deadlock was **counting the calls to `Invoke`**. An event that
arrived and was then discarded by our code, and an event that never arrived,
look exactly the same from outside: only a counter tells them apart. It lives in
`asyncCallback`, together with the event log and each event's outcome.

**And closing that object did not wait for it.** `close()` shut a channel and
returned, which stops the *next* subscription and says nothing about the call
already running: the caller then released the event generator and the transform
while a Media Foundation thread was inside `EndGetEvent`, calling through the
vtable of what was being freed. The window is small and it is walked constantly
— **the resolution scale rebuilds the encoder**, so this teardown happens at
every step of the ladder — and the file's own comment beside the leaked pin
already said that at that moment *a request is nearly always in flight*. That
sentence was the argument for leaking the pin, and the Go object was the only
thing it protected.

It now counts the Invokes inside the object and waits for them. **A counter and
not an `RWMutex`**, and the arithmetic is the whole reason: `Invoke` re-arms the
subscription before it returns, so were Media Foundation ever to deliver the
next event on that same thread, from inside that call, the read side would be
taken twice with a writer waiting — which in Go is a deadlock, that is, **a hung
monitor instead of a crashed one**. A counter is reentrant by construction. The
pin is still leaked, for the reason it always was; what changed is that a late
`Invoke` now returns without touching anything.

**`lastErr` was the other half of the same object and had no protection at
all.** It is written from the Media Foundation thread and read by `Diagnose`
from whichever thread asked — an interface is two words, and the reader of a
torn one gets a type and somebody else's pointer. `invocations` beside it was
already atomic and `seen` already had the mutex; this one field had neither.
**The race detector cannot say so here** — it wants cgo and this machine has no
C compiler — so the guard reads the syntax tree instead and refuses any mention
of the field outside its two accessors.

**And a candidate that failed to configure was released and not closed.**
`configure` walks a fixed order and can fail at any point along it; by the last
two steps it has already taken an `ICodecAPI`, a second reference through
`IMFMediaEventGenerator` and armed the callback. `t.Release()` left all three,
and the worst is the callback: that extra reference keeps the rejected
transform alive, so a candidate we have just turned down goes on being handed
events for the life of the process with nobody reading them. The branch seven
lines below it already called `Close()`, which is what makes this legible as an
oversight rather than a decision.

`MF_E_TRANSFORM_STREAM_CHANGE` from `ProcessOutput` is not an error: the encoder
is asking for its output format to be reassigned and to retry. Without D3D the
Intel encoder asks for it immediately.

### A wrong GUID does not complain

`IMFAttributes` is a key-value store: a key that does not exist is accepted like
any other and does nothing. So a GUID written by hand with one digit out of
place **gives no error** — the setting simply never gets applied.

`MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING` was wrong for months:
`f81da2c7-b103-...` instead of `0f81da2c-b537-...`, that is, the same value with
the digits shifted and the leading zero lost. The Source Reader converted
nothing, and it did not show because the development webcam already exposes
NV12: the conversion was not needed. On one that exposes **only YUY2** nothing
opens at all, and the error, `MF_E_INVALIDMEDIATYPE` from `SetCurrentMediaType`,
accuses the requested format — that is, points exactly where the fault is not.

**And there is a run-time control, which the headers cannot give.** Two agreeing
headers say a value is transcribed correctly, not that *this* encoder knows the
key: `SetValue` on a key that does not exist answers `S_OK`. `IsSupported`
does tell them apart — **but only if one also asks it something known to be
wrong**, otherwise a uniform "yes" proves nothing about the question. Measured
on Quick Sync, adding a made-up GUID beside the two being introduced:

| key | `IsSupported` | `IsModifiable` |
|---|---|---|
| `AVEncH264CABACEnable` | `S_OK` | `S_OK` |
| `AVEncMPVDefaultBPictureCount` | `S_OK` | `S_FALSE` |
| `AVEncMPVGOPSize`, already in use | `S_OK` | `S_OK` |
| a GUID invented for the occasion | `E_NOTIMPL` | `E_INVALIDARG` |

**The last row is what makes the other three mean anything.** It is a throwaway
measurement, not a check to leave in the program: on AMD `IsModifiable` answers
`E_NOTIMPL` to things it then does perfectly well, so as a permanent guard it
would accuse working encoders.

The SDK headers are the only authority, and they must be searched in **every**
form in which GUIDs are declared, because one pattern does not find them all:
`EXTERN_GUID`/`DEFINE_GUID`, `DEFINE_CODECAPI_GUID` for codec properties,
`MIDL_INTERFACE("...")` for IIDs, and `DEFINE_MEDIATYPE_GUID` with the FourCC
for video subtypes, which does not even contain the value. All 40 of ours were
checked that way: one was wrong, and it was that one. The script lives in `cmd/`
only in the reader's memory: redoing it costs ten minutes and it must be redone
whenever one is added.

### An `ICodecAPI` VARIANT's type is not deduced from its meaning

`AVEncVideoForceKeyFrame` asks yes or no, and is declared **`VT_UI4`**. Passing
it as `VT_BOOL` the Intel encoder answers **`S_OK`** and produces nothing:
measured, twelve requests and no keyframe beyond the periodic ones. With
`VT_UI4` at 1 they all arrive, within ~120 ms.

It holds for every property: the type is dictated by the declaration, not by the
sense of the question, and `SetValue` with the wrong type **does not report
it**. The fault looks like an encoder that does not support the feature, and
there is no way to tell them apart except by looking at the type. `IsSupported`
and `IsModifiable` answer before trying, and their outcome ends in
`CodecNotes()` — they are in `applyCodecSettings` deliberately, because
discovering it at the first PLI means discovering it with somebody watching a
frozen image.

### Verifying the keyframe

`S_OK` says the request was accepted, not that it was executed: the same lesson
as the `Invoke` counter.

The pipeline watches whether the keyframe comes out within five frames of the
request. After five dropped requests it declares the encoder deaf, says so
**once**, and stops asking, so the reason for a frozen image stays written
instead of being deduced. `ForceKeyFrame` tries `VT_UI4` and falls back to
`VT_BOOL` only if the first gives an **error**: a silent refusal cannot be
caught that way.

What the encoder **declares** does not enter `CodecNotes()` and raises no
alerts, because it is not a refused setting: AMD answers `E_NOTIMPL` to
`IsModifiable` and produces keyframes perfectly well (measured: 48 ms over 24
requests). "I do not answer that question" is not a no, and treating it as one
would alarm every AMD user. The watchdog runs even without congestion control:
it is the only thing that says whether commands take effect on this machine.


### A code we have met is named, and the number stays first

`ReadSample: HRESULT 0xC00D3704` is what the log carries for one consecutive
restart after another, and establishing that the code means "the hardware
pipeline could not start" costs an hour every time it is not written down. It is
nearly always another program holding the camera — typically an older copy of
this one, stuck in its own shutdown.

The names come from `mferror.h` and not from memory, and `mfName` carries
**only the codes this repository has actually met**: every one of them is
already spelled out somewhere in this package, as a constant or in a comment, so
the table is a second spelling of what we know rather than a survey of Media
Foundation. Filling it from the header would give three hundred entries of which
eight have been seen, and **a table nobody has met is a table nobody has
checked**.

**The hexadecimal stays first and the name is added after it.** What travels
outside this repository — into a search box, into somebody else's bug report —
is the number, and a message that replaced it with a sentence of ours would have
taken away the only part that does. A code we have never met arrives whole and
unadorned, so nothing here can make one look understood.

It is the shape `describeAudclnt` already has for WASAPI, and it is worth saying
why that precedent is not simply reused: there the defect was **thirty-one call
sites that never reached a describer that existed**, and the remedy was a guard
reading the syntax tree. Here there is one funnel — every HRESULT in this
package goes through `check` — so the naming cannot be skipped by a call site,
and what is left to get wrong is the table's contents.

**And the first thing the table caught was one of ours.** `source_windows.go`
carried `hrInvalidStreamNum = 0xC00D36B4`, which is `MF_E_INVALIDMEDIATYPE`:
`MF_E_INVALIDSTREAMNUMBER` is the code before it, `0xC00D36B3`. So the
enumeration's "no more streams" return was never taken — it left by the fallback
underneath and asked all eight streams every time — and nothing failed, which is
why it sat there. **A constant spelled out twice is a constant that can be seen
to disagree with itself**, and this table is the second spelling.

**`E_FAIL` is deliberately absent**, and it is the one code here that has been
met and is not named. It arrives from `PinNativeFormat` on a size the camera
does not declare, five times out of five on a Logitech Brio 105 — but `check` is
the single funnel for every HRESULT in this package, so that note would travel
with an `E_FAIL` raised while enumerating transforms or building a D3D device.
**A generic code cannot be given a meaning by the place one of its instances was
met.** Where it is worth explaining is where it happens, and it is explained
there.
