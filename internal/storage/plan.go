package storage

import (
	"crypto/subtle"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const minDiskBytes = 64 << 20

var (
	poolNameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	shareNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	uuidRe      = regexp.MustCompile(`^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}$`)
)

// zfsReserved are words zpool treats as vdev keywords.
var zfsReserved = map[string]bool{
	"mirror": true, "raidz": true, "raidz1": true, "raidz2": true, "raidz3": true,
	"cache": true, "log": true, "spare": true, "disk": true, "file": true,
}

// layoutSpec is one legal combination of filesystem and layout.
type layoutSpec struct {
	FS     string
	Layout string
	Label  string
	Min    int
	Max    int
	Even   bool
}

var layoutSpecs = []layoutSpec{
	{FSBtrfs, LayoutSingle, "Single", 1, 0, false},
	{FSBtrfs, LayoutRAID1, "RAID 1", 2, 0, false},
	{FSBtrfs, LayoutRAID10, "RAID 10", 4, 0, true},
	{FSZFS, LayoutMirror, "Mirror", 2, 0, true},
	{FSZFS, LayoutRAIDZ1, "RAIDZ1", 3, 0, false},
	{FSZFS, LayoutRAIDZ2, "RAIDZ2", 4, 0, false},
	{FSXFS, LayoutSingle, "Single disk", 1, 1, false},
}

func findLayout(fs, layout string) (layoutSpec, bool) {
	fs = strings.ToLower(strings.TrimSpace(fs))
	layout = strings.ToLower(strings.TrimSpace(layout))
	for _, s := range layoutSpecs {
		if s.FS == fs && s.Layout == layout {
			return s, true
		}
	}
	return layoutSpec{}, false
}

// validateLayout checks the filesystem, the layout and the disk count.
// It does not look at the disks themselves.
func validateLayout(fs, layout string, n int) error {
	spec, ok := findLayout(fs, layout)
	if !ok {
		return input("Choose btrfs single, raid1 or raid10, ZFS mirror, raidz1 or raidz2, or a single XFS disk.")
	}
	if n < spec.Min {
		return inputf("%s %s needs at least %d disks.", displayFS(fs), spec.Label, spec.Min)
	}
	if spec.Max > 0 && n > spec.Max {
		return inputf("An XFS pool is one disk. Create another pool for the other disks.")
	}
	if spec.Even && n%2 != 0 {
		return inputf("%s %s needs an even number of disks.", displayFS(fs), spec.Label)
	}
	return nil
}

func displayFS(fs string) string {
	switch fs {
	case FSBtrfs:
		return "btrfs"
	case FSZFS:
		return "ZFS"
	case FSXFS:
		return "XFS"
	default:
		return fs
	}
}

func validatePoolName(fs, name string) error {
	name = strings.TrimSpace(name)
	if !poolNameRe.MatchString(name) {
		return input("Name the pool with a lower-case letter, then letters, numbers or hyphens (up to 32 characters).")
	}
	if fs == FSZFS && zfsReserved[name] {
		return inputf("ZFS uses %q as a keyword. Name the pool something else, such as %spool.", name, name)
	}
	return nil
}

// confirmMatch reports whether the typed value is the disk's serial or by-id name.
func confirmMatch(typed, want string) bool {
	typed = strings.TrimSpace(typed)
	want = strings.TrimSpace(want)
	if typed == "" || want == "" || len(typed) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(typed), []byte(want)) == 1
}

