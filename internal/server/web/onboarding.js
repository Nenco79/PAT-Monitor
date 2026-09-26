// The first-configuration path.
//
// It lives in an external file and not inline: the Content-Security-Policy
// declares `script-src 'self'`, and when an inline block was not executed the
// password form fell back to native submission, putting the password in the URL.
//
// **The rule of this file, in one line: the path shows the real state, it does
// not narrate it.** Every step from 3 on is driven by `/api/status`, not by an
// "I have done it" button — a user who declares they have authorised when they
// have not arrives at the end with an address that does not answer, which is the
// worst way to finish a guided path. Buttons stay only where there is a real
// choice to make.
'use strict';

const el = (id) => document.getElementById(id);

// ------------------------------------------------------------------ scaffold

// The names and the time left for each step.
//
// **The cost is declared before it is charged.** An announced cost is borne; the
// same cost discovered half way makes people close the window, and whoever
// closes does not come back.
//
// The step names are keys: the word is chosen by the catalogue.
const NAMES = ['onb.step.security', 'onb.step.check', 'onb.step.choice',
               'onb.step.tailscale', 'onb.step.tailscale', 'onb.step.last',
               'onb.step.ready'];
const TONE = ['home', 'home', 'choice', 'away', 'away', 'away', 'ready', 'stop'];

const STEP_READY = 6;
const STEP_FAILED = 7;

// The two routes, and why there are two.
//
// **The total is not promised before the fork.** Whoever picks "at home is
// enough for me" sees four screens in all, and declaring seven at the opening is
// a wall in front of three steps: it announces a cost that person would never
// pay, in the one moment when they can still close the window. So we start from
// the short route, which is also the guaranteed minimum, and extend it when the
// user asks for the other one — that is, right after the fork has told them what
// it costs. The direction matters: announcing less and then extending is an
// accepted cost, announcing more and then discounting does not repay whoever has
// already left.
//
// **A step is a stretch that asks something of whoever walks it.** Preparing the
// secure connection asks nothing — its screen says in so many words "nothing to
// do" — so it is not a step, and it shares its rung with "all set": they are the
// wait and the arrival of the same thing.
const HOME    = [[0], [1], [2], [6]];
const OUTSIDE = [[0], [1], [2], [3], [4], [5, 6]];

// The time left, by position in the route and not by screen: the same screen
// costs five minutes to whoever goes outside and one to whoever does not.
//
// **The minutes are counted, not written.** They used to be set phrases — "about
// 5 minutes" — and in another language they would become five nearly identical
// entries. Here the number travels and `TN` composes the sentence with the right
// plural: zero means nothing is declared, and that is the last step.
const LEFT = new Map([
  [HOME,    [1, 0.5, 0, 0]],
  [OUTSIDE, [5, 5, 4, 3, 2, 0]],
]);

// timeLeft composes "about 5 minutes", or "less than a minute" below the minute.
function timeLeft(m) {
  if (!m) return '';
  if (m < 1) return T('onb.under-a-minute');
  return TN(m, 'onb.about-minutes');
}

let route = HOME;

// The position of a screen in the chosen route, or -1 if it is not part of it —
// the failure screen, and the Tailscale steps for whoever stays at home.
const ordinal = (n) => route.findIndex((g) => g.includes(n));

function setRoute(p) {
  if (p === route) return;
  route = p;
  rail.replaceChildren();
  for (let i = 0; i < route.length; i++) rail.appendChild(document.createElement('i'));
  updateHeader(step);
}

const sections = Array.from(document.querySelectorAll('.step'));
const rail = el('rail');
const progress = el('progress');

for (let i = 0; i < route.length; i++) rail.appendChild(document.createElement('i'));

let step = -1;

// homeOnly remembers that the user chose not to go outside the house.
//
// The last step needs it: without it, it could not tell "there is no access from
// outside because you did not want it" from "there is none because it did not
// work". They are two different screens, and saying it wrongly is worse than
// saying nothing.
let homeOnly = false;

// alreadyDone: the path had been completed before.
//
// Step 0 needs it. After a password reset from the icon's menu the monitor finds
// itself without a credential but with everything else configured: walking every
// step again to pick a password would charge the whole configuration to somebody
// who has only forgotten a word.
let alreadyDone = false;

// signedIn: the page has had a valid session at least once.
//
// It tells **"you have not got in yet" from "you are not in any more"**, which
// from outside are the same answer — a 401 — and are opposite pieces of news.
// Step 0 has no session by construction: it is asking for one. Without this
// distinction the heartbeat takes a second to cover the welcome screen with
// "session expired, reload the page and sign in with the password", that is,
// with a red alarm ordering the user to sign in with a credential they **have
// not chosen yet**, and to reload, which there does nothing at all. The first
// contact with the product was an invented fault.
let signedIn = false;

function show(n) {
  if (n === step) return;
  step = n;

  // **The route is the one that contains the screen we are on.** Whoever reopens
  // `/onboarding#p4` with the tunnel already up, or is taken there by
  // `followPhase`, arrives on a step the short route does not have: without this
  // line the bar at the top would say "something did not work".
  if (ordinal(n) < 0 && n !== STEP_FAILED) setRoute(OUTSIDE);

  sections.forEach((s) => s.classList.toggle('show', Number(s.dataset.step) === n));
  updateHeader(n);

  // The step lives in the anchor, so that refreshing the page does not go back
  // to the top: inside a five-minute path, starting over for an accidental F5 is
  // the kind of thing that makes people give up.
  const anchor = '#p' + n;
  if (location.hash !== anchor) history.replaceState(null, '', anchor);

  // The focus has to be moved by hand: the sections are all there from the
  // start, so for whoever navigates by keyboard or with a screen reader
  // "changing step" otherwise does not happen — the focus would stay where it
  // was, on a button that is now hidden.
  const active = sections.find((s) => Number(s.dataset.step) === n);
  if (active) {
    const heading = active.querySelector('h1');
    if (heading) {
      heading.setAttribute('tabindex', '-1');
      heading.focus({preventScroll: true});
    }
  }
  window.scrollTo({top: 0, behavior: 'smooth'});
}

