package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ReachCode says whether the public address has been **tried**, not merely
// opened.
//
// It is the encoder rule applied to the tunnel: what a command did is weighed,
// not what it answered. A valid certificate, an ingress grant and an accepted
// serve config are three answers, and together they said "active" three seconds
// after start — while from outside the address becomes reachable **minutes**
// later, because Tailscale publishes the name in public DNS only while the node
// is online with ingress active. Measured: the name absent at +5 minutes from
// start, present at +11, plus the negative cache of the viewer's resolver. In
// that window the monitor declared ready a thing that was not, and whoever had
// the phone in their hand went looking for the fault in their own network.
//
// **It is not a phase**, and that is a decision: `running` goes on meaning what
// it has always meant, and the `phase === 'running'` comparisons across the two
// pages are untouched. One more phase would have had to turn every one of them
// into `running || verified`, which is precisely the class of fault
// `phases_test.go` exists to watch — and on a machine where the check cannot be
// made the monitor would have sat for ever in a second-class state.
type ReachCode string

const (
	// ReachUnknown: not checkable from this machine. **It is not a fault**, and
	// it is the zero of the type on purpose: "I do not know" is not "it does not
	// work", and whoever has no evidence has no news. The badge goes back to
	// saying the phase.
	ReachUnknown ReachCode = ""
	// ReachChecking: the check is under way and has not succeeded yet.
	//
	// These minutes used to be ReachUnknown, that is, silence, and the badge
	// fell back on the phase — which says "active". So in the one window where
	// the address really does not answer, the monitor went on declaring it
	// ready: the optimistic claim taken out of the outcome and left in the wait.
	//
	// **A declared wait is information; a silent wait is the old lie told more
	// quietly.**
	ReachChecking ReachCode = "checking"
	// ReachVerified: a request of ours went out to the Internet, came back in
	// through the funnel and found us.
	ReachVerified ReachCode = "verified"
	// ReachFailed: the check has failed for long enough that this is no longer
	// the DNS publication window.
	ReachFailed ReachCode = "unreachable"
)

// AllReaches is the authoritative list, the one the catalogues expand their
// keys from. ReachUnknown is not in it: it is not shown, it is kept quiet.
func AllReaches() []ReachCode {
	return []ReachCode{ReachChecking, ReachVerified, ReachFailed}
}

const (
	// reachPath is served **only by the funnel listener**, that is, it exists
	// solely on the port facing the Internet. It does not go through the
	// server's mux, and that is not for convenience: the tunnel owns the nonce
	// and owns that listener, so the check sits wholly on one side. Registered
	// in the server it would be reachable from home too, where it would prove
	// nothing, and it would be one more route to keep in step.
	reachPath = "/.well-known/patmon-reach/"
	// reachDirect is the answer to whoever reaches us **without** coming
	// through the funnel, and it is the outcome that guards against the most
	// likely false alarm: on a machine where the Tailscale client is also
	// installed, MagicDNS resolves the public name to the tailnet address, the
	// request arrives all the same and has crossed nothing. Without this
	// distinction such a machine would declare itself unreachable with a
	// perfectly working funnel.
	reachDirect = "direct"

	// The check repeats until it succeeds, and then goes on more slowly: a
	// tunnel can fall over, and "verified" said once and kept for ever would be
	// a declaration standing in for a measurement all over again.
	reachFirstWait = 10 * time.Second
	reachMaxWait   = 2 * time.Minute
	reachRecheck   = 10 * time.Minute
	reachTimeout   = 15 * time.Second

	// Until this expires a failure is not news: it is the window in which the
	// public name has not been published yet. It is the same shape as the grace
	// on the alerts and the one on the microphone recheck — **it expires by
	// itself**, rather than being switched off by somebody.
	//
	// **Six minutes is too short, and the measurement three paragraphs above is
	// what says so**: the name was absent at +5 minutes from start and present
	// at +11. With a six-minute grace the first attempt past it falls around six
	// and a half — that is, **every slow cold start would declare unreachable a
	// tunnel about to publish itself**, with banner, chime and an amber icon.
	// Fifteen minutes sit half again above the worst case observed.
	//
	// The asymmetry is the usual one: waiting longer delays the discovery of a
	// fault that nobody could repair in the meantime, while announcing it too
	// early burns the only alarm the program has.
	reachGrace = 15 * time.Minute

	// How many failures in a row it takes to declare unreachable, once the
	// grace is past. **One is not enough**: an HTTP request that does not come
	// back is also a home network flickering for two seconds, and with the slow
	// recheck that single stumble would nail the alarm up for ten minutes. It is
	// the same rule by which crying wants two windows and a bark one: what lasts
	// is confirmed, what passes is not.
	reachFailsToConfirm = 2
)

