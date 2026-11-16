package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
)

type recAudit struct{ ev []store.AuditEvent }

func (r *recAudit) Record(_ string, ev store.AuditEvent) error {
	r.ev = append(r.ev, ev)
	return nil
}

func TestWizardRequiresCodeThenResumes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	audit := &recAudit{}
	s := New(Deps{Store: st, DataDir: dir, Cmd: &fakeCmd{}, Root: func() bool { return true }})
	s.UseAudit(audit)
	if err := s.Begin(false); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, Request{Step: "welcome", Confirm: true}); err == nil {
		t.Fatal("welcome continued without the setup code")
	}
	code, err := s.EnsureCode()
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := s.Apply(ctx, Request{Step: "welcome", Confirm: true, Code: code})
	if err != nil || view.Step != "host" {
		t.Fatalf("welcome %+v %v", view.Step, err)
	}
	view, _, err = s.Apply(ctx, Request{Step: "host", Skip: true, Code: code})
	if err != nil || view.Step != "network" {
		t.Fatalf("skip host %+v %v", view.Step, err)
	}
	again, err := s.Status(ctx, nil)
	if err != nil || again.Step != "network" || !again.NeedsCode {
		t.Fatalf("resume %+v %v", again, err)
	}
	if s.Pending() {
		t.Fatal("pending before an administrator exists")
	}
	found := false
	for _, ev := range audit.ev {
		if ev.Action == "wizard.skip" && ev.Result == "ok" && ev.TokenName == "setup" {
			found = true
		}
		if strings.Contains(ev.Target, code) {
			t.Fatal("audit recorded the setup code")
		}
	}
	if !found {
		t.Fatalf("audit %+v", audit.ev)
	}
}

func TestWizardAdminSkipTOTPAndRerunRefusesFormat(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	accounts.UseSecrets(box)
	root := t.TempDir()
	writeDisk(t, root)
	exec := &fakeCmd{}
	s := New(Deps{Store: st, DataDir: dir, Cmd: exec, Root: func() bool { return true }})
	s.UseAccounts(accounts)
	s.UseSecrets(box)
	s.UseVolume(storage.New(root, exec))
	if err := s.Begin(false); err != nil {
		t.Fatal(err)
	}
	code, err := s.EnsureCode()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, Request{Step: "welcome", Confirm: true, Code: code}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Request{Step: "host", Skip: true, Code: code}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Request{Step: "network", Skip: true, Code: code}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Request{
		Step: "admin", Confirm: true, Code: code, Username: "admin", Password: "short", Password2: "short", SkipTOTP: true,
	}); err == nil {
		t.Fatal("weak password accepted")
	}
	view, grant, err := s.Apply(ctx, Request{
		Step: "admin", Confirm: true, Code: code, Username: "admin",
		Password: "Wizard Passphrase 1!", Password2: "Wizard Passphrase 1!", SkipTOTP: true,
	})
	if err != nil || grant == nil || grant.Token == "" || view.Step != "storage" {
		t.Fatalf("admin %+v grant=%v err=%v", view.Step, grant, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "setup-code")); !os.IsNotExist(err) {
		t.Fatal("setup code was not consumed")
	}
	if s.st.Snapshot().Admin == nil || s.st.Snapshot().Admin.TOTPEnabled {
		t.Fatal("account or totp flag")
	}
	// Disks are listed only after the account exists.
	if len(view.Disks) == 0 {
		t.Fatal("expected a disk inventory")
	}
	if _, _, err := s.Apply(ctx, Request{
		Step: "storage", Confirm: true, StorageMode: "disk", StorageDevice: "/dev/sdb",
		StorageFormat: true, StorageMount: "/mnt/feldcore", StorageConfirm: "no",
	}); err == nil {
		t.Fatal("format without the phrase")
	}
	if joined := strings.Join(exec.calls, "\n"); strings.Contains(joined, "mkfs") {
		t.Fatal(joined)
	}
	view, _, err = s.Apply(ctx, Request{
		Step: "storage", Confirm: true, StorageMode: "keep", StorageMount: "/mnt/feldcore",
	})
	if err != nil || view.Step != "remote" {
		t.Fatalf("keep %+v %v", view.Step, err)
	}
	for _, step := range []string{"remote", "notify", "backup"} {
		view, _, err = s.Apply(ctx, Request{Step: step, Skip: true})
		if err != nil {
			t.Fatal(step, err)
		}
	}
	if view.Step != "summary" {
		t.Fatalf("step %s", view.Step)
	}
	view, _, err = s.Apply(ctx, Request{Step: "summary", Confirm: true})
	if err != nil || !view.Done {
		t.Fatalf("summary %+v %v", view.Done, err)
	}
	if s.Pending() {
		t.Fatal("still pending")
	}
	view, _, err = s.Apply(ctx, Request{BeginRerun: true, Actor: "admin"})
	if err != nil || !view.Rerun || view.Step != "host" || view.Done {
		t.Fatalf("rerun %+v", view)
	}
	if !s.st.Snapshot().Wizard.Done {
		t.Fatal("rerun cleared the finished wizard")
	}
	if _, _, err := s.Apply(ctx, Request{
		Step: "host", Skip: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Request{Step: "network", Skip: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := s.planStorage(ctx, Request{
		StorageMode: "disk", StorageFormat: true, StorageDevice: "/dev/sdb",
		StorageMount: "/mnt/feldcore", StorageConfirm: "ERASE sdb",
	}); err == nil {
		t.Fatal("rerun planned a format")
	}
	// Confirming sign-in without a password or a code is refused. Skipping
	// the step, or ticking "skip two-factor", leaves sign-in unchanged.
	if _, _, err := s.Apply(ctx, Request{Step: "signin", Confirm: true}); err == nil {
		t.Fatal("signin should still require a password")
	}
	if _, _, err := s.Apply(ctx, Request{Step: "signin", Skip: true}); err != nil {
		t.Fatal(err)
	}
	// Jumping to a destructive storage confirm is not a rerun step.
	if _, _, err := s.Apply(ctx, Request{
		Step: "storage", Confirm: true, StorageMode: "disk", StorageFormat: true,
		StorageDevice: "/dev/sdb", StorageConfirm: "ERASE sdb", StorageMount: "/mnt/feldcore",
	}); err == nil {
		t.Fatal("rerun accepted a storage step")
	}
	if joined := strings.Join(exec.calls, "\n"); strings.Contains(joined, "mkfs") {
		t.Fatal(joined)
	}
}

func writeDisk(t *testing.T, root string) {
	t.Helper()
	must := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "proc", "mounts"), "/dev/sda1 / ext4 rw 0 0\n")
	must(filepath.Join(root, "sys", "block", "sda", "size"), "134217728\n")
	must(filepath.Join(root, "sys", "block", "sda", "sda1", "size"), "134217728\n")
	must(filepath.Join(root, "sys", "block", "sdb", "size"), "2147483648\n")
	must(filepath.Join(root, "sys", "block", "sdb", "device", "model"), "Data\n")
}
