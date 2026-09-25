package pipeline

import (
	"context"
	"log/slog"
)

// retryLevel decides how loudly a failed capture attempt is written, and what
// the next attempt compares against.
//
// **A failure is news the first time, and a repeat is not.** The rule is the
// audio supervisor's: the same error again goes to Debug, which the log does
// not write at its normal level, and anything new goes to Error. "New" is a
// different error, or an attempt in which the camera came open, which means the
// monitor worked for a while and whatever failed afterwards is a new episode —
// so opened hands back an empty signature and the next failure is written in
// full.
//
// It is a function of its three inputs so that the rule can be tested without a
// camera: Run holds the signature and asks.
func retryLevel(last string, err error, opened bool) (slog.Level, string) {
	if err == nil {
		// "Capture ended on its own" has a line of its own, and an attempt
		// that ended without an error says nothing about the next one.
		return slog.LevelWarn, ""
	}
	sig := err.Error()
	if opened {
		// The camera came open during this attempt, so the failure is a new
		// episode; and the next failure after it is written in full too.
		return slog.LevelError, ""
	}
	if sig == last {
		return slog.LevelDebug, sig
	}
	return slog.LevelError, sig
}

// demoted writes every record at Debug, whatever level it was logged at.
//
// It exists so that the two warnings the camera open writes — the list
// answering nothing usable, the formats not readable — can be quietened on a
// retry without their functions learning about retries: they take a logger and
// are tested with one, and the decision stays in the one place that knows the
// attempt before failed. See retryLevel.
//
// **Enabled answers yes whenever it is holding**, and otherwise asks the wrapped
// handler about Debug rather than about the level the record was logged at: a
// handler set to Info must drop a demoted Warn, and one set to Debug (the
// -verbose monitor) must still write it. A record has to reach Handle to be
// held, whatever the handler would write.
//
// **held collects every record at its original level**, for replay: on the
// attempt where the camera finally opens, what was quietened was news. Only the
// root holds. A logger derived with With or WithGroup carries attributes the
// replay could not give back, so what goes through it is demoted and not held —
// the two callers log plainly, and a record that cannot be replayed faithfully
// is better not replayed than replayed without half of itself.
type demoted struct {
	slog.Handler
	held *[]slog.Record
}

func (d demoted) Enabled(ctx context.Context, _ slog.Level) bool {
	return d.held != nil || d.Handler.Enabled(ctx, slog.LevelDebug)
}

func (d demoted) Handle(ctx context.Context, r slog.Record) error {
	if d.held != nil {
		*d.held = append(*d.held, r.Clone())
	}
	r.Level = slog.LevelDebug
	if !d.Handler.Enabled(ctx, slog.LevelDebug) {
		return nil
	}
	return d.Handler.Handle(ctx, r)
}

func (d demoted) WithAttrs(a []slog.Attr) slog.Handler {
	return demoted{Handler: d.Handler.WithAttrs(a)}
}

func (d demoted) WithGroup(name string) slog.Handler {
	return demoted{Handler: d.Handler.WithGroup(name)}
}

// replay writes held records to h at the level they were logged at. See
// demoted.
func replay(ctx context.Context, h slog.Handler, held []slog.Record) {
	for _, r := range held {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r)
		}
	}
}
