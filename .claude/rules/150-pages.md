---
paths:
  - "internal/server/**"
  - "internal/qr/**"
  - "internal/i18n/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## The pages state what is not normal, and their drawings promise nothing

**Two readers, and neither of them wrote the program.** The viewer is looked at
at three in the morning, one-handed, for two seconds; the guided path is taken
once, in daylight, by somebody who knows nothing yet. What follows is both
registers, the palette they share, and the rule that keeps a drawing from being
read as a second instrument.

### Two palettes, and the viewer does not get lighter

The configuration's identity is **deliberately different** from the viewer's:
the monitor is watched at night, on a phone, and there a dark background is not
a preference but a necessity; the configuration is done once, in daylight, in
front of a computer. So "unify the style" does **not** mean bringing the light
earth into the three-in-the-morning page. What holds the two faces together are
the things that do not depend on the light — the seven-size type scale and three
weights, multiples of 4, the radii, the button pill, the vocabulary of the boxes
— and the **phase shades**, which are identical; only their lightness changes,
because the ground they sit on changes: a teal tuned on cream disappears on
near-black. The near-black is **warm**, not blue: next to cream a blue-grey does
not look like the same family, it looks like another program. The video's stage
stays pure black and not warm: it is the letterbox around the image, and a
tinted frame reads as a cast on the video.

The 48 px minimum button height is not typography: below that a finger misses
the target, and this page is used one-handed, in the dark, while holding a child
with the other. For the same reason input fields are at exactly 16 px — below
that, iOS zooms the page by itself at the first focus and never puts it back.

**An alias between tokens resolves where it is declared, not where it is read.**
`--accent: var(--c-home)` written on `:root` immediately becomes the dark's
colour, and elements inherit **that value**, not the reference: `body.day` can
redefine `--c-home` all it likes, it no longer reaches it. `/setup`'s "Set
password" button, on the cream page, came out for months with the teal tuned for
near-black, and there was nothing to read — the page draws, the token exists, no
error anywhere. So `body.day` recomputes **both** aliases: fixing `--accent`
alone is not enough, because `--phase: var(--accent)` has already resolved on
`:root` too.

**And the palette is written in four places**: the earth in `body.day` of
`style.css` and in `onboarding.css`'s `:root`, the tray panel copies six values
per face, the icon four phase shades. They are not unified — they are different
languages — so four tests read **the sheet**, which is the original: the two
light palettes must give the same colours (aliases resolved, otherwise two
writings of the same value get accused), every alias whose target changes must
be recomputed, and the two Go copies must match. The third is the one nobody
thinks to write: **a comparison between what is in both is blind to what is
missing in one** — removing the repairing line, the token disappears from the
intersection and the test goes green.

**And a `font` shorthand carrying a token can be dropped whole, silently.**
Reported from a photograph of a browser this machine has not got: the details
panel's group headings came out at the size an `h2` has when nobody styles it.
Measured off the picture — rows at 13 px and headings at ~19.5, which is exactly
`1.5em` of 13 — so `font: 600 var(--t-micro)/1.4 var(--sans)` was not applied
there at all, while the same version on an iPhone renders it at 11. **The
element with a rule of its own in the browser is where it shows**: on a `<span>`
a dropped declaration leaves the inherited size and nobody notices; on an `h2`,
a `<button>` or an `<input>` the browser has an opinion and that is what
appears.

**And the same headings change size with the orientation of the phone**, which
is a second mechanism and not the same one: reported level with the readings in
portrait and **larger** than them in landscape, on one page where they are 11 px
against 13. That is WebKit's text inflation — it enlarges text inside a block it
considers wide, by a factor that depends on the size it starts from, so the
smallest text grows the most and the order of the two sizes inverts. `html {
text-size-adjust: 100% }` in both sheets says the sizes are ours; **`100%` and
not `none`**, because what has to stop is the automatic inflation, not the
reader's own zoom. Verified to be a no-op here — the computed font of all 354
elements is unchanged — which is all this machine can say about it: no engine
on it inflates.

All **thirty-four** of them are longhands now, which cannot go as a group, and
the sweep was verified rather than trusted: the computed font of every element
of the three pages — 354 of them, size, weight, line height, family, style,
numeric variant and tracking — is identical before and after.
`TestNoFontShorthandCarriesAToken` keeps the form out, with a floor under it so
that a guard which has stopped reading the sheets fails instead of passing.

**A missing token does not complain**: its line becomes invalid and vanishes,
and the property's default remains. `--radius-s` was only in `style.css` and the
language selector had sharp corners in the guided path; `--gap-ico` lives in
`icons.css`, which `/login` and `/setup` were not loading despite reading the
`style.css` rules that use it.
`TestEveryTokenAPageUsesIsDeclaredSomewhereItLoads` reads the sheets **from the
page**, which is the only thing that knows which it really loads, and lets
`var(--x, fallback)` through, where the absence is expected.

**A scale is declared in full in every sheet that carries a palette, even where
not all of it is needed today.** Downstream sheets — `i18n.css`, which two pages
with two palettes read — can use only what is declared in **every** place they
might end up, and what is missing they rewrite by hand. It is one cause behind
three defects that looked different: a missing `--radius-s` produced a
hand-written `10px`, twelve radii over six sizes in `onboarding.css`, and a
shadow copied in three places. **The steps are not chosen at a desk: the rule
that generates them is nesting** — a box inside another wants a smaller radius
than the one containing it — and the twelve values became four roles (`-l` the
surface that holds cards, `--radius` the card, `-s` what sits inside a card,
`--radius-xs` what is smaller than a line of text). **A pill is not a step**: on
a 10 px track the radius 5 is half the side, it is declared `999px` and leaves
the count instead of being mapped onto a step that would say something false.

**The outline one sees is not the border: it is the focus ring.** Measured, read
from the pixels at 8:1 with a **real** click:

	ring    #3FA8A4  16 px → 2 px CSS     outline
	gap     the ground 24 px → 3 px CSS   outline-offset   <- the "black"
	border  #3FA8A4   8 px → 1 px CSS     border-color on focus

Six pixels of band, of which three are the **gap** that shows the dark ground
through. At rest the border is one, of one pixel, and there is no outline —
**the first report was looking at the control at rest while the question was
about a clicked control.** And it shows only there for a specification reason: a
text field matches `:focus-visible` **even with the mouse**, and Chromium does
the same with `<select>`, while buttons take the ring from the keyboard. The gap
is now **one** pixel (`--focus-w`, `--focus-gap`): the band drops from 6 to 4
and the dark line disappears. **And it was three writings in three sheets, with
one already gone its own way** — `.lang select` had 2px/2px against the others'
2.5/3, that is, whoever tabbed from the language selector to the password field
saw two different rings.

**A control sits on the card, it does not sink into it.** `--card-sunk` is the
role of the **inset** surface, and a text field is not a surface: the role is "a
control inside a card" and the answer is one, background `--card`, border
`--line`, radius `--radius-s`.

**A colour rejected for contrast must be rejected everywhere, not only in the
sheet.** The light `--muted` was `#6E6659` because the comment beside it
measures `#7A7264` at 4.48:1 on the card, "just below the threshold" — and then
seventeen `<text>` elements of the illustrations were exactly `#7A7264`. The
margin was not two hundredths: inside the explainer that ground is not the card
at all, `.art` there being `--card-sunk`, so the real figure was **4.04:1**.
Seventeen hex values were not corrected: the attribute was removed and a rule
added, `.art svg text:not([fill]) { fill: var(--muted) }`. **The `:not([fill])`
is the part that counts** — a CSS rule beats a presentation attribute, so a
`fill` written there would have greyed out the six labels that say something
**with** their colour.

**And then the same defect was found in the value that had replaced it, one
surface further out.** `#6E6659` was chosen against the **card**, where it is
5.34:1, and the sheet's own comment closes the question the right way — *text
that sits outside the cards uses `--ink-2`*. The tray panel paints its status
lines and its ghost labels straight onto `--ground`, which is neither: there
`#6E6659` measures **4.27:1**, under the threshold. **A token is only as
measured as its worst surface, and the surface nobody thought of is the one in
another package.**

It is why it shows in the light face and not the dark one, which is the form the
report arrived in — *the light mode looks less contrasted than the dark*. In the
dark palette `--ground` is the **darkest** of the three surfaces, so muted on
ground is the best of them at 5.42:1; in the light one the order inverts and the
ground is the lightest, so the same pairing is the worst. **Two palettes that
are mirror images in lightness are not mirror images in contrast**, and nothing
that compares them name by name can see it.

The value is **`#6A6256`** — the same hue and saturation one notch down — which
is 4.53 on the ground, 5.10 on the sunken card and 5.67 on the card, so the
surface that forced it is the only one that was failing and the other two
improve. Measured with an instrument checked first against the three canonical
figures, 21.00 for white on black and the 4.54 and 7.00 of the reference greys.

**Three numbers in the sheet's own comment were wrong, and the re-measurement is
what found them**: `#7A7264` on the ground is 3.58 and not "3.0", the explainer
figure is 4.04 and not the 4.28 written there, and `.art` was described as
sunken when the rule above it says `background: var(--card)`. None of them
changed a decision — the colour was rejected for the right reason — which is
exactly why they survived: **a number nobody acts on is a number nobody
checks.**

**What was not done is the other repair, and it is left written down rather than
taken.** The faithful reading of that comment is that the panel's rows want
`--ink-2` and not `--muted` at all, since they are text outside a card: it is
6.07:1 on the ground and it would leave every page untouched. It costs the tray
palette a field and its guard a row, and it is a change to what the panel says
rather than to what it measures, so it is a decision and not a correction.

**Three literals remain, the ones depicting somebody else's interface**:
`login.tailscale.com` in the fake browser bar, the device row in the Tailscale
panel, the Windows taskbar clock. Those do not annotate the drawing, they
populate it: making them follow our palette would mean repainting somebody
else's interface. The boundary is that, and not "it is a literal": the little
window of **our own** tray, drawn beside them, follows the token, because there
following the palette makes the drawing more faithful and not less.

**The shadow is a whole token, not a shade.** Identical geometry in the two
sheets and two colours — pure black at night, warm ink by day — and that is not
an oversight: warm on earth weighs more than black, so it was lowered, and on
the night one it **cannot go** because `#2E2A24` on `#171512` is lighter than
the ground, that is, a halo.

**And a token present on one side and missing on the other is what then forces
hand-writing.** None of the shape tests catches it — the one on literals sees
the defect **after** somebody has written it, the one on used tokens sees only
what somebody actually uses — so there is
`TestBothPaletteSheetsDeclareTheSameNames`, which compares **names** and not
values, with three argued exceptions: an entry is added to that list only when
declaring the token in the other sheet would mean **inventing** something — the
veil of a video that is not there, the plum of a step that does not exist there
— not copying it. **And the same shape one level deeper**: that test merges
`:root` and `body.day` into one set, and rightly, but that way a token declared
**only** in `body.day` passes, and the result would be an invalid line on the
**night pages** with nothing complaining.
`TestTheDayPaletteOverridesNothingTheNightOneLacks` closes the direction that
counts.

**Exemptions are written by name, not by prefix.** A `--preview-` prefix
exempted for ever even names not yet written, and among those is a
`--preview-radius` that should instead be shared like any other radius: **an
exception covering things that do not exist yet is not an argued exception, it
is a hole with a name.** And **a value is judged piece by piece**: demanding a
single `var()` would have wrongly accused an asymmetric radius — `var(--radius)
var(--radius) 0 0` for a panel resting against an edge — which has no
hand-written steps at all.

**Hence `i18n.css`'s position: it consumes and never declares.** A token
declared in a sheet that two pages with two palettes read would have the same
value on cream and on near-black, that is, it would be a third copy, wrong for
one of the two. Consuming is safe because the two tests together guarantee the
name exists in both palettes **and** is declared on every page that sheet ends
up on.

**The stage is pure black in daylight too, and it has a name.** `onboarding.css`
declared `#14110D` with the reason "warm black: it is the letterbox around the
image", and `style.css` declares the same letterbox `#000` with the **opposite**
and argued reason. They were not two components: it is the same one, the
configuration's preview, so the drawing of the stage contradicted the stage. It
is now `--stage`, declared in both sheets with the same value. **And the dark
island inside the light page has a name of its own**: the band under the preview
and the VU meter are not the night palette — they are one- or two-point drifts
from the dark's `--card`, `--ground-deep` and `--ink-2` — and not the day's
either. Calling them `--preview-*` stops the drift where it is.