// errReachDirect separates "not checkable from here" from "does not answer",
// which is the difference between a machine set up a particular way and a
// broken tunnel. They are two different pieces of news, and only the second is
// an alarm.
var errReachDirect = errors.New("reached over the tailnet, not through the funnel")

// newNonce is the value the check expects to find again, and it changes on
// every attempt: that way no answer held by an intermediary can pass for a
// verification made now.
func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// serveReach intercepts the check before it reaches the monitor.
//
// A nonce that does not match gets no confirmation of its own existence: it
// carries on to the normal handler, that is, it sees what anybody opening the
// public address would see. There is nothing to protect here — the answer only
// says "we are reachable", which the public address declares by itself — but a
// route that answers differently to a guessed path is one more detail handed to
// whoever is probing.
func (t *Tunnel) serveReach(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked, ok := strings.CutPrefix(r.URL.Path, reachPath)
		want, _ := t.reachNonce.Load().(string)
		if !ok || asked == "" || want == "" || asked != want {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// **Whoever answers also says where they were reached from.** The
		// origin is put in the context by the tunnel from `ipn.FunnelConn.Src`,
		// not by a header, which the caller would write.
		if _, viaFunnel := SourceAddr(r.Context()); !viaFunnel {
			_, _ = io.WriteString(w, reachDirect)
			return
		}
		_, _ = io.WriteString(w, asked)
	})
}

// probeReach goes out to the Internet and tries to come back in.
//
// The client is the system one on purpose: it has to travel the road the viewer
// travels — public DNS, Tailscale ingress node, TLS — and not the tsnet node's
// dialer, which would come back to us by the inside route and demonstrate only
// that we can talk to ourselves.
func (t *Tunnel) probeReach(ctx context.Context, base string) (ReachCode, error) {
	n, err := newNonce()
	if err != nil {
		return ReachUnknown, err
	}
	t.reachNonce.Store(n)

	ctx, cancel := context.WithTimeout(ctx, reachTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(base, "/")+reachPath+n, nil)
	if err != nil {
		return ReachUnknown, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ReachFailed, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ReachFailed, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	// The body is ours and short: read just enough to recognise it, because on
	// the other side of this request is the Internet.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ReachFailed, err
	}
	switch strings.TrimSpace(string(body)) {
	case n:
		return ReachVerified, nil
	case reachDirect:
		// We reached ourselves by the inside route: the check did not fail, it
		// could not be made.
		return ReachUnknown, errReachDirect
	default:
		return ReachFailed, errors.New("someone else answered")
	}
}

// reachVerdict turns the outcome of one attempt into what gets declared.
//
// It is a pure function, kept out of the loop for one reason: the branch that
// matters is the grace, and testing it inside `watchReach` would mean a test
// that waits fifteen minutes — that is, a test nobody runs. Here the same
// question is asked with a parameter.
func reachVerdict(code ReachCode, err error, sinceStart time.Duration, failsInARow int) ReachCode {
	switch {
	case code == ReachVerified:
		return ReachVerified
	case errors.Is(err, errReachDirect):
		// It did not fail: it could not be made, and never will be from this
		// machine. Saying "checking" would promise an outcome that is not
		// coming.
		return ReachUnknown
	case sinceStart < reachGrace:
		// **Inside the grace a failure is not a fault**: it is the window in
		// which Tailscale has not published the name in public DNS yet.
		// Measured on this machine, it runs past ten minutes. Nor is it
		// silence: the check is still going, and whoever is watching has a
		// right to know.
		return ReachChecking
	case failsInARow < reachFailsToConfirm:
		// Past the grace, but a single stumble is not a fallen tunnel: stay on
		// "checking" and ask again soon.
		return ReachChecking
	default:
		return ReachFailed
	}
}

