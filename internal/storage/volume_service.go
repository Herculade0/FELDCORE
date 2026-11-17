package storage

import (
	"context"
	"math"
	"os"
	"strings"
	"time"
)

// *Service is the wizard's volume and the Storage page's controller.
var (
	_ Volume     = (*Service)(nil)
	_ Volume     = (*Linux)(nil)
	_ Controller = (*Service)(nil)
)

// Disks is the wizard inventory. It is the Storage page inventory, mapped
// into the smaller shape the setup step already shows.
func (s *Service) Disks(ctx context.Context) ([]SetupDisk, error) {
	view, err := s.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	return toSetupDisks(view.Disks), nil
}

// Plan describes a wizard choice. A pool name that the controller already
// has is that pool's mount. Anything else uses the same rules as a sysfs
// inventory: keep a folder, use a mounted disk, or erase one disk after the
// phrase. A new pool name is recorded and is not assembled.
func (s *Service) Plan(ctx context.Context, in Choice) (Plan, error) {
	if p, ok, err := s.planExistingPool(in); ok || err != nil {
		return p, err
	}
	disks, err := s.Disks(ctx)
	if err != nil {
		return Plan{}, err
	}
	return Describe(disks, in)
}

// Apply carries out a wizard choice. Formatting still requires the exact
// phrase, and the command goes through the same executor the controller uses,
// so an operating-system disk is refused there too. Naming an existing pool
// mounts it by UUID and does not format anything.
func (s *Service) Apply(ctx context.Context, in Choice) (string, error) {
	if mount, ok, err := s.applyExistingPool(ctx, in); ok || err != nil {
		return mount, err
	}
	disks, err := s.Disks(ctx)
	if err != nil {
		return "", err
	}
	mount, err := Confirm(disks, in)
	if err != nil {
		return "", err
	}
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "keep"
	}
	if !in.Format || mode != "disk" {
		if err := s.ensureSetupDir(mount); err != nil {
			return "", err
		}
		return mount, nil
	}
	device := strings.TrimSpace(in.Device)
	if !strings.HasPrefix(device, "/dev/") {
		device = "/dev/" + device
	}
	if err := s.refuseSetupFormat(ctx, device); err != nil {
		s.note(ctx, "storage.setup.format", device, "error")
		return "", err
	}
	if _, err := s.run.Run(ctx, 10*time.Minute, "mkfs.ext4", "-F", "-L", "feldcore", device); err != nil {
		s.note(ctx, "storage.setup.format", device, "error")
		return "", op("Could not format " + device + ": " + err.Error())
	}
	if err := s.ensureSetupDir(mount); err != nil {
		s.note(ctx, "storage.setup.format", device, "error")
		return "", err
	}
	if _, err := s.run.Run(ctx, 30*time.Second, "mount", device, mount); err != nil {
		s.note(ctx, "storage.setup.format", device, "error")
		return "", op("Could not mount " + device + " at " + mount + ": " + err.Error())
	}
	spec := device
	if out, err := s.run.Run(ctx, 8*time.Second, "blkid", "-s", "UUID", "-o", "value", device); err == nil {
		if id := strings.TrimSpace(out); id != "" && !strings.ContainsAny(id, " \t\n/") {
			spec = "UUID=" + id
		}
	}
	if s.applySystem {
		if err := writeFstab(s.fstabFile(), spec, mount); err != nil {
			s.note(ctx, "storage.setup.format", device, "error")
			return "", err
		}
	}
	s.note(ctx, "storage.setup.format", device, "ok")
	return mount, nil
}

func (s *Service) planExistingPool(in Choice) (Plan, bool, error) {
	rec, ok := s.knownPool(in)
	if !ok {
		return Plan{}, false, nil
	}
	return Plan{
		Mount: rec.Mount,
		Lines: []string{
			"Use the existing " + rec.FS + " pool " + rec.Name + " and store shares under " + rec.Mount + ".",
			"Nothing will be formatted. The pool is mounted by its UUID.",
		},
	}, true, nil
}

func (s *Service) applyExistingPool(ctx context.Context, in Choice) (string, bool, error) {
	rec, ok := s.knownPool(in)
	if !ok {
		return "", false, nil
	}
	if err := s.Mount(ctx, rec.Name); err != nil {
		return "", true, err
	}
	return rec.Mount, true, nil
}

func (s *Service) knownPool(in Choice) (poolRec, bool) {
	if in.Format || strings.TrimSpace(in.Mode) != "pool" {
		return poolRec{}, false
	}
	name := strings.TrimSpace(in.Pool)
	if name == "" {
		return poolRec{}, false
	}
	return s.snapshot().pool(name)
}

func (s *Service) refuseSetupFormat(ctx context.Context, device string) error {
	view, err := s.Inventory(ctx)
	if err != nil {
		return err
	}
	disk, ok := findRich(view.Disks, device)
	if !ok {
		return nil
	}
	if disk.OS {
		return input(disk.Path + " holds the operating system. Choose a different disk.")
	}
	if disk.Pool != "" {
		return inputf("%s is already in pool %s. Nothing was formatted.", disk.Path, disk.Pool)
	}
	return nil
}

func (s *Service) ensureSetupDir(mount string) error {
	if !s.applySystem {
		return nil
	}
	if err := os.MkdirAll(mount, 0o755); err != nil {
		return op("Could not create " + mount + ": " + err.Error())
	}
	return nil
}

func (s *Service) fstabFile() string {
	if s.fstab != "" {
		return s.fstab
	}
	return "/etc/fstab"
}

func toSetupDisks(disks []Disk) []SetupDisk {
	out := make([]SetupDisk, 0, len(disks))
	for _, d := range disks {
		size := int64(d.SizeBytes)
		if d.SizeBytes > uint64(math.MaxInt64) {
			size = math.MaxInt64
		}
		sd := SetupDisk{
			Name: d.Name, Path: d.Path, Size: size, Model: d.Model,
			Rotational: d.Rotational, FS: d.FSType, Mount: d.Mountpoint, OS: d.OS,
			Serial: d.Serial, ByID: d.ByID, UsedFor: d.UsedFor,
		}
		for _, m := range d.ChildMounts {
			sd.Children = append(sd.Children, SetupPart{
				Name: d.Name, Path: d.Path, Mount: m, FS: d.FSType, OS: osPath([]string{m}),
			})
		}
		out = append(out, sd)
	}
	return out
}

func findRich(disks []Disk, device string) (Disk, bool) {
	device = strings.TrimSpace(device)
	if device == "" {
		return Disk{}, false
	}
	for _, d := range disks {
		if d.Path == device || d.Name == device || "/dev/"+d.Name == device {
			return d, true
		}
	}
	return Disk{}, false
}
