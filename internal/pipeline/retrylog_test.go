package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// **A camera that stays missing is written once, and what changes is written
// again.** The sequence is the one measured with the webcam disabled: the same
// "camera open: no camera found" every attempt, for as long as the monitor
// runs. Before, every attempt wrote it at Error.
//
// **Verified to catch**: with retryLevel answering Error for a repeat, the
// second step fails.
func TestARepeatedCaptureFailureIsWrittenOnce(t *testing.T) {
	missing := errors.New("camera open: no camera found")
	refused := errors.New("camera open: ActivateObject: HRESULT 0x80070005")

	steps := []struct {
		name   string
		err    error
		opened bool
		want   slog.Level
	}{
		{"the first failure", missing, false, slog.LevelError},
		{"the same again", missing, false, slog.LevelDebug},
		{"and again", missing, false, slog.LevelDebug},
		{"a different failure", refused, false, slog.LevelError},
		{"that one repeated", refused, false, slog.LevelDebug},
		// The camera came open and then failed: the monitor worked, so this is
		// a new episode, and so is the next failure after it.
		{"it opened, then failed", refused, true, slog.LevelError},
		{"the old failure after it", refused, false, slog.LevelError},
		{"and that one repeated", refused, false, slog.LevelDebug},
	}
	last := ""
	for _, s := range steps {
		lvl, sig := retryLevel(last, s.err, s.opened)
		if lvl != s.want {
			t.Errorf("%s: written at %v, wanted %v", s.name, lvl, s.want)
		}
		last = sig
	}
}

// **A demoted warning is dropped by the monitor's normal log and kept by the
// verbose one.** The first half is the fix; the second is what keeps a repeat
// findable by whoever asks for -verbose.
func TestADemotedWarningFollowsTheDebugLevel(t *testing.T) {
	for _, c := range []struct {
		level slog.Level
		kept  bool
	}{
		{slog.LevelInfo, false},
		{slog.LevelDebug, true},
	} {
		var buf bytes.Buffer
		h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: c.level})
		slog.New(demoted{Handler: h}).With("where", "open").
			Warn("cannot read the formats the camera declares", "error", "no camera found")
		out := buf.String()
		if kept := out != ""; kept != c.kept {
			t.Errorf("handler at %v: written=%v, wanted %v (%q)", c.level, kept, c.kept, out)
			continue
		}
		if c.kept && (!strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "where=open")) {
			t.Errorf("handler at %v: %q is not a Debug record carrying its attributes", c.level, out)
		}
	}
}

// **What the open quietened is written again when the camera finally opens.**
// The case is the review's: the webcam comes back and its formats cannot be
// read. On the retry that opens it, the warning is held while demoted, and the
// replay puts it in the normal log at Warn — the one moment it explains why the
// camera was asked for the preset. When the open fails again nothing is
// replayed, and the demoted copy stays out of the normal log.
//
// **Verified to catch**: with Handle not appending to held, the replay writes
// nothing and the check after it fails.
func TestWhatTheOpenQuietenedIsWrittenWhenTheCameraOpens(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	var held []slog.Record
	slog.New(demoted{Handler: h, held: &held}).
		Warn("cannot read the formats the camera declares, asking for the preset as it is",
			"error", "E_FAIL")
	if buf.Len() != 0 {
		t.Fatalf("the demoted warning reached the normal log before the outcome: %q", buf.String())
	}
	replay(context.Background(), h, held)
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "cannot read the formats") ||
		!strings.Contains(out, "error=E_FAIL") {
		t.Errorf("the replay did not write the warning at its own level: %q", out)
	}
}
