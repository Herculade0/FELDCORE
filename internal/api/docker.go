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
// A snapshot is taken first so the previous image, settings and appdata can
// be put back from the container's Snapshots panel.
func (s *Server) handleDockerUpdate(w http.ResponseWriter, r *http.Request) {
	var spec docker.Spec
	if !readJSONLimit(w, r, &spec, 1<<20) {
		return
	}
	if s.snaps != nil {
		if err := s.snaps.BeforeChange(r.Context(), chi.URLParam(r, "id"), "Automatic snapshot before the container was changed"); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	id, err := s.docker.Update(r.Context(), chi.URLParam(r, "id"), spec)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleDockerOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.docker.Options(r.Context()))
}

func (s *Server) handleDockerStart(w http.ResponseWriter, r *http.Request) {
	if err := s.docker.Start(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handleDockerStop(w http.ResponseWriter, r *http.Request) {
	if err := s.docker.Stop(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) handleDockerRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.docker.Restart(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

func (s *Server) handleDockerRemove(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	removeImage := r.URL.Query().Get("image") == "1"
	out, err := s.docker.Remove(r.Context(), chi.URLParam(r, "id"), force, removeImage)
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
