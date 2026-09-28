package media

import (
	"encoding/hex"
	"testing"
	"time"
)

// The SPSs in this test are not invented: they are the ones this machine's
// Quick Sync encoder produced during a pat-capture run with the resolution
// scale, captured one before and one after the size change.
//
// The difference matters. An SPS written by hand would prove that the bit
// reader can read back what its own author wrote, which is not the question:
// the question is whether it can read what a real encoder produces, with its
// optional fields and its anti-emulation bytes.
func TestSPSSize(t *testing.T) {
	cases := []struct {
		name string
		hex  string
		w, h int
	}{
		{
			name: "720p",
			hex:  "2742e01f95b014016ec044000003000400000300f3a100098900002625a6f7be0ed0e197",
			w:    1280, h: 720,
		},
		{
			// Produced after the scale came down, in the same session.
			name: "540p",
			hex:  "2742e01f95b03c045fbc0440000003004000000f3a100098900002625a6f7be0ed0e1970",
			w:    960, h: 540,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sps, err := hex.DecodeString(c.hex)
			if err != nil {
				t.Fatalf("unreadable test SPS: %v", err)
			}
			w, h, err := SPSSize(sps)
			if err != nil {
				t.Fatalf("SPSSize: %v", err)
			}
			if w != c.w || h != c.h {
				t.Errorf("read %dx%d, wanted %dx%d", w, h, c.w, c.h)
			}
		})
	}
}

// A truncated SPS must not produce a plausible size: a bit reader that runs out
// of data and returns zeros would give numbers that look real.
func TestATruncatedSPSIsRefused(t *testing.T) {
	sps, err := hex.DecodeString("2742e01f95b014016ec044")
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n < len(sps); n++ {
		if w, h, err := SPSSize(sps[:n]); err == nil && (w <= 0 || h <= 0) {
			t.Errorf("with %d bytes it answered %dx%d without an error", n, w, h)
		}
	}
}

// **A picture-order cycle of four billion entries is not read four billion
// times.** The loop over num_ref_frames_in_pic_order_cnt_cycle stopped only on
// its counter, so once the reader ran dry it spun on reads that answered zero at
// once: seconds of a stopped frame loop for one malformed SPS, the same hang
// bits() was already cured of.
//
// **The defect was put back and this test fails with it**, on the deadline.
func TestAnEndlessPictureOrderCycleIsNotWalked(t *testing.T) {
	w := &bitWriter{}
	w.bits(66, 8)
	w.bits(0xE01F, 16)
	w.ue(0) // seq_parameter_set_id
	w.ue(0) // log2_max_frame_num_minus4
	w.ue(1) // pic_order_cnt_type 1
	w.bit(0)
	w.se(0)
	w.se(0)
	w.ue(4_000_000_000) // num_ref_frames_in_pic_order_cnt_cycle, and then nothing
	sps := append([]byte{0x67}, w.b...)

	done := make(chan error, 1)
	go func() {
		_, _, err := SPSSize(sps)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("an SPS that ends inside the cycle was read as a size")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SPSSize was still walking the cycle after two seconds")
	}
}
