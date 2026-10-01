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
)

// publicAddress says whether an address is on the public Internet: not this
// machine, not the house, not the tailnet.
func publicAddress(a netip.Addr) bool {
	a = a.Unmap()
	switch {
	case !a.IsGlobalUnicast(), a.IsPrivate(), a.IsLoopback(), a.IsLinkLocalUnicast():
		return false
	case cgnat.Contains(a), tailnet6.Contains(a):
		return false
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
