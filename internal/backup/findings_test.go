package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/secrets"
)

func TestNonAdminBackupOmitsState(t *testing.T) {
	dir := t.TempDir()
	shares := filepath.Join(dir, "shares")
	if err := os.MkdirAll(filepath.Join(shares, "internal-affairs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{DataDir: dir, SharesRoot: shares, Compose: func(context.Context) []string { return nil }}
	plan, err := s.buildPlan(context.Background(), Destination{Sets: DefaultSets()}, "job", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Paths {
		if strings.HasSuffix(p, "state.json") {
			t.Fatalf("state file included: %v", plan.Paths)
		}
	}
	admin, err := s.buildPlan(context.Background(), Destination{Sets: DefaultSets()}, "job2", true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range admin.Paths {
		if strings.HasSuffix(p, "state.json") {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin plan dropped state: %v", admin.Paths)
	}
}

func TestRestoreStaysInsideBackupRoots(t *testing.T) {
	dir := t.TempDir()
	shares := filepath.Join(dir, "shares")
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		Secrets: box, DataDir: dir, SharesRoot: shares, Engine: &memEng{},
		Compose: func(context.Context) []string { return nil },
	}
	dest, err := s.SaveDestination(DestinationInput{
		Name: "Nightly", Kind: "local", Enabled: true, Path: filepath.Join(dir, "dest"),
		ScheduleMode: "manual",
	}, "ada", "")
	if err != nil {
		t.Fatal(err)
	}
	outside := RestoreInput{DestinationID: dest.ID, SnapshotID: "abcdef0123456789", Paths: []string{"/etc/passwd"}}
	if _, err := s.StartRestore(outside, "ada", "tok", true); err == nil {
		t.Fatal("path outside the backup roots was accepted")
	}
	note := filepath.Join(shares, "library", "note.txt")
	copy := RestoreInput{DestinationID: dest.ID, SnapshotID: "abcdef0123456789", Paths: []string{note}}
	if _, err := s.StartRestore(copy, "ada", "tok", false); err != nil {
		t.Fatal(err)
	}
	replace := RestoreInput{
		DestinationID: dest.ID, SnapshotID: "abcdef0123456789",
		Paths: []string{note}, Mode: "replace", Confirm: "replace",
	}
	if _, err := s.StartRestore(replace, "ada", "tok", false); err == nil {
		t.Fatal("non-admin replace was accepted")
	}
	if _, err := s.RevealPassword(dest.ID, "ada", "tok", false); err == nil {
		t.Fatal("non-admin password reveal was accepted")
	}
}
