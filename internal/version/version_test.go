package version

import "testing"

// **The digits have to survive the label.**
//
// The executable's version resource wants three integers and derives them from
// here. If `1.0.0-beta.1` gave the wrong patch, the file properties would carry
// a number that is not the product's, and **with no error anywhere**: digits
// counts an unreadable digit as zero on purpose, because the resource generator
// is not the place to find out that the constant is malformed.
//
// The cases sit in one table because they read together: `2.10.3` says a
// two-digit minor is not truncated, `-rc.2` that the label is not only `beta`,
// and `0.6.0` that the earlier shape still holds.
func TestTheDigitsSurviveAPreReleaseLabel(t *testing.T) {
	for _, c := range []struct {
		number     string
		ma, mi, pa int
		pre        bool
	}{
		{"1.0.0-beta.1", 1, 0, 0, true},
		{"1.0.0", 1, 0, 0, false},
		{"0.6.0", 0, 6, 0, false},
		{"2.10.3-rc.2", 2, 10, 3, true},
	} {
		t.Run(c.number, func(t *testing.T) {
			if ma, mi, pa := digits(c.number); ma != c.ma || mi != c.mi || pa != c.pa {
				t.Errorf("%d.%d.%d instead of %d.%d.%d", ma, mi, pa, c.ma, c.mi, c.pa)
			}
			if got := prerelease(c.number); got != c.pre {
				t.Errorf("pre-release %v instead of %v", got, c.pre)
			}
		})
	}
}

// And the real constant has to hold to its own rules.
//
// The table above tests the function on shapes we chose; this tests **the
// number that ends up in the executable**, which is the only one anybody will
// read. A Number written crooked — a space, a letter where a digit belongs —
// would give 0.0.0 in the file properties, and no error anywhere.
func TestTheShippedNumberParses(t *testing.T) {
	ma, mi, pa := Digits()
	if ma == 0 && mi == 0 && pa == 0 {
		t.Fatalf("no digit could be read from %q", Number)
	}
	t.Logf("%s -> %d.%d.%d, pre-release %v", Number, ma, mi, pa, Prerelease())
}

// **The two ways a version comparison is written wrong**, and both of them look
// right until a particular number arrives.
//
// A string comparison gets "2.10.3" and "2.9.0" backwards, because "1" sorts
// before "9" — and it is correct on every other pair, so it ships and waits for
// a minor to reach ten. And the intuitive reading of a pre-release is that the
// longer string is the later one, which is the opposite of what semver says and
// the opposite of what this program needs: a beta has to be offered the release
// it is a beta of.
//
// The table is in pairs of "earlier, later" so that each row reads as the claim
// it makes, and each is checked in **both directions** — asking whether the
// later one is newer, which must be yes, and whether the earlier one is, which
// must be no. One direction alone passes for a function that answers true to
// everything.
func TestNewerOrdersTheWayVersionsActuallyGo(t *testing.T) {
	for _, c := range []struct{ earlier, later string }{
		{"1.0.0", "1.0.1"},
		{"1.0.9", "1.0.10"}, // the two-digit patch
		{"2.9.0", "2.10.3"}, // the one a string comparison gets backwards
		{"1.9.0", "2.0.0"},
		{"1.0.0-beta.1", "1.0.0"}, // a pre-release is earlier than its release
		{"1.0.0-beta.1", "1.0.0-beta.2"},
		{"1.0.0", "1.0.1-beta.1"}, // and still earlier than a later release's beta
		{"0.6.0", "1.0.0"},
	} {
		t.Run(c.earlier+" < "+c.later, func(t *testing.T) {
			if !Newer(c.earlier, c.later) {
				t.Errorf("%q is not seen as later than %q", c.later, c.earlier)
			}
			if Newer(c.later, c.earlier) {
				t.Errorf("%q is seen as later than %q, backwards", c.earlier, c.later)
			}
		})
	}
}

