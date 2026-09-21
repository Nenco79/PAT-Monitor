---
paths:
  - "internal/update/**"
  - "internal/version/**"
  - "cmd/pat-sign/**"
  - "cmd/pat-licenses/**"
  - "cmd/pat-icon/**"
  - "internal/icon/**"
  - "packaging/**"
  - "build.ps1"
---

Part of PAT Monitor's engineering record; the index that carries every
chapter, in order, is in `CLAUDE.md` at the root of the repository.

## A program in somebody else's house cannot be reached, so it asks

`internal/update`. Once a day the monitor asks GitHub whether a newer release
exists, and **that is all it does**: the answer reaches the notification area
and the log, and applying it is a gesture made in front of the machine.

**The fault being removed is not a bug, it is a silence.** This is a portable
executable somebody downloaded once and left running; when a defect is found and
fixed, there is no channel back to them. A baby monitor that runs unattended for
months is the program this matters most for and the one nobody thinks to go and
check on — so the one thing its owner cannot do is notice that a fix exists.

**It downloads nothing, and the reason is where the remedy is.** Whoever watches
from a phone can be told a newer version exists and can do nothing about it;
whoever is in front of the machine is the one who can act. That is the rule
`RevokeAllSessions` and `ResetPassword` already follow — administrative commands
are given from in front of the machine — and rewriting the executable is more of
an administrative command than either. The page therefore carries none of this:
`server.Status` is serialised to a phone, so the state is passed to `trayStatus`
directly rather than through it, and it cannot reach the viewer by an oversight.

**The third value of the answer is the load-bearing one.** `Unknown`, `Current`,
`Available`, and `Unknown` is the zero: no network, a refusal, a body that will
not parse, nobody has asked yet. Rendered as "you are up to date" — which is the
tempting shape, because it is what a boolean would give — it would tell somebody
their monitor is current on the night the fix they need was published. It is
`reach.go`'s tri-state and the same argument: **"I do not know" is not "no".**

**Two things are set from one answer, and they go to different places.** The
panel's row appears whenever a release exists, **including while something is
broken** — a monitor whose camera keeps stopping is exactly the one whose owner
wants the release with the fix, so hiding it behind the fault would take it away
in the hour it is worth having. The tooltip's sentence yields to everything:
`summary` gives a `Note` precedence over a `Fault`, so a note set
unconditionally would write "a newer version is available" over "the camera has
stopped", and the tooltip is brushed to find out whether one can go to bed. One
field would have answered the wrong one of those two questions.

**The balloon fires once per version, not once per answer.** The check runs daily
for the life of an installation, and a monitor that pops the same notice every
morning is a monitor whose notices stop being read — which costs more here than
anywhere else, because the other notifications this program sends are about a
child. The panel's row stays as long as the release does, which is where somebody
who dismissed the balloon finds it again. The log follows the same rule the
refused viewers already taught: written when the answer changes, never while it
stays the same, or it is 365 identical lines a year.

**The cadence is about somebody else's budget.** Unauthenticated GitHub allows on
the order of sixty requests an hour per IP address, and several installations in
one house share one. Daily leaves it untouched, the ETag makes an unchanged
answer a 304, and the first check is kept out of start-up, where the camera, the
microphone and the tunnel are all opening and the network may not be up at all.

**A build that matches no commit asks nothing.** With `Modified` set the binary
is the code of no release, and pointing its owner at a download would be telling
them to throw away whatever they were testing — the fact `version.Full` already
prints, acted on instead of displayed. Nor does the request go out at all when
`update_check` is false: the goroutine is not started rather than started and
made to decline, because a goroutine that exists in order to do nothing is
something somebody later has to read to discover that it does nothing.

**What leaves the house is declared**, because it is the reason somebody would
switch it off: one request a day carrying this machine's address and the version
it runs. Not the configuration, not the tailnet name, not who is watching.

**And it is not an alert.** `internal/alerts` is the set of what is **wrong
now**, and a newer version is neither wrong nor fixable from where the alerts are
read. It is a `Note` and not a `Fault` for the same reason: a `Fault` turns the
tray icon brick, that is, teaches whoever walks past the machine to read a
working monitor as broken — for a version number.

### The endpoint is the policy, and the belt underneath costs two comparisons

`/releases/latest` **excludes pre-releases and drafts**. That is the whole of the
pre-release policy: a version that should not be offered is a checkbox on GitHub,
not a constant here, and there is no second list to keep in step.

**The belt is `verdict` reading `prerelease` and `draft` anyway.** Two
comparisons against a promise this program does not control: if that endpoint
ever starts answering with a pre-release, the policy goes on holding instead of
failing in the one direction nobody would look at — more offered, not less. It is
the same shape as the quantiser's imposition and its refusal downstream: **what
protects is the refusal, which does not depend on anyone else obeying.**

