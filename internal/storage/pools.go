package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// CreatePool formats the chosen disks and mounts the filesystem by UUID.
// Confirm must be each disk's serial, or its by-id name when it has no serial,
// in the same order as Disks. Any mismatch returns before mkfs runs.
func (s *Service) CreatePool(ctx context.Context, in CreatePool) (Pool, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	pool, err := s.create(ctx, in)
	if err != nil {
		s.note(ctx, "storage.pool.create", in.Name, "error")
		return Pool{}, err
	}
	s.note(ctx, "storage.pool.create", pool.Name, "ok")
	return pool, nil
}

func (s *Service) create(ctx context.Context, in CreatePool) (Pool, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.FS = strings.ToLower(strings.TrimSpace(in.FS))
	in.Layout = strings.ToLower(strings.TrimSpace(in.Layout))
	if err := validatePoolName(in.FS, in.Name); err != nil {
		return Pool{}, err
	}
	if err := validateLayout(in.FS, in.Layout, len(in.Disks)); err != nil {
		return Pool{}, err
	}
	if len(in.Confirm) != len(in.Disks) || len(in.Disks) == 0 {
		return Pool{}, input("Type the serial of every disk. Nothing was formatted.")
	}
	mount, err := s.mountPath(in.Name)
	if err != nil {
		return Pool{}, err
	}
	if err := s.haveTool(ctx, in.FS); err != nil {
		return Pool{}, err
	}
	disks, err := s.probe(ctx)
	if err != nil {
		return Pool{}, err
	}
	st := s.snapshot()
	if _, ok := st.pool(in.Name); ok {
		return Pool{}, input("A pool with that name already exists.")
	}
	annotated := s.annotate(ctx, disks, st)
	chosen := make([]Disk, 0, len(in.Disks))
	seen := map[string]bool{}
	for i, id := range in.Disks {
		d, err := resolveDisk(annotated, id)
		if err != nil {
			return Pool{}, err
		}
		if seen[d.ID] {
			return Pool{}, input("The same disk was selected twice.")
		}
		seen[d.ID] = true
		if !confirmMatch(in.Confirm[i], d.Confirm) {
			return Pool{}, inputf("The confirmation for %s does not match its %s. Nothing was formatted.", diskLabel(d), confirmLabel(d))
		}
		if !d.Eligible {
			return Pool{}, input(d.Blocked)
		}
		if err := s.requireDevice(d); err != nil {
			return Pool{}, err
		}
		chosen = append(chosen, d)
	}
	if err := emptyDir(mount); err != nil {
		return Pool{}, err
	}
	devs := make([]string, len(chosen))
	for i, d := range chosen {
		devs[i] = d.Path
	}
	cachefile := filepath.Join(s.dataDir, "zpool.cache")
	bin, args, err := FormatArgs(in.FS, in.Layout, in.Name, mount, cachefile, devs)
	if err != nil {
		return Pool{}, err
	}
	if _, err := s.cmd(ctx, 3*time.Minute, bin, args...); err != nil {
		return Pool{}, err
	}
	var uuid string
	if in.FS == FSZFS {
		uuid, err = s.zfsGUID(ctx, in.Name)
	} else {
		uuid, err = s.readUUID(ctx, devs[0])
	}
	if err != nil {
		return Pool{}, err
	}
	if err := os.MkdirAll(mount, 0o755); err != nil {
		return Pool{}, op("Could not create the mount folder.")
	}
	if in.FS == FSBtrfs {
		// Register every member with the kernel before the UUID mount.
		// This does not format anything.
		_, _ = s.run.Run(ctx, 15*time.Second, "btrfs", "device", "scan")
	}
	if in.FS != FSZFS {
		mb, ma, err := MountArgs(in.FS, uuid, mount)
		if err != nil {
			return Pool{}, err
		}
		if _, err := s.cmd(ctx, 30*time.Second, mb, ma...); err != nil {
			return Pool{}, err
		}
		info := s.mountInfo(ctx, mount)
		if !info.Mounted || !strings.EqualFold(info.UUID, uuid) {
			_ = s.umount(ctx, mount)
			return Pool{}, op("The pool did not mount by its UUID. It was not saved.")
		}
	}
	boot := s.writeUnit(ctx, in.FS, uuid, mount)
	members := make([]Member, len(chosen))
	for i, d := range chosen {
		members[i] = memberFrom(d)
	}
	rec := poolRec{
		Name: in.Name, FS: in.FS, Layout: in.Layout, UUID: uuid, Mount: mount,
		Members: members, CreatedAt: s.now().UTC(), BootMount: boot,
	}
	if err := s.update(func(f *file) error {
		if _, ok := f.pool(in.Name); ok {
			return input("A pool with that name already exists.")
		}
		f.Pools = append(f.Pools, rec)
		return nil
	}); err != nil {
		if in.FS == FSZFS {
			_, _ = s.run.Run(ctx, 30*time.Second, "zpool", "export", in.Name)
		} else {
			_ = s.umount(ctx, mount)
		}
		return Pool{}, err
	}
	return s.decorate(ctx, rec, s.snapshot().CachePool, annotated), nil
}

