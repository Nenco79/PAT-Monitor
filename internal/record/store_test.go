package record

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFake puts a file with a clip's name and the requested size into the
// folder. For the pruning the content does not matter: the instant is in the
// name and the size comes from the filesystem.
func writeFake(t *testing.T, dir, name string, kb int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, kb*1024), 0o600); err != nil {
		t.Fatal(err)
	}
}

func namesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range entries {
		out = append(out, v.Name())
	}
	return out
}

func has(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// **The promise that matters.** A monitor that runs every night must not be
// able to fill the disk of whoever hosts it: deletion starts from the oldest
// until the total is back inside the quota.
func TestPruningStopsAtTheSpaceQuota(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxBytes: 500 * 1024})

	// Five 200 KB clips: a megabyte, twice the quota.
	for i := 1; i <= 5; i++ {
		writeFake(t, dir, "clip-2026090"+string(rune('0'+i))+"-120000-motion.mp4", 200)
	}

	removed, err := s.Prune(time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 3 {
		t.Errorf("%d clips deleted instead of 3", removed)
	}
	left := namesIn(t, dir)
	if len(left) != 2 {
		t.Fatalf("%d files left: %v", len(left), left)
	}
	// **The two most recent**, not any two: a clip from last night is worth
	// more than one from four days ago.
	if !has(left, "clip-20260904-120000-motion.mp4") || !has(left, "clip-20260905-120000-motion.mp4") {
		t.Errorf("the wrong clips were left: %v", left)
	}
}

// The quota alone is not enough: without an expiry, six-month-old nights would
// be kept just because there is room.
func TestPruningDropsWhatIsTooOld(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxAge: 48 * time.Hour})

	writeFake(t, dir, "clip-20260901-120000-motion.mp4", 10) // nine days
	writeFake(t, dir, "clip-20260909-120000-cry.mp4", 10)    // one day
	writeFake(t, dir, "clip-20260910-060000-bark.mp4", 10)   // six hours

	if _, err := s.Prune(time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	left := namesIn(t, dir)
	if len(left) != 2 || has(left, "clip-20260901-120000-motion.mp4") {
		t.Errorf("the expiry did not take away what it should: %v", left)
	}
}

// **What the lock means.** An automatic rule sooner or later deletes exactly
// the clip somebody wanted to keep: the kept one has to survive both ceilings,
// and it survives because the pruning does not see it — its name lacks the
// prefix it looks for.
func TestAKeptClipSurvivesEverything(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxBytes: 100 * 1024, MaxAge: time.Hour})

	writeFake(t, dir, "keep-20260101-120000-cry.mp4", 5000) // old and enormous
	for i := 1; i <= 3; i++ {
		writeFake(t, dir, "clip-2026090"+string(rune('0'+i))+"-120000-motion.mp4", 200)
	}

	if _, err := s.Prune(time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	left := namesIn(t, dir)
	if !has(left, "keep-20260101-120000-cry.mp4") {
		t.Fatalf("the kept clip was deleted: %v", left)
	}
	for _, n := range left {
		if n != "keep-20260101-120000-cry.mp4" {
			t.Errorf("%q survived too, and it was not kept", n)
		}
	}
}

// The pruning looks at **its own files only**. Even now that the clips have a
// folder of their own, somebody else's file in there is not impossible — a copy
// made by hand, a system file — and deleting it is not our job.
func TestPruningNeverTouchesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxBytes: 1})

	foreign := []string{"monitor.log", "notes.txt", "clip-from-yesterday.mp4", "Thumbs.db"}
	for _, n := range foreign {
		writeFake(t, dir, n, 100)
	}
	// Two, because the quota never deletes the last one left: with a single one
	// the test would have nothing to delete and would pass proving nothing.
	writeFake(t, dir, "clip-20260901-120000-motion.mp4", 100)
	writeFake(t, dir, "clip-20260902-120000-motion.mp4", 100)

	if _, err := s.Prune(time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	left := namesIn(t, dir)
	for _, n := range foreign {
		if !has(left, n) {
			t.Errorf("%q was deleted, and it is not a clip of ours", n)
		}
	}
	if has(left, "clip-20260901-120000-motion.mp4") {
		t.Error("our oldest clip survived a quota of one byte")
	}
}

