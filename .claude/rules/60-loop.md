---
paths:
  - "internal/rtc/**"
  - "internal/mf/**"
  - "internal/media/**"
  - "internal/encoder/**"
  - "internal/pipeline/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## The loop: what the monitor decides, and in what order

The scheme of how it works **now**. The chapters that follow say where each
number came from: they are for when you want to change one, not for
understanding the design.

One loop. It runs **once a second**, and only when at least one viewer is
connected.

### The sensors

**Bandwidth**, with two voices:

- the gcc **estimate**, that is, what the network claims it can carry. It is in
  transport bandwidth: subtract the 64 kbit/s of audio and 5% of headers to get
  the video's. With several viewers the worst one counts, because there is one
  encoder;
- the **losses** declared by the receiver. They are not an opinion about the
  network: they say what happened to our packets, and they come before the
  estimate. Above 10% they bring things down on their own, even if the estimate
  says all is well.

**Quality**: the quantiser of the image coming out — p90 of the last second.
Higher means more approximated.

**Motion**: whether an episode has just begun in the room. It is the only one of
the three that speaks **before** the encoder — the other two describe things
that have already happened — and it does not decide a value: it revokes the
bitrate discount, and that is all. It does not go through the `detect_motion`
switch, which concerns what to notify and not what quality the video should
have.

### The levers, in order

The ceiling is the preset: `high` = 720p at 30 fps, 2500 kbit/s, in CBR. Only
the network can lower it.

1. **Network ceiling** = the lesser of the preset and the estimated bandwidth.
2. **The bitrate chases the quality**, inside that ceiling. Target `target_qp`,
   30 by default: if the image comes out **better** than the target those bits
   are not visible and are removed; if it comes out **worse**, bits are bought,
   up to the ceiling. Floor at 300 kbit/s, and five seconds of wait after every
   move. At the start of a **motion** episode the loop gives its discount back
   in one go, without waiting: the premise of the saving is that the room is
   still.
3. **The pixels**, and only when the bits have run out: if we are already at the
   ceiling and the image is broken anyway, drop one resolution step.
4. **The frames**, only below the last size: **5 a second, then 2**.

To **climb back** in resolution two conditions are needed together: 40% of
margin over the bandwidth the step above requires **and** the quality back near
the reference. One step at a time, minimum twenty seconds.

### The rules that always hold

- **First buy bits, then remove pixels.** A bitrate increase is not visible, a
  change of size is.
- **"I do not know" is not "zero".** With no estimate, or no readable quantiser,
  stay at the ceiling: the saving is lost, never the image.
- **An estimate that is not at least 15% below what we are producing is an echo
  of our own throughput**, not a measurement of the network, and brings nothing
  down.
- **The audio is never touched**, under any condition.

### The asymmetries

| | down | up |
|---|---|---|
| network bitrate | at once, on the estimate | after five seconds of a high estimate |
| quality bitrate | half a step | a whole step |
| resolution | at once, down to the right step | one at a time, with 40% margin |
| step anchor | the measured throughput | the greater of throughput and request |
| climb on motion | does not exist | at once, and up to the ceiling |

Underneath it is always the same reason: **erring downwards costs sharpness,
erring upwards costs lost packets** — that is, a stuttering image, which is the
fault that shows.

### The two things chosen by measurement, not by brand

**How the bitrate is changed.** The encoder is asked for one thing, a bitrate in
CBR. Start with the simple command, which can interrupt nothing; weigh the bytes
coming out; if the throughput did not follow, move to rebuilding the encoder,
and from then on stay on that road. The question is asked once deliberately with
a probe, and then goes on being asked: `bitrateSeen` watches every window for
the whole session.

**Where the quantiser is read from.** From the sample attribute where the
encoder declares it, from the H.264 stream where it does not — and in no case
does a reading enter before it has changed value at least once, because a
constant number is not a measurement.

No vendor list, in either case: the next user has a chip we have never seen.

### The two quality numbers, and why they are two

**Thirty** is the loop's target: the quantiser at which the image should come
out, and below which the bits are not visible. It is changed with `target_qp`,
and at zero the saving is switched off.

**Thirty-eight** is the threshold beyond which the resolution drops: eight
points above, which by specification is two and a half times fewer bits per
pixel. It is a fixed number and does not depend on `target_qp`, because whoever
switches off the saving must not also switch off the protection.

### The cadence is declared, not assumed

The encoder divides the budget by the frames it believes it will receive, and in
low light auto-exposure brings the camera down to 9-20 fps:

| declared | real | produced on 2500 | QP |
|---|---|---|---|
| 30 | 18.4 | 1598 (64%) | 27.9 |
| 15 | 13.7 | 2312 (92%) | 24.3 |

That is, at night a third of the allowed bandwidth and three and a half points
of quality were thrown away. The GOP follows from it too, being kept in seconds
but translated into frames with the declared cadence: at 9 fps keyframes arrived
every 6.6 seconds instead of every 2, and whoever opens the page waits.

The declared cadence therefore chases the measured one over a few fixed steps
(`cadence.go`), and **the asymmetry is the opposite of the instinctive one**:
declaring **more** than reality costs only waste, declaring **less** overshoots
the ceiling and loses packets. So it rises in two seconds and falls in ten, it
rounds **up**, and the cadence the scale imposes always commands, at once.

**There are two cadences**: the one **delivered** — how many frames the gate
lets through, which is what the scale decides — and the one **declared** to the
encoder. In the dark they differ by construction: the gate is wide open at 30,
the camera gives 9.8, and 10 is declared without dropping anything. With one
number it was the declaration that commanded the gate, and the declaration is
computed from the **measured** cadence, which with the gate closed is ours and
not the camera's: **the loop closes on itself.** The log showed **six minutes at
two frames a second with the bandwidth back at 2696**, and the resolution
meanwhile climbing back to full 720p — that is, `1280x720@2`, a state that by
specification **does not exist**, and which from outside is the symptom of a
jammed camera.

The remedy is in two parts, one per side: **the gate is commanded by the scale,
never by the declaration** (`videoFormat` carries two cadences instead of one),
and **a rising cap frees the declaration** (`cadenceGovernor.capFPS`) instead of
inheriting a measurement that by then describes us. The cap already commanded on
the way down, and that is the half written first because it is the obvious one:
**between the two directions, the one that closes the loop is always the one
that does not get written.** Covered by `TestARisingCapFreesTheCadence` and its
descending twin.

**Verified live** on Quick Sync, covering and uncovering the lens: the declared
cadence chased 9.8 → 24.2 → 16.1 → 10.0 in twenty-six seconds, with the climb in
3 seconds and the descents in 13 and 10, and **never below the measured one**,
which is the dangerous direction. Those four changes are **four encoder rebuilds
in twenty-six seconds** with no capture restart: on AMD the same manoeuvre has
already killed the capture, so the figure holds for Quick Sync and is not
general.

### Quality drops only if the network forces it

The preset chosen by the user is a **ceiling**: congestion control can only
lower it, and no other logic may lower it on its own initiative — least of all
"because the viewer is remote". A quality reduced by choice, if it is ever
wanted, will be an explicit saving mode asked for from the interface.

`RunBitrateControl`, in `internal/rtc/bitrate.go`. **Come down fast, climb back
slowly**: staying too high costs lost packets, that is, a stuttering image;
staying too low costs only sharpness.

- **The asymmetry is in the wait, not in the size of the step.** Climbing by a
  quarter of the gap at a time took **2 minutes 40** to reach the 2.4 Mbit/s the
  link carried from the first second, and on a baby monitor one watches for half
  a minute: the whole glance fell inside the ramp. Worse, it fed itself — gcc
  measures only the packets that pass in front of it, so **by sending little the
  estimate comes down to chase us**. Once the wait has passed, go *where the
  estimate says*, which gcc has already granted.
- **The return to the ceiling is exempt from the dead band.** Near the ceiling a
  fractional step becomes smaller than the threshold that absorbs it, and it sat
  at 2150 out of 2500 forever: it is the last command before standing still.
- **On radio, delay does not measure the queue, it measures the radio.** The gcc
  delay branch on fixed networks anticipates a filling queue; on cellular it is
  produced by radio schedulers, retransmissions and cell changes — measured, an
  estimate between 647 and 1290 within the same minute with **zero losses** and
  a quantiser of 19-23, on a link that then carried 2.4 Mbit/s. Following every
  dip produces a **ratchet**: 950 → 700 → 600 in twelve seconds on a healthy
  network. A descent is believed only with direct evidence, and there are two:
  **losses** (below 1% it is not news) and **a collapse**, that is, an estimate
  cut to less than half. The `believableDrop` threshold sits **between two
  measured phenomena** rather than at a value chosen for symmetry — radio noise
  at 50% of current, real collapse at 12%.
- **Losses must be able to bring things down on their own.** Consulted only
  inside the "the estimate is below the current bitrate" branch, they could
  **confirm** a drop and never cause one: an iPhone declared `estimate=2696
  cap=2500 lost=89.8%` with the bitrate stuck at 2500 and RTT 1.7 s, dying and
  reviving four times. Above **10%** it comes down without asking the estimate's
  permission, removing half the lost fraction — threshold and factor from
  libwebrtc's loss-based controller.
- **Only the video's report block is video loss.** With BUNDLE one receiver
  report can carry a block per stream, and every block used to be counted: audio
  lost on the way read as the picture being too big for the link. The block
  names its SSRC, so `aboutTheVideo` asks — and with no SSRC to compare every
  block still counts, because a missing number is not "nothing was lost".
- **The worst loss must expire, and reports from those who are fine must not
  keep it alive.** `recordLoss` recorded the instant of the last report
  **received** instead of the value **held**: the PC's zeros refreshed the
  window of the phone's loss, nailed at 89.8% even after the loser had left.
  With loss-commanded descent it would have become a monitor stuck at the
  minimum forever — **the two defects masked each other.**
- **An echo does not bring things down, but it remains a lower bound.** A still,
  dark room compresses beautifully, so the estimate settles onto our own
  throughput: it is compared with the **measured** throughput
  (`estimateIsCredible`, both in video bandwidth) and taken as a limit only if
  it sits at least 15% below. The two governors are spoken to differently,
  though: the bitrate is passed the current value, which for it is a do-nothing;
  the scale is **always given the real estimate with the judgement alongside** —
  giving it a zero pinned it at 640x352 for a whole session, because if gcc
  declares 2696 while the encoder produces 35 there is no doubt there is room
  for more pixels. Shortfalls are counted only on credible estimates, otherwise
  they accumulate silently through the whole saving. Covered by
  `TestForTheScaleZeroMeansNotKnown`, `TestAnEchoDoesNotBringItDown`,
  `TestItClimbsBackEvenWithAnEstimateThatEchoesUs`,
  `TestNonCredibleShortfallsDoNotAccumulate`.
- **The estimate of the first seconds is not an estimate**: eight seconds of
  warm-up per viewer, and the absence of an estimate is not an estimate of zero.
- **The estimate is the transport's, not the video's.** `BindLocalStream` hands
  the estimator every local stream, audio included, plus headers and
  retransmissions: the 64 kbit/s of audio and 5% of headers are subtracted, and
  the limits given to gcc stay in transport bandwidth, otherwise the video would
  never return to the preset.

### Of a command to the encoder, weigh the effect, not the answer

**Quick Sync accepts `SetBitrate` and does not execute it**, and declares
`IsModifiable` as `S_OK` too. Measured with `pat-capture -br 2500 -br2 800`:
Intel **2418 → 2375**, Windows **software** encoder **2413 → 829**, with the
identical call. It is neither ours nor this machine's: documented since 2013
(alax.info/blog/1823, same DLL `mfx_mft_h264ve_64.dll`), and Intel confirms the
MFT is *fixed function* — underneath is Media SDK, where the bitrate is changed
**only** with `MFXVideoENCODE_Reset`, a road the MFT does not take. Chrome does
what we used to do, so on this hardware it has the same defect: **two programs
can get the same thing wrong.**

At start-up the number takes effect exactly, hence the second road:
**reconfiguring the encoder**, that is, stopping it, reassigning its output
format and restarting it. **The two roads are complementary, and that was not
predictable:**

|                  | `SetValue`    | reconfiguration |
|------------------|---------------|-----------------|
| Quick Sync       | 2418 → 2375   | 2431 → **746**  |
| software encoder | 2413 → **829**| 2403 → 2407     |
| AMD              | 2206 → **894**| 2267 → **738**  |

Reconfiguration is **not a universal remedy**: the software encoder accepts and
ignores it, with the same `S_OK` with which Quick Sync ignores the other. On
**AMD both work**, and there moving to the expensive road is never needed — on
that chip it has already interrupted the capture.

The consistency is therefore in the **rule**, one for all: try the road that
interrupts nothing, weigh the frames coming out, move to the other only if the
first had no effect, and from then on stay there (`bitrateSeen`, twin of
`keyframeSeen`). The cost of not having the second road is measurable: from
cellular, gcc asked for **300 kbit/s for twenty seconds** while the encoder
produced 2500, and the link lost **33% of packets**.

A stop and a restart of the transform remain, which is why it is not the first
choice. A request issued before the flush arrives after it and `ProcessInput`
answers `MF_E_NOTACCEPTING`: that is the prescribed answer, and the frame is
skipped (`ErrNotAccepting`) — treating it as an error would switch off the
capture at every bitrate change.