**And the one-checkbox policy has to be seen to work, not assumed.** The live
test is to mark a release pre-release and confirm nothing is offered. A test that
only ever exercises the belt would pass on a machine where the endpoint had
quietly changed.

**And the belt had a hole the same shape as the one it was covering.** `verdict`
refused a release with **no** tag — "an unreadable answer is Unknown rather than
Current: saying you are up to date here would be a claim built on nothing" — and
let through a tag that is present and is not one of ours. That is the likelier
shape: `v1.0` with two fields, a date, a name, a tagging scheme changed one day.
`Newer` answers false to an older release **and** false to a tag nobody can
parse, so those fell into Current, and `Check` then stored the ETag — so the
monitor went on saying it out of memory, for as long as that release stayed the
latest.

It is not the permanent silence it looks like, and saying so is the point: a
real new release changes the document, so the ETag changes and the answer is a
200 rather than a 304. What it is, is **a monitor telling somebody they are up
to date on the strength of a document it had not understood** — and it becomes
permanent the day the tagging scheme is what changed, because then every future
tag is unreadable too. `version.Readable` now answers the question `Newer`
cannot: **"not newer" and "not a version" are the same answer and not the same
fact.**

### The key ships before anything reads it

`internal/update/pubkey.go` carries an ed25519 public key that **nothing
verifies**, and `build.ps1 -Release` signs every archive with the private half.
That looks like dead weight and is the opposite.

**A verifier can only trust a key the running binary already carries.** Were the
constant to first appear in the release that introduces the download, the first
version able to fetch and check an update would be the one *after* it. It costs
one file now, and it keeps that half switchable on for the installations that
exist rather than for whoever happens to be new enough.

**The first version of this paragraph priced that at "a manual hop for ever,
with nothing to say so", and the case does not give it.** Walked through: 1.0.0
carries the placeholder and only tells; the release that brings the downloader
brings the real constant with it; whoever is on 1.0.0 is told, fetches that one
by hand, and updates by itself from there. That is **one** hop — the same one
this phase asks of everybody, because this phase downloads nothing — and the
monitor does say so, saying so being the whole of it. Shipping the key early is
still worth one file, for that hop and for not having to decide twice which
signature to trust. It was not worth the sentence: **a cost written larger than
it is buys the decision it was meant to support**, and the reader who checks it
stops believing the ones beside it.

**What the signature is for, when the time comes.** TLS proves the bytes came
from GitHub unaltered; it does not prove they are ours. Whoever takes the account
publishes a release and every installation downloads it — and a `SHA256SUMS`
beside the archive closes nothing, because the same account publishes that too. A
key that has never been near GitHub is the only thing in the chain a compromised
account cannot forge.

**The cost is declared rather than discovered: lose the private key and no
installed monitor will ever accept an automatic update**, because the only remedy
is a new key, which can reach people only inside a release they would have to
install by hand. That is a backup problem, and it is somebody's actual job. A
second offline rotation key is deliberately **not** kept: one key with a real
backup is honest, two keys with one backup is theatre, and the spare would sit
unused for years and be lost the same way.

**An all-zero key is not a key**, which is why `IsSigned` exists. A verifier
handed the placeholder would refuse every signature — from outside, exactly what
a tampered download looks like, that is, the alarming diagnosis for the harmless
cause.

**The archive is the licence-compliant unit, and the bare `.exe` never was.**
Apache-2.0 §4 and libopus's BSD-3 both require the notices to travel with a
redistribution in binary form, so `-Release` stages `LICENSE`, `NOTICE` and
`licenses/` beside the binary — regenerating the tree first, because it is
derived from what is linked in and the folder is committed, which is the pair
that ages without a word. The README pointed at a bare `pat-monitor.exe` for as
long as that was the only thing published.

**And a release from a dirty tree is refused.** `r` names a commit, and a binary
that corresponds to none is an identifier that lies exactly when somebody is
trying to find out what they are running. It is cheap to refuse at the build and
impossible to correct once the archive is on the Internet.

**The job that may write holds nothing to write with, and that cost a
release.** The archive is cut by a workflow on a tag, in two jobs: the first runs
this repository's own code — the suite, `build.ps1`, the module graph — with a
token that can only read, and the second holds nothing but the zip and `gh`, and
is the only one granted `contents: write`. So the second deliberately has no
checkout. But `gh` works out which repository it is talking to from the git
remote of the directory it runs in, and in that directory there is none: the
first command died with `fatal: not a git repository`, and the guard above it
threw *could not list the releases* — the message accusing the listing rather
than the missing context. `GITHUB_REPOSITORY` is set by Actions on every job and
`gh` does not read it; the repository is handed over as `GH_REPO`. **The absence
was the point and the repair is one line**, which is the shape to expect whenever
a job is stripped of everything it does not need: what it no longer has includes
the things nobody thought to list.

