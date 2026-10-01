---
paths:
  - "internal/push/**"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## A notification is offered only where it can arrive

`internal/push`, `web/push.js`, `web/sw.js`. **It was written once, tested and
removed**, and this chapter replaced the one that said so. The reason then was
that the feature has no single solution: on a baby monitor "tell me when the
page is closed" has to work because a button was pressed, and a grey button
that cannot be unblocked from inside the product is a defect with a label. The
owner reopened it on 2026-09-30 on one condition, **no publicity**: not on the
guided path, not in the tray, not among the README's features, and the
procedure for each device in `docs/notifications.html`.

**The objection is met, not argued away.** The row in Details exists only where
every condition a page can check holds — an https page, a secure context, a
service worker, `PushManager`, `Notification` — so where they do not there is no
button at all, grey or otherwise. On an iPhone `PushManager` exists only in the
web app saved to the Home Screen, so the same four questions hide the row in a
Safari tab and show it from the icon, with no user agent read. What a page
cannot check is stated rather than hidden: a permission the browser refuses is
a sentence naming where it is switched back on, and a subscription that fails
is the observed conditions in a closed `<details>`, beside the link to the
guide. **The procedure lives in the documents because the conditions that
remain are the device's**, not the page's: whether Edge runs in the
background, whether Android's channel may break Do Not Disturb, whether the
web app is allowed through a Focus.

What was learned by paying for it the first time, and which does not need
re-measuring:

- **The push service is chosen by the browser, not by the system**, and does
  not go through us: on one Windows machine Edge delivered to
  `notify.windows.com` and Brave to `fcm.googleapis.com`.
- **A granted permission does not guarantee delivery.** Five independent
  conditions, and the last is the one nobody thinks of: a service the browser
  manages to subscribe to. Brave keeps that channel off by default, and
  `subscribe()` answers `AbortError` with permission `granted`.
- **A badly encrypted payload is accepted with a 201**, because the service
  cannot read what it carries. The encryption is therefore tested against the
  example in RFC 8291 byte for byte — somebody else's implementation, frozen —
  and by decrypting on the receiver's side with HMAC written by hand, not the
  HKDF the package calls. **Two implementations of the same wrong idea agree
  with each other**, and both tests fail with the two keys swapped in the
  derivation.
- **When a service answers only one thing, ask it two broken questions.** FCM,
  to a non-existent endpoint: a good token 410, none 401, garbage 403 — it
  checks the token before the endpoint, so the 410 means "the token got
  through".
- **Browsers are driven over the DevTools protocol**, pressing the real button;
  it is the only answer to "from here I cannot see what the browser did".

Decisions that are not visible from the code:

- **Every push shows a notification, first.** Safari revokes a subscription
  after three pushes that show nothing, Firefox after a handful, and FCM demotes
  the priority of a device whose high-priority messages show nothing. So
  `sw.js` shows before it does anything else, inside the same `waitUntil`, and
  there is no silent push of any kind — no health check, and no hiding the
  notification when the page is open.
- **The payload is Declarative Web Push, for everybody**, with `mutable`.
  Safari 18.4 and later shows it by itself even if the worker fails; every other
  browser hands the same JSON to the worker. **The words are composed on the
  monitor**, in the language the page spoke when it subscribed, because the
  system shows the payload as it comes: the codes still cross the API, and the
  edge that draws is the phone's lock screen. They are the banner's own
  `viewer.alert.` and `viewer.recovered.` entries, so there is no second
  wording of an alert to drift, and `words_test.go` holds them present in every
  catalogue, which the page's guard cannot see.
- **The news is the banner's, after the grace and the switches**: `appeared`
  and `recovered` from `registry.Update`, one call a round, which never waits
  for the network. **A fault recovers on the phone and an event does not**: the
  recovery carries the fault's `Topic` and `tag`, so it takes its place,
  queued or shown; an event's "it has stopped" would take the place of the cry,
  which is what whoever looks at the phone later wants to find.
- **`Urgency: high` on every message**, because every message is an alert: it
  is what FCM carries through Doze and what Apple sends at once. The TTL is five
  minutes for an event and an hour for a state, and **those two are chosen and
  not measured** — a cry delivered after five minutes is news about a room that
  has changed, a stopped camera learnt an hour late is still worth knowing. A
  retry stops inside the TTL; a 429's `Retry-After` is honoured if it fits.
- **Only 404 and 410 remove a subscription.** Every other refusal is about the
  message or about us, and dropping the device for it would switch somebody's
  notifications off because a service had a bad minute.
- **The store sits beside the node, not beside the configuration.** A
  subscription is bound to the origin, the origin is the node's name, and the
  node belongs to this machine while `%APPDATA%` roams. The VAPID key is in the
  same file and is made once: every browser subscribed against it.
- **`sub` is the project's address, not anybody's email** — the person running
  this monitor did not write it, and Apple refuses a `sub` it cannot reach. One
  token is reused per push service for six hours of its twelve, because Apple
  asks not to be sent a new one more than once an hour.
- **The endpoint is the one URL the monitor calls because a page said so**, so
  it is held to what a push service always is — https on 443, on the public
  Internet, checked again at dial time on the address actually dialled — and not
  to a list of companies, which would absolve the next one and refuse nothing
  it did not name. No proxy, no redirects, 32 subscriptions at most.
- **Nothing is installed for whoever does not turn it on.** The worker is
  registered by the press and unregistered by the press that turns them off;
  an opening only looks for it, to hand the subscription over again, because
  iOS never fires `pushsubscriptionchange` and a subscription can lapse without
  a word. It caches nothing: a cached live picture is a monitor that opens with
  no monitor behind it.
