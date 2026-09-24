package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"

	"patmonitor/internal/ced"
	"patmonitor/internal/gguf"
	"patmonitor/internal/resample"
)

// dilute measures how much a short event loses inside a whole window.
//
// **It is the measurement that can invalidate the thresholds**, and it appears
// in no dataset: the clips last five seconds and the event fills them, while
// the monitor's window lasts ten and a real event occupies a fraction of it. A
// threshold chosen on full clips is a threshold chosen under the wrong
// conditions.
//
// The event is placed at the centre of a window in two ways: in digital
// silence, and on a room's background. **Silence alone is not enough**: it is
// not what a bedroom contains, and the front-end's cut in decibels is relative
// to the window's maximum — so a background that exists changes the whole
// spectrogram, not only its empty parts.
func dilute(args []string) {
	fs := flag.NewFlagSet("dilute", flag.ExitOnError)
	model := fs.String("model", "internal/ced/ced-tiny-q8_0.gguf", "path of the CED model in GGUF")
	dir := fs.String("dir", "", "directory the dataset was unpacked into")
	kind := fs.String("kind", "esc50", "esc50 or donateacry")
	// **The room background always comes from ESC-50**, whatever the
	// positives are: it is `clock_tick`, the class that most resembles a
	// still room, and a corpus of cries has nothing of the kind in it. With
	// the positives taken from elsewhere this has to be said, and the tool
	// refuses rather than quietly measuring the event in silence — which is
	// the column that answers a different question and looks like this one.
	bgDir := fs.String("bg", "", "where ESC-50 is, for the room background (default: -dir)")
	count := fs.Int("n", 20, "how many positives per code")
	floorDB := fs.Float64("floor", -46, "level of the room background, in dBFS")
	// **The event is left at its own level unless this is given**, and the
	// two answer different questions. Left alone, what is measured is the
	// corpus as it is, quiet tail included, which is what a monitor across a
	// room really receives. Brought to a level, the level stops being the
	// variable and what is left is the sound itself -- which is the only way
	// to tell "this cry is harder to recognise" from "this recording is
	// quieter", two things that predict the same score. It is what `gate`
	// already does for the same reason.
	levelDB := fs.Float64("level", 0, "bring every event to this level in dBFS (0: leave it as it is)")
	// **The question the other columns cannot answer: how far above the room
	// can a cry be and still be heard?** Turning the volume down on the file
	// alone measures almost nothing, because the front end normalises on the
	// window's own maximum -- measured, 20 dB off a corpus of cries costs 14%
	// of the median. What has to fall is the distance from the floor, with the
	// floor where the room really puts it, and that is what this sweeps.
	//
	// It is one event length -- five seconds, the one a monitor gets -- on the
	// room, and it reports the share above the threshold rather than the
	// median alone, because what is being looked for is where the answer
	// stops arriving.
	sweep := fs.Bool("sweep", false, "walk the event down towards the room floor instead of the four lengths")
	threshold := fs.Float64("threshold", 0.20, "the threshold the share is counted against")
	_ = fs.Parse(args)
	if *dir == "" {
		usage()
	}
	// The same refusal judge makes, and for the same reason: an unknown name
	// answers no positives, and the report then comes out looking fine while
	// having measured nothing.
	if _, ok := positives[*kind]; !ok {
		die(fmt.Errorf("unknown dataset %q", *kind))
	}
	raw, err := os.ReadFile(*model)
	die(err)
	g, err := gguf.Parse(raw)
	die(err)
	m, err := ced.New(g)
	die(err)
	clips, err := loadIndex(*kind, *dir)
	die(err)

	// **One resampler per rate, and not one for 44100.** It was built for
	// ESC-50, where every clip is 44.1 kHz, and it was applied to whatever
	// arrived: a corpus recorded at 8 kHz would have been converted as though
	// it were 44.1, that is, stretched by five and a half and scored as a
	// different sound entirely. It is the trap this file exists to avoid, one
	// floor down — a measurement that answers, and answers about something
	// else.
	rs := map[int]*resample.Rational{}
	load := func(path string) []float32 {
		w, rate, err := loadWAV(path)
		die(err)
		if rate == m.SampleRate() {
			return w
		}
		r := rs[rate]
		if r == nil {
			r, err = resample.New(rate, m.SampleRate())
			die(err)
			rs[rate] = r
		}
		return r.Convert(w)
	}

	win := m.WindowSamples()
	// A background that is not silence. `clock_tick` is the ESC-50 class that
	// most resembles a still room; it is concatenated until it fills the window
	// and brought to the level measured with the real microphone.
	var background []float32
	bgClips := clips
	if *bgDir != "" {
		bgClips, err = loadIndex("esc50", *bgDir)
		die(err)
	}
	for _, c := range bgClips {
		if c.labels == "clock_tick" && len(background) < win {
			background = append(background, load(c.path)...)
		}
	}
	if len(background) < win {
		die(fmt.Errorf("only %d samples of background, %d wanted: with positives "+
			"from another corpus, -bg has to point at ESC-50", len(background), win))
	}
	background = background[:win]
	var rms float64
	for _, v := range background {
		rms += float64(v) * float64(v)
	}
	if rms = math.Sqrt(rms / float64(len(background))); rms > 0 {
		k := float32(math.Pow(10, *floorDB/20) / rms)
		for i := range background {
			background[i] *= k
		}
	}

	idx := map[string][]int{}
	for code, names := range chosen {
		for _, n := range names {
			i, err := m.Index(n)
			die(err)
			idx[code] = append(idx[code], i)
		}
	}
	best := func(sc []float32, code string) float32 {
		var v float32
		for _, i := range idx[code] {
			if sc[i] > v {
				v = sc[i]
			}
		}
		return v
	}

	fmt.Printf("window %.2f s, room background at %.0f dBFS, %d positives per kind\n",
		m.WindowSeconds(), *floorDB, *count)

	if *sweep {
		sweepToTheFloor(m, clips, load, background, best, positives[*kind], *count,
			*floorDB, *threshold)
		return
	}
	for _, code := range []string{"cry", "bark"} {
		positive := positives[*kind][code]
		var whole []float32
		// **The whole-clip row says how long the clip was, and it used to say
		// "5.0 s".** That was ESC-50's length written down as a constant, and
		// the day a corpus of seven-second clips arrived the instrument
		// reported a seven-second measurement under a five-second label — into
		// a file that is transcribed into `baselines/sounds.txt`. An instrument
		// that quietly changes what it measures is worse than one that measures
		// the wrong thing loudly.
		var lengthsSeen []float64
		lengths := []float64{5, 2, 1, 0.5}
		silence := map[float64][]float32{}
		room := map[float64][]float32{}
		n := 0
		for _, c := range clips {
			if c.labels != positive || n >= *count {
				continue
			}
			n++
			w := load(c.path)
			sc, err := m.Scores(w)
			die(err)
			whole = append(whole, best(sc, code))
			lengthsSeen = append(lengthsSeen, float64(len(w))/float64(m.SampleRate()))
			for _, secs := range lengths {
				part := loudestPart(w, int(secs*float64(m.SampleRate())), m.SampleRate())
				if *levelDB != 0 {
					part = atLevel(part, *levelDB)
				}
				a := make([]float32, win)
				off := win/2 - len(part)/2
				copy(a[off:], part)
				sa, err := m.Scores(a)
				die(err)
				silence[secs] = append(silence[secs], best(sa, code))

				b := append([]float32(nil), background...)
				for i, v := range part {
					b[off+i] += v
				}
				sb, err := m.Scores(b)
				die(err)
				room[secs] = append(room[secs], best(sb, code))
			}
		}
		if n == 0 {
			continue
		}
		fmt.Printf("\n--- %s: %d positives (%s) ---\n", code, n, positive)
		fmt.Printf("%-10s %-26s %s\n", "event", "in silence", "on the room background")
		slices.Sort(whole)
		sort.Float64s(lengthsSeen)
		fmt.Printf("%-10s whole clip: median %.3f  p10 %.3f  min %.3f\n",
			fmt.Sprintf("%.1f s", lengthsSeen[len(lengthsSeen)/2]),
			quantile(whole, 50), quantile(whole, 10), whole[0])
		for _, secs := range lengths {
			a, b := silence[secs], room[secs]
			slices.Sort(a)
			slices.Sort(b)
			fmt.Printf("%-10.1f med %.3f p10 %.3f min %.3f    med %.3f p10 %.3f min %.3f\n",
				secs, quantile(a, 50), quantile(a, 10), a[0],
				quantile(b, 50), quantile(b, 10), b[0])
		}
	}
}

