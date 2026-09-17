//go:build windows

package i18n

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                        = windows.NewLazySystemDLL("kernel32.dll")
	procGetUserPreferredUILanguages = kernel32.NewProc("GetUserPreferredUILanguages")
)

const mUILanguageName = 0x8 // tags like "it-IT", rather than numeric identifiers

// FromSystem is the interface language list of whoever uses this computer, in
// order of preference.
//
// **It is the declaration that speaks for whoever is in front of the machine**,
// as Accept-Language is the declaration of whoever is watching from a phone.
// The tray and the notifications are read by them, so this is their language —
// two audiences, and they may want two different ones.
//
// The list is asked for rather than GetUserDefaultUILanguage, which returns one
// only: **Windows keeps an order of preference** just as a browser does, and
// throwing it away would mean handing English to someone who put German second
// and a language we do not have first.
//
// **A failure here is not a fault**: with no answer an empty list comes back
// and the fallback language takes over, which is exactly what
// GetUserPreferredUILanguages would have given an English user.
func FromSystem() []string {
	var count, chars uint32

	// The first call is there to learn how much room is needed: a nil buffer
	// goes in and the length comes back. Asking for a fixed size would mean
	// deciding how many languages a person is entitled to have.
	r, _, _ := procGetUserPreferredUILanguages.Call(
		mUILanguageName,
		uintptr(unsafe.Pointer(&count)),
		0,
		uintptr(unsafe.Pointer(&chars)),
	)
	if r == 0 || chars == 0 {
		return nil
	}

	buf := make([]uint16, chars)
	r, _, _ = procGetUserPreferredUILanguages.Call(
		mUILanguageName,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&chars)),
	)
	if r == 0 {
		return nil
	}

	return splitMultiSZ(buf)
}

// splitMultiSZ reads a MULTI_SZ: NUL-terminated strings one after another, with
// one more NUL to close.
//
// **UTF16ToString is no use here**, and that is the mistake not to make: it
// stops at the first NUL, so it would return the first language and nothing
// else — with the worst possible symptom, which is no symptom, because a list
// of one language is perfectly plausible. Anyone with a single language would
// never notice.
func splitMultiSZ(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i > start {
			s := strings.TrimSpace(windows.UTF16ToString(buf[start:i]))
			if s != "" {
				out = append(out, s)
			}
		}
		start = i + 1
	}
	return out
}
