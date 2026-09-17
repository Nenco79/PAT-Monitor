package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// **The tray has to ask the server for the status, not the raw producer.**
//
// `statusFn` is what the monitor knows about itself: camera, microphone,
// cadence, alerts. But how many sessions are open is known **only to the
// server**, which holds them, and when that field was filled in by the HTTP
// route the page went through it and counted two while the tray called
// `statusFn` on its own and the panel declared "no device connected", with a
// phone connected and at the same instant.
//
// The remedy is `server.Status()`, which composes once for both. This test looks
// at **where the tray's status comes from**, because the defect was not a wrong
// value — it was a value asked of somebody who does not have it, and no test on
// the value catches that: on the side being looked at, the value was right.
func TestTheTrayAsksTheServerForTheStatus(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}

	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || typeName(lit.Type) != "tray.Config" {
			return true
		}
		for _, e := range lit.Elts {
			field, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := field.Key.(*ast.Ident); !ok || key.Name != "StatusFn" {
				continue
			}
			found = true
			var sources []string
			ast.Inspect(field.Value, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					sources = append(sources, typeName(c.Fun))
				}
				return true
			})
			joined := strings.Join(sources, " ")
			if !strings.Contains(joined, "srv.Status") {
				t.Errorf("%s: the tray does not ask the server for the status (%s): the "+
					"fields only it fills would reach the tray as zero",
					fset.Position(field.Pos()), joined)
			}
			// **And the raw producer must not appear at all.** Asking only for
			// the presence of `srv.Status` lets through the shape the defect
			// really had — `trayStatus(statusFn(), ...)` with a call to the
			// server next to it — that is, it absolves exactly what is meant to
			// be caught. Both halves are needed.
			for _, s := range sources {
				if s == "statusFn" {
					t.Errorf("%s: the tray still calls statusFn (%s): that one does not "+
						"carry the fields the server fills",
						fset.Position(field.Pos()), joined)
				}
			}
		}
		return true
	})

	if !found {
		t.Fatal("tray.Config with no StatusFn: the test looked at nothing")
	}
}

// typeName renders `srv.Status` and `tray.Config` as they are written.
func typeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return typeName(v.X) + "." + v.Sel.Name
	}
	return ""
}
