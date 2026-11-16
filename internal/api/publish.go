package api

import (
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/docker"
	netname "github.com/Herculade0/feldcore/internal/names"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/snapshots"
)

func (s *Server) handleRemote(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeError(w, http.StatusNotFound, "Remote access is not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.remote.View(r.Context()))
}

func (s *Server) handleRemoteSave(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeError(w, http.StatusNotFound, "Remote access is not available.")
		return
	}
	var body struct {
		Mode             string `json:"mode"`
		APIToken         string `json:"apiToken"`
		TunnelToken      string `json:"tunnelToken"`
		ClearAPIToken    bool   `json:"clearApiToken"`
		ClearTunnelToken bool   `json:"clearTunnelToken"`
		AccountID        string `json:"accountId"`
		TunnelName       string `json:"tunnelName"`
		ExposeAdmin      bool   `json:"exposeAdmin"`
		AdminHostname    string `json:"adminHostname"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	err := s.remote.Save(r.Context(), remote.SaveInput{
		Mode: body.Mode, APIToken: body.APIToken, TunnelToken: body.TunnelToken,
		ClearAPIToken: body.ClearAPIToken, ClearTunnelToken: body.ClearTunnelToken,
		AccountID: body.AccountID, TunnelName: body.TunnelName,
		ExposeAdmin: body.ExposeAdmin, AdminHostname: body.AdminHostname,
	})
	// Drop the tokens from this stack frame's view of the request as soon as
	// they have been sealed. They are not logged.
	body.APIToken, body.TunnelToken = "", ""
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.remote.View(r.Context()))
}

func (s *Server) handleRemoteInstall(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeError(w, http.StatusNotFound, "Remote access is not available.")
		return
	}
	if s.acceptInstall(w, r, "cloudflared") {
		return
	}
	if err := s.remote.Install(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.remote.View(r.Context()))
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusNotFound, "Web addresses are not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.proxy.View(r.Context()))
}

func (s *Server) handleProxySave(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusNotFound, "Web addresses are not available.")
		return
	}
	var body struct {
		Email      string `json:"email"`
		Domain     string `json:"domain"`
		UIHostname string `json:"uiHostname"`
		UIEnabled  bool   `json:"uiEnabled"`
		UITLS      string `json:"uiTls"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.proxy.Save(r.Context(), proxy.SaveInput{
		Email: body.Email, Domain: body.Domain, UIHostname: body.UIHostname,
		UIEnabled: body.UIEnabled, UITLS: body.UITLS,
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.proxy.View(r.Context()))
}

func (s *Server) handleProxyCA(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusNotFound, "Web addresses are not available.")
		return
	}
	f, err := os.Open(s.proxy.CAFile())
	if err != nil {
		writeError(w, http.StatusNotFound, "The local certificate is not ready yet. Turn on a home-network hostname, apply it, then download the certificate once Caddy has created it.")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="feldcore-root.crt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

func (s *Server) handleAddress(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeError(w, http.StatusNotFound, "Docker is not available.")
		return
	}
	info, err := s.docker.Spec(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.addressView(r, info))
}

func (s *Server) handleAddressSave(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeError(w, http.StatusNotFound, "Docker is not available.")
		return
	}
	info, err := s.docker.Spec(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var body struct {
		LanEnabled     bool   `json:"lanEnabled"`
		LanHostname    string `json:"lanHostname"`
		LanPort        int    `json:"lanPort"`
		LanTLS         string `json:"lanTls"`
		PublicEnabled  bool   `json:"publicEnabled"`
		PublicHostname string `json:"publicHostname"`
		PublicPort     int    `json:"publicPort"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if info.Spec.Name == "" {
		writeError(w, http.StatusBadRequest, "This container has no name, so it cannot have a hostname.")
		return
	}
	if s.proxy != nil {
		if err := s.proxy.SetRoute(proxy.RouteInput{
			Container: info.Spec.Name, Hostname: body.LanHostname, Port: body.LanPort,
			TLS: body.LanTLS, Enabled: body.LanEnabled,
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.proxy.Apply(r.Context()); err != nil {
			s.fail(w, r, err)
			return
		}
	} else if body.LanEnabled {
		writeError(w, http.StatusNotFound, "Web addresses are not available.")
		return
	}
	if s.remote != nil {
		if err := s.remote.SetPublic(info.Spec.Name, body.PublicHostname, body.PublicPort, body.PublicEnabled); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.remote.Sync(r.Context()); err != nil {
			s.fail(w, r, err)
			return
		}
	} else if body.PublicEnabled {
		writeError(w, http.StatusNotFound, "Remote access is not available.")
		return
	}
	resp := map[string]any{"status": "saved", "address": s.addressView(r, info)}
	if body.PublicEnabled && s.remote != nil {
		view := s.remote.View(r.Context())
		if view.Mode == "off" || !view.APITokenSet {
			resp["warning"] = "The public name is saved, but nothing is published yet. Turn on Remote access and add a Cloudflare API token. No ports are opened on your router."
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) addressView(r *http.Request, info docker.SpecInfo) map[string]any {
	domain := "home.arpa"
	if s.proxy != nil {
		if d := s.proxy.View(r.Context()).Domain; d != "" {
			domain = d
		}
	}
	host, port, from := netname.Suggest(info.Spec.Name, info.Spec.WebUI, domain)
	var ports []int
	seen := map[int]bool{}
	add := func(p int) {
		if p > 0 && p <= 65535 && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	add(port)
	for _, c := range info.Spec.Configs {
		if c.Type == docker.TypePort {
			add(c.ContainerPort)
		}
	}
	if port == 0 && len(ports) > 0 {
		port = ports[0]
	}
	if ports == nil {
		ports = []int{}
	}
	lan := map[string]any{"enabled": false, "hostname": host, "port": port, "tls": "internal"}
	pub := map[string]any{"enabled": false, "hostname": "", "port": port}
	if s.proxy != nil {
		for _, route := range s.proxy.View(r.Context()).Routes {
			if route.Container == info.Spec.Name && route.Enabled {
				lan = map[string]any{"enabled": true, "hostname": route.Hostname, "port": route.Port, "tls": route.TLS}
			}
		}
	}
	mode, apiSet := "off", false
	if s.remote != nil {
		view := s.remote.View(r.Context())
		mode, apiSet = view.Mode, view.APITokenSet
		for _, route := range view.Routes {
			if route.Container == info.Spec.Name {
				pub = map[string]any{"enabled": true, "hostname": route.Hostname, "port": route.Port}
			}
		}
	}
	return map[string]any{
		"container":   info.Spec.Name,
		"suggestion":  map[string]any{"hostname": host, "port": port, "from": from},
		"ports":       ports,
		"lan":         lan,
		"public":      pub,
		"tunnelMode":  mode,
		"apiTokenSet": apiSet,
		"domain":      domain,
	}
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.snaps == nil {
		writeError(w, http.StatusNotFound, "Snapshots are not available.")
		return
	}
	items, err := s.snaps.List(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []snapshots.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": items, "usage": s.snaps.Usage()})
}

func (s *Server) handleSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	if s.snaps == nil {
		writeError(w, http.StatusNotFound, "Snapshots are not available.")
		return
	}
	var body struct {
		Note string `json:"note"`
		Live bool   `json:"live"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	item, err := s.snaps.Take(r.Context(), snapshots.TakeOptions{
		Ref: chi.URLParam(r, "id"), Note: body.Note, Live: body.Live,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"snapshot": item, "usage": s.snaps.Usage()})
}

func (s *Server) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if s.snaps == nil {
		writeError(w, http.StatusNotFound, "Snapshots are not available.")
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "Restoring replaces this container and its mapped folders. Confirm to continue.")
		return
	}
	if err := s.snaps.Restore(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "snap")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (s *Server) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if s.snaps == nil {
		writeError(w, http.StatusNotFound, "Snapshots are not available.")
		return
	}
	if err := s.snaps.Delete(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "snap")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "usage": s.snaps.Usage()})
}

func (s *Server) handleSnapshotUsage(w http.ResponseWriter, r *http.Request) {
	if s.snaps == nil {
		writeError(w, http.StatusNotFound, "Snapshots are not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.snaps.Usage())
}

func (s *Server) handleSnapshotSettings(w http.ResponseWriter, r *http.Request) {
	if s.snaps == nil {
		writeError(w, http.StatusNotFound, "Snapshots are not available.")
		return
	}
	var body struct {
		KeepN    int    `json:"keepN"`
		KeepDays int    `json:"keepDays"`
		Dir      string `json:"dir"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.snaps.SetRetention(r.Context(), body.KeepN, body.KeepDays, body.Dir); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.snaps.Usage())
}
