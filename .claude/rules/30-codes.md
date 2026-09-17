---
paths:
  - "internal/i18n/**"
  - "internal/alerts/**"
  - "internal/tray/**"
  - "internal/server/**"
---

Part of PAT Monitor's engineering record — chapters of
**Invariants not to break**; the index that carries every chapter, in order,
is in `CLAUDE.md`.

### Codes cross the API, words stay at the edges

**Whoever has to decide something compares a code, and the words are chosen by
whoever draws the page** — who is also the only one who knows what language they
are speaking in. A sentinel value in a natural language is a decision taken in
that language, and it survives only until someone translates.

The rule cost the product's worst fault, narrowly missed. Microphone health
travelled in JSON as a **sentence**, and three places compared it: `trayStatus`,
`app.js`, `onboarding.js`. **Translating the interface would have switched those
three comparisons off with no error anywhere**, and what was switching off is
the alert for a microphone delivering zeros: green page, still meter, child
crying and nobody hearing.

Now `MicHealth.Code()` produces `ok`, `digital-silence`, `quiet`, and the device
name sits beside `MicrophoneActive` instead of being replaced by the word
"absent". Covered by `traystatus_test.go`, and **verified to catch**: putting
the comparison back on the sentence, the test fails declaring the symptom.

**The rule holds for text believed to be diagnostic only.** Three decisions were
taken on a sentence, none of which gave a compile error: `pipeline.go` decided
whether to warn by comparing `CodecNotes()` against a phrase — the question is
now put to `CodecSettingsAccepted()`, which **counts** the notes instead of
reading them; `viewerlog.go` distinguished "ICE has not chosen yet" by comparing
against a phrase, and now absence is the empty string; `version.go` recognised
the unknown commit by comparing against the word `build.ps1` writes. **A report
sentence is a sentence, and someone will end up interrogating it.**

**Which strings stay in the user's language is decided by who reads them**: the
too-short password and the Funnel without a password, plus the sixteen
`internal/rtc` errors that `err.Error()` sent verbatim to the viewer's status
dot. Those became a **code** (`signalMessage.Reason`), the detail stayed in the
log, and the sentence is chosen by the page: taking internal text out of the
interface is a gain regardless of language, because an `err.Error()` on screen
carries file paths and function names with it. `tunnel.State.Action`, `Warning`
and `Detail` stay prose, and that is a **choice**: nobody compares them, and
part of that text comes from Tailscale.

**A string type protects against nothing.** `type errCode string` looks like a
net and is not: an untyped constant converts itself, so `authError(…, "a whole
sentence")` compiles without a word and the page shows the generic message
instead of the right one. The compiler cannot be the net, and the net is looking
at the source: `TestNoProseTravelsAsACode` refuses any literal in the argument
carrying the code.

**And a field that changes shape has more consumers than anyone remembers.**
Replacing `Message: err.Error()` with `Reason` updated **one of three** readers:
the onboarding said "preview failed: monitor error" for any cause, and
`pat-viewer` printed "error from the server: " with nothing after it. Neither is
a functional fault, and **that is why they survived a reread and a review**:
they show up only when the diagnosis is needed. The question at every change of
shape is not "who writes it", it is **"who reads it"**. `pat-viewer` stays out
of `TestBothPagesKnowEverySignallingReason` deliberately: it prints codes bare,
because it is a tool and not an interface.

#### The tunnel phases are codes, and the pages hold the dictionary

The eight values of `tunnel.Phase` are compared in nine places across `app.js`
and `onboarding.js` to decide what to show. They are `off`, `starting`,
`needs-login`, `needs-funnel`, `needs-approval`, `certificate`, `running`,
`error`, and the word is chosen by the page from a dictionary that sits next to
where it is shown. `tray.Phase` carries the same names: **two lists of the same
idea written differently are how the rule gets forgotten.**

