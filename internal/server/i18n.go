package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"patmonitor/internal/i18n"
)

// The pages' dictionary, chosen from the browser's language.
//
// **The catalogues and the choice of language live in `internal/i18n`**,
// because they are not the pages' alone: the tray asks the same question of
// Windows rather than of a header. What stays here is what is HTTP — the cookie,
// the headers, the shape of the script — and the reason it is done this way.
//
// **The language is decided by whoever is watching, not by the machine hosting
// the monitor.** They are two different devices and often two different people:
// whoever installs is in front of the computer, whoever watches has a phone in
// their hand on the other side of the house. The only declaration that speaks
// for the viewer is the `Accept-Language` of their request.
//
// The dictionary is delivered as a **blocking script** and not as JSON to
// fetch: a catalogue arriving after the first paint makes the page flash before
// filling in, and an inline script is not executed under the CSP (see "SDP
// constraints", the line on `script-src`). It costs a request, paid even by
// whoever has the browser in the same language the catalogue is written in,
// because since the keys live in the markup **the text is no longer in the
// pages**. It is the declared price of this shape.

// askedLanguage is the viewer's explicit choice, if there is one and it is one
// we have.
//
// **It wins over the browser's declaration**, and that is why it exists: that
// declaration is often a choice made once when the system was installed, or not
// made at all, and whoever watches a baby monitor from somebody else's phone has
// to be able to say which language they want.
//
// **The value is validated against the list of catalogues**, and that is not a
// formality: it ends up in `<language>.json`, so a value accepted as it comes
// would be a file path written by whoever visits the page. Going through
// `available` it can only be one of the names in the embedded folder.
func askedLanguage(r *http.Request, available map[string]bool) string {
	c, err := r.Cookie("lang")
	if err != nil {
		return ""
	}
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if available[v] {
		return v
	}
	return ""
}

// serveDictionary delivers `window.__I18N` and `window.__I18N_LANG`.
func (s *Server) serveDictionary(w http.ResponseWriter, r *http.Request) {
	// The explicit choice goes ahead of everything else: it is one preference,
	// so it is put at the head of the list rather than replacing it, and if for
	// some reason that catalogue could not be read we would fall back on what
	// the browser declares.
	preferences := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	if chosen := askedLanguage(r, i18n.Languages()); chosen != "" {
		preferences = append([]string{chosen}, preferences...)
	}
	dictionary := i18n.Open(preferences)

	// **An unreadable catalogue has to be said, with its cause.** The page comes
	// out anyway — in English, or with the keys if even that is missing — and
	// without this line the symptom would be a product speaking the wrong
	// language and nowhere to read why. It is not a fatal error, so it does not
	// stop the response: it is written down and things carry on.
	for _, p := range dictionary.Problems() {
		s.log.Warn("dictionary", "language", dictionary.Requested(), "error", p)
	}

	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// The response depends on the language asked for: without this, an
	// intermediary would serve everybody the catalogue of the first one through.
	// It also depends on the cookie, so it is not cached: it is a file of a few
	// tens of kilobytes served once per page, and a cache handing back the
	// previous language is exactly the defect the selector exists to remove.
	w.Header().Set("Vary", "Accept-Language, Cookie")
	w.Header().Set("Cache-Control", "no-store")

	if !dictionary.Complete() {
		// Without even English there is nothing to fall back on, and the page
		// comes out full of keys. It is declared and an empty dictionary is
		// delivered: the keys show, and that is the right symptom for a fault
		// this serious — better a page saying `setup.submit` than one saying
		// nothing. The cause has already come out among the problems, at warning
		// level; here the tone is raised, because this is the only case with
		// nothing to fall back on.
		s.log.Error("no catalogue at all: the pages will show keys")
		_, _ = w.Write([]byte("window.__I18N={};window.__I18N_LANG=\"en\";\n"))
		return
	}

	// It goes through json.Marshal rather than concatenating the two files: a
	// broken catalogue would become a broken script, and a broken script stops
	// the loading of the ones after it — that is, the page would be left without
	// `app.js`.
	data, err := json.Marshal(dictionary.Entries())
	if err != nil {
		s.log.Error("dictionary not serialisable", "error", err)
		_, _ = w.Write([]byte("window.__I18N={};window.__I18N_LANG=\"en\";\n"))
		return
	}

	list, err := json.Marshal(i18n.Names())
	if err != nil {
		list = []byte("null")
	}

	_, _ = w.Write([]byte("window.__I18N="))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte(";window.__I18N_LANG=" + strconv.Quote(dictionary.Language()) +
		";window.__I18N_LANGS="))
	_, _ = w.Write(list)
	_, _ = w.Write([]byte(";\n"))
}

// phrase translates a key into the language of whoever made the request.
//
// It serves the one case where the server has to write **text** instead of a
// code: a refusal served before any page, where there is no browser to hand the
// translation to. It is still the same rule — the language is decided by whoever
// reads — applied where whoever shows and whoever answers are the same thing.
func (s *Server) phrase(r *http.Request, key string) string {
	preferences := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	if chosen := askedLanguage(r, i18n.Languages()); chosen != "" {
		preferences = append([]string{chosen}, preferences...)
	}
	return i18n.Open(preferences).T(key)
}