**And the rule holding all this together is a test on shape, not on values.**
`TestNoSheetRewritesTheScaleByHand` does not list the permitted radii — that
would be the second list — but requires a radius or a shadow to come from a
token, with the only exceptions a token cannot express: the pill, the circle,
absence. **It looks at the markup's `style=` too** — they are there by choice,
the blades of grass carry their own animation duration — while SVG `rx` stays
out deliberately: it is drawing geometry in `viewBox` units, and mapping it onto
a step would mean deforming the illustration to make it resemble the interface.

**Focus is declared with `:focus-visible`, and `input:focus` out-specifies it.**
0,1,1 against 0,1,0, and `:focus` also catches Tab: `input:focus { outline: none
}` removed the ring **even from keyboard users**, on `/login` and `/setup`,
while the same field in the guided path had it. The right form is
`input:focus:not(:focus-visible)`. It is measured with a real Tab, because
`:focus-visible` depends on how focus arrived and a scripted `.focus()` does not
light it: **a probe that does not know this absolves every page.**

**And `:hover` is a declaration about a pointer, which a phone has not got.**
A tap leaves the element hovered until the next one lands somewhere else, so on
a phone "Details" stayed lit after being pressed — reported as the colour
Windows gives the control under the mouse, and that is exactly what it was: the
same rule, on a device with no pointer to move away. It was read as a stuck
focus and it is not; the ring is `:focus-visible` and wants a keyboard.

Every hover in the four sheets therefore sits inside `@media (hover: hover)`,
which describes the **primary** pointer: a laptop with a touchscreen keeps its
hover, a phone never gets one. `:active` stays outside deliberately — it lasts
as long as the finger, which is the one piece of feedback a touch screen does
want. Verified in both directions with a real browser, because a rule that is
right and a device that differs cannot be told apart by any value: emulating a
pointer, hovering lights the button `#FFFFFF10`; emulating touch, the tap opens
the details and leaves the ground transparent. The one exception is by name and
not by prefix — the picker's `option` rows, where the hover half shares its
declaration with the `:focus` half and the menu closes on the choice, so
nothing stays lit behind it. `TestNoHoverIsDeclaredOutsideAPointer` reads the
sheets — the list of them from the embedded folder and not by hand, because a
hand-written list watches what somebody remembered and the fifth stylesheet is
exactly the one that gets added without being added here — and it **counts the
rules it found inside a pointer block**, failing below eight: a guard that
reads lines goes blind at the first reformat, and there green would mean
nothing. Verified to catch in both ways: unwrapping one rule, and breaking the
pattern so that nothing is read at all.

**And taking the hover off a phone left the press with no answer**, which the
first version of this did not notice: `button:active` only cancelled the lift
that the hover gives, so with no hover there was nothing left to cancel. The
press now carries the mark the hover carried — brightness on a filled button,
the faint ground on a ghost, which are the ones where nothing else answers a
press. **Not on `.toggle`**: a lit one carries the teal of `aria-pressed`, and
a ground written after that rule beats it at equal weight, so the mark would
read as the switch going off under the finger — they keep the brightness, and
their real answer to a press is the state flipping. Verified by forcing
`:active` from the protocol on the four kinds of button, because a synthetic
touch does not make a browser recognise a press.

### The night page states, the onboarding explains

Two registers, and the difference is not taste: it is **who is reading, and
when**. The configuration path is done once, in daylight, in front of a
computer, and whoever does it knows nothing yet: there a sentence explaining why
a cost is being paid is what stops them closing the window. The viewer is looked
at at three in the morning, one-handed, for two seconds: there every extra word
is a word that does not get read.

The rule in one line: **on the night page say what is true now and what to do;
not why.** Three consequences follow: **no declared cause** when it does not
change what to do — a "perhaps somebody else is speaking" is a supposition, and
two possible causes cured the same way do not need distinguishing; **the fact
before the mechanism** — "Audio filtered by the system: raw mode is not active",
not the reverse; and **the instruction stays** where there really is something
to do, because without "Re-enable it in the browser settings" the reader does
not know where to go.

**The onboarding is not touched**: there the warm register is documented as a
principle — the cost is declared before it is charged, the technical detail is
closed but present — and that is what gets whoever is installing to the end.

**And there is a third reader, whose register is neither: `README.md`.** It is
read by somebody deciding whether to install, and then installing, so it says
what the program does and how to run it — the **arguments** live here. The
defect was importing this file's voice into it: "which is a real temptation",
"worth knowing before installing rather than after", "a defect with a label
rather than a feature with a warning" — reasons for decisions, in a document
whose reader has not taken any. Trimming the asides and keeping every fact took
2270 words to 1979, and nothing was lost, because in each case the fact was
already in the sentence and the clause was arguing for it. **The test is not
whether a sentence is true: it is whether its reader has a use for it.**

Two of those asides were mine, added the same evening in a commit that was
correcting the README's claims — which is the shape to watch, because a document
being edited is a document in the editor's voice.

**But a condition the reader cannot observe is not a condition.** The rule
everybody states is conditions before instructions — the circumstance first, so
that whoever it does not concern can skip the rest. The last screen obeyed it
and defeated it: *"if the router gives the computer a different number the
address changes, and the right one is always in the icon next to the clock"*.
Nobody watches their router hand out addresses, so the reader cannot tell
whether it applies to them; what they will meet, months later, is a bookmark
that does not open. **The test is not whether the condition comes first: it is
whether the reader can tell that it has happened.** So the sentence starts from
the symptom — "if one day it stops opening" — and the mechanism survives as half
a clause, which the onboarding is allowed to keep.

**And no measurement finds it.** The two mechanical rules the guidelines agree
on are one idea per sentence and an average around 25 words, and that sentence
was **20 words in Italian**, inside every threshold: measured over the whole
catalogue it does not even come near the top. What was wrong was which fact the
condition named, and that is not a property of the string.

### On the night page, "it works" is not news

The viewer shows what is **not** normal and stays quiet about the rest: the
viewer count is empty when one is alone, the alerts banner is absent when there
is nothing to say, and the "Access from outside" box appears **only when there
is a step to take**. With the tunnel up it used to give the address of the page
one was already looking at, every night, under the image.

**The address is not deleted, though**: it is needed by whoever has to pass it
to the other parent, and it moves down into the details — the place for things
that are looked for rather than announced. It is moved by `order` on `.viewer`'s
flex column, not by a second node in the markup: two copies of the same box
would diverge at the first touch. It follows that "Details" governs two things,
and that is fine: they are two faces of the same question.

The details also hold the **QR code** of the public address, generated by `/qr`.
Nobody types a `.ts.net`, and whoever opens the details is nearly always about
to pass it to a phone in their hand. The light background lives inside the SVG
**and** in the sheet: this page is nearly black, and a code that is transparent
even for an instant is unreadable by precisely the camera that has to read it.
The box's body is a second container rather than making `.remote` a flex: that
`display` is the switch that turns it on, and **mixing switching and layout in
the same property** is the defect already paid for on the explainer's
`<dialog>`.

**In the panel, what can be pressed comes first and what is only read
follows.** Two of those rows are not readings — they are the choice of camera
and of microphone, the only controls that live here instead of on the bar — and
they sat fourth and fifth, that is, a control found by coming across it while
scanning a list of numbers. They are now the first two. The version went the
other way, to the bottom: it used to be first, argued as the thing whoever is
reporting a fault looks for, and that is true **once**, while the row is
otherwise the same every night — the opposite of what the rest of this page
does, which is to show what is not normal. The order in between is unchanged,
and the recordings stay the grid's last cell, being the only one that leads
elsewhere.

**And the readings are in three groups, each under a word on a hairline.**
Fourteen rows of one weight are a list read to the end or not at all, and the
rule every design system states for this is the same one: a divider separates
related elements, and it is paired with a label to say what the group is. The
three are what is being captured, the way in, and what this run has done — and
they buy, at no cost, the separation between the two rows that are **pressed**
and the twelve that are read.

Three decisions inside it. The label is **the eyebrow this sheet already has**,
the one on `label` and on the "from outside" box, because a fourth way of
writing a small heading is how a scale stops being one. The rule sits **above**
the label and not under it, belonging to the break rather than to the group, so
the first heading needs none — the panel's own top border is already there. And
they are `h2`, because this page had no heading at all: whoever moves by
headings had nothing to move by.

**What was refused is the box.** The obvious next step is iOS's inset grouped
list, a continuous ground running from the header to the foot of each section;
here the panel already sits on `--card-sunk` inside a dark page, so three
grounds inside a ground inside a page is the third level of container — the
nesting rule the radii carry, applied to something that is not a radius.

**And the pair is inline, with the column count following the window.** The
grid was `auto-fit` on a 160 px floor, which at 1200 px gives **six** columns
and writes the pair top to bottom in each of them — a reading and its name in a
strip narrower than the name. The rule the design systems agree on is that the
arrangement is chosen by the width of the container, name over value under
~200 px and name left with value right from ~400 up, so the floor is now 330
and the pair is a line: the name on the left, the reading on the right in
tabular figures, a hairline between rows.

The count then follows the window instead of being declared at three widths —
measured: **one** column at 390, **two** at 768, **three** at 1200, **four** at
1440, **five** at 1920, each between 330 and 472 px. The floor is written
`min(330px, 100%)` and not `330px`, because a track cannot go below its own
minimum: on a 320 px phone the 288 px of row held a 330 px column and the page
**scrolled sideways by 26 px** — measured, and outside every width the rule had
been tuned on.

**The reading then sat at the far edge of whatever that track turned out to be,
and that was the defect.** Flush right reads as a column while the column is
narrow and stops reading as anything once it is not, and `space-between` gives
the gap **whatever is left** — so the empty band between a name and its reading
was, measured on the real page in a real browser, **223 px median at 390** and
**279 px at 1700**, worst **338**. A two-character number a third of a metre
from the word naming it, and the same at every width: it was reported as a wide
window's problem and the phone had it too, hidden by there being one column to
follow rather than four.

So the reading starts at a **fixed offset**, 10 em, and the gap becomes **81 px
median, 105 worst, identical from 390 to 1920** — which is the property that was
actually bought: the distance between a name and its value no longer depends on
the window. It is still a column of readings aligned on one x, only on the edge
the eye arrives from; the tabular figures stay, because what they align is the
digits within it. The offset is an em measure and not a share of the row on
purpose — a percentage would put the reading somewhere else in every column the
grid happens to make, which is the thing being removed — and 10 em clears the
longest name measured in either language, `Fotogrammi scartati` at 115 px,
without a table of exceptions.

**What was tried first and is wrong is the pair of remedies that suggest
themselves**: centring the readings, which breaks the one column they form, and
right-aligning the names, which makes ragged the left edge a list of fourteen
names is scanned down. Both treat the symptom — the two are far apart — and
neither asks which of the two should move.

**And a heading must not be quieter than what it governs.** The three group
words took the eyebrow's `--muted`, which is the **labels' own colour**, at 11 px
against their 13 — smaller *and* no brighter than the rows under them. It held
while a phone's single column said which was which by position, and stopped
holding on a wide window, where the heading sits directly over the first label
at the same x: reported, in those words, as confusable with a label. Measured
before touching it, both were `rgb(148, 139, 124)` and both began at x=16.
`--ink-2` is the step this palette already keeps between the labels and the
readings, which is the rank the word holds — so the hierarchy is carried by
colour, the one axis still free once the size has been spent on making it an
eyebrow.

**The hairlines moved the group rule.** With a rule under every row, a second
one over the heading drew two lines a few pixels apart: the heading keeps the
air above it and the last row's rule is the break. The last reading of all has
none, because what follows it is a command and not a reading.

**What the monitor is not is said at the foot of the details, and not on the
bar.** `safety.notice` — not a medical device or a safety system, it can miss
crying, barking or motion, the picture stops with the PC or the network, and it
calls nobody — is a fact about the product, true every night, and that is
exactly what this page does not announce. So it is a `.hint` spanning the grid
below the recordings, which stay the last cell; on the sign-in card, the one
page everybody who watches passes through, as the same `.hint` `/setup` already
ends with; and on the guided path's arrival as `p.small`, where whoever set the
monitor up is about to rely on it. **The tray panel does not carry it**: every
row there is measured and the panel is states and commands, and whoever is in
front of the machine is whoever read it at the arrival. The wording has no
promise and no adjective, which is the privacy policy's rule and the same
exposure.

Two things it cost that are worth knowing next time. **Measured at phone width
over the DevTools protocol, not by resizing a window**: Edge ignores
`--window-size` below its own minimum, so the first screenshots showed the card
cut off on the right, subtitle included, from a layout laid out wider than the
picture — the instrument, not the page, and the measurement that settled it
reports the document as wide as the viewport at 360 and 390. **And the French
non-breaking space was decoded on the way in three times**: by Python, by
`sed`'s `\u`, and by the editing tool, each turning ` ` into the character
or into nothing; `TestTheNonBreakingSpaceIsWrittenAsAnEscape` caught the first
and the third, and the second produced `personne00a0:`, which it cannot see —
the limit the French section already declares. What wrote it right was a byte
replacement with the backslash spelled as `\x5C`.

