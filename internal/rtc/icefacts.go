package rtc

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

// ICEFacts is what the ICE agent had in hand, and it contains no judgement.
//
// It exists for the one moment this program cannot investigate afterwards: a
// viewer whose media never passed. The page says two words, the watcher is in
// another house, and on the way back the only thing left is the log — so the
// numbers that separate the causes have to be in it, at normal level, because
// finding them otherwise would mean having switched debug on **before** the
// failure.
//
// **The facts are asked of the agent, not tallied beside it.** Pion's own
// statistics are what the ICE agent really did with the candidates; a count of
// ours would be a second opinion, and the day the two disagreed the wrong one
// would be the one in the log. Verified on two real peer connections that the
// agent populates all of them — local candidates, remote candidates, pairs, and
// the checks sent and answered.
//
// The two exceptions are Sent and Refused, which the agent cannot know: they are
// about the **signalling channel**, that is, what the page managed to tell us.
type ICEFacts struct {
	// The candidates we gathered. No LocalSrflx means we never learnt the
	// address we have on the Internet.
	LocalHost, LocalSrflx, LocalRelay int

	// The candidates the agent holds for the other end. RemotePrflx is the
	// interesting one: a peer-reflexive candidate is **not** something the
	// browser told us, it is an address the agent learnt from an incoming
	// connectivity check. Measured on two real peer connections with the
	// signalling of candidates deliberately cut: the remote list was not empty,
	// it held one prflx. Read as "the browser sent us something", it would have
	// absolved exactly the case it was collected for.
	RemoteHost, RemoteSrflx, RemoteRelay, RemotePrflx int

	// Pairs is how many candidate pairs the agent formed, Knocked how many of
	// them it sent at least one connectivity check on, and Answered how many
	// ever answered. Knocked without Answered is the signature of a path that
	// exists on paper and does not carry a packet.
	Pairs, Knocked, Answered int

	// Sent is how many candidates the page really sent over the signalling
	// channel, and Refused how many of those pion would not take.
	Sent, Refused int

	// STUNUsable is how many ICE servers the agent was given — that is, how many
	// survived validation at start-up, **not** how many are written in the file.
	// The two differ exactly when an entry was refused, and that refusal has a
	// line of its own; naming the field after the file would make this one say
	// something false about it. Zero is a configuration that cannot work from
	// outside, and it is not a failure of the network.
	STUNUsable int
}

// LogArgs renders the facts for one log line.
//
// They are grouped into three values rather than spread over twelve keys: this
// line is read in the morning, on a narrow terminal, and the question asked of
// it — "which side had what?" — is answered by reading the two lists next to
// each other.
func (f ICEFacts) LogArgs() []any {
	return []any{
		"ours", fmt.Sprintf("host:%d srflx:%d relay:%d", f.LocalHost, f.LocalSrflx, f.LocalRelay),
		"theirs", fmt.Sprintf("host:%d srflx:%d relay:%d prflx:%d",
			f.RemoteHost, f.RemoteSrflx, f.RemoteRelay, f.RemotePrflx),
		"pairs", f.Pairs,
		"knocked", f.Knocked,
		"answered", f.Answered,
		"they_sent", f.Sent,
		"we_refused", f.Refused,
		"stun_usable", f.STUNUsable,
	}
}

// ICEFacts is what the agent knew about this session's negotiation.
//
// **It is a snapshot once the session has closed, and that is not caution.**
// Measured on a real peer connection: after Close, GetStats answers zeros for
// everything — candidates, pairs, checks — and zeros here are not a missing
// measurement, they are an accusation, because the first cause the zeros select
// is "the viewer sent no candidates". The session is closed by the very failure
// this is asked about, so the live reading would be wrong in exactly the case it
// exists for. See iceSnap, and Close, which remembers before it tears anything
// down.
func (v *Viewer) ICEFacts() ICEFacts {
	if f := v.iceSnap.Load(); f != nil {
		return *f
	}
	return v.iceFactsNow()
}

// rememberICEFacts freezes the facts while the agent is still there to answer.
func (v *Viewer) rememberICEFacts() {
	f := v.iceFactsNow()
	v.iceSnap.Store(&f)
}

// iceFactsNow interrogates the agent, and is only right while it exists.
func (v *Viewer) iceFactsNow() ICEFacts {
	f := iceFactsFrom(v.pc.GetStats())
	f.Sent = int(v.candSent.Load())
	f.Refused = int(v.candRefused.Load())
	f.STUNUsable = len(v.hub.ICEServers())
	return f
}

// iceFactsFrom counts a statistics report, and is separate from the method so
// that the counting can be tested without a peer connection — the report is the
// system's answer, the counting is ours.
//
// Iterating the map is safe here in the way it was not for the selected pair:
// this asks "how many of each", which does not depend on the order Go hands them
// out, while the selected pair asked "which one", and there the random order
// produced a path that changed at every sample.
func iceFactsFrom(report webrtc.StatsReport) ICEFacts {
	var f ICEFacts
	for _, s := range report {
		switch st := s.(type) {
		case webrtc.ICECandidateStats:
			local := st.Type == webrtc.StatsTypeLocalCandidate
			switch st.CandidateType {
			case webrtc.ICECandidateTypeHost:
				if local {
					f.LocalHost++
				} else {
					f.RemoteHost++
				}
			case webrtc.ICECandidateTypeSrflx:
				if local {
					f.LocalSrflx++
				} else {
					f.RemoteSrflx++
				}
			case webrtc.ICECandidateTypeRelay:
				if local {
					f.LocalRelay++
				} else {
					f.RemoteRelay++
				}
			case webrtc.ICECandidateTypePrflx:
				if !local {
					f.RemotePrflx++
				}
			}
		case webrtc.ICECandidatePairStats:
			f.Pairs++
			if st.RequestsSent > 0 {
				f.Knocked++
			}
			if st.ResponsesReceived > 0 {
				f.Answered++
			}
		}
	}
	return f
}
