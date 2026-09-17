---
paths:
  - "internal/mf/**"
  - "internal/media/**"
  - "internal/encoder/**"
  - "cmd/pat-capture/**"
  - "cmd/pat-diag/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## Before predicting what the hardware will do, look here

**Almost every error made on this project has the same shape: a general model
applied where a particular measurement contradicted it.** Here is only the
verdict; the numbers live in the chapter where they were measured, because **two
lists of the same thing always diverge.**

| | Quick Sync | AMD | NVIDIA | MS software encoder |
|---|---|---|---|---|
| `SetBitrate` has effect | **no** | yes | yes | yes |
| rebuilding the encoder has effect | yes | yes (but has already killed the capture) | yes | **no** |
| the QP attribute is on the samples | yes | **no** | **no** | yes |
| the slice QP in the stream is real | **no**, constant | yes | yes | yes |

**NVIDIA answers like AMD on all four rows**, and on a fifth that is not in the
table because two vendors is not a pattern: `AVEncVideoForceKeyFrame` is
**honoured** there while `IsModifiable` answers `E_NOTIMPL` about it — measured
by asking for a keyframe a second for forty seconds and counting **40 in the
stream against the ~20 the GOP alone produces**. It is the second vendor to say
*I do not answer that question* and then do the thing, which is why the answer is
never read. The four rows themselves were not predictable from any of the other
three: the
cheap road to the bitrate works, so the expensive one is never needed; the
quantiser has to be parsed out of the stream, where it is a real number that
moved with the bitrate — 25.9 at 2500 kbit/s and 28.4 at 800 — rather than the
constant Quick Sync nails it to. Measured on an RTX 4080, driver 32.0.16.1664,
`baselines/capture-nvidia.txt`.

**And two things that belong to no row.** `AVEncMPVDefaultBPictureCount` is
**refused** there, `0x80070057`, so every start on that vendor writes `some
encoder settings were not accepted` — a log line and not an alert, which was
checked rather than assumed. It costs nothing, and the reason is the one the
quantiser chapter already gives: the imposition is the belt, and the braces are
`QPSlice` refusing a B slice outright — over 585 frames it read a quantiser on
every one of them, so that stream had no B slices to refuse. The other is the
delivery: **0% in bursts on all five streams**, against AMD's 32-36% on the
video, which settles that the pairwise delivery recorded in `capture-amd.txt`
is that chip's and not the pipeline's.

And on the Intel development laptop: WASAPI **raw capture is refused**, so it
ends up in exclusive mode, where the endpoint volume has to be reapplied by
hand; a noise-suppression APO **zeroes the microphone** on the shared path; the
default-microphone role can be **empty** with microphones present; the webcam
works **always**, which is why video faults have only surfaced on other people's
hardware; the Windows SDK **is not installed**, so the GUID check described
below cannot be redone there.

**This table does not authorise an `if` on the vendor.** It is for predicting,
not for deciding: in the code the rule stays what it always was — try the road
that interrupts nothing, weigh the effect, move to the other one if it had none.
A list of "Intel yes, AMD no" inside the program ages silently, and the next
user has a chip nobody here has seen. It can live here because it is
**documentation of what was measured**, and if it is ever incomplete it will be
incomplete visibly.

**And the list had grown anyway, in the README.** Under *Requirements* stood "an
H.264 encoder: Quick Sync, AMD or NVIDIA if present", which is one brand of a
**technology** beside two brands of a **company** — and it promised a chip
**nobody had run**. It has since been measured, and its numbers are in the
table above and in `baselines/capture-nvidia.txt`. Nothing in the program had a
vendor list; the document that describes the program did.
Requirements now say what is really required — an H.264 encoder, the hardware
one where there is one — and what was measured is named where the adaptation is
described, which is the one place a reader can use it. **A rule kept in the code
is not kept in the prose about the code**, and prose has no test.

