---
paths:
  - "internal/rtc/**"
  - "internal/audio/**"
  - "internal/opuswasm/**"
  - "internal/audiocodec/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## Notifications with the page closed — written, tested and removed

**This section describes a piece that no longer exists.** The code —
`internal/push`, `sw.js`, `cmd/pat-push`, the button in the bar — was written
and deleted: it worked, and that is not the point.

**It was removed because it has no single solution.** On a baby monitor "tell me
when the page is closed" has to work because a button is pressed, not because
the right page of that browser's settings was read. A grey button that **cannot
be unblocked from inside the product** is not a feature with a warning: it is a
defect with a label. And the feature is closed for good, not postponed: ntfy,
Pushover and Telegram are technically better than Web Push — from the monitor
they are an HTTP request — and all require **an app installed on the phone**,
and there the decision is a product one. One channel remains, the banner on the
open page, which goes through nobody and works everywhere.

What was learned by paying for it, and which does not need re-measuring:

- **The notification service is chosen by the browser, not by the system**, and
  does not go through us: on the same Windows machine, same code, Edge delivered
  to `notify.windows.com` and Brave to `fcm.googleapis.com`. No Google account,
  and the code does not know who it is writing to.
- **A granted permission does not guarantee delivery.** The conditions are five
  and independent: secure context, permission, service worker, `PushManager`,
  **and a service the browser manages to subscribe to**. The last is the one
  nobody thinks of, because the other four are ours or the user's — and on
  Brave, which keeps that channel off by default, `subscribe()` answers
  `AbortError` with permission `granted` and everything else in order. A
  permission can also lapse without taking the subscription with it:
  **everything answers correctly and nothing arrives.**
- **A badly encrypted payload is accepted with a 201.** RFC 8291: the body is
  encrypted for the browser, not for the service. Hence the shape of the tests,
  which holds for anything of the kind: decrypt **from the receiving side**,
  with the derivation rewritten from the specification instead of calling one's
  own — reusing the same function would prove only that it equals itself. **Two
  implementations of the same wrong idea agree with each other.**
- **When an external service answers only one thing, interrogate it with two
  deliberately broken requests to find out in what order it checks.** Sending to
  a non-existent endpoint: our token → 410, no token → 401, garbage token → 403
  "VAPID public key must be on the P-256 curve". That is, FCM checks the token
  before looking for the endpoint, and the 410 is not indifference: it is "the
  token got through".
- **Browsers are driven with the DevTools protocol**, and that is the only
  answer to "from here I cannot see what the browser is doing". A Node process
  speaking CDP — Node has `WebSocket` among its globals since 22 — starts the
  browser with `--remote-debugging-port` and a throwaway profile, opens the
  page, presses the **real** button and reads back what appeared.
- **And the technical detail must be shown, closed.** That is the only lesson
  that survives in the product as a rule: the report was "it does not register
  the device", and the reason existed **only in the browser console** — a place
  where whoever installs a baby monitor does not look and whoever wrote it
  cannot look. The observed conditions are shown, inside a `<details>`.

## Talk-back is half duplex, and while somebody speaks the room is silent

`internal/rtc/talkback.go`, `internal/audio/render_windows.go`, the "Talk"
button. The watcher's voice arrives over the same PeerConnection that carries
the video, is decoded with the **same libopus** that compresses the microphone —
the embedded WebAssembly module carries the decoder too, so no dependency was
added — and is played on WASAPI.

**There is no echo cancellation, and that is not a shortcoming: it is the
design.** If while somebody speaks the monitor went on sending the room's audio,
the microphone would pick up the speakers and the speaker would hear themselves
come back half a second later — the doorphone howl. A real canceller is a piece
of signal processing that is not worth a ten-second feature; the silence of the
return replaces it, and it is how every walkie-talkie works. It lives in one
line of `WriteAudio`, and if it goes the monitor howls. Verified live, driving a
browser with a fake microphone: 50 packets a second when not speaking, **zero
while speaking**, and resumption afterwards.

**And one speaks at a time.** Two voices together in a room are not a dialogue,
and mixing them would mean a mixer for a case that does not arise.

### The place for the voice is laid at negotiation time

