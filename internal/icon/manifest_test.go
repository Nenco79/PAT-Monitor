package icon

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const manifestPath = "../../packaging/AppxManifest.xml"

// asset finds every `Assets\Something.png` the manifest names, wherever it
// names it: the three logos sit in two different elements and one of them is an
// element's text rather than an attribute, so looking for the attributes by name
// would be a third list to keep in step.
var asset = regexp.MustCompile(`Assets\\([A-Za-z0-9_-]+)\.png`)

// **The manifest and the generator are held together, because nothing else
// holds them.** A logo the manifest names and the tool does not draw is a
// missing file, and the package format does not fail for one: it installs and
// the shell draws its own fallback, so the first sign is an icon that is not
// ours, somewhere nobody is looking. The other direction is a file written into
// every package and referenced by nothing.
//
// **Verified to catch**: renaming StoreLogo on either side alone fails, naming
// each other's missing half.
func TestTheManifestAndTheGeneratorNameTheSameLogos(t *testing.T) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("the manifest cannot be read, and the package is built from it: %v", err)
	}

	var named []string
	seen := map[string]bool{}
	for _, m := range asset.FindAllStringSubmatch(string(data), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			named = append(named, m[1])
		}
	}
	if len(named) == 0 {
		t.Fatal("the manifest names no logo at all, which no package can be right about")
	}

	var drawn []string
	for _, l := range PackageLogos {
		drawn = append(drawn, l.Name)
	}
	sort.Strings(named)
	sort.Strings(drawn)

	if strings.Join(named, ",") != strings.Join(drawn, ",") {
		t.Errorf("the manifest names [%s] and the generator draws [%s]",
			strings.Join(named, ", "), strings.Join(drawn, ", "))
	}
}

// **The version is a token that cannot be mistaken for a version.**
//
// A placeholder that parses — 0.0.0.0 — builds, installs and ships a package
// claiming to be older than every other, and the Store then refuses the one
// after it for not going up; nothing anywhere gives an error. The token fails
// at MakeAppx instead, while somebody is standing there.
//
// The fourth field is checked with it: the Store reserves that field and takes
// a package whose fourth field is not zero as a package to reject, so a
// substitution that writes the commit count there has to fail here rather than
// at submission.
func TestTheManifestCarriesATokenAndNotAVersion(t *testing.T) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("the manifest cannot be read: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, `Version="{VERSION}"`) {
		t.Error("the version is not the token: a placeholder that parses ships as a real version")
	}
	// **The attribute is matched with its boundary**, because `MinVersion` and
	// `MaxVersionTested` end in `Version="` and carry literal numbers that
	// belong there: without the leading space this reports the template as
	// broken over the two lines that are right. A pattern that matches more
	// than it means is a guard that fails on the good state, which is the one
	// way of making a guard get removed.
	if regexp.MustCompile(`(^|\s)Version="\d+\.\d+\.\d+\.\d+"`).MatchString(s) {
		t.Error("a literal version is in the template, and the substitution would leave it there")
	}
}

// **The capabilities are the ones the program cannot run without**, and they are
// checked because removing one gives a package that installs and then cannot
// open the device: `runFullTrust` is what keeps this a desktop program rather
// than an AppContainer one, and the two device capabilities are what make the
// consent grantable per package at all.
func TestTheManifestDeclaresWhatTheProgramNeeds(t *testing.T) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("the manifest cannot be read: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		`Name="runFullTrust"`,
		`<DeviceCapability Name="webcam" />`,
		`<DeviceCapability Name="microphone" />`,
		`EntryPoint="Windows.FullTrustApplication"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the manifest does not declare %s", want)
		}
	}
}
