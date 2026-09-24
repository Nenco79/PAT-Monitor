package server

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"patmonitor/internal/config"
)

// The log must not be fillable by whoever is on the other side.
//
// A viewer that cannot connect retries, and how often is decided by their
// browser. On a test machine with a broken camera that produced over a thousand
// identical lines in twenty minutes, one a second, covering the four lines that
// said what had happened.
func serverWithLog(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	s, err := New(Options{
		Config: storeFor(t, config.Default()),
		// Info level: it is the one the monitor runs at when nobody has asked
		// for debug, that is, the configuration in which the fault was
		// observed.
		Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, &buf
}

func TestARefusedViewerIsLoggedOnceNoMatterHowOftenItRetries(t *testing.T) {
	s, buf := serverWithLog(t)
	from := origin{Kind: "local network", Addr: "192.0.2.7"}

	for range 500 {
		s.refuseViewer(from, errors.New("no keyframe received yet"))
	}

	if n := strings.Count(buf.String(), "viewer refused"); n != 1 {
		t.Errorf("500 attempts wrote %d lines, 1 is wanted", n)
	}
	// The first line has to carry the reason: it is the only one that will be
	// seen.
	if !strings.Contains(buf.String(), "no keyframe received yet") {
		t.Error("the line that comes out does not say why")
	}
}

// The run closes when somebody gets in, and that is where how long it lasted is
// said: an alert that never clears is not an alert, it is noise that stays.
func TestTheRunOfRefusalsIsReportedWhenItEnds(t *testing.T) {
	s, buf := serverWithLog(t)
	from := origin{Kind: "local network", Addr: "192.0.2.7"}

	for range 42 {
		s.refuseViewer(from, errors.New("no keyframe received yet"))
	}
	s.viewerAdmitted()

	out := buf.String()
	if !strings.Contains(out, "viewers admitted again") {
		t.Fatal("the return was not written")
	}
	if !strings.Contains(out, "refused=42") {
		t.Errorf("the count was lost:\n%s", out)
	}

	// And a new run starts over, otherwise the second fault of the night would
	// leave no trace at all.
	buf.Reset()
	s.refuseViewer(from, errors.New("no keyframe received yet"))
	if !strings.Contains(buf.String(), "viewer refused") {
		t.Error("after a return the next refusal was not announced")
	}
}

// Somebody getting in with nobody having been turned away must not produce a
// line: "it works" is not news, and it is the night page's rule.
func TestAdmittingAViewerIsSilentWhenNothingWasRefused(t *testing.T) {
	s, buf := serverWithLog(t)
	s.viewerAdmitted()
	if buf.Len() != 0 {
		t.Errorf("a normal admission wrote:\n%s", buf.String())
	}
}
