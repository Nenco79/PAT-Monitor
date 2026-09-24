// pat-sounds measures the sound recogniser against sets of recordings labelled
// by other people, and from there the classes and the thresholds are chosen.
//
// **It exists because the thresholds are not tuned on the developer's house.**
// A few recordings taken here would give numbers right for this room, this dog
// and this microphone, and wrong for anybody else — with nothing to say so,
// because they would work perfectly well where they were chosen.
//
// The datasets are not in the repository: they weigh twelve gigabytes and
// belong to other people. Where they are obtained, under which licence, and
// what came out of them is in `baselines/sounds.txt`.
//
// Two steps, because the first costs and the second does not:
//
//	pat-sounds score -kind esc50 -dir ...\ESC-50-master -out esc50.csv
//	pat-sounds judge -kind esc50 -scores esc50.csv
//
// **Everything is written down and decided afterwards.** The pass takes from
// three to forty minutes and the questions are many — which subset of classes,
// which threshold, which aggregation — so it is paid for once and answered at
// no cost.
//
// The third step does not go through a CSV because it does not read a dataset
// as it is: it takes the positives and puts them inside a whole window, which
// is the condition the monitor really works in and no dataset reproduces.
//
//	pat-sounds dilute -dir ...\ESC-50-master
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"patmonitor/internal/ced"
	"patmonitor/internal/gguf"
	"patmonitor/internal/resample"
)

// candidateClasses are the AudioSet classes worth looking at.
//
// **The list is deliberately wide.** It exists to discover which ones really
// light up, and narrowing it before looking would mean discarding the answer
// before asking the question. Which survive is in the report: two out of eight
// for the bark, and the reason is that every extra class brings its own false
// positives.
var candidateClasses = []string{
	"Baby cry, infant cry", "Crying, sobbing", "Whimper", "Wail, moan",
	"Babbling", "Children shouting", "Screaming", "Baby laughter",
	"Bark", "Dog", "Bow-wow", "Growling", "Howl", "Yip",
	"Whimper (dog)", "Canidae, dogs, wolves", "Domestic animals, pets",
	"Snoring", "Cat", "Meow", "Speech", "Silence",
}

// chosen is what the monitor looks at today, and the report says why.
var chosen = map[string][]string{
	"bark": {"Bark", "Bow-wow"},
	"cry":  {"Baby cry, infant cry", "Crying, sobbing"},
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "score":
		score(os.Args[2:])
	case "judge":
		judge(os.Args[2:])
	case "dilute":
		dilute(os.Args[2:])
	case "gate":
		gateCmd(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: pat-sounds <score|judge|dilute|gate> [flags]")
	fmt.Fprintln(os.Stderr, "  score -model m.gguf -kind esc50|urbansound8k|fsd50k|donateacry -dir DIR -out scores.csv")
	fmt.Fprintln(os.Stderr, "  judge -kind esc50|urbansound8k|fsd50k|donateacry -scores scores.csv")
	// `dilute` does not take -kind: it needs clean, short positives, and only
	// ESC-50 has them. Writing it in the usage and not accepting it would give
	// whoever copies the line a flag error.
	fmt.Fprintln(os.Stderr, "  dilute -dir ESC-50-DIR        how much a short event loses in a full window")
	fmt.Fprintln(os.Stderr, "  gate -kind KIND -dir DIR      what the shape detector lets through; -sweep tries other values")
	os.Exit(2)
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "pat-sounds:", err)
		os.Exit(1)
	}
}

// --- the expensive step ---

