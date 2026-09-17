package doc

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// **The fingerprint is the line that matters, and nothing computed it.**
//
// Two artefacts travel in the binary without being modules, so no tool sees
// them: `internal/ced/ced-tiny-q8_0.gguf`, six megabytes of trained weights that
// belong to somebody else, and `internal/opuswasm/libopus.wasm`, libopus plus a
// C bridge we cannot rebuild without a WebAssembly toolchain. Each carries a
// sha256 beside its `go:embed`, and each says in as many words that the
// fingerprint — not the address it came from — is the only thing that says
// whether what is downloaded again is what we are running.
//
// It was true when this was written, measured on the files themselves. What was
// missing is anything that keeps it true: `sha256.Sum256` appeared exactly once
// in the whole tree, and it was the machine fingerprint that names the Tailscale
// node. The day the model is regenerated — a new conversion, the f16 swapped in,
// a re-download — the **three** places that carry its hash go on describing a
// file that is no longer in the binary: the `go:embed`, the provenance of the
// parity run in `baselines/ced.txt`, and the attribution in
// `licenses/manually-added/ced-tiny/NOTICE`. The third is what makes it a
// finding rather than an omission: a repository declaring the wrong fingerprint
// for the code it redistributes.
//
// The parity test over in `internal/ced` would catch a swapped model, because a
// different file moves the scores. What it cannot say is **which** file moved
// them.
//
// **Nothing here is written by hand.** The artefacts are found by walking the
// tree, the expected hash is the hash of the bytes, and every place that carries
// one is found by reading it — so a fourth copy added tomorrow is covered with
// nothing to remember. A fingerprint whose nearest preceding mention is a file
// we do not ship is left alone, which is how `workbench/ced-tiny-f16.gguf` in
// `baselines/ced.txt` — eleven megabytes that exist only on the disk of whoever
// took that measurement — is correctly compared with nothing.

// nearbyLines is how far above a fingerprint its file may be named. The four
// declarations in the tree sit between one and five lines apart; eight leaves
// room without reaching the next paragraph.
const nearbyLines = 8

// maxScanned skips anything too large to be prose about an artefact.
const maxScanned = 4 * 1024 * 1024

type blob struct {
	name string
	sha  string
	size int
}

// hexRuns returns the maximal runs of lower-case hex digits in a line.
func hexRuns(s string) []string {
	var out []string
	start := -1
	isHex := func(b byte) bool {
		return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
	}
	for i := 0; i <= len(s); i++ {
		if i < len(s) && isHex(s[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, s[start:i])
			start = -1
		}
	}
	return out
}

// digitsOnly strips the thousands separators a size is written with.
func digitsOnly(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// sizeBefore reads the "6,211,616 bytes" a line declares, or "" for a line that
// declares none.
func sizeBefore(line string) string {
	i := strings.Index(line, " bytes")
	if i < 0 {
		return ""
	}
	j := i
	for j > 0 {
		c := line[j-1]
		if (c >= '0' && c <= '9') || c == ',' {
			j--
			continue
		}
		break
	}
	if j == i {
		return ""
	}
	return digitsOnly(line[j:i])
}

// ours keeps the walk inside what this repository writes.
func ours(d fs.DirEntry) bool {
	switch d.Name() {
	case ".git", "bin", "workbench":
		return false
	}
	return true
}

// shippedBlobs finds the artefacts that travel in the binary without being
// modules, and hashes them.
func shippedBlobs(t *testing.T) map[string]blob {
	t.Helper()
	out := map[string]blob{}
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !ours(d) {
				return fs.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".gguf", ".wasm":
		default:
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		name := filepath.Base(path)
		out[name] = blob{name: name, sha: hex.EncodeToString(sum[:]), size: len(b)}
		return nil
	})
	if err != nil {
		t.Fatalf("looking for the embedded artefacts: %v", err)
	}
	return out
}

func TestTheEmbeddedArtefactsCarryTheirOwnFingerprint(t *testing.T) {
	blobs := shippedBlobs(t)
	if len(blobs) < 2 {
		t.Fatalf("found %d embedded artefacts, expected at least 2 (the model and "+
			"libopus): this guard is looking at the wrong tree", len(blobs))
	}

	checkedHashes, checkedSizes := 0, 0
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !ours(d) {
				return fs.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".md", ".txt", ".ps1", "":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) > maxScanned {
			return nil
		}
		lines := strings.Split(string(raw), "\n")

		// lastNamed is the artefact most recently named, and on which line. A
		// fingerprint belongs to it when the two are near enough; one preceded by
		// nothing, or by a file we do not ship, belongs to somebody else and is
		// left alone.
		lastNamed, lastLine := "", -1
		for i, line := range lines {
			line = strings.TrimSuffix(line, "\r")
			for base := range blobs {
				if strings.Contains(line, base) {
					lastNamed, lastLine = base, i
				}
			}
			// A size is read only from the line that names the artefact: a number
			// of bytes further down a paragraph is about something else.
			if lastLine == i && lastNamed != "" {
				if got := sizeBefore(line); got != "" {
					checkedSizes++
					b := blobs[lastNamed]
					if want := itoa(b.size); got != want {
						t.Errorf("%s:%d: %s is declared %s bytes and is %s",
							path, i+1, b.name, got, want)
					}
				}
			}
			for _, run := range hexRuns(line) {
				if len(run) != 64 || lastNamed == "" || i-lastLine > nearbyLines {
					continue
				}
				b := blobs[lastNamed]
				checkedHashes++
				if run != b.sha {
					t.Errorf("%s:%d: the fingerprint declared for %s is stale. "+
						"declared %s, actual %s. Every place that carries it has to be "+
						"brought over together: a repository that names the wrong hash "+
						"for the code it redistributes is worse than one that names none.",
						path, i+1, b.name, run, b.sha)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	// The floors. At the time of writing the model has its hash in three places
	// and libopus in one, and two of those four carry a size as well. Fewer means
	// the shape moved and this guard is absolving whatever replaced it.
	if checkedHashes < 4 {
		t.Errorf("compared %d fingerprints, expected at least 4: a declaration has "+
			"moved out of this guard's sight, and green here would mean nothing",
			checkedHashes)
	}
	if checkedSizes < 2 {
		t.Errorf("compared %d declared sizes, expected at least 2", checkedSizes)
	}
}

// itoa keeps this file free of one import for a single call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
