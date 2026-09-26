package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// **The two public refusals cannot be used to rotate the log away.**
//
// /api/login and /api/setup turn away a foreign page's post, and /api/setup
// anybody not on this PC, before the limiter is consulted: nothing slows the
// caller down, and each refusal used to be a Warn line carrying the caller's
// headers whole. Five hundred posts with a 64 KB header were the 32 MB the log
// keeps.
//
// **The defect was put back and this test fails with it**: with the plain
// s.log.Warn restored in refuseCredentials and in apiSetup, 500 posts write 500
// lines each, and with forLog removed the one line left is 60 KB long.
func TestAFloodOfPublicRefusalsWritesOneShortLine(t *testing.T) {
	padding := strings.Repeat("x", 60<<10)

	cases := []struct {
		name  string
		path  string
		shape func(r *http.Request)
		what  string
	}{
		{"a foreign page posting a password", "/api/login", func(r *http.Request) {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Origin", "https://"+padding+".example")
		}, "credentials refused"},
		{"a setup from somewhere else", "/api/setup", func(r *http.Request) {
			r.RemoteAddr = "192.0.2.7:4000"
			r.Host = padding
		}, "first-time setup refused"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, buf := serverWithLog(t)
			for range 500 {
				r := formPost(c.path, map[string]string{"password": "p", "confirm": "p"})
				c.shape(r)
				s.Handler().ServeHTTP(httptest.NewRecorder(), r)
			}

			out := buf.String()
			if n := strings.Count(out, c.what); n != 1 {
				t.Errorf("500 refusals wrote %d lines, 1 is wanted", n)
			}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if len(line) > 1024 {
					t.Errorf("a line of %d bytes reached the log: the caller's header went in whole", len(line))
				}
			}
		})
	}
}

// The run is closed on silence, and the closing line carries the count: the
// first line said that it started, this one says how big it was.
func TestARunOfRefusalsIsSummedUpWhenItGoesQuiet(t *testing.T) {
	s, buf := serverWithLog(t)
	q := newRefusalRun(s.log, "something refused")
	q.quiet = 20 * time.Millisecond

	for range 7 {
		q.refuse("from", "192.0.2.7")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "the run is over") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "refused=7") {
		t.Fatalf("the run was not summed up:\n%s", buf.String())
	}

	// And the next refusal starts a new run, loudly.
	buf.Reset()
	q.refuse("from", "192.0.2.7")
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("after a quiet spell the next refusal was not announced:\n%s", buf.String())
	}
}

func TestForLogCutsOnACharacterAndSaysHowMuch(t *testing.T) {
	if got := forLog("short"); got != "short" {
		t.Errorf("a short value was changed: %q", got)
	}
	long := strings.Repeat("è", 200) // two bytes each, so the cut lands mid-character
	got := forLog(long)
	if !utf8.ValidString(got) {
		t.Errorf("the cut split a character: %q", got)
	}
	if !strings.Contains(got, "bytes)") || len(got) > maxLogValue+32 {
		t.Errorf("the cut did not say what it left out, or left too much: %d bytes", len(got))
	}
}