The offer carries **three** m-lines: video, room audio, and a third `recvonly`
waiting for the watcher's microphone. The page declares it `sendonly` before
answering, and from that moment pressing "Talk" is a `replaceTrack` that
renegotiates nothing.

**Without that line the m-line is negotiated `inactive`**, and `replaceTrack`
sends nothing: the button turns green and nothing is heard in the room. It is
the most insidious defect in this part, because it gives no error on either
side. The alternative — making the m-line appear when needed — would mean a full
renegotiation inside the user's gesture, with its round of ICE and its class of
faults, at precisely the moment somebody is in a hurry to speak.

**Which of the two audio m-lines it is, is said by the server**, with `talkMid`
in the offer. Deducing it from order or direction works until somebody touches
the offer, and getting it wrong would attach the microphone to the wrong track
silently.

### A box with several sources is not written, it is composed

The viewer's warning box was written by four: talk-back, the watcher's
microphone error, the voice not arriving in the room, and the status heartbeat
for non-raw audio. Each called `showWarning(text)`, that is, **overwrote**.

The symptom was small and the cause was not: pressing "Talk" showed "You are
speaking in the room…" and an instant later it vanished. It was not talk-back —
it was the status heartbeat, which runs **every three seconds** and rewrote that
box regardless. The line explaining why the room is silent lasted at most three
seconds, and exactly when it was needed: without it, the silence of half duplex
reads as an audio fault, that is, sends somebody looking for the defect where it
is not.

Now every source keeps its own message (`showWarning(source, text)`) and what
appears is the most urgent of those lit; when that one goes, the one underneath
comes back by itself. It is the same shape as the alerts at the top of the page
— whoever writes hands over the whole photograph instead of an on/off pair. The
order is `microphone`, `talk`, `raw`, and it is argued: the watcher's microphone
is their fault and now; talk-back comes before filtered audio because it
explains a silence **that is happening**, while the other is a condition that
was already there and will still be there in a minute. Verified by running the
real function, extracted with a Node process: eight behaviours, among them the
one that revealed the defect — the heartbeat coming round while somebody is
speaking must not erase anything.

### On the way out the audio engine takes part, on the way in it does not

It looks like an inconsistency with "audio is captured in WASAPI raw" and it is
not. On capture, Windows processing is the enemy, because that is where the APO
that filters crying lives. On playback there is nothing to bypass, and exclusive
mode would cost dearly: it would take the speakers, silencing the rest of the
computer for the length of a sentence, and it would bypass the volume slider
exactly when whoever is in the room would like to turn it down. **The rule is
not "always raw": it is "the audio engine takes part where it is useful".**

For the same reason the conversion is asked of the system. The engine almost
always runs at 48 kHz in floating point but can be at 44.1, and resampling by
hand would shift the pitch of the voice with no clues:
`AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM` leaves it to whoever knows how, and **our**
format is requested — mono 16-bit at Opus's rate — so one device frame is one
sample of ours. If the engine were to refuse, we say so and stop: a fallback
that is never executed is the least tested part of the program, put where nobody
could notice.

The output is opened **when somebody speaks** and closed right after: the
speakers do not stay occupied all night by a feature used for ten seconds.

### We run libopus ourselves, one module per codec

`github.com/jj11hh/opus` kept **one WebAssembly module per process** — one
runtime, one module, one linear memory — with not a single lock in the whole
package. While only the capture's encoder went in, nothing showed; with
talk-back the decoder entered from the WebRTC goroutine while the encoder was
inside, and the two overwrote each other's C stack. The error is not an error:
`__stack_chk_fail` **inside** libopus, that is, memory corruption. From outside
the symptom is different: the audio capture dies, the monitor restarts it, and
for a second the room cannot be heard — with the `mic-missing` alert appearing
and clearing. That is, **the product's worst fault, caused by the accessory.**

**A lock was the first remedy, and it was half a remedy.** It covered the defect
instead of removing it, and left standing a rule somebody had to remember: "the
only road to libopus must stay `audiocodec`". Now `internal/opuswasm` runs
libopus itself, and **every Encoder and every Decoder instantiates its own
module**: separate memories, separate C stacks, nothing to serialise. **The
difference is not performance: it is that a rule became a property of the
construction.** What is shared instead is the **compilation**, which is how
wazero wants to be used: one `CompiledModule` per process — 96 ms at start-up,
once — and one instance per codec.

