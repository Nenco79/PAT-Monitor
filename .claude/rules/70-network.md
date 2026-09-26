---
paths:
  - "internal/server/**"
  - "internal/tunnel/**"
  - "internal/rtc/**"
---

Part of PAT Monitor's engineering record — chapters of **The loop: what
the monitor decides, and in what order**; the index that carries every
chapter, in order, is in `CLAUDE.md`.

### An asset with no route does not give a 404, and nobody says so

The asset routes are listed one by one in `Server.routes()`. Adding `icons.css`,
the sheet was there, both pages asked for it, and the route was not. **The fault
is silent from all three sides**, which is what makes it instructive:

- **from the server**, because `GET /` is registered as a fallback route: an
  address with no route of its own does not answer 404, it falls onto the viewer
  page and returns **HTML that looks fine**;
- **from the browser**, because a stylesheet that does not arrive does not stop
  the load — and with `nosniff` a `text/html` served for a `.css` is discarded
  with nothing showing;
- **on screen**, because an `<svg>` without its rule **does not disappear**: it
  draws itself filled with black, which on the viewer's background is invisible.
  The report was "the icons have gone", and the icons were all there.

`assets_test.go` asks the question, and asks it **from the pages**: it reads
every local `href` and `src` from the embedded HTML and checks both ways — every
cited asset has a route of its own, and every embedded asset is cited by
somebody. A list written in the test would be the second list that diverges from
the first. **And it does not look at the status code**: written that way the
test **passed even with the route that caused the fault removed**, because the
fallback answered. So the mux is asked which pattern matched: if it is `GET /`,
nobody serves that address.

**The lesson beyond the case:** when a fallback route exists, "it answered"
stops being proof that anybody is there. It is the same shape as "a wrong GUID
does not complain": the system is built to accept, and so the question moves
from "did it error?" to "who answered?".

### The STUN servers must be handed to the browser too

Configuring them only on our side is not enough: each of the two has to discover
its own public address. A `new RTCPeerConnection()` with no `iceServers`
produces only local-network candidates.

The fault **does not show at home**, which is what makes it insidious: between
phone and monitor on the same network the `host` candidates suffice. From
cellular the only candidate offered is a carrier-private address and negotiation
always fails. They travel with the offer, in `signalMessage.ICEServers`, because
they are configurable and the page is static.

**Each one travels in an entry of its own, and that is not tidiness.** `iceServers`
asks the consumer whether an entry is acceptable and drops the ones it refuses,
because a row `NewPeerConnection` will not take is not a missing fallback — the
hub builds one connection per viewer with that list, so it is the monitor
unreachable, at home and from outside, with one failure per visit and no cause
written anywhere. Grouped into a single `webrtc.ICEServer` the question is asked
about the **whole list**, so one typo in `stun_servers` answers for every good
server with it: the loop that reads as "one entry at a time" had exactly one
entry to iterate, and its comment claimed the opposite of what it did.

**The test written beside it asserted the loss as if it were intended** — *a
malformed one carries the good ones away*, expecting zero — which is the worst
thing a test can do with a defect, because from then on the defect is the
specification. It reads the other way now, and a second guard pins the property
underneath it: one URL per entry, which no case of the first test can see, the
good lists being identical either way.

**The price was measured rather than assumed**, since one validation per server
sounds like one certificate per server: the **first** PeerConnection of the
process costs **88 ms** whatever it validates, and each server after it about
**half a millisecond**. The 88 was already being paid, so the split costs the
default pair half a millisecond — and what it buys is that a typo costs its own
row. It is also what makes `stun_usable` in the no-path diagnosis a count of
servers instead of a count of rows, which at the default was reporting **1** for
two.

### A relay is not shipped, and the reason is what it would carry

WebRTC opens a direct path between the monitor and the phone; where it cannot —
a symmetric NAT on a mobile carrier, a network that blocks UDP — the published
figures put the share of connections needing a **relay** at roughly one in ten.
A relay is a server in the middle that forwards the media, and the monitor
carried three configuration keys for one: `turn_url`, `turn_username`,
`turn_password`. They are gone.

**It was removed on what a relay costs, not on what it is worth.** The thing
being relayed is not a handshake, it is the video: 2500 kbit/s, for as long as
somebody is watching, which on a baby monitor is a night. That is well over a
gigabyte an hour of somebody's bandwidth, for ever, for a program that is given
away — and the free tiers of the relay services die on the first night. The
other road is the user installing a `coturn` of their own, which is the sentence
this whole project is written against: **one executable, no service to
install.**

