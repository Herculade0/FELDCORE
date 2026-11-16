// Package storage is the disk surface the setup wizard calls.
//
// Disk management (pools, replacement, scrubbing) is owned elsewhere. This
// package only lists disks and applies one choice: keep a folder, use a
// disk that is already mounted, or format a single empty disk after the
// caller repeats an explicit confirmation phrase. Multi-disk pool assembly
// is not performed here. A pool choice records a name and a folder.
package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// InputError is a validation failure safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a failed operation safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func input(msg string) error { return &InputError{Msg: msg} }
func op(msg string) error    { return &OpError{Msg: msg} }

// Disk is one whole disk. Children are partitions when sysfs lists them.
type Disk struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Model      string `json:"model,omitempty"`
	Rotational bool   `json:"rotational"`
	Removable  bool   `json:"removable"`
	FS         string `json:"fs,omitempty"`
	Mount      string `json:"mount,omitempty"`
	OS         bool   `json:"os"`
	Children   []Part `json:"children,omitempty"`
}

// Part is one partition of a disk.
type Part struct {
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

// Volume is the wizard's disk dependency. Another disk manager can replace
// the Linux implementation by providing the same three methods.
type Volume interface {
	Disks(ctx context.Context) ([]Disk, error)
	Plan(ctx context.Context, in Choice) (Plan, error)
	Apply(ctx context.Context, in Choice) (mount string, err error)
}

// Describe builds a plan from an already-loaded inventory. It does not
// touch the machine.
func Describe(disks []Disk, in Choice) (Plan, error) {
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
func Confirm(disks []Disk, in Choice) (string, error) {
	p, err := Describe(disks, in)
	if err != nil {
		return "", err
	}
	if p.Destructive && strings.TrimSpace(in.Confirm) != p.ConfirmPhrase {
		return "", input("Type " + p.ConfirmPhrase + " to confirm. This erases the disk.")
	}
	return p.Mount, nil
}

func describePool(disks []Disk, in Choice) (Plan, error) {
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

func describeDisk(disks []Disk, in Choice) (Plan, error) {
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
		mount, err := cleanMount(firstNonEmpty(in.Mount, "/mnt/feldcore"))
		if err != nil {
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
	return Plan{
		Mount: mountPoint,
		Lines: []string{
			"Use the filesystem already mounted at " + mountPoint + " (" + selected + ", " + orUnknown(fs) + ").",
			"Nothing will be formatted.",
		},
	}, nil
}

func findDevice(disks []Disk, device string) (Disk, Part, bool) {
	device = strings.TrimSpace(device)
	if device == "" {
		return Disk{}, Part{}, false
	}
	for _, d := range disks {
		if d.Path == device || "/dev/"+d.Name == device || d.Name == device {
			return d, Part{}, true
		}
		for _, p := range d.Children {
			if p.Path == device || "/dev/"+p.Name == device || p.Name == device {
				return d, p, true
			}
		}
	}
	return Disk{}, Part{}, false
}

func mounted(d Disk) bool {
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

func onOS(mount string, disks []Disk) bool {
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
		return "", input("Choose an absolute folder, for example /mnt/feldcore.")
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
