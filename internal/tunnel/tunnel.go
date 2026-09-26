// Package tunnel puts the monitor on the Internet without touching the router,
// by embedding a Tailscale node in the process.
//
// The traffic splits in two: the page and the WebSocket signalling come through
// here, a few kilobytes, while video and audio go straight between browser and
// application over WebRTC. The split is not an optimisation but a constraint:
// funnel traffic always crosses Tailscale's DERP relays and is subject to
// bandwidth limits, and the documentation says outright that it is not suitable
// for video streaming.
//
// Turning it on needs two settings in the Tailscale panel that cannot be made
// from here. Rather than failing with an obscure error, the package detects them
// and reports in plain words what is missing and what to do: see State.
package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
	"tailscale.com/version"

	"patmonitor/internal/config"
	"patmonitor/internal/guard"
)

// Phase is where the activation has got to.
//
// **These are codes, and they have to stay codes.** The value crosses the JSON
// and nine places between `app.js` and `onboarding.js` compare it, so the whole
// configuration path runs on these eight strings. Written as sentences in one
// language, translating the interface would switch every one of those
// comparisons off **without an error anywhere**: the remote-access panel would
// stay grey and the onboarding stuck at step 3, which is the worst way a thing
// can break. It is the `MicHealth` family.
//
// The value did **two jobs**: `app.js` compared it and also printed it in the
// panel's badge. Now the code travels and the page picks the word, from a
// dictionary that sits next to where it is shown — the same shape as the two
// signalling codes.
type Phase string

const (
	PhaseOff         Phase = "off"
	PhaseStarting    Phase = "starting"
	PhaseNeedsLogin  Phase = "needs-login"
	PhaseNeedsFunnel Phase = "needs-funnel"
	// PhaseNeedsApproval: the device has been created and the login happened,
	// but the tailnet has manual approval switched on and an administrator has
	// to admit it.
	//
	// **This is the state that was missing, and its absence produced the worst
	// possible instruction**: "authorise this device" said to somebody who had
	// authorised it a minute earlier. The device shows up in the Tailscale list
	// and stays off, and the reader concludes the program does not work.
	PhaseNeedsApproval Phase = "needs-approval"
	// PhaseCertificate: the node is ready and the TLS certificate is being
	// waited for. It is the one point on the path that can last tens of seconds
	// with nothing for the user to do, and without a phase of its own it showed
	// as "starting" — that is, indistinguishable from a stuck program, at
	// precisely the moment it is working.
	PhaseCertificate Phase = "certificate"
	PhaseRunning     Phase = "running"
	PhaseError       Phase = "error"
)