// The bar and the count at the top. They live in a function of their own because
// a change of route rewrites them too, not just a change of step.
function updateHeader(n) {
  const o = ordinal(n);
  for (let i = 0; i < rail.children.length; i++) {
    rail.children[i].className = i < o ? 'done' : (i === o ? 'now' : '');
  }
  progress.className = 'progress step ' + TONE[n];

  if (o >= 0) {
    const left = timeLeft(LEFT.get(route)[o]);
    el('p-now').textContent = T(NAMES[n]);
    // "1 of 4" is not concatenated: in another language the order of the two
    // numbers may not be this one.
    el('p-count').textContent = T('onb.count', {n: o + 1, tot: route.length}) +
      (left ? ' · ' + left : '');
  } else {
    el('p-now').textContent = T('onb.failed-title');
    el('p-count').textContent = T('onb.failed-count');
  }
}

// The buttons that only navigate. The ones that do something have an id.
document.addEventListener('click', (e) => {
  const b = e.target.closest('[data-go]');
  if (b) show(Number(b.dataset.go));
});

// ------------------------------------------------------------- code to scan

// qrFor points the image at the address's code, without remaking it every time.
//
// The heartbeat runs once a second: reassigning `src` every round makes the
// browser fetch again and the image flickers, which inside a still page reads as
// something going wrong.
function qrFor(id, url) {
  const img = el(id);
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

// --------------------------------------------------------------------- copy

// One handler for every "Copy" button: the element to copy is written in the
// attribute, so adding one does not mean touching this file.
document.addEventListener('click', async (e) => {
  const b = e.target.closest('[data-copy]');
  if (!b) return;
  const src = el(b.dataset.copy);
  if (!src) return;
  try {
    await navigator.clipboard.writeText(src.textContent.trim());
    // It is written into the `span`, not into the button: `textContent` on the
    // button replaces **all** the content, icon included, and the icon would
    // never come back — a defect that appears a second and a half after the
    // click, that is, where nobody looks for it.
    const label = b.querySelector('span') || b;
    const before = label.textContent;
    label.textContent = T('onb.copied');
    setTimeout(() => { label.textContent = before; }, 1500);
  } catch (err) {
    // The clipboard permission can be missing, and in that case the user must be
    // able to do it by hand instead of being left without an answer.
    const r = document.createRange();
    r.selectNodeContents(src);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  }
});

// ------------------------------------------------------------- 0 · password

const pwForm = el('pw-form');
const pwMsg = el('pw-msg');

function pwSays(text, kind) {
  pwMsg.textContent = text;
  pwMsg.className = 'field-msg ' + kind;
}

pwForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  const password = el('password').value;
  const confirm = el('confirm').value;

  pwMsg.className = 'field-msg';
  if (password !== confirm) {
    pwSays(T('auth.mismatch'), 'error');
    el('confirm').focus();
    return;
  }

  const submit = el('pw-submit');
  submit.disabled = true;
  try {
    const res = await fetch('/api/setup', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({password, confirm}),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      pwSays(TErr(body.error, body.retryAfter), 'error');
      return;
    }

    // **Straight after, we sign in, without going through /login.** From here on
    // the path shows real data — the camera's picture, the tunnel's state — and
    // that sits behind the session. Sending the user to retype the password they
    // have just chosen, inside a guided path, would be one more step that asks
    // nothing new.
    const acc = await fetch('/api/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({password}),
    });
    if (!acc.ok) {
      pwSays(T('onb.pw.set-no-login'), 'error');
      return;
    }
    if (alreadyDone) {
      // It was a reset, not a first install: back to watching.
      location.href = '/';
      return;
    }
    show(1);
  } catch (err) {
    pwSays(T('onb.unreachable'), 'error');
  } finally {
    submit.disabled = false;
  }
});

// ------------------------------------------------------ 0a · already chosen

// Whoever reopens the path with a password already set but no session comes in
// here. It is not a duplicate of /login: from there one ends up on the monitor,
// while here the path has to carry on, and sending the user out and back in is
// the kind of bounce that loses the thread.
const signinForm = el('pw-signin');
const signinMsg = el('pw-signin-msg');

signinForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  const submit = el('pw-signin-submit');
  submit.disabled = true;
  signinMsg.className = 'field-msg';
  try {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({password: el('signin-password').value}),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      signinMsg.textContent = TErr(body.error, body.retryAfter);
      signinMsg.className = 'field-msg error';
      el('signin-password').select();
      return;
    }
    signinForm.hidden = true;
    el('pw-done').hidden = false;
    show(1);
  } catch (err) {
    signinMsg.textContent = T('onb.unreachable');
    signinMsg.className = 'field-msg error';
  } finally {
    submit.disabled = false;
  }
});

// ------------------------------------------------- 0b · changing the password

