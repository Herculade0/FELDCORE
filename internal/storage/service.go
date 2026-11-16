package storage

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/Herculade0/feldcore/internal/install"
)

// AuditFunc records one storage change. The HTTP layer writes the audit log
// and can read the caller from ctx. A nil func skips the log.
type AuditFunc func(ctx context.Context, action, target, result string)

// Jobs is the install job runner (package install, issue 11). Scrub, balance,
// trim and disk replace are registered on it and started by name.
type Jobs interface {
	Register(name string, spec install.Spec)
	Start(name string) (install.Job, error)
	Latest(name string) (install.Job, bool)
	Current() []install.Job
}

// Deps configures the storage service. PermitHostDisks is set by the daemon.
// Tests leave it false and pass Allow, the only devices they may format.
type Deps struct {
	DataDir         string
	MountRoot       string
	SharesRoot      func() string
	ByIDDir         string
	PermitHostDisks bool
	ApplySystem     bool
	Allow           []string
	Log             *slog.Logger
	SetAppdata      func(string) error
	ShareNames      func() []string
	// Probe and Run are optional replacements, used by tests.
	Probe func(context.Context) ([]Disk, error)
	Run   Executor
	Now   func() time.Time
}

// Service is FELDCORE's disk and pool control. The HTTP handlers and the
// first-run wizard both call it. Nothing in here formats a disk unless a
// method that says so is called, and that method refuses until every disk
// serial (or by-id name) was typed.
type Service struct {
	mu   sync.Mutex
	opMu sync.Mutex
	st   file

	dataDir     string
	statePath   string
	mountRoot   string
	sharesRoot  func() string
	byID        string
	permit      bool
	allow       map[string]bool
	applySystem bool
	log         *slog.Logger
	audit       AuditFunc
	setAppdata  func(string) error
	shareNames  func() []string
	probe       func(context.Context) ([]Disk, error)
	run         Executor
	now         func() time.Time
	jobs        Jobs
	// fstab is where a wizard ext4 disk is recorded. Empty means /etc/fstab,
	// and it is written only when the daemon is allowed to change the system.
	fstab string
}

// Open loads storage.json. It does not format, mount or spin down anything;
// call Restore for that.
func Open(d Deps) (*Service, error) {
	if strings.TrimSpace(d.DataDir) == "" {
		return nil, op("Storage needs a data directory.")
	}
	if d.MountRoot == "" {
		d.MountRoot = "/mnt/feldcore"
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.ByIDDir == "" {
		d.ByIDDir = "/dev/disk/by-id"
	}
	path := filepath.Join(d.DataDir, "storage.json")
	st, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	s := &Service{
		st: st, dataDir: d.DataDir, statePath: path,
		mountRoot: filepath.Clean(d.MountRoot), sharesRoot: d.SharesRoot,
		byID: d.ByIDDir, permit: d.PermitHostDisks, applySystem: d.ApplySystem,
		log: d.Log, setAppdata: d.SetAppdata, shareNames: d.ShareNames,
		probe: d.Probe, run: d.Run, now: d.Now,
	}
	if d.Allow != nil {
		s.allow = map[string]bool{}
		for _, p := range d.Allow {
			s.allow[p] = true
			if r, err := filepath.EvalSymlinks(p); err == nil && r != "" {
				s.allow[r] = true
			}
		}
	}
	if s.run == nil {
		s.run = newHost(s.permit, s.allow)
	}
	if s.probe == nil {
		run := s.run
		dir := s.byID
		s.probe = func(ctx context.Context) ([]Disk, error) {
			return probeHost(ctx, run, dir)
		}
	}
	return s, nil
}

// SetAudit installs the audit callback. Safe to call before requests start.
func (s *Service) SetAudit(fn AuditFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.audit = fn
	s.mu.Unlock()
}

// AttachJobs uses the daemon's install runner for scrub, balance, trim and replace.
func (s *Service) AttachJobs(j Jobs) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.jobs = j
	s.mu.Unlock()
}

func (s *Service) note(ctx context.Context, action, target, result string) {
	s.mu.Lock()
	fn := s.audit
	s.mu.Unlock()
	if fn != nil {
		fn(ctx, action, clip(target, 200), result)
	}
	if s.log != nil {
		s.log.Info("storage", "action", action, "target", target, "result", result)
	}
}

func (s *Service) snapshot() file {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.clone()
}

