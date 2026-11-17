package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/secrets"
)

func TestScheduleAndBrowse(t *testing.T) {
	now := time.Date(2026, 11, 16, 3, 5, 0, 0, time.UTC)
	due, err := scheduleDue("daily", "03:00", "", time.Time{}, now)
	if err != nil || !due {
		t.Fatalf("daily due=%v err=%v", due, err)
	}
	due, err = scheduleDue("daily", "03:00", "", now, now.Add(time.Minute))
	if err != nil || due {
		t.Fatal("a finished slot should not run again")
	}
	due, err = scheduleDue("weekly", "03:00", "tue", time.Time{}, now)
	if err != nil || due {
		t.Fatal("Tuesday should not be due on a Monday")
	}
	due, err = scheduleDue("weekly", "03:00", "mon", time.Time{}, now)
	if err != nil || !due {
		t.Fatal("Monday should be due")
	}
	if _, _, _, err := cleanSchedule("weekly", "03:00", "sun"); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduleDue("hourly", "03:00", "", time.Time{}, now); err == nil {
		t.Fatal("accepted an unknown schedule")
	}

	nodes := []Node{
		{Name: "state.json", Type: "file", Path: "/var/lib/feldcore/state.json", Size: 12},
		{Name: "secrets", Type: "dir", Path: "/var/lib/feldcore/secrets"},
		{Name: "master.key", Type: "file", Path: "/var/lib/feldcore/secrets/master.key", Size: 4},
	}
	top := listDir(nodes, "/")
	if len(top.Entries) != 1 || top.Entries[0].Name != "var" || top.Entries[0].Type != "dir" {
		t.Fatalf("top: %+v", top.Entries)
	}
	lib := listDir(nodes, "/var/lib/feldcore")
	if len(lib.Entries) != 2 {
		t.Fatalf("dir: %+v", lib.Entries)
	}
	if _, err := cleanSnapPath("/var/../etc/passwd"); err == nil {
		t.Fatal("accepted a path that climbs out")
	}
}

