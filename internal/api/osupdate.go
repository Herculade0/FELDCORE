package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/install"
	"github.com/Herculade0/feldcore/internal/store"
)

func (s *Server) registerOSJobs() {
	if s.os == nil || s.installs == nil {
		return
	}
	osvc := s.os
	s.installs.Register("os-update", install.Spec{
		Label: "OS update",
		Run: func(ctx context.Context, rep install.Reporter) error {
			rep.Step("Checking the release")
			rel, err := osvc.Stage(ctx, func(line string) {
				rep.Line(line)
				if line != "" && !strings.HasSuffix(line, "%") {
					rep.Step(line)
				}
			})
			if err != nil {
				s.auditName("os.update.staged", "", "error")
				return err
			}
			s.auditName("os.update.staged", rel.Version, "ok")
			return nil
		},
	})
	s.installs.Register("os-rollback", install.Spec{
		Label: "OS rollback",
		Run: func(ctx context.Context, rep install.Reporter) error {
			rep.Step("Selecting the previous image")
			info, err := osvc.Rollback(ctx)
			if err != nil {
				s.auditName("os.update.rollback", "", "error")
				return err
			}
			rep.Line("The next boot is slot " + strings.ToUpper(info.ID) + " (" + info.Version + ").")
			s.auditName("os.update.rollback", info.Version, "ok")
			return nil
		},
	})
}

func (s *Server) auditName(action, target, result string) {
	if s.auth == nil {
		return
	}
	if err := s.auth.Record("", store.AuditEvent{
		TokenName: "admin",
		Action:    action,
		Target:    target,
		Result:    result,
	}); err != nil && s.log != nil {
		s.log.Warn("audit log write failed", "err", err)
	}
}

func (s *Server) osReady(w http.ResponseWriter) bool {
	if s.os == nil {
		writeError(w, http.StatusNotFound, "OS updates are not available.")
		return false
	}
	return true
}

func (s *Server) handleOS(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.os.Status(r.Context()))
}

func (s *Server) handleOSCheck(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) {
		return
	}
	rel, err := s.os.Check(r.Context())
	if err != nil {
		s.auditFile(r, "os.update.check", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "os.update.check", rel.Version, "ok")
	writeJSON(w, http.StatusOK, rel)
}

func (s *Server) handleOSUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) || s.installs == nil {
		if s.os != nil {
			writeError(w, http.StatusNotFound, "OS updates are not available.")
		}
		return
	}
	job, err := s.installs.Start("os-update")
	if err != nil {
		s.auditFile(r, "os.update.stage", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "os.update.stage", "", "ok")
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleOSRollback(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) || s.installs == nil {
		if s.os != nil {
			writeError(w, http.StatusNotFound, "OS updates are not available.")
		}
		return
	}
	job, err := s.installs.Start("os-rollback")
	if err != nil {
		s.auditFile(r, "os.update.rollback", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "os.update.rollback", "", "ok")
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleOSSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) {
		return
	}
	var body struct {
		At string `json:"at"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.os.SetSchedule(body.At); err != nil {
		s.auditFile(r, "os.update.schedule", body.At, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "os.update.schedule", body.At, "ok")
	writeJSON(w, http.StatusOK, s.os.Status(r.Context()))
}

func (s *Server) handleOSSource(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.os.SetManifestURL(body.URL); err != nil {
		s.auditFile(r, "os.update.source", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "os.update.source", body.URL, "ok")
	writeJSON(w, http.StatusOK, s.os.Status(r.Context()))
}

func (s *Server) handleOSReboot(w http.ResponseWriter, r *http.Request) {
	if !s.osReady(w) {
		return
	}
	if err := s.os.Reboot(r.Context()); err != nil {
		s.auditFile(r, "os.update.reboot", "", "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "os.update.reboot", "", "ok")
	writeJSON(w, http.StatusOK, map[string]string{"status": "rebooting"})
}

func (s *Server) handleOSSysextInstall(w http.ResponseWriter, r *http.Request) {
	s.startSysext(w, r, chi.URLParam(r, "id"), false)
}

func (s *Server) handleOSSysextRemove(w http.ResponseWriter, r *http.Request) {
	s.startSysext(w, r, chi.URLParam(r, "id"), true)
}

func (s *Server) startSysext(w http.ResponseWriter, r *http.Request, id string, remove bool) {
	if !s.osReady(w) || s.installs == nil {
		if s.os != nil {
			writeError(w, http.StatusNotFound, "OS updates are not available.")
		}
		return
	}
	if !sysextNameOK(id) {
		writeError(w, http.StatusBadRequest, "That extension name is not valid.")
		return
	}
	action := "os.sysext.install"
	name := "os-sysext-" + id
	label := "Extension " + id
	if remove {
		action = "os.sysext.remove"
		name = "os-sysext-remove-" + id
		label = "Remove " + id
	}
	osvc := s.os
	s.installs.Register(name, install.Spec{
		Label: label,
		Run: func(ctx context.Context, rep install.Reporter) error {
			var err error
			if remove {
				rep.Step("Removing " + id)
				err = osvc.RemoveSysext(ctx, id)
			} else {
				rep.Step("Installing " + id)
				err = osvc.InstallSysext(ctx, id, func(line string) {
					rep.Line(line)
					if line != "" && !strings.HasSuffix(line, "%") {
						rep.Step(line)
					}
				})
			}
			result := "ok"
			if err != nil {
				result = "error"
			}
			s.auditName(action, id, result)
			return err
		},
	})
	job, err := s.installs.Start(name)
	if err != nil {
		s.auditFile(r, action, id, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, action, id, "ok")
	writeJSON(w, http.StatusAccepted, job)
}

func sysextNameOK(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// OSLoop runs a scheduled update when its time arrives.
func (s *Server) OSLoop(ctx context.Context) {
	if s.os == nil || s.installs == nil {
		return
	}
	s.os.Loop(ctx, func() {
		if _, err := s.installs.Start("os-update"); err != nil && s.log != nil {
			s.log.Warn("scheduled update did not start", "err", err)
			return
		}
		s.auditName("os.update.scheduled", "", "ok")
	})
}
