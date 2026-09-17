package rtc

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// SessionReport summarises how a viewer's session is going.
//
// It exists for a precise reason: the test that matters for this program — a
// phone on a cellular network, Tailscale off, ten minutes — is done away from
// the PC, and without these lines the only report would be "it seemed all right
// to me". An impression does not separate a network losing packets from an
// encoder that does not answer, and those are two faults with two different
// remedies.
//
// The numbers are **differences** from the previous report, not totals since the
// session was born: a total that grows says something happened, but not when,
// and after ten minutes of watching a loss concentrated in the first minute is
// indistinguishable from one spread over all ten.
type SessionReport struct {
	// Path is the ICE path chosen, for example "host <-> srflx". It is the first
	// thing to look at from a cellular network: if it stays empty the problem is
	// upstream of the media. Empty means "ICE has not chosen yet", and it is
	// compared with the empty string: a word in its place would be a decision
	// taken in one language.
	Path string
	// Remote is the address of the remote candidate chosen, with its protocol.
	Remote string
	// RTT is the round-trip time measured on the candidate pair.
	RTT time.Duration

	// Elapsed is the interval the numbers that follow refer to.
	Elapsed time.Duration

	VideoKbps int
	AudioKbps int
	// EstimateKbps is the bandwidth the transport declares available.
	EstimateKbps int

	// Lost are the packets the browser declares it did not receive during the
	// interval, and LossPercent their fraction of the ones sent.
	Lost        int
	LossPercent float64
	JitterMs    float64

	// PLI and NACK are the repair requests that arrived during the interval: the
	// first ask for a whole keyframe, the second for the retransmission of one
	// packet. Many NACKs and few PLIs mean a network that loses something and
	// repairs itself; many PLIs mean it cannot keep up.
	PLI  int
	NACK int
}

// LogArgs returns the report in the shape slog expects.
func (r SessionReport) LogArgs() []any {
	path := r.Path
	if path == "" {
		path = "negotiating"
	}
	return []any{
		"path", path,
		"remote", r.Remote,
		"rtt_ms", r.RTT.Milliseconds(),
		"video_kbps", r.VideoKbps,
		"audio_kbps", r.AudioKbps,
		"estimate_kbps", r.EstimateKbps,
		"lost", r.Lost,
		"loss_pct", fmt.Sprintf("%.2f", r.LossPercent),
		"jitter_ms", fmt.Sprintf("%.1f", r.JitterMs),
		"pli", r.PLI,
		"nack", r.NACK,
	}
}

// sessionSnapshot are the cumulative counters the differences are derived from.
type sessionSnapshot struct {
	at          time.Time
	videoBytes  uint64
	audioBytes  uint64
	packetsSent uint64
	lost        int64
	pli         uint32
	nack        uint32
	valid       bool
}

// selectedPair is the candidate pair ICE is really using.
func (v *Viewer) selectedPair() (*webrtc.ICECandidatePair, error) {
	ice := v.iceTransport()
	if ice == nil {
		return nil, errNoTransport
	}
	return ice.GetSelectedCandidatePair()
}

// selectedPairStats are the measurements on the pair in use: round-trip time and
// the bandwidth the transport declares available.
func (v *Viewer) selectedPairStats() (webrtc.ICECandidatePairStats, bool) {
	ice := v.iceTransport()
	if ice == nil {
		return webrtc.ICECandidatePairStats{}, false
	}
	return ice.GetSelectedCandidatePairStats()
}

var errNoTransport = errors.New("session with no transport")

// estimateKbps is the bandwidth the congestion control estimates for this
// viewer, zero until it has enough to say.
//
// It is **transport** bandwidth, not video: it includes the audio and the
// headers. Comparing it with the encoder's bitrate without remembering that
// makes it look as though we were sending less than the network holds.
func (v *Viewer) estimateKbps() int {
	v.hub.bweMu.Lock()
	defer v.hub.bweMu.Unlock()
	e, ok := v.hub.bwe[v]
	if !ok {
		return 0
	}
	if bps := e.bwe.GetTargetBitrate(); bps > 0 {
		return bps / 1000
	}
	return 0
}

// iceTransport walks back to the session's ICE transport.
//
// The chain is always populated by NewPeerConnection, even with no data channel,
// but it is checked anyway: a nil dereference here would not give a wrong report,
// it would bring the whole process down — that is, it would switch the monitor
// off in order to write a line of log.
func (v *Viewer) iceTransport() *webrtc.ICETransport {
	sctp := v.pc.SCTP()
	if sctp == nil {
		return nil
	}
	dtls := sctp.Transport()
	if dtls == nil {
		return nil
	}
	return dtls.ICETransport()
}

// Since is how long the viewer has been connected.
func (v *Viewer) Since() time.Duration { return time.Since(v.startedAt) }

// ID identifies the session in the logs, so one viewer can be followed across
// several lines when there is more than one.
func (v *Viewer) ID() int64 { return v.id }

