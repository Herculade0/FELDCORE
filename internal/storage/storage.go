// Package storage is FELDCORE's disk and pool control.
//
// The setup wizard also uses this package. SetupDisk, Choice and Volume are
// the wizard's keep / one-disk / pool-name step. *Service implements Volume
// and Controller: the wizard lists the same disks as the Storage page, and
// naming a pool that already exists mounts that pool. Creating a multi-disk
// pool stays on Controller, because that requires each disk serial.
package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SetupDisk is one whole disk as the setup wizard shows it. Children are
// partitions when sysfs lists them. The Storage page uses Disk, which carries
// serial, SMART and pool membership; the wizard maps those into this shape.
type SetupDisk struct {
	Name       string      `json:"name"`
	Path       string      `json:"path"`
	Size       int64       `json:"size"`
	Model      string      `json:"model,omitempty"`
	Rotational bool        `json:"rotational"`
	Removable  bool        `json:"removable"`
	FS         string      `json:"fs,omitempty"`
	Mount      string      `json:"mount,omitempty"`
	OS         bool        `json:"os"`
	Serial     string      `json:"serial,omitempty"`
	ByID       string      `json:"byId,omitempty"`
	UsedFor    string      `json:"usedFor,omitempty"`
	Children   []SetupPart `json:"children,omitempty"`
}

// SetupPart is one partition of a disk in the wizard inventory.
type SetupPart struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	FS    string `json:"fs,omitempty"`
	Mount string `json:"mount,omitempty"`
	OS    bool   `json:"os"`
}

// Choice is what the wizard asks storage to do.
//
// Mode is "keep" (a folder that already exists or should be created),
// "disk" (one whole disk or partition), or "pool" (a name plus a folder).
// Format is only honoured for mode "disk", and only together with Confirm.
type Choice struct {
	Mode    string
	Device  string
	Mount   string
	Format  bool
	FS      string
	Pool    string
	Confirm string
}

// Plan is the human description of a choice. Mount is the folder shares
// would use. ConfirmPhrase is empty unless the choice erases a disk.
type Plan struct {
	Lines         []string
	Destructive   bool
	ConfirmPhrase string
	Warning       string
	Mount         string
}

// Volume is the wizard's disk dependency. *Linux serves tests that read a
// fake sysfs tree. *Service is what the daemon passes: it implements
// Controller as well, so the wizard and the Storage page share one inventory.
type Volume interface {
	Disks(ctx context.Context) ([]SetupDisk, error)
	Plan(ctx context.Context, in Choice) (Plan, error)
	Apply(ctx context.Context, in Choice) (mount string, err error)
}

// Describe builds a plan from an already-loaded inventory. It does not
// format or mount. It does probe whether the folder can be created, and
// refuses a parent on the read-only operating-system image.
func Describe(disks []SetupDisk, in Choice) (Plan, error) {
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "keep"
	}
	switch mode {
	case "keep":
		mount, err := cleanMount(in.Mount)
		if err != nil {
			return Plan{}, err
		}
		if err := CheckMountCreatable(mount); err != nil {
			return Plan{}, err
		}
		p := Plan{
			Mount: mount,
			Lines: []string{"Store shares under " + mount + ". Nothing will be formatted."},
		}
		if onOS(mount, disks) {
			p.Warning = "That folder is on the operating-system disk. Filling it up can stop the server booting. A separate disk is safer."
		}
		return p, nil
	case "pool":
		return describePool(disks, in)
	case "disk":
		return describeDisk(disks, in)
	default:
		return Plan{}, input("Choose an existing folder, one disk, or a pool name.")
	}
}

// Confirm checks a choice, including the erase phrase, and returns the
// folder it would use. It does not run mkfs or mount.
func Confirm(disks []SetupDisk, in Choice) (string, error) {
	p, err := Describe(disks, in)
	if err != nil {
		return "", err
	}
	if p.Destructive && strings.TrimSpace(in.Confirm) != p.ConfirmPhrase {
		return "", input("Type " + p.ConfirmPhrase + " to confirm. This erases the disk.")
	}
	return p.Mount, nil
}

func describePool(disks []SetupDisk, in Choice) (Plan, error) {
	if in.Format {
		return Plan{}, input("Setup does not format a multi-disk pool. Choose one empty disk, or use a pool that is already mounted.")
	}
	name := strings.TrimSpace(in.Pool)
	if !poolName.MatchString(name) {
		return Plan{}, input("Pool names use letters, numbers, hyphens and underscores.")
	}
	mount, err := cleanMount(in.Mount)
	if err != nil {
		return Plan{}, err
	}
	if err := CheckMountCreatable(mount); err != nil {
		return Plan{}, err
	}
	p := Plan{
		Mount: mount,
		Lines: []string{
			"Remember the pool name " + name + " and store shares under " + mount + ".",
			"Multi-disk assembly is left to disk management. This step does not stripe, mirror, or erase disks.",
		},
	}
	if onOS(mount, disks) {
		p.Warning = "That folder is on the operating-system disk. A pool on its own disk is safer."
	}
	return p, nil
}

