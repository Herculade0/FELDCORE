package api

import (
	"net/http"

	"github.com/Herculade0/feldcore/internal/apps"
)

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Settings are not available.")
		return
	}
	view := s.wiz.Settings(r.Context())
	user, _ := s.currentUser(r)
	hours := 168
	feed := apps.FeedURL
	if s.auth != nil {
		hours = s.auth.SessionHours()
	}
	if s.apps != nil {
		feed = s.apps.FeedURL()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"system": map[string]any{
			"hostname": view.Hostname, "timezone": view.Timezone, "zones": view.Zones,
			"ntpServers": view.NTPServers,
		},
		"network": map[string]any{
			"iface": view.Iface, "ip": view.IP, "gateway": view.Gateway, "dns": view.DNS,
			"mode": view.NetMode, "backend": view.Backend,
		},
		"services": view.Services,
		"security": map[string]any{
			"firewall": view.Firewall, "sshPorts": view.SSHPorts, "listenPort": view.ListenPort,
			"rules": view.Rules, "sessionHours": hours, "username": user,
		},
		"updates":  map[string]any{"unattended": view.Unattended, "updateCheck": view.UpdateCheck},
		"apps":     map[string]any{"feedURL": feed, "feedDefault": apps.FeedURL},
		"defaults": map[string]any{"removeImage": view.RemoveImage},
		"files":    map[string]any{"allowLANURLs": s.auth != nil && s.auth.AllowLANURLs()},
	})
}

func (s *Server) handleSettingsSystem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hostname   string `json:"hostname"`
		Timezone   string `json:"timezone"`
		NTPServers string `json:"ntpServers"`
	}
	if !readJSON(w, r, &body) || s.wiz == nil {
		return
	}
	if err := s.wiz.SaveSystem(r.Context(), body.Hostname, body.Timezone, body.NTPServers); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSettingsNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode    string `json:"mode"`
		Iface   string `json:"iface"`
		Address string `json:"address"`
		Gateway string `json:"gateway"`
		DNS     string `json:"dns"`
	}
	if !readJSON(w, r, &body) || s.wiz == nil {
		return
	}
	if err := s.wiz.SaveNetwork(r.Context(), body.Mode, body.Iface, body.Address, body.Gateway, body.DNS); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSettingsService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if !readJSON(w, r, &body) || s.wiz == nil {
		return
	}
	// Turning a service on may apt-install it. That runs as a job so the page
	// can show progress and survive a refresh. Turning it off is a quick
	// systemctl and stays on this request.
	if body.Enabled && s.acceptInstall(w, r, body.Name) {
		return
	}
	if err := s.wiz.SaveService(r.Context(), body.Name, body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSettingsFirewall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) || s.wiz == nil {
		return
	}
	if err := s.wiz.SaveFirewall(r.Context(), body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !readJSON(w, r, &body) || s.auth == nil {
		return
	}
	if err := s.auth.ChangePassword(body.Current, body.Next); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSettingsSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hours int `json:"hours"`
	}
	if !readJSON(w, r, &body) || s.auth == nil {
		return
	}
	if err := s.auth.SetSessionHours(body.Hours); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionHours": s.auth.SessionHours()})
}

func (s *Server) handleSettingsUpdates(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Unattended  bool `json:"unattended"`
		UpdateCheck bool `json:"updateCheck"`
	}
	if !readJSON(w, r, &body) || s.wiz == nil {
		return
	}
	if err := s.wiz.SaveUpdates(r.Context(), body.Unattended, body.UpdateCheck); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSettingsFeed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FeedURL string `json:"feedURL"`
	}
	if !readJSON(w, r, &body) || s.apps == nil || s.wiz == nil {
		return
	}
	if err := s.apps.SetFeedURL(body.FeedURL); err != nil {
		s.fail(w, r, err)
		return
	}
	stored := ""
	if s.apps.FeedURL() != apps.FeedURL {
		stored = s.apps.FeedURL()
	}
	if err := s.wiz.SetFeedPref(stored); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"feedURL": s.apps.FeedURL()})
}

func (s *Server) handleSettingsDefaults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RemoveImage bool `json:"removeImage"`
	}
	if !readJSON(w, r, &body) || s.wiz == nil {
		return
	}
	if err := s.wiz.SetRemoveImage(body.RemoveImage); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removeImage": body.RemoveImage})
}

func (s *Server) handleSettingsShares(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Settings are not available.")
		return
	}
	s.wiz.RecreateShares(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
