package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/Herculade0/feldcore/internal/citizenid"
)

// statusError is a failure whose HTTP status is part of the result.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }

// managementHost is the host a browser will store an administrator cookie
// under. Forwarded host is used only when the TCP peer is loopback or a
// trusted proxy (Caddy or the containerized tunnel).
func (s *Server) managementHost(r *http.Request) bool {
	if r == nil {
		return false
	}
	ui, expose, admin := s.hostPolicy()
	return citizenid.IsManagementHost(s.cookieHost(r), ui, expose, admin)
}

func (s *Server) hostPolicy() (ui string, expose bool, admin string) {
	if s.citizen != nil {
		return s.citizen.HostPolicy()
	}
	if s.proxy != nil && s.proxy.Store != nil {
		snap := s.proxy.Store.Snapshot()
		ui = citizenid.UIHost(snap)
		expose = snap.Remote.ExposeAdmin
		admin = snap.Remote.AdminHostname
	}
	if s.remote != nil && s.remote.Store != nil {
		snap := s.remote.Store.Snapshot()
		if ui == "" {
			ui = citizenid.UIHost(snap)
		}
		expose = snap.Remote.ExposeAdmin
		admin = snap.Remote.AdminHostname
	}
	return ui, expose, admin
}

func (s *Server) cookieHost(r *http.Request) string {
	if s.peerTrusted(r) {
		if h := firstForwarded(r.Header.Get("X-Forwarded-Host")); h != "" {
			return h
		}
	}
	return r.Host
}

func (s *Server) peerTrusted(r *http.Request) bool {
	ip := peerIP(r)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	if s.citizen != nil && ip != nil {
		return s.citizen.Trusts(r.Context(), ip)
	}
	return false
}

func (s *Server) forwardedHTTPS(r *http.Request) bool {
	if !s.peerTrusted(r) {
		return false
	}
	return strings.EqualFold(firstForwarded(r.Header.Get("X-Forwarded-Proto")), "https")
}

// sessionSecure is true on TLS, on a trusted https hop, and for a hostname
// management host. Loopback and LAN IP literals stay non-Secure so a direct
// http://192.168.x.x:8787 sign-in still sticks.
func (s *Server) sessionSecure(r *http.Request) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	if s.forwardedHTTPS(r) {
		return true
	}
	if citizenid.PlainLAN(s.cookieHost(r)) {
		return false
	}
	return true
}

func (s *Server) refuseForeignHost(w http.ResponseWriter, r *http.Request) bool {
	if s.managementHost(r) {
		return false
	}
	writeError(w, http.StatusForbidden, "Sign in from this server's own address.")
	return true
}

func peerIP(r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func firstForwarded(v string) string {
	return strings.TrimSpace(strings.Split(v, ",")[0])
}
