package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/backup"
)

func (s *Server) backupReady(w http.ResponseWriter) bool {
	if s.backups == nil {
		writeError(w, http.StatusServiceUnavailable, "Backups are not available.")
		return false
	}
	return true
}

func (s *Server) handleBackupOverview(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.backups.Overview(r.Context()))
}

func (s *Server) handleBackupJobs(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.backups.Jobs()})
}

func (s *Server) handleBackupJob(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	job, ok := s.backups.Job(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, "That backup job is no longer on this server.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	var in backup.DestinationInput
	if !readJSON(w, r, &in) {
		return
	}
	actor, tokenID := actorOf(r)
	dest, err := s.backups.SaveDestination(in, actor, tokenID, s.callerAdmin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, dest)
}

func (s *Server) handleBackupUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	var in backup.DestinationInput
	if !readJSON(w, r, &in) {
		return
	}
	actor, tokenID := actorOf(r)
	dest, err := s.backups.UpdateDestination(chi.URLParam(r, "id"), in, actor, tokenID, s.callerAdmin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dest)
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	actor, tokenID := actorOf(r)
	if err := s.backups.DeleteDestination(chi.URLParam(r, "id"), actor, tokenID); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleBackupTest(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	actor, tokenID := actorOf(r)
	msg, err := s.backups.TestDestination(r.Context(), chi.URLParam(r, "id"), actor, tokenID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) handleBackupRun(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	actor, tokenID := actorOf(r)
	job, err := s.backups.StartBackup(chi.URLParam(r, "id"), actor, tokenID, s.callerAdmin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleBackupCheck(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	var body struct {
		ReadData bool `json:"readData"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	actor, tokenID := actorOf(r)
	job, err := s.backups.StartCheck(chi.URLParam(r, "id"), body.ReadData, actor, tokenID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleBackupPassword(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	actor, tokenID := actorOf(r)
	pw, err := s.backups.RevealPassword(chi.URLParam(r, "id"), actor, tokenID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": pw})
}

func (s *Server) handleBackupSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	list, err := s.backups.Snapshots(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

func (s *Server) handleBackupFiles(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	view, err := s.backups.Browse(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "snap"), r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if !s.backupReady(w) {
		return
	}
	var in backup.RestoreInput
	if !readJSON(w, r, &in) {
		return
	}
	actor, tokenID := actorOf(r)
	job, err := s.backups.StartRestore(in, actor, tokenID, s.callerAdmin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
