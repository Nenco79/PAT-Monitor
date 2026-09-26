package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// **Every HTTP server lets an idle connection go.** Only ReadHeaderTimeout was
// set, so a keep-alive connection that sent nothing after its first request
// was held for ever: a goroutine and a buffer each, and on the Funnel a
// netstack endpoint, for as long as a caller on the Internet cared to keep
// them. What is read is every http.Server literal in the monitor, so a fourth
// one added tomorrow is covered with nothing to remember.
//
// **The defect was put back and this test fails with it**: with IdleTimeout
// taken off the Funnel's server, it names the line.
func TestEveryHTTPServerLetsAnIdleConnectionGo(t *testing.T) {
	found := 0
	for _, root := range []string{".", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || typeName(lit.Type) != "http.Server" {
					return true
				}
				found++
				fields := map[string]bool{}
				for _, e := range lit.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok {
							fields[id.Name] = true
						}
					}
				}
				for _, want := range []string{"ReadHeaderTimeout", "IdleTimeout"} {
					if !fields[want] {
						t.Errorf("%s: an http.Server without %s", fset.Position(lit.Pos()), want)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if found < 3 {
		t.Fatalf("only %d http.Server literals were read: this guard is no longer "+
			"looking at the monitor's source", found)
	}
}
