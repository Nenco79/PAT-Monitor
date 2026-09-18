// The viewer's WebRTC client.
//
// The server is always the one that makes the offer: here the answer is given
// and the ICE candidates are exchanged by trickle. The media arrives
// peer-to-peer, the WebSocket serves only the signalling.
'use strict';

const el = (id) => document.getElementById(id);

const video = el('video');
const dot = el('dot');
const stateLabel = el('state');
const viewersLabel = el('viewers');
const levelBar = el('level');
const meterBox = el('meter');
const gate = el('gate');
const warnBox = el('warn');
const alertBar = el('alertbar');

let ws = null;
let pc = null;
let stream = null;
let retryDelay = 1000;
let audioEnabled = false;
// **The gate is asked for at every opening, and the choice is not remembered.**
//
// It was: the intention was written to `localStorage` and on reopening the audio
// was started again on its own, so whoever came back from the recordings did not
// have to press twice. It saved a tap and it paid for it twice over.
//
// **The message went away with nobody having touched it.** `video.play()`
// resolves on an element that is already playing, so the promise came back yes
// and the gate hid itself a moment after appearing — reported exactly like
// that, as an instruction that disappears or never shows.
//
// **And the meter stayed dimmed while the room was perfectly audible.** An
// `AudioContext` built outside a gesture is born suspended, `resume()` alone
// does not wake it, and the wait for the next tap anywhere on the page is
// silent by construction: nothing on screen said a tap was wanted, so from
// outside it read as a broken level meter.
//
// So the gesture is asked for, once per opening, by the one thing that cannot
// be wrong about having happened: a press on the button. A tap on the way in
// costs less than a still meter and a notice that vanishes by itself.
// **Silence is the user's intention, not an element's state.** Keeping it in
// `video.muted` had it wiped by anybody who touched that field: the reconnection
// switched it back on, the initial tap switched it back on, and pressing before
// the tap inverted it. Whoever pressed "Silence" asked for it once and it holds
// until they take it back.
let silenced = false;
let audioCtx = null;
let mediaPath = '';
let closing = false;

// applyAudio is the only place that touches video.muted.
//
// Two conditions, and neither is negotiable: the browser does not play audio
// before a tap, and whoever has asked for silence has to have it. Going through
// one function is what stops the two facts overwriting each other along three
// different roads.
function applyAudio() {
  video.muted = silenced || !audioEnabled;
  // **The label does not change, the pill does.** What gets silenced is said by
  // the tooltip — "Silence" alone promised silence and delivered half of it, and
  // the whole sentence lives in `viewer.mute.title` — while the pill says what
  // state one is in, like the three detections and "Talk": on means the room is
  // silenced. Rewriting the label gave the one button of the bar that becomes a
  // different command when pressed, and this is the gesture made at night
  // without looking.
  el('mute').setAttribute('aria-pressed', silenced ? 'true' : 'false');
}

// ---------- visual state ----------

function setState(text, kind) {
  stateLabel.textContent = text;
  dot.className = 'dot' + (kind ? ' ' + kind : '');
}

// setViewers shows how many are watching, beside "live".
//
// **It sits in the overlay and not among the details.** The configuration path
// promises it will be seen at the top of the page, and the promise counts for
// more than where it is convenient to put it: if access from outside the house
// is easy, knowing who is watching has to be just as easy. In the "Details"
// panel, which is born closed, nobody would see it.
//
// Alone, nothing is written. "1" is the normal case for anybody who opens the
// page, and a number that never changes stops being read — the day it becomes 2
// it has to catch the eye, and it does because before there was nothing.
function setViewers(n) {
  if (typeof n !== 'number' || n < 2) {
    viewersLabel.textContent = '';
    return;
  }
  viewersLabel.textContent = TN(n, 'viewer.viewers');
}

// **The warning panel has more than one source, and the last one to write wiped
// the others.**
//
// The case that revealed it: pressing "Talk" made "You are speaking into the
// room…" appear, and an instant later it vanished. It was not the talk-back, it
// was the status heartbeat — it runs every three seconds and rewrote that panel
// **anyway**, with the filtered-audio warning or with nothing. So the line
// explaining why the room is silent lasted three seconds at most, and precisely
// while it was needed: without it, the half-duplex silence reads as a fault.
//
// It is the same shape as the alerts at the top of the page — whoever writes
// hands over the whole photograph rather than an on/off pair — applied to a
// panel that had been left out of it. Every source keeps its own message, and
// what appears is the most urgent of those switched on; when that one goes, the
// one below comes back by itself.
const warnings = new Map();
// In order of urgency. The viewer's microphone comes first because it is a fault
// of theirs, now; the talk-back before the filtered audio because that explains a
// silence that is happening, while the other is a condition that was already
// there and will still be there in a minute.
// "mic-choice" is the refusal of a microphone change: it sits straight after the
// fault of one's own microphone because it is the same species — a thing the
// viewer has just done and that did not work — and before the two that describe
// a condition.
// "recording" is of the same species as "mic-choice" — a command just given that
// did not work — and sits beside it for that reason: before the talk-back, which
// explains a silence, and well before the filtered audio, which is a condition
// that was already there and will still be there in a minute.
// "cam-choice" is the same species as "mic-choice" — a device change that was
// refused — and sits after it because in this program the audio comes first
// everywhere: a monitor that can be heard and not seen still says the child is
// crying.
const warningOrder = [
  'microphone', 'mic-choice', 'cam-choice', 'recording', 'talk', 'raw',
];

function showWarning(source, text) {
  if (text) warnings.set(source, text);
  else warnings.delete(source);

  const chosen = warningOrder.map((k) => warnings.get(k)).find(Boolean);
  if (!chosen) {
    warnBox.className = 'warnbox';
    return;
  }
  warnBox.textContent = chosen;
  warnBox.className = 'warnbox show';
}

// ---------- connection ----------

function connect() {
  if (closing) return;

  setState(T('viewer.state.connecting'));

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(`${proto}//${location.host}/ws`);

  ws.onopen = () => setState(T('viewer.state.negotiating'));

  ws.onmessage = async (ev) => {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }

    if (msg.type === 'offer') {
      await handleOffer(msg.sdp, msg.iceServers, msg.talkMid);
    } else if (msg.type === 'candidate' && pc) {
      try {
        await pc.addIceCandidate(msg.candidate);
      } catch (err) {
        // A discarded candidate is not fatal: ICE tries the others.
        console.debug('candidate ignored', err);
      }
    } else if (msg.type === 'transport') {
      // The full value is wanted by the details, so it is kept: deriving it from
      // the overlay's label — which for a direct path is deliberately empty —
      // made the "Media path" row disappear precisely in the normal case. The
      // comment below already promised this; the code did not.
      mediaPath = msg.transport;
    } else if (msg.type === 'error') {
      // **The page picks the words, from the code the server sends.** The text
      // of the Go error used to be printed here — "stream not ready yet: no
      // keyframe received" — in the middle of a vocabulary otherwise made of two
      // small words. The detail lives in the log, which is where diagnosis
      // happens.
      const reasons = {
        'not-ready': 'viewer.state.waiting-video',
        sdp: 'viewer.state.negotiation-failed',
      };
      setState(T(reasons[msg.reason] || 'viewer.state.connection-failed'), 'err');
      teardown();
      scheduleRetry();
    }
  };

  ws.onclose = () => {
    if (closing) return;
    setState(T('viewer.state.disconnected'), 'err');
    teardown();
    scheduleRetry();
  };

  ws.onerror = () => {
    // onclose arrives right afterwards anyway: the handling lives there.
  };
}

