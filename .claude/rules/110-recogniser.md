---
paths:
  - "internal/ced/**"
  - "internal/gguf/**"
  - "internal/detect/**"
  - "cmd/pat-sounds/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## The sound recogniser runs here, and parity is demonstrated once

`internal/ced` runs CED-tiny — 5.5 M parameters, 527 AudioSet classes — in pure
Go, without a line of C and without a new dependency. The beaten road was ONNX
Runtime, and it **was measured and discarded**: 108-127 MB to download in order
to get a 12 MB DLL, with which to run a 6 MB model, and on this machine DirectML
turned out **slower than the CPU** (2.42 ms against 1.80 on YAMNet). The matrix
multiplier written here does 7.1-7.6 GFLOPS on one core, which on this model is
480-545 ms per ten-second window.

**The rate was not luck: it was the constraint.** The analysis stream is
obtained by averaging a whole number of samples, so from 48 kHz one arrives at
16 and **not at 32** — which is what EfficientAT wants. CED works at 16, and
that is why it is CED.

**It sits downstream of the gate, not in its place.** `internal/detect` looks at
every block and costs nothing; this wakes up when the other says there is
something. Running it always would mean half a second of CPU every ten, all
night, for a room that is still 99% of the time.

**Parity with the oracle cannot live in `go test`, and lives in
`baselines/ced.txt`.** It wants two things that are not in the repository and
must not be: the eleven megabytes of weights and somebody else's executable. A
test that went looking for them on the disk of whoever runs it would pass here
and be skipped everywhere — the same rule already paid for with the fake clips
written into the developer's Video folder. The number: **mean deviation between
4.0e-06 and 1.1e-05 across all 527 classes**, worst 6.7e-04, with the tolerance
of 1e-3 declared before looking. The oracle is `ced.cpp`, which we did not
write: it is the same shape of cross-check as `pat-opus`.

**What stays in the automatic tests is the layout**, not the values: where each
number ends up. It is the family of defects that a comparison with the oracle
catches late and as a black box, and that a tidy-up can reintroduce without
touching a formula.

**q8_0 costs a hundredth of a probability and five megabytes less.** Worst case
over 527 classes, between 3.2e-03 and 1.5e-02, with the top five in the same
order except a fifth place at 0.013. On an alarm threshold that is noise. **In
memory, though, they cost the same**, ~22 MB: the weights are converted to
float32 on open, so the saving is in the file and not while it runs.

### The model file carries its front end with it, and must be believed

Inside the GGUF there are not only the weights: there are **the window and the
mel filterbank** the model was trained with, plus the constants of the decibel
conversion. Rebuilding them by hand means guessing a convention, and a front end
that differs slightly gives a model that *almost* works — which here is the
worst fault, because it does not complain.

**And it is not an abstract fear.** Measured, CED's window is a 512-point Hann
that sums to exactly 256 but **is not symmetric**: the last sample is 0.000038
instead of zero, that is, it is the *periodic* Hann, which is torchaudio's
default and not the one written by instinct. Generating it by hand would get one
sample in 512 wrong with nothing saying so.

For the same reason **the half-precision conversion has a test written on the
hard cases, not the easy ones**. The denormals: the first version counted shifts
from a counter starting at -1 and was wrong by **a factor of four**, and wrong
**only there** — the normal weights stayed right. In a quantised network the
small weights are the majority: what would have come out is a model that runs
and answers badly, with everything green.

### Layout defects do not complain

Three points where the right code and the wrong code look the same, and no
compiler, no type and no runtime error separates them. The tests were verified
by putting the defect back.

- **Flattening the grid is frequency-first**, `pf*tp + pt`. Reversing it leaves
  the same tokens with the same values in another order, and attention is
  **not** order-invariant, because the positionals are already inside: what
  comes out is a model that runs and answers at random.
- **The time positional is cut, not stretched.** That is the property by which
  CED accepts different durations. Stretching it, with three steps available out
  of four one would use the first, the second and the fourth: no error, every
  token in a position that is not its own.
