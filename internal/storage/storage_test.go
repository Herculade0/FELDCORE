package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeKeepAndRefuseOSFormat(t *testing.T) {
	disks := []SetupDisk{
		{Name: "sda", Path: "/dev/sda", OS: true, Mount: "/", Size: 64 << 30, Children: []SetupPart{{Name: "sda1", Path: "/dev/sda1", Mount: "/", OS: true}}},
		{Name: "sdb", Path: "/dev/sdb", Size: 1 << 40},
	}
	p, err := Describe(disks, Choice{Mode: "keep", Mount: "/mnt/feldcore/data"})
	if err != nil || p.Destructive || p.Mount != "/mnt/feldcore/data" {
		t.Fatalf("keep %+v %v", p, err)
	}
	if _, err := Describe(disks, Choice{Mode: "disk", Device: "/dev/sda", Format: true, Mount: "/mnt/feldcore"}); err == nil {
		t.Fatal("formatting the OS disk was accepted")
	}
	p, err = Describe(disks, Choice{Mode: "disk", Device: "sdb", Format: true, Mount: "/mnt/feldcore"})
	if err != nil || !p.Destructive || p.ConfirmPhrase != "ERASE sdb" {
		t.Fatalf("format plan %+v %v", p, err)
	}
	if _, err := Confirm(disks, Choice{Mode: "disk", Device: "sdb", Format: true, Mount: "/mnt/feldcore", Confirm: "erase sdb"}); err == nil {
		t.Fatal("wrong phrase accepted")
	}
	mount, err := Confirm(disks, Choice{Mode: "disk", Device: "sdb", Format: true, Mount: "/mnt/feldcore", Confirm: "ERASE sdb"})
	if err != nil || mount != "/mnt/feldcore" {
		t.Fatalf("confirm %s %v", mount, err)
	}
	if _, err := Describe(disks, Choice{Mode: "pool", Pool: "data", Format: true, Mount: "/mnt/data"}); err == nil {
		t.Fatal("pool format was accepted")
	}
	p, err = Describe(disks, Choice{Mode: "pool", Pool: "data", Mount: "/mnt/data"})
	if err != nil || p.Destructive || p.Mount != "/mnt/data" {
		t.Fatalf("pool %+v %v", p, err)
	}
}

func TestLinuxApplyFormatsOnlyAfterPhrase(t *testing.T) {
	root := t.TempDir()
	writeFakeSys(t, root)
	cmd := &fakeExec{}
	vol := New(root, cmd)
	ctx := context.Background()
	disks, err := vol.Disks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var data SetupDisk
	foundOS, foundData := false, false
	for _, d := range disks {
		if d.Name == "sda" && d.OS {
			foundOS = true
		}
		if d.Name == "sdb" && !d.OS {
			foundData = true
			data = d
		}
	}
	if len(disks) != 2 || !foundOS || !foundData || data.Model != "Data" {
		t.Fatalf("disks %+v", disks)
	}
	if _, err := vol.Apply(ctx, Choice{Mode: "disk", Device: "/dev/sdb", Format: true, Mount: "/mnt/feldcore", Confirm: "no"}); err == nil {
		t.Fatal("apply without the phrase")
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("commands ran: %v", cmd.calls)
	}
	mount, err := vol.Apply(ctx, Choice{Mode: "disk", Device: "/dev/sdb", Format: true, Mount: "/mnt/feldcore", Confirm: "ERASE sdb"})
	if err != nil || mount != "/mnt/feldcore" {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.calls, "\n")
	if !strings.Contains(joined, "mkfs.ext4 -F -L feldcore /dev/sdb") || !strings.Contains(joined, "mount /dev/sdb /mnt/feldcore") {
		t.Fatal(joined)
	}
	mkdirAt := strings.Index(joined, "mkdir -p /mnt/feldcore\n")
	mkfsAt := strings.Index(joined, "mkfs.ext4 -F -L feldcore /dev/sdb")
	if mkdirAt < 0 || mkfsAt < 0 || mkdirAt > mkfsAt {
		t.Fatalf("mkdir must run before mkfs:\n%s", joined)
	}
	fstab, err := os.ReadFile(filepath.Join(root, "etc", "fstab"))
	if err != nil || !strings.Contains(string(fstab), "/mnt/feldcore") {
		t.Fatalf("fstab %s %v", fstab, err)
	}
}

func writeFakeSys(t *testing.T, root string) {
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
	must(filepath.Join(root, "sys", "block", "sda", "device", "model"), "OS Disk\n")
	must(filepath.Join(root, "sys", "block", "sdb", "size"), "2147483648\n")
	must(filepath.Join(root, "sys", "block", "sdb", "device", "model"), "Data\n")
	must(filepath.Join(root, "sys", "block", "loop0", "size"), "100\n")
}

type fakeExec struct {
	calls []string
	fail  func(name string) error
}

func (f *fakeExec) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.fail != nil {
		if err := f.fail(name); err != nil {
			return "", err
		}
	}
	if name == "blkid" {
		return "01234567-89ab-cdef-0123-456789abcdef", nil
	}
	return "", nil
}
