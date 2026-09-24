package icon

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// The resource the linker embeds in the executable.
//
// A .syso is a COFF object file the Go linker picks up by itself from the main
// package's folder, without anybody having to tell it. Inside is a .rsrc
// section with the Windows resource tree, made of three fixed levels — type,
// identifier, language — and at the leaves the coordinates of the data.
//
// **The leaves hold an RVA, and in an object file an RVA is not yet known.**
// The final address is decided by the linker when it chooses where to put the
// section, so every leaf carries a relocation: "at this offset there is an
// address relative to the base, add where you put .rsrc". Forgetting them
// produces an object that links perfectly and an executable whose icon points
// at the start of the file: Windows does not protest, it shows the default
// icon, and there is nothing to read that explains it.
const (
	rtIcon      = 3
	rtGroupIcon = 14

	// The group's identifier. **It has to be the lowest in the binary**: the
	// icon Windows shows for an executable is the one of the group with the
	// smaller identifier, not the first one it finds.
	groupID = 1

	// The language. Neutral: the icon holds no text, and declaring it Italian
	// would mean not having it found on an English system.
	neutralLanguage = 0

	machineAMD64 = 0x8664
	machineARM64 = 0xAA64

	relAMD64ADDR32NB = 0x0003
	relARM64ADDR32NB = 0x0002

	scnReadData  = 0x40000040 // CNT_INITIALIZED_DATA | MEM_READ
	staticSymbol = 3          // IMAGE_SYM_CLASS_STATIC
)

// Syso builds the COFF object for the given architecture ("amd64" or "arm64").
//
// With ver not nil it adds the version resource, the one Explorer shows in the
// "Details" tab. It lives in the same tree as the icons because an executable
// has **one** resource section: two .syso files in the same folder do not add
// up, and there is nowhere the conflict shows.
func Syso(im []Image, ver *VersionInfo, goarch string) ([]byte, error) {
	var machine, reloc uint16
	switch goarch {
	case "amd64":
		machine, reloc = machineAMD64, relAMD64ADDR32NB
	case "arm64":
		machine, reloc = machineARM64, relARM64ADDR32NB
	default:
		return nil, fmt.Errorf("icon: unsupported architecture: %s", goarch)
	}
	if len(im) == 0 {
		return nil, fmt.Errorf("icon: no image")
	}

	var verBlob []byte
	if ver != nil {
		var err error
		if verBlob, err = ver.Blob(); err != nil {
			return nil, err
		}
	}

	section, toRelocate := resourceSection(im, verBlob)

	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }

	const header = 20 + 40
	relocAt := header + len(section)
	symbolsAt := relocAt + 10*len(toRelocate)

	// IMAGE_FILE_HEADER. The date is zero on purpose: two builds from the same
	// drawing have to give the same bytes, otherwise the binary is not
	// comparable.
	put(machine)
	put(uint16(1))
	put(uint32(0))
	put(uint32(symbolsAt))
	put(uint32(1))
	put(uint16(0))
	put(uint16(0))

	// IMAGE_SECTION_HEADER
	b.Write([]byte(".rsrc\x00\x00\x00"))
	put(uint32(0)) // VirtualSize
	put(uint32(0)) // VirtualAddress
	put(uint32(len(section)))
	put(uint32(header))
	put(uint32(relocAt))
	put(uint32(0)) // line numbers
	put(uint16(len(toRelocate)))
	put(uint16(0))
	put(uint32(scnReadData))

	b.Write(section)

	for _, off := range toRelocate {
		put(uint32(off)) // where the field to fix up sits
		put(uint32(0))   // relative to symbol 0, that is, the section itself
		put(reloc)
	}

	// The symbol table: one only, the section, which is the reference every
	// relocation leans on.
	b.Write([]byte(".rsrc\x00\x00\x00"))
	put(uint32(0)) // value
	put(int16(1))  // section number, one-based
	put(uint16(0)) // type
	put(uint8(staticSymbol))
	put(uint8(0)) // no auxiliary symbol

	// The string table is empty, but its four bytes of length are not: whoever
	// reads the object looks for it straight after the symbols, and without it
	// they end up interpreting the end of the file.
	put(uint32(4))

	return b.Bytes(), nil
}

// The tree's three structures, all of fixed size.
const (
	dirSize   = 16
	entrySize = 8
	leafSize  = 16
)

// resourceEntry is a resource to put in the tree: a type, an identifier, some
// bytes.
type resourceEntry struct {
	kind, id uint32
	data     []byte
}