**And the case it cured is already cured, by something that is not ours.** The
watcher who cannot get a direct path installs **Tailscale** on the device they
watch from and joins the tailnet: from there the media rides the tailnet, and
when a direct path is not available Tailscale relays it **itself**, on its own
servers, at nobody's expense here. The Funnel exists so that nothing has to be
installed on the watching device, which makes it the convenient road; the robust
one was already built, and the guided path already teaches it.

**What actually needed fixing was a sentence, and it is now in the log.** When
ICE finds no path the page says `connection failed`, two words, with no cause and
nothing to do about it — and it says them to exactly the one-in-ten this chapter
is about, who are also the people who most need to see the room. **Removing a
knob that nobody could turn without paying is cheap; leaving the person in front
of the failure with two words is not**, and the chapter below is the second half:
the monitor holds every fact that separates the causes, and now spends them.

Three things went with the keys, and they are the shape of the removal. The
branch in `iceServers`, whose departure left that function with one entry to
filter and so exposed a defect that had been latent under it — see "The STUN
servers must be handed to the browser too". The `· via relay` badge over the picture, which could no longer appear at
all: relay candidates come from a relay server, and with none configured neither
end has any. And the `viewer.relayed` line in both catalogues. **The media path
stays in the details**, because `host` and `srflx` are real diagnostics and are
the datum that says whether a connection from outside really worked.

**Nobody's configuration breaks.** The file is read with a non-strict
unmarshal, so a `turn_url:` left behind in somebody's `config.yaml` is ignored
rather than refused — which is the difference between removing a key and
removing a program's ability to start.

#### When no path is found, the log names the cause

The page says two words to somebody in another house, and by morning the session
is gone: **a failure with no cause written down cannot be investigated
afterwards.** The line already existed — `viewer with no ICE path established` —
and carried **one** cause, written a priori as a `note` that guessed at mobile
networks. It was right about one case in seven and silent about the others.

**The facts are asked of the ICE agent, not tallied beside it.** Pion's own
statistics are what the agent really did with the candidates; a count of ours
would be a second opinion, and the day the two disagreed the wrong one would be
in the log. Verified on two real peer connections, which is not a formality —
this file already records a `GetStats` that populates nothing for a sender: local
candidates, remote candidates, pairs, and the checks sent and answered are all
there. The only two things asked elsewhere are how many candidates the **page**
sent and how many of those pion refused, which the agent cannot know: from where
it sits, a candidate never sent and one sent badly are the same absence, and they
are two different faults — the second is ours.

**Two measurements decided the shape, and neither is visible by reading.**

**A remote candidate is not proof the browser sent one.** With the signalling of
candidates deliberately cut, the agent's remote list was **not empty**: it held
one **peer-reflexive** candidate, an address learnt from an incoming connectivity
check rather than from anything the page said. Counted among the signalled ones
it would have absolved precisely the case the facts are collected for, so
`Signalled()` leaves prflx out and the tally of what the page sent is kept at the
one place that sees it, `AddICECandidate`.

**And after the session closes, the agent answers zeros.** Measured on the same
pair: connected it reports one candidate each side, one pair, one check sent and
answered; one instant after `Close` it reports **zero of everything** — not an
error, zeros. These facts are asked for exactly when a session failed to connect,
and an ICE failure closes the session, so the obvious shape reads the agent after
it has gone and files `no-candidates-from-the-viewer` against a browser that sent
five. It is the final report's `last_video_kbps=0` one governor across, and
worse, because that one looked wrong and this one looks like a diagnosis. The
facts are therefore frozen by `Close` **before** it tears anything down, and the
order of those two statements is watched by a test that reads the syntax tree: no
test on the values can see it, because on a live agent both orders answer the
same.

**The order of the causes is the certainty scale**, as it is in `trayStatus`:
each is tested only once those above it are excluded, so the earlier one sits the
less it depends on the ones below being wrong. It runs from the signalling, which
we can see all of, outwards to the network in between, which we can only infer —
that is, in the order the evidence gets thinner.

**But the middle of that scale is not a scale, it is a fork**, and the first
version had it as a scale. The questions below — a STUN server, a public address,
a check that went unanswered — are all about **crossing the Internet**, and they
say nothing about a viewer who never had to. Asked of whoever came in over the
**tailnet**, they blamed a guest Wi-Fi and then, one branch later, recommended
installing the very tailnet the session was already on. So the road in is settled
first, and only what really crossed the Internet is asked the rest.

