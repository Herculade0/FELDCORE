package api

import (
	"net/http"

	"github.com/Herculade0/feldcore/internal/setup"
)

func (s *Server) handleWizardStatus(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Setup is not available.")
		return
	}
	view, err := s.wiz.Status(r.Context(), nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleWizard(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Setup is not available.")
		return
	}
	var in setup.Request
	if !readJSON(w, r, &in) {
		return
	}
	view, err := s.wiz.Apply(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleNetConfirm(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Setup is not available.")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.wiz.ConfirmNet(in.Token); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "kept"})
}
