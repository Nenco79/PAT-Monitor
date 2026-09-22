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

**And the rule is about the program, not about one family.** The error family
was fixed first; the **eight status messages** had kept the persona — `Riprovo`,
`Sto aspettando`, `Sto preparando il collegamento sicuro`, `Controllo che
risponda` — and there the remedy is a different shape, because the sentence is:
a failure denies (`Non è stato possibile …`) while a state names itself —
`Nuovo tentativo`, `In attesa`, `Preparazione del collegamento sicuro`,
`Verifica della raggiungibilità da fuori casa`. English and German were
impersonal in all eight (`Trying again`, `Waiting`, `Preparing the secure
connection`, `Checking that it answers`), so the persona was one language
against two.

**And the plural of the guide stays, because the base has it**: `Proviamo se si
vede e si sente` is English's `Let's see whether it sees and hears` and German's
`Sehen wir, ob …`, which is not the "we" of the authors but the reader and the
page walking together. `Ho capito`, on the button that closes the explainer, is
the reader's own voice for the same reason.

**No guard is added, and it is the argument the alerts chapter already makes**:
what would catch a persona is a list of first-person conjugations, and that is
the guard that absolves every sentence built with a verb it has never met — a
passive and a nominal form are not distinguishable from an impersonal one by
any parser. What found the eight was translating against English, which is an
act and not a check.

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

**The tray's status block is sentence case, and getting there is the point.**
The lines in the notification area's panel open with a capital — `Telecamera:
nessuna immagine`, `Microfono: permesso disattivato`, `Spettatori: 1 ·
dispositivi registrati: 2`, `Attivo da 2h14m` — and so does everything else the
tray says: `tray.note.`, `tray.verdict.`, `tray.where.`.

**They did not, and nobody had decided that either.** They were a **readout** —
`thing: state`, the shape of a gauge — opening lower case, in forty entries
across five languages, following a convention **nobody had written down**. It is
defensible on its own: the same fact is a sentence in the page's banner,
`Nessuna immagine dalla telecamera`, and a reading under the pointer. What is
not defensible is that it had never been chosen.

**German is the control, and it reads the same way in both directions.** Under
the old rule its eight fault lines were the only capitalised ones — correctly,
because every one of them opens on a noun — while its one line beginning on an
adverb, `aktiv seit`, was lower case like everyone else's. Under the new one
German needed **one change of ten**, and the other four languages needed ten of
ten: the language that looked like the exception turns out to have been there
already.

**And the change made the rule guardable, which the old one could not be.** *No
forced capital* depends on whether that word in that language takes one, which
is not decidable from a string — a check on the first letter would have accused
eight correct German entries, and one carrying a list of languages that
capitalise nouns is the guard that absolves everything it has never met. Sentence
case depends on **position** and nothing else, so
`TestEveryStatusLineOpensWithACapital` holds it in every language.
**A convention became checkable by becoming stricter**, which is the reverse of
how these usually go, and it is the reason it was worth doing rather than
merely writing the old one down.

**`tray.viewers.` stays lower case, and it is the boundary somebody will
otherwise "fix" next.** Those three are **fragments**, not lines: `summary`
joins one onto the end of a `tray.where.`, so a capital there would put
`· 1 Spettatore` in the middle of a sentence. The guard skips that prefix by
name, with this argument beside it.

**And it settled a thing that had been left open.** The tooltip shows either a
`tray.note.` — a sentence, with a full stop — or, when there is none, a
`tray.fault.`; under the same pointer, in the same pixel. They used to disagree
about their first letter, which is the one thing a reader notices there. They no
longer do. **What still differs is the full stop**, and that is right: a
sentence ends and a reading does not.

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
it: the fork's two captions were measured against their cards in every language
— 309-477 inside 276-510 in Italian, 302-484 in German, which is 26 px of room —
because that is the lesson the tray address already taught. And the probe declared itself first: it searched forward from an anchor
that sits **inside** the figure, so it measured the next one and reported
somebody else's words. **A tool that names what it measured is a tool that can
be caught.**

##### German: the decisions, because nobody here can check the sentences

The third language arrived into a mechanism that needed nothing: adding a
language is adding a file, the guards all read the catalogues from the directory
rather than from a list, and `de.json` passed every one of them on the first run.
**What it did need is decisions, and they are written here because the author
does not read German** — twenty decisions somebody can check are worth more than
411 strings nobody will.

- **`du`, not `Sie`**, and it is not a choice about tone: it is grammar, it
  changes every sentence that asks the reader anything, and it was **already
  made** — Italian gives the *tu* throughout, English does not distinguish and so
  says nothing. `du` is also shorter, which the tooltip notices.
- **`zu Hause` / `von außerhalb`, the short form**, for English's reason and not
  Italian's: German has the anchoring pair, so the bare term is not the bare
  adverb *fuori* was. Twenty-five of the thirty occurrences are `von außerhalb`;
  the five that are not are three sentence openings and **the two captions of one
  drawing**, where the pair is `der Monitor, zu Hause` against `du, außerhalb` and
  repeating the preposition is what would read wrong.
- **`der Monitor` stays, ambiguity and all.** In German *Monitor* is first of all
  a screen. It is kept because it is the product's name shortened, and because
  Italian carries exactly the same ambiguity and has carried it fine.
- **`Kamera` on the guided path, `Webcam` in the statistics**, which is the split
  English already has and not an inconsistency. Likewise `WLAN` and never *wifi*,
  because that is the word the reader has.
- **`Handy` names a gesture, `Gerät` names scope** — the same distinction this
  chapter already draws for *phone* against *device*, applied to a language that
  has a colloquial word for one and not the other.
- **The certificate is not named**, and the guided path's own rule says why: it
  says *secure connection*, not *TLS certificate*. Where the sentence needs the
  party that provides it, it names the party — `Sie kommt von einer externen
  Stelle` — and not the thing, which is also what keeps `Zertifikat` out of a
  page read by a parent.
- **The program does not say `ich`**, and German has the passive for it: `Es wird
  geprüft, ob …`, `Speichern war nicht möglich`. There is no `ich` in the
  catalogue and the one `Sie ` in it is the pronoun *sie*, capitalised at the
  start of a sentence, with a third-person verb after it.
- **`ß` where the spelling wants it, and no apostrophes at all**, which costs
  nothing: the guard that forbids the straight mark had nothing to say.

**The trap German has and the other two do not is the dangling pronoun**, and it
is the one thing here that found real defects. German pronouns agree in gender,
so *er* attaches to the nearest masculine noun rather than to the one meant:
`der Balken, ob er hört` reads as the bar listening. Five entries were written
that way from the English and all five were remade — the impersonal
`ob etwas zu hören ist`, or the subject named outright. **It is the same defect
as a doc comment on the wrong declaration**, in a language where the mistake is
grammatical rather than typographic, and what finds it is reading the sentence
rather than checking the key.

**The menu accelerators are per language and German needed its own eight.** They
are `E L V Z T B N U`, and the one that had to move is *Zu tun*, whose obvious `Z`
belongs to *zurücksetzen*: it carries `Z&u`. The guard catches this, and it
catches it because it reads the catalogue.

**What it cost in layout is one number.** The tooltip's worst case is 105
characters of the 127 against English's 100; the tray panel drawing has 21 px of
room, which is Italian's figure to the pixel; the pages add no overflow English
does not already have, at 360, 390, 768 and 1400. The one thing that moved is the
viewer bar's single-row threshold, from 980 to 1010, and that is its own chapter.

##### French: the decisions, and two guards that had been written by hand

The fourth language arrived the way the third did — a file, and every guard green
on the first run — and what it found was two lists somebody had written by hand.

- **`tu`, not `vous`**, which is not a new decision: Italian gives the *tu* and
  German followed it, so a French saying *vous* would have been the odd one out
  of four.
- **Nothing agrees with the reader.** French inflects participles and adjectives,
  and `tu es prêt` presumes the reader's gender: the sentences are turned instead
  — `Ça me va`, `Compris`, `Tout est prêt`. It is the German trap in a language
  where the mistake is agreement rather than a pronoun.
- **`le moniteur`, ambiguity and all**, for the German's reason: it is the
  product's name shortened.
- **`à la maison` / `de l'extérieur`, the short form**, for English's reason and
  not Italian's: French has the anchoring pair.
