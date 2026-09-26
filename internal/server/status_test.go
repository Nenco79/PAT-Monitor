package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// **The status has two readers, and for a while one of them saw zero.**
//
// The fields only the server can fill — how many sessions are open — were
// written inside `apiStatus`, that is, inside the **route**: the page came
// through there and saw them, the tray called `StatusFn` for itself and saw the
// zero value of the type. The panel declared "no device connected" with a phone
// connected, while the same line on the page counted two, at the same instant.
//
// Here what is watched is `Status()`, which is what both of them call.
func TestTheDeviceCountTravelsWithTheStatus(t *testing.T) {
	s, _ := serverWithPassword(t, "a long enough password")
	s.opts.StatusFn = func() Status { return Status{Viewers: 2} }

	if n := s.Status().Devices; n != 1 {
		t.Fatalf("one open session and Devices=%d", n)
	}

	if _, err := s.sessions.create("a second phone", anyRoad); err != nil {
		t.Fatal(err)
	}
	if n := s.Status().Devices; n != 2 {
		t.Errorf("two open sessions and Devices=%d", n)
	}

	// And what the page sees is the same number, not a second count.
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.apiStatus(w, r)
	var fromThePage Status
	if err := json.Unmarshal(w.Body.Bytes(), &fromThePage); err != nil {
		t.Fatal(err)
	}
	if fromThePage.Devices != s.Status().Devices {
		t.Errorf("the page sees %d devices, the tray %d",
			fromThePage.Devices, s.Status().Devices)
	}
}

// **The route composes nothing**, and it is the shape that stops the defect
// coming back: as long as `apiStatus` merely writes what `Status()` hands it, a
// new field cannot end up on only one of the two roads.
//
// The original defect was not a wrong value, it was a value filled in the wrong
// place — and no test on the value would have caught it, because from the side
// that was watched the value was right.
func TestTheRouteComposesNothing(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("sources cannot be parsed: %v", err)
	}

	found := false
	for _, p := range pkg {
		for fileName, file := range p.Files {
			if strings.HasSuffix(fileName, "_test.go") {
				continue
			}
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "apiStatus" || fn.Recv == nil {
					continue
				}
				found = true
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					a, ok := n.(*ast.AssignStmt)
					if !ok {
						return true
					}
					// **Fields are watched, not assignments.** An
					// `if err := ...` inside the route is a local variable and
					// composes nothing: refusing it would make the test fail
					// accusing a defect that is not there, which is how a guard
					// gets itself removed.
					for _, dest := range a.Lhs {
						field, ok := dest.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						t.Errorf("%s: apiStatus fills %s by itself. "+
							"What only the server knows goes into Status(), otherwise "+
							"the tray does not see it", fset.Position(field.Pos()), field.Sel.Name)
					}
					return true
				})
			}
		}
	}
	if !found {
		t.Fatal("apiStatus not found: the test looked at nothing")
	}
}
