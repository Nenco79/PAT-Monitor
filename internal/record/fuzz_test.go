package record

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzAClipNameFromTheAddressBar gives the store names a viewer typed.
//
// **The name comes from the URL**, from the Internet when the Funnel is on,
// and the store opens, renames and deletes by it. The property is that nothing
// outside the folder moves: whatever resolve accepts is a file directly inside
// it, and a file beside the folder survives every command given with that
// name.
func FuzzAClipNameFromTheAddressBar(f *testing.F) {
	dir := f.TempDir()
	clips := filepath.Join(dir, "clips")
	if err := os.MkdirAll(clips, 0o700); err != nil {
		f.Fatal(err)
	}
	outside := filepath.Join(dir, "clip-20260926-120000-motion.mp4")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		f.Fatal(err)
	}
	s := NewStore(clips, StoreConfig{})

	f.Add("clip-20260926-120000-motion.mp4")
	f.Add("keep-20260926-120000-cry.mp4")
	f.Add("../clip-20260926-120000-motion.mp4")
	f.Add(`..\clip-20260926-120000-motion.mp4`)
	f.Add("clip-20260926-120000-motion.mp4:stream")
	f.Add("clip-20260926-120000-motion.mp4.")
	f.Add("clip-20260926-120000-con.mp4")
	f.Add("clip-99999999-999999-x.mp4")

	f.Fuzz(func(t *testing.T, name string) {
		parseName(name)
		if path, err := s.resolve(name); err == nil && filepath.Dir(path) != clips {
			t.Fatalf("%q resolved to %q, outside %q", name, path, clips)
		}
		if f, _, err := s.Open(name); err == nil {
			f.Close()
		}
		s.Keep(name)
		s.Release(name)
		s.Delete(name)
		if got, err := os.ReadFile(outside); err != nil || string(got) != "outside" {
			t.Fatalf("%q reached the file beside the folder: %q, %v", name, got, err)
		}
	})
}