func (s *Service) update(fn func(*file) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.st.clone()
	if err := fn(&next); err != nil {
		return err
	}
	if err := saveFile(s.statePath, next); err != nil {
		if s.log != nil {
			s.log.Error("storage state", "err", err)
		}
		return op("Could not save storage state.")
	}
	s.st = next
	return nil
}

func (s *Service) jobber() Jobs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs
}

// Inventory is the storage page: every whole disk, every pool, and the
// schedules and share placement stored for them.
func (s *Service) Inventory(ctx context.Context) (View, error) {
	disks, err := s.probe(ctx)
	if err != nil {
		return View{}, err
	}
	st := s.snapshot()
	caps := s.caps(ctx)
	annotated := s.annotate(ctx, disks, st)
	pools := make([]Pool, 0, len(st.Pools))
	for _, rec := range st.Pools {
		pools = append(pools, s.decorate(ctx, rec, st.CachePool, annotated))
	}
	view := View{
		Privileged: os.Geteuid() == 0,
		Caps:       caps,
		Disks:      annotated,
		Pools:      pools,
		Schedules:  append([]Schedule(nil), st.Schedules...),
		SpinDown:   append([]SpinRule(nil), st.SpinDown...),
		Shares:     s.shareRows(st),
		CachePool:  st.CachePool,
		CachePath:  cachePath(st),
		MountRoot:  s.mountRoot,
		Jobs:       s.storageJobs(),
	}
	if view.Disks == nil {
		view.Disks = []Disk{}
	}
	if view.Schedules == nil {
		view.Schedules = []Schedule{}
	}
	if view.SpinDown == nil {
		view.SpinDown = []SpinRule{}
	}
	if view.Shares == nil {
		view.Shares = []ShareRow{}
	}
	if view.Jobs == nil {
		view.Jobs = []install.Job{}
	}
	return view, nil
}

// DiskHealth is the disk list notifications should read. It does not send alerts.
func (s *Service) DiskHealth(ctx context.Context) ([]DiskHealth, error) {
	view, err := s.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DiskHealth, 0, len(view.Disks))
	for _, d := range view.Disks {
		out = append(out, DiskHealth{
			ID: d.ID, Model: d.Model, Serial: d.Serial, Path: d.Path, ByID: d.ByID,
			TempC: d.TempC, SMARTHealthy: d.SMART.Healthy, Reallocated: d.SMART.Reallocated,
			Pending: d.SMART.Pending, MediaErrors: d.SMART.MediaErrors,
			UsedFor: d.UsedFor, Message: d.SMART.Message,
		})
	}
	return out, nil
}

func (s *Service) caps(ctx context.Context) Caps {
	btrfs := s.run.Look("mkfs.btrfs") && s.run.Look("btrfs")
	xfs := s.run.Look("mkfs.xfs")
	zfs := false
	if s.run.Look("zpool") {
		_, err := s.run.Run(ctx, 4*time.Second, "zpool", "list", "-H")
		zfs = err == nil
	}
	trim := s.run.Look("fstrim") || zfs
	return Caps{
		Btrfs: btrfs, XFS: xfs, XFSScrub: s.run.Look("xfs_scrub"), ZFS: zfs,
		SMART: s.run.Look("smartctl"), Hdparm: s.run.Look("hdparm"), Trim: trim,
		Layouts: layoutCatalog(btrfs, xfs, zfs),
	}
}

func (s *Service) haveTool(ctx context.Context, fs string) error {
	c := s.caps(ctx)
	switch fs {
	case FSBtrfs:
		if !c.Btrfs {
			return toolMissing(fs)
		}
	case FSXFS:
		if !c.XFS {
			return toolMissing(fs)
		}
	case FSZFS:
		if !c.ZFS {
			return toolMissing(fs)
		}
	default:
		return toolMissing(fs)
	}
	return nil
}

