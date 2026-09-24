// Package diag holds the measuring tools the diagnostic commands use.
package diag

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"
	"time"
)

// Delivery records when a stream was handed over and describes how steady that
// was.
//
// It tells paced delivery from delivery in blocks. The difference is not
// cosmetic: a WebRTC receiver estimates jitter from exactly these intervals and
// sizes its playout buffer accordingly, and that buffer is constant latency. A
// stream delivered in bursts costs delay with zero packets lost, and even over
// localhost.
type Delivery struct {
	// Nominal is the interval expected between two deliveries.
	Nominal time.Duration

	// Window is how many intervals are kept. Zero means defaultWindow.
	//
	// The intervals live in a ring, not in a list that grows: in steady state
	// video, audio and analysis together deliver close to a hundred times a
	// second, and an endless accumulation would be tens of megabytes over a
	// night, never handed back at that. A sliding window also has the merit of
	// describing delivery as it is now, rather than the average since startup.
	Window int

	// Mark is called from the goroutine that delivers, which is often not the
	// one that later reads the report: the WASAPI callback, for one, runs on a
	// thread of its own.
	mu    sync.Mutex
	last  time.Time
	ring  []time.Duration
	next  int
	total int
}

// defaultWindow covers some ten seconds of video or twenty of audio: enough to
// estimate the quantiles without chasing every single hiccup.
const defaultWindow = 512

// Mark records a delivery that happened now.
func (d *Delivery) Mark(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.last.IsZero() {
		if d.ring == nil {
			n := d.Window
			if n <= 0 {
				n = defaultWindow
			}
			d.ring = make([]time.Duration, n)
		}
		d.ring[d.next] = now.Sub(d.last)
		d.next = (d.next + 1) % len(d.ring)
		d.total++
	}
	d.last = now
}

// snapshot returns a copy of the intervals kept.
func (d *Delivery) snapshot() ([]time.Duration, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := min(d.total, len(d.ring))
	out := make([]time.Duration, 0, n)
	// Oldest to newest, so runs of short intervals stay contiguous: the bursts
	// are counted on those.
	for i := range n {
		out = append(out, d.ring[(d.next-n+i+len(d.ring))%len(d.ring)])
	}
	return out, d.total
}

// Report describes the stream's delivery on w.
//
// Besides the quantiles of the intervals it counts the bursts: a burst is a run
// of deliveries separated by intervals far shorter than the nominal one, that
// is, material that arrived all at once. Its telling measure is not the number
// of items but the airtime it carries, because that is what the receiver has to
// absorb: it compares directly against the jitterBufferMinimumDelay read in
// webrtc-internals.
func (d *Delivery) Report(w io.Writer, label string) {
	gaps, total := d.snapshot()

	if len(gaps) <= 10 {
		fmt.Fprintf(w, "  Delivery %s: too few samples (%d)\n", label, len(gaps))
		return
	}
	scope := ""
	if total > len(gaps) {
		scope = fmt.Sprintf(", last %d of %d", len(gaps), total)
	}

	sorted := append([]time.Duration(nil), gaps...)
	slices.Sort(sorted)

	// Deliberately low threshold: at a quarter of the nominal interval there is
	// no doubt the two deliveries belong to the same block rather than to two
	// real instants.
	threshold := d.Nominal / 4

	// A run of k short intervals is k+1 items that arrived together.
	var runs []int
	burst, run := 0, 1
	for _, g := range gaps {
		if g < threshold {
			burst++
			run++
			continue
		}
		if run > 1 {
			runs = append(runs, run)
		}
		run = 1
	}
	if run > 1 {
		runs = append(runs, run)
	}

	fmt.Fprintf(w, "  Delivery %s (nominal %v, %d intervals%s):\n",
		label, d.Nominal.Round(time.Millisecond), len(gaps), scope)
	fmt.Fprintf(w, "    intervals    min %v   median %v   p95 %v   max %v\n",
		sorted[0].Round(100*time.Microsecond),
		sorted[len(sorted)/2].Round(100*time.Microsecond),
		sorted[len(sorted)*95/100].Round(100*time.Microsecond),
		sorted[len(sorted)-1].Round(100*time.Microsecond))
	// **With no nominal interval there is no burst measurement, and "steady" is
	// the best verdict there is on a question never asked.** The threshold is a
	// quarter of the nominal one, so at zero no interval is ever short of it:
	// every burst counts as none, the share comes out 0%, and the line the
	// baselines say to read *first* reports the good answer. The quantiles above
	// give it away — a minimum far under the median with "steady delivery"
	// beneath it — which is the report contradicting itself instead of declaring
	// what it could not do. A meter that cannot measure does not draw silence.
	//
	// It is not reachable from the four callers as they stand, all of which set
	// Nominal. It is reachable from the zero value of an exported struct, which
	// is what the next caller writes.
	if d.Nominal <= 0 {
		fmt.Fprintf(w, "    in bursts    not measured: no nominal interval declared\n")
		fmt.Fprintf(w, "    bursts       not measured: set Delivery.Nominal\n\n")
		return
	}

	fmt.Fprintf(w, "    in bursts    %d of %d (%.0f%%)\n",
		burst, len(gaps), float64(burst)*100/float64(len(gaps)))

	if len(runs) == 0 {
		fmt.Fprintf(w, "    bursts       none: steady delivery\n\n")
		return
	}
	sort.Ints(runs)
	p95 := runs[len(runs)*95/100]
	max := runs[len(runs)-1]
	fmt.Fprintf(w, "    bursts       %d, from %d to %d items (p95 %d)\n",
		len(runs), runs[0], max, p95)
	fmt.Fprintf(w, "    burst airtime       p95 %v   max %v\n\n",
		(time.Duration(p95) * d.Nominal).Round(time.Millisecond),
		(time.Duration(max) * d.Nominal).Round(time.Millisecond))
}