**On a phone the panel opens below the fold, so the page is taken to it.** The
bar sits at the foot of the viewport, so everything "Details" reveals is under
it: measured at 390x844, pressing it turned an arrow round and nothing else
moved on screen. `scrollToDetails` takes the page down to the panel, and the
picture stays one flick up — measured over four camera shapes and a panel half
again as tall, the picture coming back whole every time.

**It aligns the panel's top and not its bottom, and a short screen is the
reason.** The two are the same thing while the whole panel fits — at 390x844
both land on the last row — and stop being the same the moment it does not: the
panel is 703 px tall, so on a 360x640 screen aligning the bottom scrolled **63
px past its top**, putting the two boxes that were moved to the head of the
list, the only two rows anybody presses, above the edge of the screen. Measured
after the change at five sizes from 320x640 up: the panel starts at the top edge
and the camera row is on screen at every one of them.

**And the first version of this chapter said the panel squeezed the picture to
a sliver, which was never measured and cannot happen.** `.stage` is a flex item
with `min-height: auto`, so its floor is its own min-content, which for a video
with an intrinsic ratio is the picture at that width: taking the rule out and
measuring the same build at 390x844 with a panel half again as tall, the stage
is **219 px**, that is, exactly the picture, and the page scrolls. What the
probe had shown going — 738 px down to 256 — was the black around the picture,
not the picture. **A number that fell is not a thing that was lost**, and the
difference between the two is one measurement with the remedy taken out, which
is what should have been made before writing the reason down.

What the rule buys is therefore the black and the cap, and both are worth
having: with the panel as it is the stage grows to **315**, of which 96 px are
letterbox between the picture and the thing one has just opened, and a camera
filming a portrait wants **693 px** of an 844 px screen — the floor has no
ceiling, `max-height: 70dvh` gives it one at 591.

**It is a rule of the phone in portrait, and the other shapes were measured
rather than assumed.** In landscape, 844x390, the floor is 475 px and the stage
is already exactly the picture: there is no letterbox to remove and nothing to
cap short of a portrait camera. On a wide window the panel sits beside a large
picture without either giving anything up.

#### The bar has one shape, and that is the property that counts

Seven commands in a row wrapped wherever they happened to. A bar that changes
shape at every width is rechecked every time, one that has a single shape is
learned. It is now **two fixed rows** below 1020 px — the three detections above,
the commands below — and one row beyond, with the three detections **in the
centre**.

- **The three switches are a group, and that is not layout.** The other buttons
  do something when pressed; crying, barking and motion say how the monitor
  should be. Grouped, the bar reads as two things instead of seven, and it wraps
  **whole** instead of splitting.
- **The spacer was the real cause of the crooked wrapping**: in a wrapping
  container, `flex: 1` eats the rest of the row and the last button ends up
  alone. On a phone it disappears.
- **On the single row the three detections sit at the centre of the bar, not
  half way between their neighbours.** They are the thing one looks at — they
  say what the monitor is watching — while the commands at the sides are
  pressed. Centring them between their neighbours would be one line and is not
  the same thing: the two ends measure **229 and 167 px**, so the group would
  stay 31 px off centre, and above all it would **move when "Talk" appears** —
  96 px measured with the old shape — that is, a control sliding under the
  finger. With three `1fr auto 1fr` columns the two tracks are equal by
  construction: **zero offset, with and without "Talk"**.
- **The glass has edges the page does not see, and the bar is against all
  three.** With the details closed the bar is the foot of the page, so its
  buttons sit on the home indicator — a strip of iPhone glass that takes the
  touch instead of the button under it — and in landscape the notch eats a
  band down one side. The two `env(safe-area-inset-*)` are **added** to the
  padding and never replace it: in portrait they are both zero and what is
  left to clear is the corner radius, which no `env()` reports at all. **The
  bottom inset belongs to the edge of the screen and not to the element**, so
  it moves to the panel when the panel is what is last: on the bar it is
  `.viewer:not(.details):not(.fs)`, and 34 px of card in the middle of the page
  is what writing it unconditionally would have given. **The second `:not` is
  there by name and not by weight**: the comment used to argue that the
  full-screen rule wins on specificity, and it does not — `:not()` contributes
  the class inside it, so both are 0,3,0 and the later one in the file commands.
  The rail really was taking a bottom padding, and it showed nowhere because the
  rail is anchored to the top: a protection that is not in force, invisible. Verified with the inset stood
  in for at 50 px, because on a desktop every one of them is zero and **a
  value that is zero everywhere one can measure is a value nobody has
  measured**: the last row of buttons ends 58 px above the glass with the
  details closed, the bar goes back to its 8 px with them open, and the panel
  carries the 50 instead.
- **The threshold is measured, not chosen**, and it must be re-measured if a
  label changes: it is the only number in that sheet that depends on the text,
  and **it is measured in every language**, not in the writer's — English asks
  for 809 px, Italian 918, German 953, and the threshold is dictated by the
  widest. **Adding a language is a label change**, and German is what proved the
  rule was load-bearing: at the 980 the first two had settled on, the middle
  group slid 10.6 px the instant `Sprechen` appeared, so the threshold went to
  **1010**. The cost is borne by every language, because a stylesheet does not
  know which one it is dressing, and that is the trade the rule already made.
  The re-measurement reproduced the two recorded figures before moving anything
  — 807 and 921 against the 809 and 918 above — which is how a probe earns the
  right to contradict a number. **Spanish found the rule again**: the boundary —
  the width below which the middle group moves when "Talk" appears — comes out
  at 1015 in Spanish against 1005 in German, 975 in Italian and 970 in French,
  so the widest is Spanish's and the threshold is **1020**. What moved it is
  **one word**: `Silenciar` for the mute command, where French has `Muet` and
  German `Stumm`. Measured, `Mudo` in its place takes the boundary to 985, which
  would have kept the old number — and it was not taken, because `Silenciar` is
  the word the Spanish Windows uses. **The label that looks guilty is not**: the
  widest in the bar, `Cerrar sesión`, moves this boundary not at all when
  shortened, because the boundary is about the left side and the log-out is on
  the right.
- **The two sides exist only on the single row**, and below they are `display:
  contents`: the box disappears, the buttons go back to being direct children of
  the bar, and the two-row shape is the usual one — verified pixel by pixel
  against the measurement taken before touching anything. **But `display:
  contents` removes the box, not the node**, and selectors look at the DOM:
  `.bar > button`, which tightens the commands' padding on a phone, stopped
  matching them the day they ended up inside a side — **no error anywhere**, and
  at 390 px the bar went from 106 to 154 px, that is, a third row. The remedy is
  `.side > button`, and the guards are in `bar_test.go`, in both directions in
  which it breaks.
- **"Talk" is the first command**: it is the only thing done *while* watching,
  and whoever looks for it looks in a hurry, one-handed, in the dark.
- **The height never changes**, in any state. "Stop talking" was the longest
  word in the bar and appeared at the worst moment, taking it from 106 px to
  154: **pressing a button moved everything under the finger by forty-eight
  pixels.**

**And a button that says how the monitor is carries the pill, not another
label.** "Mute the room" became "Listen to the room" when pressed: from outside
that reads as **a different command appearing in place of the previous one**,
not as the same one now lit — and it happens under the finger, at night, without
looking. It is now a `.toggle` with `aria-pressed`, and **in the bar the text
never changes for a change of state.**

**"Details" carries it too, and the arrow leaving the phone is what showed it
was missing.** That button said whether the panel was open with its arrow alone,
which held while the arrow was drawn: on the phone's bar it is not, so pressing
it changed nothing that could be seen. The lit pill is written once for the two
attributes — `aria-pressed` for a switch, `aria-expanded` for a thing that opens,
which is the difference between them and the reason they are not one attribute
— and measured, the two give the identical ground, border and ink. The grey of a
hover is kept off a lit control with a `:not([aria-expanded="true"])`, so that
what answers the finger there is the brightness every other button takes and not
a ground that hides the state.

**The arrow stays where it is drawn, and that is not a contradiction of "one
mark for one thing".** While the panel is open the two say the same thing; while
it is closed the pill says nothing at all, and the arrow is the only sign that
there is something behind the button. It is on the single row, where there is
room for it, and off on the phone, where it costs the glyph gap.

**When a button has an icon, the state is not written into the button.** It is
the trap that reappeared three times — "Copy" in the onboarding, "Talk" and
"Details" here: `textContent` erases the glyph along with the label. The state
is already said by the lit pill, by `aria-pressed`, and by a line in clear text;
"Details"'s arrow is chosen by `aria-expanded` in the stylesheet, so it is
written once and the sheet and the screen reader read the same thing. A label
that has to change lives in a `<span>` inside the button, never in the button.

**Equal widths where things are parallel — and on a phone the lower row is
parallel too.** The three switches sit on a `1fr 1fr 1fr` grid, which makes them
equal when there is room and shares out the rest when there is not. The
commands used to take their natural widths with a spacer eating the rest, and
the argument for it was that at 390 px equal columns would want 4 x 125 = 500
out of 366 available. **That is true of columns with a content floor and false
of `flex: 1 1 0` with `min-width: 0`**: there the row is N equal parts whatever
the labels say, and N is not a constant, because "Talk" appears only once the
incoming track is laid. Reported from a phone, and the reason is the one the
detections already had: five equal targets are found in the dark without
looking, and a row of four sizes beside a row of three equal ones reads as two
different kinds of thing.

So below 540 px both rows are equal parts. Measured in the widest language, in a
real browser, at every width a phone has:

| | 360 | 375 | 390 | 393 | 412 | 430 |
|---|---|---|---|---|---|---|
| each command | 64.8 | 67.8 | 70.8 | 71.4 | 74.4 | 78.0 |
| each detection | 109.3 | 114.3 | 119.3 | 120.3 | 126.7 | 132.7 |

Four things that fell out of it, and none of them was visible from the code:

- **The detections row was already overflowing, and nobody had measured it.** A
  bare `1fr` track keeps its content as a floor, so at 360 px the three wanted
  352 px of a 336 px row and the third was cut by the edge of the screen. It is
  `minmax(0, 1fr)` now, which is the grid's spelling of the `min-width: 0` the
  commands need.
- **`:not(.fs)` on the grow factor is not caution.** In full screen `.side` is a
  **column**, and a grow factor there stretches the pills down the height of the
  picture; the rail's rules set the size and never the flex, so nothing would
  have stopped it.
- **On the phone's bar the "Details" arrow is not drawn.** With five equal parts
  the widest content in the row is that arrow plus the longest label, and
  measured it breaks out of its own pill by 2.9 px at 360 and 1.4 at 375. The
  first cure was to tighten the gap between a glyph and its word from 8 px to 4
  — and that is a rule this file already holds the other way: **8 and no less**,
  because the air inside the drawings is not the same for all of them. Buying a
  pixel there to pay for an arrow elsewhere is the trade that rule exists to
  refuse, so the gap stays 8 and the arrow leaves the whole two-row bar. The
  word stays and the glyph goes, which is the rule the rail already carries the
  other way round — a glyph is dropped where it costs more room than it gives
  back, never the word, because the word is the accessible name. What the arrow
  says is said again by `aria-expanded` and by the panel opening. Measured with
  the gap back at 8: nothing breaks out of its pill at any width from **320** to
  540.
- **The padding stopped deciding the width and started deciding the spill.** On
  equal parts a button is as wide as its share whatever its padding, so the
  number that used to buy a row now buys the room a label has inside its own
  shape: `--s1` of horizontal padding, the glyph's gap untouched.

#### Full screen: the controls float on the picture

The night mode — the picture takes the whole screen and the commands become
half-pills cut by the two edges: speaking, clipping and silencing down the left,
the three detections down the right, the way out in the bottom-right corner.

**There is no second set of buttons**, and that is the whole design: same nodes,
same ids, same listeners, so every line that paints a state goes on working
untouched. It is the `.remote` panel's rule — moved with `order`, never written
twice — applied to six controls, and here the copy would have diverged in the
**state**, which is the half nobody looks at twice.

**The mode is ours and the Fullscreen API is an enhancement.** `position: fixed`
over the viewport fills the screen with or without it, because on an iPhone
before iOS 17.2 an element cannot go full screen at all — and Brave there does
not expose it whatever the version. So the request is made and **its refusal
changes nothing**: weigh the effect, not the answer. What is lost where it is
refused is the browser's own bars going away, and the remedy for that is not an
API — see the manifest, below.