// The STUN servers arrive from the server along with the offer.
//
// Without them the browser knows only its own local network addresses: at home
// that is enough, but on a mobile network the only candidate it offers is a
// private address of the operator's and the negotiation cannot but fail. The
// monitor already uses them on its side; both need them, because each has to
// discover its own.
async function handleOffer(sdp, iceServers, mid) {
  pc = new RTCPeerConnection({iceServers: iceServers || []});
  stream = new MediaStream();

  pc.ontrack = (ev) => {
    stream.addTrack(ev.track);
    if (video.srcObject !== stream) {
      video.srcObject = stream;
    }
    // If the user had already enabled the audio, it is switched back on without
    // asking again: after a reconnection at night nobody wants to touch the
    // screen.
    if (audioEnabled) {
      applyAudio();
      video.play().catch(() => {});
      attachMeter();
      return;
    }
    // And whoever has not enabled it yet sees the gate, at this opening as at
    // the first: there is no memory of the choice, and the reason is above.
  };

  pc.onicecandidate = (ev) => {
    if (ev.candidate && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({type: 'candidate', candidate: ev.candidate.toJSON()}));
    }
  };

  pc.onconnectionstatechange = () => {
    switch (pc.connectionState) {
      case 'connected':
        setState(T('viewer.state.live'), 'live');
        retryDelay = 1000; // connection succeeded: clear the backoff
        break;
      case 'disconnected':
        setState(T('viewer.state.unstable'), 'warn');
        break;
      case 'failed':
        setState(T('viewer.state.connection-failed'), 'err');
        teardown();
        scheduleRetry();
        break;
    }
  };

  try {
    await pc.setRemoteDescription(sdp);
    minimizePlayoutBuffer();
    prepareTalk(mid);
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    ws.send(JSON.stringify({type: 'answer', sdp: pc.localDescription}));
  } catch (err) {
    setState(T('viewer.state.negotiation-failed'), 'err');
    console.error(err);
    teardown();
    scheduleRetry();
  }
}

// minimizePlayoutBuffer asks the browser to keep the playout buffer at a
// minimum.
//
// By default the browser buffers generously in order to absorb network jitter,
// which is fine for a video call but on a live monitor translates into hundreds
// of milliseconds of constant delay. jitterBufferTarget is the current API,
// playoutDelayHint the previous one: both are set because support varies between
// browsers, and where they do not exist the assignment is simply ignored.
function minimizePlayoutBuffer() {
  for (const receiver of pc.getReceivers()) {
    try {
      if ('jitterBufferTarget' in receiver) receiver.jitterBufferTarget = 0;
      if ('playoutDelayHint' in receiver) receiver.playoutDelayHint = 0;
    } catch (err) {
      console.debug('playout buffer not adjustable', err);
    }
  }
}

function teardown() {
  // **The microphone goes off with the connection.** The browser's recording
  // indicator would stay on over a track that no longer leads anywhere: the
  // viewer would see their own phone declaring that they are being listened to,
  // and would be right to be alarmed.
  stopMicrophone();
  talkTr = null;
  if (talkBtn) {
    talkBtn.hidden = true;
    talkBtn.setAttribute('aria-pressed', 'false');
    talkBtn.title = T('viewer.talk.start');
  }
  if (pc) {
    pc.onicecandidate = null;
    pc.ontrack = null;
    pc.onconnectionstatechange = null;
    try { pc.close(); } catch {}
    pc = null;
  }
  // **First the loop is stopped, then things are cleared**, and the order is the
  // whole difference. The meter's loop does not look at `pc`: it looks at
  // `audioCtx` and `stream`, which stay where they were here. Clearing and
  // nothing else, sixteen milliseconds later the still-living loop did its round
  // again, took `unknown` off and rewrote "silence" reading an orphan analyser —
  // that is, it declared a quiet room for a stream that no longer exists, which
  // is exactly the fault this state was written for. And meanwhile it ran at
  // sixty frames a second for the whole disconnection.
  detachMeter();
  // **The colour and the word are cleared too.** Left on they would say "loud"
  // about a stream that is no longer there, which is the defect of a number that
  // ages instead of measuring.
  showLevelUnknown();
}

// detachMeter closes the running loop and disconnects the source.
//
// Moving the generation is enough to stop it: the loop checks it is still the
// last one on every round. The source is disconnected because otherwise it would
// stay attached to the previous stream inside the audio context.
function detachMeter() {
  meterGen++;
  if (meterSource) {
    try { meterSource.disconnect(); } catch {}
    meterSource = null;
  }
}

function scheduleRetry() {
  if (closing) return;
  if (ws) {
    ws.onclose = null;
    try { ws.close(); } catch {}
    ws = null;
  }
  setTimeout(connect, retryDelay);
  retryDelay = Math.min(retryDelay * 2, 15000);
}

// ---------- audio ----------

// Browsers block autoplay with audio: a user gesture is needed. The video starts
// muted anyway, so the room is seen at once.
el('listen').addEventListener('click', async () => {
  audioEnabled = true;
  applyAudio();
  gate.className = 'gate hidden';
  try {
    await video.play();
  } catch (err) {
    console.warn('playback refused', err);
  }
  attachMeter();
});

el('mute').addEventListener('click', () => {
  silenced = !silenced;
  applyAudio();
});

// attachMeter shows the incoming audio level.
//
// It serves to tell that the child is making a sound even with the phone on
// silent or the volume at zero: the indicator moves anyway.
//
// The level's two thresholds, **the same as the guided path's**: there the bar
// and its reading in decibels demonstrate the microphone to whoever is
// installing, here they say what is happening in the room to whoever is watching
// at night. The measurement is the same formula on the same stream, so the
// numbers speak about the same quantity and copying them is not a convenience.
//
// **The distinction lived only in the page one looks at once.** The viewer had a
// band six pixels tall of a single colour: a level showed, a **high** level did
// not, and that is the only difference that matters at three in the morning.
const VU_LOUD = 0.72;      // beyond: amber
const VU_PEAK = 0.92;      // beyond: light — not brick, see the stylesheet
const VU_SILENCE = 0.02;

// levelState names the band, for the colour and for whoever cannot see it.
//
// The code travels nowhere and the page picks the word: they are four keys
// written out in full and not a prefix plus a code, because here there is no
// authoritative list in Go to expand them from — and a prefix without that list
// is a family nobody can check.
function levelState(level) {
  if (level >= VU_PEAK) return {className: 'peak', key: 'viewer.level.very-loud'};
  if (level >= VU_LOUD) return {className: 'loud', key: 'viewer.level.loud'};
  if (level < VU_SILENCE) return {className: '', key: 'viewer.level.silence'};
  return {className: '', key: 'viewer.level.normal'};
}

// meterGen is the meter's generation: whoever is not the last one stops.
//
// **Every reconnection creates a new `stream`**, and with it a new analyser and
// a new `requestAnimationFrame` loop. The old ones never ended: their guard was
// `if (!audioCtx || !stream)`, and `stream` is never null — it is *replaced*.
// After three reconnections there were three loops at sixty rounds a second, two
// of them reading a dead analyser and writing zeros onto the same bar. A counter
// closes them: whoever notices they are no longer the last simply leaves.
let meterGen = 0;
let meterSource = null;

// meterResumeEvery is how often the audio context is tried again,
// meterPollWhileIdle how often it is checked whether it has restarted.
//
// **Not on every frame**, neither of them: asking fifty times a second something
// that has just said no is the defect already paid for with the talk-back's
// audio output, and running at sixty rounds a second to reread a state that only
// changes on a gesture is the same waste without even the calls. The cadence of
// a retry is not dictated by whoever wants it.
const meterResumeEvery = 2000;
const meterPollWhileIdle = 250;

// resumeOnFirstGesture restarts the audio context at the first tap, anywhere on
// the page.
//
// **What is left for it is iOS's own interruption.** An AudioContext that was
// running goes to `interrupted` on a phone call, another app or the screen
// locking, and it does not come back on its own while the video element goes on
// playing: perfectly audible audio and a still meter. The case that used to
// bring it here — the return from the recordings, where the audio restarted by
// itself and the context was built outside a gesture — no longer exists, because
// the gate is asked for at every opening and the press on it *is* the gesture.
//
// So the gesture is not asked for, it is waited for: it arms once and disarms
// itself.
let gestureArmed = false;

function resumeOnFirstGesture() {
  if (gestureArmed) return;
  gestureArmed = true;
  const resume = () => {
    document.removeEventListener('pointerdown', resume, true);
    document.removeEventListener('keydown', resume, true);
    gestureArmed = false;
    if (audioCtx) audioCtx.resume().catch(() => {});
  };
  document.addEventListener('pointerdown', resume, true);
  document.addEventListener('keydown', resume, true);
}

