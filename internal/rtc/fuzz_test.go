package rtc

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// FuzzWhatTheBrowserAnswers hands a viewer an answer and a candidate nobody
// negotiated.
//
// **Both strings come from the page**, over the signalling socket, and go to
// pion as they are: the answer to SetRemoteDescription, the candidate to
// AddICECandidate. A refusal is fine and expected; what is not fine is a panic,
// which in one of pion's own goroutines ends the process with the camera on,
// and a viewer that cannot be closed afterwards.
func FuzzWhatTheBrowserAnswers(f *testing.F) {
	h := readyHub(f)
	answer := answerTo(f, h)

	f.Add(answer, "candidate:1 1 udp 2122260223 192.0.2.10 50000 typ host", "0")
	f.Add(answer, "", "")
	f.Add("v=0\r\n", "candidate:", "9")
	f.Add("", "candidate:1 1 tcp 1 ::1 9 typ srflx raddr 0.0.0.0 rport 0 tcptype active", "1")
	f.Add("v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\na=rtpmap:111 opus/48000/2\r\na=setup:active\r\n",
		"candidate:1 1 udp 1 192.0.2.10 1 typ relay", "0")

	f.Fuzz(func(t *testing.T, sdp, candidate, mid string) {
		v, _, err := h.NewViewer()
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		_ = v.SetAnswer(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
		_ = v.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate, SDPMid: &mid})
	})
}

// FuzzTheAnswerAsText parses the answer and nothing else.
//
// **FuzzWhatTheBrowserAnswers pays a quarter of a second per input**, since
// every one needs a viewer and an offer, so in minutes it tries dozens. The
// parser is where a strange string does its damage and it needs no viewer, so
// it gets a target of its own that tries millions.
func FuzzTheAnswerAsText(f *testing.F) {
	h := readyHub(f)
	f.Add(answerTo(f, h))
	f.Add("v=0\r\n")
	f.Add("v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\n")

	f.Fuzz(func(t *testing.T, sdp string) {
		desc := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}
		_, _ = desc.Unmarshal()
	})
}

// answerTo makes the answer a browser would give to a viewer's offer, so that
// the fuzzer starts from something pion accepts and mutates inward.
func answerTo(tb testing.TB, h *Hub) string {
	tb.Helper()
	v, offer, err := h.NewViewer()
	if err != nil {
		tb.Fatal(err)
	}
	defer v.Close()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		tb.Fatal(err)
	}
	defer pc.Close()
	if err := pc.SetRemoteDescription(*offer); err != nil {
		tb.Fatal(err)
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		tb.Fatal(err)
	}
	return ans.SDP
}
