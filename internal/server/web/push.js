'use strict';

// Notifications with the page closed, for this device.
//
// **The switch appears only where a subscription can work**, and that is decided
// by asking the browser for each piece rather than by knowing which browser it
// is: an encrypted page, a service worker, `PushManager` and `Notification`.
// On an iPhone the third exists only in the web app saved to the Home Screen,
// so the same four questions hide the switch in a Safari tab and show it from the
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

// paintPush draws the card. `state` is off, on, denied or failed; `note` is a
// line under it, with the tone of the pickers' state line. The link to the
// guide is not touched: it is always there while the card is.
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

// pushWhyNot is the one sentence a card with no switch says: the condition this
// page can see, worded as what to do about it. **No user agent is read**: an
// iPhone's Safari tab is the browser that has `navigator.standalone` and no
// `PushManager`, which is a question about features, not about a name.
function pushWhyNot(c) {
  if (!c.https || !c.secureContext) return 'viewer.push.off.address';
  if (!c.pushManager && 'standalone' in navigator && !c.installed) return 'viewer.push.off.home-screen';
  return 'viewer.push.off.browser';
}

// pushUnavailable shows the card with no switch, saying why. **The card is
// always there, and where notifications cannot arrive it says so** — the
// owner asked for it: a feature that appears on one device and not on the
// next was a thing nobody could find again, and the reason is the half the
// page can state. There is still no grey button: nothing is offered that
// cannot be pressed.
function pushUnavailable(key) {
  pushToggle.hidden = true;
  pushTestBtn.hidden = true;
  pushWhy.hidden = true;
  pushState.textContent = T(key);
  pushState.className = 'pick-state';
  pushState.hidden = false;
  pushRow.hidden = false;
}

async function pushInit() {
  const c = pushConditions();
  if (!pushPossible(c)) { pushUnavailable(pushWhyNot(c)); return; }
  try {
    const res = await fetch('/api/push/key');
    if (sessionExpired(res)) return;
    // **Only a 501 says the monitor cannot send.** A 429, a 5xx or a network
    // that dropped while the page opened is a moment, not a property, and
    // stating it as one would tell somebody their notifications cannot work
    // on a monitor that sends them: the card stays hidden until the next
    // opening asks again.
    if (res.status === 501) { pushUnavailable('viewer.push.off.monitor'); return; }
    if (!res.ok) return;
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
    if (pushSub) {
      const res = await pushSend('/api/push/subscribe', pushSub);
      if (sessionExpired(res)) return;
      // **A refusal of the hand-over is the monitor saying it will not write
      // to this device**: the list is full, or the subscription is one it
      // cannot take. Showing "on" over it would promise notifications nobody
      // sends, so it is taken back the way a failed press is. A 429 or a 5xx
      // is a moment and keeps it: the monitor still has it from before.
      if (res.status >= 400 && res.status < 500 && res.status !== 429) {
        const body = await bodyOf(res);
        await pushTakeBack(reg);
        paintPush('off', TErr(body.error), ' notice');
        return;
      }
    }
  } catch (err) {
    console.warn('the subscription could not be handed over again', err);
  }
  paintPush(pushSub ? 'on' : 'off');
}

// pushTakeBack removes this browser's subscription and its worker: **nothing
// is left installed for a device the monitor will not write to**, otherwise
// the next opening finds it, hands it over again and shows "on".
async function pushTakeBack(reg) {
  if (pushSub) await pushSub.unsubscribe().catch(() => {});
  pushSub = null;
  const r = reg || await navigator.serviceWorker.getRegistration('/').catch(() => null);
  if (r) await r.unregister().catch(() => {});
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
  // **Settled, not `all`**: `all` rejects as soon as one half does and
  // drops the other's value, so a worker registered beside a refused prompt
  // had no handle to be taken back by.
  const [r, p] = await Promise.allSettled([
    navigator.serviceWorker.register('/sw.js', {scope: '/'}),
    Notification.requestPermission(),
  ]);
  if (r.status === 'fulfilled') reg = r.value;
  if (r.status === 'rejected' || p.status === 'rejected') {
    giveBack();
    pushFailed(r.status === 'rejected' ? r.reason : p.reason);
    return;
  }
  perm = p.value;
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
    await pushTakeBack();
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
