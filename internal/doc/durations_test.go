package doc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The two durations this program repeats on purpose, and the guard that was
// missing on both.
//
// **Twenty milliseconds is declared four times** - `audiocodec.FrameDuration`,
// and an `opusFrameDuration` each in `internal/rtc`, `internal/record` and
// `cmd/pat-capture` - and **a hundred milliseconds three times**:
// `pipeline.levelBlockDuration`, `analysisBlockDuration` in `cmd/pat-capture`
// and `blockDuration` in `cmd/pat-sounds`. Every one of the seven carries prose
// promising it equals the others, and until this file no test compared any two.
//
// **The repetitions are argued, and that is exactly why the comparison is the
// defect.** `internal/record` must not import `internal/rtc` for a number;
// `cmd/pat-capture` is an instrument that has to be able to measure without the
// monitor's packages, and its own comment says the copy is deliberate because
// "a reference would make it true by construction"; `cmd/pat-sounds` repeats the
// block because "a measurement made with other numbers would measure a gate
// that does not exist". None of those arguments survives the numbers actually
// diverging - and what it costs is not cosmetic: if the frame the encoder
// produces and the duration the hub declares on the packet disagree, audio time
// runs faster or slower than real time, which is not a counter that goes red
// but a delay that grows. The block is worse still, because the gate's whole
// tuning was measured on that grid.
//
// **The list is derived from the tree, not written here.** A guard carrying its
// own seven paths would protect exactly what somebody remembered, which is the
// first entry of "Guards, and how they fail"; this one walks the source and
// recognises the copies by the names they are all given. A copy named by the
// same convention is covered with nothing to remember, and the floor below
// makes a guard that has stopped recognising anything fail instead of pass.
//
// The one exception is written by name rather than by prefix:
// `defaultVideoFrameDuration` in `internal/rtc` matches the spelling and is a
// **video** frame, a different quantity that has nothing to do with an Opus
// packet.
const notAnOpusPacket = "defaultVideoFrameDuration"

// authority names, per family, the declaration all the copies promise to equal.
// It is read from the source like all the others, so this test holds no number
// of its own: what it asserts is agreement, which is the property.
var authority = map[string]string{
	"opus packet":    "internal/audiocodec/audiocodec.go:FrameDuration",
	"analysis block": "internal/pipeline/pipeline.go:levelBlockDuration",
}

// family says which of the two a constant belongs to, or "" for neither.
func family(name string) string {
	if name == notAnOpusPacket {
		return ""
	}
	l := strings.ToLower(name)
	switch {
	case l == "frameduration" || strings.HasSuffix(l, "opusframeduration"):
		return "opus packet"
	case strings.HasSuffix(l, "blockduration"):
		return "analysis block"
	}
	return ""
}

type durDecl struct {
	where string
	d     time.Duration
}

// durationOf evaluates the two shapes these constants are written in,
// `N * time.Unit` and `time.Unit / N`, and refuses anything else rather than
// guessing: a shape it cannot read would otherwise be silently absolved.
func durationOf(e ast.Expr) (time.Duration, bool) {
	bin, ok := e.(*ast.BinaryExpr)
	if !ok {
		return 0, false
	}
	unit := func(x ast.Expr) (time.Duration, bool) {
		sel, ok := x.(*ast.SelectorExpr)
		if !ok {
			return 0, false
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "time" {
			return 0, false
		}
		switch sel.Sel.Name {
		case "Nanosecond":
			return time.Nanosecond, true
		case "Microsecond":
			return time.Microsecond, true
		case "Millisecond":
			return time.Millisecond, true
		case "Second":
			return time.Second, true
		}
		return 0, false
	}
	num := func(x ast.Expr) (int64, bool) {
		lit, ok := x.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return 0, false
		}
		v, err := strconv.ParseInt(lit.Value, 10, 64)
		return v, err == nil
	}
	switch bin.Op {
	case token.MUL:
		if u, ok := unit(bin.Y); ok {
			if n, ok := num(bin.X); ok {
				return time.Duration(n) * u, true
			}
		}
		if u, ok := unit(bin.X); ok {
			if n, ok := num(bin.Y); ok {
				return time.Duration(n) * u, true
			}
		}
	case token.QUO:
		if u, ok := unit(bin.X); ok {
			if n, ok := num(bin.Y); ok && n != 0 {
				return u / time.Duration(n), true
			}
		}
	}
	return 0, false
}

func collectDurations(t *testing.T) map[string][]durDecl {
	t.Helper()
	out := map[string][]durDecl{}
	const root = "../.."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "licenses", "baselines", "bin":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		rel := strings.TrimPrefix(filepath.ToSlash(path), root+"/")
		ast.Inspect(f, func(n ast.Node) bool {
			gen, ok := n.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				return true
			}
			for _, sp := range gen.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if family(id.Name) == "" || i >= len(vs.Values) {
						continue
					}
					dur, ok := durationOf(vs.Values[i])
					if !ok {
						t.Errorf("%s: %s is written in a shape this guard cannot read, "+
							"so it is not being compared with the others",
							fset.Position(id.Pos()), id.Name)
						continue
					}
					fam := family(id.Name)
					out[fam] = append(out[fam], durDecl{where: rel + ":" + id.Name, d: dur})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	return out
}

func TestTheRepeatedDurationsAgree(t *testing.T) {
	found := collectDurations(t)

	// A guard that has stopped recognising anything passes. These are the counts
	// at the time of writing: fewer means the naming has moved and the sweep is
	// absolving copies it can no longer see.
	floors := map[string]int{"opus packet": 4, "analysis block": 3}
	for fam, min := range floors {
		if n := len(found[fam]); n < min {
			t.Fatalf("%s: found %d declarations, expected at least %d - a copy has "+
				"been renamed out of this guard's sight, and green here would mean nothing",
				fam, n, min)
		}
	}

	for fam, ds := range found {
		want, ok := authority[fam]
		if !ok {
			t.Fatalf("%s has no declared authority", fam)
		}
		ref := -1
		for i := range ds {
			if ds[i].where == want {
				ref = i
			}
		}
		if ref < 0 {
			t.Fatalf("%s: the authority %s was not found: it has moved, and every "+
				"other copy is now being compared with nothing", fam, want)
		}
		for _, d := range ds {
			if d.d != ds[ref].d {
				t.Errorf("%s: %s is %v while %s is %v - the copies were repeated on "+
					"purpose and each promises it equals the others; diverging, they do "+
					"the one thing the repetition cannot survive",
					fam, d.where, d.d, ds[ref].where, ds[ref].d)
			}
		}
	}
}
