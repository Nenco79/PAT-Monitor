// Package i18n holds the catalogues and picks the language.
//
// **It lives outside internal/server because there are two readers, and they do
// not speak the same language.** The pages are read by whoever is watching,
// from a phone that may be in another room or another house, and their
// declaration is Accept-Language; the tray is read by whoever is in front of
// the machine, and their declaration is the language of the Windows interface.
// Two audiences, and they may legitimately want two different languages — the
// usual rule, **the language is decided by whoever reads**, applied to two
// readings inside the same process.
//
// What the two share is everything else: the same files, the same fallback
// rule, the same way of choosing among the requested tags. That part lives here
// once, because written twice it would diverge — and the half that diverges
// quietly is always the one nobody looks at.
//
// **English is the fallback language and the others overlay it**, key by key: an
// incomplete catalogue is a mixed page, not a broken one.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"strconv"
	"strings"
)

//go:embed catalogs/*.json
var embedded embed.FS

// FS is the raw catalogues, `<tag>.json` at the root.
//
// It is for whoever has to **look at them as files** rather than consult them:
// the tests that compare keys against pages, and anyone who has to enumerate
// them. Consulting is done with Open.
var FS fs.FS = must(fs.Sub(embedded, "catalogs"))

// Fallback is the language everything falls back to.
const Fallback = "en"

func must(f fs.FS, err error) fs.FS {
	if err != nil {
		panic(err)
	}
	return f
}

// Languages lists the tags a catalogue exists for.
func Languages() map[string]bool { return languages(FS) }

func languages(fsys fs.FS) map[string]bool {
	out := map[string]bool{}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return out
	}
	for _, v := range entries {
		if name, ok := strings.CutSuffix(v.Name(), ".json"); ok {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

// pick takes the first preferred tag a catalogue exists for.
//
// The list arrives **already in order of preference**: from a header sorted by
// q, or from Windows, which hands it over sorted itself. All that happens here
// is looking at who comes first.
//
// The comparison also falls back to the language without its region — whoever
// asks for en-GB gets en if there is nothing better — and **an unknown tag is
// not an error**: it falls back to English, which is the language most likely
// to be understood by someone who asked for something we do not have.
//
// It is not exported, and asking "what language is it?" on its own buys
// nothing: server, tray and main all call Open, which picks and opens in one
// go.
func pick(fsys fs.FS, prefs []string) string {
	available := languages(fsys)
	for _, tag := range prefs {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if available[tag] {
			return tag
		}
		if base, _, ok := strings.Cut(tag, "-"); ok && available[base] {
			return base
		}
	}
	return Fallback
}

// FromAcceptLanguage turns the header into an ordered list of tags.
//
// It is not a complete negotiation and does not need to be: the list is sorted
// by the declared quality and the q=0 entries are dropped, since those mean
// "not this one". **The sort is stable** because at equal q what counts is the
// order the browser wrote them in.
func FromAcceptLanguage(header string) []string {
	type entry struct {
		tag string
		q   float64
	}
	var entries []entry
	for part := range strings.SplitSeq(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		q := 1.0
		for _, c := range fields[1:] {
			c = strings.TrimSpace(c)
			if v, ok := strings.CutPrefix(c, "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		entries = append(entries, entry{tag, q})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].q > entries[j].q })

	out := make([]string, 0, len(entries))
	for _, v := range entries {
		if v.q <= 0 {
			continue
		}
		out = append(out, v.tag)
	}
	return out
}

// Dictionary is a catalogue ready to consult, already overlaid on English.
type Dictionary struct {
	language  string
	requested string
	entries   map[string]any

	// What went wrong opening it.
	//
	// **Not failing does not mean staying quiet.** Open cannot return an error
	// — a menu with no items or a blank page would be worse than an English
	// page — but an unreadable catalogue with no line in the log is a product
	// speaking the wrong language and not saying why. Whoever opens the
	// dictionary reads these and reports them.
	problems []error
}

// Open composes the dictionary for the first preferred language we have.
//
// **English is always loaded, and first**, with the requested language overlaid
// on it: that way an entry missing from a translation falls back to English
// instead of showing the key. The test insists there are no missing entries,
// but the protection has to hold on the day somebody adds a language in a
// hurry.
//
// **It never fails.** With not even English available the result is an empty
// dictionary and the keys show through: that is the right symptom for a fault
// of that seriousness — better a line reading `tray.quit` than a menu with no
// items. Whoever wants to know looks at Complete.
func Open(prefs []string) *Dictionary { return open(FS, prefs) }

func open(fsys fs.FS, prefs []string) *Dictionary {
	chosen := pick(fsys, prefs)
	d := &Dictionary{language: chosen, requested: chosen, entries: map[string]any{}}

	base, err := read(fsys, Fallback)
	if err != nil {
		d.problems = append(d.problems,
			fmt.Errorf("fallback catalogue %q unreadable, the pages will show keys: %w", Fallback, err))
	} else {
		d.entries = base
	}
	if d.language != Fallback {
		over, err := read(fsys, d.language)
		if err != nil {
			d.problems = append(d.problems,
				fmt.Errorf("catalogue %q unreadable, falling back to %q: %w", d.language, Fallback, err))
			d.language = Fallback
		} else {
			maps.Copy(d.entries, over)
		}
	}
	return d
}

// Requested is the language chosen among the preferences, which may not be the
// one in use: the two diverge when the catalogue could not be read.
func (d *Dictionary) Requested() string { return d.requested }

// Problems is what went wrong, to be reported in a log.
//
// Empty is the normal case. Whoever ignores it gets a dictionary that works
// anyway, and that is on purpose — but they also get a broken catalogue that
// tells nobody.
func (d *Dictionary) Problems() []error { return d.problems }

// Language is the tag actually in use, which may not be the one requested.
func (d *Dictionary) Language() string { return d.language }

// Complete says whether there is anything inside: false means not even English
// could be read.
func (d *Dictionary) Complete() bool { return len(d.entries) > 0 }

// Entries is the map to hand to whoever has to serialise it.
func (d *Dictionary) Entries() map[string]any { return d.entries }

// T is the sentence for a key.
//
// **A key that is not there comes back as it is**, rather than becoming an
// empty string: a menu item with no text is a defect nobody can name, while
// `tray.quit` in a menu can be read and searched for.
func (d *Dictionary) T(key string) string {
	if s, ok := d.entries[key].(string); ok {
		return s
	}
	return key
}

// Names is each language's name **written in that language**.
//
// That is how someone recognises their own in a menu when the page is speaking
// another. It lives in the catalogue, under `lang.name`, because it is a
// property of the translation and not a second list to keep aligned: adding a
// language is adding a file. A language without that name is not offered —
// better not to be able to choose it than to see it appear as a code.
func Names() map[string]string { return names(FS) }

func names(fsys fs.FS) map[string]string {
	out := map[string]string{}
	for l := range languages(fsys) {
		cat, err := read(fsys, l)
		if err != nil {
			continue
		}
		if n, _ := cat["lang.name"].(string); n != "" {
			out[l] = n
		}
	}
	return out
}

// read parses one catalogue.
//
// It is re-read from the embedded filesystem every time: it is an embed.FS, so
// it is in memory already and there is nothing to cache — and a cache here
// would be one more copy that can go stale.
func read(fsys fs.FS, language string) (map[string]any, error) {
	data, err := fs.ReadFile(fsys, language+".json")
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
