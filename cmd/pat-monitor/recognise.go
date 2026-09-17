package main

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"patmonitor/internal/ced"
)

// **The model is what decides, and the shape detector acts as a gate.**
//
// The detector's own criteria — level above the floor, band, shape in time —
// tell the intermittent from the continuous and not much else: measured, they
// let through 100% of cockerels and 80% of a keyboard. That is enough to decide
// when to look, and not enough to say what is there, which is why CED does the
// saying.
//
// The two jobs want opposite tunings, and that is why they could not live in the
// same piece: whoever decides wants to be wrong rarely, whoever opens the gate
// wants to lose nothing. The two columns are in `baselines/sounds.txt`.
type recogniser struct {
	stream *ced.Stream
	model  *ced.Model
	watch  map[string][]int
	log    *slog.Logger

	// Touched by different goroutines: `wanted` by the status loop and read by
	// the capture, `badRate` by the capture alone but with a value that must not
	// be able to repeat the same line fifty times a second.
	wanted  atomic.Bool
	badRate atomic.Int64

	mu    sync.Mutex
	seen  *ced.Result
	hits  map[string][]time.Time
	until map[string]time.Time
}

// The AudioSet classes that count for each event, and the numbers that govern
// them. **Every one of these comes from a measurement**, and which one is next to it.
var (
	// watchedClasses is far narrower than one would be tempted to write. "Dog"
	// is the strongest class on real barks and catches `brushing_teeth` at
	// 0.442; "Domestic animals, pets" gives 0.877 on a cat. **Every extra class
	// brings its own false positives**: at threshold 0.10 "Bark + Dog" makes
	// fifteen false positives on ESC-50 where "Bark + Bow-wow" makes one.
	watchedClasses = map[string][]string{
		"bark": {"Bark", "Bow-wow"},
		"cry":  {"Baby cry, infant cry", "Crying, sobbing"},
	}

	// soundThreshold is the probability beyond which a class counts.
	//
	// **It was 0.20 and the measurement moved it**, over three public datasets of
	// negatives — 20,857 clips for the cry, 19,779 for the bark — plus 457 real
	// cries from donateacry. Both codes gain, and they gain much more than they
	// cost:
	//
	//	                   caught at 0.20   at 0.15      false, 0.20 -> 0.15
	//	cry, donateacry    363/457 (79%)    383 (83%)    ESC-50   0 -> 1
	//	cry, FSD50K         13/42  (30%)     15 (35%)    US8K     1 -> 4
	//	bark, US8K         660/998 (66%)    698 (69%)    FSD50K   2 -> 4
	//	bark, FSD50K        74/122 (60%)     77 (63%)    bark, all 13 -> 15
	//
	// **Sixty-three more real events against eight more false ones**, which on
	// the negatives is 0.014% to 0.043% for the cry and 0.066% to 0.076% for the
	// bark. And the next step down is where it turns: 0.20 to 0.15 buys 63 for 8,
	// 0.15 to 0.10 buys 49 for **22**. The knee is here, and it was measured
	// rather than chosen.
	//
	// **What enters at 0.15 is worth naming**, because a count hides it: ESC-50's
	// cat at 0.181; three `children_playing` on UrbanSound8K, 0.160 to 0.185, in
	// a class whose worst was already through at 0.292; a door squeak at 0.192
	// and a gasp at 0.187 on FSD50K. Nothing new in kind — the same confusions
	// the old threshold already had at its own edge.
	//
	// **It is not a per-class threshold** because CED's probabilities are not
	// comparable between classes — on an empty room the model is 38% sure it
	// hears a mouse — but these four have their floor measured at the same place,
	// and the move was checked on both codes precisely because one number serves
	// two.
	soundThreshold float32 = 0.15

	// cryHits and cryWindow: a cry wants two confirmations, a bark one.
	//
	// **The lever against the cat is time, not a higher threshold.** ESC-50's
	// cat reaches 0.181 and UrbanSound8K's children playing 0.292, so 0.30 would
	// cover both — but real cries diluted in a ten-second window sit at the
	// tenth percentile at 0.166, and the case that matters would be lost. A cat
	// miaows once, a child cries for minutes.
	//
	// **And that argument was an argument and not a measurement**, which mattered
	// more once the threshold moved below the cat. The window is 10.11 s and the
	// question is asked every 5, so two consecutive classifications overlap by
	// half a window:
	// one miaow is seen **twice**, and the two confirmations need not even be
	// consecutive — two in thirty seconds is enough. What may still save it is
	// dilution, which is brutal — a one-second event in a full window falls from
	// 0.378 to 0.025. **It was then measured by air, and the rule holds**: that
	// cat played once gives one classification at 0.619 and then zeros, with no
	// alert; played twice it alerts. So one event gives one hit, and without the
	// rule a single miaow would ring, because through the air that cat scores
	// 0.62 where the file scores 0.18. The three `children_playing` are not
	// covered by this rule at all: children playing are not an event that
	// happens once.
	//
	// **It does not apply to the bark**: that is a short episode, and demanding
	// two windows of it would mean losing it. Nor is it needed — on all three
	// datasets the bark's real false positives at 0.20 are zero.
	cryHits   = 2
	cryWindow = 30 * time.Second

	// verdictHold is how long a verdict lasts with no new confirmations. It is
	// the same `Settle` as the shape detector, and they are the same number
	// deliberately: an episode that continues refreshes the verdict, and one
	// that has ended lets it expire instead of waiting for somebody to turn it
	// off.
	verdictHold = 20 * time.Second
)