func score(args []string) {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	model := fs.String("model", "internal/ced/ced-tiny-q8_0.gguf", "path of the CED model in GGUF")
	kind := fs.String("kind", "", "esc50, urbansound8k, fsd50k or donateacry")
	dir := fs.String("dir", "", "directory the dataset was unpacked into")
	out := fs.String("out", "scores.csv", "where to write one row per clip")
	workers := fs.Int("workers", 0, "parallel workers (0 = half the cores)")
	_ = fs.Parse(args)
	if *kind == "" || *dir == "" {
		usage()
	}
	clips, err := loadIndex(*kind, *dir)
	die(err)
	raw, err := os.ReadFile(*model)
	die(err)
	n := *workers
	if n <= 0 {
		n = max(1, runtime.NumCPU()/2)
	}
	fmt.Printf("%d clips, %d workers\n", len(clips), n)

	type row struct {
		clip
		seconds float64
		scores  []float32
	}
	jobs := make(chan clip)
	res := make(chan row, 64)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := gguf.Parse(raw)
			die(err)
			m, err := ced.New(g)
			die(err)
			var idx []int
			for _, c := range candidateClasses {
				k, err := m.Index(c)
				die(err)
				idx = append(idx, k)
			}
			// **One resampler per rate, kept.** UrbanSound8K arrives at
			// different rates from clip to clip, and rebuilding the filter bank
			// for each one would cost more than the model.
			rs := map[int]*resample.Rational{}
			for c := range jobs {
				wave, rate, err := loadWAV(c.path)
				if err != nil || len(wave) == 0 {
					fmt.Fprintf(os.Stderr, "skipped %s: %v\n", c.name, err)
					continue
				}
				secs := float64(len(wave)) / float64(rate)
				if rate != m.SampleRate() {
					r := rs[rate]
					if r == nil {
						if r, err = resample.New(rate, m.SampleRate()); err != nil {
							fmt.Fprintf(os.Stderr, "skipped %s: %v\n", c.name, err)
							continue
						}
						rs[rate] = r
					}
					wave = r.Convert(wave)
				}
				sc, err := m.Scores(wave)
				if err != nil {
					fmt.Fprintf(os.Stderr, "skipped %s: %v\n", c.name, err)
					continue
				}
				keep := make([]float32, len(idx))
				for k, i := range idx {
					keep[k] = sc[i]
				}
				res <- row{c, secs, keep}
			}
		}()
	}
	go func() {
		for _, c := range clips {
			jobs <- c
		}
		close(jobs)
		wg.Wait()
		close(res)
	}()

	f, err := os.Create(*out)
	die(err)
	defer f.Close()
	w := csv.NewWriter(f)
	die(w.Write(append([]string{"file", "labels", "seconds", "extra"}, candidateClasses...)))
	done := 0
	for r := range res {
		rec := []string{r.name, r.labels, strconv.FormatFloat(r.seconds, 'f', 2, 64), r.extra}
		for _, v := range r.scores {
			rec = append(rec, strconv.FormatFloat(float64(v), 'f', 4, 32))
		}
		die(w.Write(rec))
		done++
	}
	w.Flush()
	die(w.Error())
	fmt.Printf("%d clips in %v -> %s\n", done, time.Since(start).Round(time.Second), *out)
}

// --- the datasets ---

// clip is a recording to be evaluated. `labels` is the comma-separated list of
// labels — **in FSD50K there is more than one per clip**, and a dog's may not
// carry `Bark`: looking at only one would turn a gap in the annotation into a
// false positive of the model.
type clip struct {
	path, name, labels string
	// extra carries what one dataset knows and the others do not. For
	// UrbanSound8K it is the salience, 1 in the foreground and 2 in the
	// background, which on its own explains half the recall.
	extra string
}

