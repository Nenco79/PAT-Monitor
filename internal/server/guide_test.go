package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"patmonitor/internal/i18n"
)

// The notifications guide lives in `docs/`, one page per language, and it
// **quotes the interface**: "press Details, then Receive". A label renamed in
// a catalogue leaves the guide telling somebody to press a word the page no
// longer has, and nothing about either file says so. So every word the guide
// quotes carries the key it quotes (`data-key`), and these tests hold the two
// together — the languages read from the catalogues, the keys from the English
// page, nothing listed here.

const guideURL = "https://nenco79.github.io/PAT-Monitor/"

// guide is one language's page, parsed.
type guide struct {
	file  string
	lang  string            // the <html lang>
	quote map[string]string // data-key -> the text it quotes, one space between words
	keys  []string          // every data-key, in order, repeats included
	shape map[string]int    // how many of each structural element
	links []string          // hrefs of the language bar
}

var accelerator = regexp.MustCompile(`\(&.\)|&`)

func readGuide(t *testing.T, file string) (guide, bool) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "docs", file))
	if err != nil {
		t.Errorf("%s: %v", file, err)
		return guide{}, false
	}
	defer f.Close()
	doc, err := html.Parse(f)
	if err != nil {
		t.Errorf("%s: %v", file, err)
		return guide{}, false
	}
	g := guide{file: file, quote: map[string]string{}, shape: map[string]int{}}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(text(c))
		}
		return b.String()
	}
	attr := func(n *html.Node, k string) (string, bool) {
		for _, a := range n.Attr {
			if a.Key == k {
				return a.Val, true
			}
		}
		return "", false
	}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inNav bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "html":
				g.lang, _ = attr(n, "lang")
			case "h2", "h3", "ol", "ul", "li", "code", "a", "p":
				if !inNav {
					g.shape[n.Data]++
				}
			case "nav":
				inNav = true
			}
			if inNav && n.Data == "a" {
				h, _ := attr(n, "href")
				g.links = append(g.links, h)
			}
			if k, ok := attr(n, "data-key"); ok {
				g.keys = append(g.keys, k)
				g.quote[k] = strings.Join(strings.Fields(text(n)), " ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inNav)
		}
	}
	walk(doc, false)
	return g, true
}

// guideFile is the page a catalogue's `viewer.push.guide-href` points at.
func guideFile(t *testing.T, lang string, cat map[string]any) string {
	t.Helper()
	href, _ := cat["viewer.push.guide-href"].(string)
	file, ok := strings.CutPrefix(href, guideURL)
	if !ok || strings.Contains(file, "/") || !strings.HasSuffix(file, ".html") {
		t.Fatalf("%s: viewer.push.guide-href is %q, not a page of %s", lang, href, guideURL)
	}
	return file
}

// **Every language has its own guide, in its own language**, and the row's
// link leads there: the page is declared in that language, and a browser
// choosing a catalogue for that declaration would choose this one.
func TestEveryLanguageLinksToAGuideInThatLanguage(t *testing.T) {
	s := serverWithoutPassword(t)
	cats := cataloguesOnDisk(t, s)
	seen := map[string]string{}
	for lang, cat := range cats {
		file := guideFile(t, lang, cat)
		if other, dup := seen[file]; dup {
			t.Errorf("%s and %s link to the same guide %s", lang, other, file)
		}
		seen[file] = lang
		g, ok := readGuide(t, file)
		if !ok {
			continue
		}
		if got := i18n.Open([]string{g.lang}).Language(); got != lang {
			t.Errorf("%s: %s is declared lang=%q, which chooses the %q catalogue", lang, file, g.lang, got)
		}
	}
}

// **Every word the guide quotes is the word the interface shows**, in every
// language, and every guide quotes the same things the English one does.
func TestTheGuideQuotesTheInterfaceItDescribes(t *testing.T) {
	s := serverWithoutPassword(t)
	cats := cataloguesOnDisk(t, s)
	base, ok := readGuide(t, guideFile(t, "en", cats["en"]))
	if !ok {
		t.FailNow()
	}
	if len(base.keys) < 8 {
		t.Fatalf("%d quotes in the English guide: this guard is reading nothing", len(base.keys))
	}
	for lang, cat := range cats {
		g, ok := readGuide(t, guideFile(t, lang, cat))
		if !ok {
			continue
		}
		if strings.Join(g.keys, " ") != strings.Join(base.keys, " ") {
			t.Errorf("%s: %s quotes %v, the English guide %v", lang, g.file, g.keys, base.keys)
		}
		for k, got := range g.quote {
			want, _ := cat[k].(string)
			want = strings.Join(strings.Fields(accelerator.ReplaceAllString(want, "")), " ")
			if want == "" || got != want {
				t.Errorf("%s: %s quotes %s as %q, the interface says %q", lang, g.file, k, got, want)
			}
		}
	}
}

// **A translation keeps the sections and the steps**: the same headings, list
// items and code, so a step dropped in one language shows as a count that
// differs from the English. And the language bar lists every guide, once.
func TestEveryGuideHasTheEnglishShapeAndEveryLanguage(t *testing.T) {
	s := serverWithoutPassword(t)
	cats := cataloguesOnDisk(t, s)
	var files []string
	for lang, cat := range cats {
		files = append(files, guideFile(t, lang, cat))
	}
	sort.Strings(files)
	base, ok := readGuide(t, guideFile(t, "en", cats["en"]))
	if !ok {
		t.FailNow()
	}
	for lang, cat := range cats {
		g, ok := readGuide(t, guideFile(t, lang, cat))
		if !ok {
			continue
		}
		for _, el := range []string{"h2", "h3", "ol", "ul", "li", "code", "p", "a"} {
			if g.shape[el] != base.shape[el] {
				t.Errorf("%s: %s has %d <%s>, the English guide %d", lang, g.file, g.shape[el], el, base.shape[el])
			}
		}
		links := append([]string{}, g.links...)
		links = append(links, g.file) // the current page is named, not linked
		sort.Strings(links)
		if strings.Join(links, " ") != strings.Join(files, " ") {
			t.Errorf("%s: the language bar of %s leads to %v, the guides are %v", lang, g.file, g.links, files)
		}
	}
}
