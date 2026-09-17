package server

import (
	"context"
	"net/http/httptest"
	"net/netip"
	"testing"

	"patmonitor/internal/tunnel"
)

// TestRequestOrigin checks how the origin is classified.
//
// The case that matters is the last: from the funnel, RemoteAddr is the
// Tailscale ingress node and it is the same for everybody. Taking it at face
// value would mean logging every visit from the Internet as though it came from
// the same person, which is exactly the mistake already paid for on the login
// rate limiting.
func TestRequestOrigin(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		funnelSrc  string
		wantKind   string
		wantAddr   string
		wantPublic bool
	}{
		{"loopback", "127.0.0.1:5511", "", "this PC", "127.0.0.1", false},
		{"home network", "192.168.1.42:5511", "", "local network", "192.168.1.42", false},
		{"tailnet node", "100.101.102.103:5511", "", "tailnet", "100.101.102.103", false},
		{"direct public address", "93.184.216.34:5511", "", "Internet", "93.184.216.34", true},
		{"through the funnel", "127.0.0.1:5511", "203.0.113.9:44321", "Internet (Funnel)", "203.0.113.9", true},
	}

	for _, c := range cases {
		r := httptest.NewRequest("GET", "/ws", nil)
		r.RemoteAddr = c.remoteAddr
		if c.funnelSrc != "" {
			ap, err := netip.ParseAddrPort(c.funnelSrc)
			if err != nil {
				t.Fatal(err)
			}
			r = r.WithContext(tunnel.WithSourceAddr(context.Background(), ap))
		}

		got := requestOrigin(r)
		if got.Kind != c.wantKind || got.Addr != c.wantAddr || got.Public() != c.wantPublic {
			t.Errorf("%s: %+v, wanted {%s %s %v}", c.name, got, c.wantKind, c.wantAddr, c.wantPublic)
		}
	}
}

func TestShortUA(t *testing.T) {
	cases := map[string]string{
		"": "not declared",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15": "iPhone",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36":                 "Android",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36":                "Windows",
	}
	for ua, want := range cases {
		if got := shortUA(ua); got != want {
			t.Errorf("shortUA(%q) = %q, wanted %q", ua, got, want)
		}
	}
}
