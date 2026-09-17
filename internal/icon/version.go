package icon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// The resource Explorer reads in the "Details" tab.
//
// **It exists for the same reason as the version package**, one step further
// on: "it does not work" is not a report until you know which binary it came
// from, and whoever has the executable in hand but is not running it — a file
// downloaded months ago, a copy on a memory stick, an attachment — has nowhere
// else to look. The number is in the log and in the tray **while it runs**;
// here it is in the file even at rest.
//
// The beaten path is goversioninfo, that is, a third-party binary in the build
// chain. The same question already asked for the icon, and the same answer: the
// format is small, settled since the nineties and documented, so we write it.
// It passes right by — it is another resource in the same tree — and reusing
// that costs less than adding a dependency for thirty fields.

// rtVersion is the resource type. It sits **after** RT_GROUP_ICON in the tree
// because entries go in increasing order of identifier: 3, 14, 16.
const rtVersion = 16

// versionID: the version resource is always number 1. That is not a convention
// of ours, it is the one GetFileVersionInfo looks for.
const versionID = 1

// The VS_FIXEDFILEINFO flags we use, with the sense they carry.
const (
	ffMask = 0x3F
	// VS_FF_PRERELEASE: the product is not a finished version. It is decided by
	// **the number's label** and not by its digits — see the Prerelease field,
	// and version.Prerelease which fills it.
	ffPrerelease = 0x02
	// VS_FF_PRIVATEBUILD: "not produced by the usual release process". It is
	// the field made for the case version.Modified describes — built with
	// uncommitted changes — and using it rather than inventing a convention
	// leaves the meaning intact for whoever reads the file with other tools.
	ffPrivateBuild = 0x08
)

// VersionInfo is what ends up in the file's properties.
type VersionInfo struct {
	// Major, Minor, Patch come from version.Number; Build is the commit count,
	// that is, the r.
	//
	// **The build number does not go in the third digit, and the question is
	// fair but the answer is no.** The format has four fields on purpose: the
	// first three say what the product is — and the third, by semver, is the
	// fixes inside the same milestone — the fourth says **which build**. Mixing
	// the two would jump the patch from 0 to 126 with nothing having happened
	// to the product, and would take away the place a real fix goes. With four
	// fields there is nothing to choose.
	Major, Minor, Patch, Build uint16

	Product      string // "PAT Monitor"
	Description  string // what it does, for whoever reads the file's details
	Version      string // spelled out: `1.0.0-beta.1 r126 (b5fae50)`
	Copyright    string
	FileName     string // the name it is distributed under
	InternalName string
	// Prerelease says this is not a final version, and turns on
	// VS_FF_PRERELEASE.
	//
	// **It is a field and not something deduced from Major.** The earlier rule
	// was "on while the major is zero", which at 1.0.0-beta.1 switches itself
	// off: the executable would declare itself finished exactly when the number
	// says the opposite, and nobody goes looking for a defect in the details of
	// an .exe. What fills it is version.Prerelease, which looks at what the
	// number **asserts** rather than at a consequence of it.
	Prerelease bool
	Modified   bool // built with uncommitted changes
}