// State describes the remote access in a form that can be shown to the user.
//
// Action and ActionURL exist because every missing prerequisite has to say what
// to do about it: a baby monitor is installed by somebody who did not write it,
// and a requirement that shows up as a runtime error is a product defect.
type State struct {
	Phase Phase `json:"phase"`
	// PublicURL is the address reachable from the Internet, filled in only
	// while the tunnel is active.
	PublicURL string `json:"publicUrl,omitempty"`
	// Action is the next step that falls to the user, as a **code**.
	//
	// **The words are chosen by whoever shows them**, and here that is not one
	// reader: this state crosses the JSON and is read by the configuration
	// path, which speaks the language of the viewer's browser, and by the tray,
	// which speaks Windows'. A sentence composed here would already be wrong,
	// not merely untranslated: it would be the language of whoever wrote the
	// program imposed on both audiences.
	Action ActionCode `json:"action,omitempty"`
	// ActionText is the text that comes from **Tailscale**, when there is any.
	//
	// It is the one case where a whole sentence passes through here, and it is
	// not touched: the control server writes it knowing the tailnet and the
	// role of whoever is looking, and rewriting it would mean guessing. It
	// arrives in English, and it is a foreign line inside a translated path —
	// accepted, because the alternative is inventing instructions that can be
	// wrong.
	//
	// When it is there it wins over Action: whoever shows writes this and
	// nothing else.
	ActionText string `json:"actionText,omitempty"`
	// FailedStep goes with ActionFailed and says which step did not succeed.
	FailedStep FailedStep `json:"failedStep,omitempty"`
	// ActionURL is the page to open in order to carry it out.
	ActionURL string `json:"actionUrl,omitempty"`
	// Detail carries the technical error, for diagnosis.
	//
	// **It stays prose and does not become a code**, deliberately: it is the
	// original message of whatever failed — a network error, a backend state —
	// and it sits inside a closed `<details>`. Translating it would mean
	// translating messages we do not write; hiding it would take away the only
	// handhold from whoever can read it.
	Detail string `json:"detail,omitempty"`
	// Warning says the tunnel is open but something does not bode well.
	//
	// It exists because of a fault already seen: `waitIngressGrant` failing
	// left only a Log.Warn, and the state became PhaseRunning with an address
	// that does not answer from outside. It is the shape of the v1.102.1 fault
	// — panel in order, valid certificate, a TLS handshake that never comes
	// back — and declaring it "ready" sends the reader looking for the defect
	// in their own network.
	//
	// It is the same distinction between answer and effect that holds for the
	// encoder: the funnel **agreed** to open, which does not say anything gets
	// through it.
	Warning WarningCode `json:"warning,omitempty"`
	// Reach is the other half of that distinction, taken from the positive
	// side: not "something does not bode well" but "we actually got through".
	// Warning comes from what Tailscale answered us; this comes from a request
	// of ours that went out to the Internet and came back. It lives in
	// `reach.go`.
	Reach ReachCode `json:"reach,omitempty"`
}

// ActionCode is the step that falls to the user, without the words to say it.
type ActionCode string

const (
	ActionNone ActionCode = ""
	// The TLS certificate is being issued: there is nothing to do but wait, and
	// it is the one point on the path that lasts tens of seconds.
	ActionWaitCertificate ActionCode = "wait-certificate"
	// The device has to be authorised in the tailnet.
	ActionAuthorise ActionCode = "authorise"
	// The tailnet requires manual approval by an administrator.
	ActionApprove ActionCode = "approve"
	// The Tailscale node belongs to another user of this computer.
	ActionOtherUser ActionCode = "other-user"
	// The funnel is not enabled, and Tailscale did not say how to enable it.
	// This is the fallback for ActionText: when the control server answers,
	// that sentence beats this one because it knows the role of whoever reads
	// it.
	ActionEnableFunnel ActionCode = "enable-funnel"
	// Remote access did not open. Which step failed is said by FailedStep,
	// which is a second code and not a piece of a sentence: concatenating
	// "cannot open remote access: " with the name of a step composes in one
	// language and in no other.
	ActionFailed ActionCode = "failed"
)

// FailedStep says **which** step of the opening failed.
//
// It is a code of its own and not a fragment to concatenate onto ActionFailed:
// "cannot open remote access: " plus the name of a step composes in one
// language and in no other, and that half-sentence is what the codes exist to
// remove. Whoever shows has both codes and writes a single line, in whatever
// grammar it needs.
type FailedStep string

const (
	StepNone         FailedStep = ""
	StepNodeStart    FailedStep = "node-start"
	StepFunnelListen FailedStep = "funnel-listen"
	StepCertificate  FailedStep = "certificate"
	StepFunnelServe  FailedStep = "funnel-serve"
	StepLocalClient  FailedStep = "local-client"
	StepStatusRead   FailedStep = "status-read"
	// StepPanic is the step nobody chose: a panic inside the tunnel's own work.
	//
	// It is a step like the others because of what reads it — the badge on the
	// page and the icon in the notification area — and those must not go on
	// saying "active" about something that is no longer running. Without it the
	// only honest alternative would be to let the panic through, which switches
	// the camera off because outside access broke.
	StepPanic FailedStep = "panic"
)

func AllSteps() []FailedStep {
	return []FailedStep{
		StepNodeStart, StepFunnelListen, StepCertificate,
		StepFunnelServe, StepLocalClient, StepStatusRead,
		StepPanic,
	}
}

