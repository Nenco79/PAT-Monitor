// Package version says which build is running.
//
// It exists because "it does not work" is not a report until you know which
// binary it came from. With a beta in someone else's hands, the first question
// about any fault is what code was running, and without a number the answer is
// a reconstruction from memory.
//
// The identifier is in three pieces, and each is decided by whoever knows
// something about it:
//
//   - Number is written by hand here, and this is the only place it lives. It
//     changes when the product changes, not when the code changes.
//
//   - Revision is the commit count, stamped by the linker. It grows on its own
//     with every commit and needs no file kept up to date: a counter in a file
//     would collide on every merge and give different numbers on different
//     machines, which are precisely the two things an identifier must not do.
//     Two builds from the same commit are the same code, and it is right that
//     they carry the same number.
//
//     What can break it is a change of history, and that has to be known
//     beforehand: rewriting commits shortens the count, recreating the
//     repository takes it back to one. The r then goes backwards, and from
//     there on it names code other than the code it named before — while
//     binaries carrying the old numbers are already on other machines. Hence
//     the rule: history changes before anything is built that leaves this
//     machine, and if something has already left, the product number goes up,
//     which is the other half of the identifier.
//
//   - Commit and Modified say where exactly. The second is the half that keeps
//     the first honest: built with uncommitted changes, the code is not the
//     code of that commit, and staying quiet would turn the identifier into a
//     lie exactly when it is needed, which is while something is being tried.
//
// The values are stamped with the linker rather than read from
// debug.ReadBuildInfo: the revision fields are missing when building from a
// tree with no commits, from a GitHub zip or from a shallow clone, and in that
// case the library discards the information instead of saying so.
package version

import "strings"

// Product is the name shown to the user, from *Pet And Toddler*.
//
// It lives here and not in the tray because the command line and the pages use
// it too, and because the tray package only builds on Windows: a product name
// has no business depending on the operating system.
const Product = "PAT Monitor"

// Number is the product version. It changes by hand, and only here.
//
// **It is not chosen by whoever has just finished something.** A number decided
// on a feeling drifts optimistic on its own, so the rule is outside it: the
// major moves when a milestone closes, and the suffix stays while the open
// issues still carry something the product **is missing**, as opposed to
// something about it that has not been measured.
//
// **That boundary is the whole rule, and the first version of it was the wrong
// half.** It said the suffix comes off when the open proofs are closed — and
// that list is grouped by *what it would take* to close each one,
// three of its four groups being a device nobody here has, a network that is
// not here, and somebody else's data. Under the old wording the suffix could
// never come off by any work done here: it waited on a webcam being bought and
// a dataset being granted. A rule whose condition is outside the project's
// reach is not a rule, it is a suffix for ever.
//
// The suffix has a reader that is not human: Prerelease reads it and turns on
// VS_FF_PRERELEASE in the file properties. That flag has to follow what the
// number asserts — the "-" of semver — and not a consequence of it such as "the
// major is zero": a rule built on a consequence goes wrong the day the shape
// changes, and it goes wrong quietly, because nobody opens the properties sheet
// of an .exe to find a defect.
//
// **The -beta.1 that stood here rested on three things, and two of them have
// since been measured**, which is why they are no longer on that list: NVIDIA,
// in `baselines/capture-nvidia.txt`, and the motion floor in the dark, over a
// night of 2083 samples where the answer was that there is no floor to clear.
// The third stands — the cry threshold rests on 497 clips from two sources,
// ESC-50's forty and donateacry's 457, while barking holds on three independent
// sets — and one provenance short is not a thing the product is missing.
//
// The third digit is for fixes inside the same milestone, and it is not the
// commit count. The question comes naturally — r grows by itself, why not use
// it there? — and the answer is that they are two different things: the first
// three digits say what the product is, r says which build. Merging them would
// jump the patch from 0 to 126 with nothing having happened to the product, and
// would take away the place a real fix goes. In the file properties there is no
// need to choose either: VS_FIXEDFILEINFO has four fields, and the fourth one
// is made for the build number.
const Number = "1.0.0"

// Stamped by build.ps1. The values here are those of a build made by hand with
// `go build`, and they show themselves for what they are instead of pretending.
var (
	Revision = "0"
	Commit   = ""
	Modified = ""
)

// Full is the identifier spelled out: `1.0.0-beta.1 r248 (ea21a23)`.
//
// With uncommitted changes it says so, because that binary matches no commit
// and whoever reads the line has to know.
func Full() string {
	var b strings.Builder
	b.WriteString(Number)
	if Revision != "" && Revision != "0" {
		b.WriteString(" r" + Revision)
	}
	if Commit != "" {
		b.WriteString(" (" + Commit)
		if Modified != "" {
			b.WriteString(", modified")
		}
		b.WriteString(")")
	} else if Modified != "" {
		b.WriteString(" (modified)")
	}
	return b.String()
}

// Digits splits Number into its three parts.
//
// The executable's version resource wants them as numbers and not as text. They
// belong here rather than there because **this file decides the shape of
// Number**: a second reading written elsewhere becomes the copy that diverges
// the day the number takes a different shape.
//
// A digit that does not parse counts as zero instead of stopping the build: the
// number is a constant of this file, so a malformed one shows up at once and
// everywhere, and the resource generator is not the place to discover it.
//
// Digits and Prerelease take the number as a parameter instead of reading the
// constant, for the same reason measuredFPSAt takes the instant: a function
// that interrogates a fixed value can be run, not tested, and what needs
// testing here is not today's shape but that it holds for tomorrow's.
func Digits() (major, minor, patch int) { return digits(Number) }