function attachMeter() {

  if (!stream || stream.getAudioTracks().length === 0) return;
  try {
    if (!audioCtx) {
      audioCtx = new (window.AudioContext || window.webkitAudioContext)();
    }

    // The previous source is disconnected: without that it stays attached to the
    // old stream and the context goes on holding it.
    if (meterSource) {
      try { meterSource.disconnect(); } catch {}
    }
    const source = audioCtx.createMediaStreamSource(stream);
    meterSource = source;
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 512;
    // Deliberately NOT connected to the destination: the audio is already played
    // by the video element, connecting it here would make it heard twice.
    source.connect(analyser);

    const mine = ++meterGen;
    const buf = new Float32Array(analyser.fftSize);
    let resumed = 0;
    const tick = () => {
      if (!audioCtx || !stream || mine !== meterGen) return;

      // **The context's state is watched on every round, not only on
      // attaching.** A context created outside a gesture is born suspended, and
      // on iOS one that was running can go to `interrupted` — a phone call,
      // another app, the screen locking. The video element goes on playing,
      // because it does not depend on this context: perfectly audible audio and
      // a still meter.
      //
      // **And a meter that cannot measure does not draw silence.** Zero on the
      // bar is an assertion about the room — "it is quiet" — and it is exactly
      // the wrong trust in the worst place: it is the same family as the
      // microphone that delivers zeros with the page green. The dimmed track
      // says so; the words do not, and the reason is in `showLevelUnknown`.
      //
      // The remedy, though, is not declaring it, it is the tap that wakes the
      // context: that is armed here, it is tried again now and then for iOS's
      // case of restarting by itself, and it is rechecked with a timer rather
      // than at sixty frames a second.
      if (audioCtx.state !== 'running') {
        showLevelUnknown();
        resumeOnFirstGesture();
        const now = Date.now();
        if (now - resumed > meterResumeEvery) {
          resumed = now;
          audioCtx.resume().catch(() => {});
        }
        setTimeout(tick, meterPollWhileIdle);
        return;
      }
      // Compared before assigning: removing a class that is not there is a write
      // like any other, sixty times a second all night.
      if (meterBox.classList.contains('unknown')) {
        meterBox.classList.remove('unknown');
      }

      analyser.getFloatTimeDomainData(buf);
      let sum = 0;
      for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
      const rms = Math.sqrt(sum / buf.length);
      // Scaled from -60 dBFS to 0, which is the useful range for ambient sounds.
      const db = rms > 0 ? 20 * Math.log10(rms) : -100;
      const pct = Math.max(0, Math.min(100, ((db + 60) / 60) * 100));
      levelBar.style.width = pct.toFixed(0) + '%';
      const st = levelState(pct / 100);
      if (levelBar.className !== st.className) levelBar.className = st.className;
      // The number and the word are updated together: separated, one of the two
      // would describe the previous instant.
      meterBox.setAttribute('aria-valuenow', String(Math.round(Math.max(-60, db))));
      meterBox.setAttribute('aria-valuetext', T(st.key));
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  } catch (err) {
    console.warn('level meter unavailable', err);
    showLevelUnknown();
  }
}

// showLevelUnknown declares that the level is not being measured.
//
// The bar goes to zero **with a class of its own**, which the sheet draws as a
// dimmed track rather than as silence. The dashes go on the **track**, not on
// the fill: at level zero the fill is zero wide, so a class on it would not
// show — which is exactly how "I do not know" would end up resembling "silence".
// The word is carried by `aria-valuetext`, for whoever cannot see the bar.
//
// **And nothing ends up in the warning panel, and that is a line removed.**
// There used to be one — "the audio level cannot be measured on this browser" —
// written for a case imagined to be rare. It was not rare and it was not the
// browser's: it took no more than going into the recordings and coming back to
// the monitor for it to appear every time, and it accused the browser of a fault
// that was ours. It is the codec warning's family — **a prediction, and
// predictions are wrong in both directions** — with the aggravation of sending
// the reader to look for the defect where it was not. The cause has been removed
// (`resumeOnFirstGesture`); what remains when a reading is missing is the dimmed
// track, which states and does not explain.
//
// **It is written once, not on every frame.** The branch that cannot measure
// comes through here for as long as it lasts, and repainting the same state
// every time is a write like any other.
function showLevelUnknown() {
  if (meterBox.classList.contains('unknown')) return;
  levelBar.style.width = '0%';
  levelBar.className = '';
  meterBox.classList.add('unknown');
  meterBox.setAttribute('aria-valuenow', '-60');
  meterBox.setAttribute('aria-valuetext', T('viewer.level.unknown'));
}

// ---------- access from outside the house ----------

// renderRemote shows the public address or the step that is missing.
//
// The text of the Tailscale prerequisites is not written by us: it arrives from
// the control server, which knows whether the viewer can enable the feature
// themselves or has to ask whoever administers the tailnet. It can contain
// several lines, so the style preserves the line breaks.
//
// The phases' words are chosen by the page, from the code the server sends.
//
// The badge used to print `r.phase` raw, that is, the same string that was
// compared three lines below: that value did the code and the text at once, and
// it is the reason it stayed untranslated until the last. A phase the server
// added without warning here does not leave the panel empty: the code is shown,
// which is ugly and visible, rather than nothing.
//
// The tunnel's phase arrives as a code and is shown as a word: the key is
// composed from the code, so adding a phase on the server side means adding an
// entry to the catalogues and nothing else here.
const phase = (c) => TOr('viewer.phase.' + c, 'viewer.phase.unknown');

// The one outcome of the reachability check this page compares. The other two
// need no name: "verified" is simply shown, and the absence of an outcome is the
// absence of news.
const REACH_FAILED = 'unreachable';

// The line under the badge, for the outcomes that want a different one.
//
// **During the check the address is not promised to answer**, which is what the
// normal line says: the name has to be registered on the Internet and for a few
// minutes it does not open from the phone — the first time, and every time the
// monitor has been off for a while. Without saying so, those minutes read as a
// fault, and one goes looking for it in one's own network.
const OUTCOME_NOTE = {
  checking: 'viewer.remote.checking',
  unreachable: 'viewer.remote.unreachable',
};

function renderRemote(r) {
  const box = el('remote');
  if (!r || r.phase === 'off') {
    box.className = 'remote';
    qrFor(el('r-qr'), '');
    return;
  }

  // **The badge says the check's outcome when there is one, and the phase when
  // there is not.** "Active" is what the tunnel answered — certificate, ingress,
  // serve config — and for the first minutes after the start it is true and
  // useless at once, because the public name is not in DNS yet. `reach` comes
  // from a request that went out to the Internet and came back: when it is
  // there, it is the better news of the two. When it is not, we keep quiet and
  // say the phase, which is exactly what is known.
  el('r-phase').textContent = r.reach
    ? TOr('viewer.reach.' + r.reach, 'viewer.phase.' + r.phase)
    : phase(r.phase);

  const link = el('r-link');
  if (r.phase === 'running' && r.publicUrl) {
    // The key is chosen **before** the assignment, and that is not a matter of
    // style: the prose guard looks inside what is assigned to `textContent`, and
    // a server code written in there looks in every way like a sentence to it.
    el('r-action').textContent = T(OUTCOME_NOTE[r.reach] || 'viewer.remote.reachable');
    link.textContent = r.publicUrl;
    link.href = r.publicUrl;
    link.className = 'remote-link show';
  } else {
    el('r-action').textContent = TAction(r, '');
    if (r.actionUrl) {
      link.textContent = T('viewer.remote.open');
      link.href = r.actionUrl;
      link.className = 'remote-link show';
    } else {
      link.className = 'remote-link';
    }
  }

  // The code is pointed at the address **only when it changes**. This round is
  // run by the heartbeat every three seconds: reassigning the same `src` would
  // send the browser back to ask for the image, and a code that redraws itself
  // while somebody is scanning it is a code that does not read.
  qrFor(el('r-qr'), r.phase === 'running' ? r.publicUrl : '');

  // An address that does not answer from the Internet is a step to take, not a
  // state that is fine: the panel goes back to `todo` and rises out of the
  // details, which is the right direction — "it works" is not news, "it has
  // stopped working" is.
  box.className = r.phase === 'running' && r.reach !== REACH_FAILED
    ? 'remote show ok'
    : 'remote show todo';
}

// qrFor points the image at an address's code, or hides it if there is no
// address. The server draws it (`/qr`), not the page: it is the same code that
// generates the onboarding's, so there are not two implementations that can
// diverge.
function qrFor(img, url) {
  if (!img) return;
  if (!url) {
    img.hidden = true;
    img.removeAttribute('src');
    return;
  }
  const src = '/qr?u=' + encodeURIComponent(url);
  if (img.getAttribute('src') !== src) img.setAttribute('src', src);
  img.hidden = false;
}

// ---------- the server's state ----------

// The class goes on the container and not only on the details: the "away from
// home" panel depends on it too when it has nothing to ask. A second button to
// reveal it would be one more button on a bar that already has too many, and
// they are two faces of the same question — "tell me the rest".
el('details').addEventListener('click', () => {
  const open = document.querySelector('.viewer').classList.toggle('details');
  el('stats').classList.toggle('show', open);
  // **The label does not change: the arrow says the state.** `textContent` on
  // the button would have deleted both glyphs as well — this is the third time
  // this trap has come back, after "Copy" in the onboarding and "Talk" next door
  // — and "Hide" was a longer word anyway, widening the button exactly as it is
  // being pressed.
  //
  // `aria-expanded` is not an accessory: it is what chooses which of the two
  // arrows is seen, so the state is written once and the stylesheet and the
  // screen reader read the same thing.
  el('details').setAttribute('aria-expanded', open ? 'true' : 'false');
  // The two device lists are asked for here, which is the only moment somebody
  // is about to look at them. They are asked for again on every opening rather
  // than once: a USB stick or a webcam is plugged in while the monitor runs, and
  // an old list offers a choice that is no longer there.
  if (open) { micPick.load(); camPick.load(); scrollToDetails(); }
});

// scrollToDetails brings the panel that has just opened into view.
//
// **On a phone the panel opens below the fold.** The bar sits at the foot of
// the viewport, so everything the button reveals is under it: pressing it
// appeared to do nothing but turn an arrow round. The sheet is the other half
// of this — in that mode the picture stops giving up its height to the panel,
// so the page is now longer than the screen by design, and something has to
// take the reader down it.
//
// **It scrolls and does not lock**: the video stays one flick up, which is the
// whole reason for scrolling rather than covering it with a sheet.
//
// **`block: 'start'` and not `'end'`, and a short screen is why.** Aligning the
// panel's bottom with the bottom of the window is the same thing as aligning
// its top while the whole panel fits — measured at 390x844, both land on the
// last row — and stops being the same thing as soon as it does not: the panel
// is 703 px tall, so on a 360x640 screen `'end'` scrolled **63 px past its
// top**, putting the two boxes that were moved to the head of the list, the
// only two rows anybody presses, above the edge of the screen. From the top the
// reader gets the beginning of the panel and scrolls on for the rest, which is
// what a list is for.
//
// And it is `scrollIntoView` and not `scrollTo(scrollHeight)`: the number would
// be read before the browser has laid out a panel that has just been made
// visible, while the element knows where it is by the time it is asked. Where
// somebody has asked for less motion the same journey is made in one step —
// `prefers-reduced-motion` is a request about movement, not about arriving.
function scrollToDetails() {
  const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  el('stats').scrollIntoView({behavior: still ? 'auto' : 'smooth', block: 'start'});
}

// Full screen: the picture takes the screen and the bar becomes a rail of
// glyphs. The stylesheet does all of it — see "full screen" there — and what is
// left here is the two halves that cannot live in a sheet.
//
// **The mode is ours, the API is an enhancement.** The class is what makes the
// mode, and it goes on whether or not `requestFullscreen` was granted: on an
// iPhone before iOS 17.2 an element cannot go full screen at all, and the only
// thing that can is the video's native player, which would take the page and the
// rail with it. So the request is made and its refusal is **not** an error —
// weigh the effect, not the answer — and what the user loses where it is refused
// is the browser's own chrome going away, not the mode.
//
// **The orientation is not asked of anything.** Landscape puts the rail on the
// flank and portrait under the picture, and what decides is the CSS
// `orientation` media query: no lock, no permission, no branch per operating
// system. `screen.orientation.lock()` does not exist on Safari iOS, so anything
// built on it would have been built for Android alone.
const fsBtn = el('fullscreen');

// `fullscreenElement` and not a variable of ours: the browser owns this state
// and there are three ways out of it we do not see — Esc, the swipe down, the
// tab going to the background. A flag of ours would drift from it, and a rail
// still on screen with the mode gone is a bar in the wrong shape.
function paintFullscreen(on) {
  const v = document.querySelector('.viewer');
  v.classList.toggle('fs', on);
  // **The details close on the way in, and that is the real fix.** The mode does
  // not show them, and leaving the class on meant two rules fighting over the
  // same panel: `.stats.show` and `.viewer.details .remote.show.ok` both beat
  // the hide, and what came out was the details grid across the top of the
  // picture. The stylesheet refuses them anyway — one state is better than a
  // stronger selector, but the net downstream must not depend on this line
  // having run.
  if (on && v.classList.contains('details')) {
    v.classList.remove('details');
    el('stats').classList.remove('show');
    el('details').setAttribute('aria-expanded', 'false');
  }
  // **The label does not change and the glyph does.** It is the bar's rule and
  // "Details"'s mechanism: `aria-pressed` chooses which of the two drawings is
  // seen, in the stylesheet, so the state is written once and the screen reader
  // reads the same thing. `textContent` here would delete both glyphs — the trap
  // this page has now paid for four times.
  fsBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
  // The title is the one thing that has to say **which way** the press goes,
  // because the word is clipped on the rail and the glyph is all that is left.
  fsBtn.title = T(on ? 'viewer.fullscreen.exit' : 'viewer.fullscreen.enter');
}

fsBtn.addEventListener('click', async () => {
  const v = document.querySelector('.viewer');
  const on = !v.classList.contains('fs');
  if (on) {
    // Asked for, and a refusal changes nothing: see above.
    try { await v.requestFullscreen(); } catch {}
  } else if (document.fullscreenElement) {
    try { await document.exitFullscreen(); } catch {}
  }
  paintFullscreen(on);
});

// The other three ways out. Where the API is absent this never fires and the
// click above is the only road, which is exactly the degradation wanted.
document.addEventListener('fullscreenchange', () => {
  if (!document.fullscreenElement) paintFullscreen(false);
});

el('logout').addEventListener('click', async () => {
  closing = true;
  try { await fetch('/api/logout', {method: 'POST'}); } catch {}
  location.href = '/login';
});

// The microphone's health arrives as a code (`ok`, `digital-silence`, `quiet`),
// not as a sentence: whoever has to decide something compares it, and the words
// are chosen here, which is the only place that knows what language it is
// speaking.
const micHealth = (c) => TOr('viewer.mic.health.' + c, 'viewer.mic.health.unknown');

// Whether the audio is going through the OEM's filters. **It has three answers
// and `rawAudio` is a boolean**, which is the whole reason this function exists:
// the field says whether the *last open* obtained raw mode, so its `false` means
// both "raw was refused" and "nothing has ever been opened", and its `true`
// survives the microphone being unplugged. `microphoneActive` is what separates
// the three, and it is the same guard the guided path makes at `micOk` and the
// tray makes by putting `mic-missing` in a case above `mic-filtered`.
//
// With no microphone there is no answer to give: a page that says "audio
// filtered by the system" over an absence describes a stream that does not
// exist, and sends whoever reads it looking for a filter to switch off. Here it
// was the only warning switched on, so it also won the box by default — see
// warningOrder, where `microphone` is the *viewer's* microphone and not this
// one.
function rawAudioState(s) {
  if (!s.microphoneActive) return 'unknown';
  return s.rawAudio ? 'raw' : 'filtered';
}

// What the details row says for each of the three. **The third has no sentence
// and that is the point**: "unfiltered audio: no" is a claim about a stream, so
// where there is no stream the row writes the dash this panel writes wherever
// there is nothing to say. The absence is stated once, by the box above and by
// the `mic-missing` alert.
const rawSays = {raw: 'viewer.yes', filtered: 'viewer.no', unknown: ''};

// ---------- which microphone, which camera ----------
//
// **A box says which device is capturing, not which one is written in the
// file.** They are two things that diverge in the case that matters: the chosen
// microphone is unplugged and the capture falls back on what is there, the
// chosen camera is unplugged and another room appears — and showing the choice
// alone would mean declaring a device that is not the one working.
//
// The list is asked for when the details open and not with the heartbeat:
// enumerating costs the monitor a thread through COM, and paying it every three
// seconds from every open page for a box almost nobody looks at is the kind of
// cost that goes unnoticed until it is summed over a night.
//
// **It is one mechanism used twice, and that is a decision.** The microphone's
// box came first and cost half a dozen defects — the half-second debounce, the
// arrow, the guard on the open menu, the id taken at the moment of choice — and
// every one of them applies unchanged to the camera. Copied, the second box
// would have started from the first one's fixes and then drifted from its next
// one, which on this project is not a hypothesis: it is what `--t-h1` did in two
// stylesheets. What is **not** shared is what really differs — which fields of
// the state the box reads, and the line beside it.

let lastState = null;    // to redraw as soon as a list arrives, without waiting

// `:open` says whether the menu is open, and it exists only with the
// customisable form: it is asked once, because `matches` with an unknown
// selector **throws** instead of answering no.
const canTellOpen = (() => {
  try { return CSS.supports('selector(:open)'); } catch { return false; }
})();
// **The fallback on the class expires, because that class can get stuck.**
//
// Where `:open` exists the question is exact and it decides. Where it does not,
// the class remains, which the page toggles on `mousedown` and switches off on
// `change` or `blur`: opening the menu and rechoosing the **already selected**
// entry brings neither — a `<select>` does not emit `change` for a choice that
// changes nothing, and the focus stays on the control — so the class would stay
// on and the box would stop realigning itself with the state. It is the same
// defect as the guard on the focus, by another road.
//
// Fifteen seconds is far more than a menu stays open and far less than the time
// in which a still box becomes a lie. It does not fix the arrow, which in the
// worst case stays turned up until it is pressed again: that is a glyph, this is
// what the monitor is saying.
const OPEN_MAX_MS = 15000;

// makePicker builds one of the two boxes.
//
// The four things it takes and does not deduce: `chosen` and `openName`, the
// two fields of the state that differ; `say`, the line beside the box, which
// the microphone composes from two fields while the camera is handed it ready
// by the capture — see `Status.CameraFallback`; and the two catalogue keys for
// the entries that are not devices.
//
// **The elements and the warning arrive already resolved, and that is not a
// detail.** Passing the ids and the panel's source as strings reads better and
// puts both out of reach of their guards: one looks for `el('...')` and the
// other for `showWarning('...')`, and those literal forms are the only ones
// they can see. **A generalisation that moves a value out of the shape a guard
// reads switches that guard off, silently and greenly** — so what a guard reads
// stays at the call site, and what comes in here is the result.
function makePicker(o) {
  const {sel, wrap, line} = o;
  let list = null;        // null until asked for: not an empty list
  let signature = '';     // what is drawn now, so as not to redraw the same
  let warnTimer = null;   // the timer that switches the refusal off
  let command = null;     // the timer that sends the choice once it has settled
  let openedAt = 0;

  const menuIsOpen = () => canTellOpen
    ? sel.matches(':open')
    : wrap.classList.contains('open') && Date.now() - openedAt < OPEN_MAX_MS;

  async function load() {
    try {
      const res = await fetch(o.listUrl);
      if (!res.ok) return;
      const b = await res.json();
      list = Array.isArray(b.devices) ? b.devices : [];
      signature = '';
      if (lastState) paint(lastState);
    } catch (err) {
      // With no list the box stays as it was: it says the device in use and
      // cannot be pressed, which is the same thing whoever has not opened the
      // details yet sees.
      console.warn(o.what + ' list unavailable', err);
    }
  }

  function paint(s) {
    // **Beside the box is the line that says what is really capturing, and they
    // are two different things.**
    //
    // The box is the *command*: it carries the choice, that is the line in the
    // file, because that is what pressing it changes. What is **open** can be
    // another device — the chosen one is unplugged and something else gets
    // opened — and then the box on its own would name a device that is not
    // working.
    //
    // The case is not rare and does not close itself quickly: since an absent
    // device makes the capture fall back rather than fail, plugging it in again
    // brings the monitor back to it at the next reopen, not at once. In the
    // meantime the status carries the two facts separately, and this line is
    // where their divergence shows: without it they left the server separate
    // and were reunited into a lie on the screen.
    //
    // It is rewritten only when it changes — the heartbeat runs every three
    // seconds, and a line rewritten sixty times a minute is the defect already
    // paid for by the level meter on the warning panel.
    // **The tone belongs to the message, not to the box.** "The microphone is
    // missing" is a fault and "another camera is being watched" is a thing to
    // know — they are the two levels the alerts already use — so the same
    // fallback sentence must not come out brick on one box and amber on the
    // other. It used to be the class of the element, that is one colour per box.
    const said = o.say(s);
    if (said.text !== line.textContent) line.textContent = said.text;
    const cls = said.text ? 'pick-state ' + said.tone : 'pick-state';
    if (cls !== line.className) line.className = cls;
    line.hidden = said.text === '';

    // **It is not redrawn while the menu is open.** The heartbeat runs every
    // three seconds, and remaking the entries under the finger is the defect
    // already paid for with the bar that changed height while being pressed.
    //
    // The guard was on the **focus**, and it was too wide: after a choice with
    // the mouse the control keeps the focus, so the box stopped realigning
    // itself with the state for ever — and with it the `signature = ''` that was
    // meant to bring it back to what the monitor really opened when the request
    // fails. A device that was never applied stayed written there indefinitely.
    if (menuIsOpen()) return;

    // **The box shows the choice, and the first entry is a choice too.**
    //
    // It is not a device: it is the empty line in the file, that is "follow the
    // rule rather than pin somebody" — the role Windows calls default for the
    // microphone, the first usable camera for the camera. Without an entry of
    // its own there would be no way back to it after choosing a device, and the
    // empty string — which is what the monitor has just installed — would have
    // nothing to show.
    //
    // Which device that is, in that case, is said by the tooltip: the choice is
    // the rule, the name is its consequence right now.
    const chosen = o.chosen(s) || '';
    const defaultEntry = {id: '', name: T(o.defaultKey)};
    // **The ids are compared without case**, and that is not tidiness: Windows
    // hands the same camera's symbolic link back in different cases depending on
    // who is asked, so the link in the file and the one in the list can differ
    // by nothing else. `devices.Pick` matches them exactly this way, and a box
    // that did not would draw "chosen · not connected" over the camera that is
    // on screen — the display layer contradicting the layer that decided.
    const same = (a, b) => (a || '').toLowerCase() === (b || '').toLowerCase();
    let entries;
    if (list && list.length) {
      entries = [defaultEntry, ...list];
      // **A choice that is no longer among the devices is shown, not hidden.**
      //
      // The pinned device may have been unplugged: the capture falls back —
      // rightly — but the file goes on naming that one. Putting the box on the
      // default, the viewer saw a state that is not in the file, could not
      // really choose the default (an already selected entry emits no change),
      // and when the device came back the capture would have gone back to it
      // with nobody having asked. An entry of its own says what is happening and
      // leaves a way out of it.
      if (chosen !== '' && !list.some((d) => same(d.id, chosen))) {
        entries = [defaultEntry, {id: chosen, name: T(o.goneKey)}, ...list];
      }
    } else if (chosen === '') {
      entries = [defaultEntry];
    } else {
      // List not asked for yet and a choice pinned: the open device is shown,
      // which is its visible consequence.
      entries = [{id: chosen, name: o.openName(s) || '—'}];
    }

    const sig = JSON.stringify([entries, chosen]);
    if (sig === signature) return;
    signature = sig;

    // **The options are replaced, not the children.** Inside the control there
    // is also the `<button>` carrying the chosen entry: `replaceChildren` would
    // throw it away on the first round, and the name would go back to leaving
    // the box with nothing to say so.
    for (const old of [...sel.querySelectorAll('option')]) old.remove();
    sel.append(...entries.map((d) => {
      const opt = document.createElement('option');
      opt.value = d.id;
      // The name comes from Windows: it is a proper name, not a sentence to
      // translate. In full it also stays in the entry's tooltip, because in the
      // closed box it is nearly always cut.
      opt.textContent = d.name;
      opt.title = d.name;
      return opt;
    }));
    // **The value comes from the entry, not from the file.** With the two
    // spelled in different cases, assigning the file's would match no option at
    // all and the box would fall back on the first entry — that is, it would
    // show "the first available one" over a camera that is pinned.
    const picked = entries.find((d) => same(d.id, chosen));
    sel.value = picked ? picked.id : '';
    // A net: there is always an entry for the choice, but if by some road there
    // were not, a box with nothing selected would say nothing to anybody.
    if (sel.selectedIndex < 0) sel.value = '';
    sel.disabled = !(list && list.length);
    // The tooltip carries the name in full; on the entry that is a rule and not
    // a device it carries **which** device that is now, which is the one thing
    // that line does not say.
    sel.title = (sel.value === '' && o.openName(s))
      ? o.openName(s)
      : (entries.find((d) => d.id === sel.value) || {}).name || '';
  }

  // The refusal **expires by itself**, and that is not a detail: that panel
  // carries what is wrong now, and a refused change is an event — leaving it
  // there would turn it into a condition that lasts all night.
  function refused(text) {
    clearTimeout(warnTimer);
    o.warn(text);
    if (text) warnTimer = setTimeout(() => o.warn(null), 8000);
  }

  // **The arrow says what happens when pressed**, as on "Details": down when
  // the menu is closed, up when it is open. A native `<select>` does not publish
  // that state — while the menu is open the system draws it and the page
  // receives no events — so it is followed from outside: it is toggled on
  // `mousedown`, which is the gesture that opens and the one that closes again,
  // and it goes back down as soon as something is chosen or the focus is lost.
  // With Esc the menu closes without touching either, and then it is that key
  // that puts it right.
  //
  // When we cannot know, the arrow stays down, which is the true direction
  // nearly always: a still arrow in the right direction is less bad than one
  // left turned up over a closed menu.
  sel.addEventListener('mousedown', () => {
    wrap.classList.toggle('open');
    openedAt = Date.now();
  });
  sel.addEventListener('keydown', (e) => {
    if (e.altKey && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      wrap.classList.add('open');
      openedAt = Date.now();
    }
    if (e.key === 'Escape') wrap.classList.remove('open');
  });
  for (const event of ['change', 'blur']) {
    sel.addEventListener(event, () => wrap.classList.remove('open'));
  }

  // **It is sent when it has stopped changing, not on every `change`.**
  //
  // On a closed `<select>` the arrow keys change the entry **on every press**,
  // and every `change` here means rewriting `config.yaml` and closing and
  // reopening the capture: scrolling the list from the keyboard interrupted the
  // room's audio once per key. Half a second of quiet separates "I am choosing"
  // from "I have chosen", and it does not show — the box shows the entry at
  // once, what waits is the command.
  //
  // **The id is taken now, not when the timer fires.**
  //
  // In those five hundred milliseconds of quiet the heartbeat can redraw the
  // box, and `paint` puts the state's **previous** choice back into it: reading
  // the value when the timer fires would send that one, with the box showing the
  // new entry. A command that undoes the choice just made, and no error
  // anywhere.
  sel.addEventListener('change', () => {
    const id = sel.value;
    clearTimeout(command);
    command = setTimeout(() => send(id), 500);
  });

  async function send(id) {
    sel.disabled = true;
    try {
      const res = await fetch(o.setUrl, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({id}),
      });
      if (res.ok) {
        refused(null);
      } else {
        const b = await res.json().catch(() => ({}));
        refused(TErr(b.error));
        // A refusal nearly always means the list is old: somebody unplugged the
        // device between opening the details and the click.
        await load();
      }
    } catch (err) {
      console.warn(o.what + ' not changed', err);
    } finally {
      sel.disabled = false;
      // The next round redraws with what the monitor really opened, which may
      // not be what was asked for.
      signature = '';
    }
  }

  return {load, paint};
}