- **The two branches are added to the residual, they do not replace it.** The
  first version of the test zeroed the shifts too, and so **absolved a branch
  that was not added at all** — with a residual of zero, adding it and
  forgetting it are the same thing.

And a fourth that is in the formula and not the layout: **the GELU is the exact
one, with `erf`, not the tanh approximation.** Measured, the two differ by up to
**4.7e-04 on a single activation**, and there are 9216 activations per token
across twelve layers: on its own it eats the whole margin of the comparison with
the oracle, which over the full pass measures 6.7e-04 worst case. **A deviation
that sits below every test's threshold and above the comparison's is the
quickest way to spend a week looking for it elsewhere.**

### The signal's scale matters, because there are two floors

The spectrogram is clipped at the bottom at `maximum - 120 dB`, and that maximum
is **of this window**, so on its own it would absorb any volume. But underneath
there is a second floor, `amin`, fixed at 1e-10: **on a band containing nothing
it bites first.**

It follows that raising the volume does **not** shift the spectrogram by a
constant: it changes its shape. That is, a waveform in [-1,1] and the same one
in 16-bit integer units **do not give the model the same input**, and the
difference shows precisely where the room is flat — at night, which is when this
program works. The right convention is [-1,1], and it is **verified against the
oracle**, not deduced.

**And the relative clip decides the shape of everything else**: normalisation
depends on the whole window, so the front end **cannot work streaming**, frame
by frame. The ten seconds are accumulated and transformed together. Whoever
wrote an incremental API would discover it there.

### Above the training window there is a hole, and it is not patched

The model has seen 1012 frames, that is, **10.12 seconds**. Above that, the
original model splits into whole windows, pads the last with zeros and averages
the logits **at equal weight**. Measured on a repeated sine: 0.93 at five
seconds, 0.89 at fifteen, and **0.47 at 10.12**, where the second piece is one
frame of sound and 1011 of silence.

**It is not a defect of ours and it is not corrected**: correcting it would mean
separating from the oracle, that is, losing the only thing that says whether
this code is right. Windows of `Model.WindowSamples()` are delivered, not "about
ten seconds" — which is why that method exists and its documentation carries the
numbers.

**And the probabilities do not sum to one.** The last layer is a per-class
sigmoid and not a softmax: they are 527 separate questions, and a recording can
be a dog, a room and a television at once. Whoever takes the maximum is asking
the wrong question.

### Thresholds are measured on other people's datasets, not on the developer's house

A few recordings taken here would give thresholds right for **this** room, this
dog and this microphone, and wrong for everybody else — with no way of noticing,
because they would work beautifully where they were chosen. What is needed is
recordings labelled by others, many, and of different provenance: ESC-50,
UrbanSound8K and the FSD50K eval, which are the benchmarks models of this family
report. Twelve gigabytes, all from Freesound, all outside the repository. The
numbers live in `baselines/sounds.txt` and `pat-sounds` re-measures them.

**AudioSet, which CED is trained on, cannot be used**: its audio is not
distributed, only YouTube identifiers. That the three sets come from Freesound
is therefore a piece of luck and not a coincidence — they are material different
from the training data.

**Every extra class brings its own false positives**, and that is the first
useful result. The first list looked at everything that sounded related and took
the maximum; two entries were disastrous. "Domestic animals, pets" gives **0.877
on a cat**; "Dog" is the strongest on the positives and takes `brushing_teeth`
at 0.442. What remains is much narrower — `max(Bark, Bow-wow)` and `max(Baby cry
infant cry, Crying sobbing)` — and on ESC-50, at threshold 0.10, "Bark + Dog"
produces fifteen false positives where "Bark + Bow-wow" produces one. **The
difference is not the threshold: it is the list.**

**Probabilities are not comparable across classes**, so there is no single
threshold: one looks at the classes whose floor has been measured. On an empty
room the model is 38% sure it hears a mouse, and it does us no harm only because
we do not look at that class.

**Before believing a low recall, look at what it was fed.** On UrbanSound8K the
bark goes from 82% to 36% between foreground and background, and below one
second of duration drops to 29%: a dog three blocks away covered by traffic is a
legitimately labelled `dog_bark` clip and is not what this program has to catch.
On FSD50K it goes from 81% to 46% between clips that fit inside the window and
clips that exceed it, because there it is **our** code that splits and averages.

