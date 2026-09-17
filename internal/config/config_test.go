package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The bandwidth saving has to be on without anybody doing anything: a feature
// that only lives if the user writes a line in a YAML file is a feature nobody
// uses.
func TestTheSavingIsOnByDefault(t *testing.T) {
	if got := Default().TargetQP; got != 30 {
		t.Errorf("default target_qp %d instead of 30: the saving is born switched off", got)
	}
}

// But whoever switched it off on purpose stays off: a zero written by hand is a
// choice, not a missing field.
func TestAZeroWrittenByHandSwitchesTheSavingOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("target_qp: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TargetQP != 0 {
		t.Errorf("target_qp %d: the default overwrote an explicit choice", cfg.TargetQP)
	}
}

// And a configuration already in service, written before this field existed,
// has to inherit the default instead of going without the saving forever.
func TestAnOldConfigurationInheritsTheDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("quality: high\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TargetQP != 30 {
		t.Errorf("target_qp %d on a configuration without that field", cfg.TargetQP)
	}
}

// **A loaded configuration never comes out with an empty STUN list**, by any of
// the three roads a file can take: the key missing, the key present and blank,
// or the key written as an explicit empty list. That is the fact the
// "no usable STUN server" diagnosis in internal/server/nopath.go rests on — its
// sentence used to offer "either stun_servers is empty in config.yaml" as the
// first of two causes, three lines under a comment refusing to say exactly
// that, and it would have sent whoever read the log in the morning to look at a
// file that was fine.
//
// If this test is ever changed to let an empty list through, the sentence over
// there has to get its first half back: the two say one thing between them.
func TestALoadedConfigurationAlwaysHasSTUNServers(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"the key is missing", "quality: high"},
		{"the key is blank", "stun_servers:"},
		{"the key is an explicit empty list", "stun_servers: []"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.STUNServers) == 0 {
				t.Error("the list came out empty: the monitor would have no way of " +
					"discovering its own public address, and the diagnosis that names " +
					"this state is written as if it could not happen")
			}
		})
	}
}