func TestDestinationRulesAndMountPlan(t *testing.T) {
	dir := t.TempDir()
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{DataDir: dir, SharesRoot: filepath.Join(dir, "shares"), Secrets: box, Compose: func(context.Context) []string { return nil }}
	if _, err := s.SaveDestination(DestinationInput{Name: "bad", Kind: "local", Path: "/etc/feldcore"}, "admin", ""); err == nil {
		t.Fatal("accepted an operating-system path")
	}
	if _, err := s.SaveDestination(DestinationInput{Name: "empty", Kind: "local", Path: filepath.Join(dir, "out"), Sets: &Sets{}}, "admin", ""); err == nil {
		t.Fatal("accepted a destination with nothing to copy")
	}
	dest, err := s.SaveDestination(DestinationInput{
		Name: "Nightly", Kind: "local", Enabled: true, Path: filepath.Join(dir, "out"),
		ScheduleMode: "daily", ScheduleTime: "03:00", KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6,
	}, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if !dest.Sets.InternalAffairs {
		t.Fatal("Internal Affairs should be in the default set")
	}
	if err := box.Put(passSecret(dest.ID), "smb-secret-value"); err != nil {
		t.Fatal(err)
	}
	spec, err := s.mountSpec(Destination{Kind: "smb", Host: "nas.home", Path: "backups", Username: "corey", HasPassword: true, ID: dest.ID, Port: 445}, filepath.Join(dir, "mnt"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Source != "//nas.home/backups" {
		t.Fatalf("source %s", spec.Source)
	}
	args := smbMountArgs(spec, filepath.Join(dir, "mnt.cred"))
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "smb-secret-value") || !strings.Contains(joined, "credentials=") || !strings.Contains(joined, "file_mode=0600") {
		t.Fatalf("smb args: %s", joined)
	}
	nfs := Destination{Kind: "nfs", Host: "nas.home", Path: "/volume1/backups", Port: 2049}
	ns, err := s.mountSpec(nfs, filepath.Join(dir, "mnt"))
	if err != nil || ns.Source != "nas.home:/volume1/backups" {
		t.Fatalf("nfs: %+v %v", ns, err)
	}
	nargs := strings.Join(nfsMountArgs(ns), " ")
	if !strings.Contains(nargs, "nfsvers=4.1") {
		t.Fatalf("nfs args: %s", nargs)
	}
}

func TestVMPrepCopiesAShutOffDisk(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.qcow2")
	if err := os.WriteFile(disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := &scriptCmd{rules: []scriptRule{
		{prefix: "virsh -r list", out: "vm1\n"},
		{prefix: "virsh dumpxml", out: "<domain name='vm1'/>"},
		{prefix: "virsh domstate", out: "shut off"},
		{prefix: "virsh domblklist", out: "file disk vda " + disk + "\nfile cdrom hdc /iso\n"},
	}}
	cmd.have = map[string]bool{"virsh": true}
	prep, err := prepareVMs(context.Background(), cmd, filepath.Join(dir, "domains"), filepath.Join(dir, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(prep.Paths, disk) {
		t.Fatalf("paths %+v notes %+v", prep.Paths, prep.Notes)
	}
	if err := prep.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestVMPrepPausesWhenSnapshotFails(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.qcow2")
	if err := os.WriteFile(disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := &scriptCmd{have: map[string]bool{"virsh": true}, rules: []scriptRule{
		{prefix: "virsh -r list", out: "vm1\n"},
		{prefix: "virsh dumpxml", out: "<domain/>"},
		{prefix: "virsh domstate", out: "running"},
		{prefix: "virsh domblklist", out: "file disk vda " + disk},
		{prefix: "virsh snapshot-create-as", err: op("snapshot refused")},
		{prefix: "virsh suspend", out: ""},
		{prefix: "virsh resume", out: ""},
	}}
	prep, err := prepareVMs(context.Background(), cmd, filepath.Join(dir, "domains"), filepath.Join(dir, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(prep.Paths, disk) {
		t.Fatalf("paths %+v notes %+v calls %+v", prep.Paths, prep.Notes, cmd.calls)
	}
	if err := prep.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasPrefixCall(cmd.calls, "virsh resume") {
		t.Fatalf("resume was not called: %+v", cmd.calls)
	}
}

func TestResticPasswordStaysOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "restic")
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RESTIC_FAKE_LOG\"; done\n" +
		"if [ -n \"$RESTIC_PASSWORD\" ]; then printf '%s\\n' ENV_PASSWORD_SET >> \"$RESTIC_FAKE_LOG\"; fi\n" +
		"case \"$1\" in\n" +
		"snapshots)\n" +
		"  if [ ! -f \"$RESTIC_REPOSITORY/ready\" ]; then echo '{\"message_type\":\"exit_error\",\"message\":\"repository does not exist\"}'; exit 10; fi\n" +
		"  echo '[]'\n" +
		"  ;;\n" +
		"init) mkdir -p \"$RESTIC_REPOSITORY\"; : > \"$RESTIC_REPOSITORY/ready\" ;;\n" +
		"backup) echo '{\"message_type\":\"summary\",\"percent_done\":1,\"snapshot_id\":\"abcdef0123456789\",\"total_bytes\":4,\"bytes_done\":4}' ;;\n" +
		"ls) echo '{\"struct_type\":\"snapshot\",\"paths\":[\"/data\"]}'\n" +
		"    echo '{\"message_type\":\"node\",\"name\":\"note.txt\",\"type\":\"file\",\"path\":\"/data/note.txt\",\"size\":4}' ;;\n" +
		"*) ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := Repo{Location: filepath.Join(dir, "repo"), Password: "super-secret-password"}
	cli := &CLI{Bin: script, DataDir: dir}
	t.Setenv("RESTIC_FAKE_LOG", logPath)
	if err := cli.Ensure(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	sum, err := cli.Backup(context.Background(), repo, BackupReq{Paths: []string{"/data"}, Tags: []string{"feldcore"}}, nil)
	if err != nil || sum.SnapshotID != "abcdef0123456789" {
		t.Fatalf("sum %+v err %v", sum, err)
	}
	nodes, err := cli.List(context.Background(), repo, "abcdef0123456789")
	if err != nil || len(nodes) != 1 || nodes[0].Path != "/data/note.txt" {
		t.Fatalf("nodes %+v err %v", nodes, err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, repo.Password) || !strings.Contains(text, "ENV_PASSWORD_SET") {
		t.Fatalf("argv log:\n%s", text)
	}
	if !needsInit(`{"message":"repository does not exist"}`) || needsInit("wrong password") {
		t.Fatal("needsInit misread the restic error")
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	shares := filepath.Join(dir, "shares")
	ia := filepath.Join(shares, "internal-affairs")
	if err := os.MkdirAll(ia, 0o755); err != nil {
		t.Fatal(err)
	}
	head := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(ia, "ledger.jsonl"), []byte(`{"line":"`+head+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng := &memEng{}
	bus := events.New()
	var got []events.Event
	bus.Subscribe(func(ev events.Event) { got = append(got, ev) })
	var audited []string
	s := &Service{
		Secrets: box, DataDir: dir, SharesRoot: shares, Engine: eng, Events: bus,
		Compose: func(context.Context) []string { return nil },
		Cmd:     absentCmd{},
		Audit:   func(actor, _, action, _, result string) { audited = append(audited, actor+" "+action+" "+result) },
	}
	dest, err := s.SaveDestination(DestinationInput{
		Name: "Nightly", Kind: "local", Enabled: true, Path: filepath.Join(dir, "dest"),
		ScheduleMode: "manual",
	}, "ada", "")
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.StartBackup(dest.ID, "ada", "tok")
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, s, job.ID)
	if done.State != jobSucceeded || done.SnapshotID == "" {
		t.Fatalf("job %+v", done)
	}
	raw, err := os.ReadFile(s.catalogPath())
	if err != nil {
		t.Fatal(err)
	}
	pw, err := s.RevealPassword(dest.ID, "ada", "tok")
	if err != nil || pw == "" {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pw) {
		t.Fatal("the catalog contains the repository password")
	}
	over := s.Overview(context.Background())
	if over.InternalAffairs.LastBackedHead != head {
		t.Fatalf("head %q", over.InternalAffairs.LastBackedHead)
	}
	view, err := s.Browse(context.Background(), dest.ID, done.SnapshotID, "/data")
	if err != nil || len(view.Entries) != 1 || view.Entries[0].Name != "note.txt" {
		t.Fatalf("browse %+v %v", view, err)
	}
	rest, err := s.StartRestore(RestoreInput{DestinationID: dest.ID, SnapshotID: done.SnapshotID, Paths: []string{"/data/note.txt"}}, "ada", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, s, rest.ID); got.State != jobSucceeded {
		t.Fatalf("restore %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "restores", rest.ID, "data", "note.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRestore(RestoreInput{DestinationID: dest.ID, SnapshotID: done.SnapshotID, Mode: "replace"}, "ada", ""); err == nil {
		t.Fatal("replace without the confirmation was accepted")
	}

	eng.fail = op("the disk filled up")
	failed, err := s.StartBackup(dest.ID, "ada", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, s, failed.ID); got.State != jobFailed {
		t.Fatalf("expected failure, got %+v", got)
	}
	if len(got) != 1 || got[0].Name != "backup.failed" || got[0].Severity != events.Critical || got[0].Key != "backup.failed:"+dest.ID || got[0].Fields["job"] != failed.ID {
		t.Fatalf("event %+v", got)
	}
	again := s.Overview(context.Background())
	if again.InternalAffairs.LastBackedHead != head {
		t.Fatal("a failed backup should leave the recorded ledger head")
	}
	if len(audited) == 0 || !strings.Contains(strings.Join(audited, "\n"), "backup destination saved") {
		t.Fatalf("audit %+v", audited)
	}
}

func TestRealResticRoundTrip(t *testing.T) {
	bin := resticBin(t)
	dir := t.TempDir()
	shares := filepath.Join(dir, "shares")
	ia := filepath.Join(shares, "internal-affairs")
	if err := os.MkdirAll(ia, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ia, "Home.md"), []byte("cabinet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ia, "ledger.jsonl"), []byte("{\"line\":\"aabbccddeeff00112233445566778899\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		Secrets: box, DataDir: dir, SharesRoot: shares,
		Engine:  &CLI{Bin: bin, DataDir: dir},
		Compose: func(context.Context) []string { return nil },
		Cmd:     absentCmd{},
	}
	dest, err := s.SaveDestination(DestinationInput{
		Name: "Local", Kind: "local", Enabled: true, Path: filepath.Join(dir, "dest"),
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6, ScheduleMode: "manual",
	}, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.StartBackup(dest.ID, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, s, job.ID)
	if done.State != jobSucceeded {
		t.Fatalf("backup %+v", done)
	}
	snaps, err := s.Snapshots(context.Background(), dest.ID)
	if err != nil || len(snaps) == 0 {
		t.Fatalf("snapshots %+v %v", snaps, err)
	}
	check, err := s.StartCheck(dest.ID, false, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, s, check.ID); got.State != jobSucceeded {
		t.Fatalf("check %+v", got)
	}
}

type scriptRule struct {
	prefix string
	out    string
	err    error
}

type scriptCmd struct {
	have  map[string]bool
	rules []scriptRule
	calls []string
}

func (c *scriptCmd) Look(name string) (string, error) {
	if c.have[name] {
		return "/usr/bin/" + name, nil
	}
	return "", exec.ErrNotFound
}

func (c *scriptCmd) Run(_ context.Context, name string, args ...string) (string, error) {
	joined := name + " " + strings.Join(args, " ")
	c.calls = append(c.calls, joined)
	for _, rule := range c.rules {
		if strings.HasPrefix(joined, rule.prefix) {
			return rule.out, rule.err
		}
	}
	return "", op("unexpected " + joined)
}

type absentCmd struct{}

func (absentCmd) Look(string) (string, error) { return "", exec.ErrNotFound }
func (absentCmd) Run(context.Context, string, ...string) (string, error) {
	return "", exec.ErrNotFound
}

type memEng struct {
	fail error
}

func (m *memEng) Available() error { return nil }
func (m *memEng) Ensure(context.Context, Repo) error {
	if m.fail != nil {
		return nil
	}
	return nil
}
func (m *memEng) Probe(context.Context, Repo) error { return nil }
func (m *memEng) Backup(_ context.Context, repo Repo, _ BackupReq, on ProgressFunc) (Summary, error) {
	if repo.Password == "" {
		return Summary{}, op("missing password")
	}
	if m.fail != nil {
		return Summary{}, m.fail
	}
	if on != nil {
		on(Progress{Percent: 40, BytesDone: 4, BytesTotal: 10, FilesDone: 1, FilesTotal: 2})
	}
	return Summary{SnapshotID: "abcdef0123456789"}, nil
}
func (m *memEng) Forget(context.Context, Repo, string, Retention) error { return m.fail }
func (m *memEng) Check(context.Context, Repo, bool, ProgressFunc) error { return nil }
func (m *memEng) Snapshots(context.Context, Repo, string) ([]Snapshot, error) {
	return []Snapshot{{ID: "abcdef0123456789", ShortID: "abcdef01"}}, nil
}
func (m *memEng) List(context.Context, Repo, string) ([]Node, error) {
	return []Node{{Name: "note.txt", Type: "file", Path: "/data/note.txt", Size: 4}}, nil
}
func (m *memEng) Restore(_ context.Context, _ Repo, _ string, paths []string, target string, _ ProgressFunc) error {
	for _, p := range paths {
		full := filepath.Join(target, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte("note"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func waitJob(t *testing.T, s *Service, id string) Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := s.Job(id)
		if ok && (job.State == jobSucceeded || job.State == jobFailed) {
			return job
		}
		time.Sleep(15 * time.Millisecond)
	}
	job, _ := s.Job(id)
	t.Fatalf("job %s did not finish: %+v", id, job)
	return Job{}
}

func hasPrefixCall(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func resticBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("RESTIC_BIN"); p != "" {
		return p
	}
	if p, err := exec.LookPath("restic"); err == nil {
		return p
	}
	if st, err := os.Stat("/tmp/restic-dl/restic"); err == nil && !st.IsDir() {
		return "/tmp/restic-dl/restic"
	}
	t.Skip("restic is not installed")
	return ""
}