func describeDisk(disks []SetupDisk, in Choice) (Plan, error) {
	disk, part, ok := findDevice(disks, in.Device)
	if !ok {
		return Plan{}, input("That disk is not on this server.")
	}
	if disk.OS || part.OS {
		return Plan{}, input(disk.Path + " holds the operating system. Choose a different disk.")
	}
	selected := disk.Path
	name := disk.Name
	mountPoint := disk.Mount
	fs := disk.FS
	if part.Path != "" {
		selected = part.Path
		name = part.Name
		mountPoint = part.Mount
		fs = part.FS
	}
	if mounted(disk) && in.Format {
		return Plan{}, input(disk.Path + " is mounted. Unmount it before erasing it, or use the folder that is already there.")
	}
	if in.Format {
		fsName := strings.TrimSpace(in.FS)
		if fsName == "" {
			fsName = "ext4"
		}
		if fsName != "ext4" {
			return Plan{}, input("Setup can format a single disk as ext4. Other layouts belong to disk management.")
		}
		mount, err := cleanMount(firstNonEmpty(in.Mount, "/mnt/feldcore/data"))
		if err != nil {
			return Plan{}, err
		}
		if err := CheckMountCreatable(mount); err != nil {
			return Plan{}, err
		}
		phrase := "ERASE " + name
		return Plan{
			Mount:         mount,
			Destructive:   true,
			ConfirmPhrase: phrase,
			Warning:       "This erases everything on " + selected + ". It cannot be undone.",
			Lines: []string{
				"Erase " + selected + " (" + humanSize(disk.Size) + ").",
				"Create an ext4 filesystem labelled feldcore.",
				"Mount it at " + mount + " and add it to /etc/fstab.",
				"Store shares under " + mount + ".",
				"Type " + phrase + " to confirm.",
			},
		}, nil
	}
	if mountPoint == "" {
		return Plan{}, input(selected + " has no filesystem mounted. Format it, or choose a folder that is already mounted.")
	}
	if err := CheckMountCreatable(mountPoint); err != nil {
		return Plan{}, err
	}
	return Plan{
		Mount: mountPoint,
		Lines: []string{
			"Use the filesystem already mounted at " + mountPoint + " (" + selected + ", " + orUnknown(fs) + ").",
			"Nothing will be formatted.",
		},
	}, nil
}

func findDevice(disks []SetupDisk, device string) (SetupDisk, SetupPart, bool) {
	device = strings.TrimSpace(device)
	if device == "" {
		return SetupDisk{}, SetupPart{}, false
	}
	for _, d := range disks {
		if d.Path == device || "/dev/"+d.Name == device || d.Name == device {
			return d, SetupPart{}, true
		}
		for _, p := range d.Children {
			if p.Path == device || "/dev/"+p.Name == device || p.Name == device {
				return d, p, true
			}
		}
	}
	return SetupDisk{}, SetupPart{}, false
}

func mounted(d SetupDisk) bool {
	if d.Mount != "" {
		return true
	}
	for _, p := range d.Children {
		if p.Mount != "" {
			return true
		}
	}
	return false
}

func onOS(mount string, disks []SetupDisk) bool {
	mount = strings.TrimRight(mount, "/")
	if mount == "" {
		mount = "/"
	}
	best := ""
	osDisk := false
	consider := func(path string, isOS bool) {
		if path == "" {
			return
		}
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
		hit := path == "/" || mount == path || strings.HasPrefix(mount, path+"/")
		if hit && len(path) >= len(best) {
			best = path
			osDisk = isOS
		}
	}
	for _, d := range disks {
		consider(d.Mount, d.OS)
		for _, p := range d.Children {
			consider(p.Mount, p.OS)
		}
	}
	return osDisk
}

func cleanMount(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", input("Choose an absolute folder, for example /mnt/feldcore/data.")
	}
	if strings.ContainsAny(path, " \t\n\r") {
		return "", input("The folder path cannot contain spaces.")
	}
	switch strings.TrimRight(path, "/") {
	case "", "/", "/boot", "/etc", "/usr", "/bin", "/sbin", "/lib", "/proc", "/sys", "/dev":
		return "", input("That folder is part of the operating system. Choose a data folder.")
	}
	return path, nil
}

func humanSize(n int64) string {
	if n <= 0 {
		return "unknown size"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 10 || i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown type"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}

// IsInput reports whether err is a validation error.
func IsInput(err error) bool {
	var ie *InputError
	return errors.As(err, &ie)
}
