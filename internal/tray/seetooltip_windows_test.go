//go:build windows

package tray

import (
	"os"
	"testing"

	"patmonitor/internal/i18n"
)

// Prints the real tooltip while the check is under way, so it can be read
// rather than deduced from the catalogue. It does not run in a normal
// `go test`.
//
//	PATMON_LOOK=1 go test ./internal/tray/ -run TestSeeTheVerifyingTooltip -v
func TestSeeTheVerifyingTooltip(t *testing.T) {
	if os.Getenv("PATMON_LOOK") == "" {
		t.Skip("PATMON_LOOK not set")
	}
	st := Status{
		Phase:     PhaseOutside,
		Note:      NoteVerifying,
		Viewers:   0,
		Devices:   2,
		PublicURL: "https://patmon-1.quercia-lieve.ts.net",
		Uptime:    "3m",
	}
	for _, lang := range []string{"it", "en"} {
		tr := &Tray{dictionary: i18n.Open([]string{lang})}
		tip := tr.tooltip(st)
		t.Logf("[%s] %d characters\n%s", lang, len([]rune(tip)), tip)
	}
}
