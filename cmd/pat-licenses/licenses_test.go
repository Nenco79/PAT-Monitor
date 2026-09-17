package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// **A folder that is generated and committed ages in silence.** One `go get` is
// enough for the distributed licences to stop being those of the distributed
// code: no error, no warning, and the defect surfaces when somebody asks about
// a copyright notice that is not there. These tests are the only thing that
// says so.
//
// They run under `go test ./...`, so the question is asked on its own every
// time.

func TestEveryModuleHasALicence(t *testing.T) {
	// The question that comes before all the others: a module with no licence
	// file is not a gap to annotate, it is code that cannot be redistributed.
	mods, err := modules()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) == 0 {
		t.Fatal("no modules: the enumeration did not work, and a test that looks at nothing always passes")
	}
	for _, m := range mods {
		found := false
		for _, n := range carried {
			if st, err := os.Stat(filepath.Join(m.Dir, n)); err == nil && !st.IsDir() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s@%s has no licence file at all", m.Path, m.Version)
		}
	}
}

func TestTheFolderFollowsTheDependencies(t *testing.T) {
	mods, err := modules()
	if err != nil {
		t.Fatal(err)
	}
	const dir = "../../licenses"

	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("the index is not there: %v — regenerate with `go run ./cmd/pat-licenses`", err)
	}
	text := string(index)

	// One way round: every dependency must have its folder and its row.
	expected := map[string]bool{}
	for _, m := range mods {
		expected[m.Path] = true
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m.Path))); err != nil {
			t.Errorf("%s is in the binary but not in licenses/ — regenerate", m.Path)
		}
		if !strings.Contains(text, "`"+m.Path+"`") {
			t.Errorf("%s does not appear in the index — regenerate", m.Path)
		}
		if !strings.Contains(text, m.Version) {
			t.Errorf("%s: the index does not carry version %s — regenerate", m.Path, m.Version)
		}
	}

	// **And the other way round, which is the one that gets forgotten.** A
	// dependency that has been removed leaves its folder there, and from that
	// moment the repository states that it distributes code it no longer
	// distributes: not dangerous, but false, and the kind of thing nobody
	// re-reads.
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		p := line[3:]
		p = p[:strings.Index(p, "`")]
		if !expected[p] {
			t.Errorf("%s is in the index but not in the binary — regenerate", p)
		}
	}
}

func TestWhatWeKeepByHandSurvives(t *testing.T) {
	// The generator rewrites the folder from scratch, and `manually-added/` has
	// to survive: it holds the things no module declares — libopus, which we
	// ship embedded, the standard library, and the Tabler icons pasted into the
	// page. If a rearrangement carried them off, the binary redistribution of
	// libopus would be left without its notice, which is exactly what its
	// licence asks for, and the icons without attribution.
	for _, f := range []string{
		"manually-added/README.md",
		"manually-added/opus/COPYING",
		// **This is the entry the generator would have removed.** The code
		// arrived as a module, so the folder wrote itself; since the glue
		// became ours the module is no longer in `go.mod`, but we still ship
		// `libopus.wasm`, C wrapper included. The other way round of the test
		// above asked for a regeneration, and regenerating would have made the
		// notice disappear with everything green.
		"manually-added/opus-wasm-bridge/LICENSE",
		"manually-added/opus-wasm-bridge/AUTHORS",
		"manually-added/go/LICENSE",
		"manually-added/go/PATENTS",
		"manually-added/tabler-icons/LICENSE",
	} {
		if _, err := os.Stat(filepath.Join("../../licenses", f)); err != nil {
			t.Errorf("%s is missing: %v", f, err)
		}
	}
}
