package alerts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)

func codesOf(as []Alert) []Code {
	out := make([]Code, len(as))
	for i, a := range as {
		out[i] = a.Code
	}
	return out
}

// **The property the whole design rests on.** The viewer tells "still the one
// from before" from "a new one has arrived" by the id alone: if a fault lasting
// all night changed identifier on every turn, the page would chime every three
// seconds until morning.
func TestALastingFaultKeepsItsID(t *testing.T) {
	r := NewRegistry()
	r.Update(t0, []Code{CaptureStopped})
	first := r.Active(t0)[0].ID

	for i := 1; i < 10; i++ {
		r.Update(t0.Add(time.Duration(i)*time.Second), []Code{CaptureStopped})
	}
	after := r.Active(t0.Add(10 * time.Second))
	if len(after) != 1 {
		t.Fatalf("active alerts = %d, wanted 1", len(after))
	}
	if after[0].ID != first {
		t.Errorf("id changed from %d to %d: the page would read it as a new fault", first, after[0].ID)
	}
	if after[0].Since != t0.UnixMilli() {
		t.Error("the instant it appeared was rewritten: \"for twenty minutes\" would become \"just now\"")
	}
}

// And the other direction: going away and coming back **is** news again, and it
// has to have a new id, otherwise the second fault passes in silence.
func TestAReturningFaultIsNewsAgain(t *testing.T) {
	r := NewRegistry()
	r.Update(t0, []Code{MicSilent})
	first := r.Active(t0)[0].ID

	r.Update(t0.Add(time.Second), nil)
	if n := len(r.Active(t0.Add(time.Second))); n != 0 {
		t.Fatalf("the fault did not recover: %d active", n)
	}

	r.Update(t0.Add(2*time.Second), []Code{MicSilent})
	if id := r.Active(t0.Add(2 * time.Second))[0].ID; id == first {
		t.Errorf("id reused (%d): the second fault would not be announced", id)
	}
}

// Update receives the whole snapshot, so what is no longer there switches
// itself off without anyone having to switch it off. It is the property that
// makes the alert left lit along a forgotten branch impossible.
func TestWhatIsGoneClearsItself(t *testing.T) {
	r := NewRegistry()
	r.Update(t0, []Code{CaptureStopped, MicMissing, RemoteDown})
	if n := len(r.Active(t0)); n != 3 {
		t.Fatalf("active = %d, wanted 3", n)
	}

	r.Update(t0.Add(time.Second), []Code{MicMissing})
	got := codesOf(r.Active(t0.Add(time.Second)))
	if len(got) != 1 || got[0] != MicMissing {
		t.Errorf("active = %v, wanted mic-missing only", got)
	}
}

// A colour can say one thing only and it has to say the worst one: the order is
// part of the contract with the page, which has one banner.
func TestTheWorstComesFirst(t *testing.T) {
	r := NewRegistry()
	r.Update(t0, []Code{RemoteDown})
	r.Update(t0.Add(time.Second), []Code{RemoteDown, CaptureStopped})

	got := codesOf(r.Active(t0.Add(time.Second)))
	if got[0] != CaptureStopped {
		t.Errorf("first = %v, wanted capture-stopped: the notice covered the fault", got[0])
	}
}

// At equal severity the most recent one leads, which is the one the viewer does
// not know about yet.
func TestSameLevelMostRecentFirst(t *testing.T) {
	r := NewRegistry()
	r.Update(t0, []Code{MicMissing})
	r.Update(t0.Add(time.Second), []Code{MicMissing, CaptureStopped})

	if got := codesOf(r.Active(t0.Add(time.Second)))[0]; got != CaptureStopped {
		t.Errorf("first = %v, wanted the most recent one", got)
	}
}

// **An Event is not a third severity, and nothing was asking.** The comparator
// separates `Fault` from everything else and orders the rest by recency, which
// is what the `Event` constant claims in as many words — *"in the ordering it
// sits beside the notices"*. Every ordering test above uses two `Fault` codes,
// so the pair that could have disagreed — a Notice and an Event — never met:
// had somebody given events a tier of their own, the suite would have stayed
// green while a movement in the room covered "access from outside has stopped".
func TestAnEventRanksBesideANotice(t *testing.T) {
	r := NewRegistry()
	r.Update(t0, []Code{Motion})
	r.Update(t0.Add(time.Second), []Code{Motion, RemoteDown})

	// The notice is the more recent of two codes of equal standing, so it leads.
	if got := codesOf(r.Active(t0.Add(time.Second)))[0]; got != RemoteDown {
		t.Errorf("first = %v, wanted remote-down: the event was ranked as its own severity", got)
	}

	// And the other way round, so that the assertion is about recency and not
	// about which of the two levels was written first.
	r2 := NewRegistry()
	r2.Update(t0, []Code{RemoteDown})
	r2.Update(t0.Add(time.Second), []Code{RemoteDown, Motion})
	if got := codesOf(r2.Active(t0.Add(time.Second)))[0]; got != Motion {
		t.Errorf("first = %v, wanted motion: the notice was ranked above the event", got)
	}

	// And a fault still covers both, which is the half that must not be lost.
	r3 := NewRegistry()
	r3.Update(t0, []Code{CaptureStopped})
	r3.Update(t0.Add(time.Second), []Code{CaptureStopped, Motion, RemoteDown})
	if got := codesOf(r3.Active(t0.Add(time.Second)))[0]; got != CaptureStopped {
		t.Errorf("first = %v, wanted capture-stopped", got)
	}
}

