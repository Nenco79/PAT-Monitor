package rtc

import (
	"testing"
	"time"
)

// TestRequestKeyframeCoalesce checks that PLIs close together become a single
// request to the encoder, and that the first is served at once.
//
// That is the part that matters: the first request is the one that repairs the
// frozen picture, the later ones inside the interval ask for a keyframe that is
// already on its way. The function takes the instant as a parameter precisely so
// this can be checked without waiting.
func TestRequestKeyframeCoalesce(t *testing.T) {
	var forced int
	h := New(Config{OnKeyframeRequest: func() { forced++ }})

	t0 := time.Date(2026, 8, 10, 22, 0, 0, 0, time.UTC)

	if !h.requestKeyframe(t0) {
		t.Fatal("the first request must be forwarded at once")
	}
	// Three viewers losing packets together, as is typical when the bandwidth
	// drops: one keyframe repairs them all.
	for i, dt := range []time.Duration{time.Millisecond, 100 * time.Millisecond, keyframeMinGap - time.Millisecond} {
		if h.requestKeyframe(t0.Add(dt)) {
			t.Errorf("request %d at +%v forwarded: it was meant to be absorbed", i, dt)
		}
	}
	if forced != 1 {
		t.Errorf("keyframes forced = %d, want 1", forced)
	}

	// Once the interval has passed, a new loss has to be served: it is a new
	// fault, not the echo of the previous one.
	if !h.requestKeyframe(t0.Add(keyframeMinGap)) {
		t.Error("request after the minimum interval not forwarded")
	}
	if forced != 2 {
		t.Errorf("keyframes forced = %d, want 2", forced)
	}
	if got := h.Stats.KeyframeForced.Load(); got != 2 {
		t.Errorf("Stats.KeyframeForced = %d, want 2", got)
	}
}

// TestRequestKeyframeWithoutAnEncoder checks that a hub with no callback does
// not claim to have forced anything: the counter serves diagnosis, and a count
// that grows while the encoder receives nothing would say the false.
func TestRequestKeyframeWithoutAnEncoder(t *testing.T) {
	h := New(Config{})
	if h.requestKeyframe(time.Now()) {
		t.Error("with no OnKeyframeRequest the request cannot be forwarded")
	}
	if got := h.Stats.KeyframeForced.Load(); got != 0 {
		t.Errorf("Stats.KeyframeForced = %d, want 0", got)
	}
}