## A package is a second shape of the same binary, and it updates itself elsewhere

`build.ps1 -Msix`. The archive and the package carry the same program and are
not the same artefact, and every difference below is one the format imposes
rather than a choice made for tidiness.

**The binary inside a package never asks GitHub.** There the updating belongs to
the Store, and a build that also asked would offer somebody a download it must
not install and cannot install — the remedy for a packaged program is a store
page, not a file. So `version.Packaged` is stamped by the linker and the
goroutine is not started at all, which is the rule that was already there for
the configuration key: a goroutine that exists in order to decline is something
somebody later has to read to find out it declines.

**It is a stamp and not a key, and the difference is the whole of it.** A key is
something a reader turns back on. A changed default is worse: an existing
configuration file carries `update_check: true` straight past it, so the one
installation that would go on asking is the one that has been there longest.
`asksAboutUpdates` takes both and answers with neither alone, and
`-show-config` goes through the same predicate — printed separately, that line
would go on announcing a check that no longer happens, and "off" over a
configuration that reads `true` is a line somebody reads as a defect and goes
looking for.

**The version is the release and not the build.** A manifest has four fields and
the fourth belongs to the Store, which leaves three for four numbers, so the
commit count is the one that goes: `major.minor.patch.0`. The file says which
build — the executable's fourth field and the log line — and the package says
which release. Two packages therefore cannot go out under one product number,
which is right rather than a limitation: they would be two different things
claiming to be the same release.

**Three things the format needs that the archive does not:**

- **the logos, drawn and not scaled.** The three sizes a manifest names are in
  no version of `Sides`, and the target sizes below 44 are the ones the taskbar
  and the app list ask for. Measured against a package without them, on the
  image the shell hands back: the distance from the drawing made at that size
  goes from 13.37 to 9.48 at 16 px, and from 9.35 to 5.02 at 32. What is left
  over is the shell's own treatment, the same for both, which is why neither
  reaches zero.
- **the resource index, or the small logos are dead weight.** Measured: a
  package carrying them and no `resources.pri` gives back an image identical,
  pixel for pixel, to one that does not carry them at all. `makepri` is part of
  producing a package, not a refinement of it. Its `priconfig.xml` is an input
  and is removed before packing.
- **the manifest, from a template whose version is a token.** A placeholder that
  parses — `0.0.0.0` — builds, installs and ships a package claiming to be older
  than every other, and the Store then refuses the one after it for not going
  up, with no error anywhere along the way. `{VERSION}` fails at MakeAppx
  instead, while somebody is standing there.

**The SDK is looked for and not written down.** The version folder under Windows
Kits changes with every SDK, and on a CI image it is whatever that image happens
to carry: a path spelled in the script is a build that works on one machine and
fails in somebody else's log.

**What comes out is unsigned, and that is not an omission.** A package submitted
to the Store is re-signed by the Store with a Microsoft certificate, so a
signature made here would be replaced — which is also why the MSIX road costs no
certificate while the EXE/MSI road costs a real one. The signature worth making
is for sideloading, and it wants a certificate this repository does not hold.

**And a dirty tree is refused, for the archive's reason.** `r` names a commit,
and a binary corresponding to none is an identifier that lies exactly when
somebody is trying to find out what they are running. It is cheap to refuse at
the build and impossible to correct once the package is submitted.

**What is not known is whether the fourth field really must be zero for a
desktop package.** The requirement is documented for UWP packages, and the two
pages about packaged Win32 apps neither repeat it nor contradict it. It is not
resolvable by reading and becomes measurable only at a first submission —
`major.minor.patch.0` is the shape that holds under either answer, which is why
it would be the choice even if the rule turned out to be the permissive one.

## Licences are collected from what is shipped, not from go.mod

`go run ./cmd/pat-licenses` rewrites `licenses/`. The project is **Apache-2.0**
(`LICENSE` and `NOTICE` at the root), and the sixty dependencies are all
permissive: MIT, two- and three-clause BSD, ISC, Apache-2.0. No copyleft.

**It starts from the linked packages, not from `go.mod`.** They are two
different lists: `go.mod` also carries what is needed only by tests or by
another operating system, and reporting the licence of code we do not ship is
not more scrupulous, it is noise that hides the real entries.

**Three things come from no module, and they are the ones that get forgotten.**
**libopus and its wrapper**: `internal/opuswasm/libopus.wasm` ends up in the
executable through a `go:embed`, and inside it are two works by two authors —
libopus, BSD-3-Clause, which requires the copyright notice in redistributions
**in binary form** too, and two hundred lines of C wrapper, MIT. **The Go
standard library**, statically linked: it is not a module, so `go list` does not
report it, and it comes with `PATENTS`, which goes **together with** the
licence. And **the Tabler icons**, pasted as paths inside the pages: other
people's work that `go:embed` puts in the binary and that no tool sees.