// The microphone's box. Its fallback is **derived** from two fields, because
// what falls back is WASAPI and the capture only reports the endpoint it got:
// two ids that differ mean the chosen one was not there.
const micPick = makePicker({
  sel: el('s-mic'), wrap: el('s-mic-wrap'), line: el('s-mic-state'),
  listUrl: '/api/microphones', setUrl: '/api/microphone',
  warn: (text) => showWarning('mic-choice', text), what: 'microphone',
  defaultKey: 'viewer.stats.microphone-default',
  goneKey: 'viewer.stats.microphone-gone',
  chosen: (s) => s.microphoneChosen,
  openName: (s) => s.microphone,
  // Three states and one line: nobody is capturing, another one is capturing,
  // or there is nothing to say.
  say: (s) => {
    if (!s.microphoneActive) {
      return {text: T('viewer.stats.microphone-absent'), tone: 'fault'};
    }
    const fallback = !!s.microphoneChosen && !!s.microphoneId &&
      s.microphoneId !== s.microphoneChosen;
    if (!fallback) return {text: '', tone: ''};
    return {
      text: T('viewer.stats.microphone-other', {name: s.microphone || '—'}),
      tone: 'notice',
    };
  },
});

// The camera's box. **Its fallback is not derived**, and that is the one place
// the two boxes differ in substance: the chosen link comes out of a file and the
// open one out of Media Foundation's enumeration, and Windows gives the same
// symbolic link back in different cases depending on who is asked — so the
// comparison the microphone makes would declare another camera about the right
// one. The capture matches them once and hands over the outcome.
//
// **And there is no "nobody is capturing" state here.** A camera that delivers
// nothing is `capture-stopped`, which is an alert at the top of the page and a
// picture that has visibly stopped: saying it a third time in the details would
// add nothing, while the thing that cannot be seen by looking is that the room
// on screen is not the room that was chosen.
const camPick = makePicker({
  sel: el('s-cam'), wrap: el('s-cam-wrap'), line: el('s-cam-state'),
  listUrl: '/api/cameras', setUrl: '/api/camera',
  warn: (text) => showWarning('cam-choice', text), what: 'camera',
  defaultKey: 'viewer.stats.camera-default',
  goneKey: 'viewer.stats.camera-gone',
  chosen: (s) => s.cameraChosen,
  openName: (s) => s.camera,
  say: (s) => (s.cameraFallback
    ? {text: T('viewer.stats.camera-other', {name: s.camera || '—'}), tone: 'notice'}
    : {text: '', tone: ''}),
});