Of that package's 1534 lines we used five calls, and the rest were knobs never
turned; our glue is 450 lines and speaks to **libopus's ABI**, which is
documented and stable, not to a contract somebody invented.

Three non-obvious things, all paid for:

- **The test that catches is the concurrency one, not the memory one.** The
  first version compared the buffers' bytes, and **with the single module put
  back it passed anyway**, because `malloc` hands out distinct blocks even
  inside one memory. The buffers were never the danger: the danger is the **C
  stack**, which from outside has no address to look at and shows only while two
  calls are on it together. `TestEncodeAndDecodeAtTheSameTime` with the single
  module fails three times out of three, with the exact trace from the log. A
  static test remains too — encoder and decoder must not have the same
  `api.Module` — so that a tidy-up returning to sharing says so at once instead
  of depending on how two goroutines interleave.
- **`Close` now has something to close**: since the module is one per codec, not
  closing it means keeping one alive at every capture restart.
- **The start function is `_initialize`, not `_start`.** The module is compiled
  as a *reactor*, and wazero **silently skips** a start function that does not
  exist: whoever leaves the default gets no error, they simply start nothing. It
  is the family of the GUID that does not complain.

The binary drops by 28 KB, and rightly little: the weight was not the glue, it
is wazero's ~3.6 MB compiler, which stays.

#### The `.wasm` is a blob, and it belongs to others

`internal/opuswasm/libopus.wasm` is **libopus 1.5.2** plus two hundred lines of
C wrapper, compiled for `wasm32-wasi` and taken verbatim from
`github.com/jj11hh/opus@v1.0.1`. Those wrappers exist for one reason: the CTLs
go through `opus_encoder_ctl`, which is **variadic**, and a C variadic cannot be
called from outside a wasm module.

**It is the opposite of the `.syso`**: that stays out of the repository because
`build.ps1` derives it entirely from the drawing, this one is in because without
a WebAssembly toolchain we cannot regenerate it.

**A blob must not be able to become an orphan, and the right question is two
questions.** *Fetching it again* is always possible — the module proxy keeps
versions forever — and **the hash is the line that matters**, not the address:
it is the only thing that says whether what is re-downloaded is what we are
running, and that is why it is written next to the code. *Updating it*, on the
other hand, is not: that repository has **two versions, both from May 2025**,
containing libopus 1.5.2 while upstream has reached 1.6.1. **The general rule:
of a versioned artefact one records the hash and the provenance, but the
question that decides is whether whoever produces it is still alive** — and that
is not read from `go.mod`, it is looked at.

Moving up means compiling it oneself: libopus's source, a non-variadic wrapper
for `OPUS_SET_BITRATE` — the only CTL we use — and zig or wasi-sdk. **The target
must be `wasm32-wasi`**, not a module for a JavaScript host: `libopus-wasm.dev`
ships 1.6.1 ready-made and it is useless here, because it is Emscripten —
looking at its imports, `wasi_snapshot_preview1` is absent,
`emscripten_resize_heap` is present, and `_initialize` is missing. **Of a
`.wasm` one looks at the imports: they say which host it needs, and that is the
only thing that decides whether we can run it.**

**It follows that the licence note stays, and that the generator was about to
remove it.** That code arrived as a Go module, so `pat-licenses` wrote its
folder by itself; now the module is no longer in `go.mod` but we still ship the
artefact, and `pat-licenses` enumerates **modules**. On the first run it would
have emptied `licenses/github.com/jj11hh/opus/`, and
`TestTheFolderFollowsTheDependencies`, in its opposite direction, was asking to
regenerate: **the test walks you into the defect** — obeying, the note
disappears and everything stays green on a repository distributing MIT code
without its licence. So it lives in `licenses/manually-added/opus-wasm-bridge/`.
`pat-opus` prints the version that is running, because it is not readable
anywhere else.

#### A pure-Go codec: measured, and not yet

`thesyncim/gopus` and the other pure implementations remove wasm, wazero and any
shared memory. Measured here, against libopus, on the same sweep:

| | encoding | binary | rates |
|---|---|---|---|
| libopus in wasm | 6.43 ms/s of audio | +4.12 MB | all five |
| `thesyncim/gopus` | **3.21 ms/s** | **+2.24 MB** | all five |
| `pion/opus` (master) | — | — | **48 kHz only** |