**The real defect is that the two halves can diverge with nothing saying so.**
Hence `phases_test.go`: every phase named by at least one page, every phase
except `off` with a word in the dictionary, no old sentence surviving as a
comparison. The third test must **remove the dictionary before searching**,
because those phrases belong there by right.

#### The catalogue: English is the base, the others overlay it

One file per language in `internal/i18n/catalogs/<tag>.json`. Four invariants,
and nothing complains if one breaks:

- **English is the fallback and the others overlay it**, key by key: an
  incomplete catalogue degrades into English instead of showing the bare key;
- **the value of the `lang` cookie becomes a file path**, so it is validated
  against the list of embedded catalogues before use;
- in `data-i18n-html` values may contain only `strong`, `em`, `b`, `code`, `br`,
  **with no attributes**: it is text arriving from a file and ending in
  `innerHTML`;
- composite keys are expanded from the Go lists that generate them, not from a
  second hand-written list.

The shape of the keys was chosen **looking at all of them together**: splitting
on a dozen strings, when the real catalogue will have some hundreds, means
redoing it at first contact with the others.

**There are two readers, and they do not speak the same language.** The pages
are read by whoever is watching, from a phone that may be in another house, and
their declaration is `Accept-Language`; the tray is read by whoever is in front
of the machine, and theirs is `GetUserPreferredUILanguages`. That is **the
language is chosen by who reads** applied to two readings inside one process,
and it is why the catalogues live in a package of their own rather than inside
the server. From Windows one asks for the **list** and not
`GetUserDefaultUILanguage`, because that system keeps an order of preference
like a browser; the buffer that answers is a MULTI_SZ, and **`UTF16ToString` is
wrong there** — it stops at the first NUL, so it would return the first language
only, with the worst possible symptom, none.

**Codes cross the API even when the API is an HTTP error.** The JSON field is
still called `error` but carries a code, and `TErr` picks the sentence — the
fallback on `err.unknown` covers the real case of an updated monitor with a
dictionary still in cache. The same holds for the no-JavaScript fallback, which
carries the code in the address rather than the sentence.

**A duration is not written inside the sentence.** "retry in 1m30s" composed in
the server is in the server's language: an integer number of seconds travels,
and the sentence is composed by whoever knows the grammar needed. **And a
fragment of a sentence is not a code**: `ActionFailed` says remote access did
not open, `FailedStep` says which step — two codes, not a concatenation that
composes in one language and no other. The secondary gain is that the step,
being a code, is English by construction.

**The only prose that remains is Tailscale's**, and it is not touched: their
control server composes it knowing the tailnet and the reader's role — to an
administrator it gives a link that enables everything in one click, to a member
it says who to ask — and rewriting it would mean guessing. It travels in a field
of its own (`ActionText`) and takes precedence over our code, which is the
fallback for when the service does not answer.

**The program does not say "I".** Half the error family was impersonal already
and **all** of English was, so the Italian overlay had invented a persona the
base does not have: `Non sono riuscito a salvare` beside `I microfoni non si
sono potuti elencare`, one family in two voices. It is now `Non è stato
possibile …` throughout, and `onb.s7.title` says what `tunnel.action.failed`
already said — access from outside did not open — instead of `Non riesco ad
aprire`. **The "we" of whoever wrote the program stays**, and it is a different
thing: `né Tailscale né noi possiamo vederlo` is an assertion only the authors
can make. What went with the "I" is a clause that promised a support nobody
gives — `mentre sistemiamo questo`, when nobody is sorting anything out.

**One apostrophe, and it is `’`.** The catalogues held both marks — forty
Italian values with the straight one against nineteen with the typographic —
and nobody had chosen that: it is whichever keyboard was in front of whoever
added the entry. **The mixture was not only ugly: it was hiding two spelling
mistakes.** Two entries wrote `e'` and `puo'` for *è* and *può*, a typewriter
standing in for an accent, and they were the two added through a tool that could
not reach the accented keys — in a file that already spells one thing two ways,
a third way looks like the second. `po'` is not one of them and stays: it is an
apocope of *poco*, and it really does take an apostrophe.
`TestTheInterfaceHasOneApostrophe` reads the embedded catalogues rather than a
list of languages, so one added tomorrow is covered with nothing to remember.