**`dvh`, because on iOS `inset: 0` is not the screen one can see.** Where the API
is refused the browser's bars stay and a fixed element is laid against the
**layout** viewport, which is taller: the bottom of the mode, and with it the way
out, ends up under the address bar. Reported from a phone as the buttons starting
at the very bottom edge. The declaration comes after `inset: 0` on purpose —
where `dvh` is not understood it is dropped and the inset governs, which is what
the code did before.

**Two groups of three at the two edges, and that is what made everything else go
away.** The commands are half-pills cut by the **left** edge, the three
detections by the right, both anchored under the status strip, and the way out in
the bottom-right corner. The arrangements before it — a pile in one corner, then
three stations down one side — both had to fight the height: seven targets in one
column want 436 px and a phone laid down has 320 to 430 **before** the browser's
bars, an iPhone in landscape in Brave about 290. So there was a height threshold
at 460, a wrap into two columns, an exemption for the column the edge does not
cut, and a spacer holding a seat for the corner button. **Three per side is 168
px**, which fits the shortest landscape screen with room over, so the threshold,
the wrap, the exemption and the spacer are all gone and there is **one shape at
every size** — no media query of any kind in the mode. The commands go left
because they are then always in the same place, which is easier than remembering
which side they took.

**The controls float instead of taking a band**, and that is what allows it: a
solid rail has to be a column on the flank in landscape and a row under the
picture in portrait, because it is stealing width or height from the video and
which it can afford depends on the screen. Floating, it steals nothing. First
the orientation question went, then a height question took its place, and the
two-edge arrangement removed that one too. **Each shape was less measured than
the one after it, and what shrank each time was the number of things that had to
be decided.**

**Centring them was tried and is wrong**, which was reported and is obvious once
said: centred, the two groups sit wherever the screen is tall — in the middle of
the picture's band on a portrait phone, halfway up the video on a landscape one —
so where to reach is a different place on every screen. Anchored under the strip
they are always the same distance from the same edge: measured, both groups'
centres at 117 in every viewport. **A control found without looking beats a
control in the optical centre.**

**And the clearance is the strip's own formula, not the 41 px it measures.** That
band is a line of `--t-small` at 1.3 between two `--s3`, so written as the sum it
follows whoever changes either token instead of being a frozen number beside
them. What it does not cover is the strip **wrapping**, and that is declared
rather than guessed: 41 px on one line and **75 on a 320-wide screen** when the
viewer count and the path both appear and it takes three. There its last line
comes down over the first pill — an old phone in portrait, relayed, with somebody
else watching. The alternative was a constant wrong on every screen instead of on
one.

**One gap on both, and the tight one.** In the bar the three detections are told
apart from the commands beside them by sitting closer together, no frame needed,
and the commands kept the bar's wider gap here out of that habit — which showed
as a left column looser than the right. On two opposite edges the habit is
pointless: **what says the two are two is the edge each is cut by.**

**`space-between` cannot centre**, and that stays written because it is the one
CSS fact the abandoned shapes taught: it shares the free space *between* the
items, so the middle of three lands in the middle only when the two ends are the
same height. In the three-station shape they were 168 px of commands against a 60
px seat, and the detections came out 54 px below the centre.

**A half pill running off the edge, not a circle.** The right cap is past the
screen, so what is left is a shape the edge has cut — and the gain is where the
glyph lands: a circle keeps its whole width on screen, so its ink sits an inset
plus a radius in from the edge, while a pill that bleeds puts the ink **a radius
nearer the thumb**. Measured, the glyph's centre goes from 40 px in from the edge
to 24. All the padding is on the outer side and it *is* the bleed, so the content
box is exactly the visible part and centring works on that: with the usual
padding still on the inner side the glyph slid outwards to 12 px from the edge,
that is, two pixels of ink past the glass of a rounded phone.

**The safe area went inside the shape, and that was the reported defect.** It
used to be the rail's padding, so on a phone the whole thing was pushed in and
the pills stopped short of the edge with a band of picture beyond them — which
is what "the half-pills are not at the edges" was. **The desktop probe could not
see it**: there every `env(safe-area-inset-*)` is zero, so it measured the pill
against the glass and reported no gap, three shapes running. Now the bleed
carries the inset — the shape always reaches the physical edge, while the padding
that holds the content box off that edge is the bleed **plus** the inset, so the
glyph is always clear of a notch. Two properties that were fighting inside one
number, separated.

The formula was then exercised with the inset **stood in for**, because the
machine cannot produce one: at 50 px the shape still ends 24 past the glass and
the ink moves in to exactly 74. **A value that is zero everywhere one can measure
is a value nobody has measured.**

**Two arrangements were abandoned and each left a defect worth keeping.** The
piled one kept the corner button's room with a spacer, and wrapped, the spacer
went into one column while the button is anchored to the screen's corner: the two
parted company and the way out landed **on top of** the "movement" pill, both
measured at y=326. And the three-station one put a group at the top right, where
the status strip's own right end is the level meter — 32 px of it went **under**
the first pill, and it is the one indicator that says whether the room can be
heard. **Both are the same shape of defect: two things that must agree, each
worked out from its own end.** Neither can happen now, and not because they were
fixed — because the groups are centred and nothing of the rail goes near the
corner or the strip.

**The details close on the way in, and the stylesheet refuses them anyway.**
Opening them and then entering the mode left both panels in it, the details grid
across the top of the picture: `.stats.show` is worth the same 0,2,0 as the rule
that hides it and is written later, so at equal weight it commands, and
`.viewer.details .remote.show.ok` is worth 0,4,0 and commands whatever the order.
Climbing that ladder means hoping nobody adds a class. `!important` is what the
language offers for "this is not negotiable" and it is the same remedy `[hidden]`
already carries; the JavaScript closes the details so it never has to fire, and
**that is the point of it** — the protection downstream does not depend on the
line upstream having run.

**And the glyph had nobody to centre it.** `.bar button` carries the
`inline-flex` that puts a glyph on the optical centre, and the corner button is
not in the bar: it was a plain `inline-block` with an inline SVG, so the glyph
sat where text would start, left of centre and off the baseline. Reported by
looking at a photograph of a phone, and visible in no number the layout probe was
collecting.

**The safe area is added to the rail and the corner, not to the mode.** A padding
on the grid would inset the picture too, that is, letterbox the video to dodge a
notch it is perfectly happy under. What must keep clear of the rounded corner and
the home indicator is what one has to be able to *press* — and only in this mode,
because in the bar's company the stage's bottom is not the screen's.

**Twice the file's order defeated an exemption**, and the two failures look
alike. `.viewer.fs .side button` and `.viewer.fs .bar button` are both worth
0,3,1, so at equal weight the later one commands: written above the pill they
were inert. In the landscape shape that showed as the overlapping pills above; in
the tall one it showed as **nothing at all**, because there the exemption
happened to restate what the base already said. **A rule that loses by one place
in the file and a rule that is redundant are the same screenshot.**

**They are declared on `.viewer` and not on `.stage`, and that was a defect
first.** A custom property is inherited down the DOM and the bar is the stage's
**sibling**: declared on the stage, `var(--rail-btn)` did not resolve in the rail
at all, every declaration using it became invalid, and they fell away in silence.
Measured, the rail's buttons came out **36x36 with no scrim** instead of 48 with
one — the size of their own padding and glyph, which is what is left when a
`min-width` evaporates. **An undefined custom property does not complain**: it is
the family of the GUID that gives no error, in CSS.

**And the rail wanted the stage's other floor too.** It shares the picture's grid
cell, and a grid item's automatic minimum is its min-content: six targets plus
the corner's slot are 424 px, so in a viewport 360 px tall the row grew to 424
and the bottom of the mode went **off the screen**. `min-height: 0` is the same
line the stage already had, one element across.

**A control over the picture carries its own ground**, because there the surface
is whatever is being filmed: a scrim, a hairline and an ink, declared once for
`.overlay`, the corner button and the rail. And the two cover each other — over a
bright cot the scrim is what reads, over a dark room it is the white glyph, and
the hairline draws the shape in between. Verified over a real picture, not over
the black of a stage with no stream: on black a dark scrim proves nothing.

**The way in lives on the picture, bottom right, and the bar is left exactly as
it was measured.** The threshold of the single row is twice the wider side plus
the centre, so a command's label there is worth about two hundred pixels of it —
the history of this bar is that number being bought back twice, by shortening
"Record" to "Clip" and "Mute the room" to "Mute". Measured, in three states:

| the button | right side (it) | single row holds to | bar at 390 px |
|---|---|---|---|
| in the bar, with its word | 335 | 1150 | 153 |
| in the bar, word clipped | 221 | 980 | 153 |
| **on the picture** | **173** | **980** | **105** |

**The middle column is the threshold as it stood when Italian was the widest
language** — 980 then, 1010 for the chosen arrangement since German, and 1020
since Spanish — and the
third column is what those three arrangements measured at the time. The
comparison between the three rows is unaffected: it is the same bar measured
three ways, and all three would move together.

Clipping the word bought the threshold back and **not the phone** — 153 px at 390
is a third row, against the 105 the bar is documented to have. Only taking it out
of the bar gives both, and it is also where every player has already taught
people to look. **One node, and the same corner and the same shape in both
modes**: pressing it does not send the eye anywhere else and it does not change
under the finger. Two nodes would have been the copy that diverges, in
`aria-pressed`.

**What stays in the mode is not a matter of room.** The alert banner stays, and it
is the one thing here that is not negotiable: it is the only alarm the program
has, and a mode that hid it would be a baby monitor that says nothing at exactly
the hour it is watched. The warning box stays one step down, because it is what
explains the room's silence while somebody is speaking. The details, the "from
outside" panel and "Leave" go: they are done before or after, never while
watching, and "Leave" in particular has no business one thumb away from a rail
used in the dark.

**And the icon "Mute" wanted is drawn on the rail alone.** On the rail there are
no words so every button needs a glyph; in the bar this command has always been a
word, and giving it the glyph there too widened the left side by 24 px — which
with two equal tracks is 48, and it moved the width below which the group starts
sliding from about 956 px to 1004, that is, inside the single row **as it stood
at the 980 of then**. Measured then: 9 px of shift at 1000 and 19 at 980. **The
threshold stands at 1020 — 980 then, 1010 with German, 1020 with Spanish — and
the argument does not depend on the number**: the glyph costs the boundary the
same 48 px, so it lands above whatever the threshold is, and that is why it is
not drawn here.

**The word is clipped and not removed**, which is the defect that looks identical
on screen: `display: none` takes the accessible name with it and leaves a column
of buttons a screen reader cannot tell apart, on the one surface of this page
that is nothing but glyphs. That, and "every control that reaches the rail or the
picture has a glyph", are guarded from the source in `fullscreen_test.go`, with
**both halves derived rather than listed**: which ids the mode hides is read from
the sheet, so a new command must either carry an icon or be hidden, and the
choice is explicit. Verified to catch, on both.

**Of the defects above, the numbers found some and only looking found the
others**, and the split is the lesson. The evaporated custom property, the
min-content floor, the collision at the corner and the meter under the pill are
arithmetic: a probe reporting rectangles found them at once — and one of them
only after the probe was corrected, because "off screen" had been written as
"the right edge is past the viewport", which the bleeding pills make true by
design. **A criterion that was right for the previous shape quietly measures the
wrong thing in the next one.** The ragged wrap, the off-centre glyph and the
overlapping pills are not arithmetic: every number said fine, and what was wrong
was a shape nobody would choose.

#### The Home Screen is the only way to lose the browser's bars on iOS

`internal/server/manifest.go`. **No line of a page can hide them**: element full
screen does not exist before iOS 17.2 and Brave does not expose it at all, so
there the mode fills the page one can see and the address bar stays. Saved to the
Home Screen with a manifest the monitor opens with no chrome whatever, in either
orientation, and it does not depend on an API answering yes. It is also where the
guided path already sends people — "the bookmark one will not have to look for
again".

**The icons are drawn and not embedded**, which is the tray's decision applied to
the web: `internal/icon.Draw` computes the pixels in pure Go, so there is nothing
generated to commit, nothing for a build step to remember, and no second drawing
that can drift from the one in the executable. It costs a PNG encode the first
time each size is asked for, and the result is cached because the drawing is
deterministic.

Six things that are decisions rather than detail:

- **The Home Screen sizes are composited onto an opaque field and the favicon is
  not.** Apple states it outright: iOS **ignores the alpha channel** and puts the
  icon on black. Measured on what we were serving, the 180 was **27% transparent,
  all four corners included** — so the tile under the drawing was `#000000` and
  not the page's warm `#171512`, which is also the manifest's own
  `background_color`. The favicon keeps its transparency for the opposite
  reason: a browser tab gives the icon whatever colour the tab is, and an opaque
  field there is a small dark rectangle among the others. **Both directions are
  guarded**, because a test that only demanded opacity would be satisfied by
  filling all four. What nothing catches is the margin: the drawing runs edge to
  edge with 3 px to spare where Apple suggests twenty, and a round shape survives
  the squircle mask, so that one is a judgement and is left written down rather
  than fixed.
- **The name is taken, not repeated.** `version.Product`, the same string the
  tray and the data folder use: a second copy of "PAT Monitor" in an asset would
  part company at the first rename, and this is the file that names the thing on
  somebody's Home Screen for years.
- **`fullscreen`, with `standalone` behind it.** Standalone still keeps the
  status bar, and the point of this file is a mode that wants the screen; where
  fullscreen is not honoured the browser falls back by itself.
- **`start_url` is the viewer**, not the page that happened to be open when
  somebody saved it — saving from the recordings would otherwise give a Home
  Screen icon that opens a list of files.
- **The manifest and the icons are open**, like the stylesheets: a browser
  fetches a manifest before anybody has signed in, and an icon behind a session
  is an icon iOS saves as a broken image.
- **One route per size, from a list that is the authority.** A pattern with a
  wildcard would let a request name any number and make us draw it. The 32 is the
  favicon, and it closes a small hole of its own: with no icon link the browser
  asks for `/favicon.ico`, which has no route and so falls onto `GET /` — that
  is, a request for an image answered with the viewer's HTML.

**Apple's two meta tags say in their own words what the manifest says**, because
iOS does not read the manifest's `display` on every version, and `black` and not
`black-translucent`: translucent puts the page under the clock, and the top of
the viewer is where the alert banner and the state line live. The two cream pages
carry **no** `theme-color` — the night ground would be the wrong colour there and
a second literal copied out of the palette would be the wrong idea.

#### The glyphs are other people's, the semantics are ours

They are **Tabler Icons v3.46.0**, MIT, taken verbatim: `microphone`,
`mood-cry`, `dog`, `walk` in the bar, plus `volume-off`, `maximize` and
`minimize` for the full-screen rail — and `video` and `file-text` in the tray's
panel, which is the same set arriving as Go source. **Whoever adds one adds it
here too**, and the count is not kept anywhere: the licence note covers the set
and not the list, so a name forgotten in this sentence costs nothing legally and
costs the next reader the knowledge that it is not ours. They used to be
hand-drawn, and went wrong three
times running: the first dog's head read as a **cat**, the second smudged, the
third had become a paw print, that is, a surrender. **At 16 pixels drawing is a
trade** — there is a grid, a stroke weight and twenty years of convention, and
redoing it worse does not make it more ours. The onboarding illustrations and
the executable's dog stay ours: there the space exists, and the product's
character is in those.

**But the set does not decide the semantics.** Drawing **what the signal is
about** — a crying face, a dog, somebody walking — is our choice, and it is why
the three are distinguishable: three drawings of the *sensor* would all look
alike.

The rules for whoever adds one:

- the stroke is **`currentColor`**, so the icon lights with its pill without a
  second rule to keep aligned; and `flex: none`, because an SVG in a flex
  container lets itself be squashed silently;
- the **stroke weight** is the set's — 2 in the 24 grid — and is not retouched:
  changing it smudges them at the small size;
- the **gap from the text is 8 px** and no less, because the air inside the box
  is not the same for all of them (2 units for `mood-cry` and `dog`, 5 for
  `walk`). The gap is raised instead of correcting the tight glyphs: a table of
  per-icon adjustments is a second list that diverges at the first new glyph,
  and **erring wide is far less noticeable than erring tight**;
- the rule lives in `icons.css`, which both pages read. Written twice it would
  diverge, and on this project that is not a hypothesis: it happened with
  `--t-h1`, 28 in one sheet and 24 in the other with nothing saying so;
- the cost is `licenses/manually-added/tabler-icons/`, watched by
  `licenses_test.go`: the paths pasted into the pages are other people's work
  ending up in the binary through `go:embed`, and no tool can find it. **And
  from the tray panel two of those `d` strings travel as Go source** — there is
  no browser to draw an SVG, so the string lives in
  `internal/tray/glyph_windows.go`: same work, same set, same licence, and an
  attribution stopping at the `.html` would stop describing what is shipped.

**In the onboarding the glyphs sit on the commands, not on the drawings**, and
there are two: `copy` on the "Copy" buttons, and `external-link` on the buttons
that open a third-party site in a new tab — that is the case where an icon
**says something** that was not said before, because `target="_blank"` alone is
not visible.

Two things are not judged by reading the code: **a glyph is looked at** — the
cat and the smudge were visible in no other way — and **where the buttons wrap
is measured**, driving a real browser with the DevTools protocol. The tool is
disposable and stays out of the repository: the question can be redone in ten
minutes and is not worth a binary in `cmd/`.

### The configuration path, and the rules it must preserve

`/onboarding`, in `internal/server/web/`. **Four steps for whoever stays at
home, six for whoever goes out**, plus a failure screen. The order of the first
two is not negotiable: the password comes first because `CanExposePublicly`
refuses to expose without one, and the check at home comes first because asking
five minutes of configuration from somebody who has not yet seen an image is
asking for trust on credit.

**The path shows the real state, it does not narrate it.** From step 3 on,
`/api/status` commands, not an "I have done it" button: a user who declares they
have authorised when they have not arrives at the end with an address that does
not answer, which is the worst way to finish a guided path. Buttons stay where
there really is a choice.

#### "You have not signed in yet" and "you are no longer in" are the same 401

The heartbeat starts with the page and asks `/api/status`. At first start that
route answers **403** — no password, so no session — and the page declared it a
fault: a second after the welcome, a red "session expired, reload the page and
sign in with the password". That is, the first contact with the product was an
alarm ordering a sign-in with a credential **the user has not yet chosen**,
while the path was working: **a fault invented on top of something that was
fine.**

The two situations give the same answer and are opposite pieces of news, and
only **where one is coming from** separates them: `signedIn` remembers whether a
session ever existed. Before, the 403 is the normal state of step 0; after, it
is a dead session and must be said. It is the same shape as the bandwidth
collapse that does not exist while the estimate is rising: **two states
indistinguishable in their value, separated only by history.**

#### The total is not promised before the fork

The counter at the top said **"1 of 8"** to everybody, and whoever chooses "home
is enough for me" sees **four** screens: that is, a wall of eight steps was
announced to somebody who would take three, in the one instant at which they can
still close the window — the exact opposite of endowed progress.

So it starts from the short path, which is also the guaranteed minimum, and
extends to six when the user asks for the other — that is, **right after** the
fork has told them what it costs. The direction matters and is not symmetric:
announcing less and then extending is an accepted cost, announcing more and then
discounting does not repay whoever has already left. The two paths are `HOME`
and `OUTSIDE` in `onboarding.js`, and **the path is the one containing the
current screen**: whoever reopens `#p4` with the tunnel up would otherwise find
a bar that does not include that step.

The remaining time lives in a table **by position in the path**, not by screen:
the same screen costs five minutes to whoever goes outside and one to whoever
does not, and a single table would say something false to one of the two halves.

**And the counter was not the only thing promising it.** The welcome screen's
caption said *"without a password, access from outside cannot even be turned on:
that is why it comes before everything else"* — two screens before the fork, in
the same instant at which the reader can still close the window. Every word of
it is true, and it is the reason the **order** is not negotiable; it is also the
implementer's reason and not the reader's. To whoever chooses home it justifies
the first thing asked of them with a feature they are about to decline, and to
everybody it introduces *from outside* without the half that matters, which is
that it is optional — and that introduction already has a place, one screen
later, where the lead says it **comes later and is optional**. The caption now
says what the drawing cannot and what holds on both paths: every phone or
computer you watch the room from asks for that password — which is also the fact
the reader needs **before** choosing one, rather than after. **A reason that is
true of the code is not automatically a reason for the reader**, and the
question that separates them is whether its premise is already in their hands.

**And the first version of it said the same thing twice, which is how it went
wrong.** It closed with *"there is one for the whole house"*, meant as "one
password, every device" — which the first clause already asserts, because if
*every* phone asks for **it** there is only one. Said again it stopped being
redundant and became ambiguous: *una sola per tutta la casa* reads just as
easily as "only for the house", that is, a password valid at home and not from
outside — **the one ambiguity that screen must not raise**, on the screen from
which that very subject had just been removed. It went, and nothing was lost.

**The caption stays a caption, and that is the layout's doing, not the prose's.**
Removing it was the other road, and five of the nine figures carry none — but
this figure is the deliberate twin of the next step's, same frame and same two
positions with the padlock where the microphone goes, so a caption on one and
not the other is exactly the change of shape between consecutive screens that
composition was built to avoid.

#### A step is a stretch that asks something of whoever takes it

Preparing the secure connection occupied a whole step and its own screen
declares "no action required": it is a wait, not a step, and from outside it
made it look as though work was missing when only time was. So it shares its
step with "all ready" — they are the wait and the arrival of the same thing —
and it stays a screen of its own because the box explaining **why** that wait is
paid for now is exactly what stops it looking like a fault.

The general rule: a stretch is a step if a datum or a choice goes into it. If it
asks for nothing, it is a state of the one before or the one after.

#### "At home" is not localhost, and the interface is chosen by the system

The last screen gave `location.origin` as the home address, that is, almost
always `http://localhost:8080`. It is the one address that from the other end
**certainly** does not work: only this computer sees it. The fault is silent
because the page showing it runs right there, and it is discovered only with the
phone in hand.

So the program computes it (`homeAddress`) and it arrives in the state as
`localUrl`. **The interface is not chosen by us**: a UDP socket is opened
towards 192.0.2.1 — the network RFC 5737 reserves for documentation — and the
address the system would start from is read back; no packet is sent, because
`connect` on UDP just consults the routing table. Measured on this machine,
which has **eleven** IPv4 addresses, any hand-written heuristic gets it wrong:
"the first" takes a link-local (`169.254.x`), "the first private one" takes a
virtual adapter (ICS, VMware). **Ask the system instead of deducing.**

**And the tray, which already had that answer in the state, recomputed it.**
`trayStatus` wrote `LocalURL: localURL(cfg.ListenAddr, "/")`, which with a
listen on every interface answers `localhost`, and threw away the field
`homeAddress` had filled a moment earlier: **the page received 192.168.x.y and
the panel localhost, from the same state and in the same instant.** It showed
**only before the tunnel came up**, that is, in the first two or three seconds
after start-up, and what showed was not a wrong line: it was an address that
holds only for this computer **with a QR code under it leading there**. A false
affordance costs more than a missing one.

**The real cause was not the recomputation: it was that the field did two
jobs.** There are two readers and they want two different addresses: the **click
on the icon** opens a page **here**, and there `localhost` is the better answer
— it works even with no network, and it is the same origin as `SetupURL`, that
is, the same session instead of a password asked for a second time; the
**panel** shows and engraves an address handed to **another device**, and there
`localhost` is the one that certainly does not work. Hence `tray.Status.OpenURL`
and `tray.Status.HomeURL`, and the rule: **a field two readers use for two
purposes is not tuned, it is split.** Covered by
`TestTheAddressHandedToOtherDevicesIsNotLocalhost`, which looks at the two
together because the defect was that they were one.

**And `homeAddress` never returns empty**, which is the tail of the same story:
with no IPv4 route its last fallback is `localhost`. For the page that is fine;
for the panel it is not, because there that address is **shown** and **engraved
into a QR code**, that is, it invites a gesture that cannot succeed. So whoever
hands it over filters — `addressOthersCanReach`, which closes **both** roads,
the name and the number: looking at the word alone lets `127.0.0.1` through,
looking at the number alone lets `localhost` through, and **a test on one value
alone absolves the defect it should catch.** `homeAddress` was not corrected
because for the other reader that fallback is the right answer.

What is gained is not one more connection — down there the monitor is
unreachable anyway — it is **not promising one**. The panel already knows how to
handle the empty case, and that was **looked at** rather than deduced, by
forcing the fallback in a throwaway build: no line, no code, no space held, and
the panel shrinking from 977 pixels to 636. The click on the icon stays, and
that is the half that must not follow the other — with no network the monitor
still works at home.

Beside the address is the **QR code**, and there it is not a luxury: it is a
number with dots plus a port, the phone is another device, copying is no use and
typing gets it wrong. **And it sits in the same box as the address**
(`.addr.con-qr`), not in a box underneath: there were two, and in a column they
read as two things to do, while they are two ways of doing the same one —
getting there.