// **A screen that declares a limit without offering the way out is where the
// user goes looking elsewhere for something that is right here.** Whoever
// reopens the path with a password already set can change it from here, with the
// old one in hand, instead of being told that from here it cannot be done.

const changeForm = el('pw-change-form');
const changeMsg = el('pw-change-msg');

el('pw-change').addEventListener('click', () => {
  changeForm.hidden = false;
  el('pw-change').hidden = true;
  el('current').focus();
});

el('pw-change-cancel').addEventListener('click', () => {
  changeForm.hidden = true;
  el('pw-change').hidden = false;
  changeMsg.className = 'field-msg';
  changeForm.reset();
});

changeForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  const current = el('current').value;
  const password = el('new-password').value;
  const confirm = el('new-confirm').value;

  changeMsg.className = 'field-msg';
  if (password !== confirm) {
    changeMsg.textContent = T('auth.mismatch');
    changeMsg.className = 'field-msg error';
    el('new-confirm').focus();
    return;
  }

  const submit = el('pw-change-submit');
  submit.disabled = true;
  try {
    const res = await fetch('/api/password', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({current, password, confirm}),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      changeMsg.textContent = TErr(body.error, body.retryAfter);
      changeMsg.className = 'field-msg error';
      return;
    }

    // The change has closed **every** session, this one included: without
    // signing in again the next steps would meet a 401 and the path would stop
    // without saying why. We sign in with the new one, which is also the proof
    // that it really was saved.
    const acc = await fetch('/api/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({password}),
    });
    if (!acc.ok) {
      changeMsg.textContent = T('onb.pw.changed-no-login');
      changeMsg.className = 'field-msg error';
      return;
    }
    changeForm.reset();
    changeForm.hidden = true;
    el('pw-change').hidden = false;
    show(1);
  } catch (err) {
    changeMsg.textContent = T('onb.unreachable');
    changeMsg.className = 'field-msg error';
  } finally {
    submit.disabled = false;
  }
});

// ---------------------------------------------------------------- 1 · preview

// Video preview, one way and mute.
//
// **It is the real stream and not a thumbnail**, because the step exists to
// prove that the camera works and a picture proves nothing. Mute on purpose:
// here it has to be seen, the microphone is proved by the level bars, and
// without audio the autoplay block does not trigger — that is, one tap fewer
// inside a path.
let previewPc = null;
let previewWs = null;

function closePreview() {
  // The meter's loop stops **before** the connection is closed: a
  // requestAnimationFrame that keeps polling a detached analyser runs forever at
  // sixty rounds a second with nothing to say so.
  vuActive = false;
  vuPeak = 0;
  if (previewWs) { previewWs.onclose = null; previewWs.close(); previewWs = null; }
  if (previewPc) { previewPc.close(); previewPc = null; }
}

function openPreview() {
  closePreview();
  const video = el('ob-video');
  const state = el('ob-video-state');
  state.textContent = T('onb.prev.opening');

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const ws = new WebSocket(proto + '//' + location.host + '/ws');
  previewWs = ws;

  ws.onmessage = async (ev) => {
    let m;
    try { m = JSON.parse(ev.data); } catch (err) { return; }

    if (m.type === 'offer') {
      const pc = new RTCPeerConnection({iceServers: m.iceServers || []});
      previewPc = pc;
      const stream = new MediaStream();
      pc.ontrack = (t) => {
        stream.addTrack(t.track);
        if (video.srcObject !== stream) video.srcObject = stream;
        if (t.track.kind === 'audio') attachVu(stream);
      };
      pc.onicecandidate = (c) => {
        if (c.candidate && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({type: 'candidate', candidate: c.candidate.toJSON()}));
        }
      };
      pc.onconnectionstatechange = () => {
        if (pc.connectionState === 'connected') state.textContent = T('viewer.state.live');
        if (pc.connectionState === 'failed') state.textContent = T('onb.prev.failed-connect');
      };
      // **`m.sdp` is already a complete description, not a string.** The server
      // sends `webrtc.SessionDescription`, that is `{type, sdp}`, and repacking
      // it inside another object produces an `sdp` worth "[object Object]": the
      // negotiation fails with no error saying where. The same in the answer,
      // where `pc.localDescription` is sent whole and not the text alone — that
      // is what the server deserialises.
      try {
        await pc.setRemoteDescription(m.sdp);
        const answer = await pc.createAnswer();
        await pc.setLocalDescription(answer);
        ws.send(JSON.stringify({type: 'answer', sdp: pc.localDescription}));
      } catch (err) {
        // **A negotiation that fails has to say so.** Without this branch the
        // line stays on "opening the camera…" forever, and a preview that never
        // arrives reads as a broken camera: it sends people looking for the
        // fault exactly where it is not.
        state.textContent = T('onb.prev.failed') + ': ' + err;
        console.error('camera preview', err);
      }
      return;
    }

    if (m.type === 'candidate' && previewPc) {
      try { await previewPc.addIceCandidate(m.candidate); } catch (err) { /* too late */ }
      return;
    }

    if (m.type === 'error') {
      // The words are chosen by this page, from the code the server sends.
      //
      // **A field that changes shape has more readers than anybody remembers.**
      // When signalling started carrying a code, the viewer was updated and this
      // page was not: it kept reading `m.message` and so always showed the
      // generic sentence — that is, the preview said "did not work" without ever
      // saying why, which is the one thing that step exists for.
      const reasons = {
        'not-ready': 'onb.prev.not-ready',
        sdp: 'onb.prev.sdp',
        'too-many-viewers': 'onb.prev.too-many',
      };
      state.textContent = T('onb.prev.failed') + ': ' +
        (reasons[m.reason] ? T(reasons[m.reason])
                           : m.reason || T('onb.prev.server-error'));
    }
  };

  ws.onerror = () => { state.textContent = T('onb.prev.failed') + ': ' + T('onb.prev.refused'); };
  ws.onclose = () => {
    if (previewPc && previewPc.connectionState === 'connected') return;
    state.textContent = T('onb.prev.failed') + ': ' + T('onb.prev.closed');
  };
}

