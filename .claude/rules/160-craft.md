These are the rules that hold wherever one is working, so this file declares no
`paths` and is the one slice loaded in every session. Part of PAT Monitor's
engineering record; the index that carries every chapter, in order, is in
`CLAUDE.md` at the root of the repository.

## Editing this codebase

### A doc comment on the wrong declaration does not complain

A paragraph that names `X` sitting on a `func Y` compiles, reads plausibly in a
diff, and makes `go doc` render it against the wrong symbol — and the documented
function then follows with no doc of its own, so it loses its own too. Fourteen
of them were found across three packages. The shapes recur:

- a block **split in half** by tests inserted between its two sentences, so its
  first clause documents somebody else and its second sits alone above the
  function it was written for;
- an **orphan** for a function that no longer exists, fused onto its neighbour
  and still asserting a behaviour the code has dropped;
- a paragraph that simply **slid** onto the declaration above or below during a
  move.

**The rule is the one Go already states: the first word of a doc comment names
the declaration it sits on.** Nothing enforces it, and the only thing that finds
it is reading the paragraph and the `func` line under it together — which is
what reading a file whole forces and what reading a diff does not.

**And a comment can be stale in the same way a doc can be misplaced.** Two
comments in the same file can contradict each other and neither is wrong on its
own; what shows it is reading them one after another. Before carrying a claim
across a rewrite, check it against the code: several claims here were assertions
a later measurement had already overturned, and the moment somebody rewrites a
line is the first time in a year anybody reads it.

### A text substitution is not a rename

A renamer that works on the syntax tree touches identifiers and **not comments
or strings** — which is precisely the property that stops it ruining prose. A
`sed` does not have it, and the files it can damage are exactly the ones it was
not aimed at, where a Go identifier is indistinguishable from an ordinary word.
A tree-wide substitution of one identifier once landed inside string literals in
a test that had nothing to do with it: it compiled, the tests passed, and the
failure messages were gibberish.

The corollaries, both paid for:

- **a rename can land on the wrong receiver.** The same method name can exist on
  two types, one forwarding to the other; at the call site they look identical,
  and only the receiver's package says which is being renamed.
- **a rename can collide with a name in another file and kill a whole page.**
  See below.

**Verify a comment-only edit mechanically.** Strip the comments from the old and
the new file and diff what is left; an empty diff means the code is untouched,
and a non-empty one has to be exactly the intended renames. For a file of
numbers — a baseline — strip the decimal and thousands separators, extract every
digit run, sort, count, and diff against the same treatment of the old file.
Identical means no number moved.

### A grouped selector is not a line

Removing `#transport` from the stylesheet, a script deleted the two lines that
named it. Both were the **second** member of a grouped selector, and each carried
the declaration block:

	#viewers,
	#transport { color: #C8C0B3; font-weight: 400; }
	#viewers:empty,
	#transport:empty { display: none; }

What was left is `#viewers, #viewers:empty, .dot { … }` — **valid CSS**, which is
why nothing complained: the page parsed, every test stayed green, and the viewer
count became a 9 px grey circle with the number crammed inside it, beside a
second dot that `:empty` no longer hid. The two rules it needed had been carried
off by the name of a third element.

**The edit asserted the line was present and unique, and it was**, which is the
whole trap: a line-oriented edit cannot see that a line is half of a rule, and in
CSS the half that carries the meaning is the last one. It is "a text substitution
is not a rename" in a language with no compiler — and the remedy is the same
shape, to edit the **rule** rather than the line, then read back the block that
remains. What caught it here was a review; what confirmed the repair was a diff
against the file before the removal, so that the only difference in that region
is `#transport` and nothing else moved.

**No guard is added, and that is argued.** The broken form was valid CSS and
valid HTML, so no parser separates it from the good one; what separates them is
what the browser computes, and that is measured by driving one rather than by
reading a sheet. The rules the automatic tests do enforce on these files —
tokens, hovers, `[hidden]`, the palettes — all watch things a sheet can be asked
about. This one cannot, so it is written down instead of pretended.

### Two classic scripts on one page share one lexical scope

Two top-level `const`, `let` or `class` of the same name in two classic scripts
on one page are a redeclaration: **the second file then fails to parse in its
entirety.** Not one line of it runs. With `function` or `var` it is legal and
the later one silently replaces the earlier, which is worse to find.

The recordings page kept its title and its translated words — the first script
had already run — and lost the listing, the player, the four commands and the
summary line. **Nothing caught it**: `go vet` and `go test` were green, `node
--check` per file is green because each file is valid on its own, and the
browser says so only in its console. **Parsing the files one at a time cannot
see it; parsing a page's scripts together can**, which is what
`TestNoTwoScriptsOnAPageDeclareTheSameName` does.

The fix, though, is the older rule and not the test: **there is one language and
`i18n.js` owns it.** Two declarations of the same value were the defect; the
collision was only the day it started shouting.

### The same character is harmless in a comment and fatal in a string

`build.ps1` has no byte-order mark, so Windows PowerShell reads it as ANSI. The
em dashes this file's prose is full of are three UTF-8 bytes each, and in a
**comment** those three bytes are three harmless characters nobody ever sees —
which is why the script has carried them for its whole life. Put one inside a
double-quoted **string** and the parser loses the closing quote: *missing string
terminator*.