#### Arrival is declared, not merely reached

The last screen listed addresses, QR code and warnings, and said nowhere **what
had just happened**. Whoever took the long path has just set up a private
network and a valid certificate without knowing they did, and it is the only
moment at which saying so does not sound like boasting, because the credit is
theirs. The line is composed by `composeFinal`, in two versions: the two feats
are not the same thing and one sentence would flatten both.

#### The explainer is a box, not a step

"How it works" was the fourth step and everybody read it to serve the few per
cent who wanted it. It is now a `<dialog>` opened from the fork and from the
authorisation screen — the two places where doubt arrives.

**But not everything went into it**, and that is the part that counts. That
screen did two jobs: it explained the meeting point, and it answered the three
questions that *stop* people — it is free, no new password is needed, nothing is
touched on the router. Those three stayed in clear text on the fork, where the
decision is taken: buried behind the link, the result would not have been "one
step fewer", it would have been somebody pressing "yes" and finding themselves
facing a request to sign in with Google to a service they have never heard of,
that is, the moment of maximum suspicion in the whole path. Inside the box goes
what the fork cannot hold: **that it is a VPN**, that the video takes the short
road and does not pass through the meeting point, and that the network stays the
user's and is useful for other things too.

A native `<dialog>` and not one more section, for two reasons. Opening it **does
not move the path**, so the anchor in the address and the bar at the top go on
telling the truth. And it brings Esc, focus trapped inside, focus returned to
the button that opened it, and the rest of the page declared inert: the four
things hand-made modals lack, and none of them is noticed until one tries with
the keyboard alone.

Two `<dialog>` traps, both paid for:

- **A height cap is not enough to make it scroll.** With the box as a block,
  `max-height` merely clips: the child inherits no limit, its `overflow-y: auto`
  has nothing to scroll, and what overflows disappears under the edge —
  including the "got it" row. A flex column with `min-height: 0` on the body is
  needed.
- **The `display` goes on `#explainer[open]`, never on the box alone.** A closed
  `<dialog>` is invisible only because the browser gives it `display: none`, and
  that rule is beaten by any `display` we write with an id: the box stayed
  **always open at the bottom of the page**, under the current screen. It does
  not look like a styling defect: in a `<dialog>` the `display` **is the
  switch**, and touching it outside `[open]` does not change the appearance, it
  changes whether the thing exists.

**And the path never goes back on its own.** The phase oscillates at start-up —
the node connects, then asks for the certificate — and taking the user back to a
screen already passed reads as "something went wrong". One only advances, except
towards the failure screen.

The principles that designed it, listed because they must survive somebody
modifying it:

- **One decision per screen.** One primary action and at most one secondary;
  where there are three possibilities, the third is a text link under the
  buttons.
- **The way out is always visible but does not compete.** A path with no exit
  does not make people complete: it makes them close the window, which is worse,
  because whoever closes does not come back.
- **The cost is declared before it is charged.** "Five minutes, once" must be
  said *before* opening the link: an announced cost is borne, the same cost
  discovered half way makes people abandon.
- **The technical detail exists but stays closed**, inside a `<details>`: hiding
  it entirely would remove the only handhold for whoever can read it.
- **An error always says what still works.** The failure screen opens by
  declaring the monitor at home is active: it is the interface translation of
  the invariant on separate lifetimes.
- **Names the user recognises**: "secure connection", not "TLS certificate". The
  only exception is text arriving from Tailscale, which is not touched.
- **Targets of 48 px, visible focus, motion that can be switched off.** The
  computer holding the monitor may be attached to a television and be badly
  controllable: the keyboard has to be enough.

Three things the prototype did and that in the product would be lies: **the
level bars are not animated on their own**, they are commanded by the level read
from `/api/status` — bars dancing by themselves would say "I hear" precisely in
the case where nothing is heard, which is the fault that step exists to find;
**the preview is the real stream, not a picture**, because the step exists to
show the camera works and a drawing does not show it (muted on purpose — the
audio is shown by the bars, and without audio the autoplay block does not
trigger); **administrator and member are distinguished by the address, not by
the text**, because `QueryFeature` returns the link that enables everything
**only** to whoever has the right to use it, and it is the service that knows.

**Two consecutive screens share the layout, not only the style.** Two different
compositions in a row read as a change of book, and the remedy is not to move
the figures until they agree — that produces a third drawing that is neither the
first nor the second — but **to take the first and change what is inside it**,
same positions and same labels.

**The visual vocabulary**, to be respected if an illustration is added: teal is
service traffic and things in progress, green is the media and things that
succeeded, **red appears only** on what we do not do or what does not work. The
house is **one drawing** repeated with a `translate`, changing the phase shade:
drawn four times with different proportions, leafing through the steps it read
as four houses instead of as **the** house. No animation carries information,
and `prefers-reduced-motion` switches them all off with no loss.

#### The box promises, the row beside it explains

The check screen has two rows — camera, microphone — each with a detail that
appears only when that row is bad, and under them a `note warn` box that is
always there. **They are two jobs and the screen had them muddled.**

The box carried an example: *on a laptop it happens when you close the lid: the
microphones go, the camera does not.* That sentence was **already on the same
screen, word for word**, as `onb.s1.mic-bad-detail` — which is where it belongs,
because that is the row it explains and it appears exactly when it applies.
Nothing said so: a duplicate reads perfectly well in a diff of one file, and the
only thing that finds it is reading the screen from the top as its reader does.

**And the box asserted its invariant in one direction only.** *If the microphone
turned out to be missing, the monitor would carry on showing the picture* — true,
and half of what the code guarantees: the two lives do not switch each other off
either way round, and `runVideo` failing must not silence the microphone any
more than the reverse. The lid is why the half was never noticed: it is a cause
that can only go one way, taking the microphones and leaving the camera, so an
example chosen from it cannot state the rule it is an example of.

**What made the missing half reachable is the chapter above this one.** A
revoked permission is two separate switches in Windows, so the camera can go
while the microphone stays — the direction no lid produces. The rule had been
half-written since before there was a way to reach the other half.

So the division is: **the box states the promise, in both directions and with no
example; the rows state the causes, each where it applies.** The permission's own
explanation is a row detail for the same reason, and it sits three lines above
the box — which is the other thing that made the example redundant.

**And the command that acts on a cause goes in that cause's row.** The Windows
settings page is offered as an `a.btn.ghost` inside the row, with the glyph the
three Tailscale links already use, because `ms-settings:` really is elsewhere.
It is the tray panel's rule met on a page: an affordance three elements away
from the sentence saying *which* of the two devices was refused reads as
belonging to something else — and here there can be **two** of them at once,
since the permissions are two switches, so the row is the only thing that says
which is which.

**It appears only where it can work.** `ms-settings:` is a Windows shell
address, so from a phone it does nothing, and a dead affordance on the screen
that is diagnosing a fault is worse than none: the reader cannot tell a link
that did nothing from a permission that did not take. The test is
`location.hostname` being a loopback one, which is sound because this page is
served by the monitor itself — a loopback host means the browser is on the
machine the permission belongs to. Opening the path by its home address **from
that same machine** answers no, and that false negative is the direction to err
in: it costs the button and keeps the sentence.

**Which is why the sentence names the page rather than commanding it.** It used
to read *Open Settings › Privacy & security › Camera and turn camera access on* —
a command, which beside a button that is also a command is the same instruction
twice, and on a phone is the only one there is. It states where the switch is
now, and the button offers to go: **the line names the thing, the command says
what pressing does**, which is the division the recordings page's lock and the
tray's own notice both already make.

**`hidden` is the switch, and it works here only because of a rule elsewhere.**
`.btn` writes a `display`, which beats the attribute; what saves it is
`[hidden] { display: none !important }` at the bottom of the sheet — the chapter
below this one, met from the side where it would have left a button permanently
visible with `href="#"`.

**No guard, and the argument is the alerts chapter's own**: what would have to be
checked is whether two sentences on one screen say the same thing, and whether an
invariant is stated in as many directions as the code holds it. Neither is a
property of a string. What protects it is that the same rule is written twice,
once as code in `Run` and once as prose here, so writing the second is the moment
to read the first.

#### `hidden` hides nothing if a sheet writes a `display`

`[hidden] { display: none }` lives in the **browser's default sheet**, and any
`display` we write beats it — not by specificity, but because author style
always beats the default. So an `.addr.con-qr { display: flex }` is enough for
`element.hidden = true` to stop hiding, with no error anywhere.

Measured on the guided path, **four** elements marked `hidden` were visible:
whoever chose **not** to go outside arrived at the end and found an empty
address box and a green tick on something they had not done — the arrival screen
declaring a feat never performed.

It is the third time this family has appeared, after the `<dialog>` that stayed
open and the label that erased the icon. The rule now lives **once and for all**
at the bottom of every sheet that dresses a page: `[hidden] { display: none
!important; }`. The `!important` is what the language offers for saying "this is
not negotiable", and it is the remedy the specification recommends. It is in
**both** sheets because the two pages share none, and
`TestHiddenMeansHiddenOnEveryPage` watches it together with the fact that the
pages really load those sheets — otherwise the test would be looking at a file
nobody reads.

#### A class nobody looks up any more does not complain

Moving the QR code **inside** the address box, the class `.inquadra` disappeared
from the markup. The JavaScript went on looking for it with
`closest('.inquadra')`, which does not complain: it returns `null`, the line
throws, and the instructions **after** it do not run — among them the assignment
of the address, so the authorisation step showed its dash for ever. Reported as:
"the Tailscale address to open does not appear, while in the tray the message to
authorise the device does" — the observation that pinpoints the defect, because
the two halves read the **same** phase and it showed only one.

**Renaming a class is a change to the stylesheet**, and nobody goes looking for
what interrogates it from JavaScript:
`TestEveryClassLookedUpFromTheCodeExistsInItsPage` does, for every `closest` in
the two pages. It removes comments first — the remedy to a defect tells its
story, and the story names the vanished class — and looks for the class among
those **declared**, not as a substring, otherwise "addr" would be found inside
"addr-actions".

**And an exception in the state round must not be able to stay silent.** The
comment on the polling guard already said it — a path that stops following the
state and does not declare it is worse than one that stops with an error — but
it held only for the network request. The fault came from the other half: the
round went on turning, the rest of the page updated, and the tunnel part was
frozen for ever. That stretch now sits in a `try`, and whoever throws declares
it at the top of the page.

#### Where the real thing is on the page, its drawing does not explain: it imitates

The last screen had an illustration with a QR code, an example address and a
phone, **above** the two boxes carrying the real codes and the real addresses.
Three defects in one drawing: **two addresses on the same screen, one of them
false** and neither declared as an example; **a drawn code that can be scanned**
and leads nowhere, that is, an invitation to do exactly the gesture the step
teaches, ending in the void; and a caption the two real boxes already gave, in
bold, both of them.

It is the preview's rule — "it is the real stream, not a picture" — one step
further. **A drawing earns its place when it shows what cannot be seen there**,
like the Tailscale window at the authorisation step, which explains a
third-party site the user is about to go to.

### The QR code is engraved, not imported

`internal/qr`. Same choice as the tray icon, and the same reason: one
executable, and a piece that fits in one file and will never change — the QR
specification has been fixed since 2000. It deliberately covers one slice: byte
mode, correction M, versions 1-6, that is, 106 characters, and so the two most
insidious pieces disappear: the version information, which exists only from 7
up, and the alignment-pattern table, which from 2 to 6 is one pattern in a known
place. Beyond 106 characters an error is returned: **a code that scans and leads
to a truncated address is worse than an absent code.**

**And the route that serves it is for whoever is in the house.** It takes the
address from the query — deliberately, because the page shows one at a time and
knows which — and it was reachable from the Funnel with no authentication: an
encoder, on the owner's own trusted address, of a code leading wherever the
caller wrote. Nothing is injected, the text never reaching the SVG; what is
served is the trust of the origin. A QR code is pointed at with a phone in the
same room as the screen, so from the Internet this route has no use at all, and
both pages that ask for one are walked at home — step 4 shows Tailscale's
authorisation address and step 7 the public one, and both are read off a screen
that is here.

**A QR encoder is built to fail silently**, and that is why the verification was
done the way it was. The matrix looks right in every part — finders, timing,
quiet zone — even when the data inside is rubbish, and no reread notices. So it
was compared module by module with `github.com/skip2/go-qrcode`, added for the
length of the test and then removed: the same road as `pat-opus`. The test found
the **generator polynomial built backwards** and absolved everything else — a
one-line defect, invisible by construction to any inspection.

