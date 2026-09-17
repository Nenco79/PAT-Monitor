package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Rotation is code that looks obvious and goes wrong quietly: walking the files
// in the wrong direction has them overwrite one another, and the defect stays
// invisible until yesterday's file is the one needed.
//
// **Counting the files and weighing them says nothing about the order.** Both
// are true properties — the second one is the promise about the disk of
// whoever hosts the monitor, and it stays — but the wrong direction would only
// be caught through a side effect: a rotation that keeps four files and fills
// them in the wrong order would pass.
//
// So the order is read, and this is the only way to read it: every line carries
// its own number, and the numbers have to fall going from monitor.log to .1,
// .2 and .3.
func TestRotationKeepsTheOrder(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// **More is written than the folder can hold**: it is the only way to prove
	// the oldest files really do get deleted. Stopping at Keep rotations would
	// only check that the names shift along.
	const lineLen = 1024
	writes := (MaxBytes / lineLen) * (Keep + 2)
	for i := 0; i < writes; i++ {
		// The sequence number at the head of the line is what makes it possible
		// to ask a file **when** it was written instead of how much it weighs.
		line := fmt.Sprintf("%08d%s\n", i, strings.Repeat("x", lineLen-9))
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// **The names are built rather than listed**, and that is the stronger
	// question: the rotation's contract is that the file in use keeps the
	// plain name and the older ones carry a growing number, so asking for
	// exactly those names asserts the contract instead of asserting that
	// some helper sorted a folder.
	files := []string{filepath.Join(dir, "monitor.log")}
	for i := 1; i <= Keep; i++ {
		files = append(files, filepath.Join(dir, fmt.Sprintf("monitor.%d.log", i)))
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s is missing: %v", filepath.Base(f), err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil {
		t.Fatal(err)
	} else if len(entries) != Keep+1 {
		t.Fatalf("the folder holds %d files, wanted %d: the oldest are not being "+
			"deleted", len(entries), Keep+1)
	}

	// **The order, which is what this test is named for.** Files hands them
	// over most recent first, so the starting numbers have to fall: if they do
	// not, two files have jumped over each other and whoever is after
	// yesterday's fault reads the day before's, with nothing to say so.
	for i := 1; i < len(files); i++ {
		newer, older := firstSequence(t, files[i-1]), firstSequence(t, files[i])
		if newer <= older {
			t.Errorf("%s starts at %d and %s at %d: the most recent is not the first",
				filepath.Base(files[i-1]), newer, filepath.Base(files[i]), older)
		}
	}

	// And the folder does not grow past the limit: it is the only property that
	// stops a monitor left on for months from filling the disk.
	var total int64
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		total += st.Size()
	}
	if max := int64(MaxBytes) * (Keep + 1); total > max {
		t.Errorf("the logs take %d bytes, past the limit of %d", total, max)
	}
}

// firstSequence is the sequence number of the first whole line of a log.
//
// It looks for the first line that carries a number instead of taking line
// zero: a file that starts where the previous write stopped need not start on a
// line boundary, and a test that assumed it would break when MaxBytes changes —
// blaming rotation for a defect of its own.
func firstSequence(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) < 8 {
			continue
		}
		if n, err := strconv.Atoi(line[:8]); err == nil {
			return n
		}
	}
	t.Fatalf("%s carries no sequence number", filepath.Base(path))
	return 0
}

// Reopening continues the file instead of emptying it: a restart must not erase
// the lines that explain why there was a restart.
func TestItReopensAtTheEnd(t *testing.T) {
	dir := t.TempDir()

	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("before the restart\n"))
	w.Close()

	w2, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	w2.Write([]byte("after the restart\n"))
	w2.Close()

	b, err := os.ReadFile(filepath.Join(dir, "monitor.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"before the restart", "after the restart"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%q is missing from the reopened log", want)
		}
	}
}

// A log that cannot be written must not stop the monitor: it is a witness, and
// a witness that falls does not stop what it was watching.
func TestAFailedWriteDoesNotPropagate(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "monitor.log")
	if err != nil {
		t.Fatal(err)
	}
	// Close the file underneath, which is what happens if someone deletes the
	// folder or the disk fills up.
	w.f.Close()

	if _, err := w.Write([]byte("any line at all\n")); err != nil {
		t.Errorf("a failed write propagated the error: %v", err)
	}
	// And it does not retry forever, otherwise every log line would generate as
	// much noise as the log itself.
	if _, err := w.Write([]byte("another one\n")); err != nil {
		t.Errorf("second write: %v", err)
	}
}