**And where the two languages disagree on a tense, one of them is wrong — it is
the future one.** It found three: `viewer.mic.insecure` promised in English that
the browser *will not* grant the microphone while the Italian said it *does
not*; the same for whether the page moves on, and for playing the audio. Those
are standing properties, not events, and the guides all say the same thing —
present tense unless it is genuinely later in time. **The check is worth more
than the rule**, because it needs no judgement: it is two translations of one
fact, and a fact does not change tense in translation. What stays future is what
both languages agree is future — the device that will appear in the Tailscale
list, the bookmark one will not have to look for again.

**And nothing compares a term against itself inside one language.** Italian said
*da fuori casa* in fifteen places and *da fuori* in eleven — not one outlier but
two terms, and the short one is the worse of the two: *outside of what?* It is
now *da fuori casa* throughout. **English had the same drift and smaller**,
which is exactly what makes a base look clean: twenty-six values said *from
outside* and two *from outside your home*.

**What settled that it was drift and not variation is where the two sat.** Each
was one line from a short one: the fork's title above its own lead, and a box's
label above the alt text of the code inside it — the same screen saying it both
ways. Read on their own, both had a case for being explicit.

**And English converged the other way, on the short form, which is not an
inconsistency.** The majority is twenty-six to two, and the pair *At home … from
outside* runs through the whole interface anchoring it —
`tray.where.home-and-away` is literally that pair — while the long form makes
*Access from outside your home did not open: {step}* out of a line that has to
fit a tooltip. Italian has
no such pair to lean on, and *fuori* on its own is a bare adverb. **The term is
chosen per language for the same reason the language is: by whoever reads it.**

**The first version of this paragraph asserted that English was uniform, and had
counted Italian.** Twenty-six — the Italian total — carried across to a language
nobody had measured, and it stood because it was plausible. It is the same
defect as a constant written beside a measurement that contradicts it, one step
earlier: **a number belongs to the thing it was measured on, and moving it to
another claim is not an inference.** What found it was re-running the count
while reviewing, not rereading the sentence.

**The two that could not take the words as they stand are the interesting
ones**: where the sentence already carries *casa*, repeating it reads worse than
the ambiguity — *In casa e da fuori casa* says one word twice in four — and both
were resolved by moving a word rather than by keeping the short form.

**And the same drift twice over one word doing two jobs.** *The device you watch
from* is a claim about **scope** — it tells the reader whether the sentence
concerns them — while *the phone* names a **gesture**, and scanning a code or
saving something to a home screen really is done with a phone. Three of the
scope claims had been narrowed to the phone: nothing to install *on the phone
you watch from*, *the phone* calling the meeting point, and the password asked
for by *every phone or computer*. All three understate what they assert, and the
second gives the game away — the paragraph said *phone* while the aria-label of
the drawing beside it, in the same box, already said *device*. The rule is
therefore not one term but **which of the two jobs the sentence is doing**, and
the gestures keep the phone.

**What stays an enumeration stays one, and that is a decision.** The fork says
*phone, tablet or computer* and so does the arrival, next to a line that says
*any device*: at those two moments the reader is asking "will it work on mine?",
and a list answers that better than a category. **A general term is the right
answer to "does this concern me?" and the wrong one to "does it work on the
thing I own?"**

**Lengthening a term is a layout change**, and in an illustration nothing clips
it: the fork's two captions were measured against their cards in both languages
— 309-477 inside 276-510 — because that is the lesson the tray address already
taught. And the probe declared itself first: it searched forward from an anchor
that sits **inside** the figure, so it measured the next one and reported
somebody else's words. **A tool that names what it measured is a tool that can
be caught.**

