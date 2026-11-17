package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeNVIDIARuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.json")
	if err := os.WriteFile(path, []byte("{\n  \"data-root\": \"/var/lib/feldcore/docker\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MergeNVIDIARuntime(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "/var/lib/feldcore/docker") || !strings.Contains(text, "nvidia-container-runtime") {
		t.Fatalf("runtime merge dropped settings:\n%s", text)
	}
	if err := MergeNVIDIARuntime(path); err != nil {
		t.Fatal(err)
	}
}

func TestMergeNVIDIARuntimeCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker", "daemon.json")
	if err := MergeNVIDIARuntime(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