func (s *Service) annotate(ctx context.Context, disks []Disk, st file) []Disk {
	out := make([]Disk, len(disks))
	copy(out, disks)
	for i := range out {
		d := &out[i]
		if rec, ok := poolOf(st, *d); ok {
			d.Pool = rec.Name
		}
		d.UsedFor = usedFor(*d, st.CachePool)
		switch {
		case d.OS:
			d.Eligible = false
			d.Blocked = "This disk holds the operating system."
		case d.Mountpoint != "" || len(d.ChildMounts) > 0:
			d.Eligible = false
			where := d.Mountpoint
			if where == "" && len(d.ChildMounts) > 0 {
				where = d.ChildMounts[0]
			}
			d.Blocked = "This disk is in use at " + where + "."
		case d.Pool != "":
			d.Eligible = false
			d.Blocked = "This disk is already in the " + d.Pool + " pool."
		case d.Confirm == "":
			d.Eligible = false
			d.Blocked = "This disk has no serial and no by-id path, so FELDCORE will not format it."
		case d.SizeBytes < minDiskBytes:
			d.Eligible = false
			d.Blocked = "This disk is too small to become a pool."
		case s.allow != nil && !s.allowed(d.Path):
			d.Eligible = false
			d.Blocked = "This disk is not in the set of disks this operation may change."
		case s.allow == nil && !s.permit:
			d.Eligible = false
			d.Blocked = "Formatting host disks is turned off for this process."
		default:
			d.Eligible = true
		}
		if d.Partitions > 0 && !d.OS {
			d.Warnings = append(d.Warnings, "Formatting erases the partitions on this disk.")
		}
		if d.FSType != "" && d.Pool == "" && !d.OS {
			d.Warnings = append(d.Warnings, "Formatting replaces the "+d.FSType+" filesystem on this disk.")
		}
		if d.ConfirmKind == "by-id" {
			d.Warnings = append(d.Warnings, "This disk has no hardware serial. Type its by-id name to confirm.")
		}
	}
	_ = ctx
	return out
}

func poolOf(st file, d Disk) (poolRec, bool) {
	for _, p := range st.Pools {
		for _, m := range p.Members {
			if sameDisk(d, m) {
				return p, true
			}
		}
	}
	return poolRec{}, false
}

func sameDisk(d Disk, m Member) bool {
	if m.ID != "" && d.ID == m.ID {
		return true
	}
	if m.Serial != "" && d.Serial != "" && m.Serial == d.Serial {
		return true
	}
	if m.ByID != "" && d.ByID != "" && m.ByID == d.ByID {
		return true
	}
	return m.Path != "" && d.Path == m.Path
}

func usedFor(d Disk, cache string) string {
	switch {
	case d.OS:
		return "Operating system"
	case d.Pool != "":
		if d.Pool == cache {
			return "Cache pool " + d.Pool
		}
		return "Pool " + d.Pool
	case d.Mountpoint != "":
		return "Mounted at " + d.Mountpoint
	case len(d.ChildMounts) > 0:
		return "In use at " + d.ChildMounts[0]
	case d.FSType != "":
		return d.FSType + " filesystem, not in a pool"
	default:
		return "Unassigned"
	}
}

func (s *Service) allowed(path string) bool {
	if s.allow == nil {
		return s.permit
	}
	if s.allow[path] {
		return true
	}
	if r, err := filepath.EvalSymlinks(path); err == nil && s.allow[r] {
		return true
	}
	return false
}

func (s *Service) requireDevice(d Disk) error {
	if d.OS {
		return input(d.Path + " holds the operating system. FELDCORE will not change it.")
	}
	if !s.allowed(d.Path) {
		if s.allow == nil && !s.permit {
			return input("Refusing to change a disk. This process is not allowed to format host disks.")
		}
		return inputf("Refusing to change %s. It is not one of the disks this operation is allowed to touch.", d.Path)
	}
	return nil
}

func resolveDisk(disks []Disk, id string) (Disk, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Disk{}, input("Choose a disk.")
	}
	for _, d := range disks {
		if d.ID == id || d.Path == id || d.ByID == id || (d.Serial != "" && d.Serial == id) || (d.Confirm != "" && d.Confirm == id) {
			return d, nil
		}
	}
	return Disk{}, input("That disk is not on this server.")
}

func (s *Service) mountPath(name string) (string, error) {
	if !poolNameRe.MatchString(name) {
		return "", input("Name the pool with a lower-case letter, then letters, numbers or hyphens (up to 32 characters).")
	}
	root := filepath.Clean(s.mountRoot)
	p := filepath.Join(root, name)
	if !strings.HasPrefix(p, root+string(os.PathSeparator)) {
		return "", input("That pool name is not valid.")
	}
	return p, nil
}

