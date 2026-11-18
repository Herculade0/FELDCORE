package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestFormatDefaultsToDataMount(t *testing.T) {
	disks := []SetupDisk{
		{Name: "sda", Path: "/dev/sda", OS: true, Mount: "/", Size: 64 << 30},
		{Name: "sdb", Path: "/dev/sdb", Size: 1 << 40},
	}
	p, err := Describe(disks, Choice{Mode: "disk", Device: "sdb", Format: true})
	if err != nil || p.Mount != "/mnt/feldcore/data" || !p.Destructive {
		t.Fatalf("default mount %+v %v", p, err)
	}
}

func TestReadOnlyParentBlocksPlanConfirmAndApply(t *testing.T) {
	orig := createProbe
	t.Cleanup(func() { createProbe = orig })
	createProbe = func(string) error { return syscall.EROFS }

	disks := []SetupDisk{
		{Name: "sda", Path: "/dev/sda", OS: true, Mount: "/", Size: 64 << 30},
		{Name: "sdb", Path: "/dev/sdb", Size: 1 << 40},
	}
	for _, in := range []Choice{
		{Mode: "keep", Mount: "/srv/feldcore"},
		{Mode: "disk", Device: "sdb", Format: true, Mount: "/srv/feldcore"},
		{Mode: "pool", Pool: "data", Mount: "/srv/feldcore"},
	} {
		p, err := Describe(disks, in)
		if err == nil || p.Mount != "" || !strings.Contains(err.Error(), readOnlyFolderMsg) {
			t.Fatalf("plan %+v %v for %+v", p, err, in)
		}
		if _, err := Confirm(disks, in); err == nil || !strings.Contains(err.Error(), "read-only operating-system image") || !strings.Contains(err.Error(), "/mnt/feldcore") {
			t.Fatalf("confirm %v for %+v", err, in)
		}
	}

	root := t.TempDir()
	writeFakeSys(t, root)
	cmd := &fakeExec{}
	vol := New(root, cmd)
	_, err := vol.Apply(context.Background(), Choice{
		Mode: "disk", Device: "/dev/sdb", Format: true, Mount: "/srv/feldcore", Confirm: "ERASE sdb",
	})
	if err == nil || !strings.Contains(err.Error(), readOnlyFolderMsg) {
		t.Fatal(err)
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("commands ran before the folder check: %v", cmd.calls)
	}
}

func TestExistingUsableDirIsNotBlocked(t *testing.T) {
	dir := t.TempDir()
	orig := createProbe
	t.Cleanup(func() { createProbe = orig })
	createProbe = func(path string) error {
		if path == dir {
			return nil
		}
		return syscall.EROFS
	}
	disks := []SetupDisk{{Name: "sdb", Path: "/dev/sdb", Size: 1 << 40}}
	p, err := Describe(disks, Choice{Mode: "keep", Mount: dir})
	if err != nil || p.Mount != dir {
		t.Fatalf("usable dir %+v %v", p, err)
	}
	if _, err := Confirm(disks, Choice{Mode: "keep", Mount: dir}); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionDeniedDoesNotBlockUnownedPaths(t *testing.T) {
	orig := createProbe
	t.Cleanup(func() { createProbe = orig })
	createProbe = func(string) error { return syscall.EACCES }
	disks := []SetupDisk{{Name: "sdb", Path: "/dev/sdb", Size: 1 << 40}}
	p, err := Describe(disks, Choice{Mode: "keep", Mount: "/mnt/feldcore"})
	if os.Geteuid() == 0 {
		if err == nil || !strings.Contains(err.Error(), readOnlyFolderMsg) {
			t.Fatalf("root EACCES should block, got %+v %v", p, err)
		}
		return
	}
	if err != nil || p.Mount != "/mnt/feldcore" {
		t.Fatalf("custom mount %+v %v", p, err)
	}
}

func TestApplyDoesNotFormatWhenMkdirFails(t *testing.T) {
	root := t.TempDir()
	writeFakeSys(t, root)
	cmd := &fakeExec{fail: func(name string) error {
		if name == "mkdir" {
			return errors.New("mkdir /mnt/feldcore: read-only file system")
		}
		return nil
	}}
	vol := New(root, cmd)
	_, err := vol.Apply(context.Background(), Choice{
		Mode: "disk", Device: "/dev/sdb", Format: true, Mount: "/mnt/feldcore", Confirm: "ERASE sdb",
	})
	if err == nil || !strings.Contains(err.Error(), "Could not create /mnt/feldcore") {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.calls, "\n")
	if strings.Contains(joined, "mkfs") || strings.Contains(joined, "mount ") {
		t.Fatalf("format ran after mkdir failed:\n%s", joined)
	}
	if !strings.Contains(joined, "mkdir -p /mnt/feldcore") {
		t.Fatalf("mkdir was not attempted:\n%s", joined)
	}
}

func TestServiceApplyDoesNotFormatWhenMkdirFails(t *testing.T) {
	disks := []Disk{
		{ID: "sdb", Name: "sdb", Path: "/dev/sdb", SizeBytes: 1 << 40, Model: "Data", Serial: "SER123", Eligible: true},
	}
	s, run, _ := newSvc(t, disks, true, nil)
	s.applySystem = true
	orig := mkdirAll
	t.Cleanup(func() { mkdirAll = orig })
	mkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir /mnt/feldcore/data: read-only file system")
	}
	mount := filepath.Join(t.TempDir(), "data")
	_, err := s.Apply(context.Background(), Choice{
		Mode: "disk", Device: "/dev/sdb", Format: true, Mount: mount, Confirm: "ERASE sdb",
	})
	if err == nil || !strings.Contains(err.Error(), "Could not create") {
		t.Fatal(err)
	}
	if run.saw("mkfs") || run.saw("mount ") {
		t.Fatalf("format ran after mkdir failed: %+v", run.calls)
	}
}

func TestCreatePoolDoesNotFormatWhenMkdirFails(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, _ := newSvc(t, []Disk{d}, true, nil)
	orig := mkdirAll
	t.Cleanup(func() { mkdirAll = orig })
	mkdirAll = func(string, os.FileMode) error {
		return errors.New("read-only file system")
	}
	_, err := s.CreatePool(context.Background(), CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{d.ID}, Confirm: []string{"SERIAL1"},
	})
	if err == nil || !strings.Contains(err.Error(), "Could not create the mount folder") {
		t.Fatal(err)
	}
	if run.saw("mkfs") || run.saw("zpool") {
		t.Fatalf("format ran after mkdir failed: %+v", run.calls)
	}
}

func TestBlocksCreate(t *testing.T) {
	if blocksCreate(nil) || !blocksCreate(syscall.EROFS) {
		t.Fatal("EROFS classification")
	}
	if os.Geteuid() == 0 {
		if !blocksCreate(syscall.EACCES) || !blocksCreate(syscall.EPERM) {
			t.Fatal("root permission errors should block")
		}
		return
	}
	if blocksCreate(syscall.EACCES) || blocksCreate(syscall.EPERM) {
		t.Fatal("non-root permission errors must not block absolute test paths")
	}
}

func TestRealProbeAcceptsTempDir(t *testing.T) {
	dir := t.TempDir()
	if err := CheckMountCreatable(filepath.Join(dir, "shares")); err != nil {
		t.Fatal(err)
	}
	if err := CheckMountCreatable(dir); err != nil {
		t.Fatal(err)
	}
}
