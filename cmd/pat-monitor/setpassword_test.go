package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"strings"
	"testing"
)

// **-set-password against a running monitor is refused before it asks for
// anything.**
//
// It wrote the file and said "Password set", while the running monitor kept
// the old password in memory, kept every session open, and put the old hash
// back in the file at the next setting changed from the page. What is watched
// is the order inside the command: the port is taken before the first prompt,
// so a monitor that is running refuses the change instead of swallowing it.
// A test on a value cannot see this, the defect being a call that is not made.
//
// **The defect was put back and this test fails with it**: with the port no
// longer taken, the first call in the function is the prompt.
func TestThePasswordIsNotSetBehindARunningMonitorsBack(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "promptAndSetPassword" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("promptAndSetPassword is not in main.go: this guard reads nothing")
	}

	var order []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := typeName(call.Fun); {
		case name == "holdThePort", strings.HasSuffix(name, "ReadPassword"), strings.HasSuffix(name, "Save"):
			order = append(order, name)
		}
		return true
	})
	if len(order) == 0 || order[0] != "holdThePort" {
		t.Errorf("promptAndSetPassword calls %v: the port has to be taken before the "+
			"password is asked for, or a running monitor keeps the old one", order)
	}
}

// And the refusal says what to do, which is the half the person reads.
func TestAHeldPortIsDeclaredAsARunningMonitor(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()

	if ln, err := holdThePort(taken.Addr().String()); err == nil {
		ln.Close()
		t.Fatal("a port already held was taken again")
	} else if !strings.Contains(err.Error(), "running monitor") {
		t.Errorf("the refusal does not name the running monitor: %v", err)
	}

	free := taken.Addr().String()
	taken.Close()
	ln, err := holdThePort(free)
	if err != nil {
		t.Fatalf("a free port was refused: %v", err)
	}
	ln.Close()
}
