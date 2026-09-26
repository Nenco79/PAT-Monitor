package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// **A second -generate does not replace the key.** Every installation carries
// the public half of the first, so a new private half written over it strands
// them all, and the command is most likely to be run twice exactly when
// somebody is in a hurry. The refusal is the create itself, so nothing can
// come between a check and the write.
func TestASecondKeyDoesNotReplaceTheFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.key")
	if err := makeKey(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = makeKey(path)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second key over the first answered %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(first) {
		t.Fatal("the first key was overwritten")
	}
}