// WarningCode is what does not bode well with the tunnel open.
type WarningCode string

const (
	WarningNone WarningCode = ""
	// The node is active but Tailscale has not granted public ingress.
	WarningNoIngress WarningCode = "no-ingress"
)

// AllActions and AllWarnings are the authoritative lists.
//
// They serve the guard that demands a word in every catalogue and a branch in
// every reader: a code added here and forgotten over there would appear **as a
// code** on the screen, and no compiler would say so. It is the same shape as
// the alerts' `AllCodes` and the phases'.
func AllActions() []ActionCode {
	return []ActionCode{
		ActionWaitCertificate, ActionAuthorise, ActionApprove,
		ActionOtherUser, ActionEnableFunnel, ActionFailed,
	}
}

func AllWarnings() []WarningCode { return []WarningCode{WarningNoIngress} }

// Config gathers what it takes to open the tunnel.
type Config struct {
	// Hostname becomes the first label of the public name.
	Hostname string
	// AuthKey avoids logging in by hand. If empty, the authorisation address is
	// shown to the user.
	AuthKey string
	// StateDir keeps the node's identity from one start to the next: without
	// it, every restart would create a new node in the tailnet.
	StateDir string

	// OnHostname receives the name the tailnet **actually** granted, when it
	// differs from the one asked for. It may be nil.
	//
	// **Asking for a hostname is a request, not a fact.** If that name is
	// already taken — another installation, or an old node left on the list —
	// Tailscale does not refuse: it renames, and does not say so. Whoever
	// receives this callback writes the real name into the configuration, so
	// that the next start asks for what it already has and nothing is renamed.
	//
	// It also serves the opposite direction, which is the more insidious one:
	// if one day the old node vanished from the list, the short name would come
	// free and we would take it back — that is, the public address **would
	// change again**, silently, breaking the bookmark somebody has saved in the
	// meantime.
	OnHostname func(string)

	Log *slog.Logger
}

// Tunnel drives the embedded Tailscale node.
type Tunnel struct {
	cfg Config

	// enabled is permission to start. Run waits for it rather than presuming
	// it.
	//
	// **It exists because remote access is switched on during configuration,
	// not before.** Building the tunnel only if `funnel_enabled` was already
	// true, the fork "do you want it from outside too?" could only answer
	// "restart the program" — which inside a guided path is where people stop.
	// The tunnel is now always there and asleep: `New` opens nothing, it costs
	// a struct.
	enabled chan struct{}
	once    sync.Once

	mu    sync.RWMutex
	state State

	// reachNonce is the value the reachability probe expects to find again. It
	// lives here rather than in the state because it is not a thing to show:
	// the prober writes it and whoever serves the request reads it, and those
	// are two different goroutines.
	reachNonce atomic.Value
}