**Two fears were disproved by measurement**: it **does not cost a keyframe** and
it **does not change the `level_idc`** — the worse of the two, because the SDP
declares one level, fixed at the offer, and a transform that emitted another
would stop matching the decoder the browser has already set up, silently.
Measured again on all three vendors, forcing the road against the command road
in the same session:

| | keyframes, command → reconfigure | levels seen |
|---|---|---|
| Quick Sync | 10 → **10** | `42e01f`, one |
| NVIDIA | 15 → **12**, twice | `42e01f`, one |
| AMD | 15 → **15**, twice | `42e01f`, one |

No extra keyframe anywhere; on NVIDIA it emits fewer, which is the frames
skipped around the flush. One AMD pair read 14 → 20 and did not reproduce in
three more whose frame counts were within four of each other: an outlier,
recorded as one rather than explained.

**They are not guarantees, and the test is a command.** Whoever touches
`ReconfigureBitrate` reruns it — `pat-capture` collects every `profile-level-id`
it sees and fails on more than one:

	pat-capture -br 2500 -br2 800 -brmode reconfigure

**And "rebuilding the encoder" means two different things here**, which is why
this paragraph names the road. The resolution scale rebuilds it too, and there
the level really does change — `42e01f` to `42e016` at 640x352 — which is the
reason the announced level is frozen on the preset instead of chasing the
stream.

#### The probe exists because the evidence does not arrive on its own

`probeBitrate`. As soon as the capture starts, and **once per process**, half
the bitrate is requested and the bytes are weighed for four seconds. On Quick
Sync: 1250 asked for, throughput from 2520 to **2512**. Zero.

**It is needed because a ratio does not tell the two encoders apart, and the
step that does tell them apart never happens in service.** The original
criterion accused whoever produced more than requested, and caught Quick Sync
(1.25) but also AMD, which obeys with a tuning a third wide (1.20): the two
signatures overlap. Replaced with "does the throughput follow?", the reverse
emerged: to answer, a big step is needed, and the quality loop on this chip
never makes one, because the quantiser sits against the target and asks for
between 77% and 100% of the ceiling. **The watchdog stayed silent by
construction.**

Instead of waiting for that step, it is made. Three properties make it harmless
and all three must be kept: **once per process** and not per capture restart,
otherwise it would be half the bitrate at every backoff; **before anyone is
watching**, because the capture starts with the program; and **the bitrate is
put back** through `SetBitrate`, that is, by the road just chosen. Halving sits
well above the measurement noise, which between nearby windows reaches 20-30%:
it is the largest step that can be asked for without making the image
unrecognisable, and it is what makes the answer clean rather than statistical.
On the wire, measured from a real viewer: **629 kbit/s against the 2481** of the
same test without the probe.

**And "once per process" has a second half that was missing: an interrupted
probe is not a probe.** The flag is set by the caller **before** the goroutine
starts — it has to be, or two overlapping sessions would both probe — so an
abort that left it set means the question is never asked again for the whole
process, and what is left is `bitrateSeen`, which on this chip stays silent by
construction. The flag is now given back on the way out, and what the probe had
lowered goes back with it: the value in force is what a rebuilt encoder starts
from, so an abort between the two measurements would hand the next encoder half
the preset with nobody having asked.

**It became reachable by a change somewhere else**, which is the part worth
keeping: the capture's context became **per-session**, so that choosing a camera
can close it, and this goroutine was holding that context. Before, it held Run's
and outlived every restart — which was not right either, because it would then
weigh bytes across a gap. **Aborting is the correct half, and re-arming is the
half that had no reader**: the lifetime of a context is a decision, and a
goroutine that outlives the thing it measures is one of its readers. Nothing
failed, no value was wrong, and the only symptom would have been a machine that
never asks the one question that tells a deaf encoder from an obedient one.

#### The watchdog's signature is "does not follow", not "sits above"

- **A refusal is not always conspicuous.** With requests dropping 7% at a time
  no single step overshoots by 40%: target from 1988 to 1848 while the encoder
  produced 2400 without moving, and the watchdog silent. **Modest, repeated**
  overshoots are counted too, above 15% for three checks.
- **But the signature is not "it is above every time".** AMD obeys and is above
  always, with a tuning about a third wide: a constant **gain error** is not
  deafness, and the quality loop absorbs it on its own because it closes on the
  bytes produced. The real signature is throughput **still** while the target
  slides: not "sits above", but **does not follow**.
- **The highest request in the window is kept, and the direction was wrong.**
  The window is not cleared at every command, otherwise with commands every two
  seconds and a four-second check it never closes; and in a descent the lowest
  request arrived an instant ago, while the bytes were produced by the previous
  one — 743 produced against 523 asked gives 1.42, but 743 against the previous
  803 is obedience. It was declared deaf for having obeyed the previous order,
  **by construction**, in every session where the quality loop did its job. **A
  false alarm moves the machine onto the risky road forever; a missed alarm
  delays the diagnosis by one window.** Covered by `bitratewatch_test.go`.
- **An encoder is not judged while it is answering.** After a command the frames
  in flight come out at the old bitrate: right after a descent from 2500 to 1900
  the throughput was still 2628. `bitrateSettle` skips the transient — more than
  one GOP — and concedes nothing to an encoder that really does ignore commands.
- **And the instrument that checks all this had the opposite defect, found by
  the first NVIDIA machine.** `pat-capture`'s hot-bitrate verdict asked whether
  the throughput **landed near** the number requested, while the comment above
  it stated the rule correctly: moved towards it by at least half the gap. The
  two differ exactly where an encoder **overshoots**, and there they must, since
  overshooting is obedience. Measured: asked 800 with a camera delivering half
  its nominal cadence, the throughput went 1113 → 444, and the tool printed
  *congestion control on this machine is a fiction* about an encoder that had
  just cut its output by 60%. `bitrateTookHold` asks the one-sided question now,
  and declines to answer at all when the new request sits above what was already
  coming out — which is `probeVerdict`'s guard, in the tool that was supposed to
  be checking for it. **A wrong measurement always accuses somebody else**, and
  this one accused the only vendor nobody had ever run.

### Constant quality is done inside the encoder, not around it

In CBR the encoder spends the whole budget whatever there is to film: measured,
**2382 kbit/s for a still room that at 979 sat at the same quality** (QP 28.8
against 30, indistinguishable). On a baby monitor that is the opposite of what
is wanted — the room is still for 99% of the night and moves in the 1% that is
the only moment that matters.

**An outer loop was tried and failed**, and it is worth remembering because the
idea was right and the shape was not. The design was: the network computes a
ceiling, the quantiser computes a need, send the minimum. Three faults in
sequence, each corrected and followed by the next — oscillation 300 ↔ 2500 every
two seconds, descent into the void (the need anchored to the request instead of
to the measured throughput), slow oscillation from a fixed step against a sensor
that answers a second late. Underneath there was a **structural** one that no
tuning could solve: the reference quantiser is learned only by sitting at the
ceiling, and that loop prevented returning there — after a few minutes the
reference was frozen on an old scene and the bitrate stayed at **834 kbit/s
while the network granted 2000**. The loop starved the calibration that fed it.

**And it is no accident that nobody does it.** libwebrtc gives the encoder the
estimate's bitrate and uses the quantiser **only** for resolution and cadence;
the rest of the world gets constant quality with *capped CRF*, that is, quality
and ceiling together **inside** the encoder in a single control. The reason is
what we measured: the encoder already has a loop, and wrapping a second one
around it puts two controllers in a fight.

### The bitrate chases a quality, and it is the only loop

The rule fits in a line: **lower the bitrate only if the quality is in excess**,
that is, only when the image comes out better than necessary and those bits are
not visible. On a difficult scene the condition never holds, so nothing comes
down: the protection is in the rule, not in a tuning.

`internal/rtc/quality.go`. Measured live on AMD with a real viewer, on a still
room:

```
asked 2500 → 1655 → 1106 → 737 → 456 → 300     quantiser steady at 23
then it settles between 300 and 353            quantiser 23-27
```

**Seven times less bandwidth without one point of quality lost.**

Four non-obvious things:

- **The encoder is asked for one thing: a bitrate, in CBR.** No special mode,
  nothing to verify chip by chip: it is the lowest common denominator of every
  encoder, and it is also the only command seen to take effect on different
  hardware.
- **The loop closes on the bytes going out, not on what we asked for.** Measured
  on AMD: 2500 asked, 2800 produced. Anchoring to the request cuts a number
  unrelated to reality; anchoring to the throughput, **how much the encoder lies
  stops mattering**.
- **The step is computed, not chosen**: six points of quantiser are a doubling
  of bits by specification, so the deviation from the target gives the exact
  factor. Whole steps up and half steps down, the usual asymmetry.
- **The floor is `bitrateFloorKbps`.** Below it the right answer is not to
  remove more bits but to send fewer pixels: without that limit the loop went
  down to 125 kbit/s and oscillated there — 125 → 213 → 152 → 125 → 435 —
  because at that bitrate a keyframe every two seconds dominates the window and
  the quantiser read jumps between 23 and 40. The loop was not unstable: the
  measurement down there is no longer a measurement.

**And there is an interaction that killed the capture four times in eighty
seconds.** The encoder watchdog weighs bytes over four seconds; commanding every
two, every window contained a change, and coming down 631 → 372 → 300 it
compared 300 with the 553 produced when we were asking for 631, concluded "the
encoder does not obey" and moved to reconfiguration — which on AMD produces
`ProcessOutput: 0x8000FFFF` and **interrupts the capture**. The loop must
therefore speak more slowly than the other one can listen: `qualitySettle` sits
above `bitrateVerifyAfter`, and the two must be looked at together. It is **an
encoder is not judged while it is answering** applied to two pieces of ours
instead of to the hardware. Spacing the commands out was the symptomatic half,
though: the cause was the comparison with the lowest request of the period, that
is, with an order that arrived after those bytes had already left.

#### The discount only exists at full size, and the QP window is two GOPs

Two defects with the same root: **the loop was watching a quantity that
described itself.**

**The first: the saving starved the measurement that was to end it.** The
premise of the saving — "the image is already as we want it, so these bits buy
nothing" — is false as soon as the scale has **shrunk** the image: there the
extra bits buy **pixels**, which is precisely what the scale is waiting for. And
gcc measures only the traffic that passes in front of it, so by sending little
the estimate chases our throughput and the scale climbs back only if the
estimate covers the step above. The loop closes like this: the scale comes down
→ the scene becomes easy → the quantiser falls below target → we cut → the
estimate can no longer rise → the scale does not climb back. Nine minutes in one
session, with the viewer on 5G:

| | |
|---|---|
| bitrate commands | 74, one every 7.5 s |
| bitrate asked | median **576** kbit/s |
| quantiser | median **28**, i.e. better than the target of 30 |
| estimate | median 758, **maximum 1487** |
| to climb back to 720p | **1547** |

Four minutes stuck **sixty kbit/s below the threshold**, 4%. And the proof that
the network was not to blame is in the same log: as soon as the viewer reopened
the page the estimate reset, we sent 2237 kbit/s and gcc measured **2253 on the
same radio**. **Traffic comes first, the estimate follows it.**

The remedy is one condition: **below full size the bitrate is the ceiling**,
that is, the lesser of the preset and the bandwidth. The byte ceiling, the
descent at QP ≥ 38 and the climb with margin all stay active. It was preferred
to a *floor* equal to what the step above needs because in the real case the two
coincide, and the second costs a new number and a coupling between two
governors.

**The second: the quantiser alternates with the GOP, and the loop chased it.**
That number is the p90 of **one second** and the GOP lasts **two**, so one
second in two contains a keyframe:

	qp  28  28  32  28  32  27  28  28  33  27  28  28  32  28  28  33

None of those values is in the dead band, which is one point, and the average is
29.5 — that is, **the target falls in the middle of the alternation**, and the
loop chased a value the quantity it read never touched. Those are the **74
commands** in the table, and on a machine where the bitrate is changed by
rebuilding the encoder every command **flushes the transform**, that is, throws
away the frames in flight: those are the micro-stutters one sees while watching.
The window is therefore two GOPs here too.

**But the averaging lives in the loop and not in the pipeline**, and that is the
decision that matters: two readers want two different things from that number.
The **scale** needs the instant — a bad second must be caught at once, and its
threshold is 38 — while **this loop** needs the stretch, because it decides how
many bits to buy for the scene. **Damp where you decide, not where you
measure.**

**And "sitting at the ceiling" is a loop too.** Written the first time it reused
`byteCeiling`, whose guard asks "does what is coming out exceed the ceiling?" —
and there that answer is the **consequence** of the correction: as soon as it
works, the guard judges it useless and takes the request back to the ceiling,
the encoder overshoots again, and round it goes. Measured with an encoder
producing 1.66 times the request: **19 commands in 30 turns**, cycle 2500 → 1672
→ 1098 → 851 → 2500 forever. Asking instead "how much **would come out** if I
asked for the ceiling?" the fixed point exists: two commands in thirty and then
still.

**And the window does not survive a change of size.** Crossing it, it carried
four samples of an easier scene: the first turn at full size cut **2500 →
1512**, that is, removed bits at the instant the scale had just asked for them.
Below full size nothing is collected — the same reason as `scaleSettle` on the
other side — and the four seconds it takes to fill it are spent at the ceiling,
which after an enlargement is where one wants to be.

