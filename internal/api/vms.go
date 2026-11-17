package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/vm"
)

func (s *Server) handleVMs(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	ov, err := s.vms.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

func (s *Server) handleVMHost(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.vms.Host(r.Context()))
}

func (s *Server) handleVMSettings(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.vms.SettingsView())
}

func (s *Server) handleVMSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	var body struct {
		DomainsPath string `json:"domainsPath"`
		ISOsPath    string `json:"isosPath"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.vms.SetSettings(body.DomainsPath, body.ISOsPath); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.vms.SettingsView())
}

func (s *Server) handleVMCreate(w http.ResponseWriter, r *http.Request) {
	s.saveVM(w, r, false)
}

func (s *Server) handleVMUpdate(w http.ResponseWriter, r *http.Request) {
	s.saveVM(w, r, true)
}

func (s *Server) saveVM(w http.ResponseWriter, r *http.Request, update bool) {
	if !s.readyVMs(w) {
		return
	}
	var body struct {
		vm.Spec
		Start bool `json:"start"`
	}
	if !readJSONLimit(w, r, &body, 256<<10) {
		return
	}
	if update {
		body.Name = chi.URLParam(r, "name")
	}
	res, err := s.vms.Save(r.Context(), body.Spec, body.Start)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleVMGet(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	det, err := s.vms.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, det)
}

func (s *Server) handleVMDelete(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	disks := r.URL.Query().Get("disks") == "1"
	if err := s.vms.Delete(r.Context(), chi.URLParam(r, "name"), disks); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleVMXML(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	doc, err := s.vms.XML(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"xml": doc})
}

func (s *Server) handleVMXMLPut(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	var body struct {
		XML string `json:"xml"`
	}
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "Replacing a VM's XML needs an administrator.")
		return
	}
	if err := s.vms.DefineXML(r.Context(), chi.URLParam(r, "name"), body.XML, true); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleVMStart(w http.ResponseWriter, r *http.Request) {
	s.vmAct(w, r, s.vms.Start)
}

func (s *Server) handleVMStop(w http.ResponseWriter, r *http.Request) {
	s.vmAct(w, r, s.vms.Stop)
}

func (s *Server) handleVMForceStop(w http.ResponseWriter, r *http.Request) {
	s.vmAct(w, r, s.vms.ForceStop)
}

func (s *Server) handleVMPause(w http.ResponseWriter, r *http.Request) {
	s.vmAct(w, r, s.vms.Pause)
}

func (s *Server) handleVMResume(w http.ResponseWriter, r *http.Request) {
	s.vmAct(w, r, s.vms.Resume)
}

func (s *Server) vmAct(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) error) {
	if !s.readyVMs(w) {
		return
	}
	if err := fn(r.Context(), chi.URLParam(r, "name")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVMAutostart(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.vms.SetAutostart(r.Context(), chi.URLParam(r, "name"), body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"autostart": body.Enabled})
}

func (s *Server) handleVMSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	snaps, err := s.vms.Snapshots(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) handleVMSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.vms.CreateSnapshot(r.Context(), chi.URLParam(r, "name"), body.Name, body.Description); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "created"})
}

func (s *Server) handleVMSnapshotRevert(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	if err := s.vms.RevertSnapshot(r.Context(), chi.URLParam(r, "name"), chi.URLParam(r, "snap")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reverted"})
}

func (s *Server) handleVMSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	if err := s.vms.DeleteSnapshot(r.Context(), chi.URLParam(r, "name"), chi.URLParam(r, "snap")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleVirtio(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.vms.Virtio(r.Context()))
}

func (s *Server) handleVirtioDownload(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	var body struct {
		Channel string `json:"channel"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	job, err := s.vms.StartVirtioDownload(body.Channel)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleVirtioDelete(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	if err := s.vms.DeleteVirtio(chi.URLParam(r, "file")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleVMJob(w http.ResponseWriter, r *http.Request) {
	if !s.readyVMs(w) {
		return
	}
	job, err := s.vms.Job(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) readyVMs(w http.ResponseWriter) bool {
	if s.vms == nil {
		writeError(w, http.StatusServiceUnavailable, "The VM manager is not available.")
		return false
	}
	return true
}
