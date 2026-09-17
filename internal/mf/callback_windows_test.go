//go:build windows

package mf

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// **Closing waits for an Invoke that is already inside the object.**
//
// The caller releases the event generator and the transform the moment this
// returns, and an Invoke in flight is at that point calling EndGetEvent through
// the vtable of what is about to be freed. close() used to shut a channel and
// return, which stops the *next* subscription and says nothing about the call
// already running — and the file's own comment records that at teardown "a
// request is nearly always in flight".
//
// **The defect was put back and this test fails with it**: with close() reduced
// to setting the flag, it returns while the Invoke is still counted in, and the
// first branch below fires.
//
// No Media Foundation here, and none is needed: enter, leave and close are the
// whole of the mechanism, and they are plain Go. The thing the encoder does in
// between is what must not be interrupted, not what is being tested.
func TestClosingWaitsForAnInvokeAlreadyInside(t *testing.T) {
	cb := &asyncCallback{}
	if !cb.enter() {
		t.Fatal("a fresh callback refused the first Invoke")
	}

	done := make(chan struct{})
	go func() {
		cb.close()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("close returned while an Invoke was still inside: the generator " +
			"and the transform are released under a Media Foundation thread")
	case <-time.After(100 * time.Millisecond):
	}

	cb.leave()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not return after the Invoke left")
	}

	// And a request armed before the close still arrives afterwards: it cannot
	// be cancelled, so the answer it has to get is "do nothing".
	if cb.enter() {
		t.Error("an Invoke was let in after the close: it would reach a released " +
			"event generator")
	}
}

// The count is reentrant, and that is the reason it is a count.
//
// An RWMutex would be the obvious shape and would deadlock: Invoke re-arms the
// subscription before returning, so a Media Foundation that ever delivered the
// next event on that same thread, from inside that call, would take the read
// side twice with a writer waiting. A hung monitor is worse than the crash
// being fixed, so the mechanism must survive being entered twice.
func TestTheInvokeCountIsReentrant(t *testing.T) {
	cb := &asyncCallback{}
	if !cb.enter() || !cb.enter() {
		t.Fatal("the second, nested Invoke was refused")
	}
	cb.leave()

	done := make(chan struct{})
	go func() { cb.close(); close(done) }()
	select {
	case <-done:
		t.Fatal("close returned with one Invoke still inside")
	case <-time.After(100 * time.Millisecond):
	}
	cb.leave()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not return once both Invokes had left")
	}
}

// **lastErr is written from a Media Foundation thread, so nobody touches it
// directly.**
//
// It used to be assigned in Invoke and read in Diagnose with nothing in
// between. An interface is two words, and the reader of a torn one gets a type
// and somebody else's pointer; the race detector cannot say so here, because it
// wants cgo and this machine has no C compiler — which is exactly why the guard
// reads the source instead.
//
// It asks the syntax tree rather than the lines: a grep for the name is
// answered by the comment that explains it, and this repository has a chapter
// about guards that pass because the prose moved.
func TestNobodyTouchesLastErrOutsideItsAccessors(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkg {
		for name, file := range p.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok {
					return true
				}
				if fn.Name.Name == "fail" || fn.Name.Name == "lastError" {
					return false
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == "lastErr" {
						t.Errorf("%s touches lastErr directly, at %s: it is written "+
							"from a Media Foundation thread and must go through fail "+
							"or lastError", fn.Name.Name, fset.Position(sel.Pos()))
					}
					return true
				})
				return false
			})
		}
	}
}

// **A candidate that fails to configure is closed, not released.**
//
// configure can fail after it has taken an ICodecAPI, a second reference
// through IMFMediaEventGenerator and armed the async callback; `t.Release()`
// left all three, and the extra reference keeps the rejected transform alive
// with its callback still being handed events. The branch seven lines below it
// already called enc.Close(), which is what makes this one legible as an
// oversight rather than a decision.
//
// It reads the tree because there is nothing to run: reaching that branch wants
// a machine whose encoders fail to configure, which is the machine nobody here
// has.
func TestARejectedCandidateIsClosed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "encoder_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "NewVideoEncoder" {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Release" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "t" {
				t.Errorf("NewVideoEncoder releases the transform directly at %s: "+
					"a candidate that has been through configure holds an ICodecAPI, "+
					"an event generator and an armed callback, and only Close gives "+
					"those back", fset.Position(sel.Pos()))
			}
			return true
		})
		return false
	})
	if !found {
		t.Fatal("NewVideoEncoder was not found: this guard is watching nothing")
	}
}
