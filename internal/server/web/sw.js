'use strict';

// The service worker, and it does one thing: show the notifications the
// monitor sends, and open the monitor when one is touched.
//
// **It caches nothing.** A worker is usually the piece that makes a page work
// offline, and this page is a live picture: a cached copy of it would be a
// monitor that opens with no monitor behind it, which is the one thing it must
// never look like.
//
// **Every push shows a notification, first and always.** A push that shows
// nothing is one Safari counts, and at the third it revokes the subscription;
// Chrome shows a notice of its own and Firefox unsubscribes after a few. So the
// notification is shown before anything else is tried, inside the same
// `waitUntil`, and the receipt comes after it.

self.addEventListener('push', (event) => {
  let msg = {};
  try {
    msg = event.data ? event.data.json() : {};
  } catch (err) {
    msg = {};
  }
  // The payload is the Declarative Web Push shape, which Safari can show on
  // its own; here the same JSON is read and shown by hand.
  const n = msg.notification || {};
  const target = n.navigate || '/';
  let id = '';
  try {
    id = new URL(target, self.location.origin).searchParams.get('n') || '';
  } catch (err) {
    id = '';
  }
  event.waitUntil((async () => {
    const options = {
      body: n.body || '',
      lang: n.lang || undefined,
      icon: '/icon-192.png',
      data: {navigate: target},
    };
    // A tag makes a new notification of the same code take the place of the
    // old one, and `renotify` makes it sound again anyway: a second cry is
    // news. `renotify` without a tag is an exception, so they go together.
    if (n.tag) {
      options.tag = n.tag;
      options.renotify = true;
    }
    await self.registration.showNotification(n.title || 'PAT Monitor', options);
    // The receipt says how long it took to be shown. It needs the monitor to
    // be reachable, and a phone away from home without it is exactly the case
    // where it is not: then there is no receipt, and nothing else is lost.
    if (id) {
      try {
        await fetch('/api/push/seen', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({id}),
          credentials: 'same-origin',
        });
      } catch (err) {
        // Nothing to do: the notification is already on screen.
      }
    }
  })());
});

// A touch opens the monitor: the window already open if there is one, a new
// one otherwise.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target = (event.notification.data && event.notification.data.navigate) || '/';
  event.waitUntil((async () => {
    const open = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    for (const c of open) {
      if (new URL(c.url).origin === self.location.origin && 'focus' in c) return c.focus();
    }
    return self.clients.openWindow(target);
  })());
});

// A browser that replaces a subscription says so here — Firefox does, the
// others hardly ever — and the new one is handed to the monitor, the old one
// taken back.
self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil((async () => {
    const res = await fetch('/api/push/key', {credentials: 'same-origin'});
    if (!res.ok) return;
    const key = (await res.json()).key;
    const s = key.replace(/-/g, '+').replace(/_/g, '/');
    const raw = atob(s + '='.repeat((4 - s.length % 4) % 4));
    const sub = await self.registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: Uint8Array.from(raw, (ch) => ch.charCodeAt(0)),
    });
    const post = (url, body) => fetch(url, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
      credentials: 'same-origin',
    });
    const j = sub.toJSON();
    await post('/api/push/subscribe', {endpoint: j.endpoint, keys: j.keys, lang: self.navigator.language || ''});
    if (event.oldSubscription) {
      await post('/api/push/unsubscribe', {endpoint: event.oldSubscription.endpoint});
    }
  })());
});