Covered by `TestTheDiscountOnlyExistsAtFullSize`,
`TestSittingAtTheCapDoesNotOscillate`,
`TestSittingAtTheCapIsSilentWithAnHonestEncoder`,
`TestSittingAtTheCapRespectsTheFloor`,
`TestTheQuantiserWindowDoesNotSurviveASizeChange`,
`TestTheByteCeilingStillHoldsBelowFullSize`,
`TestAnAlternatingQuantiserCommandsNothing` — which uses the real sequence from
the log — and `TestASustainedRiseStillCommands`, which is the half not to lose:
the window must not make the loop deaf.

#### Four numbers that spoke of a moment already over

Four in this governor, and they are the same defect four times: **a number held
in memory went on answering after what it described had ended.**

- **A missing reading ages the window.** `TakeRecentQP` answers `!ok` more often
  than it seems — capture restart, a second without samples — and with a full
  window the **previous** average went on answering: with an obedient encoder
  and a stalled average below target, **1512 → 1200 → 952 → 756 → 600, down to
  the floor, without a single reading**. Now a missed reading removes the oldest
  sample: an isolated gap costs one tick at the ceiling, four in a row empty the
  window.
- **The byte ceiling is decided by a projection, not by a measurement.** The
  guard asked "does what **comes out** exceed the ceiling?", a question that is
  right only where the request in force is already the ceiling: elsewhere it
  never fires, because under a discount the throughput is below the ceiling by
  definition — **the motion jump was never limited**, and with an encoder
  producing 1.66 times the request it put **4150 kbit/s on a ceiling of 2500**.
  The question is now one for all: **how much would come out if I asked for
  this?** The real cause was a **copy**: `atCap` already had that projection,
  written inline, and the two versions diverged in the usual way.
- **The tick that enlarges still carries the previous quantiser.** The scale
  writes the new size **inside** `scale.target()`, which the loop calls before
  this governor: `atFullSize()` is already true while that turn's number was
  produced by the small image, and the window restarted with a sample of an
  easier scene — a cut of **2500 → 2138** four seconds after the climb. The
  first good sample is the next turn's; a new governor, on the other hand, is
  born at full size, otherwise the first sample would be thrown away at every
  start.
- **And the discount does not outlive whoever had it.** With no viewers
  `release` puts the encoder back at the ceiling and this governor is not called
  at all: `current` and the two windows stayed those of the last session — A
  leaves with a discount at 1191, B arrives ten minutes later on a scene exactly
  at target, where the right answer is **no command**, and the first tick asks
  for 2102. And until it realigns, `current` says 1191 while the encoder sits at
  the ceiling: the projection reads that gap as an encoder producing 65% more
  and cuts.

**And there was a fifth, one floor up, which would have brought the fourth back
for good.** All three `release` functions are called from one place — `viewers
== 0` in the loop — and `viewers` is not counted: it is `len` of the estimate
map, read by `worstEstimate`. That map was written **as soon as the Viewer
existed**, with five error returns still to come below it — the two tracks, the
talk-back transceiver, the offer, `SetLocalDescription` — and the only thing
that removes an entry is `Viewer.Close`, which on those paths nobody can call,
because the viewer is never handed to the caller.

One failure, once, and **`viewers == 0` never happens again for the life of the
process**: the three releases stop being called, and every viewer from then on
inherits the discount, the quantiser window and the estimate of whoever left —
that is, the fourth case above, made permanent. And the estimate is taken as a
**minimum**, so a dead viewer's last one, frozen, holds the encoder down for
everybody who follows.

The repair is positional rather than five cleanups: **the entry goes in last,
once nothing below can fail.** The interceptor's callback has to fire early —
it runs inside `NewPeerConnection`, before the viewer exists — but collecting
the estimator and publishing it to the hub are two different moments, and only
the second has to wait. It is guarded by reading the syntax tree, because
reaching those branches wants `AddTrack` or `CreateOffer` to fail on demand and
nothing here can do that: `TestTheEstimateIsRegisteredOnlyWhenNothingCanFail`
asserts that no error return stands after the registration, and counts the ones
before it so that a green result cannot mean it found none.

**The method, which is worth more than the four cases.** None of these is
visible by reading the function: they are visible by **running** it with the
condition that lights them — twenty-five turns with no reading, an encoder
producing more than requested, the exact tick at which the scale climbs back,
ten minutes with nobody there. Two of the four had a comment beside them
declaring the opposite of what the code did, and that comment was written by
someone who had just measured the code: **a test that does not cross the branch
absolves the branch.**

### A room that moves revokes the discount, in one go

The saving is a loan granted on a premise: **the room is still, so those bits
are not visible.** When the room stops being still the premise has fallen, and
the right answer is not to rediscover one step at a time that bandwidth is
needed — it is to repay the loan.

**And it was watched for a whole night, which is the test this loop was written
for.** A viewer connected for six and a half hours — the loop runs only while
somebody is watching, and two earlier nights measured nothing for want of
that:

| | |
|---|---|
| the discount taken | 2500 → 744 → 509 → 321 → **300**, the floor, in **15 seconds** |
| the night at the floor | ~300 kbit/s produced at **qp 23**, against a target of 30 |
| the network governor | left at the cap throughout, correctly: at 424 kbit/s against 300 produced, the estimate is an **echo** and brings nothing down |
| motion at 05:26 | `fraction=0.1044`, and in the **same second** the loop at `kbps=2500 qp=40` |
| size changes | **zero**, in six and a half hours |
| after dawn | silent again — hours 07 and 08 carry not one command |

**Zero size changes is the half that was being asked about.** A night at the
floor of the bitrate with the scale untouched is the whole design working: the
picture stayed at 1280x720 while the room was still, and nothing was spent
proving it. And the motion jump is the same 386 ms behaviour measured before, met
this time by accident at half past five in the morning rather than on purpose.

**The dawn hour is the one to look at twice.** Between 06:00 and 07:00 the loop
commanded **1602 times**, of which 523 at the cap — the light rising and somebody
up, which is a scene really changing. The rest wobble in a narrow band every five
seconds, 328, 333, 325, 334, with the quantiser steady at 25-26: the loop is
anchored to the throughput it measures, and a throughput that varies by 5%
produces a command that varies by 5%. It costs nothing on this chip, where the
bitrate is set with a command; **on one where it is set by rebuilding the
encoder it would be a flush every five seconds**, which is the micro-stutter this
file already describes. Nobody has watched a dawn on such a chip.

**Measured live, and the surprise is that the cost was not a ramp: it was a
command that did not arrive at all.** Quick Sync, a real viewer, room still
until the loop had come down to 772 kbit/s, where it stayed two minutes
forty-one — settled, then, not mid-descent — then somebody moves:

```
14:34:22.813  motion in the room  fraction=0.0078
14:34:23.199  bitrate matched to quality  kbps=2500  qp=30  target_qp=30  produced_kbps=667  motion=true
14:34:24.199  produced_kbps=2353  qp=26
```

**386 milliseconds**, and above all `qp=30` with `target_qp=30`: the deviation
was **zero**, that is, squarely in the dead band. Without this branch the loop
would not have made a slower climb — **it would have done nothing.** The obvious
objection — "motion comes from the same source as the video, so the quantiser
rises in the same instant and the loop can already climb by itself" — is
answered by that: the quantiser is the p90 of a one-second window, taken at the
tick, so it arrives **later**. Detection is the only thing that speaks first.

**And it is not one episode: the session had fifteen**, fourteen with a jump,
and the picture together says more than the first — delay from motion between 82
and 808 ms, **median 377**, always under a second because the signal is consumed
at the next tick; quantiser at the moment of the jump **28-32, that is, against
the target**. **In thirteen cases out of fourteen the loop on its own would have
raised nothing**: ten times it was inside the dead band and **three times at 28,
that is, better than target — the loop would have lowered.** The rest of the
sequence says the jump was right and dismantles itself: throughput from 667 to
2353 in the following second, then the loop took its discount back at the normal
pace, going below 1200 kbit/s in forty seconds, with **zero losses and no size
change**.

**When the quantiser does have time to rise, the cost goes back to being a
ramp**, and it does not vanish: in half-light the camera drops to 9-20 fps and
the quantiser stays nailed at 32-33 while the bitrate changes threefold, so with
a three-point deviation each step buys 41% — **four commands and twenty
seconds** from the bottom of the discount to the ceiling, against one and one
second (`TestWithAStuckQuantiserTheClimbTakesSeveralCommands`). That is, the
worst case is not rare: it is night, which is when this program works.

- **It goes to the ceiling and not by a computed step.** A step would be what
  the loop will do anyway next turn; the ceiling removes the ramp entirely and
  **has no constant to tune**, being limited by construction to the bandwidth
  the network has already granted.
- **And this branch can only raise**, by a written condition: if the proposed
  limit is **below** what is already being asked for, it does not execute — a
  cut decided by motion would stay in force for five seconds. The cut is
  legitimate and the normal branch makes it a tick later. Covered by
  `TestMotionNeverCutsTheBitrate`.
- **`current < cap` is the exact condition**, not an approximation: with a
  target set, the only way to be below the ceiling is for this loop to have
  lowered it, because the ceiling clamp always brings `current` back to the
  ceiling.
- **The jump replaces a command, it does not add one**: it rearms `stirred`, and
  right after the loop resumes identically, descents included. Motion at
  midnight does not hold the monitor at the ceiling until morning.
- **Skipping the wait here is harmless**, and the reason is not an opinion: the
  five seconds exist so as not to judge an encoder while it is answering and not
  to put two commands inside one `bitrateSeen` window, while **a rising request
  cannot trigger that watchdog** — the first branch of its switch resets the
  reference as soon as more is asked than before. It was a real property written
  down nowhere, that is, the kind of thing a tidy-up removes without noticing:
  `TestARiseNeverCountsAsAVerification` nails it.
- **The signal does not go through the `detect_motion` switch**, which is
  argued where the mask lives: it says what to notify, not what quality the
  video should have. The live test was run with `detect_motion: false`.
- **It is read at the one-second tick and consumed by reading**
  (`Config.MotionStarted`). No channel and no early wake-up: the gain would be
  less than four tenths of a second, the price a **second point from which the
  encoder is commanded** — which on this program is precisely the defect that
  produces governors fighting each other. The signal is consumed **every turn,
  even with no viewers**: an episode that began while nobody was watching is not
  news for whoever arrives afterwards.

**What was not taken, and from where.** NVENC's 2-pass real time — the one
Sunshine uses — looks like the thing to imitate and is not: the first pass at
quarter resolution runs **on the same frame** about to be encoded and
distributes the bits *within* that frame, while lookahead over subsequent frames
is a separate function that **not even Sunshine enables**. In both cases it is a
second quality controller inside the encoder, under the first one's feet: the
capped CRF family. The same holds for `CODECAPI_AVEncVideoROIEnabled` and
`MFSampleExtension_ROIRectangle`, which really do exist on Media Foundation —
telling the encoder **where** to spend is possible. **They are still not used**,
for the reason above: they are a second controller, not a thing that does not
exist.

### Capped CRF and the two-mode switch — removed, and what remains

**This section describes pieces that no longer exist**, and it remains for the
measurements. `ratemode.go`, the switch between CBR and constant quality, was
deleted when the saving could be obtained by commanding the **bitrate alone**.

- **The ceiling in quality mode does not exist there at all.**
  `AVEncCommonMaxBitRate` "applies when the rate control mode is
  **PeakConstrainedVBR**", and we were setting it in `Quality`;
  `AVEncCommonMeanBitRate` "is **ignored** if the rate control mode is Quality".
  Two properties asked of the wrong mode.
- **Real capped CRF works on a still scene — 24% fewer bits than CBR at the same
  quality — and not under load**: with someone in front of the camera, mean 2308
  and peaks 3913 on a ceiling of 2500, with the quantiser nailed to the floor.
  **The floor beats the ceiling.**
- **The quantiser reported in quality mode is the value set, not the one used**,
  and it is documented: of `AVEncCommonQuality`, "internally, the encoder
  converts this property to an `AVEncVideoEncodeQP` value". Variance exactly
  zero over 339 frames, keyframes included — **an encoder does not produce 339
  identical frames, a log does.**
- **The switch died of a structural defect**: there is no way to know whether
  the scene has calmed down while staying in CBR, because there the encoder
  fills the pipe by definition.

**The scale looks at a percentile of the last second, not the mean**, and this
measurement decided it: in CBR at 900 kbit/s the mean was 29.9 and the maximum
**43**, and blocking shows in the worst frames. The percentile is **90 and not
95**: the window is one second, twenty to thirty samples, and out of twenty-five
the p95 falls on the last, which is almost always the keyframe, encoded worse
**by construction** (`qpwindow_test.go`). And the window is **taken**, that is,
cleared at every read: a cumulative percentile ages.

Three things from that work remain in force:

- **The motion transient is two seconds and stays below the break.** At reduced
  bitrate the QP rises from 28 to 32 in one second and to 37 in two, then stops;
  the encoder answers a bitrate increase within one second. The watcher sees the
  image soften, not break — provided one reacts to a small QP rise instead of
  waiting for the break threshold.
