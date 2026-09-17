package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// **The announced level is computed from the preset alone, and `-bitrate` must
// not reach it.**
//
// MinLevelIDC exists to avoid declaring a level higher than necessary: browsers
// announce 3.1 and negotiation fails over more, which is why hardware encoders'
// over-cautious 3.2 is pulled back down. Handed `settings.BitrateKbps` — the
// value **after** the diagnostic flag — the same function answers 3.2 as soon as
// somebody forces past 14000 kbit/s, and the announcement would stop every
// viewer connecting. Where that flag really does push the stream above the
// preset's level, the stream carries it in its own SPS and announce() takes the
// larger of the two: the belt is downstream, and does not depend on this call
// being right.
//
// This reads the source because the defect is **which number was passed**, and
// no test on the value catches that: with the preset's bitrate and the forced
// one equal, which is every run without the flag, both spellings give the same
// answer.
func TestTheAnnouncedLevelIsComputedFromThePresetAlone(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("main.go cannot be parsed: %v", err)
	}

	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || typeName(lit.Type) != "rtc.Config" {
			return true
		}
		for _, e := range lit.Elts {
			field, ok := e.(*ast.KeyValueExpr)
			if !ok || typeName(field.Key) != "LevelIDC" {
				continue
			}
			found = true
			call, ok := field.Value.(*ast.CallExpr)
			if !ok || typeName(call.Fun) != "media.MinLevelIDC" {
				t.Errorf("LevelIDC is not computed with media.MinLevelIDC: a level " +
					"written by hand beside the numbers that determine it is a second list")
				continue
			}
			for _, arg := range call.Args {
				name := typeName(arg)
				if !strings.HasPrefix(name, "preset.") {
					t.Errorf("media.MinLevelIDC is given %q: every argument must be the "+
						"preset's, because the announcement has to hold for the whole "+
						"process and -bitrate can raise the answer to 3.2", name)
				}
			}
		}
		return true
	})

	// **Without this the test passes by finding nothing**, which is exactly how
	// a guard on an absence absolves the defect it was written for.
	if !found {
		t.Fatal("no LevelIDC in rtc.Config: this test is looking in the wrong place")
	}
}