**The folder is generated and committed, and that is the combination that ages
silently**: one `go get` and the distributed licences are no longer those of the
distributed code, with no error anywhere. `licenses_test.go` asks the question
at every `go test`, both ways — a new dependency not in the folder, and a folder
left behind for a removed dependency. The second is the one that gets forgotten:
it is not dangerous, it is **false**, and the repository ends up declaring it
distributes code it no longer distributes.

**But the opposite direction has a way of being wrong that the test does not
cover.** When libopus's glue became ours, that code stopped being a module while
the compiled artefact went on travelling in the binary: the generator enumerates
**modules**, so it would have emptied the folder, and the test was asking to
regenerate — **obeying the test produced the defect**, with everything green.
The stricter rule that follows: **the opposite direction says "this folder is no
longer needed" only if the bytes have gone too.** When the two facts diverge,
the entry moves to `manually-added/`, which is precisely the list of what is
shipped without an automatic list knowing it.

## Dead code is found with a tool, not by eye

`go run golang.org/x/tools/cmd/deadcode@latest -test ./...` lists the functions
no path reaches, tests included. It is worth rerunning after every abandoned
experiment, because what is left behind is not harmless: **twenty-two
unreachable functions** on the first pass, among them the hot-reconfiguration
engines of two cancelled experiments — ready to rebuild the encoder on nobody's
behalf — plus `SourceReader.MediaType` and `VideoEncoder.OutputType`, that is,
the "ask the encoder how it is doing" that this file documents as **a liar**:
keeping them is an invitation to fall for it again, and whoever found them would
have no way of knowing they are the wrong road.

Deleting the first batch uncovers more: the second pass must give zero. And the
**unused** things the tool cannot see are the most insidious, because they are
not code but promises — see the three configuration keys that did nothing.

**And the command above gives zero while sixteen functions are unreachable from
the program**, which is this chapter's own defect and the guards chapter's first
failure: a guard that covers less than it claims. `-test` adds the test binaries
as roots, so a function **only a test reaches** is reachable — and a function
whose only caller is the test written to cover it is exactly what an abandoned
experiment leaves behind. The flag was not a mistake: without it the seven
authoritative code lists (`AllErrCodes`, `AllSteps`, `AllFaults` and the rest)
come back as dead every time, and a sweep that cries wolf seven times is a sweep
nobody runs.

So **both are run, and the difference is read**:

	go run golang.org/x/tools/cmd/deadcode@latest ./...        # from the program
	go run golang.org/x/tools/cmd/deadcode@latest -test ./...  # must be empty

The second must be empty, as it always had to. The first is never empty, and
what it may contain is fixed: the code lists the catalogue guards feed on, and
functions whose doc comment **says** they serve the tests — `WithSourceAddr` is
the model, and it says so in as many words, precisely because the only natural
way to reach that code is an `*ipn.FunnelConn` nobody can fabricate. Anything
else in that list is an accessor written for a reader that does not exist, and
the rule about those is the one this repository already applied to `setConf`,
`Config` and `UpdateConfig`: **until the reader arrives, the code for it must
not be there.**

**Sixteen was the first reading, and eight of them went.** `Registry.Add` ("for
the self-test"), `applog.Files` ("the local interface needs it"),
`Motion.Ratio`, `gguf.File.Names`, `Pipeline.KeyframeOnDemand`,
`ICEFacts.Signalled` and `Talkback.Stats` ("for the status page and for the
report") were accessors with a test keeping each one green and a doc comment
naming a reader nobody had written. They are gone, and so is what fed only
them: the `order` field in `gguf`, the two counters behind `Talkback.Stats`,
and the whole `test` alert code — which nothing could emit once `Registry.Add`
went, so it took its level and its four catalogue words with it.

**Three of those removals would have cost a test of real behaviour, and none
did.** The rule is the one the guards chapter states from the other side: what
is being removed is the **observer**, not the thing observed, so the test moves
to the thing. `Pipeline.KeyframeOnDemand` reported `kfServed`, which is what
decides `kfDeclaredBad`, and the four keyframe tests now read that field — same
package, same property, one indirection fewer. `applog.Files` was how the
rotation test enumerated the folder; it now builds the names the rotation
promises and asks whether they are there, which is the stronger question.
`ICEFacts.Signalled` was a sum of three fields, and the test adds them.

`globalLimiter.delay` stays, and it is the ninth line of that list for a reason
written in its own comment: reading the pressure **without adding to it** is the
only way to check the decay, and `fail` would move the thing being measured. It
declares itself a test helper, which is the model `WithSourceAddr` sets — and
that is what makes the list readable rather than a second inventory kept by
hand.

