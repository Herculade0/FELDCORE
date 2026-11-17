package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/store"
)

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if s.health == nil {
		writeError(w, http.StatusNotFound, "Health monitoring is not available.")
		return
	}
	s.health.Collect(r.Context())
	s.auditFile(r, "health.check", "", "ok")
	writeJSON(w, http.StatusOK, s.health.Report())
}

func (s *Server) handleHealthReport(w http.ResponseWriter, r *http.Request) {
	if s.health == nil {
		writeError(w, http.StatusNotFound, "Health monitoring is not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.health.Report())
}

func (s *Server) handleHealthHistory(w http.ResponseWriter, r *http.Request) {
	if s.health == nil {
		writeError(w, http.StatusNotFound, "Health monitoring is not available.")
		return
	}
	hours := 24
	if q := r.URL.Query().Get("hours"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Hours must be between 1 and 168.")
			return
		}
		if n > 168 {
			n = 168
		}
		hours = n
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	writeJSON(w, http.StatusOK, map[string]any{
		"hours":  hours,
		"points": s.health.History(since, 360),
	})
}

func (s *Server) handleHealthThresholds(w http.ResponseWriter, r *http.Request) {
	if s.health == nil {
		writeError(w, http.StatusNotFound, "Health monitoring is not available.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thresholds": s.health.ThresholdSettings()})
}

func (s *Server) handleHealthThresholdsSave(w http.ResponseWriter, r *http.Request) {
	if s.health == nil {
		writeError(w, http.StatusNotFound, "Health monitoring is not available.")
		return
	}
	var body store.HealthThresholds
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.health.SaveThresholds(body); err != nil {
		s.auditFile(r, "health.thresholds", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "health.thresholds", "", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"thresholds": s.health.ThresholdSettings()})
}

func (s *Server) handleNotifyChannelSave(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	var body notify.ChannelInput
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.mail.SaveChannel(body); err != nil {
		s.auditFile(r, "notifications.channel", body.Kind, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "notifications.channel", body.Kind, "ok")
	writeJSON(w, http.StatusOK, s.mail.View(r.Context()))
}

func (s *Server) handleNotifyChannelDelete(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.mail.DeleteChannel(id); err != nil {
		s.auditFile(r, "notifications.channel.delete", id, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "notifications.channel.delete", id, "ok")
	writeJSON(w, http.StatusOK, s.mail.View(r.Context()))
}

func (s *Server) handleNotifyChannelTest(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.alerts.Test(r.Context(), id); err != nil {
		s.auditFile(r, "notifications.test", id, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "notifications.test", id, "ok")
	writeJSON(w, http.StatusOK, map[string]string{"message": "Test message sent."})
}

func (s *Server) handleNotifyRoutes(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	var body notify.RoutesInput
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.mail.SaveRoutes(body); err != nil {
		s.auditFile(r, "notifications.routes", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "notifications.routes", "", "ok")
	writeJSON(w, http.StatusOK, s.mail.View(r.Context()))
}

func (s *Server) handleNotifyPolicy(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	var body notify.PolicyInput
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.mail.SavePolicy(body); err != nil {
		s.auditFile(r, "notifications.policy", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "notifications.policy", "", "ok")
	writeJSON(w, http.StatusOK, s.mail.View(r.Context()))
}

func (s *Server) handleNotifyInbox(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil || s.alerts.Inbox == nil {
		writeJSON(w, http.StatusOK, map[string]any{"unread": 0, "items": []notify.Note{}})
		return
	}
	unread, items := s.alerts.Inbox.List()
	if len(items) > 50 {
		items = items[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread, "items": items})
}

func (s *Server) handleNotifyInboxRead(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil || s.alerts.Inbox == nil {
		writeError(w, http.StatusNotFound, "Notifications are not available.")
		return
	}
	var body struct {
		All bool     `json:"all"`
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	s.alerts.Inbox.MarkRead(body.All, body.IDs)
	s.auditFile(r, "notifications.read", "", "ok")
	unread, items := s.alerts.Inbox.List()
	if len(items) > 50 {
		items = items[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread, "items": items})
}