**And the fork is taken on a code, not on the label.** `origin.Kind` is prose
written for a human reading the log — "Internet (Funnel)", "local network" — and
`Public` was the only thing beside it a decision could ask, which is a single bit
where there are five roads: it answered the same for the tailnet, the house and
an address that would not parse. `originClass` is the code, `Public` is derived
from it rather than stored alongside — a flag and a code saying the same thing
are two lists, and that one is read by `apiQR`, which decides whether a code is
engraved for the caller. The first-time setup asks the class directly, because
it wants one road and not the complement of another. The guard is a test that spoils every
fact at once and requires that no road which never left the house is handed an
Internet cause.

| what separates it | the cause | where the fault is |
|---|---|---|
| the page sent no candidates at all | `no-candidates-from-the-viewer` | the browser or the signalling, not the network |
| every one it sent was refused | `candidates-refused` | **ours** |
| the two are on the same private network | `blocked-on-the-local-network` | a guest Wi-Fi, client isolation, a firewall here |
| `stun_servers` is empty | `no-stun-configured` | a configuration that cannot work from outside |
| no `srflx` of our own | `stun-did-not-answer` | outgoing UDP blocked on this machine |
| no `srflx` of theirs | `the-viewer-has-no-public-address` | the network the watcher is on |
| checks sent, not one answered | `direct-path-refused` | the network in between — and the remedy is Tailscale on the watching device |

**And the last answer is "I do not know".** A plausible wrong cause sends whoever
reads it to spend an evening on their router for a fault in their phone, and this
line is read once, in the morning, by somebody who cannot re-run the failure: it
is the refusal `guard.site` makes about a stack frame it does not recognise. The
facts go into the line either way, so an unnamed cause is still a diagnosable
one.

**The causes are derived from the source, not listed in the test.** A table of
scenarios written by hand protects exactly what somebody remembered, so the guard
reads `noPathCause`'s own returns and fails if any of them is a sentence no test
ever makes the code say — with a floor under it, because a guard that has stopped
reading anything passes.

**The page still says two words, and that is a decision.** The watcher is holding
a phone in the dark and can do nothing about a symmetric NAT at three in the
morning; whoever can act is in front of the machine, in the morning, and that is
who the log is for. What the two readers need is not one sentence said twice —
it is the same distinction the tray and the page already keep.

### From the Funnel, `RemoteAddr` is the ingress node, not the visitor

Every request arriving from the Internet appears to come from the Tailscale node
that forwarded it: they are **the same address for everybody**. Progressive rate
limiting, which is per address, would treat the whole world as one person, and
whoever tries random passwords from outside would lock out whoever has it — a
simple way to switch the monitor off from the outside.

The real address exists, in `ipn.FunnelConn.Src`. It is not a header (those are
written by the caller): Tailscale puts it on the connection. It is recovered
with `ConnContext` on the HTTP server, going up from `tls.Conn.NetConn()`, and
ends in the context; `clientKey` prefers it to `RemoteAddr`. On connections
arriving from the tailnet the assertion does not hold and `RemoteAddr` is the
right one.

### Signalling that drops must not switch off the media

After negotiation the WebSocket is useless until there is something to
renegotiate: audio and video travel a road that no longer has anything to do
with it. But it stays **idle**, and an idle connection gets closed by
intermediaries: measured through the Funnel from mobile, **two sessions
interrupted at 2m34s each**, the same duration to the tenth of a second. A
network that drops does not drop twice at the same instant — that regularity is
always a timeout — and the state the connection ended in confirmed it: `closed`,
not `disconnected` nor `failed`, that is, it was not ICE that lost contact, it
was our `defer viewer.Close()`.

Two remedies, and **both** are needed: a ping every thirty seconds, so an idle
WebSocket does not look abandoned; and `CloseUnlessConnected`, so that the
signalling channel dropping does not touch a view that is working. The ping
alone makes the fault rarer, the second makes it harmless. Whoever really leaves
is reported by ICE, which loses consent within a few seconds.

### An encoder is judged only if the request was the constraint

The watchdog asks "I asked for less and did the output fall?". The question
makes sense **only if the smaller request is below what was already coming
out**: if the encoder was producing 114 kbit/s and is granted 309, nothing has
been taken from it, and the throughput is still decided by the scene. Observed
on a test machine's log, with a 640x480 webcam and a still room:

```
19:28:17  asked 775  produced 114
19:28:40  asked 309  produced 235  ->  "the encoder does not apply the bitrate"
```

Those are two numbers dictated by the scene, and in between the request never
touched the output. The cost of the false alarm is not a log line: that machine
moved to **rebuilding the encoder** for the rest of the session, that is, to the
manoeuvre that on AMD has already interrupted the capture. And thirty-four
seconds earlier the probe had established the opposite, that is, **a controlled
experiment overturned by an observation taken where the quantity was not
observable**: when the two contradict each other, the defect is in the one that
did not choose its conditions.

**Only the probe had not chosen its conditions either.** It asked for 1250 with
597 coming out: the request sat **above** the throughput, so it constrained
nothing, and the drop was produced by the scene — the conclusion "the command
takes effect" was right by accident. With a still scene it would have been the
opposite, and **permanent**: it halved the preset and judged, and in CBR on a
still scene the encoder always under-produces. The cost of a negative verdict is
**the whole process** on the road that rebuilds the encoder.

The rule therefore holds in both places, and is now in one function per side —
`probeVerdict`, twin of `bitrateSeen`'s guard, with `judgeable` saying "no
answer is possible here" instead of answering. And the expected drop is `start -
asked` and not "half of start": those are the same number only when the encoder
produces what it is asked for, which is precisely what the probe does not
assume. The remedy **has no threshold to tune**: a check counts only if the new
request sits below the throughput measured at the reference — either the ceiling
cuts what was coming out, or it is not a ceiling. Covered by `probe_test.go` and
`bitratewatch_test.go`, with the real log sequence inside.

**And the restore put back the preset instead of what was in force.** The probe
measures for eight seconds from the start of capture, and inside that window
somebody may have connected: from that moment the loop commands, so putting
`base` back means sending the maximum on a link just measured at 600 kbit/s —
and **it does not correct itself**, because the loop commands only when its own
state changes: it would believe it was at 600 while the encoder sat at the
preset, until the network moved. Now the value in force is remembered and
nothing is put back if somebody else has commanded in the meantime, which shows
in one way only — the bitrate in force is no longer the one the probe asked for.

### The port is taken before the camera

`ListenAndServe` does two things in one — takes the port and serves — and it sat
in an errgroup goroutine **beside** the one that starts the capture. A second
instance therefore opened camera and microphone, and *then* discovered the port
was already taken and exited. Observed on a user's machine, twice in the same
log, while the first monitor was retrying to open the same webcam:

```
10:26:45  microphone opened ... camera opened ... capture started
10:26:46  exited with error  listen tcp :8080: bind: ... only one usage ...
```

Contending for the device **with oneself** is the surest way to turn a double
click on the executable into a camera fault, and whoever suffers it has no way
of tracing the cause: the first monitor reports a webcam error, and the webcam
is fine.

So `net.Listen` is separated from `Serve`: the port is taken before the
errgroup, and whoever does not get it exits without having touched anything.
**The listen is also the only single-instance check we have**, and it is free —
whoever does not have the port is not this machine's monitor. Verified live: the
second instance exits in 135 ms without one device line, where before it wrote
three.

The `defer ln.Close()` is not redundant: `Shutdown` closes the listeners it is
serving, but if it arrives **before** `Serve` starts — they are different
goroutines — that exits immediately and the port would stay held by a process
that is exiting.

### Whoever retries decides how often, not how much gets written

A viewer that cannot connect retries, and the cadence of the retry is decided by
their browser. The refusal was written once per attempt: on that user's machine
that was **over a thousand identical lines in twenty minutes**, one a second,
covering the four lines that said what had actually happened. It is not a
verbosity defect: it is **the volume of our log placed in the hands of whoever
is on the other side.**

The right shape is the alerts' one: **write when the condition appears and when
it clears, never while it lasts.** The reason the refusals continue is already
in the log — the `capture-stopped` alert comes out on appearance and on recovery
— so the per-attempt line added nothing. The count is not lost: it comes back in
the recovery line (`viewers admitted again refused=42 lasted=1m20s`), where it
says **how long it lasted** instead of saying it is happening. The repetitions
stay at `-v`. Covered by `viewerrefuse_test.go`, and verified to catch: putting
the `Warn` back, the test fails declaring 500 lines over 500 attempts.

**And the same shape arrived from a library.** Tailscale repeats "restart with
TS_AUTHKEY set, or go to: …" **every five seconds** until somebody authorises
the device — forty identical lines in four minutes — and the address they carry
we already write ourselves, once, beside the phase. `UserLogf` therefore goes
through `sayOnce`, which suppresses **consecutive** repeats by comparing the
formatted text and not the format: two lines differing by a value are two pieces
of news.

The direction to remember is that the volume of the log is not decided by
whoever writes the line: it is decided by whoever makes it be written. It holds
for a viewer that retries, for an audio sender, and for a dependency.

### Access is slowed down, never blocked

Per-address blocking does not cover somebody trying passwords from many
addresses, and the Funnel URL is public. But the remedy **cannot be a global
block**: that would be a way to switch the monitor off from outside, by getting
the password wrong enough times.

So it is only slowed down, and with two properties that make the slowdown
harmless for whoever has the right to enter: **you pay only by getting it
wrong** — whoever types the right password gets in at once even in the middle of
an attack — and whoever already has a valid session cookie does not pass through
it at all. The pressure **decays**, that is, it is not a state one enters and
stays in. Covered by `auth_test.go`, where the test that matters is the one
showing that ten thousand attempts do not close the door.

**The per-address table only ever grew.** A record is dropped when that same
address knocks again after the window has passed, or on a login that works — and
an address that never comes back does neither, so the table held every address
that had ever got a password wrong, for the life of the process. Through the
Funnel those keys are the Internet's and are the caller's to choose. The session
store beside it has had a sweeper since it was written; this was the same shape
with the sweeper missing. It now sweeps when it is full, at 4096 records, and
**refuses newcomers rather than evicting the locked-out** — the other direction
is a way for whoever is serving a lockout to clear it by knocking from four
thousand other addresses, and a caller with four thousand addresses to spare was
already past a per-address limit by construction.

**And the slowdown is charged after the work it is meant to bound**, which is
deliberate and is why it cannot be the answer to the cost. The delay is paid by
getting the password wrong, and that is known only once argon2 has run: charging
it earlier would make whoever types the right password wait for somebody else,
which is the property this chapter is built on. So the ceiling is elsewhere —
`hashSlots`, four — and it bounds the **concurrency** rather than the rate: a
hundred requests arriving together used to ask this process for 6.4 GB, and four
slots is 256 MiB, the same order as the frame buffers already in flight. A
correct password waits at most for the hashes already running.

### Administrative commands are given from in front of the machine

`RevokeAllSessions` and `ResetPassword` **are not HTTP routes**, and that is
deliberate: they are administrative commands, and the proof of who you are is
physical presence, not a credential. A public URL cannot ask for it — and for a
password reset the only available credential would be the forgotten one. They
are called by the tray, where that presence exists by construction.

**Resetting the password opens a window, and it must be closed with it.** With
no password there is no authentication, so `/setup` is open to anyone who
reaches it: right at first start, where the only way to begin is for somebody to
set one. But resetting it while the tunnel is on, for the duration of that
window the public address means **"set the password and take the camera"** — the
first passer-by takes it, and the owner discovers they are locked out of their
own house. So `/setup` and `/api/setup` accept only a connection from this PC,
recognised with `requestOrigin` — that is, with `tunnel.SourceAddr` first, not
with a header, which the caller would write, so that the Funnel's visitor is
never taken for loopback. At first start it costs nothing: the guided setup is
opened on `localhost` by the program itself, and the Funnel cannot be on anyway,
because `CanExposePublicly` refuses it until a password exists. **It is
therefore a permanent rule and not a special case of the reset**, which is how
a protection survives somebody touching it without knowing its history.

**The first version refused only the Internet, and the house is not the owner
either.** It left the window open to every device on the Wi-Fi — a guest's
phone, a neighbour on a shared network — and the privacy policy, audited
against the code, had to say "anyone on your home network can set it". The
proof asked for is the one the tray's commands already rely on, physical
presence at the machine, and on the wire that is loopback. What it cost: the
tray panel used to keep the home address and its QR code with no password, as
the way of finishing the setup from a phone, and now hands out none, because a
code leading to a refusal is a false affordance.
`TestTheFirstConfigurationIsNotDoneFromAnotherDeviceAtHome` failed with the old
check put back.

**And "this PC" is not only loopback.** With `listen_addr` bound to one
interface there is no loopback listener: the tray opens that interface's
address, Windows sends the request from it, and a check that asked only for
loopback refused the one machine the setup exists for. A review caught it before
it shipped. A connection whose source equals the address it arrived on
(`http.LocalAddrContextKey`) is this PC too, and it cannot be produced from
another device, because a TCP handshake from the PC's own address completes only
on the PC. `TestTheFirstConfigurationIsDoneAtThisPCOnItsOwnLANAddress` failed
with the loopback-only check put back.

**And the browser of the person who owns the monitor will carry a submission
into that window on anybody's behalf.** The refusal above asks *where the
request came from*, and a form posted by somebody else's page comes from the
house: it is the owner's browser making the connection. `application/x-www-form-urlencoded`
is a *simple request* — no preflight, nobody's permission asked — and the
attacker never needs to read the answer, because the write has already happened.
So any page open in that browser could post a password of its own choosing to
the monitor on the home network, and then walk in from the Internet through the
Funnel, which a reset does not take down.

**Neither of the two things that look as though they cover it does.** The
session cookie is `SameSite=Strict`, and it protects none of these three,
because they are how a session is obtained and carry no cookie. The CSP's
`form-action 'self'` is enforced on the document that **contains** the form, so
it constrains our pages and says nothing about anyone else's — a rule that
governs the wrong end of the submission.

What does cover it is `fromOurOwnPages`, in `credentials`, which is the one
function all three routes go through. It reads `Sec-Fetch-Site`, which the
browser writes about itself and script cannot forge — `same-origin` is one of
our pages, `none` is a typed address or a bookmark, which no form submission
can be — and falls back to comparing `Origin` with the host for a browser too
old to send it. **`same-site` is refused along with `cross-site`**, and on a
`*.ts.net` address that is the point rather than pedantry: a sibling name under
the same suffix is exactly the neighbour being guarded against. A request
carrying neither header is let through, which is `curl`, the tests, and anything
that is not a browser — none of which is riding somebody's session, that being
the whole of what this is about.

**And a page can make itself the same origin, which every check above
believes.** A site whose DNS answer is switched to 127.0.0.1 after it has loaded
— DNS rebinding — makes the owner's browser connect from loopback, so the road
says *this PC*; and to that browser the page and the monitor are now one origin,
so `Sec-Fetch-Site` says `same-origin` and `Origin` equals `Host`, both being the
attacker's name. A security audit traced the whole chain: the password written,
a login, the public address read off `/api/status`, and the Funnel entered with
video, audio and talk-back. **What that name cannot be is one only this PC
answers to**, so the setup also asks what the request calls us — `namesThisPC`,
an address written as a number or `localhost`, which nobody else's DNS resolves.
The machine's own name is not on the list, being resolved by the router: whoever
types it at this PC is sent on to the address rather than refused.
`TestAPageUnderAForeignNameCannotSetThePasswordAtThisPC` failed with the name
check taken out. **The profiler has the same hole and the same answer**: bound
to loopback it kept other machines out and not other pages, and it now answers
421 to any Host that is not a loopback name.

**Nothing may be put in front of the port**, and that is the price of reading
presence off the socket. A reverse proxy on this PC — a separately installed
Tailscale client serving `localhost:8080`, for instance — delivers every caller
from loopback: the setup would take a password from the Internet through it, and
the limiter would see the whole world as one address. The README says so beside
`listen_addr`; a one-time token in the URL the tray opens would close it in code,
and was left for the day somebody needs a proxy, because it has to travel
through the guided path's first step as well.

**And `/api/setup` hashed before it refused.** argon2id is 64 MiB and about a
tenth of a second **by choice**, and that choice is a lever for whoever calls the
route: the check for a password already being set sat *after* the hash, so every
call on a monitor that was already configured — that is, every call for the rest
of the machine's life — bought an argon2 run before discovering there was
nothing to do, from anyone at home, with no limiter anywhere. The cheap refusal
now comes first and the route is throttled like `/api/login`. **The check inside
the store stays**, and must: two simultaneous first-time requests both pass the
cheap gate, and only the one held under the store's own lock stops the second
overwriting the first.

Changing the password with the old one **is** a route — `/api/password`,
authenticated — because there a credential exists. The current password is
required even from somebody who already has a session: a cookie is a bearer
token, it says "somebody had logged in from this browser" and not "it is you".
And it goes through the same attempt throttling as `/api/login`, otherwise it
becomes an unwatched oracle for guessing it. **On change all sessions die,
including the caller's**: if the password is being changed because it has fallen
into somebody's hands, leaving already-admitted devices alive defeats the point
of the change.

**`/ws` is not a page.** The two guards in `requireAuth` — no password, no
session — answered the same request differently: 303 to `/setup` for the first,
401 for the second. To a WebSocket handshake a redirect means nothing, the
client does not follow it and reports a generic failure, that is, the diagnosis
is lost. It was not a hole — in neither case is the connection upgraded — it was
the same rule applied by halves, and it showed only when the test for the other
branch was written. It is now decided by `answersWithAPage`, one function.

**And SameSite did not protect everything behind `requireAuth` either**, which
this chapter used to assert. It separates sites, not origins: another port on
this PC, or another node of the owner's tailnet, is the same site, and its
requests carry the cookie. From there a page could switch the crying detection
off, delete clips or open the Funnel, with nobody having pressed anything — the
monitor's one job undone in silence, and write-only, so nothing needs reading.
Every command behind `requireAuth` now goes through `fromOurOwnPages` too, and
reads are left alone, because a cross-origin page cannot read what they answer.
`TestACommandFromASiblingOriginIsRefused` failed with the check taken out.

**A session opened on the home network is not accepted from the Internet.** The
LAN listener is plain HTTP, so its cookie crosses the Wi-Fi in clear, and there
was one session store for every road: a cookie read off a shared network also
opened the public address, from anywhere, and sliding renewal kept it alive
after its reader had left. The owner's browser never does this by itself — the
home address and the public name are two hosts with two cookie jars — so what
is refused is a cookie somebody carried. The tailnet's sessions go everywhere,
being encrypted end to end, which is the phone with Tailscale switched off on
the way out of the house. The token is refused, not revoked: revoking would let
whoever holds a copy log the owner out.

**And the browser's copy slides with the server's.** The renewal was sliding on
the server and fixed in the browser, whose cookie took its `MaxAge` at login: a
week later the browser threw away a session the server still held, which is the
fixed expiry the sliding renewal exists to avoid. The cookie is handed out again
once it is half its life old.

### The Funnel does not come up without a password

`CanExposePublicly()` is the most important check in the application: the
published address is reachable by anybody.

### Outside access declares itself active only after passing through it

The tunnel wrote `remote access active` **three seconds after start-up**, and
the line was true: valid certificate, ingress grant arrived, serve config
accepted. Those are three **answers**, and it is the same distinction that holds
for the encoder — of a command, weigh the effect, not what answered.

From outside, that address did not exist. **Tailscale publishes the name in
public DNS only while the node is online with ingress active**, and publication
is not instant: measured, the name absent at +5 minutes from start-up and
present at +11, plus the negative cache of the watcher's resolver, whose TTL is
300 seconds. With the monitor **off** the name does not exist at all: whoever
tries from a phone does not get a connection error, they get "server not found"
— a symptom that looks nothing like a tunnel, and sends them looking for the
fault in their own network.

Now the monitor goes out to the Internet and tries to come back in
(`internal/tunnel/reach.go`).

- **It is not a phase, and that is a decision.** `running` goes on meaning what
  it has always meant, and the `phase === 'running'` comparisons across the two
  pages are untouched: one more phase would have had to turn them all into
  `running || verified`, which is precisely the class of defect `phases_test.go`
  watches. And it is a field because on a machine where the check cannot be made
  the phase would stay forever in a second-class state.
- **The check's route sits on the Funnel listener, not in the mux.** The tunnel
  owns the nonce and owns that listener, so the check is all on one side;
  registered in the server it would be reachable from home too, where it would
  prove nothing.
- **The nonce changes at every attempt**, otherwise a response cached by an
  intermediary would pass for a check made now. And a nonce that does not match
  sees the ordinary page: a route that answers differently to an attempted path
  is a detail given away to whoever is probing.
- **Reaching yourself is not a check, and is not a fault.** Where the Tailscale
  client is also installed — the development machine — MagicDNS resolves the
  public name to the tailnet address, the request arrives all the same and has
  crossed nothing. It is the most likely false alarm in the whole mechanism, and
  what separates it is the response itself: whoever arrived without a
  `SourceAddr` receives `direct` instead of the nonce.
- **Inside the grace a failure is not announced.** Fifteen minutes, and it is
  the window in which the name is not yet published: without it the alarm would
  fire at **every** start-up, and this is the only alarm the program has. It
  expires by itself, like the alerts' one and the microphone re-examination's.
  `TestTheGraceCoversTheMeasuredPublicationWindow` keeps it tied to the
  measurement: **a measurement written next to a constant that contradicts it
  protects nothing.**
- **The outcome enters through `remoteDown` and not through a second place.**
  That predicate is read by two — the banner and the icon — and written twice it
  would say different things about the same monitor.
- **"I do not know" is not "it does not work"**, and it remains for the machine
  where the check cannot be made: there the badge goes back to saying the phase
  and nothing is announced.
- **But the wait is declared.** In the first version the minutes between opening
  the tunnel and the first successful check were "I do not know", that is,
  silence, and the badge fell back to the phase — which says **"active"**. So in
  the one window in which the address really does not answer, the monitor went
  on declaring it ready: **the optimistic declaration removed from the outcome
  and left in the wait.** There is now `ReachChecking`, "checking", and the
  grace decides only when that wait becomes an alarm. Where the check cannot be
  made it stays "I do not know", because "checking" would promise an outcome
  that will never come.
- **The wait does not enter the log.** It is shown to whoever is watching now;
  whoever rereads in the morning is looking for what happened, and a wait
  resolved in eleven seconds is nothing happening — while it runs at every
  start-up. The two outcomes go to the file.
- **That the page really reads the outcome is watched by the catalogue**, in the
  direction that gets forgotten: if `app.js` stopped composing `viewer.reach.`,
  the three entries would become orphans and `internal/server` fails.
- **And the wait is explained in two places, because there are two readers.**
  Whoever installs meets it for the first time, and finds it in the last screen
  of the guided path beside the public address (`onb.s6.first-wait`), which is
  the principle of the cost declared before it is charged; whoever reopens the
  monitor after days meets it again — the name has to be republished at every
  switch-on — and finds it under the viewer's badge while the outcome is
  `checking` (`viewer.remote.checking`). They are two sentences and not one
  because they are two registers: the first explains to somebody who knows
  nothing yet, the second states a fact while it is happening.

**Verified live, both branches on the same machine**, which is the only way to
show that the branch protecting against the false alarm is not merely covering a
defect:

```
13:59:34  remote access active                       (hosts pointed at the ingress)
13:59:45  public address verified from the Internet  <- eleven seconds
14:00:52  remote access active                       (hosts restored)
14:01:02  cannot verify the public address ...       <- the direct branch
```

**And what diverts the name is not NRPT: it is the `hosts` file, where the
Tailscale client writes a line per tailnet node** — `100.x.y.z
patmon-1.<tailnet>.ts.net.` — which takes precedence over any DNS. Hence the
asymmetry that misled the diagnosis: `ping` and `curl` go through the Windows
resolver and see the tailnet, `nslookup` queries the DNS server and sees the
public ingress. **Two tools answering differently about the same name are not
contradicting each other: they are answering two different questions.**

What the live test **did not** cross is that window: there the name was already
cached by the resolvers, so publication was immediate, whereas the day before —
from a long-stopped node — it had taken more than ten minutes.

### The Tailscale node version is stamped by the linker

`tailscale.com/version` derives the version from `debug.ReadBuildInfo()`,
reading the commit's revision and date. If those fields are missing the library
**discards the information** and the node announces itself as
`1.102.1-ERR-BuildInfo`, which in the control panel reads as a defective client.
They are missing more often than it seems: building from a tree with no commit,
from a zip downloaded from GitHub, or from a shallow clone. Linker stamps depend
on none of that.

`version.longStamp` and `version.shortStamp` are the intended way — it is what
`build_dist.sh` does in Tailscale's source. They live in `build.ps1`, and the
version is asked of `go list -m tailscale.com` instead of being repeated,
otherwise at the first module update the node would declare the wrong version.

### Do not go below `tailscale.com v1.102.2`

**v1.102.1 breaks the Funnel** and does so silently. Tailscale's ingress nodes
are unsigned peers by construction; in that version `peerCapsLocked` strips
every capability from unsigned peers, including
`https://tailscale.com/cap/ingress`, and the node refuses public connections
with `peerapi: ingress: denied; no ingress cap`.

From outside the symptom says none of this: TCP opens and the TLS handshake gets
no answer. From the control panel everything looks fine — node connected, Funnel
badge, valid certificate — and our own logs at normal level say "remote access
active". It shows **only** with `-v`, because the refusal is written by the
Tailscale backend at debug level.