- **Removing pixels is the last resort, not the first.** While there is unused
  bandwidth, a high quantiser is cured by buying bits: the scale intervenes only
  when the bitrate is already at the ceiling (`maxBits`).
- **The audio is never compressed to make room for video**: a monitor that can
  be heard but not seen still warns that the child is crying, the converse does
  not. It is the same priority libwebrtc gives audio in its `BitrateAllocator`.
### The quantiser: the attribute where it exists, the stream where it does not

It is libwebrtc's rule, which uses the QP declared by the encoder and falls back
to its `QpParser` only when it is missing. Each of the two readings alone leaves
a machine uncovered.

**`MFSampleExtension_VideoEncodeQP` is optional.** The documentation promises it
is set by "the H.264 Video Encoder", and that link points at Microsoft's
**software** encoder; the HLK certifies `ICodecAPI` properties, not sample
attributes. Chromium knows and has a recovery branch **only for Intel**. On AMD
the attribute really is absent: over 542 frames the encoder puts 17 on its
samples and the quantiser is not among them — and both facts are needed
together, because reading seventeen proves we can enumerate them.

**The stream is read with libwebrtc's formula** (`internal/media/qp.go`):

	QP = 26 + pic_init_qp_minus26 + slice_qp_delta

The first term is in the PPS, the second in the slice header; `QPReader` keeps
SPS and PPS and re-parses them **when they really change**, comparing the bytes,
because the scale rebuilds the encoder and a new SPS arrives from there.

**The proof that the parser is right is a falsifiable prediction**, not a
reread: imposing a floor, that floor must be read back. On AMD, which does not
expose the attribute at all, asking for 30 reads 30.0 exactly and asking for 38
reads 38.0.

**The forgotten field was `num_ref_idx_active_override_flag`**, which exists
only in P slices: one bit, and everything after it shifts. The symptom is the
one this project fears most — a number **plausible and wrong**: with the floor
at 30, IDRs, which do not cross that branch, read 30 and P slices read 18, that
is, two populations of which the more numerous was read wrong. For the same
reason `ParsePPS` **refuses** instead of guessing: CABAC, weighted prediction,
slice groups, high-fidelity profiles, `pic_order_cnt_type` other than 0 and 2.

**A B slice was the one case nobody refused, and it was read.** `ParsePPS`
turns away CABAC and the High profiles, and B frames are legal from Main
upwards: a B slice header carries three fields this parser does not know —
`direct_spatial_mv_pred_flag`, `num_ref_idx_l1_active_minus1` and a second
reference-list modification — so everything after them shifts. It is the same
defect as the forgotten `num_ref_idx_active_override_flag`, one slice type
across.

**And the misreading is not conveniently fatal.** Swept over the fields that
move the bit positions — the direct flag, the frame number, the override flag,
the list modifications, the quantiser from 20 to 45 — **896 of 3328 headers come
back as a legitimate quantiser**, every one of them wrong: a slice carrying 24
read as **48**, one carrying 20 read as 26. The first would tell the loop the
picture is broken ten points past the break threshold and cost a resolution step
for nothing. The other 2432 are refused **by accident**, which is not a
protection — it is the same misreading falling out of range.

`QPSlice` now returns "I do not know". The cost is the saving on a stream with
B frames, never the picture, and it is the same trade as everywhere else here.

**The two ends are done together, and the belt is the smaller half.** The
encoder is now *told* not to use CABAC or B frames — `AVEncH264CABACEnable` and
`AVEncMPVDefaultBPictureCount`, imposed rather than left to the defaults, which
are right on every encoder seen here and are still defaults. But a profile is a
request like any other, and this file is a monument to requests accepted and not
executed: **what protects is the refusal downstream, which does not depend on
anyone obeying.** On this machine the imposition cannot be shown to change
anything, because there was nothing to correct — and that is said rather than
implied.

**And the fourth instance was inside the marking list, where the argument was
read for an operation that has none.** `dec_ref_pic_marking` gives an argument
to MMCO 1, 2, 3 (two of them), 4 and 6; **MMCO 5 — mark everything unused for
reference — takes none**, and the `ue(v)` read for it came out of
`slice_qp_delta`'s bits. It is the same shape as the forgotten
`num_ref_idx_active_override_flag`, the unrefused B slice and the unrefused
interlaced SPS: **an unread field is not a value that can be corrected
afterwards, it is a bit offset.**

It is latent rather than seen — our encoders mark with the sliding window, so
the flag is zero and the loop is never entered — and that is not the argument,
for the reason this chapter already gives three paragraphs up: the marking mode
is the encoder's to choose, and a mode we do not use is not a mode we cannot be
given. What is new is only where to look next: **every list in that header now
ends on a code it does not know**, rather than skipping it, because a code
outside the table means the offset is already lost and the loop would go on
consuming one `ue(v)` per turn until it arrived at the quantiser with a number
to hand out.

**And the other half of that repair is not about correctness at all: it is about
a loop that would not end.** `Log2MaxFrameNum` and `Log2MaxPicOrderCntLsb` are
handed straight to `bits()`, once a frame, and they are exp-Golomb values plus
four — so the largest a malformed SPS could hand over is **4 294 967 298**.
`bits()` ran its loop to the end whatever happened, which is harmless while the
count is a literal and is not harmless here: measured with the old loop
restored, **7.2 seconds for a single call**. The quantiser was never wrong,
which is why nothing pointed at this — the frame loop simply stopped. Both
fields are now refused outside the 0-to-12 the specification gives them, and
`bits()` stops when the reader is empty.

**A stopwatch is the instrument for that one, and it was nearly the wrong one.**
A guard was written on the *allocations* instead — the old loop also built an
error per bit, a million of them at n = 1<<20 — and it **passed with the loop
put back**, because once `bit` records its exhaustion once there is nothing left
to count. The two defects are one, the cheap assertion measured the half that
does not matter, and it was thrown away rather than kept for the green.

**The parser had no test at all**, which is how a hole like this stays open, and
the first one written **passed with the defect put back**: it asserted the
refusal on one hand-made header, and that header happens to run off the end of
the buffer when misread. It is the family the guards chapter names — a test
written by the same mistake it should catch — and the remedy was to stop
choosing the header and sweep the shape.

#### The reserve for the parameter sets is not where it looks

Without an SPS and a PPS the quantiser cannot be read at all, and `QPReader`
took them from the stream alone. On an encoder that does not put them there it
would stay mute for the whole session: no quantiser means the bitrate loop sits
at the cap, so the saving is lost and the picture never — **the fault is
silent**, which is why it needed doing before somebody met it.

The reserve is `MF_MT_MPEG_SEQUENCE_HEADER`, whose documented content is exactly
what is needed: "concatenated NAL units in Annex B format, along with their
start codes … for h264 they are SPS & PPS".

**But the attribute does not exist when the encoder is built.** Measured on
Quick Sync: **zero bytes** at build time, **forty-eight** by the first frame.
Read at the obvious place — beside the output stream info, where every other
property of the encoder is taken — this reserve would have been permanently
empty on the one machine available to test it, and everything else would have
gone on working. It is read once, after the first sample, in the same shape as
the attribute inventory beside it.

**And the declared sets are not the stream's bytes.** Of thirty-five, **two
differ**, and they are the two that matter most elsewhere:

	in-band   27 42 e0 1f 95 b0 14 01 6e c0 44 …     constraint flags E0, level 3.1
	declared  27 42 40 20 95 b0 14 01 6e c0 44 …     constraint flags 40, level 3.2

The PPS is identical, and so is everything else in the SPS. **For reading the
quantiser the difference is nothing** — both are profile 66, and the two parse
to the same `ppsInfo`, which is asserted with the real bytes in
`TestTheDeclaredHeaderOfARealEncoderIsAsGoodForReading`. **For anything that
announces a profile it is everything**: the SDP is refused without the `0xE0`
flags, as the chapter on SDP constraints already records. So this blob is fit
for one purpose and unfit for the other, and the day somebody reaches for it to
fill in a profile, that is where they should find it written.

It follows that **the stream is authoritative by construction, not by
convention**: `QPReader` records where its parameters came from, and a seed is
refused once the stream has spoken. The reserve is offered on **every** frame —
from the one place that has the frames, rather than the three that build
encoders — so there is nothing to remember and nothing to forget.

**A reserve nobody exercises is the least tested part of a program**, and on
every encoder seen here the stream speaks on the first keyframe, so this one is
never taken. Hence the test built from the bytes a real encoder actually
produced, which also carry the three `000003` sequences a synthetic parameter
set does not, and which the emulation-prevention stripping has to remove.

#### On Quick Sync the slice quantiser is a constant

**26.0 exactly over 696 frames**, keyframes included — that is,
`pic_init_qp_minus26` and `slice_qp_delta` both zero — while the attribute moved
between 26 and 42 on the same scene. That encoder does rate control **per
macroblock**; that it applies the deltas is shown by `-minqp 30`, with the
attribute reading back exactly 30.0. Intel confirms: `pic_init_qp_minus26` at 0
is expected in bitrate-governed modes, and "you need to look at mb_qp_delta as
well" — which would mean decoding the macroblock layer, that is, half a decoder.

**The two hypotheses — parser broken on Intel streams, or constant slice QP —
predicted the identical numbers**, and no reread could separate them. An
experiment separated them: same parser, same machine, same bitrate, **different
encoder**.

| encoder                    | QP from the stream       | agreement with the attribute |
|----------------------------|--------------------------|------------------------------|
| Quick Sync                 | **26.0 fixed** over 696  | deviation 6.5 · direction **+6.5** |
| H264 Encoder MFT (software)| mean 30.7 · p95 38 · max 45 | deviation 1.7 · direction **+0.0** |
| AMD                        | mean 23.3 · p95 25 · max 28 | attribute absent |

A broken parser does not produce zero systematic error on one encoder and six
and a half points on another. The three rows also say the rule covers the real
cases: where the stream does not measure there is the attribute, where the
attribute is missing the stream measures, and where both exist they agree.

**The direction of the deviation matters more than its size.** With the sign,
one sees that they were **+3.5 on every single frame**: adaptive quantisation
moves macroblocks now above, now below, and its deviation cancels out, so **a
deviation always on the same side is not a phenomenon, it is a reading that is
always wrong in the same way.** `pat-capture` fails a direction beyond one point
too.

Two method errors, in the same story:

- **The count said it from the start**: 1396 readings over 698 frames, exactly
  double. There was a second `qpSeen` left over from before the parser, and the
  window was receiving **two samples per frame, one per source**, that is, two
  machines measuring different quantities. **An exact ratio of 2.0 between two
  counts is never a coincidence.**
- **The control had been promoted wrongly**: on Intel, attribute and stream
  agreed with a mean deviation of 1.5, but the measurement was taken at 2500
  kbit/s, where the true value passes close to 26 — **a constant number next to
  the right one looks exactly like agreement.** Dropping the bitrate was enough:
  3.5 points at 800, 6.5 under load.

