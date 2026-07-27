package admin

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP is the address the login throttle counts against.
//
// X-Forwarded-For is read only when the peer is one of the configured proxies.
// The header is caller-supplied, so believing it unconditionally would let
// anyone choose which bucket they are counted in — a throttle that is bypassed
// by setting a header is worse than none, because it looks like protection.
// With no proxies configured, which is the default, the header is never read.
func (a *Admin) clientIP(r *http.Request) string {
	peer := hostOnly(r.RemoteAddr)
	if len(a.trusted) == 0 || !a.isTrusted(peer) {
		return peer
	}

	// Everything to the right was appended by infrastructure we trust. Walking
	// left, the first entry that is not itself trusted is the furthest back the
	// chain can be believed; anything beyond it was supplied by the client.
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			// A malformed hop means the chain cannot be trusted past this
			// point, and the peer is the honest answer.
			return peer
		}
		if !a.isTrusted(addr.String()) {
			return addr.String()
		}
	}
	return peer
}

func (a *Admin) isTrusted(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, p := range a.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// hostOnly strips the port RemoteAddr carries, tolerating an address without one.
func hostOnly(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}