// resourceSection builds the tree and returns, alongside the bytes, the offsets
// of the fields that need relocating.
//
// The tree was written for two types only, with the offsets worked out by hand
// one after another. With the version resource the types become three, and the
// short road — a third block of arithmetic beside the other two — is the one
// where a wrong offset does not protest: the tree stays well formed and points
// elsewhere. So it is built from a list, and the sums are done once for all.
func resourceSection(im []Image, version []byte) ([]byte, []int) {
	var entries []resourceEntry
	for k, i := range im {
		entries = append(entries, resourceEntry{rtIcon, uint32(k + 1), i.Data})
	}
	entries = append(entries, resourceEntry{rtGroupIcon, groupID, group(im)})
	if len(version) > 0 {
		entries = append(entries, resourceEntry{rtVersion, versionID, version})
	}

	// **The entries have to be sorted**, the named ones by name and the
	// numbered ones by increasing number: whoever reads does a binary search,
	// and on an unsorted tree it answers "not there" about a resource that is.
	// Here the order comes for free — 3, 14, 16, and the identifiers assigned
	// in sequence — but it is the property everything else rests on, so the
	// types are collected by walking the entries in the order they were built
	// rather than trusting a map, which Go iterates at random.
	var kinds []uint32
	byKind := map[uint32][]resourceEntry{}
	for _, v := range entries {
		if _, already := byKind[v.kind]; !already {
			kinds = append(kinds, v.kind)
		}
		byKind[v.kind] = append(byKind[v.kind], v)
	}

	n := len(entries)
	// Level 1, then one directory per type, then one language directory per
	// entry, then the leaves: the sizes are all fixed, so they are counted
	// before anything is written.
	kindOff := make([]int, len(kinds))
	after := dirSize + len(kinds)*entrySize
	for k, t := range kinds {
		kindOff[k] = after
		after += dirSize + len(byKind[t])*entrySize
	}
	langOff := after
	leavesAt := langOff + n*(dirSize+entrySize)
	dataAt := align(leavesAt + n*leafSize)

	var data bytes.Buffer
	where := make([]int, 0, n)
	howMuch := make([]int, 0, n)
	for _, t := range kinds {
		for _, v := range byKind[t] {
			where = append(where, dataAt+data.Len())
			howMuch = append(howMuch, len(v.data))
			data.Write(v.data)
			pad(&data)
		}
	}

	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	writeDir := func(nEntries int) {
		put(uint32(0)) // characteristics
		put(uint32(0)) // date, zero for the same reason as above
		put(uint16(0)) // major version
		put(uint16(0)) // minor version
		put(uint16(0)) // no named entries
		put(uint16(nEntries))
	}
	writeEntry := func(id uint32, off int, subtree bool) {
		put(id)
		v := uint32(off)
		if subtree {
			v |= 0x80000000
		}
		put(v)
	}

	// Level 1: the types.
	writeDir(len(kinds))
	for k, t := range kinds {
		writeEntry(t, kindOff[k], true)
	}

	// Level 2: the identifiers, in the order the entries were collected — which
	// is also the order of the language directories below.
	j := 0
	for _, t := range kinds {
		writeDir(len(byKind[t]))
		for _, v := range byKind[t] {
			writeEntry(v.id, langOff+j*(dirSize+entrySize), true)
			j++
		}
	}

	// Level 3: the language, and under it the leaf.
	for k := range n {
		writeDir(1)
		writeEntry(neutralLanguage, leavesAt+k*leafSize, false)
	}

	// The leaves. The offset of the field that opens them is the one to
	// relocate.
	toRelocate := make([]int, 0, n)
	for k := range n {
		toRelocate = append(toRelocate, b.Len())
		put(uint32(where[k]))
		put(uint32(howMuch[k]))
		put(uint32(0)) // code page
		put(uint32(0)) // reserved
	}
	for b.Len() < dataAt {
		b.WriteByte(0)
	}
	b.Write(data.Bytes())

	return b.Bytes(), toRelocate
}

// group is the GRPICONDIR: the list that tells Windows which sizes exist and
// under which identifier to ask for them. This is the resource the executable
// exposes as "its icon"; the others are the images it points at.
func group(im []Image) []byte {
	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	put(uint16(0))
	put(uint16(1))
	put(uint16(len(im)))
	for k, i := range im {
		b.Write(dirEntry(i))
		put(uint32(len(i.Data)))
		put(uint16(k + 1)) // the identifier of the matching RT_ICON
	}
	return b.Bytes()
}

func align(n int) int { return (n + 3) &^ 3 }

func pad(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}
