package tunnel

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// Tailscale repeats "restart with TS_AUTHKEY set, or go to: …" every five
// seconds for as long as nobody authorises the device, and that wait lasts as
// long as a person takes to read an email. Measured on a test machine: forty
// identical lines in four minutes.
func TestARepeatedLineIsSaidOnce(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	say := sayOnce(log)

	for range 40 {
		say("go to: %s", "https://login.example/a/abc")
	}
	if n := strings.Count(buf.String(), "login.example"); n != 1 {
		t.Errorf("40 repetitions wrote %d lines, 1 is wanted", n)
	}
}

// But the comparison is on the text, not on the format: two lines that differ
// by a value are two pieces of news, and both have to be said. These share one
// call site and one format string, so watching the format would silence them.
func TestTwoLinesFromTheSameFormatAreBothNews(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	say := sayOnce(log)

	say("phase %s", "needs-login")
	say("phase %s", "needs-login")
	say("phase %s", "running")
	out := buf.String()
	if strings.Count(out, "needs-login") != 1 || strings.Count(out, "running") != 1 {
		t.Errorf("the two pieces of news did not come out one apiece:\n%s", out)
	}
}

// And a line that comes back after another one is news again: what is
// suppressed is repeats **in a row**, not the message for ever.
func TestALineThatComesBackIsNewsAgain(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	say := sayOnce(log)

	say("%s", "waiting")
	say("%s", "connected")
	say("%s", "waiting")
	if n := strings.Count(buf.String(), "waiting"); n != 2 {
		t.Errorf("the line that came back was written %d times, 2 are wanted", n)
	}
}
