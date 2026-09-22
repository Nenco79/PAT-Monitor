package server

import (
	"regexp"
	"strings"
	"testing"
)

// **"The audio is filtered" is a claim about a stream, and the page made it
// over an absence.**
//
// `rawAudio` says whether the *last* microphone open obtained WASAPI raw mode.
// It is written in one place, where the capture opens, and is deliberately not
// cleared when the capture stops — the road obtained is the road that will be
// obtained again, the same choice `Pipeline.Microphone` makes about the device
// name. So the field has two values and the question has three: `false` means
// both "raw was refused" and "nothing has ever been opened", and `true` outlives
// the microphone being unplugged.
//
// With no microphone at all the viewer therefore announced "Audio filtered by
// the system: raw mode is not active", and it was the only warning switched on,
// so it also won the box — the `microphone` source above it in `warningOrder` is
// the *viewer's* microphone, for talk-back, not the monitor's. The mirror case
// is on the same line: a microphone open in raw and then unplugged left
// "unfiltered audio: yes" over silence.
//
// The other consumers already ask the question in the right order — the guided
// path computes `micOk` first, the tray puts `mic-missing` in a case above
// `mic-filtered` — which is what makes the viewer a place that forgot, and not
// a decision. **The guard is therefore about the readers and not about the line
// that was wrong**: whoever reads `rawAudio` must, in the same function, ask
// whether there is a microphone.
//
// **What it reaches is the browser, and that is narrower than "every reader".**
// `rawAudio` is the JSON field, so this walks the scripts the pages load and
// nothing else; the Go side reads `Pipeline.RawAudioMode` and `Status.RawAudio`
// and is not held here. No `go/ast` guard is added for it, and that is argued:
// the tray asks through `micMissing(s)` and `pat-capture` through its own
// sample count, so a test demanding the words `MicrophoneActive` or
// `AudioActive` in the caller would accuse two correct readers — a guard that
// has to be taught its own exceptions protects the list, not the rule. It is
// written down instead, in `RawAudioMode`'s doc and in the chapter.
//
// It is coarse in the other direction too, and the limit is worth writing down:
// it looks for `microphoneActive` anywhere in the enclosing top-level function,
// not in the same expression, so a second unguarded read added to a function
// that happens to ask elsewhere would pass. What it does catch is the shape the
// defect had — a reader in a function that never asks — and it caught it: with
// the old line put back it fails on `app.js`.
func TestNothingSaysWhetherTheAudioIsFilteredWithoutAskingIfThereIsAudio(t *testing.T) {
	seen := map[string]bool{}
	readers := 0
	for _, c := range pagesAndScripts(t) {
		if seen[c.js] {
			continue
		}
		seen[c.js] = true

		src := withoutComments(readAsset(t, c.js))
		for _, at := range readsRawAudio.FindAllStringIndex(src, -1) {
			readers++
			fn := enclosingTopLevel(src, at[0])
			if !strings.Contains(fn, "microphoneActive") {
				t.Errorf("%s reads rawAudio in a function that never asks microphoneActive: "+
					"with no capture the field says \"filtered\" about a stream that does not exist",
					c.js)
			}
		}
	}
	// **The subject here is a presence and not an absence, but only just**: if
	// the field were renamed, every reader would stop matching and this test
	// would go green over a page saying whatever it liked.
	if readers == 0 {
		t.Fatal("nothing reads rawAudio: either the field has been renamed, or this test is watching nothing")
	}
}

var readsRawAudio = regexp.MustCompile(`\brawAudio\b`)

// topLevelStart matches what these files put in column zero, which is what top
// level means in them — the same convention `topLevelDeclaration` relies on.
// Braces are not counted: a JavaScript brace can sit inside a string, a
// template or a regular expression, and all three are in these files.
var topLevelStart = regexp.MustCompile(`(?m)^(?:async +)?(?:function|const|let|var|class)\b`)

// enclosingTopLevel returns the stretch of source from the top-level
// declaration that contains the offset to the next one.
func enclosingTopLevel(src string, at int) string {
	starts := topLevelStart.FindAllStringIndex(src, -1)
	from, to := 0, len(src)
	for _, s := range starts {
		if s[0] <= at {
			from = s[0]
			continue
		}
		to = s[0]
		break
	}
	return src[from:to]
}

// **And a mute is the same claim about the same stream.**
//
// `microphoneMuted` is written where the capture opens and describes the last
// open, exactly as `rawAudio` does, so it carries the same three-into-two
// squeeze: on a microphone that has since gone it is yesterday's answer, and a
// page that states it says *Windows has the microphone muted* — with a command
// offering the volume slider — about a device that is not in the machine.
//
// It is a second test and not a list inside the first, because the two say
// different things to whoever reads a failure: one is about a property of the
// audio, the other about a state of Windows. What they share is the machinery,
// which is why that lives in functions.
//
// **And it does not catch the defect it was written after, which is the thing
// to know about it.** The read that was wrong sits in the same function as
// `micOk`, which asks `microphoneActive` two lines down, so at this resolution
// the function is guarded and the expression is not — the coarseness its
// sibling already declares, met by the one case where it matters. Sharpening it
// to the statement would accuse the legitimate shape where the absence is
// returned early and the mute asked afterwards, that is, it would protect the
// list of exceptions rather than the rule.
//
// **What it does catch is a new reader in a function that never asks at all**,
// and that is verified rather than assumed: a `mutedSays(s)` added to `app.js`
// fails it by name. What found the other one was a review.

func TestNothingCallsAMicrophoneMutedWithoutAskingIfThereIsOne(t *testing.T) {
	seen := map[string]bool{}
	readers := 0
	for _, c := range pagesAndScripts(t) {
		if seen[c.js] {
			continue
		}
		seen[c.js] = true

		src := withoutComments(readAsset(t, c.js))
		for _, at := range readsMicrophoneMuted.FindAllStringIndex(src, -1) {
			readers++
			fn := enclosingTopLevel(src, at[0])
			if !strings.Contains(fn, "microphoneActive") {
				t.Errorf("%s reads microphoneMuted in a function that never asks "+
					"microphoneActive: with the device gone the page offers a volume "+
					"slider for a microphone that is not there", c.js)
			}
		}
	}
	if readers == 0 {
		t.Fatal("nothing reads microphoneMuted: either the field has been renamed, " +
			"or this test is watching nothing")
	}
}

var readsMicrophoneMuted = regexp.MustCompile(`\bmicrophoneMuted\b`)
