'use strict';

// Notifications with the page closed, for this device.
//
// **The row appears only where a subscription can work**, and that is decided
// by asking the browser for each piece rather than by knowing which browser it
// is: an encrypted page, a service worker, `PushManager` and `Notification`.
// On an iPhone the third exists only in the web app saved to the Home Screen,
// so the same four questions hide the row in a Safari tab and show it from the
// icon, with no user agent read anywhere.
//
// **Nothing is installed for whoever does not turn them on.** The service
// worker is registered by the press and unregistered by the press that turns
// them off; at an opening it is only looked for, to hand the subscription over
// again — a browser can lose one without saying so, and iOS never fires the
// event that would tell.
//
// Every name here starts with `push`: this script shares one scope with the
// others on the page, and a second top-level name the same as one of theirs
// would stop this whole file from running.

const pushRow = el('push-row');
const pushToggle = el('push-toggle');
const pushTestBtn = el('push-test');
const pushState = el('push-state');
const pushGuide = el('push-guide');
const pushWhy = el('push-why');
const pushFacts = el('push-facts');

let pushKey = '';     // the monitor's key, fetched before any press
let pushSub = null;   // this browser's subscription, when there is one

// pushConditions is what this browser, on this page, has — the conditions a
// subscription needs, observed rather than assumed, and shown when it fails.
function pushConditions() {
  const has = (x) => x in window;
  return {
    https: location.protocol === 'https:',
    secureContext: window.isSecureContext === true,
    serviceWorker: 'serviceWorker' in navigator,
    pushManager: has('PushManager'),
    notification: has('Notification'),
    permission: has('Notification') ? Notification.permission : 'absent',
    installed: matchMedia('(display-mode: standalone)').matches ||
      matchMedia('(display-mode: fullscreen)').matches || navigator.standalone === true,
  };
}

function pushPossible(c) {
  return c.https && c.secureContext && c.serviceWorker && c.pushManager && c.notification;
}

// pushKeyBytes is the key in the form `subscribe` takes.
function pushKeyBytes(b64) {
  const s = b64.replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(s + '='.repeat((4 - s.length % 4) % 4));
  return Uint8Array.from(raw, (ch) => ch.charCodeAt(0));
}

