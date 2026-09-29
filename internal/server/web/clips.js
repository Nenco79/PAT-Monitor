'use strict';

// The recordings page: a listing grouped by day, filters by kind, one player
// that carries Download and Delete for the clip it is playing, and a selection
// that downloads, keeps or deletes several at once.
//
// The words all come from `T`: there is not one in here. The kinds reuse the
// keys the viewer's bar already has — `viewer.bark` and its sisters, the word
// on the switch that turns that detection on — rather than a second dictionary
// of the same codes, which would diverge at the first touch.

// **The language is `i18n.js`'s, and it is declared there.** This page loads
// that script first, so `LANG` is already in scope: declaring a second one here
// is what killed this page — two top-level `const` of the same name in two
// classic scripts share one lexical scope, and the second file then fails to
// parse **in its entirety**. Not one line of it runs, and the page shows its
// title and nothing else.
const NUMBER = new Intl.NumberFormat(LANG, {maximumFractionDigits: 1});

// **The list is grouped by day, so a row says only the time.** Every row used
// to repeat the whole date, seconds included, and a night of clips read as a
// column of one date. The day heading is the browser's words, not ours:
// `RelativeTimeFormat` says "today" and "yesterday" in the page's language, and
// past those a weekday and a date do, with the year only when it is not this
// one. No key in the catalogues, so no language can be missing one.
const CLOCK = new Intl.DateTimeFormat(LANG, {timeStyle: 'short'});
const RELATIVE_DAY = new Intl.RelativeTimeFormat(LANG, {numeric: 'auto'});
const DAY = new Intl.DateTimeFormat(LANG, {weekday: 'long', day: 'numeric', month: 'long'});
// DATE is the date alone, for the two days the heading names in words: "today"
// says which day it is only to whoever knows what today is.
const DATE = new Intl.DateTimeFormat(LANG, {day: 'numeric', month: 'long'});
const DAY_AND_YEAR = new Intl.DateTimeFormat(LANG,
  {weekday: 'long', day: 'numeric', month: 'long', year: 'numeric'});

