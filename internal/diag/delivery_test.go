package diag

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// The instrument had no test at all, and it is the one whose line the baselines
// say to read **first** — *"the line to look at first is delivery regularity,
// not the totals"*. Its arithmetic needs no device: a clock is a parameter of
// `Mark` precisely so that it can be driven.
//
// It is also the family that has already cost this project twice: `pat-viewer`
// counted a cadence wrong in two opposite directions at once and made the
// monitor's own counter look suspect, and a truncated buffer made a tool stop
// measuring without saying so. **A wrong measurement always accuses somebody
// else**, and this one's verdict is about the hardware.

var t0 = time.Date(2026, 9, 14, 22, 0, 0, 0, time.UTC)

// marks delivers at the given intervals and returns the meter.
func marks(d *Delivery, gaps ...time.Duration) *Delivery {
	at := t0
	d.Mark(at)
	for _, g := range gaps {
		at = at.Add(g)
		d.Mark(at)
	}
	return d
}

// steady is n intervals of exactly the nominal one.
func steady(n int, nominal time.Duration) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = nominal
	}
	return out
}

func report(t *testing.T, d *Delivery) string {
	t.Helper()
	var b bytes.Buffer
	d.Report(&b, "test")
	return b.String()
}

// **The ring is read oldest to newest, and the burst counting rests on it.** A
// run of short intervals is contiguous only if the order is the real one:
// reconstructed backwards, or rotated, the same deliveries would be counted as
// several short bursts instead of one long one, which is the number the report
// exists to give.
func TestTheRingIsReadOldestToNewest(t *testing.T) {
	d := &Delivery{Nominal: 10 * time.Millisecond, Window: 4}
	// Six intervals into a window of four: the first two fall off the end.
	marks(d, 1*time.Millisecond, 2*time.Millisecond, 3*time.Millisecond,
		4*time.Millisecond, 5*time.Millisecond, 6*time.Millisecond)

	got, total := d.snapshot()
	if total != 6 {
		t.Errorf("total %d, wanted 6", total)
	}
	want := []time.Duration{3, 4, 5, 6}
	if len(got) != len(want) {
		t.Fatalf("kept %d intervals, wanted %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w*time.Millisecond {
			t.Errorf("interval %d is %v, wanted %v — the ring is not in delivery order: %v",
				i, got[i], w*time.Millisecond, got)
		}
	}
}

// And the report says the window slid, rather than presenting the tail as the
// whole run.
func TestTheWindowSaysWhatItDropped(t *testing.T) {
	d := &Delivery{Nominal: 10 * time.Millisecond, Window: 16}
	marks(d, steady(40, 10*time.Millisecond)...)

	out := report(t, d)
	if !strings.Contains(out, "last 16 of 40") {
		t.Errorf("the report does not say the window slid:\n%s", out)
	}
}

// **A run of k short intervals is k+1 items**, which is what the comment on the
// counting says and what the airtime is computed from: the report is read
// against `jitterBufferMinimumDelay`, so an item out means a nominal interval of
// delay attributed to the wrong place.
func TestARunOfKShortIntervalsIsKPlusOneItems(t *testing.T) {
	const nominal = 20 * time.Millisecond
	d := &Delivery{Nominal: nominal, Window: 64}

	// Three deliveries at once — two short intervals — inside otherwise steady
	// delivery, and enough intervals around it to clear the "too few" floor.
	gaps := steady(6, nominal)
	gaps = append(gaps, time.Millisecond, time.Millisecond)
	gaps = append(gaps, steady(6, nominal)...)
	marks(d, gaps...)

	out := report(t, d)
	if !strings.Contains(out, "bursts       1, from 3 to 3 items") {
		t.Errorf("two short intervals were not reported as three items arriving together:\n%s", out)
	}
	if !strings.Contains(out, "in bursts    2 of 14") {
		t.Errorf("the count of short intervals is wrong:\n%s", out)
	}
	// The airtime is the item count times the nominal interval: three items at
	// 20 ms is the 60 ms the receiver has to absorb.
	if !strings.Contains(out, "p95 60ms") {
		t.Errorf("the burst airtime is not three nominal intervals:\n%s", out)
	}
}

// **A burst that is still open when the samples end is counted.** The loop
// records a run when it meets a long interval, so the last one is recorded only
// by the line after the loop — and a burst at the very end is exactly the shape
// a stream that has just started stuttering has.
func TestABurstStillOpenAtTheEndIsCounted(t *testing.T) {
	const nominal = 20 * time.Millisecond
	d := &Delivery{Nominal: nominal, Window: 64}

	gaps := steady(11, nominal)
	gaps = append(gaps, time.Millisecond, time.Millisecond, time.Millisecond)
	marks(d, gaps...)

	out := report(t, d)
	if !strings.Contains(out, "bursts       1, from 4 to 4 items") {
		t.Errorf("the burst at the end was dropped:\n%s", out)
	}
}

// And one that is open at the very beginning, where `run` starts at 1 before any
// interval has been seen.
func TestABurstAtTheVeryBeginningIsCounted(t *testing.T) {
	const nominal = 20 * time.Millisecond
	d := &Delivery{Nominal: nominal, Window: 64}

	gaps := []time.Duration{time.Millisecond, time.Millisecond}
	gaps = append(gaps, steady(11, nominal)...)
	marks(d, gaps...)

	out := report(t, d)
	if !strings.Contains(out, "bursts       1, from 3 to 3 items") {
		t.Errorf("the burst at the start was dropped:\n%s", out)
	}
}

