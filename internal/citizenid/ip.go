package citizenid

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// clientIP is the address lockout and the audit log attribute to the caller.
// CF-Connecting-IP is used only from cloudflared or loopback (the host
// connector). X-Forwarded-For is used only from Caddy or loopback, and only
// the rightmost address, which is the hop the proxy added. A direct client
// is its own TCP peer.
func (g *Gateway) clientIP(r *http.Request) string {
	return clientIP(r, g.trust, g.tunnel)
}

// ClientIP is clientIP for callers outside this package.
func (g *Gateway) ClientIP(r *http.Request) string {
	return g.clientIP(r)
}

func clientIP(r *http.Request, proxy, tunnel trustPeer) string {
	peer := remoteIP(r)
	if peer == nil {
		return ""
	}
	loop := peer.IsLoopback()
	fromTunnel := loop || (tunnel != nil && tunnel(r.Context(), peer))
	if fromTunnel {
		if ip := parseIPHeader(r.Header.Get("CF-Connecting-IP")); ip != nil {
			return ip.String()
		}
	}
	fromProxy := loop || (proxy != nil && proxy(r.Context(), peer))
	if fromProxy {
		if ip := rightmostForwarded(r.Header.Get("X-Forwarded-For")); ip != nil {
			return ip.String()
		}
	}
	return peer.String()
}

func rightmostForwarded(xff string) net.IP {
	if strings.TrimSpace(xff) == "" {
		return nil
	}
	parts := strings.Split(xff, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	return parseIPHeader(last)
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
