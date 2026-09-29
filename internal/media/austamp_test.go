package media

import (
	"testing"
	"time"
)

// Two slices that each open a picture: the first bit of the header is
// first_mb_in_slice = 0, which is what makes the assembler close the one
// before.
var (
	idrSlice   = []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x21, 0x43}
	interSlice = []byte{0, 0, 0, 1, 0x41, 0x9a, 0x22, 0x11, 0x07}
)

// **An access unit leaves one Write late, and it must leave with its own
// instant.** The assembler closes a frame when the next one begins, so frame n
// comes out of the call that delivered frame n+1: stamping it with that call's
// instant would date every frame one frame late — 33 ms at 30 fps, on top of
// the encoder's delay the stamp exists to remove.
//
// Put back — `At: at` of the closing call instead of a.pendingAt — every frame
// here reads the instant of its successor and the test fails on each one.
func TestAnAccessUnitCarriesTheInstantOfItsOwnFrame(t *testing.T) {
	var a AUAssembler
	t0 := time.Unix(1000, 0)
	step := 33 * time.Millisecond

	var got []AccessUnit
	for i := range 6 {
		chunk := interSlice
		if i == 0 {
			chunk = idrSlice
		}
		got = append(got, a.Write(chunk, t0.Add(time.Duration(i)*step))...)
	}
	if len(got) != 5 {
		t.Fatalf("%d access units out of six frames, want five: the last one is still pending", len(got))
	}
	for i, au := range got {
		if want := t0.Add(time.Duration(i) * step); !au.At.Equal(want) {
			t.Errorf("frame %d dated %v after the first, want %v", i, au.At.Sub(t0), want.Sub(t0))
		}
	}
	if !got[0].Keyframe {
		t.Error("the first frame is not a keyframe: the chunks are not what the test thinks")
	}
}

// **A frame that arrives in two pieces is dated by the first.** The piece
// still waiting in the buffer came in with an earlier call than the one that
// completes it, and it is that earlier instant that says when the frame began.
func TestAFrameSplitAcrossWritesKeepsItsFirstInstant(t *testing.T) {
	var a AUAssembler
	t0 := time.Unix(1000, 0)

	// Cut inside the slice, past its header: the first piece already says a
	// picture begins, the second completes it.
	const cut = 7
	var out []AccessUnit
	out = append(out, a.Write(idrSlice, t0)...)
	out = append(out, a.Write(interSlice[:cut], t0.Add(33*time.Millisecond))...)
	out = append(out, a.Write(interSlice[cut:], t0.Add(40*time.Millisecond))...)
	out = append(out, a.Write(idrSlice, t0.Add(66*time.Millisecond))...)

	if len(out) != 2 {
		t.Fatalf("%d access units, want the keyframe and the split frame", len(out))
	}
	if got := out[0].At.Sub(t0); got != 0 {
		t.Errorf("the keyframe is dated %v after itself", got)
	}
	if got := out[1].At.Sub(t0); got != 33*time.Millisecond {
		t.Errorf("the split frame is dated %v after the first, want 33ms: the piece that began it", got)
	}
}
