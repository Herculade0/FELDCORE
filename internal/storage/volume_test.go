package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceVolumeUsesInventoryAndExistingPool(t *testing.T) {
	disks := []Disk{
		{ID: "sda", Name: "sda", Path: "/dev/sda", SizeBytes: 64 << 30, OS: true, Mountpoint: "/", Model: "OS", ChildMounts: []string{"/"}},
		{ID: "sdb", Name: "sdb", Path: "/dev/sdb", SizeBytes: 1 << 40, Model: "Data", Serial: "SER123", ByID: "/dev/disk/by-id/ata-data", Eligible: true},
	}
	s, run, aud := newSvc(t, disks, false, nil)
	fstab := filepath.Join(t.TempDir(), "fstab")
	mountDir := filepath.Join(t.TempDir(), "data")
	s.applySystem = true
	s.fstab = fstab
	run.fn = func(name string, args []string) (string, error) {
		if name == "blkid" {
			return "01234567-89ab-cdef-0123-456789abcdef\n", nil
		}
		return "", nil
	}
	ctx := context.Background()

	listed, err := s.Disks(ctx)
	if err != nil || len(listed) != 2 || listed[1].Serial != "SER123" || listed[1].ByID == "" || !listed[0].OS {
		t.Fatalf("disks %+v %v", listed, err)
	}
	if len(listed[0].Children) != 1 || listed[0].Children[0].Mount != "/" {
		t.Fatalf("os child %+v", listed[0])
	}

	if _, err := s.Apply(ctx, Choice{Mode: "disk", Device: "/dev/sdb", Format: true, Mount: mountDir, Confirm: "no"}); err == nil {
		t.Fatal("format without the phrase")
	}
	if run.saw("mkfs") {
		t.Fatal("mkfs ran without the phrase")
	}
	if _, err := s.Apply(ctx, Choice{Mode: "disk", Device: "/dev/sda", Format: true, Mount: mountDir, Confirm: "ERASE sda"}); err == nil {
		t.Fatal("formatted the operating-system disk")
	}
	if run.saw("mkfs") {
		t.Fatal("mkfs ran for the operating-system disk")
	}

	got, err := s.Apply(ctx, Choice{Mode: "disk", Device: "sdb", Format: true, Mount: mountDir, Confirm: "ERASE sdb"})
	if err != nil || got != mountDir {
		t.Fatal(err)
	}
	if !run.saw("mkfs.ext4 -F -L feldcore /dev/sdb") || !run.saw("mount /dev/sdb "+mountDir) {
		t.Fatalf("commands %+v", run.calls)
	}
	body, err := os.ReadFile(fstab)
	if err != nil || !strings.Contains(string(body), "UUID=01234567-89ab-cdef-0123-456789abcdef "+mountDir+" ext4") {
		t.Fatalf("fstab %s %v", body, err)
	}
	formatted := false
	for _, ev := range *aud {
		if ev.action == "storage.setup.format" && ev.result == "ok" && ev.target == "/dev/sdb" {
			formatted = true
		}
	}
	if !formatted {
		t.Fatalf("audit %+v", *aud)
	}

	if err := s.update(func(f *file) error {
		f.Pools = []poolRec{{
			Name: "data", FS: FSBtrfs, Layout: LayoutSingle,
			UUID: "01234567-89ab-cdef-0123-456789abcdef", Mount: "/mnt/data",
			Members: []Member{{ID: "sdb", Path: "/dev/sdb", Serial: "SER123"}},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run.mu.Lock()
	run.calls = nil
	run.mu.Unlock()
	if _, err := s.Apply(ctx, Choice{Mode: "disk", Device: "/dev/sdb", Format: true, Mount: mountDir, Confirm: "ERASE sdb"}); err == nil {
		t.Fatal("formatted a disk that is already in a pool")
	}
	if run.saw("mkfs") {
		t.Fatal("mkfs ran for a pool member")
	}

	run.fn = func(name string, args []string) (string, error) {
		if name == "findmnt" && strings.Contains(strings.Join(args, " "), "/mnt/data") {
			return `UUID="01234567-89ab-cdef-0123-456789abcdef" FSTYPE="btrfs" SOURCE="/dev/sdb" TARGET="/mnt/data"`, nil
		}
		return "", nil
	}
	plan, err := s.Plan(ctx, Choice{Mode: "pool", Pool: "data", Mount: "/srv/elsewhere"})
	if err != nil || plan.Destructive || plan.Mount != "/mnt/data" {
		t.Fatalf("existing pool plan %+v %v", plan, err)
	}
	run.mu.Lock()
	run.calls = nil
	run.mu.Unlock()
	got, err = s.Apply(ctx, Choice{Mode: "pool", Pool: "data", Mount: "/srv/elsewhere"})
	if err != nil || got != "/mnt/data" {
		t.Fatalf("apply pool %s %v", got, err)
	}
	if run.saw("mkfs") || run.saw("mount ") {
		t.Fatalf("existing pool ran a command %+v", run.calls)
	}

	plan, err = s.Plan(ctx, Choice{Mode: "pool", Pool: "other", Mount: mountDir})
	if err != nil || plan.Destructive || plan.Mount != mountDir {
		t.Fatalf("new pool name %+v %v", plan, err)
	}
	if _, err := s.Plan(ctx, Choice{Mode: "pool", Pool: "other", Format: true, Mount: mountDir}); err == nil {
		t.Fatal("assembling a new pool during setup was accepted")
	}
}
