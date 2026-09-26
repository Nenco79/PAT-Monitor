package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
)

// hideTailnet writes the owner's tailnet name as `<tailnet>` in every line from
// Info up, and leaves it whole at Debug.
//
// **The log is the file people attach to an issue**, and this package is the
// one that knows the public address: it wrote it in full four times at the
// default level — the address when the tunnel came up, the certificate's
// domain twice, and the serve configuration, which is keyed by it — plus every
// error the reachability probe got back, which net/http composes with the URL
// inside. A tailnet name is publicly resolvable DNS tied to an account, which
// is why this repository refuses one in a commit; a log pasted into a public
// tracker is the same exposure. The node's own label stays, because it is what
// says which installation the line is about, and `-v` still has the whole
// name for whoever is diagnosing on purpose.
//
// It is a handler rather than a function at each call site because the call
// sites are the list that diverges: the error from the probe carried the name
// in a place nobody thought of as "the address", and the server writes the Host
// of a refused request, which from the Funnel is the public name. So the
// monitor puts it on its root logger, and New puts it on the tunnel's in case
// whoever builds one hands in a logger without it.
type hideTailnet struct{ slog.Handler }

// HideTailnet wraps h so that no line from Info up carries a tailnet's name.
func HideTailnet(h slog.Handler) slog.Handler { return hideTailnet{h} }

func (h hideTailnet) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < slog.LevelInfo {
		return h.Handler.Handle(ctx, r)
	}
	out := slog.NewRecord(r.Time, r.Level, withoutTailnet(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(hideAttr(a))
		return true
	})
	return h.Handler.Handle(ctx, out)
}

// WithAttrs hides the name in attributes bound in advance too: those reach
// every level, so they are treated as if they were Info.
func (h hideTailnet) WithAttrs(as []slog.Attr) slog.Handler {
	hidden := make([]slog.Attr, len(as))
	for i, a := range as {
		hidden[i] = hideAttr(a)
	}
	return hideTailnet{h.Handler.WithAttrs(hidden)}
}

func (h hideTailnet) WithGroup(name string) slog.Handler {
	return hideTailnet{h.Handler.WithGroup(name)}
}

func hideAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, withoutTailnet(v.String()))
	case slog.KindGroup:
		group := v.Group()
		hidden := make([]any, len(group))
		for i, g := range group {
			hidden[i] = hideAttr(g)
		}
		return slog.Group(a.Key, hidden...)
	case slog.KindAny:
		// An error or anything else that prints itself is text by the time it
		// reaches the file, and the probe's errors carry the URL.
		switch x := v.Any().(type) {
		case error, fmt.Stringer:
			return slog.String(a.Key, withoutTailnet(fmt.Sprint(x)))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// tailnetName matches a name under ts.net: an optional node label, then the
// tailnet's own.
var tailnetName = regexp.MustCompile(`(?i)\b(?:([a-z0-9-]+)\.)?[a-z0-9-]+\.ts\.net\b`)

// withoutTailnet turns `patmon-1a2b3c.quercia-lieve.ts.net` into
// `patmon-1a2b3c.<tailnet>.ts.net`, and a bare tailnet name into
// `<tailnet>.ts.net`.
func withoutTailnet(s string) string {
	return tailnetName.ReplaceAllStringFunc(s, func(m string) string {
		if node := tailnetName.FindStringSubmatch(m)[1]; node != "" {
			return node + ".<tailnet>.ts.net"
		}
		return "<tailnet>.ts.net"
	})
}