// pushSameKey says whether a subscription was made with the monitor's key: one
// made with another is a subscription nothing will ever be sent to.
function pushSameKey(sub, b64) {
  const had = sub.options && sub.options.applicationServerKey;
  if (!had) return true;
  const a = new Uint8Array(had), b = pushKeyBytes(b64);
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

function pushSend(url, sub) {
  const j = sub.toJSON();
  return postJSON(url, {endpoint: j.endpoint, keys: j.keys, lang: LANG});
}

// paintPush draws the row. `state` is off, on, denied or failed; `note` is a
// line under it, with the tone of the pickers' state line.
function paintPush(state, note, tone) {
  pushToggle.hidden = state === 'denied';
  pushToggle.setAttribute('aria-pressed', state === 'on' ? 'true' : 'false');
  pushToggle.disabled = false;
  pushTestBtn.hidden = state !== 'on';
  pushTestBtn.disabled = false;
  const line = state === 'denied' ? T('viewer.push.denied')
    : state === 'failed' ? T('viewer.push.failed') : (note || '');
  pushState.textContent = line;
  pushState.hidden = !line;
  pushState.className = 'pick-state' + (tone || (state === 'denied' || state === 'failed' ? ' notice' : ''));
  pushGuide.hidden = !(state === 'denied' || state === 'failed');
  pushWhy.hidden = state !== 'failed';
}

// pushFailed shows the conditions and what the browser answered, closed.
function pushFailed(err, code) {
  const facts = pushConditions();
  if (err) facts.error = `${err.name || 'Error'}: ${err.message || err}`;
  if (code) facts.monitor = code;
  pushFacts.textContent = Object.entries(facts).map(([k, v]) => `${k}: ${v}`).join('\n');
  paintPush('failed');
}

async function pushInit() {
  const c = pushConditions();
  if (!pushPossible(c)) return;
  try {
    const res = await fetch('/api/push/key');
    if (!res.ok) return; // a monitor with no notifications keeps the row hidden
    pushKey = (await res.json()).key;
  } catch (err) {
    return;
  }
  pushRow.hidden = false;
  if (c.permission === 'denied') { paintPush('denied'); return; }
  try {
    const reg = await navigator.serviceWorker.getRegistration('/');
    pushSub = reg ? await reg.pushManager.getSubscription() : null;
    if (pushSub && (c.permission !== 'granted' || !pushSameKey(pushSub, pushKey))) {
      await pushSub.unsubscribe();
      pushSub = null;
    }
    if (pushSub) await pushSend('/api/push/subscribe', pushSub);
  } catch (err) {
    console.warn('the subscription could not be handed over again', err);
  }
  paintPush(pushSub ? 'on' : 'off');
}

// pushOn is the press. **The permission is asked in the same turn as the
// press**, before anything is awaited: Safari grants the prompt only to a
// gesture, and a promise in between is a gesture that has ended.
async function pushOn() {
  pushToggle.disabled = true;
  let reg, perm;
  // **A press that does not end with notifications on takes its worker back.**
  // Measured on Brave: the subscription failed and the worker stayed
  // registered, installed for somebody who had not got what it was for.
  const giveBack = () => reg && reg.unregister().catch(() => {});
  try {
    [reg, perm] = await Promise.all([
      navigator.serviceWorker.register('/sw.js', {scope: '/'}),
      Notification.requestPermission(),
    ]);
  } catch (err) {
    giveBack();
    pushFailed(err);
    return;
  }
  if (perm !== 'granted') {
    giveBack();
    paintPush(perm === 'denied' ? 'denied' : 'off');
    return;
  }
  try {
    await navigator.serviceWorker.ready;
    pushSub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: pushKeyBytes(pushKey),
    });
  } catch (err) {
    // Brave with Google's push service off answers AbortError here, with the
    // permission granted and everything else in order.
    giveBack();
    pushFailed(err);
    return;
  }
  const res = await pushSend('/api/push/subscribe', pushSub).catch(() => null);
  if (!res || !res.ok) {
    const body = res ? await bodyOf(res) : {};
    if (res && sessionExpired(res)) return;
    await pushSub.unsubscribe().catch(() => {});
    pushSub = null;
    giveBack();
    pushFailed(null, body.error || 'no-answer');
    pushState.textContent = TErr(body.error);
    return;
  }
  paintPush('on');
}

async function pushOff() {
  pushToggle.disabled = true;
  try {
    if (pushSub) {
      await pushSend('/api/push/unsubscribe', pushSub).catch(() => null);
      await pushSub.unsubscribe();
    }
    const reg = await navigator.serviceWorker.getRegistration('/');
    if (reg) await reg.unregister();
  } catch (err) {
    console.warn('turning the notifications off', err);
  }
  pushSub = null;
  paintPush('off');
}

// pushTest asks the monitor to send one now, and says what the push service
// answered. **It is the only honest check there is**: a push service takes a
// message for a phone that is off, and nothing but a notification appearing
// says it reached the device.
async function pushTest() {
  if (!pushSub) return;
  pushTestBtn.disabled = true;
  const res = await pushSend('/api/push/test', pushSub).catch(() => null);
  const body = res ? await bodyOf(res) : {};
  if (res && sessionExpired(res)) return;
  if (!res || !res.ok) {
    paintPush('on', TErr(body.error), ' notice');
    return;
  }
  const r = body.result || 'unavailable';
  if (r === 'gone') {
    pushSub = null;
    paintPush('off', T('viewer.push.result.gone'), ' notice');
    return;
  }
  paintPush('on', TOr('viewer.push.result.' + r, 'viewer.push.result.unavailable'),
    r === 'delivered' ? '' : ' notice');
}

pushToggle.addEventListener('click', () => {
  if (pushToggle.getAttribute('aria-pressed') === 'true') pushOff(); else pushOn();
});
pushTestBtn.addEventListener('click', pushTest);

pushInit();
