---
paths:
  - "internal/detect/**"
---

Part of PAT Monitor's engineering record — chapters of
**Invariants not to break**; the index that carries every chapter, in order,
is in `CLAUDE.md`.

### Motion is measured on the merged frame, with the camera's breathing removed

`internal/detect/motion.go`, on the analysis frames: 160x90 greyscale, five a
second.

**And "five a second" was true in daylight and false at night.** The pipeline
subsampled with `frameNo % (fps / MotionFPS)` — a divisor of the cadence the
camera **declares**, which is the number this record has a whole file about not
believing. In the dark auto-exposure takes the camera to 9.8-20 fps while the
format still says 30, so a divisor of six delivered **1.6 analyses a second**
against the five this detector is built on: a third of its frames, in the one
condition the program exists for, and the same condition in which the exposure
hunts — that is, exactly when the global-shift subtraction below has the most
work to do and the fewest frames to do it with.

Measured, with the divisor put back, at the cadences this record has recorded
from life:

| the camera delivers | divisor | time gate |
|---|---|---|
| 9.8 | **1.6/s** | 4.9/s |
| 13.7 | 2.3/s | 4.6/s |
| 18.4 | 3.1/s | 4.7/s |
| 27.0 | 4.5/s | 5.1/s |

The gate is the one the encoder's cadence already uses, `cadenceGate`, so
nothing new was written: it counts time instead of frames, delivers five a
second whatever the camera does, and passes **everything** when the camera falls
below five, which is the whole of what there is to pass. It is the encoder's own
lesson — *the cadence is declared, not assumed* — met a second time, on the
other consumer of the same frames, where nobody had looked for it.

**Half the work against noise is already done by that frame.** It is not a crop,
it is a **box average**: from 1280x720 that is 64 sensor pixels averaged into
each one, so noise divided by eight before anyone looks at it. That is why a
thirty-line detector can work.

Above it there is a first-order filter, one multiplication per pixel. **In 8.8
fixed point, and that is not elegance:** with 8-bit integers alone the filter
stalls as soon as the difference drops below four, and that fixed residue is
exactly where the noise it was meant to remove lives.

**Global shift is subtracted, and that is the protection that matters most.**
When auto-exposure changes gain **all** pixels change together, and in the dark
exposure hunts — 9.8 → 24.2 → 16.1 fps in twenty-six seconds — so without
removing it every breath of the camera would be motion, every night. **A change
that concerns the whole image is the camera, not the room.**

**An episode is announced once**: two thresholds on the value and twenty seconds
of quiet before rearming. Continuous motion has nothing new to say; moving again
after the room has settled does.

**The thresholds are provisional and declared as such**, and the log at `-v`
carries the two numbers for choosing them. In the light, a still room gives **0
exactly** and a person moving 0.0099 to 0.053 of changed pixels, with 22-47
levels of maximum: the floor is zero, so the threshold of 0.006 is cautious.
**And the measurement in the dark says the floor is zero there too**, which was
the fear this paragraph carried for months: auto-exposure raises the gain at
night, so the floor was expected to lift off zero and take the threshold with
it. Measured over one night on an ACER HD User Facing in an unlit room, three
hours in the middle of it and **2083 samples** of the `-v` line:

| fraction of changed pixels | worst per-pixel difference | samples |
|---|---|---|
| **0** | **0** | 2051 |
| 0 | 1 | 19 |
| 0 | 2 | 4 |
| 0 | 4, then 6 | 2 |

**Not one still sample produced a changed pixel**, and the worst per-pixel
difference of the night was **6 levels against a pixel threshold of 12** — half
of what it takes to count at all. So the threshold of 0.006 is not merely
cautious by day: at night there is no floor for it to clear. The three defences
are what pay for it, and they are all upstream of the threshold — the analysis
frame is a box average of 64 sensor pixels, which divides the noise by eight
before anyone looks; the first-order filter runs in 8.8 fixed point so it does
not stall on a residue of four; and the global shift is subtracted, which is
exactly the gain the darkness makes auto-exposure hunt for.

**What this does not say is that every camera behaves so**: it is one sensor,
one room, one night. The two real events in that window read 0.53 and 0.58,
which is somebody walking through — three orders of magnitude above a floor of
nothing. The two numbers also serve to tell
two zeros apart: a fraction of zero can mean "the room is still" or "I am
comparing identical frames", that is, a detector that is not plugged in, and the
per-pixel maximum separates them because on a live camera there are always a few
levels. **An event that never arrived and an event that was discarded look the
same from outside.**