el('ob-recheck').addEventListener('click', openPreview);

// checkRow updates one of step 1's check lines.
// **The link is offered only where it can work.** `ms-settings:` is a Windows
// shell address: on the machine the browser hands it to the Settings app, and
// from a phone it leads nowhere at all. A dead affordance is worse than none
// **on this screen in particular**, because the screen is diagnosing a fault and
// the reader has no way to tell a link that does nothing from a permission that
// did not take.
//
// The test is the origin, and it is sound because this page is served by the
// monitor itself: a loopback host means the browser is running on the machine
// the permission belongs to. Opening the path by its home address from that same
// machine answers no, which costs the button and keeps the sentence — the
// sentence names the page in words and is true everywhere, which is why it says
// where the switch is rather than leaving that to the link.
const onTheMonitorsMachine = () =>
  ['localhost', '127.0.0.1', '[::1]', '::1'].includes(location.hostname);

// checkRow draws one of the two checks: the badge, the name, the detail, and —
// when there is something to grant and somebody who can grant it — the command
// that opens the Windows page.
// waitRow puts a row back to the state its markup is born in: the neutral badge
// and "checking".
//
// **It exists because the two the rows can show are both claims.** A device
// being opened is neither good nor bad, and packaged that wait is Windows
// asking the person standing at this very screen whether the program may use
// the camera and the microphone — so a row shouting `!` at them is the page
// answering a question that is still on their screen. The state was already
// designed and already in the markup; what was missing was going back to it.
function waitRow(id, title) {
  const li = el(id);
  if (!li) return;
  const badge = li.querySelector('.badge');
  badge.className = 'badge wait';
  badge.innerHTML = '&hellip;';
  li.querySelector('b').textContent = title;
  li.querySelector('small').textContent = T('onb.s1.checking');
  const go = li.querySelector('.btn');
  if (go) go.hidden = true;
}

function checkRow(id, good, title, detail, settings) {
  const li = el(id);
  if (!li) return;
  const badge = li.querySelector('.badge');
  badge.className = 'badge ' + (good ? 'ok' : 'warn');
  badge.innerHTML = good ? '&#10003;' : '!';
  li.querySelector('b').textContent = title;
  li.querySelector('small').textContent = detail;

  const go = li.querySelector('.btn');
  if (!go) return;
  const show = !!settings && onTheMonitorsMachine();
  // **The address is written before the button is shown**, never after: a
  // button revealed with `href="#"` for one frame is a button that can be
  // pressed and do nothing.
  if (show) go.href = settings;
  go.hidden = !show;
}

// fromDbfs brings the level into 0..1 on a scale that makes sense to the eye.
//
// **The scale is logarithmic and the floor is -60 dBFS, not -96.** Digital
// silence sits at -96, but the noise of an empty room is already around -70:
// measuring all the way down would leave the bars visibly lit on nothing, that
// is, they would say "I hear" when there is nothing to hear — which is exactly
// the fault this step exists to find.
function fromDbfs(db) {
  if (typeof db !== 'number' || !isFinite(db)) return 0;
  const v = (db + 60) / 60;
  return Math.max(0, Math.min(1, v));
}

// ---------------------------------------------------------------- level meter

// The meter feeds from the **received stream**, not from the server's state.
//
// The two measurements say different things and both are needed. The server's is
// what the microphone hears, and it is the answer to the step's question; this
// one is what **reaches the browser** after capture, Opus compression and the
// network, that is, the only one that proves the whole chain. If the microphone
// hears and nothing arrives here, the fault is in between — and without this
// measurement there would be no way of knowing.
//
// The rate is the other reason: `/api/status` answers once a second, and a level
// refreshed once a second is not a meter, it is a series of photographs. Here
// the speed **is** the information.
let audioCtx = null;
let vuActive = false;
let vuPeak = 0;
let vuPeakSince = 0;

const VU_FLOOR = -60;  // dBFS: below this it is the empty room, not digital silence
const VU_LOUD = 0.72;  // above: amber
const VU_PEAK = 0.92;  // above: red, it is clipping

function writeVu(level, db) {
  const fill = el('ob-vu-fill');
  const peak = el('ob-vu-peak');
  const read = el('ob-vu-read');
  const vu = el('ob-vu');
  if (!fill) return;

  const pct = Math.max(0, Math.min(1, level)) * 100;
  fill.style.width = pct.toFixed(1) + '%';
  fill.className = 'vu-fill' + (level >= VU_PEAK ? ' peak' : level >= VU_LOUD ? ' loud' : '');

  // The peak rises at once and falls slowly: that is what tells a meter from a
  // bar. A bark lasts half a second and the eye has no time to see it rise; the
  // mark stays there and declares it.
  const now = performance.now();
  if (level >= vuPeak) {
    vuPeak = level;
    vuPeakSince = now;
  } else if (now - vuPeakSince > 900) {
    vuPeak = Math.max(level, vuPeak - 0.012);
  }
  peak.style.left = 'calc(' + (Math.max(0, Math.min(1, vuPeak)) * 100).toFixed(1) + '% - 1px)';

  read.textContent = db <= VU_FLOOR ? '≤ −60 dB' : db.toFixed(0).replace('-', '−') + ' dB';
  vu.setAttribute('aria-valuenow', Math.max(VU_FLOOR, Math.round(db)));
  vu.setAttribute('aria-valuetext', T(
    level < 0.02 ? 'onb.vu.silence' : level >= VU_PEAK ? 'onb.vu.very-loud' :
    level >= VU_LOUD ? 'onb.vu.loud' : 'onb.vu.normal'));
}