func New(cfg Config) *Tunnel {
	// No default is invented here: `config.Default` already has one, and it is
	// distinct per machine. A second default written by hand in this file would
	// be the classic second list that diverges from the first — and it would
	// diverge on the very value that generates the public address.
	if cfg.Hostname == "" {
		cfg.Hostname = config.DefaultFunnelHostname()
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Tunnel{
		cfg:     cfg,
		enabled: make(chan struct{}),
		state:   State{Phase: PhaseOff},
	}
}

// Enable gives Run permission to start, and can be called more than once.
//
// It does not check `CanExposePublicly`: that check belongs to whoever knows the
// configuration, and duplicating it here would mean two places deciding the most
// delicate thing in the program. The caller does it first.
func (t *Tunnel) Enable() {
	t.once.Do(func() { close(t.enabled) })
}

// State returns the current state.
func (t *Tunnel) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// setState updates the state and notes in the log what changed.
//
// The missing step has to be told to whoever is watching the console and not
// the page: the person installing the monitor is often at the PC, not at the
// phone. It is only logged when something changes, because the state is reread
// every two seconds.
//
// The comparison includes the address to open, not the phase alone: Tailscale
// produces the authorisation address a few seconds after declaring that a login
// is needed, so watching the phase alone would mean the link — the one thing the
// user really has to have — never appeared in the log at all.
func (t *Tunnel) setState(s State) {
	t.mu.Lock()
	changed := t.state.Phase != s.Phase || t.state.ActionURL != s.ActionURL ||
		t.state.Warning != s.Warning
	t.state = s
	t.mu.Unlock()

	if !changed {
		return
	}
	attrs := []any{"phase", string(s.Phase)}
	if s.Action != "" {
		attrs = append(attrs, "todo", s.Action)
	}
	if s.ActionURL != "" {
		attrs = append(attrs, "open", s.ActionURL)
	}
	// A warning is not a phase: "active" is still reached, but with an address
	// that might not answer from outside. It goes at WARN, otherwise the line
	// is lost among those of normal operation.
	if s.Warning != "" {
		t.cfg.Log.Warn("remote access active with a caveat",
			append(attrs, "warning", s.Warning)...)
		return
	}
	t.cfg.Log.Info("remote access", attrs...)
}

// Run opens the tunnel and serves handler until the context is cancelled.
//
// **It answers nil even when it fails**, and every one of run's own failure
// paths does exactly that: missing prerequisites end up in State, where the
// user can read them and put them right, and the waiting continues. The
// monitor on the home network has to keep working regardless.
//
// A panic is one more failure and is declared the same way, on a step nobody
// chose: left to propagate it would come out of the errgroup and switch off
// the camera because the tunnel broke, and swallowed in silence it would leave
// the badge and the icon saying "active" about something that is no longer
// running.
//
// **Only a panic is filed as one.** guard.Run hands back whatever run
// returned as well, and run answers nil today — but the day it grows a real
// `return err`, filing it under StepPanic would put "an unexpected fault
// inside the monitor" on the page in place of the real diagnosis.
func (t *Tunnel) Run(ctx context.Context, handler http.Handler) error {
	err := guard.Run(t.cfg.Log, "remote access", func() error {
		return t.run(ctx, handler)
	})
	switch {
	case errors.Is(err, guard.ErrPanic):
		t.fail(StepPanic, err)
	case err != nil:
		return err
	}
	return nil
}

// run is the tunnel's whole life, and every road out of it answers nil: see
// Run, which is where that contract is argued and where a panic is turned into
// a failure like the others.
func (t *Tunnel) run(ctx context.Context, handler http.Handler) error {
	// Permission is waited for without consuming anything. Until it arrives the
	// tunnel does not exist as far as the system is concerned: no registered
	// node, no tsnet directory, no connection to the control server.
	select {
	case <-t.enabled:
	case <-ctx.Done():
		return nil
	}

	srv := &tsnet.Server{
		Dir:      filepath.Join(t.cfg.StateDir, "tsnet"),
		Hostname: t.cfg.Hostname,
		AuthKey:  t.cfg.AuthKey,
		// The backend's logs are verbose and concern Tailscale's own workings:
		// they go at debug level, not to the user.
		Logf: func(format string, args ...any) { t.cfg.Log.Debug(fmt.Sprintf(format, args...)) },
		// **The ones meant for the user are said once.** Tailscale repeats
		// "restart with TS_AUTHKEY set, or go to: …" **every five seconds**
		// for as long as nobody authorises the device, which is a wait that can
		// last as long as a person takes to read an email. Measured on a test
		// machine, forty identical lines in four minutes — and the address they
		// carry we already write ourselves, once, next to the phase.
		//
		// It is not a library being verbose: it is the same shape as the
		// refused viewers, that is, the volume of our log decided by somebody
		// else. The first one comes out, the repeats stay at `-v`.
		UserLogf: sayOnce(t.cfg.Log),
	}
	defer srv.Close()

	// The version the node declares to the control panel. It has to be stamped
	// by the linker in build.ps1: without that, the library finds no VCS data
	// and announces "ERR-BuildInfo", which in the node list looks like a
	// defective client.
	t.cfg.Log.Info("Tailscale node", "version", version.Long())

	t.setState(State{Phase: PhaseStarting})
	if err := srv.Start(); err != nil {
		t.fail(StepNodeStart, err)
		return nil
	}

	// Waits for the node to be logged in and the prerequisites to be satisfied,
	// reporting meanwhile what is missing.
	if err := t.waitReady(ctx, srv); err != nil {
		return nil
	}

	ln, err := srv.ListenFunnel("tcp", ":443")
	if err != nil {
		t.fail(StepFunnelListen, err)
		return nil
	}
	defer ln.Close()

	t.setState(State{Phase: PhaseCertificate,
		Action: ActionWaitCertificate})
	if err := t.warmCertificate(ctx, srv); err != nil {
		t.fail(StepCertificate, err)
		return nil
	}

	t.logServeConfig(ctx, srv)

	var warning WarningCode
	if !t.waitIngressGrant(ctx, srv) {
		t.cfg.Log.Warn("public ingress not granted: connections from the Internet will be refused")
		warning = WarningNoIngress
	}

	url := t.publicURL(ctx, srv)
	t.checkHostname(url)
	t.setState(State{Phase: PhaseRunning, PublicURL: url, Warning: warning})
	t.cfg.Log.Info("remote access active", "url", url)

	// The reachability probe starts now and lives as long as the tunnel: it
	// goes out to the Internet, comes back in through the funnel and looks for
	// itself. Until it succeeds the state says "I do not know", which is
	// different from "it does not work".
	//
	// **Its context is a child and closes here.** With the caller's, the
	// goroutine outlived `Run`: on the way out of a server fault — where `fail`
	// writes PhaseError — it stayed interrogating the public address for the
	// life of the process, and writing its own outcome over a state that
	// describes something else. The viewer saw "does not answer from outside"
	// instead of the real error, that is, the diagnosis replaced by its least
	// useful symptom.
	reachCtx, stopReach := context.WithCancel(ctx)
	defer stopReach()
	guard.Go(t.cfg.Log, "the reachability check", func() { t.watchReach(reachCtx, url) })

	httpSrv := &http.Server{
		// serveReach sits **in front of** the monitor and only on this
		// listener, that is, only on the port facing the Internet: that is what
		// makes the answer evidence rather than an assertion.
		Handler:           t.serveReach(handler),
		ReadHeaderTimeout: 10 * time.Second,
		// A keep-alive connection that sends nothing more is let go after two
		// minutes: without it one held open costs a goroutine and a netstack
		// endpoint for as long as the caller likes, and this listener faces
		// the Internet. A hijacked WebSocket is not idle in this sense and is
		// not touched.
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
		ConnContext:    withFunnelSource,
	}
	guard.Go(t.cfg.Log, "the shutdown of public access", func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	})

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.fail(StepFunnelServe, err)
		return nil
	}
	t.setState(State{Phase: PhaseOff})
	return nil
}

