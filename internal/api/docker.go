package api

import (
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
	if err := s.syncVPN(r, spec); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.docker.Create(r.Context(), spec)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
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
func (s *Server) handleDockerUpdate(w http.ResponseWriter, r *http.Request) {
	var spec docker.Spec
	if !readJSONLimit(w, r, &spec, 1<<20) {
		return
	}
	s.quietName(r, chi.URLParam(r, "id"))
	if spec.Name != "" {
		s.quietName(r, spec.Name)
	}
	if err := s.syncVPN(r, spec); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.docker.Update(r.Context(), chi.URLParam(r, "id"), spec)
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
	if err := s.docker.Install(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.docker.Status(r.Context()))
}
