package server

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzAnOriginAndAHost gives checkOrigin headers no browser sends.
//
// **Both headers are the visitor's to write.** The property is the one the
// cross-site refusal rests on: an Origin is let through only when its host is
// the request's host. And whatever the two say, the error that goes to the log
// carries them cut to size, and still valid UTF-8 if they were.
func FuzzAnOriginAndAHost(f *testing.F) {
	f.Add("https://patmon-1a2b3c.quercia-lieve.ts.net", "patmon-1a2b3c.quercia-lieve.ts.net")
	f.Add("https://evil.example", "patmon-1a2b3c.quercia-lieve.ts.net")
	f.Add("null", "localhost:8080")
	f.Add("http://localhost:8080@evil.example", "localhost:8080")
	f.Add("http://LOCALHOST:8080", "localhost:8080")
	f.Add("://", "")
	f.Add(strings.Repeat("é", 200), strings.Repeat("h", 300))
	f.Add("https://"+strings.Repeat("o", 4000), strings.Repeat("h", 4000))

	f.Fuzz(func(t *testing.T, origin, host string) {
		r := httptest.NewRequest("GET", "http://placeholder/", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		err := checkOrigin(r)
		if err == nil {
			if origin == "" {
				return
			}
			u, perr := url.Parse(origin)
			if perr != nil || !strings.EqualFold(u.Host, host) {
				t.Fatalf("origin %q let through for host %q", origin, host)
			}
			return
		}
		// %q writes an invalid byte as four characters, so the bound is four
		// times two values cut by forLog, with the error's own words around them.
		msg := err.Error()
		if len(msg) > 4*2*(maxLogValue+32)+100 {
			t.Fatalf("a %d byte error from a %d and a %d byte header", len(msg), len(origin), len(host))
		}
		if utf8.ValidString(origin) && utf8.ValidString(host) && !utf8.ValidString(msg) {
			t.Fatalf("valid headers made an invalid log line: %q", msg)
		}
	})
}