- **The certificate is not named** — `Il est fourni par un organisme extérieur` —
  and the German decision was reached again from the other side, because the
  guided path says *secure connection*.
- **The program does not say `je`**: `Nouvelle tentative`, `En attente`,
  `Préparation du lien sécurisé`, `Vérification de l'accessibilité depuis
  l'extérieur`.
- **`accessibilité` for the outside check**, so that `viewer.remote.reachable`
  says `Accessible depuis n'importe quel réseau` in the same family of words.
- **`Mo` and not `MB`**: the one place where a unit in a catalogue is a
  translation and not a copy.
- **Eight accelerators, distinct**: `j` journaux, `q` Quitter, `r` Réinitialiser,
  `e` Déconnecter, `g` guidée, `f` faire, `n` Nouvelle, `v` vidéos.
- **`Muet` for the mute command**, the word Windows uses and the shorter of the
  two candidates: one label's length in the bar is a number, and `Silence` is two
  characters more.
- **The space before `:` `;` `?` `!` is written non-breaking** — `\u00a0` in the
  file rather than the character, so that whoever reads a diff can see it, since
  on screen the two spaces are the same width. French asks for it, and **57
  values had an ordinary one there**: a breakable space is worse than no space,
  because the line ends between the word and the mark and the mark begins the
  next line. **No other catalogue of the four has one** — German, Italian and
  English write no space at all before those marks.