// loudestPart cuts out the loudest seconds.
//
// **It is not taken from the start**: many clips begin with a moment of
// silence, and cutting the first seconds would measure the cut instead of the
// dilution.
func loudestPart(w []float32, n, rate int) []float32 {
	if len(w) <= n || n <= 0 {
		return w
	}
	at, best := 0, -1.0
	for start := 0; start+n <= len(w); start += rate / 10 {
		var e float64
		for _, v := range w[start : start+n] {
			e += float64(v) * float64(v)
		}
		if e > best {
			best, at = e, start
		}
	}
	return w[at : at+n]
}

// atLevel brings a stretch to a given RMS in dBFS, so that comparing two
// corpora compares the sound and not how close the microphone was.
func atLevel(x []float32, db float64) []float32 {
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	rms := math.Sqrt(sum / float64(len(x)))
	if rms <= 0 {
		return x
	}
	k := float32(math.Pow(10, db/20) / rms)
	out := make([]float32, len(x))
	for i, v := range x {
		out[i] = v * k
	}
	return out
}

// sweepToTheFloor walks the event down towards the room's own level and says
// where the recogniser stops answering.
//
// The event is five seconds, which is what a cry gives a window, and it is
// mixed onto the same background as everywhere else here. What changes from row
// to row is one thing: how far above the floor it sits.
func sweepToTheFloor(m *ced.Model, clips []clip, load func(string) []float32,
	background []float32, best func([]float32, string) float32,
	pos map[string]string, count int, floorDB, threshold float64) {

	win := m.WindowSamples()
	part := int(5 * m.SampleRate())

	for _, code := range []string{"cry", "bark"} {
		positive := pos[code]
		if positive == "" {
			continue
		}
		var events [][]float32
		for _, c := range clips {
			if c.labels != positive || len(events) >= count {
				continue
			}
			events = append(events, loudestPart(load(c.path), part, m.SampleRate()))
		}
		if len(events) == 0 {
			continue
		}
		fmt.Printf("\n--- %s: %d positives, five seconds on the room ---\n", code, len(events))
		fmt.Printf("%-14s %-10s %10s %8s %s\n",
			"event", "above room", "median", "p10", "at or over "+fmt.Sprintf("%.2f", threshold))

		for _, over := range []float64{30, 24, 18, 12, 9, 6, 3, 0, -3} {
			level := floorDB + over
			var got []float32
			for _, e := range events {
				a := append([]float32(nil), background...)
				scaled := atLevel(e, level)
				off := win/2 - len(scaled)/2
				for i, v := range scaled {
					a[off+i] += v
				}
				sc, err := m.Scores(a)
				die(err)
				got = append(got, best(sc, code))
			}
			slices.Sort(got)
			over20 := 0
			for _, v := range got {
				if float64(v) >= threshold {
					over20++
				}
			}
			fmt.Printf("%-14s %+6.0f dB %10.3f %8.3f %8d/%d (%.0f%%)\n",
				fmt.Sprintf("%.0f dBFS", level), over,
				quantile(got, 50), quantile(got, 10),
				over20, len(got), 100*float64(over20)/float64(len(got)))
		}
	}
}