func loadIndex(kind, dir string) ([]clip, error) {
	switch kind {
	case "esc50":
		rows, err := readCSV(filepath.Join(dir, "meta", "esc50.csv"))
		if err != nil {
			return nil, err
		}
		var out []clip
		for _, r := range rows {
			out = append(out, clip{filepath.Join(dir, "audio", r[0]), r[0], r[3], ""})
		}
		return out, nil
	case "urbansound8k":
		rows, err := readCSV(filepath.Join(dir, "metadata", "UrbanSound8K.csv"))
		if err != nil {
			return nil, err
		}
		var out []clip
		for _, r := range rows {
			out = append(out, clip{
				filepath.Join(dir, "audio", "fold"+r[5], r[0]), r[0], r[7], r[4],
			})
		}
		return out, nil
	case "donateacry":
		// **Every clip is a positive and there are no negatives at all**, which
		// is what this corpus is for: the other three bring thousands of
		// negatives and forty cries between them, and what is missing is the
		// other side. Its labels say **why** a baby is crying — hungry, tired,
		// belly pain — and that is exactly the reason it had been passed over
		// here: the labels are useless to us and the audio is not.
		//
		// The reason travels as `extra`, so the report can be broken down by it
		// without this becoming a dataset of five classes: a cry from hunger and
		// a cry from pain are the same event for a monitor.
		var out []clip
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".wav") {
				return err
			}
			out = append(out, clip{
				path, filepath.Base(path), "crying_baby",
				filepath.Base(filepath.Dir(path)),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s holds no .wav: the corpus is the "+
				"`donateacry_corpus_cleaned_and_updated_data` folder", dir)
		}
		return out, nil
	case "fsd50k":
		rows, err := readCSV(filepath.Join(dir, "FSD50K.ground_truth", "eval.csv"))
		if err != nil {
			return nil, err
		}
		var out []clip
		for _, r := range rows {
			out = append(out, clip{
				filepath.Join(dir, "FSD50K.eval_audio", r[0]+".wav"), r[0] + ".wav", r[1], "",
			})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown dataset %q", kind)
}

// positives says which label is the positive of each code, per dataset. The cry
// does not have one everywhere, and then only the false positives remain.
var positives = map[string]map[string]string{
	"esc50":        {"cry": "crying_baby", "bark": "dog"},
	"urbansound8k": {"bark": "dog_bark"},
	"fsd50k":       {"cry": "Crying_and_sobbing", "bark": "Bark"},
	"donateacry":   {"cry": "crying_baby"},
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s: %d rows", path, len(rows))
	}
	return rows[1:], nil
}

// --- the step that costs nothing ---

type scored struct {
	name, labels, extra string
	seconds             float64
	by                  map[string]float32
}

func (s scored) has(label string) bool {
	return slices.Contains(strings.Split(s.labels, ","), label)
}

func (s scored) best(classes []string) float32 {
	var m float32
	for _, c := range classes {
		if s.by[c] > m {
			m = s.by[c]
		}
	}
	return m
}

