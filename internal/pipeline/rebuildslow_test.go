package pipeline

import (
	"testing"
	"time"
)

// **The threshold weighs what the picture waited, not what one encoder cost.**
//
// A rebuild can make two attempts: the first with the new cadence, and, if the
// encoder refuses it, a second with the old one. No frame is encoded for either,
// nor while the old encoder is closed — so the stall is the sum. Weighing only
// the build of the encoder that was kept, a refusal of 1.8 s followed by a build
// of 1.0 and a close of 0.1 came to 1.1 and stayed under `slowRebuild`: a stall
// of nearly three seconds with the log silent, which is the one thing the timing
// was added for.
//
// **The defect was put back to watch this fail**: with `built+closed` in place
// of the sum, the first case answers false.
func TestARefusedCadenceCountsTowardsTheStall(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		name                   string
		refused, built, closed time.Duration
		want                   bool
	}{
		// The measured case: turned down, rebuilt, closed. Only the sum sees it.
		{"a refusal and a build", 1800 * ms, 1000 * ms, 100 * ms, true},
		// The ordinary rebuild, which is 107 to 330 ms on this machine across a
		// day of size changes: it must stay out of the log.
		{"an ordinary rebuild", 0, 300 * ms, 30 * ms, false},
		// A refusal alone is enough, and it is the shape that hid: the encoder
		// kept was built in no time at all.
		{"a slow refusal alone", 2500 * ms, 50 * ms, 20 * ms, true},
		// And the close accuses somebody too, which was already true.
		{"a slow close", 0, 200 * ms, 2500 * ms, true},
	} {
		if got := rebuildWasSlow(c.refused, c.built, c.closed); got != c.want {
			t.Errorf("%s: rebuildWasSlow(%v, %v, %v) = %v, want %v",
				c.name, c.refused, c.built, c.closed, got, c.want)
		}
	}
}
