package version

import (
	"regexp"
	"testing"
)

// **The obvious spelling is `Number + ".0"`, and it is wrong on the one number
// that matters.**
//
// A manifest takes four numeric fields and nothing else, so a pre-release label
// cannot come along: concatenation composes `1.1.0-beta.1.0`, which no manifest
// accepts, and the failure arrives at whoever is packaging rather than at
// whoever wrote the rule. `Digits` already drops the label, so the composition
// has to go through it.
//
// **Verified to catch**: with `Package` composing `Number + ".0"`, the
// pre-release case fails carrying the label.
func TestThePackageVersionIsFourNumbersEvenOnAPreRelease(t *testing.T) {
	shape := regexp.MustCompile(`^\d+\.\d+\.\d+\.0$`)

	for _, number := range []string{"1.1.0", "1.1.0-beta.1", "0.9.3-rc.2", "2.0.0"} {
		got := packageVersion(number)
		if !shape.MatchString(got) {
			t.Errorf("%s -> %q, which is not four numbers ending in zero", number, got)
		}
	}
}

// **The fourth field is a zero and not the commit count.** Putting `r` there is
// what the Store reserves the field against; putting it in the third is what
// would make the package announce a different product for a commit that changed
// a comment. It carries neither, and that is the decision rather than an
// omission — the file says which build, the package says which release.
func TestThePackageVersionCarriesNoBuildNumber(t *testing.T) {
	// The revision is what a stamped build has, and it must not reach the
	// version whatever it is.
	old := Revision
	t.Cleanup(func() { Revision = old })
	Revision = "248"

	if got := Package(); got != "1.1.0.0" {
		t.Errorf("Package() = %q with r=248, wanted the release and not the build", got)
	}
}
