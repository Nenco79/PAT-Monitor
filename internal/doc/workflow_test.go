package doc

import (
	"os"
	"strings"
	"testing"
)

// **The release workflow and `pat-icon` agree on one word, and nothing compared
// them.** `-print-prerelease` prints `yes`, the workflow tests `-eq 'yes'`, and
// the two literals are written in two languages in two files. Change the Go
// side to print `true` and the workflow goes on running, goes on succeeding, and
// stops passing `--prerelease` — so the release is published without GitHub's
// mark, `/releases/latest` stops excluding it, and a beta is offered to every
// installation as the latest stable version.
//
// That is the worst outcome this repository has an automatic path to, and it
// arrives with every step green. It is the shape `TestThePageKnowsTheManualClipCode`
// already guards one language across, and the shape the whole pre-release policy
// rests on: one checkbox, so whatever sets it has to be right.

const releaseWorkflow = "../../.github/workflows/release.yml"
const patIconMain = "../../cmd/pat-icon/main.go"

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", path, err)
	}
	return string(b)
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

// comparedWord is the word the workflow tests PRERELEASE against.
func comparedWord(t *testing.T, yml string) string {
	t.Helper()

	_, after, ok := strings.Cut(yml, "$env:PRERELEASE -eq '")
	if !ok {
		t.Fatal("release.yml: nothing compares PRERELEASE, so the pre-release mark is set by nobody")
	}
	word, _, _ := strings.Cut(after, "'")
	return word
}

func TestTheWorkflowAndPatIconAgreeOnThePreReleaseWord(t *testing.T) {
	printed := printedWord(t, read(t, patIconMain))
	compared := comparedWord(t, read(t, releaseWorkflow))

	if printed == "" || compared == "" {
		t.Fatalf("read %q from pat-icon and %q from the workflow: this guard is looking at nothing", printed, compared)
	}
	if printed != compared {
		t.Errorf("pat-icon prints %q and release.yml compares against %q, so a pre-release would be "+
			"published without GitHub's mark and offered as the latest stable version", printed, compared)
	}
}

// Asking and acting are two halves and the test above sees neither: with the
// comparison removed the words still match, and with `--prerelease` removed the
// workflow still asks. Both are one line, and one line is what gets deleted
// while tidying.
func TestTheWorkflowBothAsksAndMarks(t *testing.T) {
	yml := read(t, releaseWorkflow)

	for _, want := range []string{"-print-prerelease", "--prerelease"} {
		if !strings.Contains(yml, want) {
			t.Errorf("release.yml does not carry %q: the pre-release checkbox is the whole policy, "+
				"and nothing else sets it", want)
		}
	}
}
