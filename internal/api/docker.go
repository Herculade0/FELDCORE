package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/docker"
)

func (s *Server) handleDockerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.docker.Status(r.Context()))
}

func (s *Server) handleDockerContainers(w http.ResponseWriter, r *http.Request) {
	cs, err := s.docker.Containers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range cs {
		if s.proxy != nil {
			if u := s.proxy.URLFor(cs[i].Name); u != "" {
				cs[i].Hostname = u
				cs[i].WebURL = u
			}
		}
		if s.remote != nil {
			if host := s.remote.PublicHost(cs[i].Name); host != "" {
				cs[i].PublicHost = host
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": cs})
}

func (s *Server) handleDockerImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.docker.Images(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": imgs})
}

func (s *Server) handleDockerCreate(w http.ResponseWriter, r *http.Request) {
	var spec docker.Spec
	if !readJSONLimit(w, r, &spec, 1<<20) {
		return
	}
	if !s.allowRisk(w, r, &spec) {
		return
	}
	if err := s.syncVPN(r, spec); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.docker.Create(r.Context(), spec)
	if id != "" {
		s.attachProxy(r.Context(), id, spec.Name)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// handleDockerReview reports risky settings and, when asked, applies the
// trivial safer edit. It does not create or change a container.
func (s *Server) handleDockerReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec  docker.Spec `json:"spec"`
		Apply []string    `json:"apply"`
	}
	if !readJSONLimit(w, r, &req, 1<<20) {
		return
	}
	spec := docker.ApplySafer(req.Spec, req.Apply)
	findings := docker.Review(spec)
	writeJSON(w, http.StatusOK, map[string]any{"spec": spec, "findings": findings})
}

// allowRisk rejects a high-risk spec until an administrator sets deployAnyway.
// The flag acknowledges the warning. It does not authorise a lesser token.
func (s *Server) allowRisk(w http.ResponseWriter, r *http.Request, spec *docker.Spec) bool {
	findings := docker.Review(*spec)
	if docker.NeedsAck(findings) {
		if !s.isAdmin(r) {
			writeError(w, http.StatusForbidden, "High-risk settings need an administrator.")
			return false
		}
		if !spec.DeployAnyway {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    "This setup has high-risk settings. Read the warnings, apply a safer setting, or choose Deploy anyway.",
				"findings": findings,
			})
			return false
		}
	}
	spec.DeployAnyway = false
	return true
}

// attachProxy puts the container on the proxy network when a hostname
// already points at it. Recreating a container drops that extra network.
func (s *Server) attachProxy(ctx context.Context, id, name string) {
	if s.proxy == nil {
		return
	}
	if name == "" && s.docker != nil {
		if info, err := s.docker.Spec(ctx, id); err == nil {
			name = info.Spec.Name
		}
	}
	if name == "" {
		return
	}
	_ = s.proxy.Attach(ctx, name)
}

// handleDockerSpec returns a container's current settings for the edit dialog.
func (s *Server) handleDockerSpec(w http.ResponseWriter, r *http.Request) {
	info, err := s.docker.Spec(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleDockerUpdate applies edited settings by recreating the container.
// High-risk settings are refused before anything is changed. A snapshot is
// then taken so the previous image, settings and appdata can be put back
// from the container's Snapshots panel.
func (s *Server) handleDockerUpdate(w http.ResponseWriter, r *http.Request) {
	var spec docker.Spec
	if !readJSONLimit(w, r, &spec, 1<<20) {
		return
	}
	s.quietName(r, chi.URLParam(r, "id"))
	if spec.Name != "" {
		s.quietName(r, spec.Name)
	}
	if !s.allowRisk(w, r, &spec) {
		return
	}
	if s.snaps != nil {
		if err := s.snaps.BeforeChange(r.Context(), chi.URLParam(r, "id"), "Automatic snapshot before the container was changed"); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.syncVPN(r, spec); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.docker.Update(r.Context(), chi.URLParam(r, "id"), spec)
	if id != "" {
		s.attachProxy(r.Context(), id, spec.Name)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleDockerOptions(w http.ResponseWriter, r *http.Request) {
	opts := s.docker.Options(r.Context())
	if s.vpn != nil {
		for _, c := range s.vpn.Choices() {
			opts.VPNs = append(opts.VPNs, docker.VPNChoice{
				ID: c.ID, Name: c.Name, Provider: c.Provider,
				Server: c.Server, Country: c.Country, Enabled: c.Enabled,
			})
		}
	}
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) handleDockerStart(w http.ResponseWriter, r *http.Request) {
	if err := s.docker.Start(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handleDockerStop(w http.ResponseWriter, r *http.Request) {
	s.quietName(r, chi.URLParam(r, "id"))
	if err := s.docker.Stop(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) handleDockerRestart(w http.ResponseWriter, r *http.Request) {
	s.quietName(r, chi.URLParam(r, "id"))
	if err := s.docker.Restart(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

func (s *Server) handleDockerRemove(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	removeImage := r.URL.Query().Get("image") == "1"
	name := ""
	if cs, err := s.docker.Containers(r.Context()); err == nil {
		id := chi.URLParam(r, "id")
		for _, c := range cs {
			if c.ID == id || c.Name == id {
				name = c.Name
				break
			}
		}
	}
	s.quietName(r, chi.URLParam(r, "id"))
	out, err := s.docker.Remove(r.Context(), chi.URLParam(r, "id"), force, removeImage)
	if err == nil {
		s.detachVPN(r, name)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDockerLogs(w http.ResponseWriter, r *http.Request) {
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 5000 {
			tail = n
		}
	}
	logs, err := s.docker.Logs(r.Context(), chi.URLParam(r, "id"), tail)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *Server) handleDockerInstall(w http.ResponseWriter, r *http.Request) {
	if s.acceptInstall(w, r, "docker") {
		return
	}
	if err := s.docker.Install(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.docker.Status(r.Context()))
}
