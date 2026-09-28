//go:build windows

package i18n

import (
	"strings"

	"golang.org/x/sys/windows"
)

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
//
// The answer is a MULTI_SZ, and x/sys splits it. **UTF16ToString is wrong on
// that buffer**, which is why the splitting is not written here: it stops at
// the first NUL and returns the first language only — a list of one is
// perfectly plausible, so the mistake would have no symptom.
func FromSystem() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	out := langs[:0]
	for _, l := range langs {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
