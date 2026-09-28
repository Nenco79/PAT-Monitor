package config

import (
	"path/filepath"
	"testing"
)

// **What a run overrides is not written to the file.** `-listen` used to be
// set on the configuration itself, and the first save of anything carried it
// into the file, where every later start obeyed it.
//
// **The defect was put back and this test fails with it**: with the override
// applied to the stored value, the saved file carries the one-off address.
func TestAnOverrideIsReadAndNeverWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.SetPath(path)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	onFile := c.ListenAddr

	s := NewStore(c)
	s.Override(func(c *Config) { c.ListenAddr = "127.0.0.1:8099" })
	if got := s.Get().ListenAddr; got != "127.0.0.1:8099" {
		t.Fatalf("the override is not what Get answers: %q", got)
	}
	// Twice: the second save is the one that starts from what the first left.
	var got Config
	for range 2 {
		var err error
		if got, err = s.Set(func(c *Config) { c.DetectBark = !c.DetectBark }); err != nil {
			t.Fatal(err)
		}
	}
	if got.ListenAddr != "127.0.0.1:8099" {
		t.Errorf("Set answered %q, not what this run uses", got.ListenAddr)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.ListenAddr != onFile {
		t.Errorf("the file now says %q: a one-off override was saved", back.ListenAddr)
	}
}