// newRecogniser loads the embedded model and resolves the classes.
//
// **A failure here does not stop the monitor.** Refusing to watch over a child
// because a classifier could not be loaded would be absurd: it is the same rule
// as the log that cannot be written. It is declared and things go on without it
// — and without it, the monitor watches and listens as it always has, only it
// reports neither cries nor barks.
//
// **The two possible failures are not of the same kind, and the message tells
// them apart.** The first is the file, which lives inside the binary: it can be
// missing only if the executable is corrupt, and then this is the least of the
// problems. The second is **our list** — a class name the model does not have —
// and it is not a run-time risk: it is a compile-time property, caught by
// `TestNothingIsAskedWithBothSwitchesOff`, which calls `newRecogniser` for real.
// Accusing the model of a name we got wrong would send the reader looking for
// the fault where it is not.
func newRecogniser(log *slog.Logger) *recogniser {
	m, err := ced.Embedded()
	if err != nil {
		log.Warn("the sound model did not load, cries and barks will not be reported", "error", err)
		return nil
	}
	r := &recogniser{
		model: m,
		log:   log,
		watch: map[string][]int{},
		hits:  map[string][]time.Time{},
		until: map[string]time.Time{},
	}
	for code, names := range watchedClasses {
		for _, n := range names {
			i, err := m.Index(n)
			if err != nil {
				log.Warn("a class the monitor watches is not in the model, cries and barks will not be reported", "event", code, "error", err)
				return nil
			}
			r.watch[code] = append(r.watch[code], i)
		}
	}
	r.stream = ced.NewStream(m, recogniseInterval)
	// The threshold is formatted, not passed bare: it is a float32, and `slog`
	// promotes it to float64 printing `0.20000000298023224` — the noise of the
	// conversion read as though it were a fine tuning.
	log.Info("sound recognition ready",
		"classes", len(m.Labels()), "rate_hz", m.SampleRate(),
		"window_s", fmt.Sprintf("%.2f", m.WindowSeconds()),
		"threshold", fmt.Sprintf("%.2f", soundThreshold))
	return r
}

// recogniseInterval is the minimum wait between two classifications.
//
// The gate stays open for twenty seconds after seeing something, so one episode
// produces four or five classifications: enough for a cry to reach its two
// confirmations, and sparse enough to cost 10% of a core while it lasts. **The
// cadence lives in the stream and not in whoever asks**, so it holds for every
// road a request can come from.
const recogniseInterval = 5 * time.Second

// Feed hands over the analysis stream. It has to be called from the capture
// goroutine: it does not block, and the classification is on a goroutine of its
// own.
//
// **A rate the model does not accept is declared.** The analysis stream always
// comes out at 16 kHz, so it should never happen — but if it did, the recogniser
// would stop answering and from outside that would be indistinguishable from a
// quiet room. Now that the alert no longer says "possible", an unexplained
// silence is the wrong confidence in the worst place.
func (r *recogniser) Feed(pcm []byte, rate int) {
	if r == nil {
		return
	}
	// **Every** rate is recorded and the line is written only when it changes
	// and is wrong: this way the line comes out once per microphone and not
	// fifty times a second, and if a bad rate returns after a good one it says
	// so again. Recording only the bad ones would leave the return silent.
	if r.badRate.Swap(int64(rate)) != int64(rate) && rate != r.model.SampleRate() {
		r.log.Error("the analysis stream is not at the model's rate: cries and barks will not be reported",
			"hz", rate, "wanted_hz", r.model.SampleRate())
	}
	r.stream.FeedS16(pcm, rate)
}