**Every prefix has its guard where its reader is.** The page keys are checked by
`internal/server` looking at the markup; the `tray.` ones by `internal/tray`,
which is the only place that can **expand the codes** — `tray.fault.` plus a
`Fault` value — and those are exactly where an entry gets forgotten: the new
code is added in Go, the catalogue lags, and the key appears in the menu. The
127-character tooltip ceiling is tested from there too, **across every
language**.

### Alerts are the set of what is wrong now, not a list of events

`internal/alerts`. The state carries what **is wrong now**, not the history: a
list of transitions grows, has to be browsed, pruned and remembered by both
sides, while the watcher has one question — "is anything wrong?" — and whoever
reloads the page remembers nothing.

- **What makes the set possible is the stable identifier.** An alert keeps its
  id for as long as it lasts, so a new id is a new alert and a vanished id is a
  recovery: the page computes both by comparing with the previous round, and the
  server keeps nothing. Without that stability a fault lasting all night would
  chime every three seconds; without a new id on return, a fault that comes and
  goes would announce itself once.
- **`Update` takes the whole photograph**, not an on/off pair. The caller
  recomputes every fault each round, which makes impossible the class of defect
  where an alert stays lit because a branch forgot to clear it. The only
  exception is expiring alerts — the test one — which no observation can
  contradict.
- **Fault predicates live in one place** and are read by two, the tray for
  colour and the alerts for the banner: written twice they diverge at the first
  touch, and then the icon says one thing and the page another about the same
  monitor.
- **Transitions also go to the file.** The banner is seen by whoever has the
  page open; whoever comes back in the morning has only the log, and that is
  where the time the camera stopped has to be.
- **A fault names a state, never a transition.** An alert appearing is not
  proof that anything changed: on a machine with no microphone, or whose
  webcam another process is holding, the very first round after the grace
  raises it, and there was no before. "The microphone is gone" and "the camera
  has stopped sending images" therefore both said something false in exactly
  the case they existed for, and sent whoever read them looking for what had
  unplugged a thing that was never plugged in. They are now "The microphone is
  missing" and "No images from the camera", which are true either way. The
  recoveries follow: "back" asserts the same thing in reverse, so they state
  the good condition instead. **Events are the exception and need no rule** —
  crying, barking and motion are transitions by nature.
- **The words are the ones whoever reads them uses.** It is the guided path's
  rule — names the user recognises — and it holds for the banner and the tray,
  which are read by a parent and not by whoever wrote the program: "no stream:
  capture will not start" is two pieces of video jargon in five words. The
  details panel is the exception and keeps its own, because it is a diagnostic
  surface and its reader has opened it on purpose.
- **These two have no guard, and that is a decision.** Both would need a list
  of forbidden words, which is the guard that absolves everything it has never
  met; and neither has a mechanical form, because whether a sentence is true at
  the first round is not a property of the string. What protects them is that
  the same condition is written twice — once for the tray, once for the banner
  — so writing the second is the moment to read the first.

**Nothing is announced in the first thirty seconds.** The log announced
"microphone absent" at instant zero and the recovery a second later, while
WASAPI was opening the device. The grace covers **events** too, and there the
reason is stronger: **whoever starts the monitor is in front of the machine**,
that is, in front of the camera, and moving — a motion alert would not be rare,
it would be guaranteed. It lives in `presentAlerts`, and simulated faults are
outside it deliberately.

They are tested by **simulating**, because a fault does not happen on command:
`-simulate-fault` alternates the given codes in twenty-second windows, and
**alternates deliberately** — the case one gets wrong is not the alert that
appears, it is the one that does not go away. A misspelled code stops start-up
instead of producing a banner with no words: that is the family of the GUID that
does not complain. **Events** are tested live, by moving in front of the camera
or getting someone to bark.

