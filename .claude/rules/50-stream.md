---
paths:
  - "internal/rtc/**"
  - "internal/media/**"
---

Part of PAT Monitor's engineering record — chapters of
**Invariants not to break**; the index that carries every chapter, in order,
is in `CLAUDE.md`.

### The media clock chases the real cadence, not the nominal one

`nextVideoDurationAt` in `internal/rtc/hub.go`. The limits on the inter-frame
interval are anchored to the **measured average**: anchoring them to the nominal
framerate creates a fixed ceiling (at a declared 30 fps that is 7.5 fps) below
which the declared time advances more slowly than real time and **the delay
starts growing again**. In the dark auto-exposure brings the cadence down a long
way. Covered by `hub_clock_test.go`, light/dark transition included; the
function takes the instant as a parameter precisely so it can be verified.

**The cadence shown to the user is counted over a recent window, not from the
start.** It was a cumulative average, and a cumulative average ages instead of
measuring: a webcam that started at 20 fps in the dark went on declaring 20 long
after the room had brightened. The symptom described the defect better than any
diagnosis: "if it starts at 20 it never goes up; if it starts at 24 or 27 then
it varies" — that is, the average moved only when it was already close to the
true value. Five seconds of window, and `measuredFPSAt` takes the instant as a
parameter for the same reason as the clock.

**A number that ages is worse than an absent number**: it sends someone looking
for a fault in the camera while the camera has already recovered.

**And the same sentence had a second half nobody had read.** That window is
recomputed **only when a frame arrives**, so the defect above was fixed in one
direction and left standing in the other: it would not go **up** after the light
returned, and it would not come **down** when the camera stopped. With the
frames stopped nothing closes a window, so the last closed one went on answering
— and a capture restart lasts up to half a minute, for all of which the status
page declared the cadence of before. A camera that has stopped, reported as
delivering twenty-four frames a second.

The gap reset in the media clock does clear it, but only when a frame arrives,
which is exactly what is not happening. So an **open** window that has outrun
its own length answers instead of the closed one: the frames there have really
been, over the time there has really been, which decays towards zero on its own
and needs no new state. In ordinary running it costs nothing — a frame closes
the window within a frame's time of its reaching five seconds — so the branch is
taken only when the frames really have stopped.

The second direction is the worse of the two, and that is why it is worth the
paragraph: the first **invents** a fault that has passed, the second **hides**
one that is happening. Covered by `TestTheCadenceDoesNotSurviveTheCameraStopping`.

### SDP constraints, all discovered as "codec is not supported by remote"

- H.264 **constrained** baseline, `42e0xx`. Plain baseline `4200xx` is refused:
  the constraint flags must be `0xE0`.
- **Two things are called "the level", and only one of them may only go down.**
  `media.PatchLevel` lowers the `level_idc` **inside the SPS**, and downwards
  alone: raised there, the stream would declare a conformance it does not
  respect. The **SDP** is the other one, and it may announce more than is sent —
  that field says how much capacity to allocate, and allocating too much breaks
  nothing. This entry used to assert the opposite, that announcing higher than
  sent fails in silence, and it contradicted the paragraph underneath it.

  **So the announced level is the preset's, not the capture's**
  (`rtc.Config.LevelIDC`, from `MinLevelIDC` on the preset's own numbers). Only
  the profile and the constraint flags still come from the SPS, because those
  describe the encoder and no preset can predict them. Measured on Edge with a
  viewer really connected, capture at 640x360@15:

  | announced | really sent | decoded |
  |---|---|---|
  | `42e016`, the capture's — as it was | 2.2 | 150 frames, **0 dropped** |
  | `42e01f`, the preset's | 2.2 | 151 frames, **0 dropped** |

  **The argument holding up the old shape had a false premise.** It said the
  frozen first value is the full-pixel one "because the capture always starts
  from the preset" — and `PickCameraSize` lowers the size wherever the camera
  has fewer pixels, so on a small webcam that value is the **camera's**. It held
  anyway, by luck, because the scale can only come down from where it started;
  it stops holding the moment the camera can change while the monitor runs.
  Taking the announcement from the preset makes the premise a fact instead of an
  assumption, and it is also the more ordinary value: 3.1 is what browsers
  negotiate for themselves.

  **And the claim that browsers take `42e01f` and nothing else was too strong.**
  It sat in the comment of a field nothing read, and the first row above
  disproves it: `42e016` negotiated and decoded. The measurement behind it was
  real and was taken at 720p, where the SPS gives 3.1 anyway — the conclusion
  had been carried to a case nobody had tried. What survives is the half that is
  true, as `TestEveryPresetFitsTheLevelBrowsersNegotiate`: no preset may need
  more than 3.1, because we would announce it with nobody having checked.
  **A claim living in the comment of a dead field is the worst of both places:
  the field cannot break and the comment cannot fail.**

  **The announced level still freezes, and does not chase the stream.**
  Rebuilding a smaller encoder, the new SPS carries a lower one — measured,
  `42e01f` → `42e016` at 640x352@6 and back on the way up. What the freeze
  guards is that `NewViewer` reads the value **at the moment of the offer**:
  whoever connected in the low window would negotiate the tight contract and
  then receive a stream exceeding it. It has happened — a session negotiated 2.2
  and received 3.1 for eleven minutes, and Chrome decoded it anyway. **"This
  decoder forgives" is not a guarantee: it is something discovered on somebody
  else's phone.** The alarm stays **non-directional**, because the case that
  matters is a stream rising above what was announced.
- Opus must declare **2 channels** in the SDP even if the stream is mono (RFC
  7587).
- CSP: `script-src` must be declared **explicitly**. Letting it fall back to
  `default-src` blocks inline scripts without saying so, and forms fall back to
  native submission, putting the password in the URL. No inline scripts.

