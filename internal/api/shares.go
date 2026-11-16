package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/shares"
)

func (s *Server) handleSharesOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.shares.Overview(r.Context()))
}

func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	var in shares.Input
	if !readJSON(w, r, &in) {
		return
	}
	view, err := s.shares.Create(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleShareUpdate(w http.ResponseWriter, r *http.Request) {
	var in shares.Input
	if !readJSON(w, r, &in) {
		return
	}
	view, err := s.shares.Update(r.Context(), chi.URLParam(r, "name"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleShareDelete(w http.ResponseWriter, r *http.Request) {
	kept, err := s.shares.Delete(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"folderKept": kept})
}

func (s *Server) handleServiceInstall(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "service")
	if name == "smb" {
		name = "samba"
	}
	if s.acceptInstall(w, r, name) {
		return
	}
	if err := s.shares.Install(r.Context(), chi.URLParam(r, "service")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.shares.Overview(r.Context()))
}

type shareUserInput struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) handleShareUserCreate(w http.ResponseWriter, r *http.Request) {
	var in shareUserInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.shares.AddUser(r.Context(), in.Name, in.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": in.Name})
}

func (s *Server) handleShareUserPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.shares.SetUserPassword(r.Context(), chi.URLParam(r, "name"), in.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleShareUserDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.shares.DeleteUser(r.Context(), chi.URLParam(r, "name")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