// ---------- what to listen for ----------
//
// The two toggles live in the monitor's configuration, not in the browser:
// "there is a dog in this room" is a fact of the installation, and whoever opens
// the page from a second phone has to find the same answer. Here they are drawn
// and changed, and the real state always arrives from /api/status.
//
// A list and not three variables: adding a fourth sensor one day must not mean
// touching four places and forgetting one.
const DETECT = [
  { btn: el('d-cry'), key: 'cry', state: 'detectCry' },
  { btn: el('d-bark'), key: 'bark', state: 'detectBark' },
  { btn: el('d-motion'), key: 'motion', state: 'detectMotion' },
];

// **Until the real state has arrived, the buttons say nothing.** They started
// from "off" written in the markup, and whoever pressed one in the first seconds
// was asking to switch on a thing that was already on: the answer brought both
// back on, and from outside it read as "I press one and both come on". The
// defect was not in the command, it was in showing a state before knowing it —
// the same shape as the "you have not got in yet" that announced itself as an
// expired session.
function paintDetect(s) {
  for (const d of DETECT) {
    d.btn.setAttribute('aria-pressed', s[d.state] ? 'true' : 'false');
    d.btn.disabled = false;
  }
}

// The button goes dead while the request is in flight, and it does **not**
// colour itself: the colour is decided by the answer. Anticipating it would show
// as done something the monitor might not have saved.
async function toggleDetect(d) {
  const next = d.btn.getAttribute('aria-pressed') !== 'true';
  for (const x of DETECT) x.btn.disabled = true;
  try {
    const res = await fetch('/api/detect', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ [d.key]: next }),
    });
    if (res.ok) {
      const got = await res.json();
      paintDetect({ detectCry: got.cry, detectBark: got.bark, detectMotion: got.motion });
    }
  } catch (err) {
    console.warn('detector not changed', err);
  } finally {
    // They come back only if they have a colour: a failed answer must not leave
    // pressable buttons that do not know what they are showing.
    const known = DETECT[0].btn.hasAttribute('aria-pressed');
    for (const x of DETECT) x.btn.disabled = !known;
  }
}