// Steady delivery says so in as many words: it is the good verdict, and it must
// be reachable only by actually being steady.
func TestSteadyDeliverySaysNone(t *testing.T) {
	d := &Delivery{Nominal: 20 * time.Millisecond, Window: 64}
	marks(d, steady(20, 20*time.Millisecond)...)

	out := report(t, d)
	if !strings.Contains(out, "none: steady delivery") {
		t.Errorf("steady delivery was not reported as such:\n%s", out)
	}
	if !strings.Contains(out, "in bursts    0 of 20 (0%)") {
		t.Errorf("the burst share is wrong on a steady stream:\n%s", out)
	}
}

// Too few samples is declared rather than answered with quantiles taken from
// three numbers. The floor is eleven intervals: ten is too few.
func TestTooFewSamplesIsDeclared(t *testing.T) {
	d := &Delivery{Nominal: 20 * time.Millisecond}
	marks(d, steady(10, 20*time.Millisecond)...)
	if out := report(t, d); !strings.Contains(out, "too few samples (10)") {
		t.Errorf("ten intervals were reported on:\n%s", out)
	}

	d = &Delivery{Nominal: 20 * time.Millisecond}
	marks(d, steady(11, 20*time.Millisecond)...)
	if out := report(t, d); strings.Contains(out, "too few samples") {
		t.Errorf("eleven intervals were refused:\n%s", out)
	}
}

// **The percentile indices are taken by integer arithmetic, and the edges are
// where that kind of index goes off the end.** Eleven intervals is the first
// length that reports at all, and one run is the first that has a p95.
func TestThePercentileIndicesHoldAtTheEdges(t *testing.T) {
	for _, n := range []int{11, 12, 19, 20, 21, 100} {
		d := &Delivery{Nominal: 20 * time.Millisecond, Window: 256}
		gaps := make([]time.Duration, 0, n)
		// Alternate so there are as many runs as the length allows: every burst
		// is one short interval between two long ones.
		for i := range n {
			if i%2 == 1 {
				gaps = append(gaps, time.Millisecond)
			} else {
				gaps = append(gaps, 20*time.Millisecond)
			}
		}
		out := report(t, d.markAll(gaps))
		if !strings.Contains(out, "bursts") {
			t.Errorf("%d intervals produced no burst line:\n%s", n, out)
		}
	}
}

// markAll is marks() as a method, so a table can build the meter inline.
func (d *Delivery) markAll(gaps []time.Duration) *Delivery { return marks(d, gaps...) }

// **An empty meter is not a steady stream.** Report is called on whatever the
// run produced, and a stream that delivered nothing at all must not come back
// with a verdict.
func TestAMeterNobodyMarkedSaysSo(t *testing.T) {
	d := &Delivery{Nominal: 20 * time.Millisecond}
	out := report(t, d)
	if !strings.Contains(out, "too few samples (0)") {
		t.Errorf("an empty meter reported something:\n%s", out)
	}
	// And one single delivery, which records no interval at all.
	d = &Delivery{Nominal: 20 * time.Millisecond}
	d.Mark(t0)
	if out := report(t, d); !strings.Contains(out, "too few samples (0)") {
		t.Errorf("a single delivery produced an interval:\n%s", out)
	}
}

// Mark is called from the goroutine that delivers — the WASAPI callback runs on
// a thread of its own — so it is called concurrently with nothing else holding
// it. The race detector cannot be run here (it wants cgo, and there is no C
// compiler on this machine), so what is asserted is that no interval is lost.
func TestEveryMarkIsCountedWhenTheyComeFromSeveralGoroutines(t *testing.T) {
	d := &Delivery{Nominal: time.Millisecond, Window: 32}

	const writers, each = 8, 200
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range each {
				d.Mark(t0.Add(time.Duration(w*each+i) * time.Millisecond))
			}
		}(w)
	}
	wg.Wait()

	_, total := d.snapshot()
	// The first Mark records no interval; every one after it records exactly one.
	if want := writers*each - 1; total != want {
		t.Errorf("%d intervals recorded, wanted %d: a delivery was lost or double counted",
			total, want)
	}
}

// **A meter with no nominal interval does not report steady delivery.**
//
// The burst threshold is a quarter of the nominal one, so at zero no interval is
// ever below it: the share comes out 0% and the verdict "none: steady delivery"
// — the best answer there is, on a question that was never asked, on the line the
// baselines say to read first. The quantiles gave it away all along, a minimum
// far under the median with "steady" beneath it, which is the report
// contradicting itself.
//
// Every caller today sets Nominal. What this guards is the zero value of an
// exported struct, which is what the next caller writes.
func TestWithNoNominalTheBurstsAreNotDeclaredSteady(t *testing.T) {
	d := &Delivery{Window: 64} // Nominal forgotten
	gaps := []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	gaps = append(gaps, steady(11, 20*time.Millisecond)...)
	marks(d, gaps...)

	out := report(t, d)
	if strings.Contains(out, "steady delivery") {
		t.Errorf("a meter that cannot measure declared the stream steady:\n%s", out)
	}
	if !strings.Contains(out, "not measured") {
		t.Errorf("it did not say it could not measure:\n%s", out)
	}
	// And the intervals are still reported: they need no nominal, and they are
	// what shows the burst is there at all.
	if !strings.Contains(out, "min 1ms") {
		t.Errorf("the quantiles went with it:\n%s", out)
	}

	// The other direction: with a nominal, the same stream is judged.
	d2 := &Delivery{Nominal: 20 * time.Millisecond, Window: 64}
	marks(d2, gaps...)
	if out := report(t, d2); !strings.Contains(out, "from 4 to 4 items") {
		t.Errorf("with a nominal the burst was not counted:\n%s", out)
	}
}
