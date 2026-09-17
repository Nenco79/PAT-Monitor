// pat-icon generates the resources the linker embeds in the executable: the
// icon and the version number.
//
// `build.ps1` calls it before compiling. The `.syso` is not in the repository —
// `.gitignore` excludes build products, and this is one: it derives entirely
// from the drawing and from the numbers passed below, which are code instead.
// Given the same arguments two runs produce the same bytes, so regenerating it
// on every build dirties nothing.
//
// **Icon and version live in the same file**, and that is not a convenience: an
// executable has one resource section, and two `.syso` in the same directory do
// not add up.
//
// With -ico it also writes the .ico file, which the binary does not need but is
// the only way to **look at** the icon without installing it somewhere.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"patmonitor/internal/icon"
	"patmonitor/internal/version"
)

var (
	out     = flag.String("o", filepath.Join("cmd", "pat-monitor", "icon_windows.syso"), "where to write the resource")
	arch    = flag.String("arch", "amd64", "target architecture")
	icoFile = flag.String("ico", "", "also write the .ico file here (optional)")

	// Revision, commit and tree state cannot be read from here: at run time the
	// linker stamps them, and this program runs **before**. build.ps1 passes
	// them, having already computed them for the stamps.
	rev      = flag.String("rev", "", "commit count, the r of the version")
	commit   = flag.String("commit", "", "short hash of the commit")
	modified = flag.Bool("modified", false, "the working tree has uncommitted changes")

	// With this it prints the product number alone and exits, writing nothing.
	//
	// `build.ps1` needs it for its summary line, so that a build says which
	// version it has just produced — the question one asks on the eve of a tag.
	// The number lives in `version.Number` and has to stay there: reading it
	// from PowerShell with a regular expression would be **a second reader of
	// the format**, and `1.0.0-beta.1` is exactly what a hand-written rule for
	// `x.y.z` breaks on, in silence.
	printVersion = flag.Bool("print-version", false, "print the product number and exit")

	// **Whether this number is a pre-release is asked here and not worked out
	// by whoever needs it.** `internal/version` owns that fact — `Prerelease`
	// reads the "-" of semver, and the PE properties already depend on it — and
	// the release workflow needs the same answer to decide whether GitHub's
	// pre-release checkbox goes on. Left to the caller it would be one
	// comparison written a second time, in another language, where nothing
	// would ever compare the two; and getting it wrong there does not produce a
	// wrong file property, it offers a beta to every installation as the latest
	// stable release.
	printPrerelease = flag.Bool("print-prerelease", false, "print yes or no: whether the product number is a pre-release, and exit")
)

func main() {
	flag.Parse()

	if *printVersion {
		fmt.Println(version.Number)
		return
	}

	if *printPrerelease {
		if version.Prerelease() {
			fmt.Println("yes")
		} else {
			fmt.Println("no")
		}
		return
	}

	im := icon.Images()

	if *icoFile != "" {
		if err := os.WriteFile(*icoFile, icon.ICO(im), 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("%s  (%d sizes)\n", *icoFile, len(im))
	}

	data, err := icon.Syso(im, versionInfo(), *arch)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%s  (%d sizes, version %s, %d KB)\n",
		*out, len(im), version.Full(), len(data)/1024)
}

// versionInfo composes what ends up in the file's properties.
//
// **The string is composed by `version.Full`, not by this file.** The values the
// linker stamps at run time are assigned here by hand, and from there on the
// format is the usual one: so the line Explorer shows and the line the monitor
// writes in the log are **the same function**, and cannot diverge. Writing it
// here a second time would be the second list that diverges from the first.
func versionInfo() *icon.VersionInfo {
	version.Revision = *rev
	version.Commit = *commit
	if *modified {
		version.Modified = "yes"
	}

	major, minor, patch := version.Digits()
	return &icon.VersionInfo{
		Major: uint16(major), Minor: uint16(minor), Patch: uint16(patch),
		Build: build(*rev),

		Product: version.Product,
		// **Whoever uses the monitor at home reads this line too**, and that
		// was not planned: Windows puts it at the top of the tray
		// notifications, where the program's name belongs. Under a sentence
		// that opens with a category, the notice that remote access is open
		// arrives under the heading of a brochure.
		//
		// It therefore starts with the name, and the name **is taken, not
		// rewritten**: a second copy of "PAT Monitor" diverges at the first
		// rename. The rest stays English like LICENSE and NOTICE — whoever
		// redistributes reads it, and so do the tools that scan executables.
		Description:  version.Product + " - webcam over WebRTC",
		Version:      version.Full(),
		Copyright:    "Copyright 2026 Nenco79 — Apache-2.0",
		FileName:     "pat-monitor.exe",
		InternalName: "pat-monitor",
		// The pre-release flag follows the number's **label**, not its digits:
		// `version.Prerelease` looks at what `Number` states, so a
		// 1.0.0-beta.1 declares itself for what it is.
		Prerelease: version.Prerelease(),
		Modified:   *modified,
	}
}

// build converts the commit count into the fourth field.
//
// It is a `uint16`, so it stops at 65535. That is not a limit this project will
// ever reach, but a silent truncation there would produce a version that **goes
// backwards** after commit 65536 — it saturates instead of wrapping, which is
// the one of the two mistakes that gets noticed.
func build(s string) uint16 {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		if n = n*10 + int(c-'0'); n > 65535 {
			return 65535
		}
	}
	return uint16(n)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pat-icon:", err)
	os.Exit(1)
}