**And in a multi-label set a false positive can be a gap in the annotation.** On
FSD50K, all nine clips exceeding 0.20 without being labelled `Bark` **are dog
clips**, labelled `Dog` and nothing else: the true false positives are zero out
of 10,109. Counting them as model errors would have been a verdict on the
annotation.

**Crying and barking do not have the same support, and that must be said.** The
bark stands on three independent sets; the cry stood on **forty clips**,
ESC-50's, and now stands on **497 from two sources**. FSD50K has
`Crying_and_sobbing`, which includes adult crying and in whose vocabulary "Baby
cry" does not exist.

**And the reason the fourth set had been passed over was wrong.** It said the
specialised corpora classify **why** a child is crying rather than whether they
are — which is true, and beside the point: the labels are useless to us and the
audio is not. What was missing was positives, and a corpus labelled by cause is
a corpus of positives with its labels thrown away. `donateacry-corpus` is 457
of them, ODbL, fifty megabytes, no request to anybody. The second reason given
— that none of those corpora permits redistributing what comes out of them —
does not hold either: what leaves this repository is a table of numbers, and
the ODbL asks for attribution, which is a lighter condition than the CC BY-NC
of the two sets already in use.

**What it says is that forty clips were not a sample.** On 457 real cries the
recall at 0.20 is **79%**, against the 97% of ESC-50's forty; the median is
0.561. Two explanations were removed by experiment rather than by argument,
because all three predict the same number. **Not the band**: those recordings
are 8 kHz, and the same forty ESC-50 clips with their spectrum zeroed above 4
kHz still give 40/40 — removing the top half costs nothing and gains a little.
**Not the level**: the medians are 1.8 dB apart, and bringing every event to
-20 dBFS moves the diluted median from 0.204 to 0.220 while leaving ESC-50's at
0.324. What is left is the material — cries recorded by parents on a phone,
whose hard ones are quieter and sparser: the lost sit at -28.3 dBFS filling 39%
of their clip, the caught at -20.9 filling 52%.

**It is still not three sources.** One collection method, one kind of room, one
distance; the numbers are in `baselines/sounds.txt` with what remains.

**And it is not going to become three, which is a decision and not a
postponement.** It stayed open until the roads were counted, and the
counting is the result: everything freely downloadable is one of the two
provenances already here, or a copy of one — the corpus that looked most like a
third describes itself, in its own words, as *"built through the Donate-a-cry
campaign"* — while a provenance of its own means, with one exception, a form, a
request to an institute, or an email to an author. **The gate is the price of
the independence**: these are recordings of identifiable infants, made by
researchers with an ethics committee behind them, so a corpus that can sit on a
public URL is by construction one whose audio somebody else had already
collected from a library.

**And the exception is what makes that precise, so it is named rather than
rounded off.** Free and independent is not impossible: `CryCeleb2023` is both —
a click-through with no human behind it — and it ships manually segmented
**expirations** from newborns in one country, that is, the wrong granularity.
So the rule is not two-way but three: free and already here, free and the wrong
shape, or independent and behind a gate. Saying it in two would have been
tidier and would have hidden the one road that disproves it. It is the dB
anchor's shape, below — a road refused for a reason rather than for want of
time.

**And what one collection method leaves thin is the recall more than the
threshold**, which is two claims and not one. 0.15 was chosen where the trade
turns — 63 more real events for 8 more false ones, against 49 for 22 one step
lower — and **what turns is the cost**: the gain barely moves, 63 against 49,
while the price triples. That side is measured on some twenty thousand negative
clips per code, from **three** independent sets, and donateacry is not among
them.

**The gain side is another matter, and the honest figure is 20 of the 63.**
Those are donateacry's, that is, a third of what the move buys, so a third
source of positives could move the gain — by how much cannot be checked from
here, because the 49 of the step below was never broken down per set and the
CSVs it would take are not in this repository. What is claimed is therefore the
narrow thing: the **turn** is on the negatives, the size of the gain is not.