// sayOnce lets a single line through for each message repeated in a row.
//
// The comparison is on the already formatted text, not on the format: two lines
// that differ by a number are two pieces of news, and both have to be said.
func sayOnce(log *slog.Logger) func(string, ...any) {
	var mu sync.Mutex
	var last string
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		mu.Lock()
		repeat := line == last
		last = line
		mu.Unlock()
		if repeat {
			log.Debug(line)
			return
		}
		log.Info(line)
	}
}

// ---------- the visitor's real address ----------

type funnelSrcKey struct{}

// withFunnelSource notes in the context the address of whoever connected.
//
// Through the funnel every request appears to come from the Tailscale ingress
// node that forwarded it, not from the visitor: RemoteAddr is the same for
// everybody. Rate limiting based on that would treat the whole world as one
// person, and whoever tries passwords at random from outside would lock out the
// person who knows it too. The real address is there, in a separate field of the
// connection.
//
// On connections arriving from the tailnet the assertion does not hold and
// RemoteAddr stays valid, which there is already the right address.
func withFunnelSource(ctx context.Context, c net.Conn) context.Context {
	tc, ok := c.(*tls.Conn)
	if !ok {
		return ctx
	}
	fc, ok := tc.NetConn().(*ipn.FunnelConn)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, funnelSrcKey{}, fc.Src)
}