// Wanted says whether anybody will read the verdict.
//
// It has to be called from the status loop, which already reads the switches:
// **the stream is always delivered**, so that turning one on finds the window
// already full, but with both off the model is not woken for a verdict nobody
// will look at. Unlike motion — which has a second reader, the bitrate discount
// — this verdict has a single consumer.
func (r *recogniser) Wanted(cry, bark bool) {
	if r != nil {
		r.wanted.Store(cry || bark)
	}
}

// Ask is the gate opening.
//
// **A classification is not asked for at the instant the sound begins**, and it
// is not even attempted: the window holds ten seconds, and at the start of an
// episode nine of them are from before. Measured, one second of crying inside
// ten is worth 0.025 — invisible. The remedy is not a delay written here but the
// fact that the gate stays open and the question repeats: the second
// classification looks at a window the event has filled.
func (r *recogniser) Ask() {
	if r != nil && r.wanted.Load() {
		r.stream.Ask()
	}
}

// Verdict is what is in the room now, according to the model.
//
// It has to be called at a regular cadence — from the status loop, once a second
// — and consumes the results as they arrive. With the recogniser absent it
// answers no twice: **the shape detector no longer decides in any case**,
// because its present tuning is that of a gate and as a decider it would sound
// on more than half the loud sounds of a room.
func (r *recogniser) Verdict(now time.Time) (cry, bark bool) {
	if r == nil {
		return false, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if res := r.stream.Last(); res != nil && res != r.seen {
		r.seen = res
		r.apply(res, now)
	}
	return now.Before(r.until["cry"]), now.Before(r.until["bark"])
}

// apply carries a result into the verdict.
func (r *recogniser) apply(res *ced.Result, now time.Time) {
	if res.Err != nil {
		r.log.Warn("sound recognition failed", "error", res.Err)
		return
	}
	args := []any{"window_s", fmt.Sprintf("%.2f", res.Seconds), "took_ms", res.Took.Milliseconds()}
	for _, code := range []string{"cry", "bark"} {
		label, p := r.best(res.Scores, code)
		args = append(args, code, fmt.Sprintf("%.3f", p), code+"_class", label)
		if p < soundThreshold {
			continue
		}
		need := 1
		if code == "cry" {
			need = cryHits
		}
		if need == 1 {
			r.until[code] = now.Add(verdictHold)
			continue
		}
		// The confirmations are counted inside a window, not one after the
		// other: the first classification of an episode looks at a window
		// the event has not yet filled, and demanding two **consecutive**
		// results would turn it into a lost confirmation instead of a
		// missing one.
		keep := r.hits[code][:0]
		for _, t := range r.hits[code] {
			if now.Sub(t) <= cryWindow {
				keep = append(keep, t)
			}
		}
		r.hits[code] = append(keep, now)
		if len(r.hits[code]) >= need {
			r.until[code] = now.Add(verdictHold)
			r.hits[code] = nil
		}
	}
	// **Every verdict is written down, even the one that decides nothing.**
	// Whoever comes back in the morning has only the log, and that is where they
	// have to be able to see that the model looked and what it saw: without it,
	// a cry that was not reported and a model that never ran look the same.
	r.log.Debug("sound recognised", args...)
}

// best is the most lit class among those that count for a code.
//
// **The highest is taken, not the sum**: they are independent probabilities and
// not a distribution, so adding them can exceed one and means nothing. The class
// is reported next to the number because "Bark 0.42" and "Bow-wow 0.42" are two
// different pieces of news for whoever reads the log.
func (r *recogniser) best(scores []float32, code string) (string, float32) {
	label, best := "", float32(0)
	for _, i := range r.watch[code] {
		if scores[i] > best {
			label, best = r.model.Labels()[i], scores[i]
		}
	}
	return label, best
}

func (r *recogniser) Close() {
	if r != nil {
		_ = r.stream.Close()
	}
}