// midnight is the start of d's day on this device's clock, which is the day
// whoever watches lives in.
function midnight(d) {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

// dayName names the day d falls on, seen from today. The difference is rounded
// rather than divided: a day with the clocks changed lasts 23 or 25 hours.
function dayName(d, today) {
  const days = Math.round((midnight(d) - midnight(today)) / 86400000);
  if (days === 0 || days === -1) return RELATIVE_DAY.format(days, 'day') + ' · ' + DATE.format(d);
  return (d.getFullYear() === today.getFullYear() ? DAY : DAY_AND_YEAR).format(d);
}

// duration writes a time the way a player writes it: minutes and seconds, with
// no words. That way there is nothing to translate and nothing to get wrong.
//
// **A duration that is not there is written with a dash.** The field is missing
// when the file's header could not be read — a clip truncated by a power cut —
// and writing "0:00" would give the plausible, wrong number in place of the
// admission of not knowing.
function duration(ms) {
  if (!ms) return '—';
  const s = Math.round(ms / 1000);
  return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');
}

// **The code of a clip asked for by hand is not an alert**, so it has no entry
// among those: no `viewer.alert.manual` exists, and none must — that list is the
// list of codes the monitor can emit as an alert, and a thing that is not one
// would end up inside it.
//
// The string is the same as `record.CodeManual`, which is the only place it
// lives on the monitor's side: the two are watched by
// `TestThePageKnowsTheManualClipCode`, because two copies of a code diverge and
// here the divergence would be silent — the row would show "manual".
const MANUAL = 'manual';

// eventName is the banner's sentence for a code this page has no kind for:
// the one road a code from a newer monitor still gets a word by.
function eventName(code) {
  return TOr('viewer.alert.' + code, code);
}

// KINDS are the kinds a clip can be of, in the order the filters list them, and
// for each the short word and the glyph a row and a filter show.
//
// **Barking comes first because the monitor is mostly watching animals**, and
// the order is the one somebody coming home runs down: what the dog did, then
// what moved. The glyphs are the viewer bar's own — the same drawing on the
// switch that turns a detection on and on the clips it produced — and the
// manual clip takes the "Clip" button's dot.
const KINDS = [
  {code: 'bark', word: 'viewer.bark', glyph: 'bark'},
  {code: 'motion', word: 'viewer.motion', glyph: 'motion'},
  {code: 'cry', word: 'viewer.cry', glyph: 'cry'},
  {code: MANUAL, word: 'clips.kind.manual', glyph: 'manual'},
];

// kindOf gives a code's kind. **A code this page does not know is still a
// kind**: an updated monitor and a page in cache are two versions of one
// product, and such a clip is shown with its whole sentence and no glyph rather
// than dropped from the list.
function kindOf(code) {
  return KINDS.find((k) => k.code === code) || {code: code, word: null, glyph: null};
}

function kindName(code) {
  const k = kindOf(code);
  return k.word ? T(k.word) : eventName(code);
}

// **The browser declares what it can decode, and the question is asked in two
// pieces.** If it says it can read H.264 on its own but not H.264 with Opus,
// that is exactly Safari's case before iOS 17: the video shows and the audio
// does not. One question alone would not tell it apart from a browser that knows
// nothing about MP4.
function audioUnsupported() {
  const v = document.createElement('video');
  const withOpus = v.canPlayType('video/mp4; codecs="avc1.42E01F, opus"');
  const videoOnly = v.canPlayType('video/mp4; codecs="avc1.42E01F"');
  return withOpus === '' && videoOnly !== '';
}

// **A declaration is not evidence, and this one has been wrong in both
// directions.**
//
// `audioUnsupported` was the only thing that decided the warning, and on WebKit
// it says no to a list containing `opus` even when it then plays it: on an
// updated iPhone — where the audio is perfectly audible, iOS 17 and later play
// it — the page promised it would not be heard. Reported live, on Brave for iOS,
// with the audio perfectly audible. (On Chromium the same question answers
// "probably": the warning did not appear, and that is why the defect showed only
// on a phone.)
//
// Now the declaration is a **suspicion**, and the warning is made to appear by
// playback: whether the player really decoded any audio is watched.
// - if it decoded some, the suspicion falls **for ever** on this page;
// - if it decoded none and the suspicion was there, then the declaration was
//   telling the truth and the line is wanted, with its advice to download the
//   file.
//
// The comment on the `error` listener below said as much already and held only
// halfway: if playback really fails, that is not an opinion — and it holds the
// other way round too, if it succeeds.
let audioSuspect = false;

// audioDecoded says whether the player produced audio, by the two roads browsers
// actually offer: WebKit and Chromium count the decoded bytes, WebKit and
// Firefox expose the tracks. Neither is everywhere, so it is enough for one to
// say yes — they are affirmations, not negations: `undefined` does not mean
// zero.
function audioDecoded(v) {
  if (typeof v.webkitAudioDecodedByteCount === 'number' && v.webkitAudioDecodedByteCount > 0) {
    return true;
  }
  if (v.audioTracks && v.audioTracks.length > 0) return true;
  return v.mozHasAudio === true;
}

// **And the clip that is playing has to have a sound track**, otherwise "no
// audio decoded" proves nothing about the browser.
//
// A clip recorded while the microphone was absent is video only —
// `record/clip.go` adds the audio only if there is any — and from here that is
// identical to a player that cannot decode audio. Without this condition, on iOS
// (where the suspicion is born switched on) a silent clip made "this browser
// cannot play the audio" appear, which is a false accusation and a sticky one at
// that: it stayed until a clip with audio was played. The field is read by the
// monitor from the file, which is the only place that knows.
function checkTheAudioArrives() {
  const v = el('clip');
  if (!audioSuspect) return;
  if (audioDecoded(v)) {
    // The evidence contradicted the declaration: it is not asked again.
    audioSuspect = false;
    el('codec').classList.remove('show');
    return;
  }
  const v0 = entries.find((c) => c.name === chosen);
  if (!v0 || !v0.audio) return;
  // **"None decoded yet" is evidence only after something has played.** A
  // `timeupdate` also arrives when a source is set and when the position is
  // moved — which is what pressing Keep does, the lock being a rename — and
  // there not one sample of sound has been asked for, so the accusation came
  // up on a browser that plays the audio perfectly. `played` is what this
  // source has really played, and it starts from nothing with every source.
  if (playedSeconds(v) < 1) return;
  el('codec').classList.add('show');
}

// playedSeconds is how much of the current source has really been played.
function playedSeconds(v) {
  let s = 0;
  for (let i = 0; i < v.played.length; i++) s += v.played.end(i) - v.played.start(i);
  return s;
}

let entries = [];
let chosen = null;

// filter is the kind the list is narrowed to, or null for all of them.
let filter = null;

// selecting says whether the list is in selection mode, and selected holds the
// names chosen in it.
let selecting = false;
const selected = new Set();

// **A delete waits before it is sent, so that it can be taken back.** The page
// used to ask "Delete this recording? It cannot be undone." before every one,
// and a question asked every time is answered without being read: it protected
// nothing and it slowed the one gesture somebody makes twenty times after a
// day of motion. The clip leaves the list at once, a notice offers to put it
// back, and only when that has had its time does the request leave.
//
// gone are the names hidden from the list: waiting in `pending`, or on their
// way to the server. They stay hidden until the listing that follows says
// whether the delete took — a refused one comes back into the list on its own.
const UNDO_MS = 6000;
const gone = new Set();
let pending = null;

// **The warning panels are switched on with `.show`, not with `hidden`.** That
// class is the switch the sheet uses for every `.warnbox`: they are
// `display: none` to begin with, so removing `hidden` would not show them. It is
// the opposite face of the defect already paid for — a `display` written by us
// that stopped `hidden` hiding.
function showError(code) {
  const box = el('error');
  box.textContent = TErr(code);
  box.classList.add('show');
}

function hideError() {
  el('error').classList.remove('show');
}

// play loads the clip into the single player and declares which is playing.
function play(v) {
  chosen = v.name;
  el('player').hidden = false;
  el('clip').src = '/api/clips/' + encodeURIComponent(v.name);
  el('playing-when').textContent = CLOCK.format(new Date(v.at)) + ' · ';
  el('playing-what').textContent = kindName(v.code);
  draw();
  el('clip').scrollIntoView({block: 'nearest', behavior: 'smooth'});
}

// closePlayer empties the player, for a clip that has just left the list.
function closePlayer() {
  chosen = null;
  el('clip').removeAttribute('src');
  el('player').hidden = true;
}

// swapSource points the player at the name a clip has just been given, without
// the person watching it noticing.
//
// **The lock is a rename**, so the old address stops existing the moment the
// clip is kept, and the player has to be moved: a seek later on asks the old
// name for a range and gets nothing. Moved bare, though, the video started
// again from zero and, until the new file's size arrived, took the browser's
// default shape — the player shrank and grew back, as though the clip had been
// reloaded, which it had. So the height is held while the new source opens,
// and the position and the playing state are given back once it has.
function swapSource(name) {
  const v = el('clip');
  const at = v.currentTime;
  const playing = !v.paused && !v.ended;
  v.style.height = v.getBoundingClientRect().height + 'px';
  v.addEventListener('loadedmetadata', () => {
    v.currentTime = at;
    v.style.height = '';
    if (playing) v.play().catch(() => {});
  }, {once: true});
  v.addEventListener('error', () => { v.style.height = ''; }, {once: true});
  v.src = '/api/clips/' + encodeURIComponent(name);
}

// renamed carries a clip's new name to the two places that hold its old one:
// the player, if it is the open clip, and the selection.
//
// **It is written once, and the two callers share it.** The lock's single
// command and the selection's each carried a copy, and the copies drifted: a
// clip kept from its row while selected dropped out of the selection.
function renamed(old, now) {
  if (!now || now === old) return;
  if (chosen === old) {
    chosen = now;
    swapSource(now);
  }
  if (selected.delete(old)) selected.add(now);
}

// inFlight are the clips with a command on its way.
//
// **A second press before the list is redrawn is not sent.** Both presses carry
// the row's old name, and the first renames the file: the second then found no
// clip under that name and the page reported a refusal for a clip that was kept.
const inFlight = new Set();

// command runs an action on a clip and returns the name the clip exists under
// afterwards, which the lock changes.
async function command(name, action) {
  if (inFlight.has(name)) return null;
  inFlight.add(name);
  hideError();
  try {
    const r = await fetch('/api/clips/' + encodeURIComponent(name) + '/' + action,
      {method: 'POST'});
    if (sessionExpired(r)) return null;
    if (!r.ok) {
      const body = await bodyOf(r);
      showError(body.error);
      return null;
    }
    const body = await bodyOf(r);
    // **The new name is said by the server**, which is the only one that knows
    // how it is composed: recomposing it here would be the second copy of the
    // prefix rule, and the two would diverge at the first touch.
    renamed(name, body.name);
    await load();
    return body.name || name;
  } catch (e) {
    showError('');
    return null;
  } finally {
    inFlight.delete(name);
  }
}

// toggleKeep puts the lock on a clip or takes it off.
function toggleKeep(v) {
  command(v.name, v.kept ? 'release' : 'keep');
}

// MAX_ZIP is the most clips one archive carries: `maxZipClips` on the
// monitor's side, and `TestThePageKnowsTheArchiveCeiling` holds the two
// together. Past it the server refuses, and a refusal met by a download link
// is shown by nobody: the page says so before asking.
const MAX_ZIP = 200;

// downloadSelected hands the selection over as files: one clip as itself, more
// as one archive the monitor composes. **One download, not one per clip**: a
// page that starts several is asked by Chrome for permission, and on an iPhone
// only the first arrives.
function downloadSelected() {
  const names = [...selected];
  if (names.length === 0) return;
  if (names.length > MAX_ZIP) {
    const box = el('error');
    box.textContent = T('clips.zip-limit', {max: MAX_ZIP});
    box.classList.add('show');
    return;
  }
  const a = document.createElement('a');
  if (names.length === 1) {
    a.href = '/api/clips/' + encodeURIComponent(names[0]) + '?download';
  } else {
    a.href = '/api/clips.zip?' + names.map((n) => 'name=' + encodeURIComponent(n)).join('&');
  }
  a.setAttribute('download', '');
  document.body.append(a);
  a.click();
  a.remove();
}

// keepSelected puts the lock on the selection, or takes it off when every
// clip in it is kept already — the star's two directions, for a handful.
//
// **The selection follows the renames.** The lock changes a clip's name, and
// a selection holding the old names would lose every clip it had just kept.
async function keepSelected() {
  const all = [...selected].map((n) => entries.find((v) => v.name === n)).filter(Boolean);
  const release = all.length > 0 && all.every((v) => v.kept);
  const names = all.filter((v) => v.kept === release).map((v) => v.name)
    .filter((n) => !inFlight.has(n));
  if (names.length === 0) return;
  hideError();
  names.forEach((n) => inFlight.add(n));
  const answers = await Promise.allSettled(names.map((n) =>
    fetch('/api/clips/' + encodeURIComponent(n) + '/' + (release ? 'release' : 'keep'), {method: 'POST'})));
  names.forEach((n) => inFlight.delete(n));
  let failed = null;
  for (let i = 0; i < names.length; i++) {
    const a = answers[i];
    if (a.status === 'fulfilled' && sessionExpired(a.value)) return;
    if (a.status !== 'fulfilled' || !a.value.ok) {
      failed = a.status === 'fulfilled' ? (await bodyOf(a.value)).error || '' : '';
      continue;
    }
    renamed(names[i], (await bodyOf(a.value)).name);
  }
  await load();
  if (failed !== null) showError(failed);
}

// deleteLater takes clips out of the list and sends the delete once the notice
// has had its time. A delete already waiting is sent first: one notice, one
// batch, and an undo that takes back only what it says.
function deleteLater(names) {
  names = names.filter((n) => !inFlight.has(n) && !gone.has(n));
  if (names.length === 0) return;
  if (pending) sendPending();
  names.forEach((n) => gone.add(n));
  if (names.includes(chosen)) closePlayer();
  pending = {names: names, timer: setTimeout(sendPending, UNDO_MS)};
  const said = TN(names.length, 'clips.deleted');
  el('toast-text').textContent = said;
  el('toast').hidden = false;
  // **The notice is said by a region that is always there.** A live region
  // that appears together with its words is, to most screen readers, a region
  // that never changed; this one sits in the page from the start, empty, and
  // only its words move.
  el('toast-say').textContent = said;
  el('toast-undo').focus({preventScroll: true});
  draw();
}

// hideToast takes the notice away, and the focus with it if it was there: a
// focused button that vanishes leaves the keyboard nowhere.
function hideToast() {
  const had = el('toast').contains(document.activeElement);
  el('toast').hidden = true;
  el('toast-say').textContent = '';
  if (had) el('list').focus({preventScroll: true});
}

// undo puts the waiting clips back.
function undo() {
  if (!pending) return;
  clearTimeout(pending.timer);
  pending.names.forEach((n) => gone.delete(n));
  pending = null;
  hideToast();
  draw();
}

// sendPending sends the waiting deletes.
//
// **`keepalive`, because the page may be leaving, and every request starts at
// once.** Whoever deletes and then closes the tab has decided, and the
// notice's six seconds must not be the reason the clip is still there
// tomorrow: `pagehide` and a page going to the background send what is
// waiting, and a request marked `keepalive` outlives the page that made it —
// but only a request already made. Sent one after another, with an `await`
// between them, a page closing on five clips deleted the first and kept four.
async function sendPending() {
  if (!pending) return;
  const names = pending.names;
  clearTimeout(pending.timer);
  pending = null;
  hideToast();
  hideError();
  names.forEach((n) => inFlight.add(n));
  const answers = await Promise.allSettled(names.map((n) =>
    fetch('/api/clips/' + encodeURIComponent(n) + '/delete', {method: 'POST', keepalive: true})));
  names.forEach((n) => inFlight.delete(n));
  let failed = null;
  const refused = new Set();
  for (let i = 0; i < names.length; i++) {
    const a = answers[i];
    if (a.status === 'fulfilled' && sessionExpired(a.value)) return;
    if (a.status === 'fulfilled' && a.value.ok) continue;
    refused.add(names[i]);
    failed = a.status === 'fulfilled' ? (await bodyOf(a.value)).error || '' : '';
  }
  // **A clip comes back only on evidence.** One the server refused is shown
  // again at once; one it accepted leaves `gone` only once a listing has said
  // it is no longer there — after a listing that failed, the old one would put
  // a deleted clip back in the list.
  const listed = await load();
  names.forEach((n) => { if (listed || refused.has(n)) gone.delete(n); });
  draw();
  if (failed !== null) showError(failed);
}

// glyph clones an icon from the templates at the top of the page.
//
// **The drawings live in the markup**, as on the other two pages: here the rows
// are built from JavaScript, but the paths stay where they can be looked at and
// where the licence guard knows they are somebody else's work.
function glyph(name) {
  const t = document.getElementById('i-' + name);
  // A missing template gives a command with no drawing, not a broken page: the
  // words are there anyway, and an absent icon shows without doing damage.
  if (!t || !t.content || !t.content.firstElementChild) return null;
  return t.content.firstElementChild.cloneNode(true);
}

// label composes a command's content: the glyph and the word.
//
// **The word lives in a `<span>`, never in the button.** `textContent` on the
// button would delete the drawing too: it is the trap that has already come back
// three times, on "Copy", "Talk" and "Details".
function label(el, name, key) {
  const g = name ? glyph(name) : null;
  if (g) el.append(g);
  const s = document.createElement('span');
  s.textContent = T(key);
  el.append(s);
}

// visible are the clips the list shows: of those not on their way out, the
// ones of the kind the filter asks for.
function visible(live) {
  return live.filter((v) => !filter || v.code === filter);
}

// row draws one clip: its kind, when, how long, and the star.
//
// **A row carries one command, and it is the star.** Download, keep and delete
// used to sit as three labelled pills on every row — eighteen buttons on a
// page of six clips, heavier than the clips they were for, and a delete one
// thumb away from every row of the list. Now the row is pressed to play the
// clip, Download and Delete live under the player for the clip that is open,
// and what stays on the row is the one thing worth changing without opening
// it, which is whether the clip is kept. While selecting the star goes: the
// selection has a Keep of its own, and a star that renames a chosen clip under
// the finger is a second way of doing the same thing.
function row(v) {
  const li = document.createElement('li');
  const isSelected = selecting && selected.has(v.name);
  li.className = 'clip-row' + (v.name === chosen ? ' on' : '') +
    (v.kept ? ' kept' : '') + (isSelected ? ' sel' : '');

  const open = document.createElement('button');
  open.className = 'clip-open';
  open.type = 'button';
  open.dataset.name = v.name;

  if (selecting) {
    // **A kept clip can be selected like any other.** It could not, once: the
    // lock was read as "no rule touches it", a delete by the handful included,
    // and the star had to come off first. But the lock is against the monitor's
    // own cleanup, not against the owner, and every delete here can be taken
    // back for a few seconds — a second protection that only cost a step.
    const box = document.createElement('span');
    box.className = 'clip-check';
    box.setAttribute('aria-hidden', 'true');
    const tick = glyph('check');
    if (tick) box.append(tick);
    open.append(box);
    open.setAttribute('aria-pressed', isSelected ? 'true' : 'false');
  }

  const k = kindOf(v.code);
  const kindGlyph = k.glyph ? glyph(k.glyph) : null;
  if (kindGlyph) {
    kindGlyph.classList.add('kind');
    open.append(kindGlyph);
  }
  const data = document.createElement('span');
  data.className = 'clip-data';
  const what = document.createElement('b');
  what.textContent = kindName(v.code);
  const when = document.createElement('em');
  when.textContent = CLOCK.format(new Date(v.at)) + ' · ' + duration(v.ms);
  data.append(what, when);
  open.append(data);
  open.addEventListener('click', () => {
    if (!selecting) {
      play(v);
      return;
    }
    if (selected.has(v.name)) selected.delete(v.name);
    else selected.add(v.name);
    draw();
  });
  li.append(open);

  if (!selecting) {
    // **The star is a glyph alone, and the state is what explains it.** An
    // icon without its word asks to be guessed, which is why the two commands
    // under the player keep theirs; a star that fills when pressed is
    // the one convention every list of this kind already has, and its name is
    // said to whoever cannot see it by `aria-label`, its effect by `title`.
    const star = document.createElement('button');
    star.className = 'clip-star';
    star.type = 'button';
    star.dataset.name = v.name;
    star.setAttribute('aria-pressed', v.kept ? 'true' : 'false');
    star.setAttribute('aria-label', T('clips.keep'));
    star.title = T(v.kept ? 'clips.keep.on' : 'clips.keep.off');
    const g = glyph('star');
    if (g) star.append(g);
    star.addEventListener('click', () => toggleKeep(v));
    li.append(star);
  }
  return li;
}

// drawFilters draws one filter per kind, and one for all of them.
//
// **Every kind is always there**, the ones with no clip greyed out: the row
// keeps one shape whatever the list holds, and a kind that has not happened
// is itself something to read. A code this page does not know is added after
// them when a clip carries it.
function drawFilters(live) {
  const counts = new Map();
  live.forEach((v) => counts.set(v.code, (counts.get(v.code) || 0) + 1));
  if (filter && !counts.has(filter)) filter = null;
  const box = el('filters');
  box.hidden = live.length === 0;
  if (box.hidden) {
    box.replaceChildren();
    return;
  }
  const codes = KINDS.map((k) => k.code);
  counts.forEach((_, c) => { if (!codes.includes(c)) codes.push(c); });

  const chip = (code, text, n) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'chip';
    b.setAttribute('aria-pressed', filter === code ? 'true' : 'false');
    const k = code ? kindOf(code) : null;
    const g = k && k.glyph ? glyph(k.glyph) : null;
    if (g) b.append(g);
    const s = document.createElement('span');
    s.textContent = text;
    // A kind's word can go on a narrow screen, where the glyph says it; "All"
    // has no glyph, so its word stays.
    if (g) s.className = 'w';
    const count = document.createElement('span');
    count.className = 'n';
    count.textContent = String(n);
    b.append(s, count);
    b.disabled = n === 0;
    b.addEventListener('click', () => {
      filter = code;
      draw();
    });
    return b;
  };
  box.replaceChildren(chip(null, T('clips.filter.all'), live.length),
    ...codes.map((c) => chip(c, kindName(c), counts.get(c) || 0)));
}

