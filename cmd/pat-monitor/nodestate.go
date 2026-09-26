package main

import (
	"path/filepath"
	"strings"

	"patmonitor/internal/config"
)

// nodeStateDirs says where the Tailscale node's identity is kept, and where it
// used to be if it has moved: see tunnel.moveTheNode.
//
// **It moves only with the default configuration.** That one lives in the
// roaming profile, which is the reason for moving. A configuration named with
// `-config` keeps its node beside it, as it always has: that flag is how two
// monitors run side by side, and one local folder for both would hand them one
// identity — two nodes answering to one name, each taking it from the other.
//
// The three paths are handed in, so that both directions are tested without
// asking the system.
func nodeStateDirs(cfgPath, defaultPath, local string) (dir, former string) {
	here := filepath.Dir(cfgPath)
	if local == "" || defaultPath == "" || !samePath(cfgPath, defaultPath) {
		return here, ""
	}
	return config.DataDirIn(local), here
}

// defaultConfigPath is config.DefaultPath, or nothing if the system cannot say.
func defaultConfigPath() string {
	p, err := config.DefaultPath()
	if err != nil {
		return ""
	}
	return p
}

// samePath compares two paths the way Windows does: clean, and without regard
// to case.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
