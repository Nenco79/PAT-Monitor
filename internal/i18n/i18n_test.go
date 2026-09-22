package i18n

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// The made-up catalogues are there to test things the two real ones cannot
// test: a language we do not have, and a half-finished translation. With `en`
// and `it` alone, half of these cases could not be written.
func threeLanguages() fstest.MapFS {
	return fstest.MapFS{
		"en.json": {Data: []byte(`{"lang.name":"English","a":"A","b":"B"}`)},
		"it.json": {Data: []byte(`{"lang.name":"Italiano","a":"A in italiano","b":"B in italiano"}`)},
		"de.json": {Data: []byte(`{"lang.name":"Deutsch","a":"A auf Deutsch","b":"B auf Deutsch"}`)},
	}
}

func TestTheDeclaredOrderPicksTheLanguage(t *testing.T) {
	fsys := threeLanguages()

	cases := []struct {
		header string
		want   string
		why    string
	}{
		{"", "en", "with no header we stay in the fallback language"},
		{"it-IT,it;q=0.9", "it", "an Italian browser gets the Italian catalogue"},
		{"en-GB,en;q=0.9", "en", "the region falls back to the language"},
		{"fr-FR,fr;q=0.9", "en", "a language we do not have falls back to English"},
		{"fr,de;q=0.8", "de", "the first one we have wins, not the first one asked for"},
		{"de;q=0.5,it;q=0.9", "it", "the declared quality beats the order"},
		{"it;q=0, de", "de", "q=0 means \"not this one\""},
		{"IT-it", "it", "the comparison ignores case"},
	}
	for _, c := range cases {
		if got := pick(fsys, FromAcceptLanguage(c.header)); got != c.want {
			t.Errorf("Accept-Language %q -> %q, wanted %q (%s)",
				c.header, got, c.want, c.why)
		}
	}
}

// **The same function serves the tray**, which has no header and receives an
// already sorted list from Windows: that is why the choice takes a list instead
// of a string.
func TestTheSameChoiceServesAListThatNeverWasAHeader(t *testing.T) {
	fsys := threeLanguages()

	if got := pick(fsys, []string{"de-AT", "en-US"}); got != "de" {
		t.Errorf("Windows list -> %q, wanted \"de\"", got)
	}
	if got := pick(fsys, nil); got != Fallback {
		t.Errorf("no preference -> %q, wanted the fallback", got)
	}
}

// An incomplete translation has to give a **mixed** page, not a page showing
// keys. The test on the real catalogues insists there are no missing entries;
// this is the protection for the day somebody adds a language in a hurry, and
// it is the only place it can really be tested, because there is no incomplete
// language in the repository.
func TestAPartialTranslationFallsBackToTheReserve(t *testing.T) {
	fsys := threeLanguages()
	fsys["it.json"] = &fstest.MapFile{Data: []byte(`{"lang.name":"Italiano","a":"A in italiano"}`)}

	d := open(fsys, []string{"it"})
	if d.Language() != "it" {
		t.Fatalf("language = %q, wanted \"it\"", d.Language())
	}
	if got := d.T("a"); got != "A in italiano" {
		t.Errorf("the translated entry = %q", got)
	}
	if got := d.T("b"); got != "B" {
		t.Errorf("the missing entry = %q, wanted the English one", got)
	}
}

// A language whose catalogue cannot be read **is not that language**: the
// fallback takes over and it is declared, otherwise the page would claim to
// speak Italian while showing English sentences.
func TestAnUnreadableCatalogueIsNotTheLanguageItClaimed(t *testing.T) {
	fsys := threeLanguages()
	fsys["it.json"] = &fstest.MapFile{Data: []byte(`{ this is not JSON`)}

	d := open(fsys, []string{"it"})
	if d.Language() != Fallback {
		t.Errorf("language = %q, wanted the fallback", d.Language())
	}
	if got := d.T("a"); got != "A" {
		t.Errorf("entry = %q, wanted the English one", got)
	}

	// **And it does not do it silently.** Falling back to English without
	// saying so would give a product speaking the wrong language and nowhere to
	// read why: the cause comes out of here, and whoever opens the dictionary
	// reports it.
	if len(d.Problems()) == 0 {
		t.Error("no problem reported: the fallback would be mute")
	}
	if d.Requested() != "it" {
		t.Errorf("requested = %q: without the requested language the log does not say which catalogue is broken", d.Requested())
	}
}

