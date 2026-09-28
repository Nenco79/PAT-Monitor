package pipeline

import (
	"context"
	"testing"
	"time"
)

// **The wait both supervisors sleep doubles up to its cap, a wake cuts it short,
// and a cancelled context ends it.** The three are what the camera's and the
// microphone's loops each wrote for themselves, and `nextBackoff` has to be
// exactly them: a wake that did not reset would leave a choice waiting half a
// minute, a cap that did not hold would let a crash loop sleep for ever.
func TestTheBackoffDoublesToItsCapAndAWakeCutsItShort(t *testing.T) {
	// hi is not a power of two times lo, so that doubling alone never lands on
	// it and only the cap can.
	const lo, hi = time.Millisecond, 6 * time.Millisecond

	backoff := lo
	for _, want := range []time.Duration{2 * lo, 4 * lo, hi, hi, hi} {
		var ok bool
		backoff, ok = nextBackoff(context.Background(), nil, backoff, lo, hi)
		if !ok {
			t.Fatal("nextBackoff gave up with a live context")
		}
		if backoff != want {
			t.Fatalf("next backoff = %v, want %v", backoff, want)
		}
	}

	// A wake returns at once, whatever the wait was, and the next wait restarts
	// from lo and doubles like any other: 2*lo, the value both loops produced.
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	start := time.Now()
	got, ok := nextBackoff(context.Background(), wake, 10*time.Second, lo, hi)
	if el := time.Since(start); el > time.Second {
		t.Errorf("a wake waited %v: a choice sits out the backoff", el)
	}
	if !ok || got != 2*lo {
		t.Errorf("after a wake: backoff = %v, ok = %v, want %v, true", got, ok, 2*lo)
	}

	// A cancelled context ends the supervisor, and at once.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	if _, ok := nextBackoff(ctx, nil, 10*time.Second, lo, hi); ok {
		t.Error("a cancelled context does not end the wait")
	}
	if el := time.Since(start); el > time.Second {
		t.Errorf("a cancelled context waited %v", el)
	}
}
