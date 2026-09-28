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
//
// **And the account's email is hidden at every level, Debug included.** tsnet
// writes the signed-in Tailscale account at Debug ("active login: ..."), so a
// `-v` log carried the owner's email address to wherever it was pasted. Unlike
// the tailnet's name it says nothing whoever is diagnosing needs, so there is
// no level at which it is kept.
type hideTailnet struct{ slog.Handler }

// HideTailnet wraps h so that no line from Info up carries a tailnet's name,
// and no line at all carries an email address.
func HideTailnet(h slog.Handler) slog.Handler { return hideTailnet{h} }

func (h hideTailnet) Handle(ctx context.Context, r slog.Record) error {
	hide := withoutEmail
	if r.Level >= slog.LevelInfo {
		hide = hideBoth
	}
	out := slog.NewRecord(r.Time, r.Level, hide(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(hideAttr(a, hide))
		return true
	})
	return h.Handler.Handle(ctx, out)
}

// WithAttrs hides the name in attributes bound in advance too: those reach
// every level, so they are treated as if they were Info.
func (h hideTailnet) WithAttrs(as []slog.Attr) slog.Handler {
	hidden := make([]slog.Attr, len(as))
	for i, a := range as {
		hidden[i] = hideAttr(a, hideBoth)
	}
	return hideTailnet{h.Handler.WithAttrs(hidden)}
}

func (h hideTailnet) WithGroup(name string) slog.Handler {
	return hideTailnet{h.Handler.WithGroup(name)}
}

func hideAttr(a slog.Attr, hide func(string) string) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, hide(v.String()))
	case slog.KindGroup:
		group := v.Group()
		hidden := make([]any, len(group))
		for i, g := range group {
			hidden[i] = hideAttr(g, hide)
		}
		return slog.Group(a.Key, hidden...)
	case slog.KindAny:
		// An error or anything else that prints itself is text by the time it
		// reaches the file, and the probe's errors carry the URL.
		switch x := v.Any().(type) {
		case error, fmt.Stringer:
			return slog.String(a.Key, hide(fmt.Sprint(x)))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

func hideBoth(s string) string { return withoutLoginLink(withoutTailnet(withoutEmail(s))) }

// emailAddress matches an address with a domain that has a dot in it, which
// is what an account's is and what a Go identifier or a `user@host` is not.
var emailAddress = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

// withoutEmail writes every email address as `<email>`.
func withoutEmail(s string) string { return emailAddress.ReplaceAllString(s, "<email>") }

// loginLink is the address that signs a node into a tailnet. Until somebody
// opens it, whoever opens it first can take the node into their own, so it is
// a key and not a diagnosis: the tray and the page show it whole, the file from
// Info up keeps where it points and not the key. `-v` still has it.
var loginLink = regexp.MustCompile(`(?i)(login\.tailscale\.com/a/)[A-Za-z0-9]+`)

// withoutLoginLink writes the key of every sign-in link as `<hidden>`.
func withoutLoginLink(s string) string { return loginLink.ReplaceAllString(s, "${1}<hidden>") }

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