Surviving the removed dependency are a **frozen fixed-mask reference** and the
**syndrome check**, which does not recompute the correction but verifies its
property — a complete codeword is divisible by the generator — that is, it asks
the question a real decoder asks instead of redoing the arithmetic with the code
that produced it. Two divergences remain legitimate: the two implementations
**choose different masks**, and each of the eight gives a valid code; and where
the text has long runs of digits go-qrcode switches to numeric mode.

**And the last test was done by phones.** Two implementations agreeing module by
module say the arithmetic is the same, not that a real decoder reads the matrix:
only pointing a camera at it says that. It is the same shape of cross-check as
`pat-opus` — the confirmation is given by something we did not write.

**One vocabulary of motion**: everything smooth and eased. Traffic in the
diagrams was a dash at constant speed — the only linear-time thing on the page —
and looked like a network diagram that had wandered into a picture book. The one
deliberate exception is the audio level indicator, which moves fast because
there **speed is the information**.

### Only the stars one can see pulse, and the price is per element

The configuration path warmed the machine up, and the report was a question:
"is it the stars and the grass?". It was — 105 stars pulsing and 43 blades
swaying, all of them `position: fixed`, so they run on **every** screen of the
path for as long as it is open. Measured on this machine, at 120 Hz, as CPU time
of the renderer processes:

| | cores |
|---|---|
| the backdrop with nothing moving | 0.098 |
| grass only (43 blades) | 0.175 |
| stars only (105) | 0.283 |
| both, as it was | 0.343 |
| **25 stars and 8 blades** | **0.199** |

**The answer is not "the stars": it is the number.** Per animated element the
price is the same to within noise — 1.79 milli-cores for a star, 1.86 for a
blade, and 1.75 for the eight that were left — so the only lever is **how many
move**, not which kind, and there is no cheaper animation to switch to. What
made the stars the expensive half is that there were two and a half times more
of them. On a 60 Hz screen halve every figure in the table.

The reason it costs at all is that these are **SVG children**: a `transform` on
one is not handed to the compositor the way it is on an HTML element, so every
frame is a style recalculation and a repaint of the SVG that contains it. The
sky is nearly the whole viewport.

**What was done is the reported proposal, measured**: the twenty-five stars one
can actually see keep the pulse — corner scale ≥ 0.5, loose ones ≥ 8 px — and
eight blades of the front plane keep the sway. 0.38 → 0.225 of a core with the
old behaviour put back in the same batch, that is, **41% of the whole page**.

- **A still star is not a dimmer star, it is a brighter one.** With no rule of
  its own it sits at full opacity, while the pulsing ones oscillate between .34
  and .74: the eighty that stopped moving would have become the conspicuous
  ones. `.star` therefore carries the **time-average of the animation**, which
  is exactly the midpoint — .54 here, .65 on the login page, whose keyframes are
  the night palette's .3 to 1 — because the value is linear in a progress whose
  easing is symmetric. **This is arithmetic and not a judgement**, and it was
  not visible in a screenshot: two frozen frames differ by the animation's phase
  far more than by the bias.
- **The class was split because one name was about to lie.** `.twinkle` on a
  star that does not twinkle is the `.msg` defect again, so it is `.star` for
  the star and `.star.pulse` for the ones that pulse — `transform-box` and its
  comment moving to where the scale actually happens. The blades went the other
  way: a still blade needs **no** class, because `rotate(0)` is the geometry it
  was drawn with, so `.blade` still means "this one sways" and the inert
  durations went with the class.
- **Which eight is chosen by hand.** The meadow's spacings are engraved rather
  than computed because a regular one reads as a printed pattern, and the
  durations sit on each blade for the same reason: a selection of every fourth
  blade would be a third periodic pattern over the two. They are non-adjacent
  and irregularly spaced across the width.
- **Both pages, and only two.** `login.html` carries the same 105 stars from
  `style.css` — the two sky blocks are identical bar a comment — while the
  viewer, which is the page open all night, has no backdrop at all and never
  had this cost.
- **What was not taken.** Dropping the `scale` from the pulse buys a further
  ~12 points (0.216 → 0.164 in one batch), because the scale is what invalidates
  the raster. It was refused: the twenty-five that were kept are precisely the
  visible ones, and without the swell they blink rather than twinkle. **The
  saving was already had by counting, and the second one would be paid for in
  the only place where the animation shows.**
- **And stopping the last eight blades buys nothing measurable.** Asked for
  after the change, it read 0.252 and 0.269 with them and 0.342 and 0.239
  without — once above and once below, that is, **under this instrument's noise
  floor**, which at this level is about 0.05. The wind stays. What is worth
  writing down is the shape of the answer: at 1.75 milli-cores each, eight
  elements are not a quantity this measurement can resolve, and the honest
  report of that is "no difference", never the mean of two contradictory
  readings.
- **The meadow's two transparent waves were never animated**, and the question
  of stopping them has no answer because they are the ground, drawn once. The
  dog's tail and ear are the two things still moving there: some four
  thousandths of a core at the rate above, derived from it rather than measured
  separately — and that is said instead of being implied.

**The instrument, which is the part worth keeping.** Four traps, and each one
made the measurement say something false before it said anything true:

- **In headless, `prefers-reduced-motion` is `reduce`**, and the sheet switches
  every animation off under it: the first run reported 0.2% of a core for both
  the animated page and the still one. It is already written down for the wave
  in the guided path, and it was met again here — `Emulation.setEmulatedMedia`.
- **Frames are not produced unless somebody consumes them**, so a headless
  window animates nothing. `Page.startScreencast` makes them, and then the
  JPEG encoding costs more than the phenomenon: the subsets read **above** the
  whole, which is impossible and is how the tool declared itself broken. The
  answer was to stop measuring headless — a real window, no screencast, and only
  the `renderer` processes of `SystemInfo.getProcessInfo`, which is CPU time and
  therefore the quantity the report was about.
- **The level drifts between batches and holds inside one.** The same variant
  read 0.34 in one batch and 0.44 in another, with 0.093 against 0.097 between
  two runs of the baseline a minute apart. Comparisons are therefore interleaved
  within a batch, and a difference of 0.04 means nothing.
- **The page counts its own frames**, and that is what caught the two runs that
  had stopped measuring: `fps 0` on a variant reading 0.005 cores, and `NaN` on
  the next. Without that column those two numbers were the best result of the
  session. **A tool that stops measuring and does not say so makes what it
  measures look fixed**, which is the same lesson as `pat-viewer`'s truncated
  buffer, one direction across.
- **And it leaked forty-four browsers.** On Windows `child.kill()` reaches the
  process that launched the browser and not the tree under it, so every run left
  its renderers behind — reported from the other side of the desk, as a
  clogged machine, with 4.4 GB of throwaway profiles on the disk too. It is the
  same shape as `dlv attach`: **the diagnostic tool has to be able to leave.**
  Killing them wants the profile path as the filter, never the process name:
  thirty-seven of the eighty-one were the user's own session.

### What only the server knows cannot be filled in by a route

The tray panel declared **zero devices connected** with a phone connected, while
the same line on the page counted two — in the same instant and from the same
state.

That number is the one thing the state's producer cannot know: sessions are held
by the server. It was filled in **inside** `apiStatus`, that is, inside the HTTP
route, and there are two readers — the page, which goes through it, and the
tray, which calls `StatusFn` on its own account and received the type's zero
value. It is the rule that file already states — **whoever owns a datum
publishes it** — missed in the easiest way there is: the datum *was* published,
but by a route, and **a route is called by only one of the two readers.** Now
`server.Status()` composes it, and the route does nothing but write what it is
handed.

**The defect was not a wrong value: it was a value filled in the wrong place**,
and no test on the value catches that — from the side being watched the value
was right. The two guards therefore watch the **shape**:
`TestTheRouteComposesNothing` refuses any assignment inside `apiStatus`, and
`TestTheTrayAsksTheServerForTheStatus` reads `main.go` and requires the tray's
state to come from the server.

### Two roles with one name lie, and they do not collide

`.msg` was two components. In `style.css` it is a **box** — border, background,
the `.note` vocabulary — because on `/login` and `/setup` that message is the
only answer the page gives and it must be findable. In `onboarding.css` it was a
line of text under the field it belongs to, where a box would compete with the
step's real `.note`s.

**No page loads both sheets, so they did not collide: they lied.** It is the
case where one does not unify — they are two roles — but separates the name: the
box keeps `.msg`, the line under the field is `.field-msg`, and **neither
changes shape**, measured from the page. With the name went a dead class: the
script wrote `'msg show error'` and in that sheet **no rule reads `show`** — the
space is reserved by `min-height` so that an arriving message does not move the
button. A class nobody interrogates does not complain, and a rename is the
occasion to notice.

### A drawing that follows the measurement is a second instrument in disguise

The three bars of the microphone illustration, at the first step, followed the
real level read from `/api/status`, and the reason was good: they were the only
indicator, and **bars moving on their own would say "I hear" precisely where
nothing is heard.** The argument stopped holding when the real VU meter appeared
beside them — the decibels written, the peak that holds, the four states named
for whoever cannot see. From that moment there were **two** things on screen
measuring the same quantity, and one of them was a drawing.

Now a wave crosses them, phase-shifted by a third of a period from one bar to
the next. It is not an aesthetic choice: with 120 degrees between them the three
**are never at the same height** — measured, ten samples out of ten — and no VU
meter shows a crest travelling. **A drawing that resembles no instrument cannot
be mistaken for a measurement.** Under `prefers-reduced-motion` a still waveform
remains, and not three equal bars, for the same reason.

It is the rule of depictions applied in the opposite direction: **what we draw
of our own follows the palette because there fidelity is following the palette;
what we draw must not follow the measurement, because there fidelity is
declaring itself a drawing.**

**And the wave is clipped, not squashed.** The first version shortened the bars
with `scaleY`, and a `scaleY` shortens the radius too: at a fifth of the height
the rounded ends become ellipses and the pill deflates. **A radius is a
measurement in pixels, and no scale leaves it in pixels.** The remedy is not a
different value, it is a different mechanism: `clip-path: inset(N% 0 0 0 round
5px)` removes the top of the whole pill and re-rounds the edge it has just
created, with the same radius as the rectangle's `rx` — the shape is identical
at every height, what changes is how much of it is seen.

**And in headless, `prefers-reduced-motion` is `reduce`.** Measuring without
declaring it one reads the still branch and concludes the animation is not
there: `Emulation.setEmulatedMedia` turns it on, and both tests must be run
because they are two different drawings.

**And the sentence that pointed at the drawing stayed pointing at it.** The
caption under that figure read *"the bars must move when you speak. If they stay
still, the problem is here and nowhere else"* — written when the three bars did
follow the level. After the wave it names the wrong object **and** says
something false about it: those bars move whatever the microphone does, which is
the whole point. Worse, it is the one sentence on that screen that tells the
reader how to read the test, so a stuck meter beside a moving drawing would read
as everything being fine. It now names the instrument: *the bar under the
video*. **Not "under the camera's picture"**, which was the first attempt and
walked back into the ambiguity it was removing: with a drawing of a camera
immediately above, that phrase reads as the drawing just as easily. *Video* is
the one word there that cannot mean a picture of something.

**The change had three readers and updated one.** The bars, the caption, and a
source comment beside the preview still saying *"the audio is proved by the
bars"*. It is the question the codes chapter already asks at every change of
shape — not "who writes it" but **"who reads it"** — and a drawing is read by
its caption before it is read by anybody.

**And it had a fourth, which the count itself missed.** The failure row's detail
still said *"the bars stay still"*, in the plural — that is, after the wave it
names the only bars on that screen that move whatever the microphone does, and
it says so **precisely** in the case it exists for: whoever has a mute
microphone reads that the bars are still while watching them travel. Same
defect as the caption, one reader further on, and it survived because the
correction was made where the report had come from. **A count of readers is
itself a hand-written list**, and what found the fourth was somebody asking what
a sentence meant.

**And half the corrected caption went on pointing at nothing.** *"the problem is
here and nowhere else"* was written when the bars were the instrument, and it
meant: the fault is in the capture, not in the network nor in the steps still to
come. With the object corrected, *here* points at the bar — an instrument, not a
place a fault can sit — and *nowhere else* contrasts with a set the reader does
not have yet, because at step 2 there is no outside access, no phone and no
chain to exclude. **It is the router condition's defect one step across**: a
contrast the reader cannot make is not a contrast. The diagnosis it stood in for
already exists in the row that fires exactly when the bar stays still, so it
went, and the caption states the fact alone — which is also why the second half
survived the first correction, being read as a lesson about the object and not
about the clause hanging off it.

