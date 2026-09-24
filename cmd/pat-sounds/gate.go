package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"patmonitor/internal/detect"
	"patmonitor/internal/resample"
)

// gate measures the **gate**, that is, the shape detector that decides when to
// wake the model.
//
// **It is a different question from the model's, and it is judged the other way
// round.** What matters about the model is that it does not get things wrong;
// what matters about the gate is that it loses nothing: what does not pass here
// the model will never see, and a cry the gate discards is a cry nobody hears.
// The price of letting too much through is not a false alarm — the model is
// downstream — it is half a second of CPU per passage.
//
// So the two columns that count are **recall on the positives** and **the
// fraction of negatives that pass**, and the second reads as how much work the
// model will do for nothing.
//
//	pat-sounds gate -kind esc50 -dir DIR          what today's gate does
//	pat-sounds gate -kind esc50 -dir DIR -sweep   and what other values would do
func gateCmd(args []string) {
	fs := flag.NewFlagSet("gate", flag.ExitOnError)
	kind := fs.String("kind", "", "esc50, urbansound8k or fsd50k")
	dir := fs.String("dir", "", "directory the dataset was unpacked into")
	floorDB := fs.Float64("floor", -46, "the room background the gate has settled on, in dBFS")
	levelDB := fs.Float64("level", -30, "level the clips are brought to, in dBFS RMS")
	sweep := fs.Bool("sweep", false, "try other bands and durations")
	workers := fs.Int("workers", 0, "parallel workers (0 = half the cores)")
	_ = fs.Parse(args)
	if *kind == "" || *dir == "" {
		usage()
	}
	if _, ok := positives[*kind]; !ok {
		die(fmt.Errorf("unknown dataset %q", *kind))
	}
	clips, err := loadIndex(*kind, *dir)
	die(err)

	blocks := blocksFor(clips, *levelDB, *workers)
	fmt.Printf("%d clips out of %d read\n\n", len(blocks), len(clips))

	base := settled(*floorDB)
	if !*sweep {
		report(*kind, blocks, base, *floorDB)
		return
	}
	sweepGate(*kind, blocks, base, *floorDB)
}

// analysisRate is the analysis stream's rate, and blockDuration the duration of
// the block the pipeline hands the detector: 100 ms. **They live over there**,
// and are repeated here because a measurement made with other numbers would
// measure a gate that does not exist.
const (
	analysisRate  = 16000
	blockDuration = 100 * time.Millisecond
)

// clipBlocks is a clip already reduced to what the detector sees.
//
// **It is computed once and the parameters are replayed over it.** Reading and
// resampling two thousand clips costs minutes; running the blocks through the
// detector costs milliseconds, and there are dozens of combinations to try.
type clipBlocks struct {
	labels string
	blocks []detect.Block
}

