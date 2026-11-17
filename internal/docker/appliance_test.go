package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/appliance"
)

func TestStatusHidesInstallOnImage(t *testing.T) {
	if _, err := osStat("dockerd"); err == nil {
		t.Skip("dockerd is on PATH, so this host is not the missing-engine case")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "feldcore-os-release")
	if err := os.WriteFile(path, []byte("ID=feldcore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := appliance.SetReleasePath(path)
	defer restore()
	m := NewManager(filepath.Join(dir, "missing.sock"), nil, nil)
	st := m.Status(context.Background())
	if st.CanInstall {
		t.Fatal("the image offered to install Docker")
	}
	if st.Installed {
		t.Fatal("dockerd was reported installed")
	}
	if !strings.Contains(st.Message, "FELDCORE OS") {
		t.Fatalf("message %q", st.Message)
	}
}

func TestInstallRefusedOnImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feldcore-os-release")
	if err := os.WriteFile(path, []byte("ID=feldcore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := appliance.SetReleasePath(path)
	defer restore()
	m := NewManager(filepath.Join(dir, "missing.sock"), nil, nil)
	err := m.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "FELDCORE OS") {
		t.Fatalf("install error %v", err)
	}
}

func osStat(name string) (string, error) {
	return lookPath(name)
}

func lookPath(name string) (string, error) {
	if p, ok := lookBin(name); ok {
		return p, nil
	}
	return "", os.ErrNotExist
}