**This is also where the signal comes from that makes the quality loop give back
the bitrate discount**, and a constraint on these thresholds follows:
**`StartRatio` also governs how much bandwidth is spent**, so a threshold that
is too generous does not merely produce a spurious alert, it sends the bitrate
to the cap for nothing.

**And a change of size is not motion.** It is the twin of the protection above —
there the camera's gain changes, here **where the pixels come from** — and it
was missing, because the guard watched the wrong thing: the analysis frame is
always 160x90, so its **length never changes**. What changes is the provenance
of each cell: from 1280x720 it is the average of 8x8 sensor pixels, from 640x352
of 4x4, and not even the proportions are the same because the steps round to the
macroblock. The result is a different image **everywhere**, and not being a
uniform shift the breathing subtraction does not touch it.

Measured over a night of log: **twelve events out of twenty** fell between 0.22
and 0.59 s after a format change, with the fraction between 0.0246 and 0.0275 —
three thousandths of spread over twelve episodes — while real motion in the same
log gave 0.0115, 0.0153, 0.0328, 0.1178 and 0.7047. A signature, not a
coincidence; the seven remaining changes without an event are those that fell
inside the twenty-second rearm, and the test, putting the defect back, reports
**0.0278**. It cost a false banner and a false chime every time the network
changed size, that is, at night, and for a defect of ours.

The analysis frame therefore **carries the size it came from** (`Sinks.Motion`
and `Motion.Feed`), and the detector resets when that changes. It travels with
the frame instead of being announced separately so that there is nothing to
remember: a new caller cannot forget it. One frame is lost, two hundred
milliseconds; **an episode already under way stays under way**, because the
image changed, not the room.

### Crying and barking are recognised by shape, and the floor is measured by the room

`internal/detect/sound.go`, on the analysis stream.

**There is not one dBFS of threshold in the code, and that is the point.** The
absolute level depends on microphone, gain and distance, that is, on three
variables that differ in every house: measuring it here would give a number
valid here. The thresholds are **distances from the floor**, and the floor is
measured by the room itself, so there is nothing to tune per installation.

**The two things that do not depend on us are band and cadence**, and they come
from physiology: a bark lasts 69-1000 ms and comes in bursts 0.1-0.5 s apart, a
cry is a long cycle of exhalation and pause, and both sit between 120 and 1640
Hz. Hence the non-obvious consequence: **frequency does not separate them.**
Only time does, which is why there are two criteria and not one made more
permissive. The only frequency information used is the **zero-crossing count**,
one comparison per sample: it says which decade one is in, and serves to keep
out the thud below 200 Hz and the hiss above 2500.

**What comes out is a "possible", and the interface says so** — with these means
one recognises a shape, not a species. Precision is `internal/ced`'s job, which
replaces the decider without touching the rest of the chain; the codes stay
`cry` and `bark`.

Three defects found by testing, not by reasoning:

- **A frozen floor, on its own, freezes forever.** Freezing it during the event
  is mandatory — otherwise a three-minute cry eats its own reference — but if
  someone switches on a fan the level rises twenty dB and stays there. The
  criterion that separates the two cases is not duration in itself: **neither a
  cry nor a bark is continuous**, so twenty seconds of sound without a single
  pause is neither, the floor moves up onto it and it starts again.
- **Digital silence is not a room.** The first blocks arrive before the
  microphone delivers anything and read -99: taken as the floor, everything
  after looks fifty dB above the room — measured, half a minute of blindness at
  every start.
- **Zero dBFS is full scale.** Until there was a measurement the fields stayed
  at zero, and the log declared full scale: "I do not know yet" reading as "it
  is blaring". Until it is known, declare silence.

**The switches say what is in the room** — crying, barking, motion — and they
live in the monitor's configuration, not in the browser: that there is a dog is
a fact of the installation, not a preference of whoever is watching, and two
parents with two phones must get the same answer. They switch off the **event**,
not its alert.

**And they do not switch off the sensor, which has a second reader.** The
bitrate discount that motion gives back **deliberately bypasses the mask**:
whoever switches off the motion alert because there is a cat in the house has
not asked for a worse image when the child moves. The mask lives in one place
and downstream, where the alerts are composed — and whoever adds a third reader
should decide which side they are on rather than inheriting it from how the code
happens to be written.

