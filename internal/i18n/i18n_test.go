package i18n

import (
	"encoding/json"
	"io/fs"
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
		check := func(where, s string) {
			if strings.Contains(s, "'") {
				t.Errorf("%s: %s carries a straight apostrophe: %q — the interface "+
					"uses ’ (U+2019), and where it stands in for an accent the fix "+
					"is the accented letter", e.Name(), where, s)
			}
		}
		for key, value := range cat {
			switch v := value.(type) {
			case string:
				check(key, v)
			case map[string]any:
				for form, s := range v {
					if text, ok := s.(string); ok {
						check(key+"#"+form, text)
					}
				}
			}
		}
	}
	// A guard that examined no catalogue would pass in silence.
	if seen < 2 {
		t.Fatalf("%d catalogues read: the test is not looking at them", seen)
	}
}