// attachVu hooks the analyser to the received stream.
//
// The audio is **not connected to the destination**: the track is there even
// though the video is muted, and connecting it would make it audible — that is,
// a monitor playing the room to itself, with a risk of feedback given that the
// microphone is in the same room.
function attachVu(stream) {
  if (!stream || stream.getAudioTracks().length === 0) return;
  try {
    if (!audioCtx) {
      audioCtx = new (window.AudioContext || window.webkitAudioContext)();
    }
    // The context is born suspended until there has been a user gesture. Here
    // there must have been one — step 1 is reached by pressing a button — but
    // the request is made all the same, because "must have" is not a guarantee:
    // whoever reopens the page on the #p1 anchor has pressed nothing.
    if (audioCtx.state === 'suspended') audioCtx.resume();

    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 512;
    audioCtx.createMediaStreamSource(stream).connect(analyser);

    const buf = new Float32Array(analyser.fftSize);
    vuActive = true;
    const tick = () => {
      if (!vuActive) return;
      analyser.getFloatTimeDomainData(buf);
      let sum = 0;
      for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
      const rms = Math.sqrt(sum / buf.length);
      const db = rms > 0 ? 20 * Math.log10(rms) : -100;
      writeVu(fromDbfs(db), db);
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  } catch (err) {
    // Without Web Audio the server's measurement at one hertz is left: less
    // alive, but it still answers the step's question. What is lost is the
    // fluidity, not the proof.
    console.warn('level meter from the stream unavailable', err);
  }
}

// ------------------------------------------------------------ 2 and 3 · choice

async function markDone() {
  try { await fetch('/api/onboarding/done', {method: 'POST'}); } catch (err) { /* does not block */ }
}

// "At home is enough for me" is a legitimate outcome, not an abandonment: the
// path is finished and has to be marked as such, otherwise it would open again
// at the next start.
el('home-only').addEventListener('click', async () => {
  homeOnly = true;
  await markDone();
  show(STEP_READY);
});

el('skip-funnel').addEventListener('click', async () => {
  homeOnly = true;
  await markDone();
  show(STEP_READY);
});

el('failed-home-only').addEventListener('click', async () => {
  homeOnly = true;
  await markDone();
  show(STEP_READY);
});

// Switches on access from outside. It is the only point of the path that changes
// the configuration besides the password, and also the only one that cannot be
// undone from here: switching it off is done from the icon by the clock.
//
// **It sits on the fork's button**, which used to only navigate. The choice and
// the switching on are the same decision, and asking for it twice was a step
// that existed only because there was an explanation in between.
async function enableOutside() {
  const b = el('enable-outside');
  b.disabled = true;
  try {
    const res = await fetch('/api/remote/enable', {method: 'POST'});
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      showFailure(T('onb.enable-failed'), TErr(body.error, body.retryAfter));
      return;
    }
    setRoute(OUTSIDE);
    show(3);
  } catch (err) {
    showFailure(T('onb.unreachable'), String(err));
  } finally {
    b.disabled = false;
  }
}
el('enable-outside').addEventListener('click', enableOutside);

// "How it works" opens a box, not a step.
//
// The native `<dialog>` brings with it Esc, the focus confined inside, the focus
// returned to the button that opened it and the rest of the page declared inert:
// the four things hand-made modals lack, and none of them is noticed until it is
// tried with the keyboard alone.
//
// Opening it **does not change step**, so it does not touch the anchor in the
// address and does not move the bar at the top: whoever closes it is exactly
// where they were.
const explainer = el('explainer');
document.addEventListener('click', (e) => {
  if (e.target.closest('[data-explainer]')) explainer.showModal();
});

// Trying again means passing through "Yes, from outside too" again, which is
// also the button that retries switching on: the fork is the right place, not
// the authorisation step, where there is nothing to retry.
el('failed-retry').addEventListener('click', () => show(2));

function showFailure(what, detail) {
  el('failed-what').textContent = what;
  el('failed-detail').textContent = detail || T('onb.s7.no-detail');
  show(STEP_FAILED);
}

// -------------------------------------------------------- the state commands

