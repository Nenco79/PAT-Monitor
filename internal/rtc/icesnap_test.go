package rtc

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

// Close must remember the facts before it takes away what answers for them, and
// the defect is not a wrong value: it is the order of two statements. No test on
// the numbers can see it — on a live agent both orders give the same answer, and
// the one that matters is the one taken an instant after a failure.
//
// It is the shape of TestOnlyTheSupervisorClearsTheAudioFlag: what is being
// watched is which write comes first.
func TestCloseRemembersBeforeItTearsDown(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "hub.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var body []ast.Stmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Close" || fn.Recv == nil {
			return true
		}
		// The work is inside the closeOnce.Do closure, not in Close's own body.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok && body == nil {
				body = lit.Body.List
			}
			return true
		})
		return false
	})
	if len(body) == 0 {
		t.Fatal("Close's closure was not found: this guard is reading nothing")
	}

	render := func(s ast.Stmt) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, s)
		return b.String()
	}

	remembered, tornDown := -1, -1
	for i, s := range body {
		line := render(s)
		if remembered < 0 && strings.Contains(line, "rememberICEFacts") {
			remembered = i
		}
		if tornDown < 0 && strings.Contains(line, "v.pc.Close()") {
			tornDown = i
		}
	}
	if remembered < 0 {
		t.Fatal("Close does not remember the negotiation's facts: after it, GetStats answers zeros")
	}
	if tornDown < 0 {
		t.Fatal("v.pc.Close() was not found in Close: this guard no longer reads what it claims")
	}
	if remembered > tornDown {
		t.Errorf("the facts are remembered at statement %d, after the agent goes at %d", remembered, tornDown)
	}
}