// Report measures the session against the last time it was asked for.
//
// Calling it more than once from different goroutines would produce differences
// computed over the wrong interval, so the state is under a lock: the report is
// collected by a single observer per session.
func (v *Viewer) Report() SessionReport {
	now := time.Now()

	var cur sessionSnapshot
	cur.at = now

	rep := SessionReport{}

	// The path is asked of the ICE transport, which knows which one is chosen.
	//
	// Looking for it in the statistics report means walking a **map** in search
	// of a pair in the "succeeded" state, and when there is more than one — the
	// ordinary case, host and srflx both stay valid — Go's iteration order is
	// random: the path "changed" on every sample, back and forth, for a whole
	// night of false lines.
	if pair, err := v.selectedPair(); err == nil && pair != nil {
		rep.Path = pair.Local.Typ.String() + " <-> " + pair.Remote.Typ.String()
		rep.Remote = net.JoinHostPort(pair.Remote.Address, strconv.Itoa(int(pair.Remote.Port))) +
			"/" + pair.Remote.Protocol.String()
	}
	if st, ok := v.selectedPairStats(); ok {
		rep.RTT = time.Duration(st.CurrentRoundTripTime * float64(time.Second))
	}
	// The estimate is asked of gcc and not of the candidate pair: Pion does not
	// populate AvailableOutgoingBitrate, and that field reads without errors
	// while staying at zero — the same trap as the outbound statistics, at
	// another point. A zero in a report is worse than an absent field: it looks
	// like a measurement.
	rep.EstimateKbps = v.estimateKbps()

	// **A snapshot is valid only if the counters were really read.** It used to
	// be marked valid before anything was asked, so a turn in which the
	// statistics answer nothing — the interceptor drops a stream when it is
	// unbound, which happens while the session is being torn down — stored a
	// snapshot of **zeros** that looked like a measurement. The next
	// difference then subtracted a large total from zero in `uint64`, which
	// does not go negative: it wraps, and `VideoKbps` came out at about
	// 1e16 kbit/s.
	//
	// That number does not stay in a debug line. The watcher's loop keeps the
	// last report and hands it to `logSessionEnd`, so it lands in the closing
	// line of the session — which this file's opening paragraph calls the only
	// report there is for the test that counts, a phone on a cellular network
	// far from the PC.
	//
	// It is the rule the three windows in this package already keep, met here
	// for the first time on a pair of counters: **a missing reading is not a
	// reading of zero.** An invalid snapshot costs one interval — the next
	// report has nothing to subtract from and says so with Elapsed zero — and
	// that is the harmless direction.
	if v.stats != nil {
		if s := v.stats.Get(v.videoSSRC); s != nil {
			cur.valid = true
			cur.videoBytes = s.OutboundRTPStreamStats.BytesSent +
				s.OutboundRTPStreamStats.HeaderBytesSent
			cur.packetsSent = s.OutboundRTPStreamStats.PacketsSent
			cur.pli = s.OutboundRTPStreamStats.PLICount
			cur.nack = s.OutboundRTPStreamStats.NACKCount
			// What matters about the video is the loss and the jitter: the audio
			// has small regular packets and stays good even when the picture
			// breaks up.
			cur.lost = s.RemoteInboundRTPStreamStats.PacketsLost
			rep.JitterMs = s.RemoteInboundRTPStreamStats.Jitter * 1000
			// The round-trip time measured over RTCP is the fallback when ICE
			// does not expose it.
			if rep.RTT == 0 {
				rep.RTT = s.RemoteInboundRTPStreamStats.RoundTripTime
			}
		}
		if s := v.stats.Get(v.audioSSRC); s != nil {
			cur.audioBytes = s.OutboundRTPStreamStats.BytesSent +
				s.OutboundRTPStreamStats.HeaderBytesSent
		}
	}

	v.statsMu.Lock()
	prev := v.lastSnap
	v.lastSnap = cur
	v.statsMu.Unlock()

	// **A difference needs two real readings, and it used to need one.**
	// The test was on `prev` alone, so a turn whose own counters could not be
	// read still subtracted: zeros minus a large total, in `uint64`, which
	// wraps instead of going negative. Marking this snapshot invalid protects
	// the **next** report and not this one, which is the half that was written
	// first and the half that does nothing on its own.
	if !prev.valid || !cur.valid {
		// First report, or one where the statistics answered nothing: there is
		// no interval to take differences over, and inventing one from the
		// session's birth would give averages that describe no moment at all.
		rep.Elapsed = 0
		return rep
	}

	rep.Elapsed = now.Sub(prev.at)
	secs := rep.Elapsed.Seconds()
	if secs <= 0 {
		return rep
	}
	rep.VideoKbps = int(float64(cur.videoBytes-prev.videoBytes) * 8 / 1000 / secs)
	rep.AudioKbps = int(float64(cur.audioBytes-prev.audioBytes) * 8 / 1000 / secs)
	rep.PLI = int(cur.pli - prev.pli)
	rep.NACK = int(cur.nack - prev.nack)
	rep.Lost = int(cur.lost - prev.lost)

	if sent := cur.packetsSent - prev.packetsSent; sent > 0 && rep.Lost > 0 {
		rep.LossPercent = 100 * float64(rep.Lost) / float64(sent)
	}
	return rep
}

// sessionCounter numbers the sessions. It lives here and not in the hub because
// it serves only the logs.
var sessionCounter struct {
	sync.Mutex
	n int64
}

func nextSessionID() int64 {
	sessionCounter.Lock()
	defer sessionCounter.Unlock()
	sessionCounter.n++
	return sessionCounter.n
}