for (const d of DETECT) d.btn.addEventListener('click', () => toggleDetect(d));

// ---------- recording by hand ----------
//
// **The pre-roll is already running**, so nothing is switched on here: the
// monitor is asked to hand over the seconds it holds in the ring and to collect
// the next ones, exactly as movement in the room would make it do. The clip
// lasts as long as an event's, and it is born **kept** — a person asked for it —
// until somebody releases it from the recordings page.
//
// **The colour is not decided by the press, it is decided by the monitor.** That
// state belongs to the room and not to the browser: it lasts ten seconds, it
// survives the page closing, and whoever opens a second phone with the clip
// already running has to see it. So it arrives from /api/status like the three
// toggles, and that is also why a successful answer colours at once: waiting for
// the heartbeat would mean up to three seconds of a dead button over a clip that
// is running.
const recordBtn = el('record');

// recording remembers what we are showing, so that the tooltip and the pill are
// rewritten only when something changes: the heartbeat runs every three seconds,
// and rewriting an attribute forever is the way not to notice that one of the
// two had fallen behind.
let recording = null;

function paintRecord(active) {
  if (recording === active) return;
  recording = active;
  recordBtn.setAttribute('aria-pressed', active ? 'true' : 'false');
  // **The label does not move, the tooltip does**: it is the rule of the bar — a
  // text that changes under the finger reads as a different command appearing in
  // place of the previous one. The tooltip instead says what happens on a press,
  // and pressing now makes the clip longer.
  recordBtn.title = T(active ? 'viewer.record.more' : 'viewer.record.title');
}