func digits(number string) (major, minor, patch int) {
	p := strings.SplitN(number, ".", 3)
	n := func(k int) int {
		if k >= len(p) {
			return 0
		}
		v := 0
		for _, c := range p[k] {
			if c < '0' || c > '9' {
				return v
			}
			v = v*10 + int(c-'0')
		}
		return v
	}
	return n(0), n(1), n(2)
}

// Prerelease says whether Number carries a pre-release label, that is whether
// there is a "-" after the digits — the shape semver uses for 1.0.0-beta.1.
//
// **The question is not "is the major zero".** That holds only while the only
// unfinished thing is a 0.x; at 1.0.0-beta.1 the condition switches itself off
// and the executable declares in its properties that it is a final release,
// which is the exact opposite of what the number tells whoever reads it.
//
// It sits next to Digits for the same reason: the shape of Number is decided by
// this file, and a second reading written wherever it happens to be needed
// becomes the copy that diverges.
func Prerelease() bool { return prerelease(Number) }

func prerelease(number string) bool { return strings.Contains(number, "-") }

// Short is what fits in a tooltip or next to a title: `1.0.0-beta.1 r248`.
func Short() string {
	if Revision == "" || Revision == "0" {
		return Number
	}
	return Number + " r" + Revision
}

// Newer says whether the version b is later than a, by semver's ordering.
//
// It lives here for the reason Digits and Prerelease do: **this file decides
// the shape of Number**, and a comparison written where it happens to be needed
// — in the update check, say — becomes the copy that diverges the day the shape
// changes. Both arguments are taken as parameters, so what is tested is the
// rule and not today's constant.
//
// Two properties carry the whole thing, and neither is what a string comparison
// does:
//
//   - **the fields are numbers.** "2.10.3" is later than "2.9.0", and compared
//     as text it is not, because "1" sorts before "9". That is the defect a
//     version comparison written in a hurry always has, and it shows only once
//     a minor reaches ten — that is, long after it was written.
//
//   - **a pre-release sorts below its own release.** 1.0.0-beta.1 is earlier
//     than 1.0.0, which is the opposite of what the longer string suggests. It
//     is what makes a beta offer the release it is a beta of.
//
// Anything it cannot read compares as equal, which answers "not newer": the
// caller is an update check, and the safe answer to a number nobody can parse
// is to say nothing rather than to offer a download.
func Newer(a, b string) bool { return compare(a, b) < 0 }

// Readable says whether a string is one of our release tags at all.
//
// **It exists because "not newer" and "not a version" are the same answer from
// Newer, and they are not the same fact.** Newer answers false for an older
// release and false for a tag nobody can parse, which is the right conflation
// for whoever is deciding whether to offer a download and the wrong one for
// whoever is deciding whether they learned anything. The update check needs
// both, and it had only the first: a tag it could not read came back as
// **you are up to date**, which is a claim built on nothing — the very sentence
// its own empty-tag branch refuses to make.
func Readable(v string) bool {
	_, _, ok := split(v)
	return ok
}

// compare orders two versions: negative when a comes first, zero when they are
// the same, positive when b comes first. Anything it cannot read compares as
// equal, which is what makes Newer answer "not newer" about it.
func compare(a, b string) int {
	an, ap, aok := split(a)
	bn, bp, bok := split(b)
	if !aok || !bok {
		return 0
	}

	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}

	// Same digits: whoever carries a label is the earlier one. Two labels are
	// compared as text, which is enough for beta.1 before beta.2 and is not
	// semver's full rule — the identifiers are meant to be compared field by
	// field, numeric ones numerically. **We do not publish anything that needs
	// the difference**, and the cost of being wrong is offering or withholding
	// one pre-release to somebody who opted into pre-releases, which nobody
	// can do here: the check only ever sees releases GitHub calls latest.
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	default:
		return 1
	}
}

// split separates the three numbers from the pre-release label, and says
// whether what it was given was a version at all.
//
// **The third return is the whole of it, and the first version had it read as a
// zero instead.** A field that is not a number counted as nought and the
// comparison carried on, so `2.x` parsed as 2.0.0 and `2024-01-15` as 2024 with
// a label — that is, a malformed tag and a date-shaped one were both announced
// to every installation as a newer version. The defect sat under a doc comment
// that already promised the opposite, which is the pair this project keeps a
// chapter about: **the claim was right and only the code was wrong, so nothing
// read as suspicious.**
//
// **It asks for three fields, and refusing `1.0` is the point.** This is not a
// general semver parser: it is the thing that decides whether to tell somebody
// their monitor is out of date, and there the useful question is not "can I
// squeeze a number out of this" but "is this unmistakably one of our release
// tags". Every tag this repository has ever carried has three. Whatever does
// not is refused, and a refusal costs a day's silence where a guess costs a
// notice pointing at something that may not be a release at all.
//
// Build metadata — semver's "+" — is dropped before anything else, because by
// specification it takes no part in the ordering: two versions differing only
// there are the same version, and a build stamp reaching this function must not
// be able to make one look newer than the other.
func split(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}

	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return nums, "", false
	}
	for i, p := range fields {
		// An empty field is not a zero: "1..0" and "1.0." are malformed, and
		// reading them as 1.0.0 would make a typo in a tag look like a release.
		if p == "" || len(p) > 9 { // nine digits keeps the multiply inside an int
			return nums, "", false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nums, "", false
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return nums, pre, true
}
