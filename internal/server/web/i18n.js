// Translation of the pages.
//
// **The language is chosen by the viewer's browser, not by the computer hosting
// the monitor.** They are two different devices and often two different people:
// whoever installs the application is in front of the machine, whoever watches
// has a phone in their hand on the other side of the house, or in another
// country.
//
// **The keys are identifiers, and the main language is English.** Every language
// has its catalogue in `internal/i18n/catalogs/`, and there is no text in the
// markup, there are keys. English is what everybody falls back on: the
// repository is public and the product is made to travel, so the fallback
// language has to be the one somebody understands when theirs is missing.
//
// The price is declared: **reading the HTML no longer says what the page says**,
// and a key with no entry shows as a key. Both are covered by `i18n_test.go`,
// which demands every key in every catalogue and no orphan entry, both ways.
//
// The dictionary arrives from `/dictionary.js`, which the server composes by
// reading `Accept-Language` and **overlays the language asked for on English**:
// an entry missing from a translation falls back on English rather than showing
// the key. It is deliberately not fetched: a catalogue arriving after the first
// paint makes the page flash, and an inline script is not executed under the
// CSP.

'use strict';

const DICTIONARY = (typeof window !== 'undefined' && window.__I18N) || {};
// The language is declared here and **only here**: `clips.js` reads this same
// `LANG` for its Intl formats, because two scripts on one page share one scope
// and a second declaration of the same name stops the second file from parsing.
const LANG = (typeof window !== 'undefined' && window.__I18N_LANG) || 'en';

// The plural rules are known by the browser. They are not "one and everything
// else": Russian has three, Polish four, and concatenating a number onto a
// sentence written for one language makes them ungrammatical.
const PLURALS = (() => {
  try {
    return new Intl.PluralRules(LANG);
  } catch (err) {
    return null;
  }
})();

// fill replaces the {name} placeholders. The number always travels as {n}.
function fill(text, params) {
  if (!params) return text;
  return text.replace(/\{(\w+)\}/g, (whole, name) =>
    Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : whole);
}

// T translates a key.
//
// With no entry **the key** is shown, which is ugly and visible. That is
// deliberate: an empty string would hide the hole precisely where it is easiest
// not to notice, and on this page a missing label reads as a broken button.
function T(key, params) {
  const entry = DICTIONARY[key];
  return fill(typeof entry === 'string' ? entry : key, params);
}

// TOr translates a key, and if it is not there falls back on another key.
//
// **It serves the composed keys.** `T('viewer.alert.' + code)` is the right
// shape — the code arrives from the server and the page picks the word — but a
// code this page does not know yet would produce a `T` of a non-existent key,
// that is, the key printed on the screen. It really happens: an updated monitor
// and a `dictionary.js` still in cache are two different versions of the same
// product, and it happens precisely on the alert banner, which is the one line
// of the page that cannot afford to be unreadable.
//
// The fallback is a key and not a sentence: a sentence here would be text inside
// the code, that is, the very thing this mechanism exists to remove.
function TOr(key, fallback, params) {
  return typeof DICTIONARY[key] === 'string' ? T(key, params) : T(fallback, params);
}

// TErr translates the reason for a refusal from the server.
//
// **The server sends a code, not a sentence.** It used to send the sentence, and
// the pages showed it as it came: `body.error || T('auth.failed')` — the
// catalogue was already there and was the fallback, while the real text arrived
// from `server.go` in one language. With the interface translated it would have
// been an English form answering the first error in another language.
//
// The fallback on `err.unknown` is not wasted caution: an updated monitor and a
// dictionary still in cache are two versions of the same product, and there the
// new code would appear bare inside a red panel.
//
// `retryAfter` arrives as a **number of seconds** because a duration written
// inside the sentence belongs to one language as much as the words do — and for
// the same reason it goes through `TN`: the first draft wrote "try again in 1
// seconds", which is the defect the catalogue's plurals exist to remove.
//
// **The check on `retryAfter` is explicit and not a `Number()` and be done.**
// `params.get()` of an absent key returns `null`, and `Number(null)` is
// **zero**: without these three lines an ordinary refusal, which has no wait at
// all, would say "try again in 0 seconds".
function TErr(code, retryAfter) {
  if (!code) return T('err.unknown');
  const wait = (retryAfter === null || retryAfter === undefined || retryAfter === '')
    ? NaN : Number(retryAfter);
  if (Number.isFinite(wait) && DICTIONARY['err.' + code] !== undefined) {
    return TN(wait, 'err.' + code);
  }
  return TOr('err.' + code, 'err.unknown');
}

