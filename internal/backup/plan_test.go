package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestSelectAndPruneAndDue(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "snapshots"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := SelectSources(dir, filepath.Join(dir, "missing-app"), true, true, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
	var names []string
	for _, s := range got {
		if s.Path != "" {
			names = append(names, s.Name)
		}
	}
	if strings.Join(names, ",") != "state.json,snapshots" {
		t.Fatalf("included %v", got)
	}
	if notes := Notes(got); len(notes) != 1 || !strings.Contains(notes[0], "appdata") {
		t.Fatalf("notes %v", notes)
	}
	del := Prune([]string{
		"notes.txt",
		"feldcore-20260101T000000Z.tar.gz",
		"feldcore-20260103T000000Z.tar.gz",
		"feldcore-20260102T000000Z.tar.gz",
	}, 2)
	if len(del) != 1 || del[0] != "feldcore-20260101T000000Z.tar.gz" {
		t.Fatalf("prune %v", del)
	}
	if extra := Prune([]string{"feldcore-20260101T000000Z.tar.gz"}, 0); extra != nil {
		t.Fatalf("keep 0 should keep everything, got %v", extra)
	}
	now := time.Date(2026, 3, 2, 4, 0, 0, 0, time.UTC)
	due, err := Due("03:00", time.Date(2026, 3, 2, 2, 0, 0, 0, time.UTC), now)
	if err != nil || !due {
		t.Fatalf("due %v %v", due, err)
	}
	due, err = Due("03:00", time.Date(2026, 3, 2, 3, 30, 0, 0, time.UTC), now)
	if err != nil || due {
		t.Fatalf("already ran %v %v", due, err)
	}
	due, err = Due("", time.Time{}, now)
	if err != nil || due {
		t.Fatal("empty schedule should not run")
	}
	if _, err := CleanRemote("../etc"); err == nil {
		t.Fatal("remote path should reject ..")
	}
}

func TestObscureRoundTripAndConfig(t *testing.T) {
	const password = "proton-drive-password"
	obscured, err := Obscure(password)
	if err != nil {
		t.Fatal(err)
	}
	if obscured == password {
		t.Fatal("obscure returned the plaintext")
	}
	back, err := Reveal(obscured)
	if err != nil || back != password {
		t.Fatalf("reveal %q %v", back, err)
	}
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, Secrets: box, DataDir: dir}
	if err := svc.Save(Settings{
		Enabled: true, Username: "user@proton.me", Password: password,
		Mailbox: "mailbox-secret", TwoFA: "123456", IncludeSnapshots: true, Retention: 7,
	}); err != nil {
		t.Fatal(err)
	}
	path, hidden, err := svc.writeConfig()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, password) || strings.Contains(text, "mailbox-secret") {
		t.Fatal("rclone config contains a plaintext secret")
	}
	if !strings.Contains(text, "type = protondrive") || !strings.Contains(text, "2fa = 123456") {
		t.Fatalf("config:\n%s", text)
	}
	joined := text + " " + password
	if strings.Contains(secrets.Redact(joined, hidden...), password) {
		t.Fatal("redaction missed the password")
	}
	view := svc.View()
	if !view.Experimental || !view.HasPassword || !view.HasMailbox || view.Username != "user@proton.me" {
		t.Fatalf("view %+v", view)
	}
}

func TestArchiveName(t *testing.T) {
	name := ArchiveName(time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("X", 3600)))
	if name != "feldcore-20260102T020405Z.tar.gz" {
		t.Fatal(name)
	}
}
