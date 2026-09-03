package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// trustedProxies is an explicit allowlist of CIDR ranges whose immediate
// TCP connection is trusted to supply an accurate X-Forwarded-For header.
// The zero value trusts nothing, so X-Forwarded-For is always ignored by
// default — a deployment must opt in.
type trustedProxies struct {
	nets []*net.IPNet
}

// parseTrustedProxies builds a trustedProxies from CIDR strings (e.g.
// "127.0.0.1/32"). An empty list is valid and trusts nothing.
func parseTrustedProxies(cidrs []string) (*trustedProxies, error) {
	tp := &trustedProxies{}
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("invalid --trusted-proxy value %q: %w", c, err)
		}
		tp.nets = append(tp.nets, n)
	}
	return tp, nil
}

func (tp *trustedProxies) contains(ip net.IP) bool {
	if tp == nil {
		return false
	}
	for _, n := range tp.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP extracts the address rate limiting should key on: the direct TCP
// peer, unless that peer is an explicitly trusted proxy, in which case the
// LAST address in X-Forwarded-For is used instead.
//
// It must be the last entry, not the first: a reverse proxy such as Caddy
// APPENDS the peer address it itself observed to any existing
// X-Forwarded-For value rather than replacing it — see Caddy's own
// documented behavior for the reverse_proxy directive. For this function's
// single-trusted-hop model (Internet -> Caddy -> 127.0.0.1:this server),
// that means the last entry is the one our trusted proxy vouches for, and
// every entry before it was supplied by whoever connected to Caddy and is
// exactly as trustworthy as any other client-controlled header — i.e. not
// at all. A client that spoofs "X-Forwarded-For: 9.9.9.9" when connecting
// directly to Caddy ends up with Caddy appending its own view, producing
// "9.9.9.9, <the spoofing client's real address>" — taking the first entry
// would trust the spoofed value; taking the last one does not.
//
// A malformed or absent XFF value falls back to the peer address (Caddy's
// own) rather than failing the request or trying an earlier, untrusted
// entry: this function feeds a rate limiter, not authentication, so
// fail-safe (rate-limit by the proxy's own address) is preferable to
// fail-closed here, and falling through to an earlier entry would reopen
// exactly the spoofing hole this function exists to close.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	peerIP := net.ParseIP(host)
	if peerIP == nil || !s.trustedProxies.contains(peerIP) {
		return host
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if net.ParseIP(last) == nil {
		return host // malformed trailing entry: safe fallback, never try an earlier (untrusted) entry
	}
	return last
}
