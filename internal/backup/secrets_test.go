package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretsAreOptInAndAdminOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "master.key"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{DataDir: dir}
	plain, err := s.buildPlan(context.Background(), Destination{Sets: Sets{Config: true}}, "job", false)
	if err != nil {
		t.Fatal(err)
	}
	if containsPath(plain.Paths, "secrets") {
		t.Fatalf("default plan included secrets: %v", plain.Paths)
	}
	opted, err := s.buildPlan(context.Background(), Destination{Sets: Sets{Config: true, Secrets: true}}, "job2", false)
	if err != nil {
		t.Fatal(err)
	}
	if containsPath(opted.Paths, "secrets") {
		t.Fatal("a non-admin job included secrets")
	}
	admin, err := s.buildPlan(context.Background(), Destination{Sets: Sets{Config: true, Secrets: true}}, "job3", true)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(admin.Paths, "secrets") || !containsPath(admin.Paths, "state.json") {
		t.Fatalf("admin opt-in plan: %v", admin.Paths)
	}
	if !s.protectedRestore(nil) || !s.protectedRestore([]string{"/var/lib/feldcore/state.json"}) || !s.protectedRestore([]string{"/var/lib/feldcore/secrets/master.key"}) {
		t.Fatal("state and secrets restores were not protected")
	}
	if s.protectedRestore([]string{"/data/note.txt"}) {
		t.Fatal("an ordinary file was treated as state")
	}
}

func containsPath(paths []string, name string) bool {
	for _, p := range paths {
		if filepath.Base(p) == name || filepath.Base(filepath.Dir(p)) == name {
			return true
		}
	}
	return false
}