// The notice's timer: one only, so that two presses close together do not leave
// behind a line that goes out after the right one.
let recordNotice = null;

async function askForClip() {
  recordBtn.disabled = true;
  try {
    const res = await fetch('/api/record', {method: 'POST'});
    if (res.status === 401) {
      location.href = '/login';
      return;
    }
    if (res.ok) {
      showWarning('recording', null);
      paintRecord(true);
      return;
    }
    // **The refusal is shown to whoever pressed.** There is only one and it
    // passes by itself — the ring is empty until the first keyframe arrives,
    // after startup or after the capture restarts — but a command that does
    // nothing and does not say so is a knob that moves nothing.
    const body = await res.json().catch(() => ({}));
    showWarning('recording', TErr(body.error));
    // **And it goes away by itself.** It is not a state anybody can contradict:
    // it is a thing that has just happened, and a line left on forever ends up
    // covering a real warning.
    clearTimeout(recordNotice);
    recordNotice = setTimeout(() => showWarning('recording', null), 6000);
  } catch (err) {
    // The monitor is not answering: the heartbeat will say so, and it touches
    // the whole page.
    console.warn('clip not requested', err);
  } finally {
    recordBtn.disabled = false;
  }
}

recordBtn.addEventListener('click', askForClip);

// ---------- talking into the room ----------
//
// **The place for the voice is laid at negotiation time, not when the button is
// pressed.** The monitor already announces in the offer an m-line waiting for
// our microphone: here it is enough to declare it `sendonly` before answering,
// and from then on attaching the microphone is a `replaceTrack` that
// renegotiates nothing. Making it appear on demand would mean a renegotiation
// inside the user's gesture, with its round of ICE, precisely when somebody is
// in a hurry to speak.
//
// Which of the two audio m-lines it is, the server says with `talkMid`: deducing
// it from the order or from the direction works until somebody touches the
// offer, and getting it wrong would attach the microphone to the wrong track in
// silence.
const talkBtn = el('talk');
let talkTr = null;      // the transceiver to send the voice on
let talkStream = null;  // the microphone, while it is on
let talkTimer = null;

// The monitor closes an over-long turn by itself; the page stops a little
// earlier, so that what stops it is whoever pressed and not a deadline they did
// not see.
const TALK_MAX_MS = 110 * 1000;

function prepareTalk(mid) {
  talkTr = null;
  if (!mid) {
    talkBtn.hidden = true;
    return;
  }
  const tr = pc.getTransceivers().find((t) => t.mid === mid);
  if (!tr) {
    talkBtn.hidden = true;
    return;
  }
  // Without this line the m-line is negotiated `inactive` and `replaceTrack`
  // sends nothing: the button lights up and nothing is heard in the room.
  tr.direction = 'sendonly';
  talkTr = tr;
  talkBtn.hidden = false;
  talkBtn.disabled = false;
  talkBtn.setAttribute('aria-pressed', 'false');
}

// **The label does not change, and that is not an oversight.** It used to say
// "Stop talking" while talking, and it was the longest word in the bar:
// measured, at 360 and 390 px it pushed the bar to **three rows** — 106 px
// becoming 154. That is, pressing the button moved everything under the finger
// by forty-eight pixels, at the moment of greatest hurry.
//
// Removing it loses nothing, because three things already say the state: the lit
// pill, `aria-pressed` for whoever uses a screen reader, and the line below that
// writes it out. It is also what the three toggles beside it do — "Crying" stays
// "Crying" — so the button now behaves like its siblings instead of like a
// command disguised as a switch. What changes is the `title`, which takes up no
// room.
function paintTalk(active) {
  talkBtn.setAttribute('aria-pressed', active ? 'true' : 'false');
  talkBtn.title = T(active ? 'viewer.talk.stop' : 'viewer.talk.start');
  // **While somebody is talking the room cannot be heard, and that has to be
  // said.** It is the half duplex: if the monitor kept sending the audio, the
  // microphone would pick up the loudspeakers and the speaker would hear
  // themselves come back. Without this line the sudden silence reads as a fault
  // in the audio.
  showWarning('talk', active
    ? T('viewer.talk.speaking')
    : null);
}

async function startTalk() {
  if (!talkTr) return;
  talkBtn.disabled = true;
  try {
    // The echo is cancelled by the browser on its own side: it is the only one
    // of the two that hears both the microphone and the loudspeaker of whoever
    // is talking.
    talkStream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
    await talkTr.sender.replaceTrack(talkStream.getAudioTracks()[0]);
    paintTalk(true);
    talkTimer = setTimeout(stopTalk, TALK_MAX_MS);
  } catch (err) {
    console.warn('microphone unavailable', err);
    stopMicrophone();
    paintTalk(false);
    showWarning('microphone', microphoneError(err));
  } finally {
    talkBtn.disabled = false;
  }
}

// The reason is said, instead of leaving a button that does nothing. They are
// the same three cases as the microphone everywhere: permission denied, no
// device, or the page is not on a secure connection.
function microphoneError(err) {
  const name = (err && err.name) || '';
  if (!window.isSecureContext) {
    return T('viewer.mic.insecure');
  }
  if (name === 'NotAllowedError' || name === 'SecurityError') {
    return T('viewer.mic.denied');
  }
  if (name === 'NotFoundError' || name === 'OverconstrainedError') {
    return T('viewer.mic.none');
  }
  // The error's name is an identifier of the browser and is not translated: if
  // there is one it is shown in brackets, and they are two different sentences
  // rather than a concatenation — in another language the bracket may not come
  // last.
  return name ? T('viewer.mic.unavailable-named', {name: name})
              : T('viewer.mic.unavailable');
}

function stopMicrophone() {
  if (talkTimer) {
    clearTimeout(talkTimer);
    talkTimer = null;
  }
  if (talkStream) {
    for (const t of talkStream.getTracks()) t.stop();
    talkStream = null;
  }
}

async function stopTalk() {
  stopMicrophone();
  // **First the track is detached, then it is painted.** Leaving it attached
  // would keep the monitor in half duplex — that is, the room mute — with the
  // button declaring it has stopped.
  try {
    if (talkTr && talkTr.sender) await talkTr.sender.replaceTrack(null);
  } catch (err) {
    console.warn('microphone not released', err);
  }
  paintTalk(false);
}

talkBtn.addEventListener('click', () => {
  if (talkBtn.getAttribute('aria-pressed') === 'true') stopTalk();
  else startTalk();
});

// **The green button has to be contradictable by the monitor.**
//
// Whoever presses while the turn already belongs to somebody else would send
// packets that the monitor drops: green button, no sound in the room, and no way
// of noticing. The same happens if the voice does not arrive at all. The two are
// cured the same way — try again shortly — so there is no need to tell them
// apart: it is enough to ask the monitor whether it is hearing anybody, and to
// believe it rather than one's own button.
//
// Two readings and not one: the state is read every three seconds and the first
// can fall before the first packet has arrived.
let talkUnconfirmed = 0;
function checkTalk(busy) {
  if (talkBtn.getAttribute('aria-pressed') !== 'true') {
    talkUnconfirmed = 0;
    return;
  }
  if (busy) {
    talkUnconfirmed = 0;
    return;
  }
  if (++talkUnconfirmed >= 2) {
    stopTalk();
    showWarning('talk', T('viewer.talk.failed'));
  }
}