// **The name arrives from whoever is watching, inside a URL, and with us it
// becomes a path.** It is the lesson of the language cookie, which ended up
// concatenated into a file name: it is validated against a strict shape, and
// then looked up in the real folder.
func TestAClipNameCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(filepath.Join(dir, "video"), StoreConfig{})
	writeFake(t, s.Dir(), "clip-20260901-120000-motion.mp4", 1)

	bad := []string{
		"../secret.txt",
		"..\\secret.txt",
		"/etc/passwd",
		"C:\\Windows\\win.ini",
		"clip-20260901-120000-motion.mp4/../../secret.txt",
		"monitor.log",
		"clip-20260901-120000-motion.mp4.exe",
		"clip-2026-120000-motion.mp4",
		"",
		".",
		"clip-20260902-120000-motion.mp4", // right shape, file not there
	}
	for _, n := range bad {
		if _, _, err := s.Open(n); err == nil {
			t.Errorf("Open(%q) succeeded", n)
		}
		if err := s.Delete(n); err == nil {
			t.Errorf("Delete(%q) succeeded", n)
		}
		if _, err := s.Keep(n); err == nil {
			t.Errorf("Keep(%q) succeeded", n)
		}
	}
	// And the good name has to work, otherwise the test would pass by refusing
	// everything.
	f, e, err := s.Open("clip-20260901-120000-motion.mp4")
	if err != nil {
		t.Fatalf("the valid name was refused: %v", err)
	}
	f.Close()
	if e.Code != "motion" {
		t.Errorf("code read %q instead of motion", e.Code)
	}
}

// The lock is a rename, so pressing it twice must not be an error: whoever taps
// twice on a phone did not ask for two things.
func TestKeepIsARenameAndCanBePressedTwice(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{})
	writeFake(t, dir, "clip-20260901-120000-motion.mp4", 1)

	renamed, err := s.Keep("clip-20260901-120000-motion.mp4")
	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	// The new name goes back to whoever pressed: after the rename the old one
	// no longer answers, and whoever was watching that clip has the old URL.
	if renamed != "keep-20260901-120000-motion.mp4" {
		t.Errorf("the name returned is %q", renamed)
	}
	if !has(namesIn(t, dir), renamed) {
		t.Fatalf("the clip was not renamed: %v", namesIn(t, dir))
	}
	again, err := s.Keep(renamed)
	if err != nil {
		t.Errorf("pressing the lock twice gave %v", err)
	}
	if again != renamed {
		t.Errorf("the second press returned %q instead of %q", again, renamed)
	}
}

// With no ceilings nothing is deleted: zero means "no limit", not "delete
// everything". It is the direction where being wrong would cost the recordings.
func TestNoQuotaMeansNoDeletion(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{})
	for i := 1; i <= 4; i++ {
		writeFake(t, dir, "clip-2026090"+string(rune('0'+i))+"-120000-motion.mp4", 5000)
	}
	removed, err := s.Prune(time.Date(2030, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 || len(namesIn(t, dir)) != 4 {
		t.Errorf("with the ceilings at zero %d clips were deleted", removed)
	}
}

// The test that joins the two halves: a real clip is saved and read back from
// the listing. The duration is not in the name — it is read from the file's
// header — so this is also the only test that path works on what we write
// ourselves.
func TestSaveWritesAClipThatTheListingCanRead(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{})

	ring := NewRing(nil)
	at := feedGOP(t, ring, sps720p, t0, 20, step)
	feedGOP(t, ring, sps720p, at, 20, step)
	for i := 0; i < 200; i++ {
		ring.WriteAudio([]byte{0xfc, byte(i)}, t0.Add(time.Duration(i)*opusFrameDuration))
	}
	snap := ring.Snapshot()
	event := t0.Add(4 * time.Second)
	if err := s.Save(Clip{Snapshot: snap, Code: "motion", At: event}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d clips listed instead of 1", len(entries))
	}
	v := entries[0]
	if v.Code != "motion" {
		t.Errorf("code %q instead of motion", v.Code)
	}
	// Compared as instants and not as strings: the name is in local time, the
	// event in the test is in UTC, and they are the same moment.
	if v.At.Unix() != event.Truncate(time.Second).Unix() {
		t.Errorf("instant %v instead of %v", v.At, event.Truncate(time.Second))
	}
	if v.Kept {
		t.Error("a clip just saved comes back as kept")
	}
	if v.Bytes == 0 {
		t.Error("size zero")
	}
	if gap := v.Duration - snap.Span(); gap > step || gap < -step {
		t.Errorf("duration read %v, samples covering %v", v.Duration, snap.Span())
	}
}