**And the 83% is recall on whole clips, which is not the catch rate in a
room.** There the event fills the recording; what answers for a room is the
dilution table below, and it answers much lower — a second of crying inside a
window is worth 0.025, and at the gate's own twelve decibels the recogniser
answers for a quarter of the cries. The 83% stays declared as what it is, cries
recorded by parents on a phone, close, indoors, and a fourth corpus of that
same shape would not repair it.

**What the search did turn up is a different question, and it is in
the open issues, where open questions go.** Every negative here comes from somebody
else's street or from Freesound, and `pat-sounds dilute` assembles an event at
the centre of a window over a floor measured on another evening — it
**simulates** the condition the monitor lives in. `HomeBank`'s de Barbaro corpus
is a recording of it, and what would be worth the form is not its seven hours of
crying but the other 54, home audio that is **not** crying from the same rooms.
That is not the provenance this chapter closes: it is the dilution curve, and
the two were confused here for a paragraph.

**And the room was measured separately, because no corpus of clips carries
it.** CSIBE-AIBO is the same material played through a speaker and recorded
again by a robot's microphones at 1 m and at 3 m, each in a reverberant and a
dry room — the axis the monitor actually lives on and which nothing else here
covers. At equal level the four conditions all sit above 0.20 at the tenth
percentile and their ordering does not hold together, which on eight
recordings is what it should be read as: **the room's acoustics cost little,
and what three metres really costs is 20.9 dB of level** — which is where the
other corpus loses its recall, and which the gate's twelve dB above the floor
already governs. The first version of that measurement reported a factor of
seven between dry and reverberant, and it was an artefact of cutting the clips
to a second and repeating them: the same treatment on ESC-50's forty cries,
which score 0.599 whole, gives **0.003**. **A manipulation that flattens
everything looks exactly like a phenomenon that flattens everything**, and only
putting the known case through the same manipulation tells them apart.

#### A short event inside a long window: the bark holds, the cry does not

Dataset clips last five seconds and the event fills them; the monitor's window
lasts 10.11 and a real event occupies a fraction of it. **That is the condition
the monitor works in, and no dataset reproduces it.** Measured by putting the
positives at the centre of a whole window, on a room floor of -46 dBFS:

| event | cry | bark |
|---|---|---|
| 5.0 s | 0.378 | 0.563 |
| 2.0 s | 0.173 | 0.561 |
| 1.0 s | **0.025** | 0.529 |
| 0.5 s | **0.006** | 0.461 |

Half a second of barking inside ten is still worth 0.46; one second of crying is
invisible. A bark is a burst with a signature of its own, a cry a long shape the
model recognises only if there is enough of it.

Two rules follow that no five-second clip could have shown:

- **The model is not asked at the instant the gate opens**, because there the
  window contains one second of event and nine of before. It is asked later.
- **For the cry the lever is time, not the threshold.** Raising it to 0.30 would
  cover ESC-50's cat (0.181) and UrbanSound8K's children playing (0.292), but
  positives diluted in a real room sit at the tenth percentile at 0.004 — the
  number was 0.166 while it was being read off twenty clips, which is to say
  off the second value in a list of twenty — so the case that matters would be
  lost. Instead the threshold must be exceeded on
  **two consecutive windows** — a cat miaows once, a child cries for minutes. It
  is the same thing that already separates cry and bark in `internal/detect`:
  only time separates them. For the bark it does not hold, being a short
  episode: demanding two windows would mean losing it.

**The worst confusion is a bedroom sound.** On ESC-50 `snoring` reaches 0.438
through the "Dog" class — that is, somebody snoring read as a dog. The model is
not wrong: on the same clip it declares "Snoring" at 0.975. What was wrong was
the list, and with `max(Bark, Bow-wow)` it drops to 0.036.

#### The gate is tuned the other way round, and no longer decides

`internal/detect`'s shape detector does not have to be right: it has to lose
nothing, because what does not pass it the model will never see. Measured with
`pat-sounds gate` — every clip at the same level above a settled floor, so the
dB threshold stops being the variable and what remains are the two things a
dataset can teach, **the band and the shape in time**:

- **the band is almost inert.** Removing it entirely costs **four points** more
  negatives and does not touch the positives, because nearly every sound has a
  zero-crossing rate inside 150-2500 Hz. Narrowing it to the 120-1640 Hz of the
  physiological literature costs **12.5 points of recall on the cry** to gain
  5.7 of rejection, which for a gate is the wrong direction. **It stays as it is
  because it is nearly free, not because it does much** — and knowing which knob
  moves nothing is worth as much as knowing which one moves.
- **what really separates is intermittent from continuous**, and there it works:
  rain 0%, idling engine 0%, air conditioner 2%, jackhammer 3%, washing machine
  7%. A rooster passes at 100% and a keyboard at 80%, and that is not a defect:
  distinguishing a rooster from a dog is the model's job.
- **the bursts are the lever, and they cost recall.** With a single burst the
  cry goes to 100% at any threshold and the bark gains twenty points, for eleven
  points more negatives.

**And the conclusion was applied together with the model.** While what the gate
declared became the alert, a single burst would have made 59% of a room's loud
sounds ring; now the decider is `internal/ced`, and the two bursts were twenty
points of events the model would never have seen. **The two knobs moved
together, and that is why they could not be moved before.**

**Distance from the floor cannot be taught by a dataset**: it is a property of
the room, not of the sound. The twelve dB rest on the only measurement that
concerns them, the -46 dBFS floor measured here with the real microphone.

**But what happens on the other side of the gate had never been measured, and
the two halves do not meet.** Walking an event down towards a room floor fixed
at -46 dBFS — `pat-sounds dilute -sweep` — at the gate's own twelve decibels
the recogniser answers for **92% of the barks and 25% of the cries**, and the
cry is gone entirely by six. The bark is still over the threshold at the room's
own level (52%) and below it (38%): a burst with a signature survives a bad
signal-to-noise ratio, a long shape at low contrast does not. So **the chain's
sensitivity is set by the model and not by the gate**, and for the cry it sits
between +18 and +24 dB, which is two octaves of loudness from where the gate
opens. It is not a reason to move the gate — a gate that let less through would
lose what the model can still catch — it is that one threshold serves two
detectors needing numbers twenty decibels apart, and only one of the two was
written down. **What it does not say is that a child cannot be heard across a
room**: it is a curve, and the other half would be how far above the floor a
real cry sits at three metres. The exchange rate for the distance is measured,
20.9 dB for three metres off-axis; the anchor is not.

**And the anchor is not going to be measured, which is a decision and not a
postponement.** It stayed open for weeks as "one number, and the last
link of the chain", and it asks for exactly the thing the first paragraph of
this chapter refuses: an absolute level, which depends on the microphone, its
gain, the room and the distance. **It is not even stable in one room.** The
floor here was measured at **-46 dBFS** on the evening the dilution curve was
taken and at **-32** on another, same house, same microphone array, same
machine — fourteen decibels between two evenings, which is more than the whole
margin the curve is about.

So a number taken here would be this room at that hour, and the temptation it
would create is the one this project has already paid for once: a threshold in
decibels, tuned where it was measured and wrong everywhere else. What is
transferable is what is already written — the model's sensitivity as a distance
from **whatever** floor the room has, and the exchange rate for metres — and the
product's answer to "will it hear the child" is not a constant but a placement:
near the cot. That belongs to the guided path, not to a measurement.

#### The model decides, and the chain has been walked by the air

`cmd/pat-monitor/recognise.go`. The shape detector opens the gate, the analysis
stream fills a 10.11-second ring, and what says what is in the room is CED.

Three rules, each from a measurement:

- **The threshold is 0.15 for both**, but the confirmations are not: the bark
  wants one, the cry two within thirty seconds. It was 0.20, and what moved it
  is the only thing that could: the negatives re-measured. They come from
  **three** sets and two had to be fetched again, 12.3 GB of them, which is why
  this used to say two — and the third is ESC-50, the one carrying the cat
  below. **Sixty-three more real events for eight more false ones**, over
  some twenty thousand negative clips per code — the cry from 363 to 383 of
  donateacry's 457 real cries, the bark from 660 to 698 of UrbanSound8K's — and
  the step after it is where the trade turns, 0.10 buying 49 for **22**. The
  knee was measured, not chosen, and it was checked on **both** codes because
  one number serves two. `baselines/sounds.txt` carries the tables.

  **What enters at 0.15 is nothing new in kind**: ESC-50's cat at 0.181, three
  `children_playing` on UrbanSound8K between 0.160 and 0.185, a door squeak and
  a gasp on FSD50K — every one a confusion the old threshold already had at its
  own edge, while the two worst negatives of all, a bird at 0.554 and a shouting
  man at 0.252, were through at 0.20 and are untouched.

  **Against the cat the lever is time**, and that is what the two-window rule is
  for — raising the threshold to 0.30 would cost the diluted cries, whose
  **median** is 0.204: on a hundred and fifty real positives half of the windows
  of a real cry already fall on the wrong side of 0.20 on their own, which is
  what makes the rule necessary rather than prudent, and 0.30 impossible.

  **And the rule's own argument was an argument and not a measurement**, which
  mattered more once the threshold had moved below the cat. *A cat miaows once* is a claim
  about hits, and the window is 10.11 s while the question is asked every 5: two
  consecutive classifications **overlap by half a window**, so one miaow is seen
  twice, and the two confirmations need not be consecutive anyway. What may
  still save it is dilution, which is brutal — one second of crying inside ten
  is worth 0.025. **It was then measured by air**, and the table below is the
  answer: one play of that cat gives one hit and no alert. The three
  `children_playing` are outside the rule's reach in any case: children playing
  are not an event that happens once.
- **It is not asked at the instant the gate opens**, and there is no written
  delay: the gate stays open twenty seconds and the question repeats every five,
  so the second classification looks at a window the event has filled. One
  second of crying inside ten is worth 0.025.
- **The verdict is consumed in the state's round, one round a second**, not in a
  callback. A second point from which the state is commanded is precisely the
  defect that produces governors fighting each other.

**And it has been walked by the air.** Real ESC-50 clips played through the
speakers and picked up by the microphone, with the monitor running: barks up to
0.435 and a `bark` alert, cries up to 0.590 and a `cry` alert on the second
confirmation, and **zero** on the wrong event in all sixteen classifications.
The volume was low and the room noisy, so those numbers are a floor.

**And the two-window rule was walked by the air too, which is the only way it
could be settled.** Its reason — *a cat miaows once* — is a claim about **hits**,
and the worry was arithmetic: the window is 10.11 s and the question is asked
every 5, so two consecutive classifications overlap by half a window and one
sound might be seen twice. It is not, measured with the worst cat ESC-50 has,
the one that scores 0.181 in the corpus:

| | |
|---|---|
| that cat played **once** | one classification at **0.619**, then 0.000, 0.000, 0.000 — **no alert** |
| the same cat played **twice** | two confirmations, `alert code=cry`, and a clip saved |
| a cry played for 27 seconds | 0.116, 0.086, 0.197, 0.362 — **two alerts**, two clips |

So one event gives one hit: the second window does not see it again. **The rule
holds, and it is load-bearing** — without it that single miaow fires the alarm,
because through the air it scores 0.62 where the file scores 0.18.

**That last number is the one to take away.** In a real room, through speakers,
the cat scores three to five times what the corpus says, and **higher than
ESC-50's own cries did in the same session**. The corpus understates it: what
keeps a cat off the banner is not the margin between the two — there is none —
it is time.

**What this does not measure is a level.** The speakers were at an arbitrary
volume in a room whose floor was -32 dBFS, so the absolute numbers are this
room's. What is measured is the **mechanism**: one event, one confirmation.

**What the monitor no longer does is just as important**: with no model it
reports nothing. There is no fallback onto the shape detector, because its
current tuning is a gate's and as a decider it would ring on more than half a
room's loud sounds — a fallback known to be noisy is not a fallback, it is a
fault with a label.

