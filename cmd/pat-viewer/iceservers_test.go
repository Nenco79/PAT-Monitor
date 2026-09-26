package main

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// **The viewer adopts the STUN servers the monitor sends**, as the page does.
// Without them it gathers only host candidates, and from another network —
// where this tool is run to test the Funnel — it finds no path the page would
// have found.
//
// **The defect was put back and this test fails with it**: with the offer
// handled as before, the peer's configuration keeps no server.
func TestTheViewerTakesTheMonitorsSTUNServers(t *testing.T) {
	pc, err := newPeerConnection("42e01f")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	sent := []webrtc.ICEServer{{URLs: []string{"stun:stun.example.org:3478"}}}
	if err := takeICEServers(pc, sent); err != nil {
		t.Fatal(err)
	}
	got := pc.GetConfiguration().ICEServers
	if len(got) != 1 || len(got[0].URLs) != 1 || got[0].URLs[0] != "stun:stun.example.org:3478" {
		t.Errorf("the peer's servers are %v, wanted the monitor's", got)
	}
}