// Without even the fallback nothing is faked: the keys show through, and that
// is the right symptom for a fault of that seriousness.
func TestWithoutTheReserveTheKeysShow(t *testing.T) {
	d := open(fstest.MapFS{}, []string{"it"})
	if d.Complete() {
		t.Error("a dictionary with no catalogues is not complete")
	}
	if got := d.T("tray.quit"); got != "tray.quit" {
		t.Errorf("missing key = %q, wanted the key itself", got)
	}
	if len(d.Problems()) == 0 {
		t.Error("no problem reported: the keys would appear with no explanation")
	}
}

// A language with no `lang.name` is not offered: better not to be able to
// choose it than to see it appear in the menu as a code.
func TestALanguageWithoutItsOwnNameIsNotOffered(t *testing.T) {
	fsys := threeLanguages()
	fsys["de.json"] = &fstest.MapFile{Data: []byte(`{"a":"A auf Deutsch"}`)}

	n := names(fsys)
	if n["it"] != "Italiano" {
		t.Errorf("Italian name = %q", n["it"])
	}
	if _, there := n["de"]; there {
		t.Error("a language without lang.name must not appear in the list")
	}
}

// **One apostrophe for the whole interface, and it is the typographic one.**
//
// Nothing breaks with the straight mark: what breaks is a sentence carrying
// both, and Italian puts an apostrophe in every other word — elision there is
// grammar, not a flourish. The catalogues held the two together, forty values
// against nineteen in Italian alone, and nobody had chosen that: it is
// whichever keyboard was in front of whoever added the entry.
//
// **The same pass found what the mixture was hiding.** Two entries wrote "e"
// and "puo" with an apostrophe where the word wants è and può — a typewriter
// standing in for an accent, which is not a matter of style but a spelling
// mistake — and they were the two added through a tool that could not reach
// the accented keys. In a file that already spells one thing two ways, a third
// way looks like the second.
//
// It reads the embedded catalogues instead of a list of languages, so one
// added tomorrow is covered with nothing to remember.
func TestTheInterfaceHasOneApostrophe(t *testing.T) {
	eachCatalogueValue(t, func(file, where, s string) {
		if strings.Contains(s, "'") {
			t.Errorf("%s: %s carries a straight apostrophe: %q — the interface "+
				"uses ’ (U+2019), and where it stands in for an accent the fix "+
				"is the accented letter", file, where, s)
		}
	})
}

// **French puts a space before `:` `;` `?` and `!`, and that space must not be
// breakable.** A breakable one is worse than none: a line break lands between
// the word and the mark, and the mark begins the next line, which is the one
// thing the language will not have. Nothing else here can see it — the
// catalogue parses either way, the page renders, and the difference shows only
// the day a line happens to break there. Fifty-seven values were written with
// an ordinary space before one of those four marks, in a file whose apostrophes
// say the typography was a decision and not an accident.
//
// The space is written `\u00a0` in the file rather than as the character, so
// that whoever reads a diff can see it: on screen and in a patch a
// non-breaking space and an ordinary one look the same.
//
// It reads the embedded catalogues instead of a list of languages, so one
// added tomorrow is covered with nothing to remember.
//
// **What it cannot see is a French `:` written with no space at all**, and that
// is argued rather than overlooked: a URL carries one, a clock carries one, and
// a rule that special-cased them would be a rule made of its exceptions. That
// one is left to a reader of French, and this half is not.
func TestASpaceBeforePunctuationIsNotBreakable(t *testing.T) {
	eachCatalogueValue(t, func(file, where, s string) {
		// Byte-wise is safe here: a space and the four marks are ASCII, so none
		// of them can be a continuation byte of something else.
		for i := 0; i+1 < len(s); i++ {
			if s[i] == ' ' && strings.IndexByte(";:?!", s[i+1]) >= 0 {
				t.Errorf("%s: %s has a breakable space before %q: %q — French "+
					"writes that space as a non-breaking one (U+00A0, `\\u00a0` in "+
					"the file) and the other languages write no space at all",
					file, where, string(s[i+1]), s)
			}
		}
	})
}

