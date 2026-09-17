package encoder

import (
	"testing"

	"patmonitor/internal/media"
)

// browserLevel is the level browsers negotiate for constrained baseline —
// 42e01f — measured on Safari iOS 18.7 and Chrome 148. It is a fact about
// somebody else's software, which is why it is written here and imposed
// nowhere: nothing in the program refuses a stream over it.
const browserLevel = 0x1f

// **Every preset must fit inside the level browsers negotiate.**
//
// This used to be a sentence in a comment, beside a field that nothing read.
// That is the worst place for a claim, because a dead field cannot break and a
// comment cannot fail. What it asserts is real: the level announced in the SDP
// is computed from the preset (rtc.Config.LevelIDC), so a preset needing more
// than 3.1 would announce more than 3.1 — and whether the target browsers
// accept that has never been measured. Anything above 720p30 is that case: it
// needs level 4.0.
func TestEveryPresetFitsTheLevelBrowsersNegotiate(t *testing.T) {
	// **The positive control comes first**: with a MinLevelIDC answering zero,
	// or reached with the wrong arguments, every assertion below is satisfied
	// without asking anything.
	if got := media.MinLevelIDC(1280, 720, 30, 2500); got != browserLevel {
		t.Fatalf("720p30 at 2500 kbit/s works out to level_idc 0x%02x, and this "+
			"test was written believing 0x%02x: one of the two is wrong", got, browserLevel)
	}

	for _, p := range Presets {
		got := media.MinLevelIDC(p.Width, p.Height, p.FPS, p.BitrateKbps)
		if got > browserLevel {
			t.Errorf("the preset %q needs level_idc 0x%02x, above the 0x%02x browsers "+
				"negotiate: the SDP would announce it, and that they accept it is "+
				"something nobody has measured", p.Name, got, browserLevel)
		}
	}
}