// WithSourceAddr notes the visitor's address without going through a real
// connection.
//
// It serves the tests: the only natural way to get here is an *ipn.FunnelConn,
// which cannot be fabricated, and without this function the classification of
// origins — the place where a mistake attributes every visit from the Internet
// to the same person — would be the one part that cannot be checked.
func WithSourceAddr(ctx context.Context, src netip.AddrPort) context.Context {
	return context.WithValue(ctx, funnelSrcKey{}, src)
}

// SourceAddr returns the visitor's address for requests that arrived through
// the funnel, and ok=false for all the others.
func SourceAddr(ctx context.Context) (netip.AddrPort, bool) {
	src, ok := ctx.Value(funnelSrcKey{}).(netip.AddrPort)
	return src, ok
}

// logServeConfig notes how forwarding turns out to be configured on the node.
//
// ListenFunnel can return a valid listener without public forwarding being
// active on the control side: in that case Tailscale's ingress accepts the TCP
// connection but does not hand it to us, and the visitor sees a TLS handshake
// that gets no answer. Logging it makes this case distinguishable from a network
// problem, instead of guessed at.
func (t *Tunnel) logServeConfig(ctx context.Context, srv *tsnet.Server) {
	lc, err := srv.LocalClient()
	if err != nil {
		return
	}
	sc, err := lc.GetServeConfig(ctx)
	if err != nil {
		t.cfg.Log.Warn("serve configuration unreadable", "error", err)
		return
	}
	if sc == nil {
		t.cfg.Log.Warn("no serve configuration on the node")
		return
	}
	raw, _ := json.Marshal(sc)
	t.cfg.Log.Info("serve configuration", "config", string(raw))
}

// waitIngressGrant waits for the ingress grant to appear in the filter.
//
// Ingress nodes enter the netmap only after the node has told the control plane
// that it wants to receive public traffic, which happens when the forwarding
// configuration is written: immediately after ListenFunnel the filter is still
// the previous one, and an immediate check would give a false alarm.
func (t *Tunnel) waitIngressGrant(ctx context.Context, srv *tsnet.Server) bool {
	const timeout = 15 * time.Second
	deadline := time.Now().Add(timeout)
	for {
		if t.checkIngressGrant(ctx, srv) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return true // on the way out there is no sense in raising an alarm
		case <-time.After(time.Second):
		}
	}
}

// checkIngressGrant verifies that the ingress nodes are allowed to hand us
// public connections.
//
// It is a silent, nasty fault: the tunnel looks open, the control panel shows
// the funnel, the certificate is valid, and whoever connects from the Internet
// sees only a TLS handshake that never answers. The connection reaches the node,
// but peerapi refuses it with "no ingress cap" because the filter the control
// plane sends us is missing the https://tailscale.com/cap/ingress grant towards
// the ingress nodes.
//
// The check is made on the filter rather than on the outcome of a request
// because nobody needs to knock from outside: the answer is already in the
// netmap.
//
// It covers the case where the grant does not arrive. It does not cover the one
// where it arrives and the client discards it: that is the defect in
// tailscale.com v1.102.1, where the filter was correct and connections were
// refused all the same. For that the only handhold is the backend's debug line.
func (t *Tunnel) checkIngressGrant(ctx context.Context, srv *tsnet.Server) bool {
	lc, err := srv.LocalClient()
	if err != nil {
		return true // with no local client nothing can be said: do not alarm
	}
	rules, err := lc.DebugPacketFilterRules(ctx)
	if err != nil {
		t.cfg.Log.Debug("packet filter unreadable", "error", err)
		return true
	}
	for _, r := range rules {
		for _, g := range r.CapGrant {
			_, inMap := g.CapMap[tailcfg.PeerCapabilityIngress]
			if !inMap && !slices.Contains(g.Caps, tailcfg.PeerCapabilityIngress) {
				continue
			}
			raw, _ := json.Marshal(r)
			t.cfg.Log.Debug("ingress grant", "rule", string(raw))
			return true
		}
	}
	t.cfg.Log.Debug("no ingress grant in the filter", "rules", len(rules))
	return false
}