func (s *Service) decorate(ctx context.Context, rec poolRec, cache string, disks []Disk) Pool {
	p := Pool{
		Name: rec.Name, FS: rec.FS, Layout: rec.Layout, UUID: rec.UUID,
		Mount: rec.Mount, Members: append([]Member(nil), rec.Members...),
		Cache: rec.Name == cache && cache != "", CreatedAt: rec.CreatedAt,
		BootMount: rec.BootMount,
	}
	info := s.mountInfo(ctx, rec.Mount)
	p.Mounted = info.Mounted && (rec.FS == FSZFS || strings.EqualFold(info.UUID, rec.UUID))
	if p.Mounted {
		if u, err := disk.Usage(rec.Mount); err == nil && u.Total > 0 {
			p.TotalBytes = u.Total
			p.UsedBytes = u.Used
			p.UsedPercent = u.UsedPercent
		}
	}
	// Prefer the path the disk has now, so the page shows a live node.
	for i, m := range p.Members {
		for _, d := range disks {
			if sameDisk(d, m) {
				p.Members[i].Path = d.Path
				break
			}
		}
	}
	p.Health, p.HealthDetail = s.health(ctx, rec, p.Mounted)
	return p
}

type mountInfo struct {
	Mounted bool
	UUID    string
	FSType  string
	Source  string
	Target  string
}

func (s *Service) mountInfo(ctx context.Context, path string) mountInfo {
	if path == "" {
		return mountInfo{}
	}
	out, err := s.run.Run(ctx, 5*time.Second, "findmnt", "-n", "-P", "-M", path, "-o", "UUID,FSTYPE,SOURCE,TARGET")
	if err != nil || strings.TrimSpace(out) == "" {
		return mountInfo{}
	}
	pairs := parsePairs(out)
	return mountInfo{
		Mounted: true,
		UUID:    pairs["UUID"],
		FSType:  pairs["FSTYPE"],
		Source:  pairs["SOURCE"],
		Target:  pairs["TARGET"],
	}
}

func parsePairs(s string) map[string]string {
	out := map[string]string{}
	for _, f := range strings.Fields(s) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		out[k] = strings.Trim(v, `"`)
	}
	return out
}

func (s *Service) health(ctx context.Context, rec poolRec, mounted bool) (string, string) {
	if !mounted {
		return "unmounted", "The pool is not mounted."
	}
	switch rec.FS {
	case FSBtrfs:
		out, err := s.run.Run(ctx, 20*time.Second, "btrfs", "device", "stats", rec.Mount)
		if err != nil {
			return "unknown", clip(err.Error(), 200)
		}
		if btrfsStatErrors(out) {
			return "degraded", "btrfs is reporting device errors."
		}
		return "healthy", ""
	case FSZFS:
		out, err := s.run.Run(ctx, 20*time.Second, "zpool", "status", "-P", rec.Name)
		if err != nil {
			return "unknown", clip(err.Error(), 200)
		}
		return zfsHealth(out)
	default:
		return "healthy", ""
	}
}

func btrfsStatErrors(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "_errs") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		n, err := parseUint(fields[len(fields)-1])
		if err == nil && n > 0 {
			return true
		}
	}
	return false
}

func parseUint(s string) (uint64, error) {
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + uint64(r-'0')
	}
	return n, nil
}

func zfsHealth(status string) (string, string) {
	switch {
	case strings.Contains(status, "FAULTED"):
		return "faulted", "ZFS reports the pool as faulted."
	case strings.Contains(status, "DEGRADED"), strings.Contains(status, "UNAVAIL"):
		return "degraded", "ZFS reports the pool as degraded."
	case strings.Contains(status, "ONLINE"):
		return "healthy", ""
	default:
		return "unknown", ""
	}
}

func zfsBusy(status string) bool {
	return strings.Contains(status, "in progress")
}

func (s *Service) storageJobs() []install.Job {
	j := s.jobber()
	if j == nil {
		return []install.Job{}
	}
	var out []install.Job
	for _, job := range j.Current() {
		if strings.HasPrefix(job.Name, "storage-") {
			out = append(out, job)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	return out
}

func cachePath(st file) string {
	if st.CachePool == "" {
		return ""
	}
	p, ok := st.pool(st.CachePool)
	if !ok {
		return ""
	}
	return filepath.Join(p.Mount, "appdata")
}

func memberFrom(d Disk) Member {
	return Member{ID: d.ID, Serial: d.Serial, ByID: d.ByID, Path: d.Path, SizeBytes: d.SizeBytes}
}

func (s *Service) cmd(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	out, err := s.run.Run(ctx, timeout, name, args...)
	if err == nil {
		return out, nil
	}
	var ie *InputError
	var oe *OpError
	if errors.As(err, &ie) || errors.As(err, &oe) {
		return out, err
	}
	detail := strings.TrimSpace(out)
	if detail == "" {
		detail = err.Error()
	}
	return out, opf("%s failed: %s", name, clip(detail, 500))
}