- **The permission is asked in the press's own turn**, `register` and
  `requestPermission` started together before anything is awaited, because
  Safari grants the prompt only to a gesture.
- **The receipt is the instrument.** The worker posts `/api/push/seen` after
  showing, and the log writes how long the notification took; with the phone
  off the tailnet there is no receipt and nothing else is lost. "Delivered" in
  the test's answer is what the service said, never "it was shown".

**Verified live on this machine**, with the real pages served over HTTPS on
loopback by a throwaway harness and the real button pressed over the DevTools
protocol: Edge subscribed through `wns2-…notify.windows.com` and Brave, with
Google's push service switched on in its profile, through `fcm.googleapis.com`.
Test, cry, fault, recovery and turning off went through on both. Five things
the run found that no test here could have:

- **The test's receipt came back before the service's 201 did.** It had been
  remembered only once the answer arrived, so it found nothing; it is
  remembered before sending now.
- **A failed press left the worker installed.** On Brave with its default the
  answer was the known `AbortError: Registration failed - push service error`,
  stated in the row as it should be — and the registration stayed. Every press
  that does not end with notifications on now takes it back, and the same run
  shows none left.
- **A dead subscription does not say so.** The endpoint of an Edge profile
  deleted between two runs went on answering 201 from WNS, so it stays in the
  list and costs one request per alert until the service says 404 or 410.
  Nothing on this side can tell it from a phone that is off.
- **WNS showed each in 280 to 540 ms. A fresh FCM connection held the first
  three and showed them together**, 6 to 14 s after they were sent; the next
  one took 70 ms. One run, one machine: it is a shape to look for on a phone,
  not a number.
- **The instrument lied twice before it measured.** The first `page` target of
  a fresh Edge profile can be a tab nobody sees, where script runs and mouse
  events land on nothing — three runs out of four did "nothing" on a real
  press; a tab of the driver's own, activated, fixed it. And the count of
  processes left behind counted its own shell, which carries the profile's
  name on its command line: the browsers are counted by name and path now.

**The words were read against eight, one reader per language, and every
guard had been green.** Five findings recurred across catalogues, which is
the reason for reading them together:

- **A pronoun pointed at the wrong noun in six.** *Where they are turned on*
  came right after *on pages that are open*, and in Italian, German, French,
  Spanish and both Portuguese the pronoun agrees with the pages first. The
  sentence now names the devices, as Chinese and Japanese already did, and it
  says *only* again: the rewrite had dropped the one word that bounds the list.
- **One word for two things, in Japanese.** 通知 was already the monitor's own
  warnings, in `safety.notice` and in the mute tooltip; with the feature it
  also became the system notification, so "notifications appear on devices
  with notifications on". The warnings are アラート now, outside the keys this
  change added, because the collision was this change's.
- **A participle agreeing with a noun nobody sees.** "Sent" answers "Send a
  test", which is masculine in French and Portuguese, and the reply was
  feminine for a notification the line never named. It names it now.
- **"Subscription" reads as a bill and "expired" as a clock.** A 404 or 410 is
  neither, so the line says what is true — this device no longer receives
  them — and the Japanese 登録 went too, being the word the same panel uses for
  registered devices.
- **"Did not answer" was false half the time**: the outcome also covers a 5xx,
  which is an answer. It says the service could not take the message.

**And the row is as wide as the grid**, measured in the nine languages at 360,
390 and 1200: the two controls are about 250 px, so beside any Latin label
they take a line of their own in a column, and a row that tall stretched its
neighbours to 78 px. Across the grid it is one line at 1200 and its neighbours
keep 38; on a phone the controls wrap together and nothing scrolls sideways.
Shortening the label, which one reader proposed, buys nothing there — the
controls alone do not fit beside the shortest candidate.

**The guide is in every language of the interface, and it quotes the
interface.** `docs/notifications.html` and one `notifications.<tag>.html` per
catalogue; the row's link is the catalogue's `viewer.push.guide-href`, so each
language opens its own. Every word the guide quotes carries the key it quotes,
and `guide_test.go` holds the three halves together, deriving all of it from
the catalogues: each language links a page declared in that language, each
quote equals the catalogue, and each page has the English shape — headings,
steps, code, links — so a step dropped in one language shows as a count.
**The translators found the English wrong three times** — Edge's setting had
lost "Microsoft" in English and kept it in four other languages, iOS 26 hides
Share under ••• and adds an "Open as Web App" switch, Chrome's menu item is now
"Install and create shortcut" — and the cross-reading found a test reply quoted
as the alert's log line and a language claim the code does not keep. The names
of other products' menus are read from the products where they are on this
machine — Edge's and Brave's `.pak` files — and from Chromium's, AOSP's and
Apple's own pages otherwise. **Three names rest on weaker evidence**: the iOS
permission prompt's "Allow" button, which no Apple page quotes for a web app;
Samsung's battery entries in Italian, Spanish and Chinese, taken from
Samsung's forums and press rather than a string file; and Tailscale's "VPN On
Demand", kept in English because the app is not localised.

**On an iPhone from the Home Screen it works**, as the owner reported on
2026-10-01; how long a notification takes there was not measured.

**What is not known is what arrives on a sleeping phone**, and it is not
knowable from here: an iPhone locked for a while, an Android in Doze, a phone
on a mobile network with Tailscale off. The receipt is the measurement, and
the two TTLs above are the first numbers it may move.