// warmCertificate obtains the TLS certificate before serving begins.
//
// tsnet asks for it lazily, on the first connection that arrives. The first
// issuance, though, goes through Let's Encrypt and can take tens of seconds:
// meanwhile the handshake gets no answer and the browser times out, showing an
// insecure-connection error. With a freshly created node this means the first
// visitor never gets in — nor the second, because every attempt starts over.
//
// Asking for it here, the wait is paid once by the start and whoever connects
// finds the certificate ready. The time allowed is deliberately generous: it is
// an operation that happens once in the life of the node.
func (t *Tunnel) warmCertificate(ctx context.Context, srv *tsnet.Server) error {
	domains := srv.CertDomains()
	if len(domains) == 0 {
		return errors.New("no certifiable domain: HTTPS is not enabled for the tailnet")
	}
	lc, err := srv.LocalClient()
	if err != nil {
		return err
	}

	domain := strings.TrimSuffix(domains[0], ".")
	t.cfg.Log.Info("requesting the TLS certificate, this can take tens of seconds",
		"domain", domain)

	certCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	start := time.Now()
	if _, _, err := lc.CertPair(certCtx, domain); err != nil {
		return err
	}
	t.cfg.Log.Info("TLS certificate ready", "domain", domain,
		"waited", time.Since(start).Round(time.Second))
	return nil
}

// waitReady waits for login and prerequisites, updating the state as it goes.
func (t *Tunnel) waitReady(ctx context.Context, srv *tsnet.Server) error {
	lc, err := srv.LocalClient()
	if err != nil {
		t.fail(StepLocalClient, err)
		return err
	}

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		st, err := lc.StatusWithoutPeers(ctx)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				t.fail(StepStatusRead, err)
			}
		default:
			s, ready := t.evaluate(ctx, lc, st)
			if ready {
				return nil
			}
			t.setState(s)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// askTailscale asks the control server how to enable a feature.
//
// The right instructions depend on who is looking: an administrator of the
// tailnet gets a link that enables the feature in one click, while an ordinary
// member has to be told to ask whoever administers it. Writing them by hand
// would mean guessing, and sending everybody to edit the policy file when a
// click is often enough.
func askTailscale(ctx context.Context, lc *local.Client, feature string) (text, url string, done bool) {
	info, err := lc.QueryFeature(ctx, feature)
	if err != nil || info == nil {
		return "", "", false
	}
	return strings.TrimSpace(info.Text), info.URL, info.Complete
}