// FormatArgs is the mkfs or zpool create command for a pool.
// devs are device paths, never a shell string. cachefile and mount are
// required for ZFS so the pool is not imported from the system cache and is
// mounted where FELDCORE expects it.
func FormatArgs(fs, layout, name, mount, cachefile string, devs []string) (string, []string, error) {
	if err := validatePoolName(fs, name); err != nil {
		return "", nil, err
	}
	if err := validateLayout(fs, layout, len(devs)); err != nil {
		return "", nil, err
	}
	for _, d := range devs {
		if !devicePath(d) {
			return "", nil, input("A pool can only be created on a block device FELDCORE discovered.")
		}
	}
	switch fs {
	case FSBtrfs:
		data, meta := btrfsProfiles(layout, len(devs))
		args := []string{"-f", "-L", name, "-d", data, "-m", meta}
		args = append(args, devs...)
		return "mkfs.btrfs", args, nil
	case FSXFS:
		label := name
		if len(label) > 12 {
			label = label[:12]
		}
		return "mkfs.xfs", []string{"-f", "-L", label, devs[0]}, nil
	case FSZFS:
		if mount == "" || cachefile == "" {
			return "", nil, input("ZFS needs a mount point and a cache file.")
		}
		args := []string{
			"create", "-f",
			"-o", "ashift=12",
			"-o", "cachefile=" + cachefile,
			"-m", mount,
			"-O", "atime=off",
			"-O", "xattr=sa",
			name,
		}
		args = append(args, zfsVdev(layout, devs)...)
		return "zpool", args, nil
	default:
		return "", nil, input("That filesystem is not supported.")
	}
}

func btrfsProfiles(layout string, n int) (data, meta string) {
	switch layout {
	case LayoutRAID1:
		return "raid1", "raid1"
	case LayoutRAID10:
		return "raid10", "raid10"
	default:
		if n == 1 {
			return "single", "dup"
		}
		return "single", "raid1"
	}
}

func zfsVdev(layout string, devs []string) []string {
	switch layout {
	case LayoutRAIDZ1:
		return append([]string{"raidz1"}, devs...)
	case LayoutRAIDZ2:
		return append([]string{"raidz2"}, devs...)
	default:
		var out []string
		for i := 0; i < len(devs); i += 2 {
			out = append(out, "mirror", devs[i], devs[i+1])
		}
		return out
	}
}

// MountArgs mounts a filesystem by its UUID. The device path is not used.
func MountArgs(fs, uuid, target string) (string, []string, error) {
	if !uuidRe.MatchString(uuid) {
		return "", nil, input("The filesystem has no UUID, so it was not mounted.")
	}
	if target == "" || !strings.HasPrefix(filepath.Clean(target), "/") {
		return "", nil, input("The mount point is not valid.")
	}
	switch fs {
	case FSBtrfs, FSXFS:
		return "mount", []string{"-t", fs, "-o", "noatime", "-U", uuid, target}, nil
	default:
		return "", nil, input("That filesystem is not mounted by UUID.")
	}
}

// AddArgs is the command that adds one disk. XFS cannot grow onto a second disk.
func AddArgs(fs, layout, pool, mount, existing, dev string) (string, []string, error) {
	if !devicePath(dev) {
		return "", nil, input("That is not a block device.")
	}
	switch fs {
	case FSBtrfs:
		if mount == "" {
			return "", nil, input("Mount the pool before adding a disk.")
		}
		return "btrfs", []string{"device", "add", "-f", dev, mount}, nil
	case FSZFS:
		if existing == "" || !devicePath(existing) {
			return "", nil, input("The pool has no disk to attach to.")
		}
		// attach widens a mirror, or grows a raidz when the installed ZFS
		// supports expansion. A bare `zpool add` of one disk is refused
		// because it would drop redundancy.
		return "zpool", []string{"attach", pool, existing, dev}, nil
	default:
		return "", nil, input("An XFS pool is one disk. Create another pool for this disk.")
	}
}

// ReplaceArgs copies one member onto a new disk.
func ReplaceArgs(fs, pool, mount, oldDev, newDev string) (string, []string, error) {
	if !devicePath(oldDev) || !devicePath(newDev) {
		return "", nil, input("Replace needs the current disk and the new disk.")
	}
	switch fs {
	case FSBtrfs:
		return "btrfs", []string{"replace", "start", "-B", oldDev, newDev, mount}, nil
	case FSZFS:
		return "zpool", []string{"replace", pool, oldDev, newDev}, nil
	default:
		return "", nil, input("An XFS disk cannot be replaced in place. Create a new pool and copy the files across.")
	}
}

