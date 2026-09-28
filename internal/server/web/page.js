// The helpers the pages share, written once instead of once per page.
//
// **It is loaded before the page's own script, and what it declares the pages
// then must not.** Classic scripts on one page share one lexical scope, so a
// second top-level `el` in the page's script would stop that script parsing at
// all — `TestNoTwoScriptsOnAPageDeclareTheSameName` refuses it. It is public
// like `auth.js`, because the login pages use it too.
//
// **The language is not here: it is `i18n.js`'s.** Nothing in this file writes a
// word, so nothing in it needs one.
'use strict';

const el = (id) => document.getElementById(id);

// postJSON sends a JSON body, which is how every command with a payload is
// written to the monitor.
function postJSON(url, body) {
  return fetch(url, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body),
  });
}

// bodyOf reads an answer's JSON, and an answer that has none is an empty object
// rather than an exception: a refusal is read for its `error` field, and one
// that arrives without a body still has to reach the line that shows it.
function bodyOf(res) {
  return res.json().catch(() => ({}));
}

// sessionExpired sends an expired session to the login, not to a red panel.
//
// **It was missing on the recordings page**: the listing stayed as it was, with
// a message that does not say how to get back in. A 401 is not a fault of the
// page, it is a closed door that has a key.
//
// **The viewer's heartbeat does not use it, and that is not an oversight**: it
// leaves on a 403 as well, which is the password gone after a reset from the
// tray — see `pollStatus`. Here a 403 is answered with its own sentence.
function sessionExpired(res) {
  if (res.status !== 401) return false;
  location.href = '/login';
  return true;
}

// signalingURL is the WebSocket address of the signalling, on the page's own
// host and with the scheme the page was opened with.
function signalingURL() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return proto + '//' + location.host + '/ws';
}

// qrFor points the image at an address's code, or hides it if there is no
// address. The server draws it (`/qr`), not the page: it is the same code for
// the viewer and the onboarding, so there are not two implementations that can
// diverge.
//
// **The image is reassigned only when the address changes.** The heartbeats run
// every second or three, and reassigning `src` every round makes the browser
// fetch again: a code that redraws itself while somebody is scanning it is a
// code that does not read.
function qrFor(img, url) {
  if (!img) return;
  // With no address the code hides instead of staying without a source: an
  // `<img>` with no `src` does not disappear, it draws the frame of a broken
  // image next to an address that declares it is not there.
  if (!url) {
    img.hidden = true;
    img.removeAttribute('src');
    return;
  }
  const src = '/qr?u=' + encodeURIComponent(url);
  if (img.getAttribute('src') !== src) img.setAttribute('src', src);
  img.hidden = false;
}

// The level's two thresholds on the 0..1 scale, **the same on the guided path
// and on the viewer**: there the bar and its reading in decibels demonstrate the
// microphone to whoever is installing, here they say what is happening in the
// room to whoever is watching at night. The measurement is the same formula on
// the same stream, so the numbers speak about the same quantity. What colour
// each band takes is each page's sheet's: amber above the first on both, and
// above the second brick on the guided path, where it means clipping, and light
// on the viewer — not brick, see `style.css`.
const VU_LOUD = 0.72;
const VU_PEAK = 0.92;

// dbfsOf reads the analyser once and returns the level in dBFS: the RMS of the
// block, and -100 for a block that is exactly zero.
function dbfsOf(analyser, buf) {
  analyser.getFloatTimeDomainData(buf);
  let sum = 0;
  for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
  const rms = Math.sqrt(sum / buf.length);
  return rms > 0 ? 20 * Math.log10(rms) : -100;
}

// fromDbfs brings the level into 0..1 on a scale that makes sense to the eye.
//
// **The scale is logarithmic and the floor is -60 dBFS, not -96.** Digital
// silence sits at -96, but the noise of an empty room is already around -70:
// measuring all the way down would leave the bars visibly lit on nothing, that
// is, they would say "I hear" when there is nothing to hear — which is exactly
// the fault the guided path's meter exists to find.
function fromDbfs(db) {
  if (typeof db !== 'number' || !isFinite(db)) return 0;
  const v = (db + 60) / 60;
  return Math.max(0, Math.min(1, v));
}