// drawPlayerActions points the player's Download at the clip that is open,
// whose name the lock may just have changed.
function drawPlayerActions() {
  const v = entries.find((c) => c.name === chosen);
  if (!v) return;
  const down = el('player-download');
  down.href = '/api/clips/' + encodeURIComponent(v.name) + '?download';
  down.setAttribute('download', v.name);
}

// setSelecting enters or leaves selection mode.
//
// **A delete still waiting is sent on the way in.** The notice that takes it
// back and the selection's bar sit on the same spot, and whoever starts
// selecting has moved on from the clip they deleted: the two never show
// together, which is also why their two words can be the same.
function setSelecting(on) {
  if (on && pending) sendPending();
  selecting = on;
  selected.clear();
  draw();
}

function draw() {
  const live = entries.filter((v) => !gone.has(v.name));
  drawFilters(live);

  // **A selection holds only what is still in the list.** A clip renamed by
  // the lock, deleted from the player or pruned by the monitor while it was
  // chosen would otherwise go on counting, and the delete would ask for a name
  // that is no longer there.
  const deletable = new Set(visible(live).map((v) => v.name));
  selected.forEach((n) => { if (!deletable.has(n)) selected.delete(n); });

  // **The keyboard keeps its place.** Every redraw replaces the rows, and a
  // replaced button takes the focus with it: whoever selects ten clips by
  // keyboard would start again from the top of the page after each one.
  const focused = document.activeElement;
  const refocus = focused && focused.dataset && focused.dataset.name
    ? {name: focused.dataset.name, cls: focused.className} : null;

  // The clips arrive newest first, so a day is a run of neighbours: a new
  // section opens where the day changes.
  const today = new Date();
  const days = [];
  let open = null;
  visible(live).forEach((v) => {
    const at = new Date(v.at);
    const key = midnight(at).getTime();
    if (!open || open.key !== key) {
      const section = document.createElement('section');
      section.className = 'clips-day';
      const head = document.createElement('div');
      head.className = 'clips-dayhead';
      const h = document.createElement('h2');
      h.textContent = dayName(at, today);
      head.append(h);
      // "Select", or "Cancel" while selecting, rides on the first day's
      // heading: moved there at every redraw, since the rows are rebuilt.
      if (days.length === 0) head.append(selecting ? cancelButton : selectButton);
      const ul = document.createElement('ul');
      ul.className = 'clips-list';
      section.append(head, ul);
      days.push(section);
      open = {key, ul};
    }
    open.ul.append(row(v));
  });
  el('list').replaceChildren(...days);
  if (refocus) {
    const again = [...el('list').querySelectorAll('[data-name]')]
      .find((b) => b.dataset.name === refocus.name && b.className === refocus.cls);
    if (again) again.focus({preventScroll: true});
  }
  el('empty').hidden = live.length > 0;

  let bytes = 0;
  let kept = 0;
  live.forEach((v) => {
    bytes += v.bytes;
    if (v.kept) kept += 1;
  });
  el('note').textContent = live.length
    ? TN(live.length, 'clips.note', {mb: NUMBER.format(bytes / 1048576), kept: kept})
    : '';

  // **"Select" is there whenever the list is.** It used to appear only when
  // something could be deleted, so keeping the last unkept clip made it vanish
  // under the finger, which reads as a fault.
  // With no clips there is no heading for them to ride on, and they stay where
  // the markup put them: hidden, or they would stand under the empty state.
  selectButton.hidden = selecting || live.length === 0;
  cancelButton.hidden = !selecting || live.length === 0;
  el('selcmds').hidden = !selecting;
  el('tools').hidden = live.length === 0;
  // **Nothing chosen says nothing.** Portuguese and French take the singular
  // for zero, so "0 selected" would read "0 recording selected" there: the
  // count is written only once there is one, and the greyed "Delete" beside it
  // already says there is nothing to delete.
  el('sel-count').textContent = selected.size ? TN(selected.size, 'clips.selected') : '';
  el('sel-delete').disabled = selected.size === 0;
  el('sel-download').disabled = selected.size === 0;
  const keepButton = el('sel-keep');
  keepButton.disabled = selected.size === 0;
  const allKept = selected.size > 0 &&
    [...selected].every((n) => (entries.find((v) => v.name === n) || {}).kept);
  keepButton.setAttribute('aria-pressed', allKept ? 'true' : 'false');
  drawPlayerActions();
}