**The fault that would have followed is the product's worst**: with `target_qp:
30` and a nailed 26, the loop concludes "quality is in excess" every turn and
goes down to the floor of 300 kbit/s forever, whatever happens in front of the
camera. The scale would merely have been inert, because it learns its reference
from the same constant number.

**The default for `target_qp` is 30**, switched on after measuring on two
machines and three encoders: leaving it off would have meant nobody used it,
because **nobody hand-writes a configuration line whose existence they do not
know of**. It is declared — `-show-config` writes "saving: on, quality 30" —
because a feature that halves the bits on the wire without appearing anywhere is
indistinguishable from a fault.

#### The net underneath: a reading enters only after being seen to change

For encoders we have never seen, which might nail the stream **and** not expose
the attribute, the protection is in `qpMeasured`: a value enters the statistics
**only after that reading has changed value at least once**. A constant 26 never
enters, and with no quantiser the loop sits at the ceiling: it costs the saving,
never the image. The judgement is not withdrawn — once the number has shown it
moves, standing still is news about the scene.

**The first attempt was different, looked more rigorous and was wrong.** It
said: if the bitrate drops and the quantiser does not move, that reading does
not measure this encoder. False positive on AMD in under a minute — bytes down
55% and quantiser steady at 23 — because that relation holds **only when the
encoder is really constrained**, and in CBR on a still scene it under-produces
without using the rest to improve quality: that is, "the bits go down and the
quantiser does not move" is not the symptom of the fault, **it is the definition
of this loop succeeding**. A watchdog that cannot tell success from failure is
built on the wrong quantity: the right question is not "did the number react to
the drop in bits?" but "does this number ever change?".

**And the false positive was found by the hardware, not by reasoning**: the test
written to protect that very case simulated a *constant* throughput while here
the bytes really were falling, so it passed. **A test written by the same
mistake it should catch catches nothing.**

### The quantiser floor, and the auto-tuning that was removed

`AVEncVideoMinQP` in `RateCapped` decides below which quantiser the encoder
stops spending to improve. On a still scene it is worth a lot: 2500 kbit/s in
CBR against 163-214 with the floor at 32.

**It is an absolute QP, so in theory it is not portable** — libwebrtc uses 24/37
with openh264 and 28/39 with VideoToolbox for the same H.264. An auto-tuning at
start-up that looked for the knee of the curve over four floors was **written,
tested on two machines and removed**, and the reason is worth more than the
code:

- **The two machines gave the same number, 32**, despite encoders from different
  vendors: the knee depends more on sensor noise — the camera and the light —
  than on the chip.
- **The fingerprint contained encoder, driver and binary, not the scene.** It
  locked down the variable that does not change the result and ignored the one
  that does: a measurement taken in the dark stayed frozen in daylight.
- **In three runs it got two wrong.** One chose an already-broken image because
  the criterion was made of ratios alone and was blind to scale — 19 kbit/s at
  QP 37 against 180 at QP 31 looks like nine times, but 180 on a ceiling of 2500
  is nothing. The other measured the room instead of the encoder.

**And `min_qp` is not even a knob any more: it is a `pat-capture` flag.** It was
in the configuration — with `rate_control` and `max_qp` — but `cmd/pat-monitor`
**never** passed them to the pipeline. **A knob that moves nothing is worse than
an absent knob**: whoever turns it concludes the program does not respond and
looks for the fault where it is not. Found with `deadcode`, not by rereading the
code.

They would not come back today anyway: the saving is done by `target_qp`
commanding the **bitrate alone**, while `capped` and `min_qp` put a second
quality controller **inside** the encoder. For measuring the knee the flags
remain: `pat-capture -rc capped -minqp N`. **It is a quantity no other piece
measures**: not the reference "what a free encoder produces" (on AMD, QP 22.6),
but "how far one can degrade while gaining a lot" (QP 30-32, ten times fewer
bits).

### The requested bitrate is not the produced bitrate, not even in CBR

Measured on AMD, still scene, four bitrates:

| asked | produced | deviation |
|-------|----------|-----------|
| 2500  | 2415     | **-85** |
| 1200  | 1253     | +53    |
|  600  |  616     | +16    |
|  300  |  314     | +14    |

The ceiling holds within 5%, and at full bitrate the encoder sits **below**.

It remains true that AMD **prefers to spend rather than approximate** when the
scene gets difficult: observed, five seconds at 3400-3475 kbit/s on a ceiling of
2500 with the quantiser steady at 23-26. That is the phenomenon to fear.

**And the obvious hypothesis was disproved.** The quantiser in CBR never
exceeded 24, which suggested an encoder refusing to approximate — with
`AVEncVideoMaxQP` as the remedy. Set to 42, the quantiser did rise (max 33) and
**the bitrate did not come back**; the mean got worse. On a still scene, `MaxQP`
at 0, 42 and 35 gives **the identical result**, and one can see why: there the
quantiser never comes near the ceiling, so the ceiling touches nothing. **It is
not that `MaxQP` does not work: it is that that test does not interrogate it.**

#### The "seconds above the ceiling" column measures the GOP, not the encoder

The GOP is two seconds, so **one second in two contains a keyframe** and a
one-second window takes all of it. Measured at 600 kbit/s, second by second:

```
876  508  858  534  964  588  884  442
KEY       KEY       KEY       KEY
```

There is **not one exception**: every high value is a second with the key. As a
ratio to the ceiling that is 1.49x on the keyframe seconds, 0.86x on the others,
and **1.18x on average** — that is, counting seconds above the ceiling gives
"half of them" on a stream that on average sits inside it.

**The keyframe's share grows as the bitrate falls**, because its cost does not
halve with the budget: at 2500 the alternation is worth 5%, at 600 50%, at 300
90%. **On Quick Sync the alternation is there too**, only smaller: ±9% against
AMD's ±50% at the same relative distance from the ceiling. The difference
between the two chips is not "one respects the ceiling and the other does not":
it is how much each **spreads the keyframe's cost** over neighbouring frames.

#### The byte ceiling is judged over a window, not over a sample

The practical consequence was paid by **the quality loop**. Its byte ceiling —
"if 1.66 times what I ask comes out, I ask for 1.66 times less" — looked at the
throughput of **one second**, with a 20% dead band tuned on the noise at full
bitrate. At the bottom of the scale that dead band is no longer enough: the
second with the keyframe is worth 1.49x, steps over it, and the request was cut
to two thirds **one time in two, for a frame that has to be sent anyway**.
Verified live with a viewer, ceiling forced to 600:

```
before (sample)   383 → 600 → 400 → 300      four moves, 100% oscillation
after  (window)   372 323 402 419 356 385 444 419 378 402 407 447 479 478
```

The window is **two GOPs** and not one: with one, depending on alignment, it
contains two keyframes or none, while at two the keyframe share is the same in
every window. And it **publishes nothing until it is full**. The price is that
the constraint bites after four seconds instead of one, and that is the right
direction to err in: on the other side there was a 40% cut at every keyframe,
every night. The loop's **step** stays anchored to the sample — there an error
is corrected next turn, while the ceiling is a one-way cut.

**`bitrateSeen` is not touched**, and now the reason it had never shouted is
known: it compares the mean over four seconds, that is, over two GOPs, so the
alternation averages itself away by construction.

##### And the estimate's credibility was still judged on a sample

**The same metric, the same phenomenon, the third reader left behind.**
`estimateIsCredible` and `believableDrop` were still being given the throughput
of **one second**. Measured, with the quality discount in force and the room
still:

```
produced  789   75  560  113  558  116  543  116  864   70  651
video estimate, from transport 380-453             338
```

Between one second and the next the throughput changes **sevenfold**, and what
decides it is whether that second contained the keyframe. On the low samples the
estimate was **rightly** refused as an echo; then came the 864, and `338 < 864 x
0.40 = 346` — **two per cent past the threshold, on a sample the encoder had to
send anyway.** From there: collapse believed → estimate believed → `collapse` in
the scale, which does not wait for the three confirming samples → **720p →
640x352 in one go**, with `qp=29` against a break threshold of 38, losses at
**0.0%**, and the real bandwidth re-measuring at **2696** twenty seconds later.
Twice in five minutes, and coming back up requires two twenty-second dwells.

**The defect was not a wrong value: it was which number was being passed.** No
test on the value catches that, so the guard reads the syntax tree and refuses a
one-second throughput in the argument of whoever judges an estimate
(`TestNoEstimateIsJudgedOnASingleSample`).

**Losses go through no window**, and that is the half not to lose: they say what
happened to our packets, not what the network might do. The window publishes
nothing until it is full — with the throughput unknown no drop is credible, that
is, the saving is lost and never the image — and it **does not survive a change
of size**.

**But this correction, on its own, stopped nothing**, and that is the part to
remember: rebuilt and retried, the scale came down again ten minutes later —
`qp=27`, threshold 38, losses 0.0% — with the window in place. **A correction
that reduces the frequency of a fault looks exactly like one that removes it,
and telling them apart takes re-measuring rather than rereading the diff.** The
cause is in "The quantiser has a veto over the bandwidth descent".

Four minor defects stay fixed here, all of the same family — **a window is an
assertion about a stretch, and it holds only while the stretch is the same**: an
empty window is not a harmless unknown throughput (`0` read as "believe
everything" for three turns); a missed reading must age the window, otherwise
during a capture restart it answers with the **previous** throughput; it
publishes from **two** samples and not four, because two consecutive ones
contain exactly one keyframe and demanding four meant three turns blind to a
lossless collapse; and it resets **on a cadence-only change** and **at every
bitrate command**, otherwise during a ramp the mean describes the previous
request.

### Do not ask the camera for more pixels than it declares

With `MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING` on — and it is on,
because the resolution scale needs it — the reader **does not pick the nearest
format**: it takes the size it is given and puts a scaler in the middle. And
reading the format back is not a witness, it is the echo.

Together those make a fault that shows nowhere: a 640x480 webcam is asked for
720p, **it arrives**, and the log says `camera opened format="NV12
1280x720@30"`. On a Logitech C210, which declares 640x480 as its maximum, the
monitor was asking for **2.25 times the pixels** upscaled in software and
encoded at full bandwidth, with not one extra detail: on that machine it
delivered no frame in twenty minutes.

**The rule is not "negotiate": it is to remove the one direction in which asking
can only cost.** `mf.PickCameraSize` lowers the size to the declared maximum
**only** if the camera does not have enough pixels; if it has, the preset stays
whole and the reader shrinks, which is the good direction and is needed by the
scale anyway.

Four non-obvious things:

- **The criterion is the pixel count, not the two dimensions separately.** A
  camera declaring 1280x1024 on a 1280x720 preset has them all; looking at
  height it would be excluded and something much smaller chosen.
- **The stream is the first video one, not stream zero.** Webcams with Windows
  Hello also expose an infrared sensor, and choosing the size from one list and
  opening another would be worse than not choosing.
- **The question is asked at every open, and it used to be asked once.** The
  reason written beside the old shape was that the starting size is read by two
  — the pipeline that opens the camera and the hub that builds the scale on it —
  and lowering it in one place alone would give a scale climbing to a step that
  does not exist. Both halves are still true and the conclusion is not: asked
  once, in `main`, the answer describes **the camera that was there at
  start-up**, and a camera changed while the monitor runs would be opened at the
  previous one's size — that is, upscaled, in silence, which is the very fault
  this chapter exists for. It is now asked by whoever opens, in `runVideo`, and
  the hub reads it back through `VideoStartSize`.

  **So the resolution scale is rebuilt when that size changes.** Its steps are
  fractions of the starting size — 1, 3/4, 1/2, rounded to the macroblock — so
  from 720p they are 1280x720, 960x528 and 640x352, and a 640x480 webcam is
  **none of them**. Left with the old steps, nothing that arrives matches one,
  `resync` does not invent a size that is not a step, and a false
  `atFullSize()` switches the saving off and nails the bitrate to the cap for
  the whole session. Rebuilding throws away the judgements of the scene before,
  and that is right rather than a cost: it is another camera, somewhere else,
  pointed at something else.

  **What must not be rebuilt is the announced level**, and that is why it was
  the piece to do first: it is read at the moment of the offer and cannot be
  taken back. See the SDP chapter — it comes from the preset, which no camera
  can exceed.
- **A failure in enumeration stops nothing**: it opens as it would have opened
  before. Worse than opening a camera sub-optimally is only not opening it.

**And the function, once written, did nothing.** `ActivateObject` answered
`CO_E_NOTINITIALIZED` because the monitor initialises COM inside the capture
goroutine, and this question is asked at start-up from a thread that has not.
The fallback held, so nothing failed: the size simply came back equal to the one
asked for, **every time**. It was found by running the monitor and reading the
warning line, not by a reread and not by a test. Now `onMFThread` opens a thread
of its own, MTA and locked.

`pat-capture` instead **declares and does not correct**: it is a measuring
instrument and the numbers are chosen by whoever runs it, but with the scaler
on, a request larger than the camera succeeds, and without a warning one would
measure an upscale believing it was measuring the camera. Covered by
`pick_windows_test.go`, which uses the real C210 list.

**And moving the question into the pipeline took that away from it**, because
`pat-capture` builds a `Pipeline` too. It went on printing "what follows
measures an upscale" and then measured the camera: **an instrument that quietly
changes what it measures is worse than one that measures the wrong thing
loudly**, and it is the twin of the tool that opened the camera differently from
the monitor — the same defect from the other side, a tool that stopped differing
because a shared piece started correcting. The decision is therefore the
caller's, `KeepRequestedSize`, and **false by omission**: whoever writes nothing
gets the protection against the silent upscale, and whoever measures has to say
so. Both directions are tested, the instrument's by asserting the camera is not
even interrogated, and `cmd/pat-capture` carries a guard on its own
configuration — on a machine whose camera has the preset's pixels the two
spellings measure the same thing, so no test on a value separates them.

**The four findings of that review were one shape: a decision moved, and its
readers stayed behind.** It is the codes chapter's question — at every change of
shape, not "who writes it" but **"who reads it"** — applied to a decision rather
than a field, and the readers were not obvious: the measuring instrument that
shares the pipeline; the `sent` window, whose four samples describe a size that
a camera swap silently replaces, where the three other commands that change what
goes out already reset it; the start size published as **three** atomics, which
`wantFormat` eight fields above forbids in as many words — "the three numbers
sit in one struct because they have to be read together" — and a phantom size
here is worse, because the scale is *built* on it; and `onMFThread`, whose
recorded reason named the monitor asking from a thread with no COM, when the
monitor had just moved to asking from the one thread that has it.

### The resolution is scaled by the Source Reader, not by the camera

Below a certain bandwidth, going on removing bits from 720p gives only a mushy
image: the right answer is to send **fewer pixels**. This can be done without
touching the camera — `SourceReader.SetOutputSize`, that is,
`SetCurrentMediaType` on the video stream — and the difference from reopening
the camera on another format is everything: this way the video is not
interrupted and the motion detector goes on seeing the same scene. It is
possible because `MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING` is on.

**The proof is a size the camera does not offer.** Verified with `pat-diag`,
which goes down 1280x720 → 960x540 → **848x480** → 640x360 and back up: the
native formats alone would not distinguish scaling from switching the camera's
mode, but 848x480 is not one the camera has and it arrives all the same. The
verdict is given by **the frame's bytes** (NV12 = w x h x 3/2), not by
`SetCurrentMediaType` nor `GetCurrentMediaType`: a format accepted and not
applied would answer yes to both. The return to full size is in the test on
purpose: the scale must be able to climb back when bandwidth returns, otherwise
the first bad moment on the network would leave the image small all night.

