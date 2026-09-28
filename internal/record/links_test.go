package record

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// **A link with a clip's name is not followed.** The store checked only the
// name's shape and then followed whatever the name was: a symbolic link
// called like a clip and pointing at any file of the user's was listed, served
// to a viewer through the Funnel, and truncated by the next Save under that
// name. Planting one needs code running as the user already; what this keeps is
// the viewer's routes from reading a file that was never a clip.
//
// Creating a symbolic link on Windows needs Developer Mode or a privilege, so
// the test says so and skips where it cannot make one.
//
// **The defect was put back and this test fails with it**: with os.Stat back in
// resolve and the directory check back in list, the link is listed and its
// target is served and overwritten.
func TestALinkWithAClipsNameIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	clips := filepath.Join(dir, "clips")
	if err := os.MkdirAll(clips, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("not a clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(4 * time.Second)
	name := clipPrefix + at.Local().Format(nameLayout) + "-motion" + clipSuffix
	if err := os.Symlink(secret, filepath.Join(clips, name)); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	s := NewStore(clips, StoreConfig{})

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the link was listed as a clip: %+v", entries)
	}
	if f, _, err := s.Open(name); err == nil {
		f.Close()
		t.Error("the link's target was served")
	}

	ring := NewRing(nil)
	feedGOP(t, ring, sps720p, t0, 20, step)
	// A name that is taken is not written: the clip goes under the next free
	// one, and the link stays a link.
	if err := s.Save(Clip{Snapshot: ring.Snapshot(), Code: "motion", At: at}); err != nil {
		t.Errorf("the clip was not saved under another name: %v", err)
	}
	if st, err := os.Lstat(filepath.Join(clips, name)); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Error("a Save wrote through the link, or replaced it")
	}
	if got, _ := os.ReadFile(secret); string(got) != "not a clip" {
		t.Errorf("the link's target was overwritten: %d bytes now", len(got))
	}
}

// **Nor through a link at the name the clip is written aside under.** Save
// writes to `<name>.part` and moves it into place; created as it came, a link
// planted there had its target overwritten, and the rename put the link on the
// clip's name. Put back and watched failing.
func TestALinkAtTheAsideNameIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	clips := filepath.Join(dir, "clips")
	if err := os.MkdirAll(clips, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("not a clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(4 * time.Second)
	name := clipPrefix + at.Local().Format(nameLayout) + "-motion" + clipSuffix
	if err := os.Symlink(secret, filepath.Join(clips, name+partSuffix)); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	s := NewStore(clips, StoreConfig{})
	ring := NewRing(nil)
	feedGOP(t, ring, sps720p, t0, 20, step)
	if err := s.Save(Clip{Snapshot: ring.Snapshot(), Code: "motion", At: at}); err != nil {
		t.Fatalf("the clip was not saved: %v", err)
	}
	if got, _ := os.ReadFile(secret); string(got) != "not a clip" {
		t.Errorf("the link's target was overwritten: %d bytes now", len(got))
	}
	if st, err := os.Lstat(filepath.Join(clips, name)); err != nil || !st.Mode().IsRegular() {
		t.Error("the clip's name is not a plain file: the link was moved onto it")
	}
}