// The steps from 3 on are decided by the tunnel, not by the user.
//
// **The path never goes back by itself.** A phase that swings — and at startup
// it does swing, because the node connects and then asks for the certificate —
// would take the user back to a screen already passed, which reads as "something
// went wrong". We only move forward, except towards the failure, which is the
// one piece of news worth the jump backwards.
function followPhase(r) {
  if (step < 3 || homeOnly) return;

  switch (r.phase) {
    case 'needs-login':
      showAuthorise(r);
      if (step <= 3) show(3);
      break;

    // The device is there and the connection has happened: now it is up to an
    // administrator of the tailnet. It stays step 4 because it is the same point
    // of the path, but what it says is a different thing.
    case 'needs-approval':
      showApproval(r);
      if (step <= 3) show(3);
      break;

    // **Transient: nothing is touched.** During startup the phase passes through
    // here for a few seconds, and replacing the screen only to put it back as it
    // was makes it flicker.
    case 'starting':
      break;

    case 'needs-funnel':
      el('funnel-msg').textContent = TAction(r, 'onb.s4.missing-permission');
      // **With the address you are an administrator, without it you are not**,
      // and that is not a guess: `QueryFeature` returns the link that enables
      // everything only to whoever has the right to use it. The service knows,
      // from the tailnet and the role, so it is not deduced from the text.
      const admin = !!r.actionUrl;
      el('funnel-url').hidden = !admin;
      el('funnel-actions').hidden = !admin;
      el('funnel-admin').hidden = !admin;
      el('funnel-member').hidden = admin;
      el('funnel-title').textContent = T(admin ? 'onb.s4.title-admin'
                                               : 'onb.s4.title-member');
      if (admin) {
        el('funnel-url').textContent = r.actionUrl;
        el('funnel-open').href = r.actionUrl;
      }
      if (step <= 4) show(4);
      break;

    case 'certificate':
      if (step <= 5) show(5);
      break;

    case 'running':
      show(STEP_READY);
      break;

    case 'error':
      showFailure(TAction(r, 'onb.s7.failed'), r.detail);
      break;
  }
}

// The two faces of step 4: "you have to authorise" and "you have authorised,
// wait". **There is one box only: address and code live together.**
function authoriseBox() { return el('auth-url').closest('.addr'); }

function showAuthorise(r) {
  el('auth-approve').hidden = true;
  el('auth-waiting').hidden = false;
  authoriseBox().hidden = false;

  // **The address arrives after the phase, and the wait has to be said.**
  //
  // Tailscale announces `needs-login` and hands over the link a couple of
  // seconds later, sometimes more. In between, the box carried a dash and the
  // code's box was empty: a dash next to the label "address to open" reads as
  // "there is none", that is, as a fault, and that is the moment somebody closes
  // the window. Measured on a test machine, a clean two seconds between the two
  // lines — enough to see it, not enough to understand that it is coming.
  //
  // Now in place of the address it says that we are waiting, and in place of the
  // code a spinner of **the same size** turns: that half of the box does not
  // move. The other half does, and it is right that it should — measured, from
  // 202 to 260 px — because what appears is the address and its two commands,
  // that is, the thing that was being waited for.
  const waiting = !r.actionUrl;
  el('auth-qr-waiting').hidden = !waiting;
  el('auth-pending').hidden = !waiting;
  el('auth-url').hidden = waiting;
  // The commands disappear: an "Open" leading to `#` and a "Copy" that copies a
  // waiting sentence are worse than two absent buttons — they promise something
  // that is not there yet, and whoever presses them concludes it is broken.
  el('auth-open').closest('.addr-actions').hidden = waiting;
  if (waiting) {
    qrFor('auth-qr', '');
    return;
  }
  el('auth-url').textContent = r.actionUrl;
  el('auth-open').href = r.actionUrl;
  // The image is reassigned only if the address has changed: the heartbeat runs
  // every second, and reloading it every round would make it flicker.
  qrFor('auth-qr', r.actionUrl);
}

function showApproval(r) {
  // The authorisation address and its code disappear: it has already been used,
  // and leaving it there invites doing again the thing that has already worked.
  // They live in the same box, so hiding one hides both.
  authoriseBox().hidden = true;
  el('auth-waiting').hidden = true;
  el('auth-approve').hidden = false;
  el('approve-msg').textContent = TAction(r, '');
  if (r.actionUrl) {
    el('approve-url').textContent = r.actionUrl;
    el('approve-open').href = r.actionUrl;
  }
}

// The last step says what is really there, and tells the three outcomes apart:
// home only, outside working, outside open but with no way in.
function composeFinal(s) {
  // **The home address is computed by the program, and if it has none it is not
  // invented.**
  //
  // `location.origin` looks like a harmless fallback and is not. Opened on the
  // monitor's own computer it is `localhost`, that is, the one address that from
  // a phone certainly does **not** work — and that is the reason the field
  // exists. Opened from the public address it is `https://…ts.net`, so the "at
  // home" box would end up showing, and engraving into the code, the address
  // **from outside**: a code that scans, leads somewhere, and is not the one
  // promised.
  //
  // With no address we declare we have none. The paragraph below already says
  // where to find it — the icon by the clock — so nothing is lost, while a wrong
  // address costs a failed attempt on the phone.
  const home = s.localUrl || '';
  el('url-home').textContent = home || '—';
  qrFor('home-qr', home);

  const r = s.remote || {};
  const active = r.phase === 'running' && r.publicUrl;

  // The finishing line. It says what has just happened, and the two versions are
  // different because the two feats are: a camera watched at home is not the
  // same thing as a private network with a valid certificate put up by somebody
  // who did not know they were doing it.
  el('final-line').textContent = T(active ? 'onb.s6.done-out' : 'onb.s6.done-home');

  el('outside-block').hidden = !active;
  el('outside-outcome').hidden = !active;
  // The wait concerns the public address, so it appears and disappears with it:
  // to whoever stays at home it says nothing, and would be a doubt given away.
  el('wait-note').hidden = !active;
  if (active) {
    el('url-outside').textContent = r.publicUrl;
    qrFor('outside-qr', r.publicUrl);
  }

  // **A warning on the "ready" step is worth more than the rest of the page.**
  // The Funnel can be open and not get through — it is the fault where the panel
  // is green and nothing answers from outside. Declaring it here is what saves
  // the user from looking for the defect in their own network.
  const warning = el('outside-warning');
  if (active && r.warning) {
    warning.textContent = TOr('tunnel.warning.' + r.warning, 'err.unknown');
    warning.hidden = false;
  } else {
    warning.hidden = true;
  }
}

