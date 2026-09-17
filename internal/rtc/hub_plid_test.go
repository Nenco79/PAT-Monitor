package rtc

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// fakeSPS is just enough of an SPS: ProfileLevelID reads the three bytes after
// the NAL header and does not look at the rest. The tail is only there so it is
// not mistaken for a truncated one.
func fakeSPS(profile, constraints, level byte) []byte {
	return []byte{0x67, profile, constraints, level, 0x00, 0x00}
}

// The level announced in the SDP freezes on the first one, and does not chase
// the stream.
//
// The sequence is the one read on wifi: the scale comes down to 640x352@6, the
// rebuilt encoder emits an SPS with level 2.2, and climbing back it returns to
// 3.1. In between, a viewer connected.
//
// **A stream below the announced level is not a problem**: the field says how
// much capacity to allocate, and allocating too much breaks nothing. The fault
// is that NewViewer reads this value at the moment of the offer, so whoever
// comes in during the low window negotiates a tight contract and then receives a
// stream that exceeds it. It has really happened, and Chrome decoded it anyway —
// but "this decoder forgives" is something discovered on somebody else's phone,
// not a guarantee.
func TestTheAnnouncedLevelDoesNotChaseTheStream(t *testing.T) {
	h := New(Config{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	h.setSPS(fakeSPS(0x42, 0xe0, 0x1f)) // full 720p, level 3.1
	if got := h.ProfileLevelID(); got != "42e01f" {
		t.Fatalf("at start-up %q is announced instead of 42e01f", got)
	}

	// The bandwidth drops, the scale comes down, the encoder emits a lower level.
	h.setSPS(fakeSPS(0x42, 0xe0, 0x16)) // level 2.2
	if got := h.ProfileLevelID(); got != "42e01f" {
		t.Errorf("the stream came down to 2.2 and the SDP announces %q: whoever "+
			"connects now negotiates a contract that in a minute will be too tight", got)
	}

	// And climbing back does not change it either: it is frozen, not chased.
	h.setSPS(fakeSPS(0x42, 0xe0, 0x1f))
	if got := h.ProfileLevelID(); got != "42e01f" {
		t.Errorf("climbing back, %q is announced instead of 42e01f", got)
	}
}

// **The alarm fires only when the announced level really changes.**
//
// Two guards stand between an SPS and this warning, and they mask each other:
// setSPS returns early when the bytes are identical, so a test that feeds the
// same bytes twice never reaches the comparison on the id, and would stay green
// with either guard removed. What is fed here are two different SPSs carrying
// the same profile-level-id — the byte guard lets them past, and only the
// comparison on the id keeps the log quiet.
//
// The log is what is looked at and not the private field: the alarm is what
// somebody will read at three in the morning, and that is what has to stay
// quiet. **The phrase searched for is the one setSPS writes** — with any other,
// this test passes without ever being able to fail.
func TestOnlyARealChangeOfLevelIsAnnounced(t *testing.T) {
	warned := func(feed func(*Hub)) string {
		var lines strings.Builder
		h := New(Config{Log: slog.New(slog.NewTextHandler(&lines, nil))})
		feed(h)
		if !strings.Contains(lines.String(), "profile-level-id changed") {
			return ""
		}
		return lines.String()
	}

	// Same level, different bytes: nothing to announce.
	if out := warned(func(h *Hub) {
		h.setSPS([]byte{0x67, 0x42, 0xe0, 0x1f, 0x00, 0x00})
		h.setSPS([]byte{0x67, 0x42, 0xe0, 0x1f, 0x11, 0x22})
	}); out != "" {
		t.Errorf("alarm over an SPS carrying the same level:\n%s", out)
	}

	// **The positive control, without which every assertion above is satisfied
	// by an alarm that never fires at all.**
	if out := warned(func(h *Hub) {
		h.setSPS(fakeSPS(0x42, 0xe0, 0x1f))
		h.setSPS(fakeSPS(0x42, 0xe0, 0x16))
	}); out == "" {
		t.Error("the level went from 3.1 to 2.2 and nothing was written in the log")
	}
}

// quiet is a hub that writes its log nowhere: these tests read the announced
// value, not the warnings.
func quiet(cfg Config) *Hub {
	cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg)
}

// **The announced level is the preset's, and the capture's is not consulted.**
//
// The case is a webcam with fewer pixels than the preset: PickCameraSize lowers
// the capture before anything is built, the encoder emits level 2.2, and
// announcing that would promise less capacity than the session can end up
// needing — a promise that cannot be taken back, because NewViewer reads it at
// the moment of the offer.
//
// It was measured before being written: with 3.1 announced and 640x360 really
// on the wire, Edge decoded 151 frames and dropped none.
func TestTheAnnouncedLevelIsThePresetsAndNotTheCaptures(t *testing.T) {
	h := quiet(Config{LevelIDC: 0x1f})

	h.setSPS(fakeSPS(0x42, 0xe0, 0x16)) // the camera is smaller than the preset
	if got := h.ProfileLevelID(); got != "42e01f" {
		t.Errorf("the capture is at 2.2 and the SDP announces %q: the preset's "+
			"3.1 is the only bound the whole session respects", got)
	}
}

// Only the level comes from the preset. The profile and the constraint flags
// describe the **encoder**, not the size, and no preset can predict them: read
// from the stream, they are the same three bytes the browser has to agree with.
func TestTheProfileAndFlagsStillComeFromTheStream(t *testing.T) {
	h := quiet(Config{LevelIDC: 0x1f})

	h.setSPS(fakeSPS(0x4d, 0x40, 0x16))
	if got := h.ProfileLevelID(); got != "4d401f" {
		t.Errorf("announced %q: the profile and the flags are the stream's, only "+
			"the level is the preset's", got)
	}
}

// **The belt, and it should never bite.** Nothing raises the size or the bitrate
// above the preset, so a stream declaring more than the announcement means that
// construction is broken somewhere. It is worth three lines because the two
// directions do not cost the same: below the announcement is free, above it is
// what stops a decoder.
func TestAStreamAboveTheAnnouncementIsNotUnderstated(t *testing.T) {
	h := quiet(Config{LevelIDC: 0x16})

	h.setSPS(fakeSPS(0x42, 0xe0, 0x1f))
	if got := h.ProfileLevelID(); got != "42e01f" {
		t.Errorf("the stream is at 3.1 and %q is announced: understating the "+
			"level is the one direction that stops a decoder", got)
	}
}

// With no level configured nothing changes: the stream's own value is
// announced, which is what a Hub built without this field gets — every test
// above this one in the file, and any caller that has not been taught yet.
func TestWithNoPresetLevelTheStreamStillDecides(t *testing.T) {
	h := quiet(Config{})

	h.setSPS(fakeSPS(0x42, 0xe0, 0x16))
	if got := h.ProfileLevelID(); got != "42e016" {
		t.Errorf("announced %q with no level configured, instead of the stream's", got)
	}
}