The packets decode each other's output, and the two decoders give the same
result to four decimal places. **It was still not adopted**, and the reasons are
not quality: they are **172,000 lines** of non-test Go against the 25,000 of
this whole program — not a piece that can be subsetted, because the decoder
receives packets built by Chrome, nor one that can be reviewed; its README says
of itself "Released version: none yet"; and the criterion written in
`audiocodec` — "no codec rewrite we can trust" — is not caution, it is the
observation that **we cannot do that verification**, and that code sits behind a
public URL receiving bytes written by somebody else.

**The question is to be re-asked, not filed**: the day one of these reaches a
real release with published parity, the gain is the one in the table. The
yardstick already exists and it is `pat-opus`, which decodes with `pion/opus`,
that is, with something we did not write.

### A lock is not held across opening a device

`acquire` opened the audio output **holding the talk-back mutex**. `Active` goes
through that mutex, and `WriteAudio` consults it **for every audio packet**, as
does the status page, which the tray interrogates every second: holding it
across a call into WASAPI means giving a driver the right to stop everything
that touches it. In the normal case that is the 313 ms of the open, that is,
fifteen audio packets lost at every "Talk"; in the bad case it is a driver that
does not return, and then it is a stopped monitor with the camera on.

Now the floor is reserved, the open happens **outside** the lock, and the result
is recorded on re-entry; an `opening` flag keeps the second packet out, because
fifty packets a second would otherwise open fifty audio outputs. Covered by
`TestOpeningTheOutputDoesNotBlockTheRest`, which opens with a fake device that
never returns and checks the state answers anyway.

**And that test hung for ten minutes, through a defect of its own.** It launched
the first `acquire` in a goroutine and moved straight to the second, assuming
the first was already inside the open; on this machine the order is regularly
the other one, and the code was doing exactly its job — the `opening` flag
turned the first away — while what was spinning was the test. Hence the rule,
which holds for every test with two goroutines: **an order is waited for, not
assumed.**

### An output that will not open is not requested fifty times a second

Whoever speaks sends a packet every twenty milliseconds, and the first version
asked for the device at each one. Where the output is absent — and it happens:
inside a Remote Desktop session `IAudioClient` answers `E_INVALIDARG`
**immediately** — that becomes fifty failed COM calls a second, fifty identical
log lines, and a monitor working for nothing instead of watching the room.
Reproduced live by connecting over RDP and pressing "Talk": the log filled at 50
lines a second.

Now after a refusal it waits five seconds. **It is a pause, not a sentence**:
whoever plugs the speakers in while the monitor runs must be able to speak
without restarting anything. Two lines instead of hundreds, measured on the same
test. **The defect is of the family this project knows**: an operation that can
fail, put in a loop governed by somebody else — and the retry cadence cannot be
dictated by the sender.

### An HRESULT other than zero is not automatically a fault

`go-ole` returns an error for **any** non-zero HRESULT, and `CoInitializeEx` has
two that mean success:

| outcome | meaning | what to do |
|---|---|---|
| `S_OK` | initialised now | balance with `CoUninitialize` |
| `S_FALSE` (1) | it was **already** initialised here | balance it anyway |
| `RPC_E_CHANGED_MODE` (0x80010106) | the thread is in another apartment | proceed, **without** balancing |

`comThread` treated them all as faults, and the consequence was worse than a
reported error: **`fn` was not called at all.** Whoever was waiting for a result
produced from inside `fn` waited forever, with talk-back holding a lock that
`WriteAudio` goes through. It is the lesson of "weigh the effect, not the
answer", reversed: **here an answer that said yes was read as a refusal.** Hence
the second half of the remedy, which holds regardless: `NewPlayer` never waits
forever — five seconds and talk-back declares itself unavailable, because an
accessory used for ten seconds cannot have the power to stop a monitor that must
watch all night.

**And that reading was written four times, two of them wrong.** The two wrong
copies were also in the two worst places, and that is not a coincidence: the
correct ones were where the fault had already been paid for. In `runVideo` an
`S_FALSE` would have exited before opening the camera, that is, a blind monitor
with a diagnosis accusing the webcam; in `pat-diag` it would have switched the
tool off before the format list, which is the only thing that says what should
have been asked for when the open fails. `runVideo` also carried the other half:
an unconditional `defer ole.CoUninitialize()`, which with `RPC_E_CHANGED_MODE`
would release somebody else's reference — it did not fire only because the
`return` preceded it, that is, **the two defects masked each other.**

