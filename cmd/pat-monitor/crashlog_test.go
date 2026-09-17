package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The log is not closed, and this refuses the line that would close it.
//
// It reads as untidiness — a file opened and never closed — so it is exactly
// what somebody puts back while cleaning up, and what it costs is invisible
// until the night it matters: a deferred close runs **while a panic is
// unwinding**, gives the standard error back, and the runtime's trace is
// written an instant later to nowhere. Measured, with the close in place: the
// log said `crash traces=this file` and then carried no trace at all.
//
// Nothing is lost by not closing. applog.Writer buffers nothing, so there is
// no flush to miss, and the handle goes back to the system when the process
// ends. What it buys is that the log is the last thing alive.
//
// **No test of behaviour can see this**, which is why it is a test of the
// source: the whole effect lives in a process with no console of its own, and
// `go test` always has one. The live check is `-simulate-panic now`.
func TestTheLogIsNotClosedOnTheWayOut(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	ast.Inspect(f, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		sel, ok := d.Call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Close" {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "logFile" {
			return true
		}
		t.Errorf("`defer logFile.Close()` at %s: it runs while a panic is "+
			"unwinding and takes the standard error away an instant before the "+
			"runtime writes the trace into it", fset.Position(d.Pos()))
		return true
	})
}

// And the other direction: the protection has to be asked for. Without this
// call the log is an ordinary file again, and nothing anywhere would say so —
// the traces would simply stop arriving.
func TestTheLogIsAskedToCatchTheCrashes(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CaptureCrashes" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("nobody calls CaptureCrashes: a panic would end the process " +
			"leaving nothing in the log, which is the fault it was written for")
	}
}