// Equal versions are not newer, in either direction.
//
// It is the case an update check asks **every day for the life of an
// installation** and almost always gets: answering true here would offer the
// running version to itself, for ever.
func TestAVersionIsNotNewerThanItself(t *testing.T) {
	for _, v := range []string{"1.0.0", "1.0.0-beta.1", "2.10.3", Number} {
		if Newer(v, v) {
			t.Errorf("%q is reported as newer than itself", v)
		}
	}
}

// The tag arrives from GitHub as `v1.0.0`, and build metadata takes no part.
//
// The "v" is the shape of a git tag and not of a version, so it is stripped
// rather than left to make every comparison unequal. Build metadata is dropped
// because semver says it is not part of the ordering: two builds of the same
// version are the same version, and a stamp must not be able to make one look
// newer than the other — which, in an update check, would mean offering a
// download that installs what is already running.
func TestTheTagShapeAndBuildMetadataDoNotChangeTheOrder(t *testing.T) {
	if Newer("v1.0.0", "1.0.0") || Newer("1.0.0", "v1.0.0") {
		t.Error("the tag's leading v changes the comparison")
	}
	if !Newer("v1.0.0", "v1.0.1") {
		t.Error("two tags do not compare")
	}
	if Newer("1.0.0+abc", "1.0.0+def") || Newer("1.0.0+def", "1.0.0+abc") {
		t.Error("build metadata changes the comparison")
	}
	if !Newer("1.0.0+abc", "1.0.1+def") {
		t.Error("build metadata hides a real difference")
	}
}

// **What cannot be read answers "not newer", and that is the safe direction.**
//
// The caller is an update check: a number nobody can parse must make it stay
// quiet, never offer a download. Anything else would turn a malformed tag —
// somebody's typo on the GitHub releases page — into a notice pointing every
// installation at it.
//
// **The first version of this test passed with the defect in place**, and the
// reason is the whole lesson: every string it tried began with a letter or a
// dot, so the parser read nought for the major and answered "older" by
// accident. The cases that catch it are the ones that **start with a number
// larger than ours** — `2.x`, `9-hotfix`, `10`, and a date-shaped tag — because
// there a field read as nought still leaves a bigger first number, and the
// answer flips to "newer". Measured before the fix: all four offered an update.
// It is the guards chapter's fourth failure, met by walking into it — a test
// written by the same mistake it should catch catches nothing.
func TestNothingUnreadableIsEverNewer(t *testing.T) {
	for _, junk := range []string{
		"", "latest", "v", "nightly", "..", "-", "1.x",
		// The ones that matter: a bigger leading number carries the comparison
		// even while the rest of the string is not a version.
		"2.x", "9-hotfix", "10", "2024-01-15", "v2024-01-15", "9.9",
		"9.9.9.9", "1.0.0.0", "99999999999999.0.0", "9..9", "9.9.",
	} {
		t.Run(junk, func(t *testing.T) {
			if Newer(Number, junk) {
				t.Errorf("%q is offered as newer than %q", junk, Number)
			}
		})
	}
}

// And the refusal must not swallow the versions it exists to compare.
//
// A guard that refuses everything passes the test above and breaks the feature:
// the check would go quiet for ever, which from outside is a monitor that never
// finds an update — the silence this whole package exists to remove.
//
// **The base is a literal and deliberately not Number**, which is the one thing
// here that was learned rather than designed: written against the constant, the
// list of newer tags was a list of versions newer than *that release*, so
// cutting 1.0.1 made four of its six cases assert that 1.0.1 is newer than
// itself and the suite went red over a rule nobody had touched. `Newer` takes
// both sides as parameters precisely so that what is tested is the rule and not
// today's constant — and a test that feeds it the constant gives that back.
func TestTheRefusalStillReadsARealTag(t *testing.T) {
	const base = "1.0.0"
	for _, good := range []string{"1.0.1", "v1.0.1", "2.10.3", "v10.0.0", "1.0.1-beta.1", "1.0.1+build.7"} {
		if !Newer(base, good) {
			t.Errorf("%q is not seen as newer than %q", good, base)
		}
	}
}