// Blob builds VS_VERSIONINFO.
//
// The declared language is en-US with the Unicode code page. **That is not an
// oversight against the multilingual interface**: this sheet is not read by
// whoever uses the monitor, it is read by whoever redistributes it and by the
// tools that scan executables — the same audience as LICENSE and NOTICE, and
// the same language.
func (v VersionInfo) Blob() ([]byte, error) {
	if v.Product == "" || v.Version == "" {
		return nil, fmt.Errorf("icon: version resource without a name or a number")
	}

	flag := uint32(0)
	if v.Prerelease {
		flag |= ffPrerelease
	}
	if v.Modified {
		flag |= ffPrivateBuild
	}

	var fixed bytes.Buffer
	put := func(x uint32) { _ = binary.Write(&fixed, binary.LittleEndian, x) }
	put(0xFEEF04BD) // dwSignature
	put(0x00010000) // dwStrucVersion
	put(uint32(v.Major)<<16 | uint32(v.Minor))
	put(uint32(v.Patch)<<16 | uint32(v.Build))
	put(uint32(v.Major)<<16 | uint32(v.Minor))
	put(uint32(v.Patch)<<16 | uint32(v.Build))
	put(ffMask)
	put(flag)
	put(0x00040004) // VOS_NT_WINDOWS32
	put(0x00000001) // VFT_APP
	put(0)          // no subtype
	put(0)          // date, unused: it is zero in Microsoft's binaries too
	put(0)

	// The strings, in the order Explorer shows them. Empty entries are not
	// written: a field present and empty is worse than a field absent, because
	// it takes up a row of the sheet to say nothing.
	pairs := [][2]string{
		{"ProductName", v.Product},
		{"FileDescription", v.Description},
		{"FileVersion", v.Version},
		{"ProductVersion", v.Version},
		{"LegalCopyright", v.Copyright},
		{"OriginalFilename", v.FileName},
		{"InternalName", v.InternalName},
	}
	if v.Modified {
		pairs = append(pairs,
			[2]string{"PrivateBuild", "built from a working tree with uncommitted changes"})
	}
	var texts [][]byte
	for _, c := range pairs {
		if c[1] == "" {
			continue
		}
		texts = append(texts, node(c[0], utf16Text(c[1]), true, nil))
	}

	table := node("040904B0", nil, true, texts)
	sfi := node("StringFileInfo", nil, true, [][]byte{table})

	// The translation is binary: language and code page in one DWORD, and the
	// two numbers have to be the same as the key of the table above — that is
	// how whoever reads finds the right block.
	translation := make([]byte, 4)
	binary.LittleEndian.PutUint16(translation[0:], 0x0409)
	binary.LittleEndian.PutUint16(translation[2:], 0x04B0)
	vfi := node("VarFileInfo", nil, true, [][]byte{node("Translation", translation, false, nil)})

	return node("VS_VERSION_INFO", fixed.Bytes(), false, [][]byte{sfi, vfi}), nil
}

// node builds one of the format's nested structures.
//
// They are all alike: length, value length, type, key, and then the value and
// the children, each aligned to four bytes. **All three traps are in the
// length**, and none of the three gives an error — whoever reads takes bytes at
// random and the sheet comes out empty or crooked:
//
//   - wValueLength for text counts **characters**, not bytes, and it counts the
//     terminator too. For a binary value it counts bytes.
//   - wLength includes the **internal** padding — the one between key and
//     value, and the one between one child and the next — but not the one after
//     the last child: that is added by whoever embeds it.
//   - the children start on a multiple of four, and the key is of variable
//     length, so the padding is not optional.
func node(key string, value []byte, isText bool, children [][]byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 0, 0, 0}) // wLength, wValueLength, wType: later
	b.Write(utf16Text(key))
	pad(&b)
	b.Write(value)

	// The padding goes **between the value and the first child** as well, not
	// only between one child and the next: the value has a free length, and
	// that one is easy to skip. Where the value is already aligned it adds
	// nothing, which is why the mistake would go unnoticed until somebody put a
	// value of odd length there.
	for _, f := range children {
		pad(&b)
		b.Write(f)
	}

	out := b.Bytes()
	valLen := len(value)
	if isText {
		valLen /= 2 // characters, terminator included
	}
	binary.LittleEndian.PutUint16(out[0:], uint16(len(out)))
	binary.LittleEndian.PutUint16(out[2:], uint16(valLen))
	if isText {
		binary.LittleEndian.PutUint16(out[4:], 1)
	}
	return out
}

// utf16Text is the string as the format wants it: UTF-16 little-endian with the
// terminator.
func utf16Text(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2*len(u)+2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return append(b, 0, 0)
}