- **The labels are infinitives, and the prose is not.** The French state's design
  system says it in one line — *commencer les textes des boutons avec un verbe
  d'action à l'infinitif* — with *ne pas utiliser « Je »* beside it, which is our
  own rule about the program not saying "I", reached from the other side: `Se
  déconnecter` is that infinitive and not a person. The *tu* of the sentences
  around them is another thing, and not a convention anybody prescribes: it is
  the voice, decided once with Italian's *tu* and German's *du*.

**And the two guards that had been written by hand went wrong the same way.**
`TestTheChosenLanguageBeatsTheBrowser` used `fr` as its example of *a language we
do not have*, which stopped being true the day French arrived — and the case kept
its other assertion and lost the one it exists for.
`TestTheDictionaryCarriesTheLanguageNames` named English and Italian, so German
had already arrived outside it. Both now derive their list from the thing itself:
the unknown tag from the candidates, the names from the catalogues.

**The typography has a guard, and the persona rule deliberately has none, and
they are not the same kind of claim.** `TestASpaceBeforePunctuationIsNotBreakable`
walks every value of every catalogue from the embedded files — one walk, shared
with the apostrophe guard, because the count that says the walk saw something is
the part two copies would diverge on — and fails on any space a line could break
before those four marks. It was shown to fail, naming the file, the key and the
value, with one value put back the way it had been. What a sentence conjugates is
a matter of reading; a space is a byte. **What it cannot see is a French `:`
written with no space at all**, and that is argued rather than overlooked: a URL
carries one, a clock carries one, and a rule that excepted them would be a rule
made of its exceptions.