// ---------- warnings ----------
//
// The state carries the list of what is wrong **now**, with a stable identifier
// for each. Here we do the one thing the server cannot do: compare it with the
// previous one. A new id is a new warning, and has to be announced; a
// disappeared id is a recovery, and has to be said once and then forgotten.
//
// The words live here and not in the API, which carries codes: this is the only
// place that knows what language it is speaking.
//
// The warning codes become keys, and the words live in the catalogues. They used
// to be two dictionaries in here, and that was already the right shape — whoever
// decides compares a code, whoever draws picks the words — but the words were in
// one language only. Now the key is composed from the code: a new warning on the
// server side is two lines in the catalogues and nothing here.
const alertText = (c) => TOr('viewer.alert.' + c, 'viewer.alert.unknown');
const recoveredText = (c) => TOr('viewer.recovered.' + c, 'viewer.recovered.unknown');


let seenAlerts = new Map();   // id -> code
let recoveryTimer = null;

// sinceLabel composes "for three minutes". Under the minute nothing is written:
// "for 4 seconds" is noise, and whoever reads it has just watched it appear.
function sinceLabel(since) {
  const min = Math.floor((Date.now() - since) / 60000);
  if (min < 1) return '';
  if (min < 60) return TN(min, 'viewer.since.minutes');
  const hours = Math.floor(min / 60);
  return TN(hours, 'viewer.since.hours');
}

function showAlert(a) {
  clearTimeout(recoveryTimer);
  alertBar.innerHTML = '';
  const text = document.createElement('b');
  text.textContent = a.code ? alertText(a.code) : T('viewer.alert.unknown');
  const when = document.createElement('span');
  when.className = 'when';
  when.textContent = sinceLabel(a.since);
  alertBar.append(text, when);
  alertBar.className = 'alertbar show' + (a.level === 'fault' ? '' : ' ' + a.level);
}

// The recovery shows for a few seconds and then goes: it is good news, and good
// news left on the screen becomes part of the frame.
function showRecovery(code) {
  clearTimeout(recoveryTimer);
  alertBar.textContent = code ? recoveredText(code) : T('viewer.recovered.unknown');
  alertBar.className = 'alertbar show recovered';
  recoveryTimer = setTimeout(() => { alertBar.className = 'alertbar'; }, 8000);
}

// chime makes a two-note stroke.
//
// It is generated rather than loaded from a file, for the same reason the icon
// is drawn: one more sound to ship, to version and to keep aligned, for two sine
// waves.
//
// **"Mute" silences the room, not the warnings**, and that is deliberate:
// whoever mutes does it because the room's hiss is annoying, not because they
// are giving up being told. A monitor that stops warning precisely when you
// silence it does the opposite of its job.
//
// The label is one word because it was the widest line in the bar, and the whole
// promise lives in the tooltip, `viewer.mute.title`.
//
// **And the context opens by itself.** It used to lean on the one created by the
// level meter, which is born with the tap on "Watch and listen": it was enough
// to reload the page, or for the browser to suspend the context while one was
// elsewhere, for the warning to appear mute. A stroke must not depend on a level
// indicator — they are two things with nothing to do with each other, and tying
// them together produces exactly the worst case: the microphone's fault
// announced without a sound.
//
// The gesture is still needed, but **once per page**: after the first
// interaction the document stays activated and a context created later starts
// all the same. If nothing has ever been touched we keep quiet and declare it in
// the console, instead of being left without an explanation.
function chime(low) {
  try {
    if (!audioCtx) {
      audioCtx = new (window.AudioContext || window.webkitAudioContext)();
    }
  } catch (err) {
    console.warn('chime: audio context unavailable', err);
    return;
  }
  // resume() is asynchronous: playing right after calling it, with the context
  // still suspended, produces silence and no errors. We wait.
  audioCtx.resume().then(() => playChime(low)).catch((err) => {
    console.warn('chime: audio context not started', err);
  });
}

function playChime(low) {
  if (!audioCtx || audioCtx.state !== 'running') return;
  const t = audioCtx.currentTime;
  const notes = low ? [523.25, 392.00] : [659.25, 880.00];
  notes.forEach((f, i) => {
    const osc = audioCtx.createOscillator();
    const vol = audioCtx.createGain();
    osc.type = 'sine';
    osc.frequency.value = f;
    // Soft attack and a long tail: a square wave or a sharp cut in amplitude
    // makes a click, and at night a click wakes more than the warning does.
    vol.gain.setValueAtTime(0.0001, t + i * 0.18);
    vol.gain.exponentialRampToValueAtTime(0.22, t + i * 0.18 + 0.02);
    vol.gain.exponentialRampToValueAtTime(0.0001, t + i * 0.18 + 0.45);
    osc.connect(vol).connect(audioCtx.destination);
    osc.start(t + i * 0.18);
    osc.stop(t + i * 0.18 + 0.5);
  });
}

// updateAlerts receives the list of now and decides what has changed.
function updateAlerts(list) {
  const current = new Map((list || []).map((a) => [a.id, a.code]));

  // Appeared: an id that was not there before. There is one sound only even if
  // three arrive together, otherwise starting up with the microphone unplugged
  // and the tunnel down would ring a peal.
  let appeared = null;
  for (const a of list || []) {
    if (!seenAlerts.has(a.id)) appeared = appeared || a;
  }

  // Recovered: an id that was there and is not any more.
  let recovered = null;
  for (const [id, code] of seenAlerts) {
    if (!current.has(id)) recovered = recovered || code;
  }
  seenAlerts = current;

  if (list && list.length > 0) {
    showAlert(list[0]);              // the server sends them worst first
    // A fault sounds low, an event and a note sound high: they are different
    // things and have to sound different with the screen off too.
    if (appeared) chime(appeared.level === 'fault');
    return;
  }
  if (recovered) {
    showRecovery(recovered);
    chime(false);
    return;
  }
  alertBar.className = 'alertbar';
}

async function pollStatus() {
  try {
    const res = await fetch('/api/status');
    if (res.status === 401) {
      location.href = '/login';
      return;
    }
    const s = await res.json();
    lastState = s;

    el('s-ver').textContent = s.version || '—';
    el('s-enc').textContent = s.encoder ? `${s.encoder} (${s.encoderVendor})` : '—';
    // The real framerate is shown next to the requested one: in the dark the
    // webcam slows down a good deal, and it is better for that to be visible
    // than to look like lag. There are three cadences and two are shown, the
    // third only when something changes. Measured over declared says how the
    // camera is doing; "delivered" appears **only** when the gate lets fewer
    // through, that is when we are dropping images on purpose — without that
    // line an image refreshing every two seconds reads as a stuck camera.
    const reduced = s.deliveredFps > 0 && s.deliveredFps < s.fps
      ? T('viewer.stats.delivered', {n: s.deliveredFps}) : '';
    el('s-res').textContent = s.resolution
      ? `${s.resolution} @${s.measuredFps ? s.measuredFps.toFixed(1) : '?'}/${s.fps} fps${reduced} · ${s.videoKbps} kbit/s`
      : '—';
    micPick.paint(s);
    camPick.paint(s);
    const raw = rawAudioState(s);
    el('s-raw').textContent = rawSays[raw] ? T(rawSays[raw]) : '—';
    el('s-lvl').textContent = `${s.audioLevelDbfs.toFixed(1)} dBFS (${micHealth(s.micHealth)})`;
    setViewers(s.viewers);
    el('s-key').textContent = s.keyframes;
    el('s-plireq').textContent = s.keyframeReqs;
    el('s-restart').textContent = s.restarts;
    // The frames dropped on purpose: it is the other number that tells the
    // saving from a fault, and it is read together with "delivered".
    el('s-drop').textContent = s.videoDropped;
    el('s-path').textContent = mediaPath || '—';
    el('s-up').textContent = s.uptime || '—';
    el('s-dev').textContent = s.devices;

    renderRemote(s.remote);
    paintDetect(s);
    // A recording in progress is declared by the monitor, not by the press: it
    // is the room that is being recorded, and it has to be seen from a second
    // phone opened with the clip already running. It is also the only way of
    // putting the pill out — the clip ends by itself, ten seconds later, with
    // nobody pressing anything.
    paintRecord(!!s.recording);
    checkTalk(s.talkbackBusy);
    updateAlerts(s.alerts);

    // Digital silence is not said **here as well**: the bar at the top announces
    // it, with the sound. Saying it twice on the same page with different words
    // makes one fault look like two.
    showWarning('raw', raw === 'filtered' ? T('viewer.raw.filtered') : null);
  } catch (err) {
    // A status error must not touch the streaming.
  }
}

connect();
pollStatus();
setInterval(pollStatus, 3000);