The reading now lives in `internal/wincom`, and there are two functions because
the callers are of two kinds: `Init` for whoever keeps the thread themselves,
`Thread` for whoever wants one. **What the package does not decide is the
apartment**: video in MTA because asynchronous transforms deliver from an
internal queue, audio in STA because that is where WASAPI objects expect to be
born — those two reasons live where they are chosen.

**And centralising had removed a distinction without saying so, which is how a
tidy-up makes things worse.** The four copies disagreed on a second point: what
to do when the thread is **already** in a different apartment. `internal/audio`
and `internal/mf` carried on, and rightly; but `runVideo` must **refuse**,
because finding itself in STA means leaving the asynchronous transform queue
without the loop it wants — the result would not be an error but a silent hang.
The first shared version was tolerant for everyone, that is, it had **traded a
readable error for a hung monitor with the camera on**. Hence `Sharing`, which
is a parameter and not a default value: `Required` is the type's zero, because
whoever has no reason to write `Accepted` must choose well by omission.

**The tests open COM for real, and the first version did not.** It looked
rigorous to interrogate the classification with the three codes in hand, but the
balancing test was asking the same thing under another name, and **putting
`CoUninitialize` back on the other-apartment branch it stayed green**: it
absolved exactly the defect it was named after. The path can be constructed —
pin the thread, put it in STA, and from there every request for MTA finds an
apartment that is not its own — and that `release` did not balance is observable
from outside in one way only: a fresh `CoInitializeEx` must answer `S_FALSE` and
not `S_OK`. The count is now kept by `TestNobodyReadsTheOutcomeOnTheirOwn`,
which reads the tree and refuses any `CoInitializeEx` or `CoUninitialize`
outside that package — **removing comments before searching**, because the
comments name them deliberately.

### Only what one has is written, never filler silence

Filling the free space of the buffer with zeros looks tidier and does the
opposite: the silence queues **in front of** the voice, and the sentence comes
out half a second after being said, for the whole session. In shared mode an
empty buffer produces no defect at all — the engine mixes what is there, and
from us there is nothing.

For the same reason, when the queue exceeds 400 ms **the old is thrown away, not
the new**: whoever is listening wants what is being said now.

### A ceiling the next packet can get round is not a ceiling

The floor expires on silence after two seconds and on duration after two
minutes. **The second, as first written, limited nothing**: the floor was taken
away at two minutes and the next packet — arriving twenty milliseconds later, if
the microphone stayed on — took it back. The room would have stayed mute all
night in two-minute blocks, that is, exactly the case the ceiling exists to
cover.

Whoever exhausts the duration therefore stays out **until they really stop**,
and the silence is measured on their packets, which go on arriving even while
the floor is refused: it is the only way to tell "they have finished speaking"
from "they left the microphone on". **The time is counted by the monitor, not by
the browser**: a page that closes abruptly, a network that drops, a phone that
locks — in all three the packets stop and nobody says so.

### "Written into the buffer" is not "it was heard"

The playback path can accept the format, consume the queue and throw nothing
away while producing no sound at all. From inside the program the two cases are
identical — it is the same question as the microphone delivering zeros, from the
other side of the room.

WASAPI has the answer: open a **capture** client on a **render** endpoint with
`AUDCLNT_STREAMFLAGS_LOOPBACK` and the mix the engine is playing arrives. It
lives in `pat-wasapi -out`, together with the endpoint volume, which is the
third cause of "it cannot be heard" and the only one the program cannot
compensate for.

**The verdict has three values, and the third is the one that matters.** The
first version had two and declared "nothing" whenever the tone did not emerge:
on this machine, where something was playing at -36 dBFS and the output volume
is at -35 dB, it accused the driver of a fault that was not there. **Above a
noisy floor one does not conclude**, and the threshold is on the floor and not
on the rise — no tuning of the rise fixes a covered tone. Measured in a silent
room: tone 52 dB above the floor, zero samples dropped, output opened in 313 ms.