// ------------------------------------------------------------------ the pulse

// stalled shows, or removes, the line saying why the page is not refreshing.
//
// It sits at the top and is the only thing that appears outside the steps: a
// path that stops following the state and does not declare it is worse than one
// that stops with an error, because it goes on showing old information as if it
// were current.
function stalled(text) {
  let box = el('heartbeat-stopped');
  if (!box) {
    if (!text) return;
    box = document.createElement('p');
    box.id = 'heartbeat-stopped';
    box.className = 'note bad';
    box.setAttribute('role', 'status');
    progress.after(box);
  }
  box.textContent = text;
  box.hidden = text === '';
}

// One network round a second feeds everything: step 1's outcomes, the
// microphone's level, the tunnel's phase, the last step's addresses. They are
// the same question, and making four of them would mean four answers that
// contradict each other for a moment.
async function heartbeat() {
  let s;
  try {
    const res = await fetch('/api/status', {cache: 'no-store'});
    if (res.status === 401 || res.status === 403) {
      // Before signing in, the 401 is step 0's normal state and not a fault:
      // that screen is already asking for what is missing, and repeating it in
      // red would be telling it that something has broken.
      if (!signedIn) {
        stalled('');
        return;
      }
      // **A dead session used to stop the page in silence.** It really happens:
      // a password reset from the icon's menu is enough, and it closes every
      // session. From then on the heartbeat left on every round and the path
      // stayed on the last screen with nothing to say so — indistinguishable
      // from a monitor that is not answering.
      stalled(T('onb.session-expired'));
      return;
    }
    if (!res.ok) {
      stalled(T('onb.http-status', {code: res.status}));
      return;
    }
    s = await res.json();
    signedIn = true;
    stalled('');
  } catch (err) {
    stalled(T('onb.no-answer'));
    return;
  }

  if (step === 1) {
    // **`ready` is a latch and cannot say whether it works now**, which is the
    // defect this repository has already paid for once, in the alert that was
    // built on the same field: it closes on the first keyframe and never opens
    // again. So this row went green at the first frame and **stayed** green —
    // with the permission revoked under it, with the cable pulled out of it,
    // with the detail line beside it saying so and the button to Windows
    // offered underneath. Pressing "Check again" could not help: it re-reads a
    // field whose whole meaning is *it started once*.
    //
    // `measuredFps` is the live half, and it needs no new state: it is counted
    // over a five-second window that is charged against **real time**, so with
    // the frames stopped it decays towards zero on its own. `cameraDenied` is
    // the immediate half — it goes true at the refused open, without waiting
    // for the window to run down — and it is also the only one of the two that
    // says *why*.
    const camOk = !s.cameraDenied && s.measuredFps > 0 && s.resolution !== '';
    // The device is being opened: see waitRow. The refusal is not covered by
    // this — it is an answer, and it comes back with the flag already down.
    if (s.cameraOpening) waitRow('r-cam', T('onb.s1.cam'));
    else checkRow('r-cam', camOk,
          T(camOk ? 'onb.s1.cam-ok' : 'onb.s1.cam-bad'),
          camOk
            // **Two sentences and not a hole.** Some languages inflect the
            // preposition before the encoder's name, and they cannot do it on a
            // substituted value: the case with no name therefore has a sentence
            // of its own, where the article is written out.
            ? (s.encoder
                ? T('onb.s1.cam-detail', {
                    res: s.resolution,
                    fps: Math.round(s.measuredFps),
                    enc: s.encoder,
                  })
                : T('onb.s1.cam-detail-gpu', {
                    res: s.resolution,
                    fps: Math.round(s.measuredFps),
                  }))
            // **A refused permission is told apart from a camera that will
            // not open**, and this is the screen where it matters most:
            // whoever is here is setting the monitor up for the first time,
            // in front of this machine, so it is the one moment when the
            // remedy is two clicks away. The generic line would send them to
            // check a cable.
            : T(s.cameraDenied ? 'onb.s1.cam-denied-detail' : 'onb.s1.cam-bad-detail'),
          s.cameraDenied ? 'ms-settings:privacy-webcam' : '');

    // The microphone's half is already live — `microphoneActive` is the
    // capture's own answer — and the refusal is added for the reason it is
    // added above: it is immediate, where the grace that covers a planned
    // reopen is two seconds wide, and it is the one that carries a cause.
    //
    // **The mute is the third cause, and it is the one this screen is for.**
    // The generic detail sends whoever reads it to open the laptop's lid; the
    // muted one names a slider in Windows, and whoever is on this screen is
    // sitting in front of the machine that has it. The order is the tray's
    // order — refused, muted, then the silence they both produce — because two
    // places deciding which cause wins is two places that disagree.
    // **A mute is only a mute while there is a microphone.** The field describes
    // the last open, so on a device that has since gone it is yesterday's
    // answer: unguarded, this row said *Windows has the microphone muted* and
    // offered a volume slider, about a microphone that is not in the machine,
    // beside a tray icon saying it is missing. It is the chain `activeFaults`
    // already walks — refused, absent, muted — and the guard is here because
    // this is where the sentence is chosen.
    const muted = s.microphoneMuted && s.microphoneActive;
    const micOk = !s.microphoneDenied && !muted && s.microphoneActive &&
      s.micHealth !== 'digital-silence';
    if (s.microphoneOpening) waitRow('r-mic', T('onb.s1.mic'));
    else checkRow('r-mic', micOk,
          T(micOk ? 'onb.s1.mic-ok' : 'onb.s1.mic-bad'),
          T(micOk ? (s.rawAudio ? 'onb.s1.mic-raw' : 'onb.s1.mic-filtered')
                  : s.microphoneDenied ? 'onb.s1.mic-denied-detail'
                  : muted ? 'onb.s1.mic-muted-detail'
                  : 'onb.s1.mic-bad-detail'),
          s.microphoneDenied ? 'ms-settings:privacy-microphone'
            : muted ? 'ms-settings:sound' : '');

    // **The illustration's bars follow nothing any more**: they are a drawing,
    // and the reason sits next to `@keyframes wave`. The server's measurement is
    // used by the meter, and **only as a fallback**: when Web Audio works, that
    // one is more alive and proves the chain as well, not only the microphone.
    if (!vuActive) writeVu(fromDbfs(s.audioLevelDbfs), s.audioLevelDbfs);
  }

  // **An exception in here must not be able to pass unnoticed.**
  //
  // `stalled`'s comment already says it for the network: "a path that stops
  // following the state and does not declare it is worse than one that stops
  // with an error". That held for the request only, and the fault came from the
  // other half: a line that threw, the round still turning, and the tunnel's
  // part frozen forever with nothing to say so.
  //
  // We do not try to carry on: we declare. The detail goes to the console, which
  // is where whoever can read it will look.
  try {
    if (s.remote) followPhase(s.remote);
    if (step === STEP_READY) composeFinal(s);
  } catch (err) {
    stalled(T('onb.page-error'));
    console.error('status poll', err);
  }
}