**And it does not accuse the line it is on.** Measured, one em dash inside one
`Write-Host` produced six errors, the first pointing at a `<` two lines further
down, then a missing `}` fifty lines up, then another two hundred lines up, and
finally a `try` with no `catch` at the end of the file. The guilty line is in
none of them. What found it was parsing the string on its own — it was valid —
and then a one-line file with an em dash in a string and no BOM, which was not.

The rule that follows is cheap: **outside comments, ASCII.** The alternative is
to give the file a BOM, which would remove the cause rather than avoid it and is
the road this project usually takes; it is not taken here only because nobody
has needed a non-ASCII character in a string, and a byte added to the front of a
file is a decision about bytes other tools have opinions about.

**What catches it is running the script under the parser that breaks**, and that
is a narrower claim than it looks. `gofmt` and `go vet` do not read PowerShell.
CI running `build.ps1` catches it **only because that step says `shell:
powershell`**: on a GitHub runner `pwsh` is PowerShell 7, which reads a BOM-less
file as UTF-8 and parses the broken form quite happily. A workflow on `pwsh`
would have stayed green over a script the machine that cuts releases cannot
start — the guard covering less than it claims, which is the first failure the
guards chapter lists.

## Guards, and how they fail

Nearly every test in this repository that watches a rule rather than a value
fails in one of four ways. All four have happened here.

- **A hand-written list of what to check protects exactly what somebody
  remembered.** The page/script pairs in `web_test.go` were written by hand, so
  the recordings page — which arrived later — was watched by nobody, on two
  guards at once. They now read the pairs from the markup, like `assets_test.go`
  and `palette_test.go` already did. Wherever a guard needs to know "all the X",
  derive the list from the thing itself; a list in the test is the second list,
  and it diverges silently and greenly.
- **A guard built on a list of known words absolves everything it has never
  met.** One existed here and was removed: the green it produced was worth far
  less than it looked, and a guard that sells confidence it does not have is
  worse than no guard.
- **A test that searches for a phrase passes vacuously once the phrase moves.**
  Search for the declaration, not for the prose around it, and where possible
  ask the syntax tree instead of the lines — `go/ast` guards live in
  `cmd/pat-monitor/traystate_test.go`, `internal/pipeline/micgrace_test.go`,
  `internal/rtc/sentwindow_test.go`, `internal/server/errors_test.go` and
  `internal/server/status_test.go`. A guard that reads comments must **strip
  them first**: the remedy to a defect tells its story, and the story names the
  very thing being searched for.
- **A test written by the same mistake it should catch catches nothing.**
  Whenever a test is written for a defect that has just been fixed, **put the
  defect back and watch it fail.** Several here have a note saying it was done;
  the ones that matter most are those whose subject is an absence, because a
  passing test and a test that asks nothing look identical.

**And a test must not ask the system for anything it can be given.** A function
that interrogates the operating system cannot be tested, only run — and run
inside a constructor it runs for whoever wanted to test the rest, on the disk of
whoever runs the tests. Hand the answer in as a parameter and test both
directions.

**What cannot live in `go test` says so and lives in `baselines/`**: anything
wanting gigabytes of somebody else's data or somebody else's executable. A test
that went looking for them on disk would pass here and be skipped everywhere.

## Method, as it turned out to be necessary

**Measure before hypothesising.** On audio latency, eight plausible hypotheses
were disproved by measurement, one after another. Whenever a change "makes no
difference", the first suspicion is that the wrong stream is being watched, or
that something is hiding the measurement.

**Beware buffers that hide what you want to measure.** 1 MB of pipe made our
writes regular whatever happened downstream: **the producer's measurement proves
nothing about the consumer if there is a reservoir between them.**

**A wrong measurement always accuses somebody else.** It appears three times in
this file — the CPU profile that accused the tray, the Opus sweep that accused
the codec, the handle counter that accused the icons — and every time the
accused component was fine.

**Two programs can get the same thing wrong.** That another implementation fails
identically is not evidence about where the defect is.

**An experiment that fails at both ends does not show where the defect is** —
only that neither of the two works.

**Before declaring the fault is outside, raise the log level of what you
control.** One fault was filed as somebody else's for days on the strength of an
isolation test that seemed decisive; the cause was a version of one of our
dependencies, and the answer had been in a line the backend writes at debug
level all along.

**For external services, ask them instead of deducing.** The Funnel instructions
had been hand-written by deducing them from missing capabilities. Tailscale
exposes `QueryFeature`, which answers according to the tailnet **and the
reader's role**: less code, and right instructions in situations we did not
foresee. The same holds for the system — the videos folder, the local address,
the preferred UI languages.

**External prerequisites are detected and explained, never assumed.** Whoever
installs a baby monitor is not whoever wrote it.

**Run what you touched.** After a change that crosses the tree, running the
tools costs a minute and sees things that cannot be searched for: a `grep` looks
where it already knows to look, while eight seconds of `pat-capture` puts the
contradiction on the first screen.

**A correction that reduces the frequency of a fault looks exactly like one that
removes it**, and telling them apart takes re-measuring, not rereading the diff.

**When every occurrence of a fault carries the same condition, that condition is
not background: it is the question.**
