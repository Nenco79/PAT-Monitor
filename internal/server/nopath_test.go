package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"patmonitor/internal/rtc"
)

// sane is a session that got as far as it can get without connecting: the page
// sent its addresses, both ends know their public one, and the checks went out.
// Each case below spoils exactly one thing in it, which is what makes the cause
// attributable to that thing and not to the shape of the fixture.
func sane() rtc.ICEFacts {
	return rtc.ICEFacts{
		LocalHost: 2, LocalSrflx: 1,
		RemoteHost: 2, RemoteSrflx: 1,
		Pairs: 4, Knocked: 4, Answered: 0,
		Sent: 5, Refused: 0, STUNUsable: 2,
	}
}

var (
	outside = origin{Kind: "Internet (Funnel)", Addr: "203.0.113.7", Class: originInternet}
	home    = origin{Kind: "local network", Addr: "192.168.1.42", Class: originLocal}
	thisPC  = origin{Kind: "this PC", Addr: "127.0.0.1", Class: originThisPC}
	tailnet = origin{Kind: "tailnet", Addr: "100.71.3.9", Class: originTailnet}
	nowhere = origin{Kind: "unknown origin", Addr: "?", Class: originUnknown}
)

// origins is every road a request can arrive by, derived from the constants
// rather than listed: a road added without a branch in noPathCause would
// otherwise be judged by the Internet chain, which is the defect this table was
// rewritten for.
var origins = []origin{outside, home, thisPC, tailnet, nowhere}

func TestEachCauseIsNamedByTheFactThatSeparatesIt(t *testing.T) {
	cases := []struct {
		name  string
		facts func(f rtc.ICEFacts) rtc.ICEFacts
		from  origin
		want  string
	}{
		// The signalling is upstream of every road, so it answers the same from
		// a tailnet as from a mobile network.
		{"the page sent nothing", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.Sent, f.Refused = 0, 0
			return f
		}, tailnet, "no-candidates-from-the-viewer"},

		{"we refused all of them", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.Refused = f.Sent
			return f
		}, outside, "candidates-refused"},

		// Some refused is not all refused: the session went on with what was
		// left, so the cause lies further down.
		{"we refused some of them", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.Refused = f.Sent - 1
			return f
		}, outside, "direct-path-refused"},

		{"the same house", func(f rtc.ICEFacts) rtc.ICEFacts { return f },
			home, "blocked-on-the-local-network"},

		{"the same machine", func(f rtc.ICEFacts) rtc.ICEFacts { return f },
			thisPC, "blocked-on-the-local-network"},

		// A tailnet viewer crossed no NAT and needed no public address, so every
		// question below is the wrong one — and the Internet chain would end by
		// recommending the tailnet they are already on.
		{"already inside the tailnet", func(f rtc.ICEFacts) rtc.ICEFacts { return f },
			tailnet, "blocked-on-the-tailnet"},

		// A STUN failure is a fact about this machine and says nothing about a
		// viewer who never had to leave the house.
		{"no stun, but from the same house", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.STUNUsable, f.LocalSrflx = 0, 0
			return f
		}, home, "blocked-on-the-local-network"},

		{"an address that would not parse", func(f rtc.ICEFacts) rtc.ICEFacts { return f },
			nowhere, "unknown"},

		{"no usable stun server", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.STUNUsable, f.LocalSrflx = 0, 0
			return f
		}, outside, "no-stun-configured"},

		{"stun configured and silent", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.LocalSrflx = 0
			return f
		}, outside, "stun-did-not-answer"},

		{"only their own network", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.RemoteSrflx = 0
			return f
		}, outside, "the-viewer-has-no-public-address"},

		{"nobody answered a check", func(f rtc.ICEFacts) rtc.ICEFacts { return f },
			outside, "direct-path-refused"},

		// Both sides complete, and not one pair was ever knocked on: nothing
		// here accuses anybody, and saying so is the answer.
		{"no check was ever sent", func(f rtc.ICEFacts) rtc.ICEFacts {
			f.Pairs, f.Knocked = 0, 0
			return f
		}, outside, "unknown"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cause, remedy := noPathCause(c.facts(sane()), c.from)
			if cause != c.want {
				t.Errorf("cause = %q, want %q", cause, c.want)
			}
			if remedy == "" {
				t.Error("a cause with nothing to do about it")
			}
		})
	}
}

// A cause nobody exercises is a sentence that will be read once, in the morning,
// by somebody who cannot reproduce the failure — so the table above is not
// allowed to be the hand-written list that protects only what somebody
// remembered. The list of causes is derived from the function itself.
func TestNoCauseGoesUntested(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "nopath.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	declared := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "noPathCause" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok || len(ret.Results) == 0 {
				return true
			}
			lit, ok := ret.Results[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				declared[s] = true
			}
			return true
		})
		return false
	})

	// A guard that reads nothing passes, and that is what a refactor turns this
	// into silently.
	if len(declared) < 8 {
		t.Fatalf("only %d causes read from the source: %v", len(declared), declared)
	}

	produced := map[string]bool{}
	for _, from := range origins {
		for _, spoil := range []func(rtc.ICEFacts) rtc.ICEFacts{
			func(f rtc.ICEFacts) rtc.ICEFacts { return f },
			func(f rtc.ICEFacts) rtc.ICEFacts { f.Sent, f.Refused = 0, 0; return f },
			func(f rtc.ICEFacts) rtc.ICEFacts { f.Refused = f.Sent; return f },
			func(f rtc.ICEFacts) rtc.ICEFacts { f.STUNUsable, f.LocalSrflx = 0, 0; return f },
			func(f rtc.ICEFacts) rtc.ICEFacts { f.LocalSrflx = 0; return f },
			func(f rtc.ICEFacts) rtc.ICEFacts { f.RemoteSrflx = 0; return f },
			func(f rtc.ICEFacts) rtc.ICEFacts { f.Pairs, f.Knocked = 0, 0; return f },
		} {
			cause, _ := noPathCause(spoil(sane()), from)
			produced[cause] = true
		}
	}

	for cause := range declared {
		if !produced[cause] {
			t.Errorf("the code can answer %q and no test ever makes it", cause)
		}
	}
}

// **The network chain must not be reachable from a road that never crossed a
// network**, which is the finding this file was rewritten for: with the branch
// asked only whether the origin was public, a tailnet session was told its
// trouble was a guest Wi-Fi, and a failing one at home could be blamed on a STUN
// server it had no use for. Spoiling every fact at once is the strongest form of
// the question — whatever is wrong, a viewer who never left the house is not
// told to go and look at the Internet.
func TestNoInternetCauseIsGivenToAViewerWhoNeverLeftTheHouse(t *testing.T) {
	internetOnly := map[string]bool{
		"no-stun-configured":               true,
		"stun-did-not-answer":              true,
		"the-viewer-has-no-public-address": true,
		"direct-path-refused":              true,
	}

	broken := rtc.ICEFacts{Sent: 5, Pairs: 4, Knocked: 4}
	for _, from := range []origin{home, thisPC, tailnet, nowhere} {
		cause, _ := noPathCause(broken, from)
		if internetOnly[cause] {
			t.Errorf("a %s session was told %q", from.Kind, cause)
		}
	}
}