// **A quota smaller than one clip must not switch the recordings off.**
//
// Found by trying it live: the pruning runs right after every save, so with a
// ceiling of one megabyte and clips of five it deleted what had just been
// written. The log said "clip saved" and then "clip pruned", and the folder
// stayed empty: a feature that does nothing and does not declare it.
func TestTheQuotaNeverDeletesTheLastClip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxBytes: 1024}) // one kilobyte

	writeFake(t, dir, "clip-20260901-120000-motion.mp4", 500)
	writeFake(t, dir, "clip-20260902-120000-motion.mp4", 500)
	if _, err := s.Prune(time.Date(2026, 9, 3, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	left := namesIn(t, dir)
	if len(left) != 1 {
		t.Fatalf("%d clips left instead of the last one: %v", len(left), left)
	}
	if !has(left, "clip-20260902-120000-motion.mp4") {
		t.Errorf("the oldest survived instead of the most recent: %v", left)
	}
}

// **Expiry by age, on the other hand, deletes everything, the last one
// included.** It is not tidiness: it is a promise about how long a child's
// recordings live, and keeping one past the deadline so as not to leave the
// folder empty would break the promise for an aesthetic reason.
func TestAgeDeletesEvenTheLastClip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxAge: 24 * time.Hour})

	writeFake(t, dir, "clip-20260901-120000-motion.mp4", 10)
	if _, err := s.Prune(time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if left := namesIn(t, dir); len(left) != 0 {
		t.Errorf("an expired clip survived because it was the last: %v", left)
	}
}

// **A malformed code must not produce an invisible file.**
//
// Save composes the name by dropping the code in without looking at it. A
// character the name expression does not allow gives a file that List, Open,
// Delete and the pruning all skip: a clip that takes up space, appears nowhere
// and counts in neither ceiling. Refusing is worse than saving, and far better
// than a folder growing at night without a line.
func TestASaveWithAnUnusableCodeIsRefused(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{})

	ring := NewRing(nil)
	feedGOP(t, ring, sps720p, t0, 20, step)
	snap := ring.Snapshot()

	for _, code := range []string{"", "Motion", "smoke_alarm", "movement!", "cry.loud", "-cry"} {
		err := s.Save(Clip{Snapshot: snap, Code: code, At: t0})
		if !errors.Is(err, ErrBadCode) {
			t.Errorf("Save with the code %q gave %v", code, err)
		}
	}
	if entries := namesIn(t, dir); len(entries) != 0 {
		t.Errorf("%v ended up in the folder", entries)
	}

	// And the good codes get through, otherwise the test would refuse
	// everything.
	for _, code := range []string{"motion", "cry", "bark", "smoke-alarm", "cry2"} {
		if err := s.Save(Clip{Snapshot: snap, Code: code, At: t0}); err != nil {
			t.Errorf("Save with the code %q gave %v", code, err)
		}
	}
	if entries, _ := s.List(); len(entries) != 5 {
		t.Errorf("%d clips saved instead of 5", len(entries))
	}
}

