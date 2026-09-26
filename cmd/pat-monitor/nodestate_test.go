package main

import (
	"path/filepath"
	"testing"
)

// **The node moves out of the roaming profile only with the default
// configuration**, and a configuration named with `-config` keeps its node
// beside it: two monitors side by side must not share one identity.
//
// **The defect was put back and this test fails with it**: with the state dir
// always beside the configuration, the default case finds no local folder; and
// with it always in the local one, the two named configurations share it.
func TestTheNodeMovesOnlyWithTheDefaultConfiguration(t *testing.T) {
	roaming := filepath.Join(`C:\Users\somebody\AppData\Roaming`, "PAT Monitor")
	def := filepath.Join(roaming, "config.yaml")
	local := `C:\Users\somebody\AppData\Local`

	dir, former := nodeStateDirs(def, def, local)
	if dir != filepath.Join(local, "PAT Monitor") || former != roaming {
		t.Errorf("default configuration: node in %q, moved from %q", dir, former)
	}
	// Windows does not care about case, and neither does the comparison.
	if dir, _ := nodeStateDirs(`c:\users\SOMEBODY\appdata\roaming\PAT Monitor\config.yaml`, def, local); dir != filepath.Join(local, "PAT Monitor") {
		t.Errorf("the default path spelt in another case was taken for a named one: %q", dir)
	}

	a, _ := nodeStateDirs(`D:\monitors\one\config.yaml`, def, local)
	b, _ := nodeStateDirs(`D:\monitors\two\config.yaml`, def, local)
	if a == b {
		t.Errorf("two named configurations share one node: %q", a)
	}
	if a != `D:\monitors\one` {
		t.Errorf("a named configuration's node left its folder: %q", a)
	}

	// No answer from the system: the node stays where it always was.
	if dir, former := nodeStateDirs(def, def, ""); dir != roaming || former != "" {
		t.Errorf("with no local folder the node moved to %q from %q", dir, former)
	}
}