// **The non-breaking space is written as an escape, and that is a property of
// the file rather than of the value.**
//
// The guard above insists French puts an unbreakable space before its four
// marks; the convention beside it — declared where that space was introduced —
// is that it is spelled ` ` in the JSON and never typed as the character,
// because on screen and in a patch the two spaces are the same width, so a diff
// cannot show which one arrived. Every reader of these files parses the escape,
// so **by the time a value has been read the two forms are indistinguishable**:
// this is the one guard here that has to look at the bytes.
//
// It caught itself at once. The Chinese catalogue was written through a tool
// whose own argument is JSON, so the six characters ` ` were decoded on the
// way in and two literal ones reached the file — in `clips.note` and in
// `tray.line.uptime`, that is, in exactly the two entries the other four
// catalogues carry the escape in.
//
// **Verified to catch**: with either one typed as the character, this fails
// naming the file and the line.
func TestTheNonBreakingSpaceIsWrittenAsAnEscape(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		seen++
		for n, line := range strings.Split(string(raw), "\n") {
			if strings.ContainsRune(line, ' ') {
				t.Errorf("%s:%d carries a literal non-breaking space: it is "+
					"written `\\u00a0` so that a diff can show it, and nothing "+
					"downstream can tell the two apart: %q", e.Name(), n+1, line)
			}
		}
	}
	// A walk that read nothing passes in silence, which is the failure this
	// file's guards can least afford.
	if seen < 2 {
		t.Fatalf("%d catalogues read: the test is not looking at them", seen)
	}
}

// **A lost placeholder is a sentence with a hole in it, and nothing else looks
// at it.** The key exists, the entry exists, the page renders — and the reader
// is shown `{n}` where a number should be. It is the family of the code with no
// word in the catalogue: the shape is right and the content is not, so every
// guard that compares shapes is green.
//
// It compares the placeholders and not the text, because that is the freedom a
// translation has: a language may say it in another order, or with another verb,
// and may not lose a hole or invent one. The comparison is key by key and form
// by form — `clips.note#one` against `clips.note#one` — because a plural form is
// a sentence of its own: the English `about a minute` has no hole at all while
// its `other` has one, and that difference is the base's, not a translation's.
//
// The holes are sorted before being compared, since the order they are written
// in is the sentence's business, and counted, since losing one of two is the
// same defect as losing the only one.
//
// It reads the embedded catalogues instead of a list of languages, so one added
// tomorrow is covered with nothing to remember.
func TestEveryTranslationKeepsThePlaceholdersOfTheBase(t *testing.T) {
	english := map[string]string{}
	eachCatalogueValue(t, func(file, where, s string) {
		if file == Fallback+".json" {
			english[where] = s
		}
	})

	compared := 0
	eachCatalogueValue(t, func(file, where, s string) {
		if file == Fallback+".json" {
			return
		}
		base, there := english[where]
		if !there {
			return // an entry the base does not have: that is the orphan guard's question
		}
		compared++
		want, got := placeholders(base), placeholders(s)
		if want != got {
			t.Errorf("%s: %s has the placeholders %v where the base has %v: %q against %q",
				file, where, got, want, s, base)
		}
	})
	// A guard that compared no translation would pass in silence.
	if compared < 2 {
		t.Fatalf("%d entries compared: the test is not looking at them", compared)
	}
}

// placeholders is the holes a sentence has, in a comparable shape: sorted and
// joined, because the order they are written in is the sentence's business and
// two of them are still two.
func placeholders(s string) string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		if end := strings.IndexByte(s[i:], '}'); end > 1 {
			out = append(out, s[i:i+end+1])
			i += end
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// eachCatalogueValue walks every value of every embedded catalogue, whatever
// shape it is written in: a plain string, or a map of plural forms.
//
// It is one walk and not two because the guards that read the catalogues
// disagree only in what they ask of a value, **and the part they would
// otherwise write twice is the count at the end** — the one that says the walk
// saw something. A guard that examined nothing passes in silence, which is the
// failure it can least afford.
func eachCatalogueValue(t *testing.T, visit func(file, where, value string)) {
	t.Helper()
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var cat map[string]any
		if err := json.Unmarshal(raw, &cat); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		seen++
		for key, value := range cat {
			switch v := value.(type) {
			case string:
				visit(e.Name(), key, v)
			case map[string]any:
				for form, s := range v {
					if text, ok := s.(string); ok {
						visit(e.Name(), key+"#"+form, text)
					}
				}
			}
		}
	}
	if seen < 2 {
		t.Fatalf("%d catalogues read: the test is not looking at them", seen)
	}
}