// **A clip asked for by hand is born held**, and the prefix is Save's to
// decide.
//
// It is not a convenience for whoever saves: between Save and a rename straight
// afterwards sits the pruning, which runs at the end of Save and does not see
// the state somebody has in mind. With a tight quota the clip just asked for
// would be the only candidate to vanish — that is, pressing "Record" would give
// a file written and deleted in the same instant.
func TestAClipAskedForByHandIsBornKept(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{})

	ring := NewRing(nil)
	at := feedGOP(t, ring, sps720p, t0, 20, step)
	feedGOP(t, ring, sps720p, at, 20, step)
	if err := s.Save(Clip{Snapshot: ring.Snapshot(), Code: CodeManual,
		At: t0.Add(2 * time.Second), Keep: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := s.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("%d clips listed (err %v)", len(entries), err)
	}
	if !entries[0].Kept {
		t.Errorf("the clip %q does not come back as kept: the pruning sees it", entries[0].Name)
	}
	if !strings.HasPrefix(entries[0].Name, keptPrefix) {
		t.Errorf("the name is %q: the lock is the prefix, not a state beside it",
			entries[0].Name)
	}

	// And the test has to have the other direction to hand as well, otherwise a
	// Save that keeps everything would pass too.
	if err := s.Save(Clip{Snapshot: ring.Snapshot(), Code: "motion", At: t0}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, _ = s.List()
	for _, v := range entries {
		if v.Code == "motion" && v.Kept {
			t.Error("an event clip was born held: no rule would ever delete it")
		}
	}
}

// **The release is the lock's other half**, and without it the clips asked for
// by hand pile up forever: no rule touches them, and the promise about the disk
// of whoever hosts us stops holding for the one kind of clip produced on
// command.
func TestReleasePutsAClipBackUnderTheRetention(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, StoreConfig{MaxAge: 48 * time.Hour})
	writeFake(t, dir, "keep-20260901-120000-manual.mp4", 1)

	renamed, err := s.Release("keep-20260901-120000-manual.mp4")
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if renamed != "clip-20260901-120000-manual.mp4" {
		t.Errorf("the name returned is %q", renamed)
	}
	if !has(namesIn(t, dir), renamed) {
		t.Fatalf("the clip was not renamed: %v", namesIn(t, dir))
	}

	// **And now the pruning really does see it.** The new name alone would not
	// prove that: the rule lives in prune, which looks at the prefix.
	if _, err := s.Prune(time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(namesIn(t, dir)) != 0 {
		t.Errorf("a nine-day-old released clip survived a two-day expiry: %v", namesIn(t, dir))
	}

	// Pressing twice is not an error, as with the lock: a repeated tap on a
	// phone did not ask for two things.
	writeFake(t, dir, "clip-20260909-120000-manual.mp4", 1)
	again, err := s.Release("clip-20260909-120000-manual.mp4")
	if err != nil || again != "clip-20260909-120000-manual.mp4" {
		t.Errorf("releasing an already prunable clip gave %q, %v", again, err)
	}
}

// The manual clips' code has to be able to become a file name.
//
// **It is the same guard as the alert codes'**, which lives in cmd/pat-monitor
// because that is where they are all visible: this one is emitted by record
// itself, so the question is asked here. A malformed code would produce a file
// that listing, pruning and deletion all skip.
func TestTheManualCodeCanBecomeAFileName(t *testing.T) {
	if !UsableCode(CodeManual) {
		t.Fatalf("the code %q cannot become a clip name", CodeManual)
	}
	// And the whole name has to pass the strict shape used to validate what
	// arrives from whoever is watching: the two expressions are different, and
	// this is the only one the server queries.
	name := prefixFor(true) + "20260903-120000-" + CodeManual + clipSuffix
	if !clipName.MatchString(name) {
		t.Errorf("the name %q is not a clip name: the routes would refuse it", name)
	}
}
