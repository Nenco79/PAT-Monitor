package rtc

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/pion/webrtc/v4"
)

// readyHub is a hub with its two tracks laid, which is all NewViewer needs to
// make an offer; no frame ever goes through it.
func readyHub(t *testing.T) *Hub {
	t.Helper()
	h := New(Config{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	video, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: h264FmtpLine("42e01f"),
	}, "video", "pat-monitor")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: opusSDPChannels,
		SDPFmtpLine: opusFmtpLine,
	}, "audio", "pat-monitor")
	if err != nil {
		t.Fatal(err)
	}
	h.videoTrack, h.audioTrack, h.profileLevelID = video, audio, "42e01f"
	return h
}

// **Sessions cannot be stacked without end.** Each sends the whole stream up
// the house's uplink and outlives its signalling on purpose, so whoever holds
// a session could open them until the connection saturated and every viewer's
// picture went with it. Past maxOpenViewers NewViewer refuses; closing one
// gives its seat back, or the refusal would be permanent.
//
// **The defect was put back and this test fails with it**: with no count,
// the eleventh session is opened like the first.
func TestViewerSessionsCannotBeStackedWithoutEnd(t *testing.T) {
	h := readyHub(t)
	var open []*Viewer
	t.Cleanup(func() {
		for _, v := range open {
			v.Close()
		}
	})
	for i := range maxOpenViewers {
		v, _, err := h.NewViewer()
		if err != nil {
			t.Fatalf("session %d of %d refused: %v", i+1, maxOpenViewers, err)
		}
		open = append(open, v)
	}
	if v, _, err := h.NewViewer(); !errors.Is(err, ErrTooManyViewers) {
		if v != nil {
			open = append(open, v)
		}
		t.Fatalf("session %d was not refused (err=%v)", maxOpenViewers+1, err)
	}

	open[0].Close()
	open[0].Close() // twice, as the state machine and the WebSocket both do
	v, _, err := h.NewViewer()
	if err != nil {
		t.Fatalf("after one session closed, a new one was refused: %v", err)
	}
	open = append(open, v)
	if n := h.open.Load(); n != maxOpenViewers {
		t.Errorf("%d sessions counted open, wanted %d: a double Close gave back two seats", n, maxOpenViewers)
	}
}

// A browser sends a handful of candidates; past maxRemoteCandidates the agent
// would be probing addresses on somebody's behalf, and the rest are refused.
func TestAViewerCannotHandTheAgentEndlessCandidates(t *testing.T) {
	h := readyHub(t)
	v, _, err := h.NewViewer()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)

	c := webrtc.ICECandidateInit{Candidate: "candidate:1 1 udp 2130706431 192.0.2.1 9 typ host"}
	for range maxRemoteCandidates {
		_ = v.AddICECandidate(c)
	}
	// Pion may refuse these on its own — there is no remote description yet —
	// and that count is the diagnosis's. What the cap turns away must not add
	// to it.
	before := v.candRefused.Load()
	if err := v.AddICECandidate(c); err == nil {
		t.Errorf("candidate %d was taken", maxRemoteCandidates+1)
	}
	if after := v.candRefused.Load(); after != before {
		t.Errorf("the capped candidate was counted as pion's refusal (%d -> %d)", before, after)
	}
}