**What it cost in layout is nothing.** The tooltip's worst case is **111**
characters of the 127 — French is the widest of the four, against German's 104,
English's 100 and Italian's 96 — and the bar's single-row boundary, measured with
a probe that lowers the media query so the widths below the threshold can be
swept, is **≈959 px**: below Italian's 971 and German's 1001, so **1010 held then —
Spanish has since taken it to 1020**, and this measurement is one of the ones
holding the number up. The instrument declared itself
first, which is what the rule asks of it: it reproduces the German record — 39.4
px of shift at 920 against the 40.6 written down, and 9.3 at 980 against 10.6.

**Every prefix has its guard where its reader is.** The page keys are checked by
`internal/server` looking at the markup; the `tray.` ones by `internal/tray`,
which is the only place that can **expand the codes** — `tray.fault.` plus a
`Fault` value — and those are exactly where an entry gets forgotten: the new
code is added in Go, the catalogue lags, and the key appears in the menu. The
127-character tooltip ceiling is tested from there too, **across every
language**.

##### Spanish: the decisions, and the bar's threshold moving

The fifth language arrived the way the fourth did, and this time every guard was
green on the first run **for a reason that can be checked**: the two lists that
had broken under French were the ones now derived, and Spanish was the first
language to walk through them with nothing to fix.

- **`tú`, not `usted`**, the decision the other three had already made, and for
  a home product the right one: `usted` is how an administration addresses a
  citizen, and this program speaks to whoever lives in the house.
- **Nothing agrees with the reader**, which is the French trap in a language
  where agreement is in every adjective. `Welcome` is not `Bienvenido`, which
  presumes the reader's gender down to its last vowel: the first screen's eyebrow
  is `Para empezar`. `Got it` is `Entendido`, which agrees with the thing
  understood and with nobody in particular.
- **`el monitor`**, ambiguity and all, for the German's and the French's reason.
- **`en casa` / `desde fuera`**, the short pair, as in French and not as in
  Italian.
- **The certificate is not named** — `Lo emite una entidad externa` — reached
  from the third side, because the guided path says *secure connection* and not
  *TLS certificate*.
- **The program does not say `yo`**, and Spanish is where that breaks most
  easily: `Nuevo intento`, `Comprobando si responde desde fuera`, `Es mejor
  pagarla ahora`.
- **The opening marks are there**: `¿Eliminar esta grabación?` and not `Eliminar
  esta grabación?`. Spanish is the only one of the five that marks where a
  question begins, and it is the one thing a Spanish reader notices at once when
  it is missing — the mirror of the French non-breaking space, and for the same
  reason: a rule that is invisible to whoever does not read the language. **It
  has no guard, and that is argued**: telling which `?` opens a question means
  reading sentences, and a check built on the sentence would either accuse an
  address or absolve the string that needs it.
- **No space before `:` `;` `?` `!`**, which is the same RAE rule the French
  section had to fight from the other side: *«se escribe pegado a la palabra o el
  signo antecedente»* (DPD, *dos puntos*) — the exact opposite of the French
  convention, so the typographic guard passes here by finding no space at all.
- **`MB` and not `Mo`**: the unit is a copy from English and Italian, and French
  is the only catalogue where it is a translation.
- **`router` and not `enrutador`**: the word whoever owns the house uses, which
  is the reason the German section gives for its own choices — the alternative
  is the one in the dictionary and the one nobody has in the living room.
- **Eight accelerators, distinct**: `r` registros, `s` Salir, `c` contraseña,
  `d` Desconectar, `g` guiada, `p` Pendientes, `n` Nueva, `v` vídeos.

**And the bar's number moved, which had not happened since German.** The
boundary — the width below which the middle group **moves when "Talk"
appears** — comes out at **1015 in Spanish** against 1005 in German, 975 in
Italian and 970 in French, so the widest is Spanish's and **the threshold went
from 1010 to 1020**, the round number above it, which is the rounding German's
1001 had already got. **The instrument was checked against the record before
anything moved**: it reproduced 1005 against the 1001 written down and 975
against Italian's 971, and with them the samples underneath — 39.4 px of shift
in German at 925 against the 40.6 at 920.

