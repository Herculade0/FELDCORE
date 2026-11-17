package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/compose"
	"github.com/Herculade0/feldcore/internal/install"
	"github.com/Herculade0/feldcore/internal/store"
)

// wireInstalls registers every one-shot install behind the shared job runner.
// The NVIDIA Container Toolkit has no install button in this build, so it is
// not registered. The design for that button lives in docs/GPU-SELECTION.md.
func (s *Server) wireInstalls() {
	s.installs = install.New(install.Deps{Log: s.log, Audit: s.auditInstall})
	if s.wiz != nil {
		wiz := s.wiz
		s.installs.Register("libvirt", install.Spec{
			Label: "libvirt / KVM",
			Apt:   true,
			Run: func(ctx context.Context, rep install.Reporter) error {
				rep.Step("Installing libvirt")
				return wiz.SaveService(ctx, "libvirt", true)
			},
		})
	}
	if s.shares != nil || s.wiz != nil {
		s.installs.Register("samba", install.Spec{
			Label: "Samba",
			Apt:   true,
			Run: func(ctx context.Context, rep install.Reporter) error {
				return s.installShare(ctx, rep, "smb", "samba", "Samba")
			},
		})
		s.installs.Register("nfs", install.Spec{
			Label: "NFS",
			Apt:   true,
			Run: func(ctx context.Context, rep install.Reporter) error {
				return s.installShare(ctx, rep, "nfs", "nfs", "NFS")
			},
		})
	}
	switch {
	case s.docker != nil:
		d := s.docker
		s.installs.Register("docker", install.Spec{
			Label: "Docker",
			Apt:   true,
			Run: func(ctx context.Context, rep install.Reporter) error {
				rep.Step("Installing Docker")
				return d.Install(ctx)
			},
		})
	case s.wiz != nil:
		wiz := s.wiz
		s.installs.Register("docker", install.Spec{
			Label: "Docker",
			Apt:   true,
			Run: func(ctx context.Context, rep install.Reporter) error {
				rep.Step("Installing Docker")
				return wiz.SaveService(ctx, "docker", true)
			},
		})
	}
	s.installs.Register("compose", install.Spec{
		Label: "Docker Compose",
		Apt:   true,
		Run: func(ctx context.Context, rep install.Reporter) error {
			rep.Step("Installing Docker Compose")
			return compose.InstallCLI(ctx)
		},
	})
	if s.backups != nil {
		dataDir := s.backups.DataDir
		s.installs.Register("restic", install.Spec{
			Label: "restic",
			Apt:   true,
			Run: func(ctx context.Context, rep install.Reporter) error {
				rep.Step("Installing restic")
				return backup.InstallRestic(ctx, dataDir)
			},
		})
	}
	if s.remote != nil {
		rem := s.remote
		s.installs.Register("cloudflared", install.Spec{
			Label: "cloudflared",
			Run: func(ctx context.Context, rep install.Reporter) error {
				rep.Step("Downloading cloudflared")
				rep.Line("Downloading the official cloudflared binary.")
				if err := rem.Install(ctx); err != nil {
					return err
				}
				rep.Line("cloudflared is installed.")
				return nil
			},
		})
	}
}

func (s *Server) installShare(ctx context.Context, rep install.Reporter, shareName, setupName, label string) error {
	rep.Step("Installing " + label)
	if s.shares != nil {
		if err := s.shares.Install(ctx, shareName); err != nil {
			return err
		}
	}
	if s.wiz != nil {
		return s.wiz.SaveService(ctx, setupName, true)
	}
	return nil
}

func (s *Server) auditInstall(action, target, result string) {
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

// acceptInstall starts a background install and writes it. It reports false
// when this process has nothing registered under name, so the caller can keep
// its previous synchronous behaviour.
func (s *Server) acceptInstall(w http.ResponseWriter, r *http.Request, name string) bool {
	if s.installs == nil {
		return false
	}
	job, err := s.installs.Start(name)
	if err != nil {
		if errors.Is(err, install.ErrUnknown) {
			return false
		}
		s.fail(w, r, err)
		return true
	}
	writeJSON(w, http.StatusAccepted, job)
	return true
}

func (s *Server) handleInstalls(w http.ResponseWriter, r *http.Request) {
	if s.installs == nil {
		writeError(w, http.StatusNotFound, "Installs are not available.")
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": s.installs.Current()})
		return
	}
	job, ok := s.installs.Latest(name)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	if s.installs == nil {
		writeError(w, http.StatusNotFound, "Installs are not available.")
		return
	}
	job, ok := s.installs.Get(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, "That install is no longer on this server.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleInstallStart(w http.ResponseWriter, r *http.Request) {
	if s.installs == nil {
		writeError(w, http.StatusNotFound, "Installs are not available.")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !s.acceptInstall(w, r, body.Name) {
		writeError(w, http.StatusBadRequest, "That install is not available.")
	}
}
