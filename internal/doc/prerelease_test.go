package doc

import (
	"os"
	"strings"
	"testing"
)

// **The release script and `pat-icon` agree on one word, and nothing compared
// them.** `-print-prerelease` prints `yes`, `build.ps1 -Publish` tests
// `-eq 'yes'`, and the two literals are written in two languages in two files.
// Change the Go side to print `true` and the script goes on running, goes on
// succeeding, and stops passing `--prerelease` — so the release is published
// without GitHub's mark, `/releases/latest` stops excluding it, and a beta is
// offered to every installation as the latest stable version.
//
// That is the worst outcome this repository has an automatic path to, and it
// arrives with every step green. It is the shape `TestThePageKnowsTheManualClipCode`
// already guards one language across, and the shape the whole pre-release policy
// rests on: one checkbox, so whatever sets it has to be right.
//
// The comparison lived in a release workflow until releases moved to the machine
// that holds the signing key; the guard moved with it.

const releaseScript = "../../build.ps1"
const patIconMain = "../../cmd/pat-icon/main.go"

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", path, err)
	}
	return string(b)
}

// psCode is a PowerShell script with its comment lines removed. The comments
// above the publishing block tell the story of this very policy, so a guard
// that searched them would find the words it wants whether or not the code
// still carried them.
func psCode(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// printedWord is the word pat-icon prints when the number is a pre-release: the
// first string printed inside the -print-prerelease branch.
func printedWord(t *testing.T, src string) string {
	t.Helper()

	_, after, ok := strings.Cut(src, "if *printPrerelease {")
	if !ok {
		t.Fatal("cmd/pat-icon: the -print-prerelease branch is not there, so this guard is looking at nothing")
	}
	body, _, _ := strings.Cut(after, "\n\t}")

	_, after, ok = strings.Cut(body, `fmt.Println("`)
	if !ok {
		t.Fatal("cmd/pat-icon: the -print-prerelease branch prints nothing this guard can read")
	}
	word, _, _ := strings.Cut(after, `"`)
	return word
}

// comparedWord is the word the script tests pat-icon's answer against.
func comparedWord(t *testing.T, ps string) string {
	t.Helper()

	_, after, ok := strings.Cut(ps, "$pre -eq '")
	if !ok {
		t.Fatal("build.ps1: nothing compares $pre, so the pre-release mark is set by nobody")
	}
	word, _, _ := strings.Cut(after, "'")
	return word
}

func TestTheReleaseScriptAndPatIconAgreeOnThePreReleaseWord(t *testing.T) {
	printed := printedWord(t, read(t, patIconMain))
	compared := comparedWord(t, psCode(read(t, releaseScript)))

	if printed == "" || compared == "" {
		t.Fatalf("read %q from pat-icon and %q from build.ps1: this guard is looking at nothing", printed, compared)
	}
	if printed != compared {
		t.Errorf("pat-icon prints %q and build.ps1 compares against %q, so a pre-release would be "+
			"published without GitHub's mark and offered as the latest stable version", printed, compared)
	}
}

// Asking and acting are two halves and the test above sees neither: with the
// comparison removed the words still match, and with `--prerelease` removed the
// script still asks. Both are one line, and one line is what gets deleted while
// tidying.
func TestTheReleaseScriptBothAsksAndMarks(t *testing.T) {
	ps := psCode(read(t, releaseScript))

	for _, want := range []string{"-print-prerelease", "'--prerelease'"} {
		if !strings.Contains(ps, want) {
			t.Errorf("build.ps1 does not carry %q: the pre-release checkbox is the whole policy, "+
				"and nothing else sets it", want)
		}
	}
}