**What moved it is one word, and the language's own word was kept.** `Silenciar`
is nine letters where French has `Muet` and German `Stumm`: measured, `Mudo` in
its place takes the boundary to 985, which would have kept 1010. It was not
taken, because `Silenciar` is what the Spanish Windows says — the same reason
`Muet` *was* taken in French, where the platform's word is the short one. **And
the label that looks guilty is not**: `Cerrar sesión` is the widest in the bar,
and shortening it to `Salir` moves the boundary not at all, because the number is
about the left side, where "Talk" appears, while the log-out sits on the right —
besides being a word this program already uses for switching the monitor off.
What the number *means*, and why it is where the group stops moving rather than
where the buttons fit, is in `150-pages.md` and nowhere else; what belongs here
is the fact that **a language moved it, and not a label somebody edited** — the
rule that chapter states, met by a language instead of by a designer. **And this
instrument sees one side**: Spanish is the first language whose right-hand labels
are the wider pair, and what that costs is neither described by this number nor
measured by anybody.

##### Chinese: the decisions, and the first script with no spaces in it

The sixth language is the first that is not written in the Latin alphabet, and
it is where the four rules the others share stop being about wording and start
being about bytes. Every guard was green on the first run, and **two of them were
shown to see it before that was believed**: a duplicate accelerator and a removed
key both fail naming `zh`.

- **Simplified only, and the file is `zh.json`.** Traditional is a declared
  limitation and not an oversight: `i18n.pick` tries the whole tag and then the
  part before the first hyphen, so `zh-TW` and `zh-Hant-TW` both land on `zh`,
  and a `zh-hant.json` beside it would be a file **no browser can reach**. The
  day somebody wants it, what has to change is `pick`, not the folder.
- **`你`, never `您`, and the platform decided it rather than a style guide.**
  Microsoft's own Simplified Chinese guide says to use the polite form in all
  software; Windows 11's Chinese interface does not — *允许应用访问你的相机* is
  the sentence on the very privacy page this program sends people to. The panel
  sits inside that interface, and the other four catalogues have all chosen the
  familiar form already, so the guide is the older evidence and the product is
  the newer.
- **`监护器` for *the monitor*, and the obvious words were refused.** `监视器`
  and `监控器` both name a **screen** first in Chinese, which is the one thing
  this is not — the trap German, French and Spanish all decided to live with, and
  the only one of the six where the language offers a way out. `监护器` is the
  word `婴儿监护器` (baby monitor) is built from: it names watching over
  somebody, not a display.
- **`摄像头` for the camera, and `相机` only where Windows says `相机`.** It is
  Italian's split, arrived at from the other side: the prose calls the device
  what a Chinese reader calls it, and where a sentence names the Settings page
  the reader is about to open, it uses the page's own word. Likewise `麦克风`,
  which is the same in both.
- **`外网访问` for access from outside**, the term Chinese routers and NAS boxes
  already use, for the reason the German section gives about `router`: the
  alternative is the one in the dictionary and the one nobody has at home.
- **`对讲` for talk-back**, which is what every Chinese camera app calls two-way
  audio, and shorter than anything a translation would have built.
- **Punctuation is full width — `。，、：；！？` — and parentheses are not.**
  The bracket is the one place two forms would collide, because the accelerator
  is `(&W)` and must be half width; a catalogue carrying `（至少 8 个字符）` in
  one entry and `(&W)` in another is the two-apostrophe defect in a script where
  it is more visible, not less. One bracket, half width, with a space against
  Latin and none against Han.
- **The ellipsis is six dots, `……`, and the menu's is one.** They look alike and
  they are not the same sign: `……` is Chinese punctuation, while the `…` on
  *重置密码(&R)…* is Windows' notation for *a dialog follows*, which every
  language's menu carries identically. The rule is therefore about what the mark
  is doing, and the two places never meet.
