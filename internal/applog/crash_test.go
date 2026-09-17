package applog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// watch replaces the three calls into the system with recording ones, and
// gives back the sequence of what happened. The handle is recorded with each
// "take", because the whole question is whether the second one arrives on a
// file that is really open.
func watch(t *testing.T, missing bool) *[]string {
	t.Helper()
	seq := new([]string)
	oldNo, oldTake, oldGive := noStandardError, takeStandardError, giveStandardError
	t.Cleanup(func() {
		noStandardError, takeStandardError, giveStandardError = oldNo, oldTake, oldGive
	})

	// **The file is kept hold of, and that is what makes this test catch.**
	// Recording the two names alone, the sequence is identical whether the
	// give happens before the close or after it — verified, by putting that
	// very defect back — because the order of two names says nothing about a
	// third event neither of them names. Asking the file whether it is still
	// open does.
	var current *os.File
	noStandardError = func() bool { return missing }
	takeStandardError = func(f *os.File) error {
		if _, err := f.Stat(); err != nil {
			*seq = append(*seq, "take-on-a-closed-file")
		}
		*seq = append(*seq, "take")
		current = f
		return nil
	}
	giveStandardError = func() error {
		if current != nil {
			if _, err := current.Stat(); err != nil {
				*seq = append(*seq, "give-after-the-close")
			}
		}
		*seq = append(*seq, "give")
		return nil
	}
	return seq
}

// The hazard this ordering exists for: a closed handle's value is handed out
// again, so a trace written after the close lands inside somebody else's
// bytes. The give must come **before** the close, and the next take only once
// the new file is open.
func TestTheStandardErrorIsGivenUpAroundEveryRotation(t *testing.T) {
	dir := t.TempDir()
	seq := watch(t, true)

	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if !w.CaptureCrashes() {
		t.Fatal("it declined with no standard error in the way")
	}
	// Past the ceiling in one write, so the rotation happens inside it.
	w.Write([]byte(strings.Repeat("x", MaxBytes+1)))
	w.Write([]byte("after\n"))

	// Two writes past the ceiling are two rotations, so what is asserted is
	// the property and not a fixed string: **never two takes in a row**, which
	// is the one shape that means a handle was picked up while the file behind
	// the previous one was already gone.
	got := *seq
	if len(got) < 3 {
		t.Fatalf("too few dealings with the standard error to prove anything: %v", got)
	}
	if got[0] != "take" {
		t.Errorf("the first thing done was %q, not a take", got[0])
	}
	for i := 1; i < len(got); i++ {
		if got[i] == "take" && got[i-1] != "give" {
			t.Errorf("a take at %d was not preceded by a give: %v", i, got)
		}
		if got[i] == "take-on-a-closed-file" || got[i] == "give-after-the-close" {
			t.Errorf("the handle was dealt with around a file that was already "+
				"closed, which is the whole hazard: %v", got)
		}
	}
	if got[len(got)-1] != "take" {
		t.Errorf("it ended without the standard error pointing anywhere: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "monitor.1.log")); err != nil {
		t.Fatalf("no rotation happened at all, so this test proved nothing: %v", err)
	}
}

// A protection that lapses says so in the file, because the start-up line has
// already promised the opposite and nobody would ever read it again.
func TestAFailedRetakeIsWrittenDownAndNotClaimed(t *testing.T) {
	dir := t.TempDir()
	seq := watch(t, true)

	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if !w.CaptureCrashes() {
		t.Fatal("it declined with nothing in the way")
	}

	// From the next take on, the system refuses.
	takeStandardError = func(*os.File) error { return errors.New("refused") }
	w.Write([]byte(strings.Repeat("x", MaxBytes+1)))

	body, err := os.ReadFile(filepath.Join(dir, "monitor.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "crash traces no longer reach this file") {
		// The body is not printed: it is eight megabytes of padding, and a
		// failure message nobody can read is the same defect as a stack
		// buried in a log line.
		t.Error("the lapse was not written down: the log would go on claiming a " +
			"protection it has lost")
	}

	// And it must not be claimed again, nor retried at every rotation while
	// every trace in between goes nowhere.
	before := len(*seq)
	w.Write([]byte(strings.Repeat("x", MaxBytes+1)))
	for _, step := range (*seq)[before:] {
		if step == "take" {
			t.Errorf("it took the standard error again after giving up on it: %v", *seq)
		}
	}
}

// It declines where somebody is already looking, and that is the rule: we
// stand in for a standard error that is not there, never take away one that is.
func TestItDeclinesWhereThereIsAStandardError(t *testing.T) {
	dir := t.TempDir()
	seq := watch(t, false)

	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if w.CaptureCrashes() {
		t.Error("it took a standard error that was already somebody's")
	}
	if len(*seq) != 0 {
		t.Errorf("it touched the handle anyway: %v", *seq)
	}
}

// Whoever never asked must not be touched: a rotation on an ordinary log
// carries no dealings with the standard error at all.
func TestWithoutAskingNothingIsTaken(t *testing.T) {
	dir := t.TempDir()
	seq := watch(t, true)

	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.Write([]byte(strings.Repeat("x", MaxBytes+1)))

	if len(*seq) != 0 {
		t.Errorf("a log that asked for nothing moved the standard error: %v", *seq)
	}
}

// Closing gives it back too, and for the same reason as the rotation: the file
// is about to stop existing.
func TestClosingGivesItBack(t *testing.T) {
	dir := t.TempDir()
	seq := watch(t, true)

	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	w.CaptureCrashes()
	w.Close()

	if got := strings.Join(*seq, ","); got != "take,give" {
		t.Errorf("the order was %q, wanted take,give", got)
	}
}
