package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"patmonitor/internal/guard"
	"patmonitor/internal/rtc"
	"patmonitor/internal/tunnel"
)

// The session log exists for the test this program cannot run at home: phone on
// a mobile network, Tailscale off, far from the PC. There no status page is
// watched and webrtc-internals is not opened; on the way back only these lines
// remain, and they have to be enough to tell a network losing packets from an
// encoder that does not answer.

// sessionSampleInterval is how often a running session is measured.
//
// Fifteen seconds is sparse enough not to fill a night's log and close enough to
// show a network hole as a dip rather than as an average: a tunnel in a car
// lasts less than a minute.
const sessionSampleInterval = 15 * time.Second

// originClass says what road the request came in on, as a code.
//
// **It exists because Kind is prose.** That field is read by a human in the log,
// so it is written for a human — "Internet (Funnel)", "local network" — and a
// decision compared against it is a decision a reword switches off in silence.
// It is the rule this project already pays for everywhere else, met here by a
// diagnosis that needed to tell a tailnet session from a Wi-Fi one.
type originClass int

const (
	// originUnknown is an address that would not parse. It is the zero value on
	// purpose: whoever forgets to classify gets the answer that promises least.
	originUnknown originClass = iota
	originThisPC
	originLocal
	originTailnet
	originInternet
)

// origin describes where the viewer is coming from.
type origin struct {
	// Kind is the readable label: "Internet (Funnel)", "tailnet", "local
	// network".
	Kind string
	// Addr is the visitor's address, the real one.
	Addr string
	// Class is the same thing for whoever has to decide something.
	Class originClass
}

// Public says whether the request arrived from the Internet. It is the
// distinction that matters: public sessions are the ones that cross NATs and
// mobile networks, that is, the ones that can go wrong in ways that do not exist
// at home.
//
// **It is derived and not stored**, because it was stored beside Class for the
// length of one draft and that is one list too many: a flag and a code saying
// the same thing diverge at the first road added, and this one is read by
// apiQR, which decides whether a code is engraved for the caller.
func (o origin) Public() bool { return o.Class == originInternet }

// requestOrigin classifies where a request came from.
//
// For requests arriving through the funnel, RemoteAddr is the Tailscale ingress
// node and it is the same for everybody: the visitor's address is put in the
// context by the tunnel, and it comes from Tailscale, not from a header the
// caller could write. The same caveat as clientKey applies.
func requestOrigin(r *http.Request) origin {
	if src, ok := tunnel.SourceAddr(r.Context()); ok {
		return origin{Kind: "Internet (Funnel)", Addr: src.Addr().String(), Class: originInternet}
	}

	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return origin{Kind: "unknown origin", Addr: host, Class: originUnknown}
	}
	switch {
	case addr.IsLoopback(), sameAsLocal(r, addr):
		return origin{Kind: "this PC", Addr: host, Class: originThisPC}
	case tailnetRange.Contains(addr), tailnetRange6.Contains(addr):
		// 100.64.0.0/10 is the space Tailscale assigns to tailnet nodes:
		// whoever arrives from there is already inside the private network, not
		// from the Internet.
		return origin{Kind: "tailnet", Addr: host, Class: originTailnet}
	case addr.IsPrivate() || addr.IsLinkLocalUnicast():
		return origin{Kind: "local network", Addr: host, Class: originLocal}
	}
	return origin{Kind: "Internet", Addr: host, Class: originInternet}
}

var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// tailnetRange6 is the same space in IPv6. It sits inside the private range,
// so without its own case a tailnet node reached over IPv6 was classed as the
// local network — and a session opened from there was taken for one that had
// crossed the Wi-Fi in clear.
var tailnetRange6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

// sameAsLocal says whether the connection came from the address it arrived on,
// which is what a browser on this PC produces when it opens one of the PC's own
// addresses rather than localhost.
//
// **Loopback alone was not "this PC", and the first-time setup found out.** With
// `listen_addr` bound to one interface there is no loopback listener at all: the
// tray opens that interface's address, Windows sends the request from it, and
// the setup, which is accepted only from this PC, refused the one machine it
// exists for, at the first start and after every reset. The same happened to
// whoever, sitting here, opened the page from a bookmark to the LAN address.
//
// It cannot be claimed from another device: a TCP connection whose source is
// this PC's own address completes only from this PC, and the value comes from
// the server's socket, not from anything the caller writes.
func sameAsLocal(r *http.Request, remote netip.Addr) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	ap, err := netip.ParseAddrPort(local.String())
	if err != nil {
		return false
	}
	return ap.Addr().Unmap() == remote.Unmap()
}

// namesThisPC says whether a Host header is a name only this PC can answer to:
// an address written as a number, or `localhost`.
//
// **It is the half of "this PC" that the socket cannot supply.** A connection
// from loopback proves the browser is here; it does not prove the page inside
// it is ours, because a page on a name its author controls can have that name
// re-pointed at 127.0.0.1 after it has loaded, and the browser then sends its
// requests here under that name. An address has no name to re-point, and
// `localhost` is resolved by the operating system — and by the browsers
// themselves — never by somebody else's DNS.
//
// The machine's own name is left out on purpose: it is resolved by the router
// or by the network, which is exactly the thing whose answers are not ours, and
// whoever types it at this PC is sent on to the address rather than refused.
func namesThisPC(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	_, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	return err == nil
}

