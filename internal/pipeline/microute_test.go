package pipeline

import (
	"errors"
	"testing"
)

// **The clean signal by the other road is still news, and for a while it was
// not written anywhere.**
//
// Raw mode and exclusive mode both deliver the unprocessed signal, so when raw
// is refused and exclusive answers, nothing is wrong: no warning fires, and the
// only line the log carries is "microphone opened … mode=exclusive". The reason
// for the refusal travels in Stream.RawError and its one reader was guarded by
// `!s.RawMode` — which the exclusive road makes false. When an endpoint begins
// refusing raw, finding out that the code is AUDCLNT_E_RAW_MODE_UNSUPPORTED
// takes a program written for the purpose, because it reaches no file.
//
// The predicate is tested rather than the log line, for the reason the shape
// tests beside this one give: runAudio wants WASAPI. **The defect was put back
// to see this fail** — with the old `!rawMode && rawErr != nil` the second case
// below answers false, which is the whole of the fault.
func TestTheCleanSignalByTheOtherRoadIsAnnounced(t *testing.T) {
	refused := errors.New("AUDCLNT_E_RAW_MODE_UNSUPPORTED (0x88890027)")

	for _, c := range []struct {
		name    string
		rawMode bool
		rawErr  error
		want    bool
	}{
		// Raw granted: there is nothing to explain, and a line here would be
		// written at every open for the life of every installation.
		{"raw granted", true, nil, false},
		// Raw refused, exclusive granted: the signal is clean and the road is
		// not the usual one. This is the case that was silent.
		{"exclusive after a refusal", true, refused, true},
		// Raw refused and exclusive too: the audio really does go through the
		// OEM effects, and that is the warning that already existed. It must
		// not be answered by this branch as well, or the same open writes the
		// degradation and a line saying the signal is unprocessed.
		{"no road to the clean signal", false, refused, false},
	} {
		if got := micCleanByOtherRoad(c.rawMode, c.rawErr); got != c.want {
			t.Errorf("%s: micCleanByOtherRoad(%v, %v) = %v, want %v",
				c.name, c.rawMode, c.rawErr, got, c.want)
		}
	}
}
