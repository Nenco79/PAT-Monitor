package doc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// **Seven hand-written code lists, and one guard between them.**
//
// The monitor keeps its codes in `const` blocks of a named string type, and
// beside each block a function — `AllSteps`, `AllFaults`, `AllErrCodes` and the
// rest — returns them as a slice. Those slices are what the catalogue tests
// expand into keys and demand a word for in every language, which is why each
// one carries a comment calling itself *the authoritative list*.
//
// **A slice literal cannot be authoritative about the block above it.** Add a
// code, use it, forget the slice, and nothing downstream ever asks for a word:
// the catalogue guard passes, the page's key extractor sees nothing, and the
// code reaches the screen bare — in the tray as the menu's first line, in the
// server inside an error message, which is the one place the codes exist to make
// readable.
//
// **This is not a hypothesis, and it is the third time.** `StepPanic` was added
// to `internal/tunnel`, neither catalogue was touched, and `go test ./...`
// passed; `CLAUDE.md` records it and says the remedy was applied. It was — to
// **one** list. `internal/tunnel/steps_test.go` is that remedy, and the same
// package grew three more hand-written lists beside it, `internal/tray` two and
// `internal/server` one, each with a sentence promising the protection and two
// of them naming `alerts.AllCodes` as their model — which is the one that is
// derived and therefore the one that does not need it.
//
// So the walk is done once, here, for the whole tree.
//
// **The pairing is discovered, not declared.** A table of (type, list) written
// in this file would be the eighth hand-written list, and it would go stale the
// same way: a list is matched to the type whose constants it actually mentions.
// A list that mentions no constant at all — `alerts.AllCodes`, which ranges over
// its `levels` table — pairs with nothing and is checked by nothing, which is
// right: a derived list is already what this guard is trying to make the others
// behave like.
//
// The empty constants are exempt **by value**: `StepNone`, `ActionNone`,
// `WarningNone`, `ReachUnknown`, `FaultNone`, `NoteNone` all carry "", an empty
// code is the absence of a code, and a catalogue asked for a word for one would
// be asked for a key ending in a dot.
func TestEveryDeclaredCodeReachesItsList(t *testing.T) {
	pkgs := readPackages(t)

	// A guard that recognises nothing passes. Seven lists pair with a type at
	// the time of writing; fewer means the shape has moved and this is
	// absolving whatever replaced it.
	const wantLists = 7
	paired := 0

	for _, dir := range sortedKeys(pkgs) {
		p := pkgs[dir]
		for _, listName := range sortedKeys(p.lists) {
			body := p.lists[listName]

			// Which type does this list speak about? The one whose constants it
			// names. `AllErrCodes` converts each to a string, so the identifier
			// is there either way.
			best, bestHits := "", 0
			for typ, consts := range p.consts {
				hits := 0
				for _, c := range consts {
					if body.idents[c.name] {
						hits++
					}
				}
				if hits > bestHits {
					best, bestHits = typ, hits
				}
			}
			if best == "" {
				// A list that names no constant is derived from something else.
				continue
			}
			paired++

			for _, c := range p.consts[best] {
				if c.value == "" {
					continue
				}
				if !body.idents[c.name] {
					t.Errorf("%s: %s is a %s and %s() does not carry it. Nothing will "+
						"ask for a word for it, so it reaches the screen as a bare code "+
						"with every test green — which is exactly what StepPanic did.",
						c.pos, c.name, best, listName)
				}
			}
			// **There is no check in the other direction, and there is no need
			// for one.** A list that named a constant nobody declares any more
			// would not compile: these lists carry the identifiers, not their
			// values. It is the one asymmetry with the catalogue guards, where
			// both directions have to be asked because the far side is a JSON
			// file the compiler never sees.
		}
	}

	if paired < wantLists {
		t.Errorf("only %d of the code lists could be matched to the constants they "+
			"are drawn from, expected at least %d: a list or a const block has "+
			"changed shape and is no longer being read", paired, wantLists)
	}
}

type constDecl struct {
	name  string
	value string
	pos   string
}

type listBody struct {
	idents map[string]bool
}

type pkgCodes struct {
	// consts, by the name of the named type they are declared with.
	consts map[string][]constDecl
	// lists, by function name.
	lists map[string]listBody
}

// readPackages collects, per directory, the typed string constants and the
// bodies of the `All…` functions.
func readPackages(t *testing.T) map[string]*pkgCodes {
	t.Helper()
	out := map[string]*pkgCodes{}
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !ours(d) {
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
		dir := filepath.ToSlash(filepath.Dir(path))
		p := out[dir]
		if p == nil {
			p = &pkgCodes{consts: map[string][]constDecl{}, lists: map[string]listBody{}}
			out[dir] = p
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				// `StepX FailedStep = "x"`: only the specs that carry a named
				// type, which is how every one of these blocks is written.
				id, ok := n.Type.(*ast.Ident)
				if !ok {
					return true
				}
				for i, nm := range n.Names {
					if i >= len(n.Values) {
						continue
					}
					lit, ok := n.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					p.consts[id.Name] = append(p.consts[id.Name], constDecl{
						name:  nm.Name,
						value: lit.Value[1 : len(lit.Value)-1],
						pos:   fset.Position(nm.Pos()).String(),
					})
				}
			case *ast.FuncDecl:
				if n.Recv != nil || !strings.HasPrefix(n.Name.Name, "All") || n.Body == nil {
					return true
				}
				ids := map[string]bool{}
				ast.Inspect(n.Body, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						ids[id.Name] = true
					}
					return true
				})
				p.lists[n.Name.Name] = listBody{idents: ids}
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