func judge(args []string) {
	fs := flag.NewFlagSet("judge", flag.ExitOnError)
	kind := fs.String("kind", "", "esc50, urbansound8k, fsd50k or donateacry")
	path := fs.String("scores", "scores.csv", "the CSV that score wrote")
	classes := fs.String("classes", "", "override the classes, as JSON like {\"bark\":[\"Bark\"]}")
	_ = fs.Parse(args)
	if *kind == "" {
		usage()
	}
	// **A dataset we do not know is refused.** Without this, the map of
	// positives answers empty and the report comes out with the air of being
	// fine, declaring that the dataset has no positives — that is, a typo in the
	// name becomes a table of false positives only, which gets read.
	if _, ok := positives[*kind]; !ok {
		die(fmt.Errorf("unknown dataset %q", *kind))
	}
	rows, err := readScores(*path)
	die(err)
	watch := chosen
	if *classes != "" {
		watch = map[string][]string{}
		die(json.Unmarshal([]byte(*classes), &watch))
	}
	fmt.Printf("%d clips from %s\n", len(rows), *path)

	codes := make([]string, 0, len(watch))
	for c := range watch {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, code := range codes {
		pos := positives[*kind][code]
		fmt.Printf("\n=== %s = max(%s) ===\n", code, strings.Join(watch[code], ", "))
		if pos == "" {
			fmt.Printf("this dataset has no positives for %s: only the false ones remain\n", code)
		}
		perClass(rows, pos, candidateClasses, watch[code])
		summary(rows, pos, watch[code])
		whereTheRecallGoes(rows, pos, watch[code])
		byLabel(rows, pos, watch[code])
	}
}

func readScores(path string) ([]scored, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	all, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	// **An empty file is the normal case, not a strange one**: `score` lasts up
	// to forty minutes, and interrupting it leaves a CSV with the header alone
	// or without even that. Without this line `judge` panics on `all[0]` — a
	// usage error presenting itself as a fault in the program.
	if len(all) < 2 {
		return nil, fmt.Errorf("%s holds %d rows: run score first, or it was interrupted", path, len(all))
	}
	head := all[0]
	out := make([]scored, 0, len(all)-1)
	for _, r := range all[1:] {
		s := scored{name: r[0], labels: r[1], extra: r[3], by: map[string]float32{}}
		s.seconds, _ = strconv.ParseFloat(r[2], 64)
		for i := 4; i < len(r) && i < len(head); i++ {
			v, _ := strconv.ParseFloat(r[i], 32)
			s.by[head[i]] = float32(v)
		}
		out = append(out, s)
	}
	return out, nil
}

// perClass is the question to ask first: **a list chosen at a desk can contain a
// class that says nothing on the positives and shouts on the negatives**, and
// then what makes the false alarms is the list, not the model.
func perClass(rows []scored, positive string, all, used []string) {
	if positive == "" {
		return
	}
	inUse := map[string]bool{}
	for _, c := range used {
		inUse[c] = true
	}
	fmt.Printf("%-24s %-4s %10s %8s %9s  %s\n",
		"class", "uses", "pos:median", "pos:p10", "neg:max", "what causes it")
	for _, c := range all {
		var pos, neg []float32
		worst, worstCat := float32(0), ""
		for _, r := range rows {
			v := r.by[c]
			if r.has(positive) {
				pos = append(pos, v)
			} else {
				neg = append(neg, v)
				if v > worst {
					worst, worstCat = v, r.labels
				}
			}
		}
		if len(pos) == 0 {
			continue
		}
		slices.Sort(pos)
		mark := "  "
		if inUse[c] {
			mark = "->"
		}
		fmt.Printf("%-24s %-4s %10.3f %8.3f %9.3f  %s\n",
			c, mark, quantile(pos, 50), quantile(pos, 10), worst, short(worstCat))
	}
}

func summary(rows []scored, positive string, classes []string) {
	var pos, neg []float32
	worst, worstCat := float32(0), ""
	for _, r := range rows {
		v := r.best(classes)
		if positive != "" && r.has(positive) {
			pos = append(pos, v)
		} else {
			neg = append(neg, v)
			if v > worst {
				worst, worstCat = v, r.labels
			}
		}
	}
	slices.Sort(pos)
	slices.Sort(neg)
	fmt.Printf("\npositives n=%d", len(pos))
	if len(pos) > 0 {
		fmt.Printf("  min %.3f  p10 %.3f  median %.3f", pos[0], quantile(pos, 10), quantile(pos, 50))
	}
	fmt.Printf("\nnegatives n=%d  p999 %.3f  max %.3f (%s)\n",
		len(neg), quantile(neg, 999), worst, short(worstCat))
	fmt.Printf("%8s %12s %12s\n", "threshold", "recall", "false")
	for _, th := range []float32{0.10, 0.15, 0.20, 0.30, 0.50} {
		tp, fp := 0, 0
		for _, v := range pos {
			if v >= th {
				tp++
			}
		}
		for _, v := range neg {
			if v >= th {
				fp++
			}
		}
		rec := "—"
		if len(pos) > 0 {
			rec = fmt.Sprintf("%d/%d (%d%%)", tp, len(pos), 100*tp/len(pos))
		}
		fmt.Printf("%8.2f %12s %8d/%d\n", th, rec, fp, len(neg))
	}
}

// whereTheRecallGoes splits the positives by the two things that explain a low
// recall without blaming the model.
//
// **A low recall is not a verdict until one knows what it is on.** The first is
// how far in the foreground the sound is: UrbanSound8K annotates it clip by
// clip, and that column alone takes the bark from 82% to 36% — a dog three
// blocks away covered by traffic is a clip legitimately labelled `dog_bark` and
// is not what a baby monitor has to catch. The second is duration: above the
// model's window — 10.11 s — **we** are the ones splitting and averaging, so a
// short event inside a long clip is diluted by a rule of ours.
func whereTheRecallGoes(rows []scored, positive string, classes []string) {
	if positive == "" {
		return
	}
	bySalience := map[string][]float32{}
	byLength := map[string][]float32{}
	for _, r := range rows {
		if !r.has(positive) {
			continue
		}
		v := r.best(classes)
		if r.extra != "" {
			bySalience["salience "+r.extra] = append(bySalience["salience "+r.extra], v)
		}
		byLength[lengthBucket(r.seconds)] = append(byLength[lengthBucket(r.seconds)], v)
	}
	show := func(title string, m map[string][]float32) {
		if len(m) < 2 {
			return // a single row explains nothing
		}
		fmt.Printf("\n%s\n%-22s %6s %9s %8s %8s\n", title, "", "n", "median", "@0.20", "@0.30")
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := m[k]
			slices.Sort(v)
			pct := func(th float32) string {
				n := 0
				for _, x := range v {
					if x >= th {
						n++
					}
				}
				return fmt.Sprintf("%d%%", 100*n/len(v))
			}
			fmt.Printf("%-22s %6d %9.3f %8s %8s\n", k, len(v), quantile(v, 50), pct(0.20), pct(0.30))
		}
	}
	show("recall by salience (1 foreground, 2 background)", bySalience)
	show("recall by clip length", byLength)
}