// --------------------------------------------------------------------- start

// Where to begin is decided by the state, not by a fixed value.
//
// Whoever arrives without a password starts from the beginning. Whoever reopens
// the page with the path already done must not see the password form again,
// which would refuse their request: we resume from the anchor if there is one,
// otherwise from the check.
async function start() {
  // **They are two questions, and confusing them was the defect.** "Is there a
  // password?" is answered by `/api/onboarding/state`, which answers anybody;
  // "is this browser in?" is answered by `/api/status`, which answers only with
  // a session. Deducing the first from the second, whoever had a password but no
  // cookie was offered the first configuration and got back "password already
  // set" — a guided path running into an error it could have foreseen.
  let hasPassword = false;
  let inside = false;
  try {
    const st = await fetch('/api/onboarding/state', {cache: 'no-store'});
    if (st.ok) {
      const j = await st.json();
      hasPassword = !!j.hasPassword;
      alreadyDone = !!j.done;
    }
  } catch (err) { /* the first start is assumed */ }
  try {
    inside = (await fetch('/api/status', {cache: 'no-store'})).ok;
    signedIn = inside;
  } catch (err) { /* no session */ }

  if (!hasPassword && alreadyDone) {
    // This is not the first configuration: it is a reset, and saying it
    // differently avoids making people believe everything else has been lost.
    const sec = sections.find((x) => Number(x.dataset.step) === 0);
    sec.querySelector('.eyebrow').textContent = T('onb.reset.eyebrow');
    sec.querySelector('h1').textContent = T('onb.reset.title');
    sec.querySelector('.lead').textContent = T('onb.reset.lead');
    el('pw-submit').textContent = T('onb.reset.submit');
  }
  el('pw-form').hidden = hasPassword;
  el('pw-signin').hidden = !(hasPassword && !inside);
  el('pw-done').hidden = !(hasPassword && inside);

  // Without a session there is no going past step 0: the preview, the tunnel's
  // state and the rest all sit behind authentication, and showing them empty
  // would be worse than asking for the password.
  const fromAnchor = Number((location.hash.match(/^#p(\d)$/) || [])[1]);
  let first = Number.isInteger(fromAnchor) ? fromAnchor : (inside ? 1 : 0);
  if (!inside) first = 0;
  show(first);

  if (first >= 1) openPreview();
  setInterval(heartbeat, 1000);
  heartbeat();
}

// The two status lines of the drawn panel, in the panel's own sentences.
//
// **The numbers are an example and are deliberately not the real ones.** They
// could be: the heartbeat already knows how many are watching. But then the
// illustration would follow the measurement, which is how a drawing turns into
// a second instrument — the same defect the microphone illustration was cured
// of when a real meter appeared beside it. What has to be recognisable here is
// the shape of the panel, not the state of the monitor.
//
// The format strings are the tray's own, so a rewording there rewrites the
// drawing too. Only the values live here.
el('tray-line-1').textContent = T('tray.line.watching', {viewers: '1', devices: '2'});
el('tray-line-2').textContent = T('tray.line.uptime', {since: '2h14m3s'});

// The preview opens when it is needed and closes when it is not needed any more:
// keeping a WebRTC session alive for the whole path would mean occupying the
// encoder while the user reads Tailscale's page.
const watch = new MutationObserver(() => {
  if (step === 1 && !previewPc) openPreview();
  if (step !== 1 && previewPc) closePreview();
});
watch.observe(document.body, {attributes: true, subtree: true, attributeFilter: ['class']});

start();
