package citizenid

import (
	"net"
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

// CanonicalHost is the name a browser stores a cookie under: no port, no
// trailing dot, lower case.
func CanonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(stripPort(host)))
	return strings.TrimSuffix(host, ".")
}

// IsManagementHost reports whether host may carry an administrator session.
// Only loopback names, IP literals, the configured UI hostname, and the
// tunnel admin hostname (when expose-admin is on) qualify. Any other name,
// including one an attacker points at this server, is refused.
func IsManagementHost(host, uiHost string, exposeAdmin bool, adminHost string) bool {
	host = CanonicalHost(host)
	if host == "" {
		return false
	}
	if loopbackName(host) || net.ParseIP(host) != nil {
		return true
	}
	if ui := CanonicalHost(uiHost); ui != "" && host == ui {
		return true
	}
	if exposeAdmin {
		if admin := CanonicalHost(adminHost); admin != "" && host == admin {
			return true
		}
	}
	return false
}

// PlainLAN reports whether host is loopback or a private address, where the
// direct listener has no TLS and a non-Secure cookie is the only way to sign in.
func PlainLAN(host string) bool {
	host = CanonicalHost(host)
	if loopbackName(host) {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func loopbackName(host string) bool {
	switch host {
	case "localhost", "localhost.localdomain", "ip6-localhost", "ip6-loopback":
		return true
	default:
		return false
	}
}

// UIHost is the management hostname from the web settings. A saved name is
// used even when the proxy switch is off. An enabled UI with no name uses
// feldcore.<domain>, which is the address the proxy publishes.
func UIHost(snap store.State) string {
	h := strings.TrimSpace(snap.Web.UIHostname)
	if h == "" && snap.Web.UIEnabled {
		d := strings.TrimSpace(snap.Web.Domain)
		if d == "" {
			d = "home.arpa"
		}
		h = "feldcore." + d
	}
	return h
}

// HostPolicy is the UI hostname and, when expose-admin is on, the tunnel
// admin hostname. The API uses the same list when it issues a session cookie.
func (g *Gateway) HostPolicy() (ui string, expose bool, admin string) {
	if g == nil || g.st == nil {
		return "", false, ""
	}
	snap := g.st.Snapshot()
	return UIHost(snap), snap.Remote.ExposeAdmin, snap.Remote.AdminHostname
}
