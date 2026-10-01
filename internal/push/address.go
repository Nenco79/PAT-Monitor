package push

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"syscall"
)

// ErrNotAllowed is an endpoint the monitor will not write to.
var ErrNotAllowed = errors.New("push: not an endpoint on the public Internet")

// **An endpoint is the one address the monitor calls because somebody else
// said so.** Every other request it makes goes to a place written in its own
// code; this one goes wherever the subscription names, and a subscription is
// posted by a page. So it is held to what a push service always is — https on
// the standard port, on the public Internet — and not to a list of which
// companies run one: the browser chooses the service, and a list would absolve
// the next one nobody has heard of and refuse nothing a list does not name.

// endpointURL parses an endpoint and refuses what no push service is.
func endpointURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, fmt.Errorf("%w: %d characters", ErrNotAllowed, len(raw))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAllowed, err)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("%w: https, a host, nothing else", ErrNotAllowed)
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, fmt.Errorf("%w: port %s", ErrNotAllowed, p)
	}
	if a, err := netip.ParseAddr(u.Hostname()); err == nil && !publicAddress(a) {
		return nil, fmt.Errorf("%w: %s", ErrNotAllowed, a)
	}
	return u, nil
}

var (
	// cgnat is 100.64.0.0/10, where the tailnet's IPv4 addresses live and
	// which netip does not count as private.
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
	// tailnet6 is Tailscale's IPv6 range. It is inside fc00::/7, so already
	// private; it is named so that nobody wonders.
	tailnet6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	// nat64 and sixToFour carry an IPv4 address inside an IPv6 one, and what
	// is reached through them is that IPv4 address: a NAT64 gateway on the
	// house's network turns 64:ff9b::c0a8:101 into 192.168.1.1. They are judged
	// by what they carry, so a push service reached through DNS64 still works.
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")

	// notTheInternet is the special-purpose blocks no push service lives in,
	// which netip counts as global unicast: "this network", the IETF's own,
	// documentation, benchmarking, reserved, Teredo, the local-use NAT64
	// prefix whose embedding is the network's choice, and discard.
	notTheInternet = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001::/32"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("100::/64"),
	}
)

// publicAddress says whether an address is on the public Internet: not this
// machine, not the house, not the tailnet, and not one of them reached
// through a translation prefix.
func publicAddress(a netip.Addr) bool {
	a = a.Unmap()
	switch {
	case !a.IsGlobalUnicast(), a.IsPrivate(), a.IsLoopback(), a.IsLinkLocalUnicast():
		return false
	case cgnat.Contains(a), tailnet6.Contains(a):
		return false
	}
	for _, p := range notTheInternet {
		if p.Contains(a) {
			return false
		}
	}
	b := a.As16()
	switch {
	case nat64.Contains(a):
		return publicAddress(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
	case sixToFour.Contains(a):
		return publicAddress(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
	}
	return true
}

// dialPublic refuses, at the moment of connecting, an address that is not
// public. **It is checked on the address dialled and not on the name**, which
// is the only place a name that resolves somewhere else at the second lookup
// cannot get round.
func dialPublic(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotAllowed, address)
	}
	if !publicAddress(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrNotAllowed, ap.Addr())
	}
	return nil
}

// publicDialer is the dialer the push client connects with.
func publicDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Control: dialPublic}
	return d.DialContext
}
