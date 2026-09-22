# images

The pictures this repository shows and does not read.

| file | what it is |
|---|---|
| `social-preview.png` | the card that is shown when somebody unfurls the repository's link. It is uploaded by hand in **Settings -> General -> Social preview** and GitHub serves it from its own CDN, so nothing in the tree reads it. |
| `social-preview.html` | the source of that card. Self-contained: no script, no external sheet. Open it in a browser and export the 1280x640 box. |
| `header.png` | the strip at the top of `README.md`. |
| `header.html` | its source, on the same terms as the card's. |
| `onboarding-choice.png` | the fork in the guided path, the one screen where the person is asked something rather than told. |
| `onboarding-ready.png` | the last screen of the guided path, with both addresses and both QR codes. |
| `recordings.png` | the clip list, in the night palette. |
| `viewer.jpg` | the page the monitor is watched on, live. The one picture here that shows the program doing its job rather than being set up. |

**The two drawn ones are rendered, not photographed**: `social-preview.html` and
`header.html` opened in Edge at their own box with a device scale factor of 2.
The header is exported with `--default-background-color=00000000`, because its
top corners are rounded and a rounded corner leaves its four pixels to whatever
is behind: exported over a ground they come out as notches, and the header's own
comment says so at length. They are the only images here that can be made again
from what is committed beside them, which is why they carry a source and the
screenshots carry a paragraph instead.

**Where the three screenshots came from**, because otherwise nobody can repeat
them: the build that printed `1.0.0 r2` — from the history this repository
carried before it was started again on 17 September 2026, so the commit that
build named is not in this one — on Windows 11, Edge driven headless over the debugging
protocol at 1180x1000 with a device scale factor of 2, captured whole and then
cropped to end on a whole element and scaled to 1400 wide. A picture cut across
the middle of a card reads as a broken layout rather than as a crop.

**`viewer.jpg` was photographed and not driven, because it cannot be driven.**
The other three are pages a headless browser can be pointed at; this one is
worth a picture only while there is a real camera pointing at a real room, so it
is a screenshot of the viewer taken by hand from the running monitor — same
build, full screen so that no address bar comes into it — and then scaled to
1400 wide like the rest. What is on screen is the live state: the barking
detection is the one lit because it is the one switched on, and the dog was
asleep on the sofa.

**And it is the one that is not a PNG.** Three quarters of it is a photograph
from a webcam, which PNG stores at **1.34 MB** against **118 kB** as a JPEG of
the same 1400 pixels — eleven times the weight of a file that is committed for
ever, for a difference nobody can see in a picture whose subject is a sofa. The
others stay PNG for the opposite reason: flat colour and text, where JPEG is
the format that shows.

**One string in `onboarding-ready.png` is not the one the program produced**,
and this is the whole of the substitution: the public address was replaced with
`https://patmon.quercia-lieve.ts.net` and its QR code regenerated from that same
text through the program's own `/qr` endpoint, so the code and the line above it
still agree. **Everything else in that picture is the real state** -- outside
access was actually up, on a real tailnet, and nothing was staged to make it look
so. The substitution exists because a tailnet name is publicly resolvable DNS
tied to an account: it must not enter this repository, least of all inside a QR
code, where no `grep` would ever find it. `patmon` rather than the
`patmon-1a2b3c` the rest of the documentation uses, because that one is five
characters longer than the real name and wraps inside the field, which looks
like a defect in the page.

The home address is this machine's own, on its LAN. It is private, behind NAT
and routable from nowhere, and the bind is explicit for that reason: left to
choose, the program picks an address among the interfaces the system offers, and
this machine also holds one in `100.64.0.0/10`.

**They go stale in silence, and that is not fixable here.** A test can ask a
page whether it still declares a token, still hides what `[hidden]` marks, still
names every configuration key. Nothing can ask a PNG whether it still looks like
the page it claims to show. When one of those pages changes, nothing regenerates
these and nothing complains: somebody has to remember, and this paragraph is the
whole of the mechanism.

**And the two drawn sources have moved ahead of their pictures, deliberately.**
`--muted` went from `#6E6659` to `#6A6256` for contrast, and both sources carry
the new value, since the header's own comment says it is the file that goes
stale with nobody saying so. `header.png` and `social-preview.png` were **not**
re-rendered, and the reason is a measurement rather than an omission: rendering
the **unchanged** `header.html` here reproduces the layout exactly — every shape
in the same place — and differs from the committed picture on **24 318 pixels of
5.8%**, all of them glyph and curve edges, because the antialiasing is that of
another browser build and another export. So a re-render is not a no-op: it
would change every edge in the image to carry a four-unit shift in one grey that
nobody can see at 22 px. **A tool that cannot reproduce what is already there
has not earned the right to replace it** — whoever next regenerates these for a
real reason will pick the new value up with them.