**And removing it left the caption naming half its subject.** The figure has two
halves, a camera and a microphone, and with the clause gone the caption spoke of
the bar alone — which was the next question asked of it, and a fair one. The
asymmetry is real, and it is the thing worth saying: **the camera proves itself
and the microphone cannot**, so one half needs an instrument pointed out and the
other needs nothing. The caption hands over to both — *the video says whether it
sees and the bar whether it hears* — in the screen's own pair, the one already
in its title and on its button, rather than in a third way of saying it. It
keeps *video* and not *picture* for the reason above, the drawing sitting
immediately over it; the two `cam-` rows go on saying *image*, which is what the
camera produces and not the element on the page one is being sent to look at.

### A high level and any level were the same colour

The guided path's VU meter has two thresholds and names four states — `silence`,
`normal`, `loud`, `very loud` — with the decibels written beside them. The
viewer had a band of **six pixels of one colour**: a level showed, a **high**
level did not. That is, the distinction lived in the page looked at once, in
daylight, with the audio verifiable by ear, and was missing where the only one
that counts is.

The thresholds are the same — 0.72 and 0.92 — and copying them is not a
convenience: the measurement is the **same formula on the same stream**, RMS
scaled from -60 dBFS to 0, so the two numbers talk about the same quantity.

**But the peak is light and not brick, and here the two pages must diverge.** In
the guided path saturation is a calibration fault and `--c-stop` is right. On
the viewer, brick is the shade of failure — the alerts banner, the broken dot —
and **a loud cry is not a fault: it is the thing this program exists for.**
Saying it in red above the video would teach people to read as an alarm the one
moment at which the monitor is doing its job. Green, amber, light; brick stays
with the banner.

**And the band can now be read without being seen.** Six pixels of colour say
nothing to a screen-reader user: `role="meter"` with `aria-valuetext` carries
the word, and it is not a live region — it is read when asked, not announced at
every frame. When the stream ends, **the colour and the word go back to zero**
as well: staying lit they would say "loud" about a stream that no longer exists,
which is the defect of a number that ages instead of measuring.

The four keys are written out in full and not as a prefix plus a code: there is
no authoritative Go list to expand them from, and a prefix without that list is
a family the catalogue's guard cannot verify. Tested on both sides of every
boundary by calling `levelState` from the real page and reading the colour back
from the sheet: 0.71 green and 0.72 amber, 0.91 amber and 0.92 light.

### A meter that cannot measure does not draw silence

The viewer's VU meter stopped moving with the room's audio perfectly audible,
and the serious part is not that it stopped: it is that it stopped **at zero**,
that is, declaring the room quiet. On a baby monitor that is misplaced
confidence in the worst place — the same family as the microphone delivering
zeros with a green page.

**The two causes are different and both are needed.** **The audio context's
state is checked every round, not only at hook-up**: on iOS an `AudioContext`
can go to `interrupted` — a phone call, another app, the screen locking — and
from there it does not come back on its own, while the `<video>` element goes on
playing because it does not depend on that context. The code checked once and
only for `suspended`; now it retries **every two seconds and not every frame**,
because the cadence of a retry is not dictated by whoever wants it. **And the
old loops did not die**: every reconnection creates a new stream, and with it a
new analyser and a new `requestAnimationFrame` loop, while the guard was `if
(!audioCtx || !stream)` and `stream` is never null — it is **replaced**. After
three reconnections there were three loops at sixty rounds a second, two of them
reading a dead analyser; now there is a generation, and whoever notices they are
no longer the last one exits.

**The drawing says the difference between empty and unknown.** At level zero the
bar is zero wide, so "silence" and "I do not know" drew identically: the track
is now dashed, and the word is carried by `aria-valuetext`. **And disconnection
must stop the loop, not merely zero the bar**: the loop does not watch `pc`, it
watches `audioCtx` and `stream`, which on close stay where they were — zeroing
alone, sixteen milliseconds later the still-live loop did its round, removed
"unknown" and rewrote "silence" from an orphaned analyser, that is, declared a
quiet room for a stream that no longer exists. First move the generation, then
zero.

**And the warnings box is no longer touched from here.** While the line existed,
the branch that does not measure went through it sixty times a second and
rewrote it: `showWarning` recomposes the text and class of a box **shared** with
talk-back and filtered audio, so repainting it all night means rewriting a
warning that may belong to somebody else.

**Tested with an oscillator in place of the room**, driving a real browser: the
meter reads the tone (-14 dBFS, "loud"); suspending the context **with no way
out** — that is, like iOS, where `resume` outside a gesture does not take — it
declares "not measurable" instead of silence; on resume it measures again; and
moving the generation, the bar **stops where it was left**, which is the proof
that no old loop is still writing.

#### The line that explained it was a prediction, and the case was not rare

Beside the dead track was a sentence in the warnings box — "the audio level
cannot be measured on this browser" — written for a case imagined to be
sporadic. **It was not sporadic and it was not the browser's**: opening the
recordings and coming back to the monitor was enough, and it appeared every
time.

The cause is the good half of another feature. Whoever comes back finds the page
reloaded and the audio starting by itself, because the choice to listen is
remembered and the video gets it back without asking — and that is right. But
**an `AudioContext` created outside a gesture is born suspended**, and
`resume()` alone does not wake it: the browsers' policy requires an activation
of *that* document. What came of it was perfectly audible audio and a frozen
meter, that is, a sentence that besides always appearing **accused the browser
of a fault of ours.**

The remedy is not to declare it better: **the gesture is not asked for, it is
waited for.** `resumeOnFirstGesture` — the same job as `listenOnFirstGesture`
for the video element — arms a listener on the document, once and with automatic
disarming, and the first touch of any kind restarts the context. The sentence
went with its two catalogue entries; **what remains is the dead track**, which
states instead of explaining — that is the night page's rule. It follows too
that that branch no longer runs at sixty frames a second to reread a state that
changes only on a touch: it is drawn once, the context is retried every two
seconds, and the reread goes through a quarter-second timer. **And the case that
remained — iOS suspending and resuming on its own — was not lost**: it is the
periodic retry, which was already there.

**And the feature whose good half caused it has since gone.** Remembering the
choice bought one tap and paid for it twice, both times reported from a phone.
`video.play()` **resolves on an element that is already playing**, so the
promise came back yes and the gate hid itself a moment after appearing — an
instruction that goes away with nobody having touched it, which is worse than
one that stays. And the tap it removed is the tap this chapter is about: the
wait for the next gesture anywhere is silent by construction, so what was on
screen was a dimmed meter beside audible audio and nothing saying what to do
about it. **A wait for a gesture nobody has been asked for is not a remedy, it
is the same fault with a listener attached.** The gate is now asked for at
every opening — `localStorage` and the two functions around it are gone — and
the press on it is the activation that wakes the context. What
`resumeOnFirstGesture` still covers is iOS's own interruption, a context that
was running and goes to `interrupted`, which no press of ours precedes.

**And the sentence lost its clause with it.** `viewer.gate.why` promised the
tap was needed "just once", which was true only while the choice was
remembered: leaving it there would have been the interface asserting a feature
that no longer exists — a knob that moves nothing, one register across.

**With the sentence removed, the track must be born unknown.** In the markup the
band started without its class and without `aria-valuetext`: until the gate is
lifted nobody measures, and an empty track reads as a quiet room — that is,
precisely the assertion this chapter exists not to make, at the one moment at
which there was no longer even a line to say it.

### The codec warning was a prediction, and predictions err both ways

The recordings page promised an up-to-date iPhone that it would not hear the
audio, and the audio was perfectly audible. The cause is that the warning was
decided by `canPlayType` alone: on WebKit that answers `''` to a list containing
`opus` even when it then plays it, while on Chromium the same question answers
"probably" — and that is why the defect showed **only on a phone**.

The declaration is now a **suspicion**, and what decides is the playback — one
looks at whether the player really decoded any audio: if it did, the suspicion
falls for good; if it did not and there was a suspicion, the declaration was
telling the truth and the line is useful.

Two details not visible from reading: `timeupdate` is listened to and not
`playing`, because at the first instant the decoded-bytes counter is still zero
on every browser and looking there would put the warning back by another road;
and the decoded-audio probes are **assertions**, not negations — `undefined`
does not mean zero, so it is enough that one of the three answers yes.

**And the clip that is playing must have a sound track**, otherwise "no audio
decoded" proves nothing about the browser. A clip recorded while the microphone
was absent is video only — `record/clip.go` adds the audio only if there is any
— and from inside the page that is identical to a player that cannot decode: on
iOS, where the suspicion is born lit, the first mute clip raised the accusation,
and stickily at that. The field is read by the monitor **from the file**
(`media.MP4HasAudio`, from the same file already opened for the duration), which
is the only place that knows: a separate list would diverge, and that is why
there is no state file beside the clips.

### A property of the stream is not stated when there is no stream

With no microphone at all the viewer announced, at the top of the page, "audio
filtered by the system: raw mode is not active" — a sentence about a stream that
did not exist, which sends whoever reads it hunting through Windows for a filter
to switch off. It is the prediction of the chapter above in a third guise, and
the cause is one field answering a question it was never asked.

**`rawAudio` says what the last open obtained, and the page read it as a
property of now.** It is written in one place, inside the capture callback, and
is deliberately not cleared when the capture stops — the road obtained is the
road that will be obtained again, the same choice `Pipeline.Microphone` makes
about the device name. So a boolean with two values carries a question with
three: `false` means both "raw was refused" and "nothing has ever been opened",
and `true` outlives the microphone being unplugged. **Both directions showed**:
"filtered" over an absence, and "unfiltered audio: yes" over a microphone that
had gone.

**The fix is not to clear the field.** What is missing is not a write, it is the
second question — `microphoneActive` — and the other two consumers were already
asking it: the guided path computes `micOk` before choosing between `mic-raw`
and `mic-filtered`, and the tray puts `mic-missing` in a `case` above
`mic-filtered` for the reason written beside it, that a colour can say one thing
and it has to say the worst. **A third reader forgot, which is what makes this a
rule and not a line.** The viewer now asks through `rawAudioState`, whose three
answers are `raw`, `filtered` and `unknown`, and the details row writes the
panel's dash for the third: the absence is stated once, by the microphone's box
and by the `mic-missing` alert.

**And nothing covered it, because the box's priority list is about somebody
else.** In `warningOrder` the source above `raw` is called `microphone` and is
the **viewer's** microphone, the one talk-back uses: the monitor's absent
microphone writes in the details box and in the alert bar, neither of which
competes for the warnings box. So `raw` was the only warning switched on and won
by default — a priority list protects nothing against a wrong entry, only
against two right ones at once.

**The guard reads the readers, not the line that was wrong.**
`TestNothingSaysWhetherTheAudioIsFilteredWithoutAskingIfThereIsAudio` takes
every script a page loads, finds each read of `rawAudio`, and demands that the
enclosing top-level function also read `microphoneActive`. It counts them too:
renaming the field would silence every match, and the test says so rather than
going green. **Its limit is written into it**: it looks in the function, not in
the expression, so a second unguarded read added to a function that asks
elsewhere would pass. It was checked by putting the defect back — the old line,
then the guard deleted from the helper, then the guided path's `micOk` opened up
— and it failed on each, naming the file.

**And it reaches the browser only, which a review found out the hard way.**
`rawAudio` is the JSON field, so the test walks the scripts and nothing else,
while the same claim is made in Go: `pat-capture` printed `[FAIL] audio in
WASAPI raw mode (OEM effects bypassed): false` on a machine where no microphone
had ever opened — the identical sentence over an absence, on the instrument
whose transcripts go into `baselines/`, where it reads as an OEM filter to go
and disable. It now states it only if the run captured samples.

**The sensor there is the run's own measurement and not `AudioActive`**, and
this is the part that cannot be read off the diff: by the time `pat-capture`
reports, `Pipeline.Run` has returned, and its deferred `audioOK.Store(false)`
has already put the flag down. Gating that line on "is it capturing?" would
answer no after a perfect run — deleting the check rather than guarding it, and
greenly. `total.Samples` is a fact about the run and cannot go stale.

**No `go/ast` guard is added for the Go readers, and that is argued.** The two
that are right do not ask in the words a test could look for: the tray asks
through `micMissing(s)`, `pat-capture` through its sample count. A test
demanding `MicrophoneActive` or `AudioActive` in the caller would accuse both,
and a guard that has to be taught its own exceptions is protecting the list of
exceptions rather than the rule — which is the second failure the guards chapter
lists. It is written down instead, here and in `RawAudioMode`'s doc comment.

**The function boundaries are found by column zero and not by counting braces**,
which is the same convention `topLevelDeclaration` rests on in the same package:
a brace in these files can sit inside a string, a template or a regular
expression, and all three are there.
