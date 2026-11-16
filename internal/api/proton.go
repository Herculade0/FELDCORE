package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/vpn"
)

func (s *Server) handleVPN(w http.ResponseWriter, r *http.Request) {
	if s.vpn == nil {
		writeError(w, http.StatusNotFound, "VPN is not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.vpn.View())
}

func (s *Server) handleVPNEnabled(w http.ResponseWriter, r *http.Request) {
	if s.vpn == nil {
		writeError(w, http.StatusNotFound, "VPN is not available.")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.vpn.SetEnabled(r.Context(), body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.vpn.View())
}

func (s *Server) handleVPNParse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Conf   string `json:"conf"`
		Preset string `json:"preset"`
	}
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	parsed, err := vpn.Parse(body.Conf, body.Preset)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The private key stays in the browser until the tunnel is saved. It is
	// not echoed back.
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": parsed.Provider, "detected": parsed.Detected,
		"server": parsed.Server, "country": parsed.Country, "natpmp": parsed.NATPMP,
		"ignoredHooks": parsed.IgnoredHooks, "extraPeers": parsed.ExtraPeers,
		"address": parsed.Config.Address, "dns": parsed.Config.DNS,
		"endpoint": parsed.Config.Endpoint, "allowedIPs": parsed.Config.AllowedIPs,
		"keepalive": parsed.Config.Keepalive, "mtu": parsed.Config.MTU,
		"peerKey": parsed.Config.PeerPublic, "hasKey": parsed.Config.PrivateKey != "",
		"hasPsk": parsed.Config.Preshared != "",
	})
}

func (s *Server) handleVPNSave(w http.ResponseWriter, r *http.Request) {
	if s.vpn == nil {
		writeError(w, http.StatusNotFound, "VPN is not available.")
		return
	}
	var body vpn.SaveRequest
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	if r.Method == http.MethodPut {
		body.ID = chi.URLParam(r, "id")
	}
	t, err := s.vpn.Save(r.Context(), body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleVPNDelete(w http.ResponseWriter, r *http.Request) {
	if s.vpn == nil {
		writeError(w, http.StatusNotFound, "VPN is not available.")
		return
	}
	if err := s.vpn.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleVPNTunnelEnabled(w http.ResponseWriter, r *http.Request) {
	if s.vpn == nil {
		writeError(w, http.StatusNotFound, "VPN is not available.")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.vpn.SetTunnelEnabled(r.Context(), id, body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.vpn.Status(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleVPNStatus(w http.ResponseWriter, r *http.Request) {
	if s.vpn == nil {
		writeError(w, http.StatusNotFound, "VPN is not available.")
		return
	}
	st, err := s.vpn.Status(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusNotFound, "Backups are not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.backups.View())
}

func (s *Server) handleBackupsSave(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusNotFound, "Backups are not available.")
		return
	}
	var body backup.Settings
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.backups.Save(body); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.backups.View())
}

func (s *Server) handleBackupsTest(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusNotFound, "Backups are not available.")
		return
	}
	msg, err := s.backups.Test(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) handleBackupsRun(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusNotFound, "Backups are not available.")
		return
	}
	msg, err := s.backups.Run(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.mail.View(r.Context()))
}

func (s *Server) handleNotificationsSave(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	var body notify.Settings
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.mail.Save(r.Context(), body); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.mail.View(r.Context()))
}

func (s *Server) handleNotificationsTest(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	if err := s.mail.Test(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Test message sent."})
}

func (s *Server) syncVPN(r *http.Request, spec docker.Spec) error {
	if s.vpn == nil {
		return nil
	}
	if spec.VPN == "" {
		if spec.Name != "" {
			return s.vpn.Sync(r.Context(), spec.Name, "", nil)
		}
		return nil
	}
	var ports []vpn.Port
	for _, c := range spec.Configs {
		if c.Type != docker.TypePort {
			continue
		}
		ports = append(ports, vpn.Port{
			HostIP: c.HostIP, Host: c.HostPort, Container: c.ContainerPort, Proto: c.Proto,
		})
	}
	return s.vpn.Sync(r.Context(), spec.Name, spec.VPN, ports)
}

func (s *Server) detachVPN(r *http.Request, name string) {
	if s.vpn == nil || name == "" {
		return
	}
	_ = s.vpn.Sync(r.Context(), name, "", nil)
}

func (s *Server) quietName(r *http.Request, id string) {
	if s.mail == nil {
		return
	}
	name := strings.TrimPrefix(id, "/")
	if s.docker != nil {
		if cs, err := s.docker.Containers(r.Context()); err == nil {
			for _, c := range cs {
				if c.ID == id || c.Name == id {
					name = c.Name
					break
				}
			}
		}
	}
	s.mail.ExpectQuiet(name)
}
