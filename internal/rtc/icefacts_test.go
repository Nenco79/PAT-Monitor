package rtc

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// report builds a statistics report out of entries, the way pion hands one over:
// a map, whose iteration order Go deliberately randomises.
func report(entries ...webrtc.Stats) webrtc.StatsReport {
	r := webrtc.StatsReport{}
	for i, e := range entries {
		r[string(rune('a'+i))] = e
	}
	return r
}

func cand(kind webrtc.StatsType, typ webrtc.ICECandidateType) webrtc.ICECandidateStats {
	return webrtc.ICECandidateStats{Type: kind, CandidateType: typ}
}

func TestTheFactsCountBothSidesSeparately(t *testing.T) {
	f := iceFactsFrom(report(
		cand(webrtc.StatsTypeLocalCandidate, webrtc.ICECandidateTypeHost),
		cand(webrtc.StatsTypeLocalCandidate, webrtc.ICECandidateTypeHost),
		cand(webrtc.StatsTypeLocalCandidate, webrtc.ICECandidateTypeSrflx),
		cand(webrtc.StatsTypeRemoteCandidate, webrtc.ICECandidateTypeHost),
		cand(webrtc.StatsTypeRemoteCandidate, webrtc.ICECandidateTypeRelay),
	))
	if f.LocalHost != 2 || f.LocalSrflx != 1 || f.LocalRelay != 0 {
		t.Errorf("ours: %+v", f)
	}
	if f.RemoteHost != 1 || f.RemoteRelay != 1 || f.RemoteSrflx != 0 {
		t.Errorf("theirs: %+v", f)
	}
}

// A peer-reflexive candidate is not something the browser told us: the agent
// learnt it from an incoming connectivity check. Measured on two real peer
// connections with the signalling of candidates deliberately cut — the remote
// list was not empty, it held exactly one prflx. Counting it among the signalled
// ones would absolve precisely the case these facts are collected for.
func TestAPeerReflexiveCandidateWasNotSignalled(t *testing.T) {
	f := iceFactsFrom(report(
		cand(webrtc.StatsTypeLocalCandidate, webrtc.ICECandidateTypeHost),
		cand(webrtc.StatsTypeRemoteCandidate, webrtc.ICECandidateTypePrflx),
	))
	if f.RemotePrflx != 1 {
		t.Fatalf("the prflx was not counted: %+v", f)
	}
	if n := f.RemoteHost + f.RemoteSrflx + f.RemoteRelay; n != 0 {
		t.Errorf("a prflx was counted among the candidates the browser sent: %d", n)
	}
}

// Knocked without Answered is the whole signature of a path that exists on paper
// and carries nothing, so the two must be counted apart.
func TestAPairThatWasKnockedOnIsNotAPairThatAnswered(t *testing.T) {
	f := iceFactsFrom(report(
		webrtc.ICECandidatePairStats{RequestsSent: 9, ResponsesReceived: 0},
		webrtc.ICECandidatePairStats{RequestsSent: 4, ResponsesReceived: 2},
		webrtc.ICECandidatePairStats{},
	))
	if f.Pairs != 3 || f.Knocked != 2 || f.Answered != 1 {
		t.Errorf("pairs=%d knocked=%d answered=%d", f.Pairs, f.Knocked, f.Answered)
	}
}

// The report also carries everything else the session produces, and none of it
// is a candidate.
func TestTheOtherStatisticsAreNotCounted(t *testing.T) {
	f := iceFactsFrom(report(
		webrtc.TransportStats{},
		webrtc.OutboundRTPStreamStats{},
		cand(webrtc.StatsTypeLocalCandidate, webrtc.ICECandidateTypeHost),
	))
	if f.LocalHost != 1 || f.Pairs != 0 {
		t.Errorf("%+v", f)
	}
}

// The snapshot is preferred over the live reading, and this test proves it the
// only way that cannot be argued with: the Viewer has no peer connection at all,
// so anybody who takes the live road here panics. Putting the defect back —
// reading the agent first — fails with a nil dereference on the line that does
// it.
func TestOnceRememberedTheAgentIsNotAskedAgain(t *testing.T) {
	v := &Viewer{}
	want := ICEFacts{LocalHost: 2, LocalSrflx: 1, RemoteHost: 3, Sent: 5, STUNUsable: 2}
	v.iceSnap.Store(&want)

	got := v.ICEFacts()
	if got != want {
		t.Errorf("ICEFacts = %+v, want %+v", got, want)
	}
}
