package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.apps.Catalogue())
}

func (s *Server) handleAppsRefresh(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.apps.Refresh(r.Context()))
}

func (s *Server) handleAppSpec(w http.ResponseWriter, r *http.Request) {
	app, spec, notes, err := s.apps.Spec(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if notes == nil {
		notes = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": app, "spec": spec, "notes": notes})
}