func diskLabel(d Disk) string {
	if d.Model != "" && d.Serial != "" {
		return d.Model + " " + d.Serial
	}
	if d.Serial != "" {
		return d.Serial
	}
	if d.ByID != "" {
		return filepath.Base(d.ByID)
	}
	return d.Path
}

func confirmLabel(d Disk) string {
	if d.ConfirmKind == "by-id" {
		return "by-id name"
	}
	return "serial"
}

func emptyDir(path string) error {
	ents, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return op("Could not check the mount folder.")
	}
	if len(ents) > 0 {
		return input("The mount folder already has files in it. FELDCORE will not use it.")
	}
	return nil
}

func (s *Service) readUUID(ctx context.Context, dev string) (string, error) {
	var last error
	for i := 0; i < 8; i++ {
		out, err := s.run.Run(ctx, 5*time.Second, "blkid", "-p", "-o", "value", "-s", "UUID", dev)
		u := firstLine(out)
		if err == nil && uuidRe.MatchString(u) {
			return u, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if last != nil {
		return "", op("The disk was formatted but its UUID could not be read, so it was not mounted or saved. Nothing else was changed.")
	}
	return "", op("The disk was formatted but it has no UUID, so it was not mounted or saved.")
}

func (s *Service) zfsGUID(ctx context.Context, name string) (string, error) {
	out, err := s.cmd(ctx, 15*time.Second, "zpool", "get", "-H", "-o", "value", "guid", name)
	if err != nil {
		return "", err
	}
	id := firstLine(out)
	if id == "" || id == "-" {
		return "", op("ZFS did not report a pool id, so the pool was not saved.")
	}
	return id, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func (s *Service) writeUnit(ctx context.Context, fs, uuid, mount string) bool {
	if fs == FSZFS || !uuidRe.MatchString(uuid) {
		return false
	}
	body := fmt.Sprintf(`[Unit]
Description=FELDCORE pool at %s
Before=remote-fs.target

[Mount]
What=/dev/disk/by-uuid/%s
Where=%s
Type=%s
Options=noatime

[Install]
WantedBy=local-fs.target
`, mount, uuid, mount, fs)
	name := escapeMountUnit(mount)
	dir := filepath.Join(s.dataDir, "mounts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		return false
	}
	if !s.applySystem {
		return false
	}
	systemPath := filepath.Join("/etc/systemd/system", name)
	if err := os.WriteFile(systemPath, []byte(body), 0o644); err != nil {
		s.log.Warn("pool will not mount on boot", "err", err)
		return false
	}
	if _, err := s.run.Run(ctx, 20*time.Second, "systemctl", "daemon-reload"); err != nil {
		s.log.Warn("pool will not mount on boot", "err", err)
		return false
	}
	if _, err := s.run.Run(ctx, 20*time.Second, "systemctl", "enable", name); err != nil {
		s.log.Warn("pool will not mount on boot", "err", err)
		return false
	}
	return true
}

// DestroyPool forgets a pool and unmounts it. The filesystem is left on the
// disks. Confirm must be the pool name.
func (s *Service) DestroyPool(ctx context.Context, name, confirm string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	err := s.destroy(ctx, name, confirm)
	if err != nil {
		s.note(ctx, "storage.pool.destroy", name, "error")
		return err
	}
	s.note(ctx, "storage.pool.destroy", name, "ok")
	return nil
}

func (s *Service) destroy(ctx context.Context, name, confirm string) error {
	name = strings.TrimSpace(name)
	if !confirmMatch(confirm, name) {
		return input("Type the pool name to forget it. The disks were not changed.")
	}
	st := s.snapshot()
	rec, ok := st.pool(name)
	if !ok {
		return input("That pool does not exist.")
	}
	if st.CachePool == name {
		return input("This pool is the cache for appdata. Clear the cache first. Nothing was unmounted.")
	}
	for _, b := range st.Shares {
		if b.Pool == name {
			return inputf("Share %s is placed on this pool. Move it off first. Nothing was unmounted.", b.Share)
		}
	}
	if err := s.unmountPool(ctx, rec); err != nil {
		return err
	}
	unit := escapeMountUnit(rec.Mount)
	_ = os.Remove(filepath.Join(s.dataDir, "mounts", unit))
	if s.applySystem {
		_ = os.Remove(filepath.Join("/etc/systemd/system", unit))
		_, _ = s.run.Run(ctx, 20*time.Second, "systemctl", "disable", unit)
		_, _ = s.run.Run(ctx, 20*time.Second, "systemctl", "daemon-reload")
	}
	return s.update(func(f *file) error {
		pools := make([]poolRec, 0, len(f.Pools))
		for _, p := range f.Pools {
			if p.Name != name {
				pools = append(pools, p)
			}
		}
		f.Pools = pools
		sched := make([]Schedule, 0, len(f.Schedules))
		for _, sc := range f.Schedules {
			if sc.Pool != name {
				sched = append(sched, sc)
			}
		}
		f.Schedules = sched
		return nil
	})
}

// Mount mounts a saved pool by the UUID recorded when it was created.
func (s *Service) Mount(ctx context.Context, name string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	err := s.mount(ctx, name)
	if err != nil {
		s.note(ctx, "storage.pool.mount", name, "error")
		return err
	}
	s.note(ctx, "storage.pool.mount", name, "ok")
	return nil
}

func (s *Service) mount(ctx context.Context, name string) error {
	rec, ok := s.snapshot().pool(strings.TrimSpace(name))
	if !ok {
		return input("That pool does not exist.")
	}
	info := s.mountInfo(ctx, rec.Mount)
	if info.Mounted && (rec.FS == FSZFS || strings.EqualFold(info.UUID, rec.UUID)) {
		return nil
	}
	if err := s.mountRecorded(ctx, rec); err != nil {
		return err
	}
	return nil
}

// Unmount unmounts a pool and leaves it in the list, so it can be mounted again.
func (s *Service) Unmount(ctx context.Context, name string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	rec, ok := s.snapshot().pool(strings.TrimSpace(name))
	if !ok {
		s.note(ctx, "storage.pool.unmount", name, "error")
		return input("That pool does not exist.")
	}
	if err := s.unmountPool(ctx, rec); err != nil {
		s.note(ctx, "storage.pool.unmount", name, "error")
		return err
	}
	s.note(ctx, "storage.pool.unmount", rec.Name, "ok")
	return nil
}

func (s *Service) unmountPool(ctx context.Context, rec poolRec) error {
	info := s.mountInfo(ctx, rec.Mount)
	if rec.FS == FSZFS {
		if _, err := s.run.Run(ctx, 15*time.Second, "zpool", "list", rec.Name); err == nil {
			if _, err := s.cmd(ctx, time.Minute, "zpool", "export", rec.Name); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mounted {
		return nil
	}
	if rec.UUID != "" && !strings.EqualFold(info.UUID, rec.UUID) {
		return input("That folder is not the pool's filesystem. It was left mounted.")
	}
	return s.umount(ctx, rec.Mount)
}

func (s *Service) umount(ctx context.Context, mount string) error {
	if _, err := s.cmd(ctx, 30*time.Second, "umount", mount); err != nil {
		return err
	}
	return nil
}

func (s *Service) mountRecorded(ctx context.Context, rec poolRec) error {
	disks, err := s.probe(ctx)
	if err != nil {
		return err
	}
	var dev string
	for _, m := range rec.Members {
		if d, ok := findMember(disks, m); ok {
			if err := s.requireDevice(d); err != nil {
				return err
			}
			dev = d.Path
			break
		}
	}
	if dev == "" && len(rec.Members) > 0 {
		dev = rec.Members[0].Path
		if err := s.requireDevice(Disk{Path: dev}); err != nil {
			return err
		}
	}
	if rec.FS == FSZFS {
		cache := filepath.Join(s.dataDir, "zpool.cache")
		_, err := s.cmd(ctx, time.Minute, "zpool", "import", "-c", cache, "-o", "cachefile="+cache, rec.Name)
		return err
	}
	if dev == "" {
		return input("None of the pool's disks are on this server, so it was not mounted.")
	}
	if rec.FS == FSBtrfs {
		_, _ = s.run.Run(ctx, 15*time.Second, "btrfs", "device", "scan")
	}
	got, err := s.blkidUUID(ctx, dev)
	if err != nil || !strings.EqualFold(got, rec.UUID) {
		return input("The pool's UUID was not found on its disks. FELDCORE will not mount anything else.")
	}
	if err := os.MkdirAll(rec.Mount, 0o755); err != nil {
		return op("Could not create the mount folder.")
	}
	bin, args, err := MountArgs(rec.FS, rec.UUID, rec.Mount)
	if err != nil {
		return err
	}
	if _, err := s.cmd(ctx, 30*time.Second, bin, args...); err != nil {
		return err
	}
	info := s.mountInfo(ctx, rec.Mount)
	if !info.Mounted || !strings.EqualFold(info.UUID, rec.UUID) {
		_ = s.umount(ctx, rec.Mount)
		return op("The pool did not mount by its UUID.")
	}
	return nil
}

func (s *Service) blkidUUID(ctx context.Context, dev string) (string, error) {
	out, err := s.run.Run(ctx, 5*time.Second, "blkid", "-p", "-o", "value", "-s", "UUID", dev)
	if err != nil {
		return "", err
	}
	return firstLine(out), nil
}

func findMember(disks []Disk, m Member) (Disk, bool) {
	for _, d := range disks {
		if sameDisk(d, m) {
			return d, true
		}
	}
	return Disk{}, false
}

// AddDisk erases one new disk and adds it to a pool. A btrfs raid pool then
// starts a balance so the new disk is actually used.
func (s *Service) AddDisk(ctx context.Context, in AddDisk) (AddResult, error) {
	s.opMu.Lock()
	rec, disk, err := s.add(ctx, in)
	s.opMu.Unlock()
	if err != nil {
		s.note(ctx, "storage.pool.add", in.Pool, "error")
		return AddResult{}, err
	}
	s.note(ctx, "storage.pool.add", rec.Name, "ok")
	res := AddResult{Pool: s.decorate(ctx, rec, s.snapshot().CachePool, nil)}
	if rec.FS == FSBtrfs && (rec.Layout == LayoutRAID1 || rec.Layout == LayoutRAID10) {
		job, err := s.RunJob(ctx, rec.Name, KindBalance)
		if err != nil {
			res.Pool.HealthDetail = "The disk was added, but the balance did not start: " + err.Error()
		} else {
			res.Job = &job
		}
	}
	_ = disk
	return res, nil
}

func (s *Service) add(ctx context.Context, in AddDisk) (poolRec, Disk, error) {
	st := s.snapshot()
	rec, ok := st.pool(strings.TrimSpace(in.Pool))
	if !ok {
		return poolRec{}, Disk{}, input("That pool does not exist.")
	}
	disks, err := s.probe(ctx)
	if err != nil {
		return poolRec{}, Disk{}, err
	}
	annotated := s.annotate(ctx, disks, st)
	d, err := resolveDisk(annotated, in.Disk)
	if err != nil {
		return poolRec{}, Disk{}, err
	}
	if !confirmMatch(in.Confirm, d.Confirm) {
		return poolRec{}, Disk{}, inputf("The confirmation for %s does not match its %s. The disk was not added.", diskLabel(d), confirmLabel(d))
	}
	if !d.Eligible {
		return poolRec{}, Disk{}, input(d.Blocked)
	}
	if err := s.requireDevice(d); err != nil {
		return poolRec{}, Disk{}, err
	}
	if err := s.haveTool(ctx, rec.FS); err != nil {
		return poolRec{}, Disk{}, err
	}
	info := s.mountInfo(ctx, rec.Mount)
	if rec.FS != FSZFS && !info.Mounted {
		return poolRec{}, Disk{}, input("Mount the pool before adding a disk.")
	}
	existing := ""
	if len(rec.Members) > 0 {
		if cur, ok := findMember(disks, rec.Members[0]); ok {
			existing = cur.Path
		} else {
			existing = rec.Members[0].Path
		}
	}
	bin, args, err := AddArgs(rec.FS, rec.Layout, rec.Name, rec.Mount, existing, d.Path)
	if err != nil {
		return poolRec{}, Disk{}, err
	}
	if _, err := s.cmd(ctx, 3*time.Minute, bin, args...); err != nil {
		return poolRec{}, Disk{}, err
	}
	rec.Members = append(rec.Members, memberFrom(d))
	if err := s.update(func(f *file) error {
		for i := range f.Pools {
			if f.Pools[i].Name == rec.Name {
				f.Pools[i].Members = append([]Member(nil), rec.Members...)
				return nil
			}
		}
		return input("That pool does not exist.")
	}); err != nil {
		return poolRec{}, Disk{}, err
	}
	return rec, d, nil
}

// ReplaceDisk starts a job that copies one member onto a new disk.
// The new disk's serial has to be typed. The copy runs on the job runner.
func (s *Service) ReplaceDisk(ctx context.Context, in ReplaceDisk) (install.Job, error) {
	s.opMu.Lock()
	rec, oldPath, newDisk, err := s.prepReplace(ctx, in)
	s.opMu.Unlock()
	if err != nil {
		s.note(ctx, "storage.pool.replace", in.Pool, "error")
		return install.Job{}, err
	}
	jobs := s.jobber()
	if jobs == nil {
		s.note(ctx, "storage.pool.replace", rec.Name, "error")
		return install.Job{}, input("The job runner is not available, so the disk was not replaced.")
	}
	name := jobName("replace", rec.Name)
	if existing, ok := jobs.Latest(name); ok && (existing.State == install.Queued || existing.State == install.Running) {
		return existing, input("A disk replace is already running for that pool.")
	}
	oldMember := in.Old
	jobs.Register(name, install.Spec{
		Label: "Replace a disk in " + rec.Name,
		Run: func(ctx context.Context, rep install.Reporter) error {
			s.opMu.Lock()
			defer s.opMu.Unlock()
			err := s.replace(ctx, rep, rec, oldPath, oldMember, newDisk)
			if err != nil {
				s.note(ctx, "storage.pool.replace", rec.Name, "error")
				return err
			}
			s.note(ctx, "storage.pool.replace", rec.Name, "ok")
			return nil
		},
	})
	job, err := jobs.Start(name)
	if err != nil {
		s.note(ctx, "storage.pool.replace", rec.Name, "error")
		return install.Job{}, err
	}
	return job, nil
}

func (s *Service) prepReplace(ctx context.Context, in ReplaceDisk) (poolRec, string, Disk, error) {
	st := s.snapshot()
	rec, ok := st.pool(strings.TrimSpace(in.Pool))
	if !ok {
		return poolRec{}, "", Disk{}, input("That pool does not exist.")
	}
	if rec.FS == FSXFS {
		return poolRec{}, "", Disk{}, input("An XFS disk cannot be replaced in place. Create a new pool and copy the files across.")
	}
	disks, err := s.probe(ctx)
	if err != nil {
		return poolRec{}, "", Disk{}, err
	}
	annotated := s.annotate(ctx, disks, st)
	var old Member
	found := false
	for _, m := range rec.Members {
		if m.ID == in.Old || m.Path == in.Old || m.Serial == in.Old || m.ByID == in.Old {
			old = m
			found = true
			break
		}
	}
	if !found {
		return poolRec{}, "", Disk{}, input("That disk is not in the pool.")
	}
	d, err := resolveDisk(annotated, in.Disk)
	if err != nil {
		return poolRec{}, "", Disk{}, err
	}
	if !confirmMatch(in.Confirm, d.Confirm) {
		return poolRec{}, "", Disk{}, inputf("The confirmation for %s does not match its %s. Nothing was replaced.", diskLabel(d), confirmLabel(d))
	}
	if !d.Eligible {
		return poolRec{}, "", Disk{}, input(d.Blocked)
	}
	if err := s.requireDevice(d); err != nil {
		return poolRec{}, "", Disk{}, err
	}
	oldPath := old.Path
	if cur, ok := findMember(disks, old); ok {
		oldPath = cur.Path
	}
	return rec, oldPath, d, nil
}

func (s *Service) replace(ctx context.Context, rep install.Reporter, rec poolRec, oldPath, oldID string, newDisk Disk) error {
	if err := s.haveTool(ctx, rec.FS); err != nil {
		return err
	}
	rep.Step("Replacing a disk in " + rec.Name)
	bin, args, err := ReplaceArgs(rec.FS, rec.Name, rec.Mount, oldPath, newDisk.Path)
	if err != nil {
		return err
	}
	timeout := 24 * time.Hour
	if _, err := s.cmd(ctx, timeout, bin, args...); err != nil {
		return err
	}
	if rec.FS == FSZFS {
		if err := s.waitZFS(ctx, rep, rec.Name); err != nil {
			return err
		}
	}
	return s.update(func(f *file) error {
		for i := range f.Pools {
			if f.Pools[i].Name != rec.Name {
				continue
			}
			next := make([]Member, 0, len(f.Pools[i].Members))
			for _, m := range f.Pools[i].Members {
				if m.ID == oldID || m.Path == oldPath || m.Path == oldID {
					continue
				}
				next = append(next, m)
			}
			next = append(next, memberFrom(newDisk))
			f.Pools[i].Members = next
			return nil
		}
		return input("That pool does not exist.")
	})
}

// Restore mounts saved pools and applies spin-down. It never formats a disk.
func (s *Service) Restore(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	st := s.snapshot()
	var first error
	for _, rec := range st.Pools {
		info := s.mountInfo(ctx, rec.Mount)
		if info.Mounted && (rec.FS == FSZFS || strings.EqualFold(info.UUID, rec.UUID)) {
			continue
		}
		if err := s.mountRecorded(ctx, rec); err != nil {
			s.log.Warn("pool was not mounted", "pool", rec.Name, "err", err)
			if first == nil {
				first = err
			}
		}
	}
	s.applySpin(ctx, st)
	return first
}