- **No plural forms.** `Intl.PluralRules('zh')` selects `other` and nothing else,
  so the six plural objects carry that form alone — `TN` falls back to it, and
  the placeholder guard compares form by form, so the missing `one` is not an
  absence anything can complain about. The three **flat** counters are another
  matter and all three are written: `tray.viewers.*` and `tray.sessions.*` are
  chosen in Go by `n == 1`, not by a plural rule.
- **Ten accelerators, distinct, in the Windows Chinese form**: the Latin letter
  in half-width brackets at the end of the label — `打开 Windows 设置(&W)` — which
  is what Microsoft's own localisation does, since the characters are not on the
  keyboard. The guard reads the character after the first `&`, so the form passes
  unchanged, and `DT_HIDEPREFIX` eats the ampersand exactly as a Chinese menu
  draws it. They are `L Q R D W G A N V`, with `A` shared by the step's three.

**Three of the rules the other five live by cannot fail here, and one new one
took their place.** There is no apostrophe, no space before `: ; ? !` and no
letter case, so those three guards pass by finding nothing. What did fail is a
rule nobody had written: **the non-breaking space must be an escape in the
file**, and the Chinese catalogue arrived with two literal ones, because it was
written through a tool whose own argument is JSON and which decoded `\u00a0` on
the way in. Every reader parses the escape, so by the time a value has been read
the two forms are the same string — and in a diff they are the same width.
`TestTheNonBreakingSpaceIsWrittenAsAnEscape` is the one guard in that file that
looks at bytes, and it was shown to fail on `zh.json` and on `fr.json`, which is
the catalogue the convention was invented for.

**What CJK costs in the panel is nothing, and that is measured.** Chinese is the
**narrowest** of the six in every status line — `remote-no-ingress` is 212 px
against German's 447 in a row of 236 — and the worst tooltip is **42 characters
of the 127**, against French's 111. Two of Tailscale's six actions drop from two
rows to one. The one number that had to be bought back is
`tray.fault.remote-no-ingress`, whose first writing left a **single character
alone on the second row**: 孤字 is a real defect in Chinese typography and no
guard here can see it, since a line that wraps within its rows is a line that
passed. It was shortened until it fits one row, and what found it was the
photograph.

**And `DT_WORDBREAK` does break between Han characters**, which was the open
question: MSDN documents `DT_NOFULLWIDTHCHARBREAK` as *preventing* that break and
says it has no effect without `DT_WORDBREAK`, so the flag we already pass is the
one that allows it. Confirmed by looking — the panel wraps mid-sentence with no
space anywhere in the text. **This is the case the width half of the fit guard
was added for**, and it is worth saying that it stayed silent: a script with no
spaces is exactly where `DT_CALCRECT` would have returned a width past its box,
and it did not, because GDI really does break.

**The typeface was the other open question and it is answered from here.**
`lfMessageFont` on this Italian Windows is Segoe UI, which has no Han glyphs at
all; the panel draws the Chinese catalogue with no tofu in it, because the charset
travels with the face and GDI's font linking substitutes. **That is the whole of
what this machine can show** — the road works where the answer is wrong — and it
is the reason the face is asked for rather than chosen.

**The pages needed nothing, and that is a fact about two lines somebody already
wrote.** `--sans` ends in `sans-serif`, so a glyph none of the named families has
falls through to whatever the system calls its sans — and `i18n.js` sets
`documentElement.lang`, which is what tells the browser **which** Han to draw:
the same codepoints are drawn differently in Chinese and Japanese, and without
that attribute the choice is the browser's guess. Adding CJK families to the
stack was the obvious next step and is not taken, because nothing here can
measure which of them a Chinese machine has: **a font list written from a machine
that cannot render it is a preference expressed in ignorance**, and the generic
at the end already asks the system, which is this project's answer to that shape
of question everywhere else.

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

