package storage

import (
	"strings"
	"testing"
	"time"
)

func TestLayoutsAndFormatCommands(t *testing.T) {
	if err := validateLayout(FSXFS, LayoutSingle, 2); err == nil {
		t.Fatal("xfs accepted two disks")
	}
	if err := validateLayout(FSBtrfs, LayoutRAID10, 3); err == nil {
		t.Fatal("raid10 accepted 3 disks")
	}
	if err := validateLayout(FSZFS, LayoutMirror, 3); err == nil {
		t.Fatal("mirror accepted an odd count")
	}
	if err := validateLayout(FSZFS, LayoutRAIDZ1, 2); err == nil {
		t.Fatal("raidz1 accepted 2 disks")
	}
	if err := validatePoolName(FSZFS, "cache"); err == nil {
		t.Fatal("zfs pool named cache")
	}
	if err := validatePoolName(FSBtrfs, "cache"); err != nil {
		t.Fatal(err)
	}

	bin, args, err := FormatArgs(FSBtrfs, LayoutSingle, "data", "", "", []string{"/dev/loop1"})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "mkfs.btrfs" || !containsAll(args, "-d", "single", "-m", "dup", "/dev/loop1") {
		t.Fatalf("single: %s %v", bin, args)
	}
	_, args, err = FormatArgs(FSBtrfs, LayoutRAID1, "data", "", "", []string{"/dev/loop1", "/dev/loop2"})
	if err != nil || !containsAll(args, "-d", "raid1", "-m", "raid1", "/dev/loop1", "/dev/loop2") {
		t.Fatalf("raid1: %v %v", args, err)
	}
	_, args, err = FormatArgs(FSBtrfs, LayoutRAID10, "data", "", "", []string{"/dev/loop1", "/dev/loop2", "/dev/loop3", "/dev/loop4"})
	if err != nil || !containsAll(args, "-d", "raid10", "-m", "raid10") {
		t.Fatalf("raid10: %v %v", args, err)
	}
	bin, args, err = FormatArgs(FSXFS, LayoutSingle, "longpoolname", "", "", []string{"/dev/loop1"})
	if err != nil || bin != "mkfs.xfs" || !containsAll(args, "-L", "longpoolname"[:12], "/dev/loop1") {
		t.Fatalf("xfs: %s %v %v", bin, args, err)
	}
	bin, args, err = FormatArgs(FSZFS, LayoutMirror, "data", "/mnt/feldcore/data", "/var/lib/feldcore/zpool.cache", []string{"/dev/loop1", "/dev/loop2"})
	if err != nil || bin != "zpool" || !containsAll(args, "create", "cachefile=/var/lib/feldcore/zpool.cache", "-m", "/mnt/feldcore/data", "mirror", "/dev/loop1", "/dev/loop2") {
		t.Fatalf("zfs mirror: %s %v %v", bin, args, err)
	}
	_, args, err = FormatArgs(FSZFS, LayoutRAIDZ2, "data", "/mnt/feldcore/data", "/tmp/zpool.cache", []string{"/dev/loop1", "/dev/loop2", "/dev/loop3", "/dev/loop4"})
	if err != nil || !containsAll(args, "raidz2", "/dev/loop1") {
		t.Fatalf("raidz2: %v %v", args, err)
	}
	if _, _, err := FormatArgs(FSBtrfs, LayoutSingle, "data", "", "", []string{"/tmp/not-a-disk"}); err == nil {
		t.Fatal("accepted a non-device path")
	}
	bin, args, err = MountArgs(FSBtrfs, "11111111-1111-1111-1111-111111111111", "/mnt/feldcore/data")
	if err != nil || bin != "mount" || !containsAll(args, "-U", "11111111-1111-1111-1111-111111111111", "-t", "btrfs") {
		t.Fatalf("mount: %s %v %v", bin, args, err)
	}
	if _, _, err := MountArgs(FSBtrfs, "not-a-uuid", "/mnt/feldcore/data"); err == nil {
		t.Fatal("mounted without a uuid")
	}
	if _, _, err := AddArgs(FSXFS, LayoutSingle, "solo", "/mnt/solo", "", "/dev/loop2"); err == nil {
		t.Fatal("xfs add")
	}
	_, args, err = AddArgs(FSZFS, LayoutMirror, "data", "/mnt/data", "/dev/loop1", "/dev/loop3")
	if err != nil || !containsAll(args, "attach", "data", "/dev/loop1", "/dev/loop3") {
		t.Fatalf("attach: %v %v", args, err)
	}
	if joined := strings.Join(args, " "); strings.Contains(joined, " zpool add ") || strings.HasPrefix(joined, "add ") {
		t.Fatal("zpool add would drop redundancy")
	}
	if _, _, err := MaintArgs(FSZFS, KindBalance, "data", "/mnt/data", false); err == nil {
		t.Fatal("zfs balance")
	}
	if _, _, err := MaintArgs(FSXFS, KindScrub, "solo", "/mnt/solo", false); err == nil {
		t.Fatal("xfs scrub without the tool")
	}
	bin, args, err = MaintArgs(FSBtrfs, KindScrub, "data", "/mnt/feldcore/data", false)
	if err != nil || bin != "btrfs" || !containsAll(args, "scrub", "start", "-B", "/mnt/feldcore/data") {
		t.Fatalf("scrub: %s %v %v", bin, args, err)
	}
	bin, args, err = MaintArgs(FSBtrfs, KindTrim, "data", "/mnt/feldcore/data", false)
	if err != nil || bin != "fstrim" {
		t.Fatalf("trim: %s %v", bin, err)
	}
	if n, err := hdparmStandby(30); err != nil || n != 241 {
		t.Fatalf("hdparm 30: %d %v", n, err)
	}
	if _, err := hdparmStandby(7); err == nil {
		t.Fatal("arbitrary standby accepted")
	}
	if !confirmMatch(" ABC ", "ABC") || confirmMatch("", "ABC") || confirmMatch("abc", "ABC") {
		t.Fatal("confirm match")
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	due := dueSchedules(now, []Schedule{
		{Pool: "data", Kind: KindScrub, Enabled: true, EveryHours: 24, NextRun: now.Add(-time.Minute)},
		{Pool: "data", Kind: KindTrim, Enabled: false, EveryHours: 24, NextRun: now.Add(-time.Hour)},
		{Pool: "data", Kind: KindBalance, Enabled: true, EveryHours: 24, NextRun: now.Add(time.Hour)},
	})
	if len(due) != 1 || due[0].Kind != KindScrub {
		t.Fatalf("due: %+v", due)
	}
	if escapeMountUnit("/mnt/feldcore/data") != "mnt-feldcore-data.mount" {
		t.Fatal(escapeMountUnit("/mnt/feldcore/data"))
	}
	if escapeMountUnit("/mnt/feldcore/a-b") != `mnt-feldcore-a\x2db.mount` {
		t.Fatal(escapeMountUnit("/mnt/feldcore/a-b"))
	}
}

func TestGuardRefusesHostDisks(t *testing.T) {
	g := guard{}
	if err := g.check("mkfs.btrfs", []string{"-f", "/dev/loop0"}); err == nil {
		t.Fatal("formatting was allowed with host disks turned off")
	}
	if err := g.check("lsblk", []string{"-J"}); err != nil {
		t.Fatal(err)
	}
	if err := g.check("btrfs", []string{"scrub", "start", "-B", "/mnt/feldcore/data"}); err != nil {
		t.Fatal(err)
	}
	osID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	g = guard{
		permit: true,
		allow:  map[string]bool{"/dev/loop3": true},
		osDevices: func() (map[string]bool, string, error) {
			return map[string]bool{"/dev/vdc": true}, osID, nil
		},
	}
	if err := g.check("mkfs.xfs", []string{"-f", "/dev/vda"}); err == nil {
		t.Fatal("vda is not in the allow list")
	}
	if err := g.check("mkfs.btrfs", []string{"-f", "/dev/vdc"}); err == nil {
		t.Fatal("operating system disk")
	}
	if err := g.check("mkfs.xfs", []string{"-f", "/dev/loop3"}); err != nil {
		t.Fatal(err)
	}
	if err := g.check("mount", []string{"-U", osID, "/mnt/feldcore/x"}); err == nil {
		t.Fatal("mounted the operating system uuid")
	}
	if err := g.check("umount", []string{"/"}); err == nil {
		t.Fatal("unmounted /")
	}
	if err := g.check("zpool", []string{"status", "data"}); err != nil {
		t.Fatal(err)
	}
	if err := g.check("zpool", []string{"create", "-f", "data", "/dev/vda"}); err == nil {
		t.Fatal("zpool create on a host disk")
	}
}

func containsAll(args []string, want ...string) bool {
	got := " " + strings.Join(args, " ") + " "
	for _, w := range want {
		if !strings.Contains(got, " "+w+" ") && !strings.Contains(strings.Join(args, " "), w) {
			return false
		}
	}
	return true
}