func blocksFor(clips []clip, levelDB float64, workers int) []clipBlocks {
	if workers <= 0 {
		workers = max(1, runtime.NumCPU()/2)
	}
	jobs := make(chan clip)
	out := make(chan clipBlocks, 64)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rs := map[int]*resample.Rational{}
			for c := range jobs {
				w, rate, err := loadWAV(c.path)
				if err != nil || len(w) == 0 {
					fmt.Fprintf(os.Stderr, "skipped %s: %v\n", c.name, err)
					continue
				}
				if rate != analysisRate {
					r := rs[rate]
					if r == nil {
						if r, err = resample.New(rate, analysisRate); err != nil {
							fmt.Fprintf(os.Stderr, "skipped %s: %v\n", c.name, err)
							continue
						}
						rs[rate] = r
					}
					w = r.Convert(w)
				}
				normalise(w, levelDB)
				out <- clipBlocks{c.labels, blocksOf(w)}
			}
		}()
	}
	go func() {
		for _, c := range clips {
			jobs <- c
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()
	var all []clipBlocks
	for b := range out {
		all = append(all, b)
	}
	return all
}

// normalise brings the clip to a known effective level.
//
// **Without it, one would measure the dataset's normalisation and not the
// gate.** The absolute level of an ESC-50 clip means nothing — it depends on
// how whoever uploaded it to Freesound had their gain set — while the gate's
// threshold is a distance from the **room's** floor. Bringing them all to the
// same level, the threshold in dB stops being the variable and the two the
// dataset can really teach are left: **the band and the shape in time.**
func normalise(w []float32, targetDB float64) {
	var sum float64
	for _, v := range w {
		sum += float64(v) * float64(v)
	}
	rms := math.Sqrt(sum / float64(len(w)))
	if rms <= 0 {
		return
	}
	k := float32(math.Pow(10, targetDB/20) / rms)
	for i := range w {
		w[i] *= k
	}
}

func blocksOf(w []float32) []detect.Block {
	n := analysisRate * int(blockDuration/time.Millisecond) / 1000
	var out []detect.Block
	buf := make([]byte, n*2)
	for i := 0; i+n <= len(w); i += n {
		for k, v := range w[i : i+n] {
			s := v * 32768
			if s > 32767 {
				s = 32767
			}
			if s < -32768 {
				s = -32768
			}
			binary.LittleEndian.PutUint16(buf[k*2:], uint16(int16(s)))
		}
		out = append(out, detect.AnalyzeS16LE(buf))
	}
	return out
}

// settled returns a detector that already has a floor, as one has in service
// after a few minutes.
//
// **Without it, the first burst becomes the floor.** The detector takes the
// first audible block as its reference: given a clip that starts with a bark,
// the floor is born on the bark and from then on nothing triggers any more —
// one would measure a closed gate and blame the thresholds.
func settled(floorDB float64) *detect.Sound {
	s := detect.NewSound(analysisRate)
	n := analysisRate * int(blockDuration/time.Millisecond) / 1000
	buf := make([]byte, n*2)
	amp := math.Pow(10, floorDB/20) * 32768 * math.Sqrt(3) // uniform noise
	seed := uint32(1)
	now := time.Unix(0, 0)
	// Three time constants are enough for the floor to converge.
	for range int(3 * detect.DefaultFloorTau / blockDuration) {
		for k := range n {
			seed = seed*1664525 + 1013904223
			v := (float64(seed>>8)/float64(1<<24) - 0.5) * 2 * amp
			binary.LittleEndian.PutUint16(buf[k*2:], uint16(int16(v)))
		}
		now = now.Add(blockDuration)
		s.Feed(detect.AnalyzeS16LE(buf), now)
	}
	return s
}

// run passes a clip through the gate and says what came out.
//
// The detector is **copied** from the settled model instead of being resettled:
// they are the same numbers, and resettling it for each of the two thousand
// clips would cost more than all the rest put together.
func run(base *detect.Sound, b clipBlocks) (cry, bark bool, hz []float64) {
	s := *base
	now := time.Unix(0, 0).Add(3 * detect.DefaultFloorTau)
	for _, blk := range b.blocks {
		now = now.Add(blockDuration)
		st := s.Feed(blk, now)
		cry = cry || st.Cry
		bark = bark || st.Bark
		if st.Level >= st.Floor+s.TriggerDB {
			hz = append(hz, st.DominantHz)
		}
	}
	return cry, bark, hz
}

func report(kind string, all []clipBlocks, base *detect.Sound, floorDB float64) {
	fmt.Printf("today's gate: band %.0f-%.0f Hz, %.0f dB above the floor (%.0f dBFS)\n",
		base.MinHz, base.MaxHz, base.TriggerDB, floorDB)
	fmt.Printf("bark %v-%v x%d in %v; cry >=%v (or >=%v x%d in %v)\n\n",
		base.BarkMin, base.BarkMax, base.BarkHits, base.BarkWindow,
		base.CryLong, base.CryMin, base.CryHits, base.CryWindow)

	type row struct {
		n, cry, bark, any int
		hz                []float64
	}
	byCat := map[string]*row{}
	for _, b := range all {
		r := byCat[b.labels]
		if r == nil {
			r = &row{}
			byCat[b.labels] = r
		}
		c, k, hz := run(base, b)
		r.n++
		if c {
			r.cry++
		}
		if k {
			r.bark++
		}
		if c || k {
			r.any++
		}
		r.hz = append(r.hz, hz...)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(a, b int) bool {
		return float64(byCat[cats[a]].any)/float64(byCat[cats[a]].n) >
			float64(byCat[cats[b]].any)/float64(byCat[cats[b]].n)
	})
	pos := positives[kind]
	fmt.Printf("%-42s %6s %8s %8s %8s   %s\n",
		"category", "n", "cry", "bark", "passes", "dominant Hz (p10/median/p90)")
	for _, c := range cats {
		r := byCat[c]
		mark := "  "
		for code, p := range pos {
			if c == p {
				mark = "->"
				_ = code
			}
		}
		sort.Float64s(r.hz)
		h := func(p int) float64 {
			if len(r.hz) == 0 {
				return 0
			}
			i := len(r.hz) * p / 100
			if i >= len(r.hz) {
				i = len(r.hz) - 1
			}
			return r.hz[i]
		}
		fmt.Printf("%s %-40s %6d %7d%% %7d%% %7d%%   %5.0f %5.0f %5.0f\n",
			mark, short(c), r.n, 100*r.cry/r.n, 100*r.bark/r.n, 100*r.any/r.n,
			h(10), h(50), h(90))
	}
}

// sweepGate tries different bands and durations and reports the two columns that matter.
func sweepGate(kind string, all []clipBlocks, base *detect.Sound, floorDB float64) {
	pos := positives[kind]
	score := func(s *detect.Sound) (recall map[string]float64, pass float64) {
		hit := map[string]int{}
		tot := map[string]int{}
		passed, negs := 0, 0
		for _, b := range all {
			c, k, _ := run(s, b)
			isPos := false
			for code, p := range pos {
				if b.labels == p || (kind == "fsd50k" && hasLabel(b.labels, p)) {
					isPos = true
					tot[code]++
					if (code == "cry" && c) || (code == "bark" && k) {
						hit[code]++
					}
				}
			}
			if !isPos {
				negs++
				if c || k {
					passed++
				}
			}
		}
		recall = map[string]float64{}
		for code, t := range tot {
			recall[code] = float64(hit[code]) / float64(t)
		}
		if negs > 0 {
			pass = float64(passed) / float64(negs)
		}
		return recall, pass
	}

	show := func(name string, s *detect.Sound) {
		r, p := score(s)
		fmt.Printf("%-38s  cry %5.1f%%  bark %5.1f%%  negatives that pass %5.1f%%\n",
			name, 100*r["cry"], 100*r["bark"], 100*p)
	}

	fmt.Println("the band (the durations stay today's)")
	for _, b := range [][2]float64{{150, 2500}, {120, 1640}, {100, 3000}, {200, 2000}, {80, 4000}, {0, 8000}} {
		s := *base
		s.MinHz, s.MaxHz = b[0], b[1]
		show(fmt.Sprintf("  %.0f-%.0f Hz", b[0], b[1]), &s)
	}

	fmt.Println("\nhow many hits are needed (the band stays today's)")
	for _, h := range [][2]int{{2, 2}, {1, 1}, {1, 2}, {2, 1}, {3, 2}} {
		s := *base
		s.BarkHits, s.CryHits = h[0], h[1]
		show(fmt.Sprintf("  bark x%d, cry x%d", h[0], h[1]), &s)
	}

	fmt.Println("\nshape in time")
	for _, d := range []struct {
		name                              string
		barkMin, barkMax, cryMin, cryLong time.Duration
	}{
		{"today", 70 * time.Millisecond, 400 * time.Millisecond, 500 * time.Millisecond, 1200 * time.Millisecond},
		{"wider bark", 50 * time.Millisecond, 1000 * time.Millisecond, 500 * time.Millisecond, 1200 * time.Millisecond},
		{"shorter cry", 70 * time.Millisecond, 400 * time.Millisecond, 300 * time.Millisecond, 800 * time.Millisecond},
		{"all wide", 50 * time.Millisecond, 1200 * time.Millisecond, 300 * time.Millisecond, 800 * time.Millisecond},
	} {
		s := *base
		s.BarkMin, s.BarkMax, s.CryMin, s.CryLong = d.barkMin, d.barkMax, d.cryMin, d.cryLong
		show("  "+d.name, &s)
	}
	// **"No duration constraint" is not a row of this table**, and trying it was
	// instructive: widening `CryLong` as well makes the first branch of the
	// switch take everything and the bark drop to zero, that is, one measures
	// the order of the cases and not the durations. An experiment that changes
	// two things at once says nothing about either.

	fmt.Println("\ndistance from the floor")
	for _, db := range []float64{6, 9, 12, 15, 18} {
		s := *base
		s.TriggerDB = db
		s.ReleaseDB = db / 2
		show(fmt.Sprintf("  %.0f dB", db), &s)
	}

	// **The two levers together.** A grid taken one axis at a time hides the
	// combinations, and here the two that matter interact: a lower threshold
	// lengthens the bursts, and longer bursts change how many hits are needed to
	// reach the count.
	fmt.Println("\ndistance from the floor and hits, together")
	fmt.Printf("%-10s", "")
	for _, h := range []int{1, 2, 3} {
		fmt.Printf(" %-28s", fmt.Sprintf("x%d (cry/bark/pass)", h))
	}
	fmt.Println()
	for _, db := range []float64{6, 9, 12, 15} {
		fmt.Printf("%-10s", fmt.Sprintf("%.0f dB", db))
		for _, h := range []int{1, 2, 3} {
			s := *base
			s.TriggerDB, s.ReleaseDB = db, db/2
			s.BarkHits, s.CryHits = h, h
			r, p := score(&s)
			fmt.Printf(" %-28s", fmt.Sprintf("%5.1f%% %5.1f%% %5.1f%%",
				100*r["cry"], 100*r["bark"], 100*p))
		}
		fmt.Println()
	}
}

func hasLabel(labels, want string) bool {
	return scored{labels: labels}.has(want)
}