// MaintArgs is the scrub, balance or trim command. Balance exists only on btrfs.
func MaintArgs(fs, kind, pool, mount string, xfsScrub bool) (string, []string, error) {
	switch kind {
	case KindScrub:
		switch fs {
		case FSBtrfs:
			return "btrfs", []string{"scrub", "start", "-B", mount}, nil
		case FSZFS:
			return "zpool", []string{"scrub", pool}, nil
		case FSXFS:
			if !xfsScrub {
				return "", nil, input("XFS scrub needs xfs_scrub, and it is not installed. FELDCORE will not install it for you.")
			}
			return "xfs_scrub", []string{"-v", mount}, nil
		}
	case KindBalance:
		if fs != FSBtrfs {
			return "", nil, input("Only a btrfs pool can be balanced. ZFS and XFS have no balance.")
		}
		return "btrfs", []string{"balance", "start", mount}, nil
	case KindTrim:
		if fs == FSZFS {
			return "zpool", []string{"trim", pool}, nil
		}
		return "fstrim", []string{"-v", mount}, nil
	}
	return "", nil, input("Choose scrub, balance or trim.")
}

func devicePath(p string) bool {
	if p == "" || strings.Contains(p, "..") || strings.ContainsAny(p, " \t\n") {
		return false
	}
	return strings.HasPrefix(p, "/dev/")
}

// hdparmStandby maps the minutes shown in the UI onto hdparm -S.
// The set is fixed so a request cannot pass an arbitrary standby value.
func hdparmStandby(minutes int) (int, error) {
	switch minutes {
	case 0:
		return 0, nil
	case 5:
		return 60, nil
	case 10:
		return 120, nil
	case 20:
		return 240, nil
	case 30:
		return 241, nil
	case 60:
		return 242, nil
	case 120:
		return 244, nil
	default:
		return 0, input("Spin-down is off, or 5, 10, 20, 30, 60 or 120 minutes.")
	}
}

func validateSchedule(kind string, hours int, enabled bool) error {
	switch kind {
	case KindScrub, KindBalance, KindTrim:
	default:
		return input("Choose scrub, balance or trim.")
	}
	if !enabled {
		return nil
	}
	if hours < 1 || hours > 8760 {
		return input("Run the job every 1 to 8760 hours.")
	}
	return nil
}

func scheduleNext(now time.Time, hours int) time.Time {
	if hours < 1 {
		hours = 1
	}
	return now.Add(time.Duration(hours) * time.Hour)
}

// dueSchedules returns schedules whose next run is due. now is the caller's clock.
func dueSchedules(now time.Time, in []Schedule) []Schedule {
	var out []Schedule
	for _, s := range in {
		if !s.Enabled || s.EveryHours < 1 {
			continue
		}
		if s.NextRun.IsZero() || !now.Before(s.NextRun) {
			out = append(out, s)
		}
	}
	return out
}

func toolMissing(fs string) error {
	switch fs {
	case FSBtrfs:
		return input("btrfs-progs is not installed. FELDCORE will not install it for you.")
	case FSXFS:
		return input("xfsprogs is not installed. FELDCORE will not install it for you.")
	case FSZFS:
		return input("ZFS is not available. zpool is missing or the kernel module is not loaded. FELDCORE will not install it for you.")
	default:
		return input("That filesystem is not supported.")
	}
}

func layoutCatalog(btrfs, xfs, zfs bool) []LayoutInfo {
	out := make([]LayoutInfo, 0, len(layoutSpecs))
	for _, s := range layoutSpecs {
		ok := false
		switch s.FS {
		case FSBtrfs:
			ok = btrfs
		case FSXFS:
			ok = xfs
		case FSZFS:
			ok = zfs
		}
		out = append(out, LayoutInfo{
			FS: s.FS, Layout: s.Layout, Label: s.Label,
			Min: s.Min, Max: s.Max, Even: s.Even, Available: ok,
		})
	}
	return out
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func jobName(kind, pool string) string {
	return "storage-" + kind + "-" + pool
}

// escapeMountUnit is the systemd unit name for a mount path.
func escapeMountUnit(path string) string {
	path = strings.Trim(filepath.Clean(path), "/")
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '/':
			b.WriteByte('-')
		default:
			fmt.Fprintf(&b, "\\x%02x", r)
		}
	}
	return b.String() + ".mount"
}