// watchSession follows a session and writes its report.
//
// The periodic lines sit at debug level and the opening and closing ones at
// normal level: whoever leaves the monitor on all night must not find the log
// full, but has to be able to say in the morning how many times the phone
// connected and how it went. With -v there is the whole detail.
func (s *Server) watchSession(ctx context.Context, notify func(path string), v *rtc.Viewer, from origin, userAgent string) {
	log := s.log.With(
		"session", v.ID(),
		"from", from.Kind,
		"address", from.Addr,
	)

	// First phase: waiting for the negotiation to finish. The ICE path is the
	// value that says whether the connection from outside worked, so the session
	// is not announced before it is known — and if it is not established within
	// the time allowed, that is the outcome and it gets written all the same.
	path, ok := waitForPath(ctx, v, negotiationTimeout)
	if !ok {
		// The cause is named here and nowhere else. The page says two words to
		// somebody in another house, and by morning the only thing left is this
		// line: a failure with no cause written down cannot be investigated
		// afterwards, because the session it belonged to is gone.
		facts := v.ICEFacts()
		cause, remedy := noPathCause(facts, from)
		log.Warn("viewer with no ICE path established",
			append([]any{
				"waited", negotiationTimeout.String(),
				"user_agent", shortUA(userAgent),
				"cause", cause,
				"remedy", remedy,
			}, facts.LogArgs()...)...)
		<-doneOrCtx(ctx, v)
		// A session that ends without ever having connected is a fault, not a
		// visit: reporting it as a visit would hide it among the others.
		log.Warn("viewer disconnected without ever establishing media",
			"duration", v.Since().Round(time.Second).String())
		return
	}
	if notify != nil {
		notify(path)
	}

	last := v.Report()
	log.Info("viewer connected",
		"path", path,
		"remote", last.Remote,
		"rtt_ms", last.RTT.Milliseconds(),
		"user_agent", shortUA(userAgent))

	tick := time.NewTicker(sessionSampleInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logSessionEnd(log, v, path, last)
			return
		case <-v.Done():
			s.logSessionEnd(log, v, path, last)
			return

		case <-tick.C:
			rep := v.Report()
			last = rep
			if rep.Path != "" && rep.Path != path {
				// The path can change while watching: a phone leaving the house
				// goes from Wi-Fi to the mobile network and renegotiates.
				log.Info("path changed", "from", path, "to", rep.Path, "remote", rep.Remote)
				path = rep.Path
			}
			log.Debug("session", rep.LogArgs()...)

			// A perceptible loss is said out loud even without -v: it is exactly
			// what one is looking for when the picture breaks up, and finding it
			// out otherwise would mean having switched debug on **before** it
			// happened.
			if rep.LossPercent >= lossWarnPercent {
				log.Warn("packet loss towards the viewer", rep.LogArgs()...)
			}
		}
	}
}

// negotiationTimeout is how long ICE is given before the media is declared not
// to pass.
const negotiationTimeout = 20 * time.Second

// lossWarnPercent is the loss beyond which the voice is raised.
//
// Below one per cent the picture is not visibly affected; at two the first
// artefacts show, and that is where it is worth writing down.
const lossWarnPercent = 2.0

// waitForPath waits for ICE to choose a candidate pair.
func waitForPath(ctx context.Context, v *rtc.Viewer, timeout time.Duration) (string, bool) {
	// Half a second: negotiation at home finishes in a few tens of
	// milliseconds, and this wait is also what delays the status line shown to
	// the viewer.
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(timeout)

	for {
		select {
		case <-ctx.Done():
			return "", false
		case <-v.Done():
			return "", false
		case <-deadline:
			return "", false
		case <-tick.C:
			if p := v.SelectedPair(); p != "" {
				return p, true
			}
		}
	}
}

// doneOrCtx closes when the session or the context ends.
func doneOrCtx(ctx context.Context, v *rtc.Viewer) <-chan struct{} {
	out := make(chan struct{})
	guard.Go(nil, "waiting for the session to end", func() {
		defer close(out)
		select {
		case <-ctx.Done():
		case <-v.Done():
		}
	})
	return out
}

// logSessionEnd closes a session's report.
//
// The numbers are those of the last good sample and are not reread now: with the
// session closed the ICE agent no longer answers and would return zeros, which
// in the final report would look like a connection dead on arrival rather than
// one that ended normally. Measured: `last_video_kbps=0 last_rtt_ms=0` on
// sessions that had lasted two and a half minutes.
func (s *Server) logSessionEnd(log logger, v *rtc.Viewer, path string, last rtc.SessionReport) {
	log.Info("viewer disconnected",
		"duration", v.Since().Round(time.Second).String(),
		"path", path,
		"last_video_kbps", last.VideoKbps,
		"last_rtt_ms", last.RTT.Milliseconds())
}

// logger is the part of *slog.Logger that is needed here, so that the function
// stays checkable without a real log.
type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// shortUA reduces the User-Agent to what it takes to recognise the device.
//
// The whole string is a hundred characters of which ninety are the same for
// everybody, and in a log read in the morning on a narrow terminal that length
// costs more than it is worth. What matters here is only telling "the phone"
// from "the PC at home".
func shortUA(ua string) string {
	switch {
	case ua == "":
		return "not declared"
	case strings.Contains(ua, "iPhone"):
		return "iPhone"
	case strings.Contains(ua, "iPad"):
		return "iPad"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Macintosh"):
		return "Mac"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	}
	if len(ua) > 40 {
		return ua[:40]
	}
	return ua
}
