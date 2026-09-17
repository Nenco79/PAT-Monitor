package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The addresses the server composes instead of reading them from a file.
var generated = map[string]bool{
	"/dictionary.js": true,
}

// The asset routes are listed by hand, one at a time, and this test exists
// because that list gets forgotten.
//
// It happened with `icons.css`: the sheet was there, the pages asked for it, the
// route was not. The fault is **silent on both sides** — a 404 on a stylesheet
// does not stop the page loading, and an `<svg>` without its rule does not
// vanish: it draws filled with black, which on the viewer's background is
// invisible. From the browser it looks like missing icons, from the code it
// looks fine.
//
// The test does not check a list somebody has to keep up to date — that would be
// the second list that diverges from the first. It reads the references **from
// the pages themselves**, that is, from the consumer's side: it is the same rule
// as the SPS read from the stream rather than asked of the encoder.
//
// **And it does not look at the status code**, which would be the obvious thing
// and does not work: `GET /` is registered as a fallback route, so an asset with
// no route of its own does not give a 404 — it falls onto the viewer's page and
// answers HTML looking perfectly well. Written that way, the test passed even
// with the route that caused the fault removed. So the mux is asked **which
// pattern matched**: if it is the fallback, that address is served by nobody.
func TestEveryAssetThePagesCiteHasARoute(t *testing.T) {
	s := serverWithoutPassword(t)

	// href="/x.css" and src="/x.js", local only: the absolute ones are somebody
	// else's.
	ref := regexp.MustCompile(`(?:href|src)="(/[^"]+\.(?:css|js))"`)

	seen := map[string][]string{}
	pages, err := fs.Glob(s.assets, "*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no page to examine: %v", err)
	}
	for _, p := range pages {
		b, err := fs.ReadFile(s.assets, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = append(seen[m[1]], p)
		}
	}

	routes := make([]string, 0, len(seen))
	for u := range seen {
		routes = append(routes, u)
	}
	sort.Strings(routes)

	for _, u := range routes {
		// The file has to exist among the embedded ones, except the few the
		// server **composes**: `/dictionary.js` is not a file, it is the
		// catalogue chosen from `Accept-Language`. It still has to have a route
		// of its own, which is the check that follows — the `GET /` fallback
		// would answer HTML to a `<script>`, that is, `icons.css`'s silent fault
		// in another form.
		if !generated[u] {
			if _, err := fs.ReadFile(s.assets, strings.TrimPrefix(u, "/")); err != nil {
				t.Errorf("%s is cited by %s but is not among the embedded assets",
					u, strings.Join(seen[u], ", "))
				continue
			}
		}
		// ...and it has to have a route of **its own**. A 200 is not demanded:
		// `/app.js` wants a session and answers 401, and that is right. What has
		// to hold is that the answer comes from whoever serves that file, not
		// from the fallback.
		r := httptest.NewRequest(http.MethodGet, u, nil)
		r.RemoteAddr = "192.168.1.40:5555"
		_, pattern := s.mux.Handler(r)
		if want := "GET " + u; pattern != want {
			t.Errorf("%s is cited by %s and has no route of its own: %q answers",
				u, strings.Join(seen[u], ", "), pattern)
		}
	}

	// And the opposite direction, which is the one that gets forgotten: an
	// embedded asset no page cites is not dangerous, it is dead weight — the
	// same question `licenses_test.go` asks of the licences, both ways.
	all, _ := fs.Glob(s.assets, "*")
	for _, f := range all {
		e := path.Ext(f)
		if e != ".css" && e != ".js" {
			continue
		}
		if _, ok := seen["/"+f]; !ok {
			t.Errorf("%s is embedded but no page cites it", f)
		}
	}
}
