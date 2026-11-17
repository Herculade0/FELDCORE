package citizenid

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// clientIP is the address lockout and the audit log attribute to the caller.
// CF-Connecting-IP and X-Forwarded-For are used only when the TCP peer is
// loopback or Trust reports it (Caddy and the Cloudflare Tunnel connector).
// Any other peer is itself the client. This is the same rule as the publish
// proxy's client address.
func (g *Gateway) clientIP(r *http.Request) string {
	peer := remoteIP(r)
	trusted := peer != nil && (peer.IsLoopback() || (g.trust != nil && g.trust(r.Context(), peer)))
	if trusted {
		if ip := parseIPHeader(r.Header.Get("CF-Connecting-IP")); ip != nil {
			return ip.String()
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := parseIPHeader(first); ip != nil {
				return ip.String()
			}
		}
	}
	if peer != nil {
		return peer.String()
	}
	return ""
}

func remoteIP(r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func parseIPHeader(s string) net.IP {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	return net.ParseIP(s)
}

func localClient(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	return parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast()
}

// trustPeer is the signature stored on the gateway. A nil trust function
// honours forwarded headers from loopback only.
type trustPeer func(context.Context, net.IP) bool
