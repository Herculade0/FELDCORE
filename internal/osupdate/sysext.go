package osupdate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Sysext is one extension, installed or offered by the signed release.
type Sysext struct {
	ID        string `json:"id"`
	Version   string `json:"version,omitempty"`
	Kernel    string `json:"kernel,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Installed bool   `json:"installed"`
	Available bool   `json:"available"`
	Source    string `json:"source,omitempty"`
	Matches   bool   `json:"matches"`
	Reason    string `json:"reason,omitempty"`
}

type sysextMeta struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Kernel  string `json:"kernel,omitempty"`
	Notes   string `json:"notes,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

func (s *Service) sysextDir() string {
	return filepath.Join(s.dataDir(), "sysexts")
}

func (s *Service) listSysexts(kernel string) []Sysext {
	byID := map[string]*Sysext{}
	order := []string{}
	add := func(id string) *Sysext {
		if sx, ok := byID[id]; ok {
			return sx
		}
		sx := &Sysext{ID: id, Matches: true}
		byID[id] = sx
		order = append(order, id)
		return sx
	}
	for _, id := range s.installedIDs(s.join("usr", "lib", "extensions"), "image") {
		sx := add(id.id)
		sx.Installed = true
		if sx.Source == "" {
			sx.Source = "image"
		}
		sx.Version = firstNonEmpty(sx.Version, id.version)
		sx.Notes = firstNonEmpty(sx.Notes, id.notes)
	}
	for _, id := range s.installedIDs(s.sysextDir(), "state") {
		sx := add(id.id)
		sx.Installed = true
		sx.Source = "state"
		sx.Version = firstNonEmpty(id.version, sx.Version)
		sx.Kernel = firstNonEmpty(id.kernel, sx.Kernel)
		sx.Notes = firstNonEmpty(id.notes, sx.Notes)
	}
	if path, err := s.cachedManifestPath(); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			if m, err := ParseManifest(b); err == nil {
				for _, art := range m.Sysexts {
					sx := add(art.ID)
					sx.Available = art.SHA256 != "" && art.Size > 0 && art.Name != ""
					sx.Version = firstNonEmpty(art.Version, sx.Version)
					sx.Kernel = firstNonEmpty(art.Kernel, sx.Kernel)
					sx.Notes = firstNonEmpty(art.Notes, sx.Notes)
				}
			}
		}
	}
	out := make([]Sysext, 0, len(order))
	for _, id := range order {
		sx := *byID[id]
		ok, reason := kernelMatch(sx.Kernel, kernel)
		sx.Matches = ok
		sx.Reason = reason
		out = append(out, sx)
	}
	return out
}

type installedSysext struct {
	id, version, kernel, notes string
}

func (s *Service) installedIDs(dir, source string) []installedSysext {
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []installedSysext
	for _, e := range list {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".raw") {
			continue
		}
		id := strings.TrimSuffix(name, ".raw")
		id = strings.TrimSuffix(id, ".sysext")
		if !sysextIDOK(id) {
			continue
		}
		item := installedSysext{id: id}
		metaPath := filepath.Join(dir, id+".json")
		if b, err := os.ReadFile(metaPath); err == nil {
			var meta sysextMeta
			if json.Unmarshal(b, &meta) == nil {
				item.version = meta.Version
				item.kernel = meta.Kernel
				item.notes = meta.Notes
			}
		}
		_ = source
		out = append(out, item)
	}
	return out
}

func kernelMatch(want, have string) (bool, string) {
	want = strings.TrimSpace(want)
	have = strings.TrimSpace(have)
	if want == "" {
		return true, ""
	}
	if have == "" {
		return false, "This system did not report a kernel version."
	}
	if want != have {
		return false, "This extension was built for kernel " + want + ". This system is running " + have + "."
	}
	return true, ""
}

// InstallSysext downloads one signed extension onto the state partition and
// asks systemd-sysext to refresh.
func (s *Service) InstallSysext(ctx context.Context, id string, logf func(string)) error {
	if logf == nil {
		logf = func(string) {}
	}
	return s.locked(func() error { return s.installSysext(ctx, id, logf) })
}

func (s *Service) installSysext(ctx context.Context, id string, logf func(string)) error {
	if !sysextIDOK(id) {
		return input("That extension name is not valid.")
	}
	lay := s.readLayout()
	if !lay.Appliance {
		return input("Extensions are installed on the FELDCORE OS image.")
	}
	m, err := s.fetchManifest(ctx)
	if err != nil {
		return err
	}
	s.remember(m)
	s.cacheManifest(m)
	art, ok := m.sysext(id)
	if !ok || art.SHA256 == "" || art.Size <= 0 || art.Name == "" {
		return input("That extension is not in the signed release.")
	}
	if ok, reason := kernelMatch(art.Kernel, lay.Kernel); !ok {
		return input(reason)
	}
	if art.Size > 2<<30 {
		return input("That extension is larger than this machine will write.")
	}
	dir := s.sysextDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return op("Could not prepare the extension directory.")
	}
	dest := filepath.Join(dir, id+".raw")
	logf("Downloading " + id + ".")
	if err := s.writeArtifact(ctx, Artifact{
		Name: art.Name, Kind: "extension", SHA256: art.SHA256, Size: art.Size, Encoding: art.Encoding,
	}, dest, true, logf); err != nil {
		_ = os.Remove(dest)
		return err
	}
	meta, _ := json.Marshal(sysextMeta{ID: id, Version: art.Version, Kernel: art.Kernel, Notes: art.Notes, SHA256: art.SHA256})
	if err := writeAtomic(filepath.Join(dir, id+".json"), append(meta, '\n'), 0o644); err != nil {
		return op("Could not record the extension.")
	}
	logf("Refreshing extensions.")
	if err := s.refresh(ctx); err != nil {
		return err
	}
	logf(id + " is installed.")
	return nil
}

// RemoveSysext deletes an extension that was installed on the state partition.
func (s *Service) RemoveSysext(ctx context.Context, id string) error {
	return s.locked(func() error { return s.removeSysext(ctx, id) })
}

func (s *Service) removeSysext(ctx context.Context, id string) error {
	if !sysextIDOK(id) {
		return input("That extension name is not valid.")
	}
	if !s.readLayout().Appliance {
		return input("Extensions are installed on the FELDCORE OS image.")
	}
	dir := s.sysextDir()
	raw := filepath.Join(dir, id+".raw")
	if _, err := os.Stat(raw); err != nil {
		return input("That extension is not installed on the state partition.")
	}
	if err := os.Remove(raw); err != nil {
		return op("Could not remove the extension.")
	}
	_ = os.Remove(filepath.Join(dir, id+".json"))
	return s.refresh(ctx)
}

func (s *Service) refresh(ctx context.Context) error {
	if s.RefreshFn != nil {
		return s.RefreshFn(ctx)
	}
	// A relocated root is an inspection or test tree. systemd-sysext only
	// merges the extensions of the machine it is running on, so calling it
	// here would change the host instead of the image under test.
	if s.Root != "" {
		return nil
	}
	if _, err := exec.LookPath("systemd-sysext"); err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "systemd-sysext", "refresh")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return op(clip("Could not refresh extensions. " + string(out)))
	}
	return nil
}

func (s *Service) cacheManifest(m Manifest) {
	buf, err := json.Marshal(m)
	if err != nil {
		return
	}
	dir := filepath.Join(s.dataDir(), "updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = writeAtomic(filepath.Join(dir, "manifest.json"), append(buf, '\n'), 0o644)
}

func (s *Service) cachedManifestPath() (string, error) {
	p := filepath.Join(s.dataDir(), "updates", "manifest.json")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}
