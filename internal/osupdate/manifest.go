package osupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	kindRoot   = "root"
	kindKernel = "kernel"
	kindInitrd = "initrd"

	maxManifest = 1 << 20
	maxNotes    = 16 << 10
)

// Release is the part of a manifest the Updates page shows.
type Release struct {
	Version  string `json:"version"`
	Channel  string `json:"channel,omitempty"`
	Released string `json:"released,omitempty"`
	Notes    string `json:"notes,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Kernel   string `json:"kernel,omitempty"`
}

// Artifact is one file the manifest names. SHA256 is the decoded bytes, the
// bytes written to a slot or the EFI partition. Encoding is empty or "zstd".
type Artifact struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding,omitempty"`
}

// SysextArtifact is one extension a release offers.
type SysextArtifact struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Kernel   string `json:"kernel,omitempty"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// Manifest is the signed description of one OS release.
type Manifest struct {
	Release
	Files   []Artifact       `json:"files"`
	Sysexts []SysextArtifact `json:"sysexts"`
}

func (m Manifest) release() Release { return m.Release }

func (m Manifest) artifact(kind string) (Artifact, bool) {
	for _, f := range m.Files {
		if f.Kind == kind {
			return f, true
		}
	}
	return Artifact{}, false
}

func (m Manifest) sysext(id string) (SysextArtifact, bool) {
	for _, s := range m.Sysexts {
		if s.ID == id {
			return s, true
		}
	}
	return SysextArtifact{}, false
}

// ParseManifest checks the shape of a manifest. It does not check signatures.
func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifest {
		return Manifest{}, input("The release description is empty or too large.")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, input("The release description is not valid.")
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (m *Manifest) validate() error {
	if !versionOK(m.Version) {
		return input("The release version is not valid.")
	}
	if m.Arch != "" && m.Arch != "amd64" && m.Arch != "x86_64" {
		return input("This release is for " + m.Arch + ". This machine takes an amd64 image.")
	}
	if len(m.Notes) > maxNotes {
		m.Notes = m.Notes[:maxNotes]
	}
	if m.Channel != "" && !tokenOK(m.Channel) {
		return input("The release channel is not valid.")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		switch f.Kind {
		case kindRoot, kindKernel, kindInitrd:
		default:
			return input("The release lists an unknown file.")
		}
		if seen[f.Kind] {
			return input("The release lists " + f.Kind + " more than once.")
		}
		seen[f.Kind] = true
		if err := checkArtifact(f.Name, f.SHA256, f.Size, f.Encoding); err != nil {
			return err
		}
	}
	ids := map[string]bool{}
	for _, s := range m.Sysexts {
		if !sysextIDOK(s.ID) || ids[s.ID] {
			return input("The release lists an extension FELDCORE will not install.")
		}
		ids[s.ID] = true
		if s.Version != "" && !versionOK(s.Version) {
			return input("An extension version is not valid.")
		}
		if s.SHA256 != "" || s.Size != 0 || s.Name != "" {
			if err := checkArtifact(s.Name, s.SHA256, s.Size, s.Encoding); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m Manifest) installable() error {
	for _, kind := range []string{kindRoot, kindKernel, kindInitrd} {
		if _, ok := m.artifact(kind); !ok {
			return input("The release does not include a " + kind + " image.")
		}
	}
	return nil
}

func checkArtifact(name, sum string, size int64, encoding string) error {
	if !fileNameOK(name) {
		return input("A release file name is not valid.")
	}
	if !shaOK(sum) {
		return input("A release file has no SHA-256 sum.")
	}
	if size <= 0 {
		return input("A release file has no size.")
	}
	switch encoding {
	case "", "zstd":
	default:
		return input("A release file uses an unsupported encoding.")
	}
	return nil
}

func versionOK(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for i, r := range v {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && (r == '.' || r == '_' || r == '+' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func tokenOK(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func sysextIDOK(id string) bool {
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

func fileNameOK(name string) bool {
	if name == "" || len(name) > 80 || name != filepath.Base(name) {
		return false
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return false
	}
	return tokenOK(strings.ReplaceAll(strings.ReplaceAll(name, ".", "-"), "+", "-")) || dottedOK(name)
}

func dottedOK(name string) bool {
	for _, r := range name {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return true
}

func shaOK(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// HashFile returns the SHA-256 and size of r.
func HashFile(r io.Reader) (sum string, n int64, err error) {
	h := sha256.New()
	n, err = io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// BuildManifest hashes the image files in dir and returns a manifest.
// A zstd root is hashed after decoding, which is what a slot receives.
func BuildManifest(dir string, rel Release) (Manifest, error) {
	m := Manifest{Release: rel, Files: []Artifact{}, Sysexts: []SysextArtifact{}}
	if err := m.validate(); err != nil && rel.Version != "" {
		// validate also checks files, which are still empty. Re-check version only.
	}
	if !versionOK(rel.Version) {
		return Manifest{}, input("The release version is not valid.")
	}
	root, enc, err := findRoot(dir)
	if err != nil {
		return Manifest{}, err
	}
	art, err := hashArtifact(root, kindRoot, enc)
	if err != nil {
		return Manifest{}, err
	}
	m.Files = append(m.Files, art)
	for _, spec := range []struct{ name, kind string }{
		{"vmlinuz", kindKernel},
		{"initrd.img", kindInitrd},
	} {
		p := filepath.Join(dir, spec.name)
		art, err := hashArtifact(p, spec.kind, "")
		if err != nil {
			return Manifest{}, err
		}
		m.Files = append(m.Files, art)
	}
	extDir := filepath.Join(dir, "sysexts")
	entries, _ := os.ReadDir(extDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".raw") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".raw")
		id = strings.TrimSuffix(id, ".sysext")
		if !sysextIDOK(id) {
			continue
		}
		art, err := hashArtifact(filepath.Join(extDir, e.Name()), "", "")
		if err != nil {
			return Manifest{}, err
		}
		notes := ""
		if b, err := os.ReadFile(filepath.Join(extDir, id+".notes")); err == nil {
			notes = strings.TrimSpace(string(b))
		}
		kernel := ""
		if b, err := os.ReadFile(filepath.Join(extDir, id+".kernel")); err == nil {
			kernel = strings.TrimSpace(string(b))
		}
		version := rel.Version
		if b, err := os.ReadFile(filepath.Join(extDir, id+".version")); err == nil {
			version = strings.TrimSpace(string(b))
		}
		m.Sysexts = append(m.Sysexts, SysextArtifact{
			ID: id, Version: version, Kernel: kernel, Name: e.Name(),
			SHA256: art.SHA256, Size: art.Size, Notes: notes,
		})
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func findRoot(dir string) (string, string, error) {
	zst := filepath.Join(dir, "feldcore-root.img.zst")
	raw := filepath.Join(dir, "feldcore-root.img")
	if _, err := os.Stat(zst); err == nil {
		return zst, "zstd", nil
	}
	if _, err := os.Stat(raw); err == nil {
		return raw, "", nil
	}
	return "", "", op("The build directory has no feldcore-root image.")
}

func hashArtifact(path, kind, encoding string) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, op("Cannot read " + filepath.Base(path) + ".")
	}
	defer f.Close()
	var r io.Reader = f
	if encoding == "zstd" {
		dec, err := zstd.NewReader(f)
		if err != nil {
			return Artifact{}, op("Cannot decode " + filepath.Base(path) + ".")
		}
		defer dec.Close()
		r = dec
	}
	sum, n, err := HashFile(r)
	if err != nil {
		return Artifact{}, op("Cannot hash " + filepath.Base(path) + ".")
	}
	return Artifact{Name: filepath.Base(path), Kind: kind, SHA256: sum, Size: n, Encoding: encoding}, nil
}

// MarshalManifest encodes a manifest with a trailing newline.
func MarshalManifest(m Manifest) ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(buf, '\n'), nil
}
