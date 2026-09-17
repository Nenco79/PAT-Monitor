'use strict';

// The recordings page: a listing, one player, and the four things that can be
// done to a clip.
//
// The words all come from `T`: there is not one in here. The event names reuse
// the keys the viewer's banner already has — `viewer.alert.motion` and its
// sisters — rather than a second dictionary of the same three codes, which would
// diverge at the first touch.

// **The language is `i18n.js`'s, and it is declared there.** This page loads
// that script first, so `LANG` is already in scope: declaring a second one here
// is what killed this page — two top-level `const` of the same name in two
// classic scripts share one lexical scope, and the second file then fails to
// parse **in its entirety**. Not one line of it runs, and the page shows its
// title and nothing else.
const WHEN = new Intl.DateTimeFormat(LANG, {dateStyle: 'short', timeStyle: 'medium'});
const NUMBER = new Intl.NumberFormat(LANG, {maximumFractionDigits: 1});

const el = (id) => document.getElementById(id);

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

function size(bytes) {
  return NUMBER.format(bytes / 1048576) + ' MB';
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

function eventName(code) {
  if (code === MANUAL) return T('clips.manual');
  return TOr('viewer.alert.' + code, code);
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
  el('codec').classList.add('show');
}

let entries = [];
let chosen = null;

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
  el('playing-when').textContent = WHEN.format(new Date(v.at));
  el('playing-what').textContent = eventName(v.code);
  draw();
  el('clip').scrollIntoView({block: 'nearest', behavior: 'smooth'});
}

// **An expired session leads to the login, not to a red panel.** It is what the
// viewer's heartbeat does, and it was missing here: the listing stayed as it
// was, with a message that does not say how to get back in. A 401 is not a fault
// of the page, it is a closed door that has a key.
function sessionExpired(r) {
  if (r.status !== 401) return false;
  location.href = '/login';
  return true;
}

// command runs an action on a clip and returns the name the clip exists under
// afterwards, which the lock changes.
async function command(name, action) {
  hideError();
  try {
    const r = await fetch('/api/clips/' + encodeURIComponent(name) + '/' + action,
      {method: 'POST'});
    if (sessionExpired(r)) return null;
    if (!r.ok) {
      const body = await r.json().catch(() => ({}));
      showError(body.error);
      return null;
    }
    const body = await r.json().catch(() => ({}));
    // **The new name is said by the server**, which is the only one that knows
    // how it is composed: recomposing it here would be the second copy of the
    // prefix rule, and the two would diverge at the first touch.
    if (body.name && chosen === name) {
      chosen = body.name;
      el('clip').src = '/api/clips/' + encodeURIComponent(body.name);
    }
    await load();
    return body.name || name;
  } catch (e) {
    showError('');
    return null;
  }
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
  const g = glyph(name);
  if (g) el.append(g);
  const s = document.createElement('span');
  s.textContent = T(key);
  el.append(s);
}

function button(className, name, key, fn) {
  const b = document.createElement('button');
  b.className = className;
  b.type = 'button';
  label(b, name, key);
  b.addEventListener('click', (ev) => {
    ev.stopPropagation();
    fn();
  });
  return b;
}

function row(v) {
  const li = document.createElement('li');
  li.className = 'clip-row' + (v.name === chosen ? ' on' : '') + (v.kept ? ' kept' : '');

  const head = document.createElement('button');
  head.className = 'clip-open';
  head.type = 'button';
  const playGlyph = glyph('play');
  if (playGlyph) head.append(playGlyph);
  const data = document.createElement('span');
  data.className = 'clip-data';
  const when = document.createElement('b');
  when.textContent = WHEN.format(new Date(v.at));
  const what = document.createElement('span');
  what.textContent = eventName(v.code);
  const numbers = document.createElement('em');
  numbers.textContent = duration(v.ms) + ' · ' + size(v.bytes);
  data.append(when, what, numbers);
  head.append(data);
  head.addEventListener('click', () => play(v));

  const actions = document.createElement('div');
  actions.className = 'clip-actions';

  const down = document.createElement('a');
  down.className = 'ghost';
  down.href = '/api/clips/' + encodeURIComponent(v.name) + '?download';
  down.setAttribute('download', v.name);
  label(down, 'download', 'clips.download');
  actions.append(down);

  // **The lock is a toggle, and it used to be one-way.** A kept clip showed a
  // badge — "Kept" — and from there there was no going back: that was enough
  // while the lock was put on by hand, one clip at a time. Since clips asked for
  // by hand are **born kept** that one road is not enough: every press of "Clip"
  // would leave a few megabytes on the disk that no rule touches again.
  //
  // The shape is the monitor bar's: the pill says the state, `aria-pressed` says
  // it to whoever cannot see it, and **the label does not move** — a text that
  // changes when pressed reads as a different command that has appeared in place
  // of the previous one. What happens when it is pressed is said by the tooltip.
  const keep = button('ghost toggle', 'keep', 'clips.keep',
    () => command(v.name, v.kept ? 'release' : 'keep'));
  keep.setAttribute('aria-pressed', v.kept ? 'true' : 'false');
  keep.title = T(v.kept ? 'clips.keep.on' : 'clips.keep.off');
  actions.append(keep);

  actions.append(button('ghost danger', 'delete', 'clips.delete', () => {
    if (!confirm(T('clips.confirm'))) return;
    const wasPlaying = v.name === chosen;
    command(v.name, 'delete').then((done) => {
      if (done && wasPlaying) {
        chosen = null;
        el('clip').removeAttribute('src');
        el('player').hidden = true;
      }
    });
  }));

  li.append(head, actions);
  return li;
}

function draw() {
  const list = el('list');
  list.replaceChildren(...entries.map(row));
  el('empty').hidden = entries.length > 0;

  let bytes = 0;
  let kept = 0;
  entries.forEach((v) => {
    bytes += v.bytes;
    if (v.kept) kept += 1;
  });
  el('note').textContent = entries.length
    ? TN(entries.length, 'clips.note', {mb: NUMBER.format(bytes / 1048576), kept: kept})
    : '';
}

async function load() {
  try {
    const r = await fetch('/api/clips');
    if (sessionExpired(r)) return;
    if (!r.ok) {
      const body = await r.json().catch(() => ({}));
      showError(body.error);
      return;
    }
    entries = await r.json();
    hideError();
  } catch (e) {
    showError('');
    return;
  }
  draw();
}

if (typeof document !== 'undefined') {
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
}