// TAction is the line that says what falls to the user for access from outside.
//
// **It lives here and not in a page because there are two readers**, and it is
// the lesson this function has already taught once: born inside
// `onboarding.js`, it left the viewer showing `r.action` as it came — that is,
// since that field became a code, the word `authorise` inside the remote-access
// panel. The question to ask at every change of shape is not "who writes it", it
// is **"who reads it"**, and the answer was two.
//
// **Three sources, and the order matters.**
//
// First `actionText`, the only text that arrives from outside: Tailscale's
// control server writes it knowing the tailnet and the role of whoever is
// looking — to an administrator it gives a link that enables everything in one
// click, to a member it says who to ask — and rewriting it would mean guessing.
// It arrives in English, and it stays the one foreign line of a translated path:
// it is the price of a right instruction instead of one of ours that can be
// wrong.
//
// Then our code, which is the fallback for when Tailscale does not answer and
// says a good deal less, because it knows neither of those two things.
//
// And last the caller's fallback, for when there is neither one nor the other.
function TAction(r, fallback) {
  if (r.actionText) return r.actionText;
  if (r.action) {
    return TOr('tunnel.action.' + r.action, fallback || 'err.unknown',
               {step: r.failedStep ? T('tunnel.step.' + r.failedStep) : ''});
  }
  return fallback ? T(fallback) : '';
}

// TN translates a key that carries a number.
//
// The catalogue entry is an object with the language's CLDR categories (`one`,
// `other`, and where needed `few`, `many`, `zero`, `two`), so that whoever
// translates gives as many as their language asks for instead of bending to
// English's two.
function TN(n, key, params) {
  const p = Object.assign({n: n}, params);
  const entry = DICTIONARY[key];
  if (entry && typeof entry === 'object') {
    const category = PLURALS ? PLURALS.select(n) : (n === 1 ? 'one' : 'other');
    return fill(entry[category] || entry.other || key, p);
  }
  if (typeof entry === 'string') return fill(entry, p);
  return fill(key, p);
}