// load fetches the listing and says whether it got one.
async function load() {
  try {
    const r = await fetch('/api/clips');
    if (sessionExpired(r)) return false;
    if (!r.ok) {
      const body = await bodyOf(r);
      showError(body.error);
      return false;
    }
    entries = await r.json();
    hideError();
  } catch (e) {
    showError('');
    return false;
  }
  // A clip the monitor pruned while it was open is closed rather than left
  // playing under a name that no longer exists.
  if (chosen && !entries.some((v) => v.name === chosen)) closePlayer();
  draw();
  return true;
}

// The fixed commands are dressed once: their words and glyphs, from the same
// `label` the rows use, so a word never lands in a button by `textContent`.
//
// "Select" and "Cancel" are held here rather than looked up: `draw` moves them
// onto the first day's heading, and once a redraw has thrown that heading away
// they can be out of the document, where `getElementById` does not find them.
const selectButton = el('select');
const cancelButton = el('sel-cancel');
label(selectButton, null, 'clips.select');
label(el('player-download'), 'download', 'clips.download');
label(el('player-delete'), 'delete', 'clips.delete');
label(el('sel-download'), 'download', 'clips.download');
label(el('sel-keep'), 'star', 'clips.keep');
label(el('sel-delete'), 'delete', 'clips.delete');
label(cancelButton, 'close', 'clips.select.cancel');
label(el('toast-undo'), 'undo', 'clips.undo');

