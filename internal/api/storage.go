package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
)

func (s *Server) storageOr(w http.ResponseWriter) (*storage.Service, bool) {
	if s.storage == nil {
		writeError(w, http.StatusNotFound, "Storage is not available.")
		return nil, false
	}
	return s.storage, true
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	view, err := st.Inventory(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleStorageDisks(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	disks, err := st.DiskHealth(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if disks == nil {
		disks = []storage.DiskHealth{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks})
}

func (s *Server) handlePoolCreate(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	var in storage.CreatePool
	if !readJSON(w, r, &in) {
		return
	}
	pool, err := st.CreatePool(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, pool)
}

func (s *Server) handlePoolMount(w http.ResponseWriter, r *http.Request) {
	s.poolAct(w, r, func(ctx context.Context, st *storage.Service, name string) error {
		return st.Mount(ctx, name)
	})
}

func (s *Server) handlePoolUnmount(w http.ResponseWriter, r *http.Request) {
	s.poolAct(w, r, func(ctx context.Context, st *storage.Service, name string) error {
		return st.Unmount(ctx, name)
	})
}

func (s *Server) handlePoolDestroy(w http.ResponseWriter, r *http.Request) {
	st, name, ok := s.poolFrom(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := st.DestroyPool(r.Context(), name, body.Confirm); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePoolAddDisk(w http.ResponseWriter, r *http.Request) {
	st, name, ok := s.poolFrom(w, r)
	if !ok {
		return
	}
	var in storage.AddDisk
	if !readJSON(w, r, &in) {
		return
	}
	in.Pool = name
	res, err := st.AddDisk(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handlePoolReplace(w http.ResponseWriter, r *http.Request) {
	st, name, ok := s.poolFrom(w, r)
	if !ok {
		return
	}
	var in storage.ReplaceDisk
	if !readJSON(w, r, &in) {
		return
	}
	in.Pool = name
	job, err := st.ReplaceDisk(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handlePoolJob(w http.ResponseWriter, r *http.Request) {
	st, name, ok := s.poolFrom(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	job, err := st.RunJob(r.Context(), name, body.Kind)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handlePoolSchedule(w http.ResponseWriter, r *http.Request) {
	st, name, ok := s.poolFrom(w, r)
	if !ok {
		return
	}
	var in storage.Schedule
	if !readJSON(w, r, &in) {
		return
	}
	in.Pool = name
	saved, err := st.SetSchedule(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleSpinDown(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	var body struct {
		ID      string `json:"id"`
		Minutes int    `json:"minutes"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := st.SetSpinDown(r.Context(), body.ID, body.Minutes); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSpinDownNow(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := st.SpinDownNow(r.Context(), body.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStorageCache(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	var body struct {
		Pool string `json:"pool"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	res, err := st.SetCache(r.Context(), body.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleStorageShare(w http.ResponseWriter, r *http.Request) {
	st, ok := s.storageOr(w)
	if !ok {
		return
	}
	share := strings.TrimSpace(chi.URLParam(r, "share"))
	var body struct {
		Pool string `json:"pool"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Pool) == "" {
		if err := st.UnbindShare(r.Context(), share); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b, err := st.BindShare(r.Context(), share, body.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) poolAct(w http.ResponseWriter, r *http.Request, fn func(context.Context, *storage.Service, string) error) {
	st, name, ok := s.poolFrom(w, r)
	if !ok {
		return
	}
	if err := fn(r.Context(), st, name); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) poolFrom(w http.ResponseWriter, r *http.Request) (*storage.Service, string, bool) {
	st, ok := s.storageOr(w)
	if !ok {
		return nil, "", false
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "Name the pool.")
		return nil, "", false
	}
	return st, name, true
}

// auditStorage writes a storage change. Browser sessions are recorded as the
// signed-in administrator. A missing principal (the boot-time restore loop)
// is recorded as admin. Token calls are also recorded by enforceScope.
func (s *Server) auditStorage(ctx context.Context, action, target, result string) {
	if s.auth == nil {
		return
	}
	name, id := "admin", ""
	if p, ok := principalFrom(ctx); ok {
		if p.ViaToken {
			name = p.TokenName
			if name == "" {
				name = "agent"
			}
			id = p.TokenID
		} else if p.Username != "" {
			name = p.Username
		}
	}
	if err := s.auth.Record(id, store.AuditEvent{
		TokenName: name,
		Action:    action,
		Target:    clip(target, 200),
		Result:    result,
	}); err != nil && s.log != nil {
		s.log.Warn("audit log write failed", "err", err)
	}
}