**And whether the reader scales at all depends on the camera, which is the
half this chapter's title gets wrong.** The request is
`SetCurrentMediaType` on the reader's **output** — NV12 at the size wanted — and
the reader satisfies it in one of two ways: by finding a native type on the
device that matches, or by putting a Video Processor in front of one that does
not. **It only scales in the second case**, and which case it is depends on what
the device happens to offer:

| | what it declares | a size it does **not** declare |
|---|---|---|
| ACER HD User Facing | YUY2 only | 848x480 **delivered** |
| Integrated Camera, Lunar Lake | **NV12 only** | 848x480 **delivered** |
| Logitech Brio 105 | NV12, MJPG **and** YUY2 | `ReadSample: HRESULT 0x80004005` |

**Every size that worked on the Brio is one it declares, and every size that
failed is one it does not** — 1280x720 and 640x360 delivered, 960x540, 848x480,
960x528 and 640x352 refused, five for five. The camera declares 960x544 and
640x360; the scale asks for 960x528 and 640x352, because its steps are fractions
of the starting size rounded to macroblocks and owe nothing to what the device
has.

**What predicts it is not "does the camera offer NV12", and that rule stood here
for one day.** It was written from two cameras — the ACER, which has no NV12 and
therefore forces a converter, and the Brio, which has NV12 and fails — and it
reads well: ask for a format the device cannot give and something must convert;
ask for one it can and the reader renegotiates the device instead. **The third
camera kills it.** The Lunar Lake laptop's Integrated Camera declares **NV12 and
nothing else**, twelve formats up to 2560x1440, and it delivers 848x480, which
is not among them. Same condition, opposite outcome.

**And then the camera itself was moved, which kills the rest of it.** The Brio
was unplugged from the machine where it fails and plugged into the Lunar Lake
one, and there **both** the steps that kill the capture go through — 960x528 and
640x352, without any pinning, no `ReadSample` error, and the SPS in the stream
reads `1280x720 then 960x528` and `1280x720 then 640x352`. Same camera, same
binary, same two requests.

**And it was plugged back in, into a different USB port, and it fails again**:
960x540 and 848x480 refused, 1280x720 and 640x360 delivered, byte for byte the
same probe. So it is not the socket either — which is the second time that
control has been run on this machine and the second time it has moved nothing.

So the fault is **not the camera's**. It travels with the **host**: a driver, a
USB generation, a chipset — and until somebody plugs a *third* camera into the
failing machine, or that camera into a *fourth*, "the pair" is as far as the
evidence goes.

**It is the same lesson as the USB port, met a second time and from the other
side**: changing the port moved the cadence and not the failures, which said they
were two causes; moving the camera moved the failure with the **host** and not
with the device, which says the cause is not where every measurement so far had
put it. **Two machines can only ever name a pair; it takes a third to say which
half.**

**The claim this chapter's title makes was true by accident all the same.** It
was verified on a webcam that exposes YUY2 alone, where a conversion that had to
happen anyway carried the scaling for free. **A property that holds because of
something else's accident is not a property** — and the correction to it, made
from one more camera, was itself a generalisation from two. It took a third to
see that, and the third was a machine nobody could reach until today.

**And on some cameras it does not arrive, which is the other half and was found
by looking.** On a Logitech Brio 105 the reader **accepts** 960x528 — the
monitor's own first step down — and the next read fails:

	video format changed  video=960x528@30  reader=960x528  error=<nil>
	capture interrupted, restarting  error="ReadSample: HRESULT 0x80004005"
	camera opened  format="NV12 1280x720@30"

That is two seconds with no picture, and a capture that comes back at **full
size** — the size the network had just failed to carry. `pat-diag`'s own probe
says the same about 960x540 and 848x480 on that device while 1280x720 and
640x360 are delivered, so it is not one unlucky number.

**And it is not one unlucky step either: it is every step there is.** Asked
again, 960x528 failed identically; asked for **640x352**, the only other size
the scale knows, it failed identically again — four attempts, two sizes, two
rounds, always `error=<nil>` on the change and `ReadSample: HRESULT 0x80004005`
on the next read. So on that camera the scale **has nowhere to go**, and the
hope that a device refusing the middle step could be served by dropping straight
to the bottom is measured and dead.

**It is the camera, and the control is what says so**: the identical step on an
ACER HD User Facing went through with no interruption at all, 669 frames, the
format change clean. **A size the Source Reader accepts is not a size the device
will deliver**, and `error=<nil>` on the change says nothing about it — the same
shape as an `ICodecAPI` property accepted and never applied, one layer down.

**And it is not the encoder, which is the first thing anybody asks.** The new
encoder is built **before** anything else moves, and an encoder that refused the
size would write `no encoder for the new format, staying where we were` and
change nothing. That line never appears: NVENC accepts 960x528, the reader
accepts it, `error=<nil>` — and then 286 ms later a frame cannot be read. The
failure is upstream of the encoder, at the camera, and the pixels never reached
anything that could have refused them.

**What the monitor does about it was watched, and it loops.** Eight minutes with
a real viewer and the ceiling forced to 900 kbit/s, which is what makes the
quantiser climb and the scale come down:

| | |
|---|---|
| requests for 960x528 | **5**, each one killing the capture |
| the wait between restarts | 1 s, 2, 4, 8, **16** — the backoff doubling |
| black picture | **~32 seconds out of 70** |
| what ended it | the quantiser fell below the break threshold at full size, and the descent went to the **cadence** instead — `declared=15`, which changes no size and works |

Between each attempt the capture comes back at full size and
`the scale was not aligned with what is being sent` fires, which is `resync`
doing its job: it realigns to what is really going out, and then, nothing having
been learned, the governor asks for the same step again. The five requests are
not five decisions — they are one decision taken five times, because the only
thing that changed between them is the backoff.

**And the cause can be removed instead of worked around**, which is where this
went. If the device's own format is **pinned**, the reader has nothing left to
renegotiate and has to put a converter in the chain — which is the thing the
ACER got for free by speaking YUY2 alone. `IMFSourceReaderEx::SetNativeMediaType`
is the documented way to say it, and nothing here had ever called it.

**Measured on the camera that fails, same binary, same session, both steps:**

| | without | with the native format pinned |
|---|---|---|
| 960x528 | `ReadSample: HRESULT 0x80004005`, twice | clean, twice |
| 640x352 | `ReadSample: HRESULT 0x80004005`, twice | clean, twice |

and the witness is the one this file insists on — not the reader's answer but
the **SPS in the stream**: `sizes seen in the stream 1280x720 then 960x528`,
and `1280x720 then 640x352`. On the ACER, where the converter was there anyway,
the switch changes nothing: the same size change goes through identically with
it and without it, which is the control that says it is not merely masking.

It lives behind `Config.PinNativeFormat` and `pat-capture -pin`, and it stays
off, because the same runs turned up a second effect **whose cause is not
established**. With the pin, a change of size doubles the frames coming out.
Measured on the same camera, on a size the device does declare, so that the two
roads are comparable:

| | frames in 40 s | after the change | bitrate | quantiser |
|---|---|---|---|---|
| without the pin | 574 | ~15 a second | ~1150 | 27 |
| with the pin | **884** | **~30 a second** | ~2700 | 17-18 |

**Without a size change the two are identical** — 287 frames and 14.1 fps
either way, three pairs — so it needs the processor in the chain, not the pin
alone. And the extra frames are **repeated, not filmed**:

| | frames between keyframes | p10 bytes | under 500 bytes |
|---|---|---|---|
| without the pin | 559 | 4709 | **12 (2%)** |
| with the pin | 864 | 367 | **181 (20%)** |

A frame that costs 367 bytes at 640x360 is a P slice with nothing in it. The
instrument is two lines in `pat-capture` — the sizes of the frames that are not
keyframes — and it exists because from every count, every rate and every
delivery measure a repeated frame and a filmed one look the same.

**Microsoft documents it, and the documentation was the thing to read first.**
`MF_SOURCE_READER_ENABLE_ADVANCED_VIDEO_PROCESSING` lists frame-rate conversion
among the four things the inserted processor does, and says that when the reader
configures it, *"it attempts to match the following attributes of the output
type"*, the first of which is `MF_MT_FRAME_RATE`. So the 30 that `requestFormat`
writes is an order, and on a camera delivering 14 the processor makes up the
difference. The road out is to stop declaring a cadence we do not have.

**And the control that seemed to disprove this was broken**, which is worth more
than the finding. Asking the ACER for 60 fps produced 30, and that was read as
"the processor does not emit at whatever rate is declared" — but the log says
`video=960x528@30 declared=30`: the pipeline had clamped the request to the
camera's cadence before it ever reached the reader. **The request was never
made**, and the measurement that measured nothing accused the documentation. It
is the session's second instrument defect of the same family, after a PowerShell
cast that swallowed exactly the runs that crashed.

**And it is not the pin's doing at all, which the third machine settled by
running the same thing without it.** On the Lunar Lake laptop, with the Brio that
scales there and with its own camera, a plain size change — no pinning — gives
**84 frames of 290 under 500 bytes, 29%**, and 81 of 271, 30%, on the other step.
So the repetition is what a **size change** costs on those cameras, today, in the
product. The pin did not introduce it; it only made it visible on a machine where
the size change used to fail outright.

**Three measurements bound what it depends on**, and no fourth was needed:

| | camera | size change | repeated |
|---|---|---|---|
| Lunar Lake, either camera | NV12 native | **none** | 0 of 191, 0 of 268 |
| Lunar Lake, either camera | NV12 native | 960x528 or 640x352 | **29%, 30%** |
| AMD laptop, ACER | YUY2 only | 960x528 | 2 of 528 |
| the failing machine, Brio **pinned** | NV12 native | 960x528 | 33 of 290, **11%** |

The cameras that repeat are the ones whose pixel format already matches, where
nothing is converting until a **resize** is asked for and the processor appears
with it; the ACER has a converter in the chain from the first frame, for colour,
and it does not. And what decides how many are repeated is the gap: those two
deliver 13.3 and 18.5 fps against the 30 written into the output type, the ACER
26.

**So the fault was the product's and it was on**, on every camera where a size
change is what puts the processor in the chain: the scale comes down because the
bandwidth is short, and from that moment a part of what goes out is frames that
carry nothing.

**Microsoft ships the switch, and it is now the normal behaviour.** Of
`MF_XVP_DISABLE_FRC` the documentation says *"if this attribute is TRUE, the
video processor will not perform frame-rate conversion. By default, the video
processor will convert the frame rate to match the output media type"* — the
second sentence being the defect, stated by whoever wrote it. It is set on the
transform's own attributes, and the transform is reached with
`IMFSourceReaderEx::GetTransformForStream`, which is on the interface added for
the pinning experiment: the road out was already half built by accident.

**Measured at a size change on all three machines, with the call and without:**

| | camera delivers | bitrate | repeated |
|---|---|---|---|
| NVIDIA + Brio, pinned | 13.7 fps | 1490-1530 → **1037-1041** | 8-13% → **0** |
| AMD + ACER | 17.5 fps | 1579 → **1269** | — |
| Lunar Lake, integrated | 27.0 fps | 2369 → 2383 | nothing to gain |

**The gain is exactly what the camera is missing from the cadence we declare**,
so it is largest in the dark and zero in daylight — which is the right shape for
this program, since the dark is when it works. On the first machine it cost
**1.2 points of quantiser**, 22.2 against 23.5, that is, the picture unchanged
for a third of the bytes.

Three things that are not obvious:

- **It is asked for after the size change**, because the transform does not
  exist until then — the reader inserts it when it is asked for a type it cannot
  serve from the device. The documentation asks for the attribute *"before
  streaming begins"*, which cannot be done from here, so what says it took is the
  frame count and not the `HRESULT`.
- **The other road does not work.** Leaving `MF_MT_FRAME_RATE` out of the output
  type altogether takes the repeats from 12% to 8% and no further: the processor
  converts to something whatever we omit. It was written, measured against this
  one in the same session, and removed.
- **The byte instrument under-reports on AMD**, and the frame count is what
  holds everywhere. On the ACER the switch removes a quarter of the frames while
  fewer than 1% of them were under 500 bytes: that encoder spends bits on a
  frame identical to its predecessor where NVIDIA's emits nearly nothing — which
  is the same "AMD prefers to spend rather than approximate" this file already
  records. **A detector calibrated on one vendor is not a detector.**

**What does hold without any of that is that the pin is safe to try**: it is
asked for, its outcome is written to the log, and a refusal changes nothing
about the capture that follows.

**The remedy's shape, if the pin ever fails, is not new**: `bitrateSeen`,
`keyframeSeen` and `kfDeclaredBad` are the same rule written three times — **try
the road, weigh the effect, and stop taking the one that failed**. What the
eight-minute measurement above adds is that giving the road up entirely is
liveable: the cadence steps work on such a camera, because they change no size,
and they are what the monitor fell back on by accident after a minute of
blackouts.

**And the monitor did not crash**, which is the other thing that had only been
inferred. Eight minutes, five capture restarts, five encoder teardowns, a viewer
connected throughout and a clean `shutdown complete` — while `pat-capture` dies
at teardown about one run in four on the same machine.