selectButton.addEventListener('click', () => setSelecting(true));
cancelButton.addEventListener('click', () => setSelecting(false));
el('sel-download').addEventListener('click', downloadSelected);
el('sel-keep').addEventListener('click', keepSelected);
el('sel-delete').addEventListener('click', () => {
  const names = [...selected];
  setSelecting(false);
  deleteLater(names);
});
el('player-delete').addEventListener('click', () => {
  if (chosen) deleteLater([chosen]);
});
el('toast-undo').addEventListener('click', undo);
document.addEventListener('keydown', (ev) => {
  if (ev.key === 'Escape' && selecting) setSelecting(false);
});
// **A page sent to the background may never come back.** On a phone the
// timer is suspended there and the tab can be discarded without a `pagehide`,
// so leaving the page for another app sends what is waiting: the undo is for
// the six seconds somebody is looking at it, not for a clip left pending.
window.addEventListener('pagehide', sendPending);
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden') sendPending();
});

// **The suspicion is not shown: it is checked.** The warning appears only if
// playback confirms the declaration. See checkTheAudioArrives.
audioSuspect = audioUnsupported();
// `timeupdate` and not `playing`: at the first instant the decoded byte
// counter is still zero on every browser, so looking at it there would always
// say "no audio" — that is, it would go back to showing the warning to whoever
// does have audio, by another road. `timeupdate` arrives once playback has
// begun and more than once, so the answer comes when it is there.
el('clip').addEventListener('timeupdate', checkTheAudioArrives);
// **A player that refuses says so.** `canPlayType` is a declaration, and a
// declaration can be wrong in both directions: if playback really fails, that
// is not an opinion.
el('clip').addEventListener('error', () => showError(''));
load();