// evaluate turns Tailscale's state into the next step to take.
//
// The two prerequisites — HTTPS certificates and the funnel attribute — are
// treated as one thing, the same way the official client does it: by asking the
// control server what is missing, rather than working it out. The difference is
// practical, not formal: an administrator of the tailnet gets an answer with a
// link that enables everything in one click, while somebody who is not an
// administrator is pointed at somebody who is. Written by hand, the instructions
// would send anybody to edit the policy file even when a click is enough.
func (t *Tunnel) evaluate(ctx context.Context, lc *local.Client, st *ipnstate.Status) (State, bool) {
	// **Backend states are not all "needs login".** There was a comparison
	// against Running alone here, so every other situation said "authorise this
	// device" — including the one where the user had already done it and was
	// waiting for somebody else. A guided path that asks for something already
	// done sends the reader looking for the fault in the wrong place: in the
	// program, rather than in the tailnet settings.
	switch st.BackendState {
	case ipn.Running.String():
		// carries on below

	case ipn.NeedsMachineAuth.String():
		return State{
			Phase:     PhaseNeedsApproval,
			Action:    ActionApprove,
			ActionURL: "https://login.tailscale.com/admin/machines",
		}, false

	case ipn.Starting.String():
		// Transient: there is nothing to ask anybody, and asking would make an
		// instruction appear and vanish within two seconds.
		return State{Phase: PhaseStarting}, false

	case ipn.InUseOtherUser.String():
		return State{
			Phase:  PhaseError,
			Action: ActionOtherUser,
			Detail: st.BackendState,
		}, false

	default: // NoState, NeedsLogin, Stopped
		return State{
			Phase:     PhaseNeedsLogin,
			Action:    ActionAuthorise,
			ActionURL: st.AuthURL,
		}, false
	}

	if st.Self != nil &&
		st.Self.HasCap(tailcfg.CapabilityHTTPS) &&
		st.Self.HasCap(tailcfg.NodeAttrFunnel) {
		return State{Phase: PhaseStarting}, true
	}

	text, url, done := askTailscale(ctx, lc, "funnel")
	if done {
		return State{Phase: PhaseStarting}, true
	}
	// **Tailscale's text wins over ours.** The control server composes it
	// knowing the tailnet and the role of whoever is looking: to an
	// administrator it gives a link that enables everything in one click, to a
	// member it says who to ask. Our code is the fallback for when it does not
	// answer, and it says a good deal less because it knows neither of those
	// two things.
	return State{Phase: PhaseNeedsFunnel, Action: ActionEnableFunnel,
		ActionText: text, ActionURL: url}, false
}

// checkHostname compares the name asked for with the one granted.
//
// **It is the usual rule applied to the tailnet: the effect is verified, not
// the answer.** `tsnet.Server.Hostname` is what we asked for; the real name is
// in the first label of the public address, which we read from the node. The two
// nearly always agree, and when they do not there is no error anywhere —
// Tailscale renames and carries on.
//
// The viewer would not notice: the URL we show is already the right one,
// because it comes from the node. Who does notice is whoever **already had** the
// old address, on their phone, and they are the only one with no way of finding
// out except by trying.
func (t *Tunnel) checkHostname(url string) {
	name := firstLabel(url)
	if name == "" || name == t.cfg.Hostname {
		return
	}
	t.cfg.Log.Warn("the tailnet assigned a name different from the one requested",
		"requested", t.cfg.Hostname, "assigned", name,
		"note", "the name was already taken, usually by another installation or by an old node still on the list; "+
			"the public address is the new one, and any bookmark on the old one no longer answers")
	if t.cfg.OnHostname != nil {
		t.cfg.OnHostname(name)
	}
}

// firstLabel pulls `patmon-1` out of `https://patmon-1.tailnet.ts.net`.
func firstLabel(url string) string {
	s := strings.TrimPrefix(url, "https://")
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return ""
}

// publicURL works out the node's public address.
func (t *Tunnel) publicURL(ctx context.Context, srv *tsnet.Server) string {
	if domains := srv.CertDomains(); len(domains) > 0 {
		return "https://" + strings.TrimSuffix(domains[0], ".")
	}
	// Fallback: composed from the node name and the MagicDNS suffix.
	if lc, err := srv.LocalClient(); err == nil {
		if st, err := lc.StatusWithoutPeers(ctx); err == nil && st.Self != nil {
			if name := strings.TrimSuffix(st.Self.DNSName, "."); name != "" {
				return "https://" + name
			}
		}
	}
	return ""
}

func (t *Tunnel) fail(step FailedStep, err error) {
	// **The step is a code, so the log line is in English without anybody
	// having to write it twice.** This used to be one string doing duty both as
	// a log message and as text shown to the user: the first rule wanted
	// English, the second wanted the reader's language, and the two could not
	// be satisfied together while it was a single string.
	t.cfg.Log.Error("remote access failed", "step", string(step), "error", err)
	t.setState(State{
		Phase:      PhaseError,
		Action:     ActionFailed,
		FailedStep: step,
		Detail:     err.Error(),
	})
}
