// pat-licenses collects the licences of everything that ends up in the binary.
//
// **It is a program and not a hand-written folder because a hand-written folder
// ages at the first `go get`.** The licences that have to be distributed are
// those of the code we actually ship, and that list changes on its own when a
// dependency changes: keeping it aligned from memory is how one ends up
// shipping a binary containing something whose copyright notice is not being
// carried.
//
// It starts from the **packages linked into `cmd/pat-monitor`**, not from
// `go.mod`. Those are two different lists: `go.mod` also carries what only the
// tests or another operating system need, and reporting licences for code we do
// not ship is not more scrupulous, it is noise that hides the real entries.
//
// **If a module has no licence file, the program fails.** That is the only
// check that matters: a module without a licence is not a detail to annotate
// later, it is code that cannot be redistributed, and it has to be looked at
// before publishing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var (
	outDir   = flag.String("o", "licenses", "folder to rewrite")
	target   = flag.String("pkg", "patmonitor/cmd/pat-monitor", "the binary whose dependencies to collect")
	listOnly = flag.Bool("l", false, "list only, writing nothing")
)

// The files that accompany the code and have to be carried with it. `PATENTS`
// is not a licence but the patent grant Google attaches to Go and to its `x/`
// modules, and it goes with the licence it refers to; `NOTICE` is required by
// clause 4(d) of Apache 2.0.
var carried = []string{
	"LICENSE", "LICENSE.txt", "LICENSE.md", "LICENCE", "LICENSE-BSD",
	"COPYING", "COPYING.txt",
	"NOTICE", "NOTICE.txt",
	"PATENTS", "AUTHORS",
}

type module struct {
	Path    string
	Version string
	Dir     string
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pat-licenses:", err)
		os.Exit(1)
	}
}

func run() error {
	mods, err := modules()
	if err != nil {
		return err
	}

	type entry struct {
		mod   module
		files []string
		kind  string
	}
	var entries []entry
	var missing []string

	for _, m := range mods {
		var found []string
		for _, n := range carried {
			p := filepath.Join(m.Dir, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				found = append(found, n)
			}
		}
		if len(found) == 0 {
			missing = append(missing, m.Path+"@"+m.Version)
			continue
		}
		k, err := licenceKind(filepath.Join(m.Dir, found[0]))
		if err != nil {
			return err
		}
		entries = append(entries, entry{m, found, k})
	}

	if len(missing) > 0 {
		return fmt.Errorf("modules with no licence file, to be looked at by hand:\n  %s",
			strings.Join(missing, "\n  "))
	}

	count := map[string]int{}
	for _, e := range entries {
		count[e.kind]++
	}
	kinds := make([]string, 0, len(count))
	for k := range count {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Printf("  %-14s %d\n", k, count[k])
	}
	fmt.Printf("  %-14s %d modules\n", "total", len(entries))

	if *listOnly {
		return nil
	}

	// The folder is rewritten from scratch: a dependency that has been removed
	// must disappear from here too, and a file left behind states something
	// that is no longer true. Only what this program generates is touched —
	// `manually-added/` is not, because we keep that one.
	if err := wipe(*outDir); err != nil {
		return err
	}

	var index strings.Builder
	index.WriteString(indexHeader)
	for _, e := range entries {
		dst := filepath.Join(*outDir, filepath.FromSlash(e.mod.Path))
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for _, n := range e.files {
			b, err := os.ReadFile(filepath.Join(e.mod.Dir, n))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dst, n), b, 0o644); err != nil {
				return err
			}
		}
		fmt.Fprintf(&index, "| `%s` | %s | %s | %s |\n",
			e.mod.Path, e.mod.Version, e.kind, strings.Join(e.files, ", "))
	}
	if err := os.WriteFile(filepath.Join(*outDir, "README.md"), []byte(index.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("  written to %s\n", *outDir)
	return nil
}

// modules lists the modules of the packages that end up in the binary.
func modules() ([]module, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", *target)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// "exit status 1" is not a diagnosis: what is needed go list writes on
		// stderr, and discarding it sends the reader looking elsewhere.
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	seen := map[string]module{}
	for dec.More() {
		var p struct {
			Module *struct{ Path, Version, Dir string }
		}
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		if p.Module == nil || p.Module.Version == "" || p.Module.Dir == "" {
			continue // our own module, and the standard library
		}
		seen[p.Module.Path] = module{p.Module.Path, p.Module.Version, p.Module.Dir}
	}
	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}

// licenceKind recognises the licence from its text.
//
// **The order of the cases matters and is not alphabetical.** ISC and two-clause
// BSD start almost the same, and the sentence that tells a three-clause BSD from
// the others — "neither the name" — appears at the end: whoever checks the
// generic formula first classifies them all the same way. Recognising a
// permissive licence wrongly does not change what may be done with it, but it
// puts a false statement in the index, and the index is there to be believed.
func licenceKind(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	t := strings.ToLower(string(b))
	switch {
	case strings.Contains(t, "apache license"):
		return "Apache-2.0", nil
	case strings.Contains(t, "gnu general public"):
		return "GPL", nil
	case strings.Contains(t, "mozilla public license"):
		return "MPL-2.0", nil
	case strings.Contains(t, "permission is hereby granted, free of charge"):
		return "MIT", nil
	case strings.Contains(t, "neither the name"):
		return "BSD-3-Clause", nil
	case strings.Contains(t, "permission to use, copy, modify"):
		return "ISC", nil
	case strings.Contains(t, "redistribution and use in source and binary"):
		return "BSD-2-Clause", nil
	}
	return "TO CHECK", nil
}

func wipe(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "manually-added" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

const indexHeader = `# Third-party licenses

**This directory is generated** by ` + "`go run ./cmd/pat-licenses`" + `. Do not edit
it by hand: the list comes from the packages actually linked into
` + "`pat-monitor.exe`" + `, so it follows the dependencies instead of being a snapshot
of whenever someone last looked. What is kept by hand lives in
` + "`manually-added/`" + `, with the reason next to it.

PAT Monitor is distributed under the Apache License 2.0 (see ` + "`LICENSE`" + ` in
the repository root). The licenses below cover other people's code that travels
inside the same executable. They are all permissive: none of them places
conditions on the code that uses them.

| module | version | license | files |
|---|---|---|---|
`