**And that "whatever it is" now has a name, given by a control and not by a
log.** Forcing the encoder with `-prefer`, fourteen runs each in one session on
one camera: the **NVIDIA H.264 Encoder MFT** dies **6 times of 14**, the
Microsoft software encoder **0 of 14**. It always dies at the very end — the
output stops after the last status line and before the report — so the event is
the **process exiting** with that encoder having been used, not a teardown as
such, which is why five mid-session teardowns went through it untouched.
Windows names nothing: no WER event in seven days that carry five crashes from
other programs, and `LocalDumps` set for that binary alone collected **no dump**
across seven deaths, because the process does not die through the
unhandled-exception path at all. What was left open was the monitor's own
exit, and the chapter **When the process is ending, the encoder is not
released** is where it was closed: 0 of 40 orderly shutdowns, against 4 of 20
before.

**And the first diagnosis of it was wrong, which is worth more than the
finding.** Both symptoms — a cadence of 14.5 fps against a declared 30, and two
scaling sizes refusing to open — appeared together on a camera plugged into a
slow port, and the report attributed both to the port. Changing the port moved
the cadence to 19.4 and left the scaling failures **byte for byte identical**.
Two symptoms that arrive together are not two symptoms of one cause, and the
only thing that separates them is removing one cause and looking again.

### To change size the encoder is rebuilt, and only the SPS says so

Reconfiguring a running encoder works for the bitrate and **does not work for
the pixels**. It is the most insidious fault met so far, because every single
declaration said otherwise: `SetOutputType` with the new size answered `S_OK`,
`GetOutputCurrentType` read back **960x540**, the Source Reader really delivered
the new pixels — and the stream went on carrying the SPS of **1280x720**, the
same one, for seven keyframes in a row.

A pipeline in that state sends the browser frames that do not match the
parameters it decodes them with, and none of our checks noticed: reading the
output format back, which had been added for the purpose, **lies too**. The only
witness that tells the truth is the SPS in the stream, because that is what the
decoder really reads — which is why `media.SPSSize` exists.

At start-up the size is always exact. So the scale **rebuilds the encoder**
(`newEncoder` inside `runVideo`) instead of reconfiguring it. Measured: new SPS
in the stream **150 ms** after the request, video never interrupted,
`profile-level-id` unchanged because the level stays fixed at the full-pixel
one. It is the bitrate lesson one step further on: **the check must be taken as
close as possible to whoever consumes the data.**

**And one step further up: the governor is not the authority on what is going
out.** The scale deposits a request and never rechecks it, because `target`
commands **only when the step changes**: a lost request produces no second
attempt. The two ways of losing it are both real — the pipeline giving up, and
the **capture restart**, which starts again from the preset and tells nobody.
From that moment the governor reasons about a step that does not exist, and the
consequence is not cosmetic: a false `atFullSize()` **switches off the saving
and nails the bitrate to the ceiling** while full pixels go on the wire, for the
whole session. The hub now reads `VideoFormat` — what the pipeline is really
sending — and `scaleGovernor.resync` realigns once the settling window has
passed. **The cadence to compare is the delivered one**, not the declared one,
which chases the camera on its own account; and **a size that is not a step is
not invented**, because it can come from a skewed read — size and cadence are in
two different atomics — and the right answer is to wait for the next turn.
Covered by `scale_test.go`.

### The quantiser is the only number that anticipates blocking

`MFSampleExtension_VideoEncodeQP` on the outgoing samples says how coarsely the
encoder encoded **that** frame. It is the most honest number it produces,
because it is not the answer to a question of ours but a property of the work
done.

It is needed because everything else arrives late. Bandwidth says how much
passes, not whether it is enough: a bitrate that covers a dark, still room does
not cover the same room when the child moves, and that is where the blocks show.
No constant we write can know how difficult the current scene is; the QP knows.

**Measured on Quick Sync**, 720p, half-light, with `pat-capture -br N` reporting
the distribution:

| asked | produced | mean QP | p95 | max |
|---------|----------|----------|-----|-----|
| 2500    | 1581     | 28.8     | 30  | 32  |
| 1200    |  726     | 31.4     | 34  | 38  |
|  600    |  371     | 34.7     | 37  | 43  |
|  300    |  207     | 38.0     | 42  | 46  |

Two things to read in there:

- **The thresholds are not copied from libwebrtc.** They use 24 and 37 with
  openh264 and **28 and 39** with VideoToolbox, for the same codec, because they
  depend on the encoder. Here at the preset we are already at 28.8 on average,
  so a 24 would never fire: our numbers say clean sits around **30** and the
  break beyond **38**.
- **In CBR the encoder under-produces considerably on a still scene** (1581 out
  of 2500) and **does not use the rest to improve quality**: it stays at QP
  28.8. The saved bitrate does not become sharpness.

The **distribution** is kept, not the mean: the mean of a night hides exactly
the few seconds in which somebody moved, which are the only ones in which the
watcher sees blocks.

The GUID does not come from headers on disk because the SDK is not installed on
the development machine: it was cross-checked between the mingw-w64 headers and
the ones Microsoft publishes in `win32metadata`, two independent sources that
agree.

### The resolution scale is commanded by the quantiser, not by the bitrate

**No bits-per-pixel constant can express this rule**, and the measurement that
proves it was taken with somebody really moving in front of the camera:

| scene          | bitrate | mean QP | p95 |
|----------------|---------|----------|-----|
| still          | 2418    | 29.5     | 32  |
| moving         | 2422    | 30.3     | 32  |
| moving         | 1091    | **38.5** | 42  |

At full bitrate motion costs almost nothing. But **at the same bitrate** that on
a still room gives QP 31 — a perfect image — the same room with somebody moving
goes to 38.5, that is, visible blocks. Seven points at equal bitrate: bandwidth
says how much passes, not whether it is enough, because it does not know how
difficult the scene is. The quantiser does, which is why libwebrtc also drives
resolution from there.

So: **the QP brings it down, bandwidth remains the constraint for climbing
back.** Above 38 it drops a step and looks again — unlike bandwidth, the QP does
not say by how much one is out, only that one is. To climb back both things are
needed: bandwidth with 40% margin **and** QP below 30. The second condition is
not the first reversed, and it is computed: the step above has three quarters
more pixels, which cost 4-5 points of QP, so starting from 30 one arrives at 35
and stays below the break; starting from 36 one would end beyond it and come
straight back down.

After a size change, **four seconds** are allowed before listening to the QP
again: the encoder has just been rebuilt and its first frames carry a keyframe
and a transient, not a difficult scene. Without that pause the scale plunges to
the bottom reading its own restart.

**The thresholds are a fixed number — break at 38, climb at 33
(`internal/rtc/qpthresholds.go`) — after an attempt to learn them.** The idea
came from a true fact: the absolute QP at equal visual quality depends on the
chip. So the quantiser the machine produces when bandwidth is plentiful was
measured, and the thresholds were distances from there. **But what it learned
was the room.** In a single evening, same machine and same encoder, the
reference went from 20 to 31 — **eleven points, that is, the whole break
margin**, and in the worst direction: in the dark the scene is cheap, the
reference falls and the threshold becomes **stricter exactly when the image is
good**. The difficult scene is precisely what the thresholds must detect: **a
reference that absorbs it is not a calibration, it is the sensor chasing the
signal.** The fixed number is not arbitrary: 38 is where **three independent
measurements** put the break — blocking measured at 38.5 on Quick Sync, and
libwebrtc's two values, 37 and 39.

Two defects found while that reference existed, of a category that recurs:

- **The first seconds of an encoder do not describe the scene, they describe an
  encoder starting** (on AMD: 48, 46, 38, then settling at 29). They entered the
  reference arithmetically and invisibly: the mean initialised with the first
  sample and every subsequent one moved it by a thirty-second, while ten were
  enough to publish the thresholds — so the threshold was born at **52**, above
  the maximum of 51, that is, the resolution scale switched off without saying
  so. **Asking for fewer samples than the mean needs to forget the first means
  publishing a threshold derived from a value nobody looks at any more.**
- **The comparison is `>=` and not `>`.** Live, the scale did not fire and the
  obvious conclusion — "the threshold is one point too high" — was wrong: what
  blocked it was the reference chasing the motion. **Before declaring a
  threshold mistuned, look at which side the equals sign is on.**

**Verified live**, in the dark, with a real viewer and 1100 kbit/s imposed: `qp
33 → 35 → 36 → 38` and then video at 960x528, new SPS in the stream 160 ms
later, and the quantiser back to 31. **Nothing else would have decided**: the
estimate was 1222 kbit/s while we were producing 1100-1300, that is, an echo,
and the bandwidth branch was silent — without the QP the image would have stayed
blocky for the whole duration of the motion, with the status page declaring all
well. **One step was enough**, and **no cascade**: the four-second pause does
its job. The size **did not climb back**, and that is the rule and not a defect:
720p30 asks for `1280x720x30x0.04 = 1106` kbit/s and with the margin needs
**1548**, while the bandwidth was nailed at 1100.

#### The quantiser has a veto over the bandwidth descent

**The rule of this chapter was written down and was not in the code.** "The QP
brings it down, bandwidth remains the constraint for climbing back", and bits
per pixel are a **fallback** for encoders that do not declare the quantiser: in
`scale.target` the bandwidth branch came down anyway, with a good reading in
hand and never looking at it.

What it cost, measured on AMD with two viewers in the house and the room still,
**four descents in twenty minutes**:

```
06:41:51  bitrate matched to quality  kbps=300  cap_kbps=2500  qp=27  produced=654
06:41:51  video format changed  640x352  qp=27  break_threshold=38  estimate_kbps=382
```

`qp=27` is **six points better than the target and eleven below the break
threshold**: the image had never been in danger. Losses 0.0% throughout, and
twenty seconds later the bandwidth re-measured at 2696 kbit/s. The price is not
the descent, it is the return: one step at a time with twenty-second dwells, so
every false alarm is worth **a good minute of small image** — and it happened
every few minutes, because a still room is the normal condition of the night.

**Why no tuning could have been enough.** The quality loop does what it exists
for — on an easy scene it asks for 300 kbit/s instead of 2500 — and gcc can
measure only the traffic that passes in front of it: its estimate settles onto
our 654 kbit/s and **oscillates** there, because at low bitrate with a
two-second GOP the stream is bursty and the delay branch reads the burst as a
queue. Observed: 1212, 1305, 1034, 650, 908, 1127, 382 in seventeen seconds,
with losses at zero. Any threshold on a ratio between estimate and throughput is
sooner or later crossed by an oscillation like that: the first correction took
the margin from 2% to 5% and the fault came back ten minutes later. **The
quantity was wrong, not its tuning.**

The rule is therefore: **if the quantiser declares a healthy image, no bandwidth
estimate may take pixels away from it.** The threshold is the **climb** one (33)
and not the break one (38), and the two halves hold together by construction: if
the image is good enough to authorise one more step, it is too good to lose one.
Between 33 and 38 bandwidth speaks again, and that is the band in which the
image really starts to suffer.

**What is not lost is the multi-step descent on a real collapse**: on a collapse
the quantiser rises within a couple of seconds, measured from 28 to 37, and from
that moment the veto is gone and bandwidth jumps to whatever steps are needed in
one go. The softening transient already measured is paid, and that is the right
direction to err in against a minute of small image at every still room.

Three details not visible from reading the condition:

- **Shortfalls are not counted while the veto is in force.** Counting them
  anyway would mean holding the counter full through a whole still room and
  making the scale plunge the instant the quantiser brushes the threshold, that
  is, a descent decided by minutes in which nobody had measured the network.
- **The climb does not go through the veto**, and must not: it is the branch
  that brings the image back to full size, and an echo there is a lower bound.
- **The veto does not look at settling, and that is a choice.** Adding a
  `!settling` looks prudent and breaks an older invariant — no wait may hold
  back a descent — which `TestTheScaleDoesNotHoldBackDescents` already defended.
  Inside that window the quantiser carries the keyframe of a just-rebuilt
  encoder, so it is high and the veto does not fire by itself.

**The natural objection to the veto — and why it does not hold.** "If the link
cannot carry 300 kbit/s and the scene is easy, the quantiser stays at 25, the
veto never lifts and 720p is never abandoned: and that is exactly what the scale
is for." The second half is false: **on this program the scale does not reduce
by one byte what is sent.** The encoder is asked for a bitrate in CBR, and that
number does not depend on the size: at equal requested bitrate, fewer pixels
mean **the same traffic with a better quantiser** — and the only point at which
size touches the bitrate is `atFullSize()`, which below full size **switches the
discount off**, that is, makes it ask for more. Removing pixels is never the
answer to congestion: it is the answer to a **broken image**. A network that
cannot carry what we are sending says so by losing packets, and that road is
intact.

Covered by `scaleveto_test.go`, which uses the real sequence from the log and
begins by **demonstrating the defect** — with the quantiser declared unknown,
the same sequence brings the scale down.

**And the veto reads a window, not the instant.** The quantiser arriving here is
the p90 of **one** second and the GOP lasts two: on the instant the veto
switched on and off on alternate seconds, and since it also resets the shortfall
counter that counter **never reached three** — the confirmed descent became
impossible and only the collapse got through, that is, the veto broke precisely
the half it claimed to leave intact. Two readings are enough to remove the bias,
four damp the rest; the break threshold goes on reading the instant, because
**you damp where you decide, not where you measure.**

#### What arrived, the network carried

**The veto protects the pixels and does not protect the ceiling**, and the
ceiling is the worse fault of the two. Observed on AMD with the room still for
some minutes:

```
07:03:22  cap_kbps=350  produced=570  qp=28  loss=0.0%
07:03:23  motion in the room  fraction=0.1178      <- somebody really moves
07:03:23  qp=38     07:03:24  qp=49     07:03:25  qp=43     07:03:26  qp=41
```

**No bitrate command in the whole sequence**, and the reason is exact: the
motion jump gives back the **discount**, and it holds "only if it was the loop
that lowered it" — `current < cap`. Here `current` was 350 **and it was the
cap**, so there was nothing to give back. Motion does not take one to the
preset: it takes one to the ceiling, and the ceiling had collapsed.

How it got there: the estimate echoed our reduced traffic, the echo pulled the
ceiling down, the ceiling reduced the traffic, and the reduced traffic confirmed
the estimate — **with losses at 0.0% throughout** and a link that seconds
earlier measured 2696 kbit/s. The room moved and the image broke because of a
rule of ours.

The rule that breaks the trap has no threshold to tune, because it is not an
estimate: **if those packets arrived, the network carried them.** What we
delivered is a **demonstrated** lower bound on capacity, and no estimate can
take us below it (`atLeastWhatWeDelivered`). It holds for the ceiling and the
scale together, because both read the same number.

**Losses remove it, and that is the half not to lose**: when the receiver
declares it did not receive, what we believed delivered was not. That is the
cellular case already measured — estimate at 300 while we sent 2500, with
**33%** of packets lost — where the ceiling must be able to come all the way
down. Below 1%, which is the noise any link produces, the floor stays.

**The floor is not a target**: it raises an estimate that sits below, it does
not lower one that sits above, and with the throughput unknown it raises nothing
— "I do not know" is not evidence. Covered by
`TestTheEstimateNeverGoesBelowWhatWeDelivered`, `TestLossesRemoveTheFloor`,
`TestAnUnknownThroughputProvesNothing`, and by a guard that reads the source:
that line is inside a loop, so no test on the value would notice its absence.

### Below the last size, frames are removed, not more pixels

Below the last resolution step there remained a mushy image at full cadence: the
bits spread over twenty-five frames a second in which nothing can be made out.
**At night, cadence is worth less than detail** — whether the child is there,
which way they are turned, whether their face is covered, are questions five
sharp frames a second answer, and two still do, while twenty-five mushy ones do
not. The audio is untouched in every case: it stays
full for the whole descent, and it is the other half of the warning.

The cadence steps sit **only at the bottom**, all on the last size: the
resolution scale is tuned and measured, and interleaving new steps would mean
reopening its tuning for a rare case.

- **Dropping frames is not enough: the cadence must be declared to the
  encoder.** If it believes it is working at 25 fps it divides the budget by 25
  and every frame stays as approximated as before — the same mushy image, only
  rarer. So a cadence step **rebuilds the encoder** like a resolution one.
- **Dropping cannot mean going back to the top of the loop.** An asynchronous
  MFT's request is worth one time only: whoever throws it away waits forever for
  the next. Read until a frame to deliver is found, and answer every request
  with exactly one delivery.
- **Frames are dropped here, the camera is not asked to slow down.** A cadence
  asked of the device can be refused or rounded, and to what value is then
  unclear; counting them ourselves it is known by construction.
- **The GOP is kept in seconds, not in frames.** Sixty frames at one frame a
  second is a keyframe a minute, and whoever opens the page waits a minute.
- **The ladder is five steps, and it is written out rather than described.**
  From a 720p30 preset: 1280x720@30, 960x528@30, 640x352@30, then **5 and 2**
  frames a second on that last size — intervals of 200 and 500 ms.
  `scaleCadences` holds those two values, and the floor is one of them rather
  than a case added after the loop.

  **It used to hold divisors, and a fraction of the starting cadence is a
  fraction of a number the camera chose.** That cadence is not the preset:
  `cameraFPS` takes the highest rate the device declares at or below it and falls
  back to the lowest it has, so the bottom of the quality ladder moved with
  whatever was plugged in — 15 and 6 on a 30 fps webcam, 12 and 5 on a 25, 12 and
  4 on a 24. Nobody picked those, and most of them are not in `cadenceSteps`, so
  a cap the scale imposed was a number the declaration could not say. Two values
  chosen here are the same two on every camera, and
  `TestTheScaleOnlyImposesCadencesThisListKnows` keeps the two vocabularies one.

  **And the first rung bought nothing on the night this program is for.** At
  night the 15 removed **no frame at all**: the gate drops one only when the
  wanted cadence is below the incoming one, and in the dark auto-exposure has the
  camera at 9.8-20 fps. The declared cadence was already 10, so a cap of 15 sat
  above that too — both halves inert, in the one condition this program exists
  for, at the price of an encoder rebuild.

  **And two documents described the old ladder wrongly, both at once, both
  flatteringly.** This file said *5 and then 2 a second*, a mistranslation of the
  original *meta', un quinto, e un fondo a 2 fps* in which the fifth became a
  five and the half vanished; this chapter said *a sharp frame every two
  seconds*, which is **2 fps read as one frame per two seconds**, a reciprocal
  taken backwards. Both of them named a monitor more frugal than the one that
  existed — the direction this chapter argues for — which is why a year of
  readers nodded at them. The tests all
  passed throughout, because they ask **properties**: the sizes fall, the cadence
  only falls at the bottom, the bottom is `scaleMinFPS`. **A property test cannot
  be read as a list**, and what a reader wants from a ladder is the list:
  `TestTheLadderIsWrittenOut` is that, and it fails if a step moves, because
  these numbers are quoted in three places outside the code. The wrong sentence
  turned out to describe the better design, and the design is now that.
- **The floor's limit comes from outside**: it is libwebrtc's
  `kMinFramerateFps`, whose `balanced` mode stops much earlier. And a browser
  counts a **freeze** when the interval exceeds `max(3 x mean, mean + 150 ms)`:
  at one frame a second a single late frame is already a declared freeze, and
  the watcher's statistics stop distinguishing our saving from a fault.
- **The saving is very sublinear, and it is measured now**: at low cadence
  prediction fails and every frame costs nearly as much as a keyframe, so a
  `MinKbps` proportional to the cadence asks for **half** of what those steps
  need. Swept on the Lunar Lake laptop at 640x352 with somebody moving in front
  of the camera, reading the bitrate each cadence needs to hold a quantiser:

  | qp | 30 fps | 5 fps | 2 fps | bits per pixel, and the ratio |
  |---|---|---|---|---|
  | 32 | 600 | 185 | 80 | 0.089 · 0.164 (**1.9x**) · 0.178 (**2.0x**) |
  | 33 | 507 | 150 | 70 | 0.075 · 0.133 (1.8x) · 0.155 (2.1x) |
  | 34 | 413 | 133 | 60 | 0.061 · 0.118 (1.9x) · 0.133 (2.2x) |
  | 35 | 320 | 117 | 56 | 0.047 · 0.104 (2.2x) · 0.125 (2.6x) |

  **The ratio transfers, the absolute figures do not.** Every row sits above the
  0.04 constant because that was tuned on a still scene and this one moves; what
  is stable is the last column — about **twice**, at every quantiser level and at
  both cadences. Hence `cadenceBitsPerPixel = 0.08`, one coefficient rather than
  two numbers, and the bottom of the ladder goes from 45 and 18 to **90 and 36**.

  **What it cost to have it wrong was the climb back**, not the descent: leaving
  2 fps asked 40% more than 45 kbit/s — 63 — against a real need around 150, so
  the picture broke and the quantiser sent it straight back down.

  **And the scene has to move.** On a still room consecutive frames resemble each
  other at any interval, the cost per frame comes back flattering, and the
  measurement confirms the formula it was there to correct.

  **The first answer was 5.5x, and it was an artefact.** The full cadence had
  been read from the report's mean quantiser and the two low ones from the median
  of the per-second p90s — two statistics five points apart, compared as though
  they were one. What caught it was re-running the full cadence through the same
  road as the others. **A ratio between two quantities measured differently is
  not a ratio**, and the cost of not noticing would have been a constant nearly
  three times too large, in the direction that keeps the monitor off the steps it
  needs.

  **And the instrument had to be the scale's own road.** The first sweep used
  `-fps 2`, which asks the **camera** for two frames a second — a thing the
  monitor never does, because a cadence asked of a device can be refused or
  rounded to a value nobody knows. The faithful flag is `-fps2`, which drops the
  cadence at the gate and leaves the device where it is.

  **What that sweep did fail with was not the cadence, and saying so was a
  wrong measurement accusing somebody else.** It opened the device at
  `640x352@2` and then failed `ReadSample` within a second, four restarts
  running, and that was written here as the camera refusing the rate. Asked
  again with nothing else holding the camera, the same command runs clean: 21
  frames, 1.6 fps, no restart. **What it had met was contention** — a
  `pat-capture` left alive on that machine by a remote run whose connection
  dropped mid-sweep. The same `0xC00D3704` came back an hour later on another
  machine, from a monitor still holding its webcam, which is what named it.

  **A measurement run over a dropped connection leaves a process behind**, and
  the next measurement on that machine measures the leftover. The cheap guard is
  to ask what is running before believing a device error.

**Cadence is commanded by bandwidth, and there is no detection hooked to it.**
Axis Zipstream and Frigate drop to nearly zero on a still scene and on motion
"the camera **immediately** returns to 30 fps", but their problem is different:
they lower the cadence **to save** on a link that would carry more, so on motion
they have everything to give back. Here cadence drops only on the last size and
only because the bandwidth is not there: on motion nothing is being held back,
and raising it on command would mean sending more bits than the network accepts,
that is, lost packets.

`internal/detect` remains the only signal that says "bandwidth is about to be
needed" **before** the encoder produces the bits, and what it commands is one
thing: **it makes the loop give back the bitrate discount.** Not the cadence.

Detection still sees **every** frame, including those the encoder never gets:
there is no reason to notice something moving later just because the network got
worse.

The reduced cadence **is declared** on the status page, and only when it is
reduced: an image refreshing every two seconds with nothing saying so is
indistinguishable from a jammed camera. `VideoDropped` counts the frames dropped
deliberately, which is the other number that tells the two apart.

**Bits per pixel remain as a fallback** for encoders that do not declare the
quantiser: the steps are full size, three quarters and half, and each one's
threshold is how much falls to each pixel — so the rule still holds if one day
it starts from 1080p, with no second list to keep aligned. The value, 0.04, is
tuned on a still scene and **underestimates motion**: that is why it is a
fallback and not the criterion.

The asymmetries, in `internal/rtc/scale.go`:

- **It comes down at once and to the right step**, skipping those in between,
  and no wait may hold back a descent. Coming down in stages looks prudent and
  is not: the bitrate falls in one go — it must — and while the pixels stay as
  many as they were the image is blocky. On a 2500 → 300 collapse that was
  twenty seconds of mush produced by **a rule of ours**, not by the network.
- **To climb back, 40% more than the bandwidth the step above requires**, one at
  a time, and a step lasts at least twenty seconds: that is where a lucky
  estimate for an instant brings one back up too early.
- **A rising estimate is not low bandwidth: it is bandwidth not yet
  discovered.** gcc starts cautious and takes about ten seconds, and the climb
  passes right through the 720p threshold — observed on wifi, the scale went
  down to 960 and a few seconds later climbed back, that is, a change of image
  shape at every page opening. The eight-second warm-up covers the bitrate and
  **is not enough here**, because a change of size shows and an adjustment of
  sharpness does not.
- **A small shortfall is confirmed over three samples, a collapse is not.**
  Brushing the threshold is almost always noise; halving it is a collapse, and
  there waiting means keeping the image broken deliberately.
- **A collapse is a fall, so it does not exist while the estimate is rising.**
  Without this condition the scale plunged to the last step at the first sample
  of every viewer: the figure of bandwidth still to grow and that of bandwidth
  just collapsed are **identical**, and only where one is coming from tells them
  apart.
- **The starting size is a ceiling**, like the preset's bitrate, and not only by
  choice: the H.264 level announced in the SDP is fixed on that size — trying to
  climb from 360p to 720p the stream went on declaring 3.0, which at 720p30 is
  not enough.

Underneath it is always the same thing: **come down fast, climb back slowly.**
The price is a small image for some twenty seconds after a network hole, and
that is the right direction to err in — a smaller image can be watched, a blocky
one cannot. Covered by `scale_test.go`, and that is where it must stay: testing
the hysteresis over a network would mean hours of bandwidth worsening and
improving on command.

**The encoder being replaced must be protected for the whole duration of its
use, not only while its pointer is read.** An `atomic.Pointer` was enough while
the encoder stayed the same for the whole capture; since the scale **replaces**
it, whoever had just loaded it — congestion control every second, a PLI at every
loss — can find themselves holding an already-released COM object, and the first
method called on it takes the process out. On a baby monitor that is the camera
going off in the middle of the night, and it has happened. `encMu` is held for
reading to command and for writing to replace; the old one is closed **after**
releasing the lock. Go's race detector is **not usable here**: it wants cgo and
there is no C compiler on this machine, so concurrency bugs like this one are
found only by reasoning, or by suffering them.

**Rebuilding the encoder starts again from the last commanded bitrate, not from
the preset**, otherwise the maximum would be sent back on the wire exactly while
the image is being shrunk because the bandwidth is not enough. The value is
recorded in **both** the roads that command the bitrate — found because one of
the two did not.