// applyHTML fills the markup: `data-i18n="key"` on the text,
// `data-i18n-html="key"` where the sentence contains emphasis, and
// `data-i18n-attr="attribute:key ..."` on the attributes.
//
// **`data-i18n-html` exists because a sentence is not broken into three.** In
// the configuration path a dozen or so paragraphs have a `<strong>` in the
// middle, and in another language that emphasised piece does not fall in the
// same place: marking the three fragments separately would give whoever
// translates three stumps and no sentence. The translated text therefore goes to
// `innerHTML`.
//
// That this is safe is not an opinion: **the catalogues are embedded assets**,
// they do not come from outside, and `i18n_test.go` allows in those entries only
// `strong`, `em`, `b`, `code` and `br`, **with no attributes at all**. No `<a>`,
// no `<button>`, no `id`: those are what `onboarding.js` looks for in order to
// attach a listener, and a catalogue that rewrote them would switch a button
// off.
//
// The attributes are the ones nobody sees until they are needed — an image's
// alternative text, a button's tooltip — and that is why they get forgotten.
function applyHTML(root) {
  const where = root || document;

  where.querySelectorAll('[data-i18n]').forEach((el) => {
    el.textContent = T(el.getAttribute('data-i18n'));
  });

  where.querySelectorAll('[data-i18n-html]').forEach((el) => {
    el.innerHTML = T(el.getAttribute('data-i18n-html'));
  });

  // `data-i18n-plain="key"` is the same sentence without its Windows
  // accelerator marker: `&Quit` is `Quit`, `&&` is one ampersand.
  //
  // **It exists so that a drawing of the tray can carry the tray's own
  // strings.** The illustration at the end of the guided path depicts the panel
  // the right button opens, and its labels are the ones the panel really
  // renders — the same keys, read twice. Written out again as `onb.…` entries
  // they would be a second list, and this file already records what that costs:
  // the drawing once offered a "Settings" entry that has never existed.
  //
  // The marker cannot simply be dropped in the catalogue, because the tray is
  // the other reader and there it is not decoration: it is what Windows
  // underlines when Alt is held.
  where.querySelectorAll('[data-i18n-plain]').forEach((el) => {
    el.textContent = T(el.getAttribute('data-i18n-plain'))
      .replace(/&(.)/g, '$1');
  });

  where.querySelectorAll('[data-i18n-attr]').forEach((el) => {
    el.getAttribute('data-i18n-attr').split(/\s+/).forEach((pair) => {
      const cut = pair.indexOf(':');
      if (cut < 0) return;
      el.setAttribute(pair.slice(0, cut), T(pair.slice(cut + 1)));
    });
  });

  // The declared language has to follow the text: hyphenation, the quotation
  // marks the browser draws and what a screen reader reads all depend on `lang`.
  // An English page declared `it` sounds wrong before it looks wrong.
  if (where === document) document.documentElement.lang = LANG;
}

// The language selector.
//
// **It exists because the detected language can be the wrong one.** The browser
// declares `Accept-Language`, and that declaration is often a choice made once
// when the system was installed, or not made at all: whoever watches a baby
// monitor from somebody else's phone, or on a computer configured in a language
// they do not speak, has to be able to say which one they want.
//
// **The choice travels in a cookie and the page reloads**, rather than replacing
// the text here. The dictionary is composed by the server, and it is the server
// that overlays the language asked for on English: redoing it in the browser
// would mean a second implementation of the same rule, and two implementations
// of the same rule diverge. A reload costs an instant and happens once.
//
// The cookie is deliberately not `Secure`: at home the page is served over
// `http`, and a cookie the browser refuses is a preference that never gets
// saved. There is nothing to protect — it is a display preference — and the
// value is **validated by the server** against the list of catalogues, so it
// cannot become a file path.
function mountSelector() {
  const where = document.querySelector('[data-lang]');
  const languages = (typeof window !== 'undefined' && window.__I18N_LANGS) || null;
  // With one language there is nothing to choose, and a menu with one entry is
  // a command that does nothing.
  if (!where || !languages || Object.keys(languages).length < 2) return;

  const sel = document.createElement('select');
  sel.setAttribute('aria-label', T('lang.choose'));
  for (const [code, name] of Object.entries(languages)) {
    const o = document.createElement('option');
    o.value = code;
    // **A language's name is written in that language.** "Italiano", not
    // "Italian": whoever looks for their own in a list looks for the word they
    // know, and if the page is in a language they do not understand that word is
    // the only one they can recognise.
    o.textContent = name;
    o.selected = code === LANG;
    sel.append(o);
  }
  sel.addEventListener('change', () => {
    document.cookie = 'lang=' + encodeURIComponent(sel.value) +
      ';path=/;max-age=31536000;samesite=lax';
    location.reload();
  });
  where.append(sel);
}

if (typeof document !== 'undefined') {
  applyHTML();
  mountSelector();
}

// For testing with Node, which has no DOM.
if (typeof module !== 'undefined') module.exports = {T, TN, TOr, TErr, TAction, fill};