// watchReach keeps the check alive for the life of the tunnel.
//
// The cadence is not dictated by whoever would like to know: retries slow down
// as attempts fail, and once verified it moves to the slow recheck. The log
// follows the shape of the alerts — written when the condition appears and when
// it clears, never while it lasts — otherwise a night of fallen tunnel would be
// fifteen hundred identical lines.
func (t *Tunnel) watchReach(ctx context.Context, base string) {
	if base == "" {
		return
	}
	start := time.Now()
	wait := reachFirstWait
	var saidDirect bool
	var failsInARow int

	// **Checking is declared before it begins.** These are the seconds in which
	// the public address exists and does not answer yet: leaving them without an
	// outcome would mean showing the phase, which says "active".
	t.setReach(ReachChecking, base)

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		code, err := t.probeReach(ctx, base)
		if ctx.Err() != nil {
			return
		}

		sinceStart := time.Since(start)
		if code == ReachVerified || errors.Is(err, errReachDirect) {
			failsInARow = 0
		} else {
			failsInARow++
		}

		switch verdict := reachVerdict(code, err, sinceStart, failsInARow); {
		case verdict == ReachVerified:
			t.setReach(ReachVerified, base)
			wait = reachRecheck

		case errors.Is(err, errReachDirect):
			// Back to "I do not know", which here is final: from this machine
			// the check will never succeed, and leaving "checking" would
			// promise an outcome that does not arrive.
			t.setReach(ReachUnknown, base)
			// The line cannot come from setReach, which knows only that the
			// outcome changed: it is said once and not repeated.
			if !saidDirect {
				saidDirect = true
				t.cfg.Log.Info("cannot verify the public address from this machine",
					"reason", "the Tailscale client installed here resolves it over the tailnet",
					"note", "the funnel itself may be fine; try from a device without Tailscale")
			}
			wait = reachRecheck

		default:
			t.setReach(verdict, base, "error", err)
			if verdict == ReachChecking {
				t.cfg.Log.Debug("public address not reachable yet",
					"since_start", sinceStart.Round(time.Second), "error", err)
			}
			// **After a failure the cadence goes back to the retry one.** With
			// doubling alone, a successful verification leaves `wait` at ten
			// minutes, so `wait < reachMaxWait` is false by construction — the
			// guard is dead code and a tunnel that has just fallen gets retried
			// ten minutes later. It is also the other half of confirmation:
			// without a second attempt soon after, asking for two failures in a
			// row would mean twenty minutes before saying so.
			if wait >= reachRecheck {
				wait = reachFirstWait
			} else if wait < reachMaxWait {
				wait *= 2
			}
		}
	}
}

// setReach changes only the outcome of the check, and writes only when it
// changes.
//
// It does not go through setState, because that replaces the whole state:
// called from here it would throw away the phase, the address and the warning
// gathered when the tunnel opened.
func (t *Tunnel) setReach(code ReachCode, url string, attrs ...any) {
	t.mu.Lock()
	// **This outcome describes an open tunnel**, and on any other phase it is
	// news about a world that no longer exists: written over a PhaseError, the
	// badge would say "does not answer from outside" instead of the real error.
	//
	// The guard is here rather than in the goroutine bookkeeping because this is
	// where the value is written: cancelling the context still leaves the round
	// already begun, and a microsecond race against a `defer` is exactly the
	// kind of thing that gets lost at every reshuffle.
	if t.state.Phase != PhaseRunning {
		t.mu.Unlock()
		return
	}
	changed := t.state.Reach != code
	t.state.Reach = code
	t.mu.Unlock()

	if !changed {
		return
	}
	// **Only the two outcomes are announced.** "Checking" and "I do not know"
	// are things to show whoever is watching now, not to leave in tonight's log:
	// whoever rereads it in the morning is looking for what happened, and a wait
	// resolved in eleven seconds is nothing happening. The branch that stays
	// quiet is also the one that runs on every start.
	attrs = append([]any{"url", url}, attrs...)
	switch code {
	case ReachVerified:
		t.cfg.Log.Info("public address verified from the Internet", attrs...)
	case ReachFailed:
		t.cfg.Log.Warn("public address does not answer from the Internet", attrs...)
	}
}