// A code nobody listed counts as a fault. Being wrong this way costs one alert
// too many; the other way it costs a fault going unnoticed because somebody
// added a code and forgot the table.
func TestAnUnknownCodeCountsAsAFault(t *testing.T) {
	if l := LevelOf(Code("something-new")); l != Fault {
		t.Errorf("level = %v, wanted fault", l)
	}
}

// Every declared code has its level: the table is the only place it lives, and
// this test is what stops anyone adding one and forgetting it.
//
// **The list is read from the const block, not written here.** It used to be
// six codes typed out by hand while there were nine — `CameraOther`, `Cry` and
// `Bark` were watched by nobody — and the sentence above claimed the whole set,
// which is the shape this repository holds to be worse than a missing guard:
// whoever reads the claim stops reading the list.
//
// **And iterating `AllCodes()` would ask nothing at all.** That function is
// derived *from* `levels`, so the question "is every code in the table?" would
// be answered by the table itself and could never fail. The const block is the
// only other place the set exists, which is why it is read from the syntax tree
// — the same technique, and the same reason, as the `AllSteps` episode: a code
// added in Go and forgotten in the table reaches no guard downstream, because
// every one of them derives from `AllCodes()`. It arrives on the page as a bare
// key, with `LevelOf` lighting it as a fault nobody can read.
func TestEveryCodeHasADeclaredLevel(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "alerts.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	declared := map[string]token.Pos{}
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		// Only the constants declared `X Code = "..."`: the Level constants sit
		// in a block of their own and are not this set.
		id, ok := spec.Type.(*ast.Ident)
		if !ok || id.Name != "Code" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			declared[strings.Trim(lit.Value, `"`)] = lit.Pos()
		}
		return true
	})

	// A guard that has stopped reading anything passes. The floor is the count
	// at the time of writing: fewer means the shape it looks for has moved, and
	// green would mean nothing.
	//
	// **It came down from nine to eight with the self-test alert**, which was
	// removed because nothing could emit it: the floor is a count and it follows
	// the list, so lowering it here is the one legitimate reason to touch this
	// number — and this comment is what separates that from lowering it to make
	// a red guard go quiet.
	if len(declared) < 8 {
		t.Fatalf("read %d codes from the const block, wanted at least 8: "+
			"the guard is looking at the wrong shape and absolves everything", len(declared))
	}

	for code, pos := range declared {
		if _, ok := levels[Code(code)]; !ok {
			t.Errorf("%s: %q is declared and is not in the levels table: "+
				"AllCodes will not carry it, so no catalogue is asked for a word "+
				"and it reaches the page as a bare key",
				fset.Position(pos), code)
		}
	}
	// And the other direction, which is the one that goes stale quietly: a
	// table entry for a code nobody declares any more.
	for c := range levels {
		if _, ok := declared[string(c)]; !ok {
			t.Errorf("%q is in the levels table and is declared by no constant", c)
		}
	}
}

// The transitions returned are what ends up in the log on file, which is the
// only witness of a night when nobody was looking at the page.
func TestUpdateReportsWhatChanged(t *testing.T) {
	r := NewRegistry()

	appeared, recovered := r.Update(t0, []Code{CaptureStopped})
	if len(appeared) != 1 || appeared[0].Code != CaptureStopped || len(recovered) != 0 {
		t.Fatalf("appeared=%v recovered=%v", codesOf(appeared), recovered)
	}

	// The same fault on the next turn is not news: announcing it again would
	// fill the log with a line every three seconds until morning.
	appeared, recovered = r.Update(t0.Add(time.Second), []Code{CaptureStopped})
	if len(appeared) != 0 || len(recovered) != 0 {
		t.Errorf("a lasting fault produced a transition: %v %v", codesOf(appeared), recovered)
	}

	appeared, recovered = r.Update(t0.Add(2*time.Second), nil)
	if len(appeared) != 0 || len(recovered) != 1 || recovered[0] != CaptureStopped {
		t.Errorf("appeared=%v recovered=%v, wanted the recovery", codesOf(appeared), recovered)
	}
}