// lengthBucket separates the clips **around the model's window**, which is the
// boundary where we are the ones doing the splitting.
func lengthBucket(s float64) string {
	switch {
	case s < 1:
		return "under 1 s"
	case s < 2:
		return "1-2 s"
	case s < 5:
		return "2-5 s"
	case s <= 10.11:
		return "5 s - the window"
	default:
		return "over the window"
	}
}

// byLabel lists where the false positives concentrate, and **it is the half the
// totals hide**: five false positives scattered over fifty classes and five all
// on "snoring" are the same number and two different pieces of news, because
// snoring is a bedroom sound.
func byLabel(rows []scored, positive string, classes []string) {
	byCat := map[string][]float32{}
	for _, r := range rows {
		if positive != "" && r.has(positive) {
			continue
		}
		byCat[r.labels] = append(byCat[r.labels], r.best(classes))
	}
	type entry struct {
		cat   string
		n     int
		p50   float32
		max   float32
		over  int
		extra string
	}
	var out []entry
	for cat, vs := range byCat {
		slices.Sort(vs)
		over := 0
		for _, v := range vs {
			if v >= 0.2 {
				over++
			}
		}
		out = append(out, entry{cat: cat, n: len(vs), p50: quantile(vs, 50), max: vs[len(vs)-1], over: over})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].max > out[b].max })
	fmt.Printf("\nwhere the false positives are (the eight categories that light up most)\n")
	fmt.Printf("%-46s %6s %9s %8s %8s\n", "category", "n", "median", "max", ">=0.20")
	for i := 0; i < 8 && i < len(out); i++ {
		fmt.Printf("%-46s %6d %9.3f %8.3f %8d\n",
			short(out[i].cat), out[i].n, out[i].p50, out[i].max, out[i].over)
	}
}

func quantile(s []float32, perMille int) float32 {
	if len(s) == 0 {
		return 0
	}
	i := len(s) * perMille / 1000
	if perMille <= 100 {
		i = len(s) * perMille / 100
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

func short(s string) string {
	if len(s) <= 44 {
		return s
	}
	return s[:41] + "..."
}
