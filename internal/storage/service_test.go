package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

type fakeRun struct {
	mu    sync.Mutex
	look  map[string]bool
	calls [][]string
	fn    func(name string, args []string) (string, error)
}

func (f *fakeRun) Look(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.look[name]
}

func (f *fakeRun) Run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return "", nil
	}
	return fn(name, args)
}

func (f *fakeRun) saw(part string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), part) {
			return true
		}
	}
	return false
}

type auditRec struct {
	action, target, result string
}

func newSvc(t *testing.T, disks []Disk, permit bool, allow []string) (*Service, *fakeRun, *[]auditRec) {
	t.Helper()
	run := &fakeRun{look: map[string]bool{
		"mkfs.btrfs": true, "btrfs": true, "mkfs.xfs": true, "xfs_scrub": true,
		"fstrim": true, "hdparm": true, "smartctl": true,
	}}
	dir := t.TempDir()
	aud := []auditRec{}
	s, err := Open(Deps{
		DataDir: dir, MountRoot: filepath.Join(dir, "mnt"),
		SharesRoot:      func() string { return filepath.Join(dir, "shares") },
		PermitHostDisks: permit, Allow: allow, ApplySystem: false,
		Probe: func(context.Context) ([]Disk, error) { return append([]Disk(nil), disks...), nil },
		Run:   run,
		SetAppdata: func(p string) error {
			return os.WriteFile(filepath.Join(dir, "appdata-path"), []byte(p), 0o600)
		},
		ShareNames: func() []string { return []string{"media", "appdata"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetAudit(func(_ context.Context, action, target, result string) {
		aud = append(aud, auditRec{action, target, result})
	})
	return s, run, &aud
}

func diskFix(path, serial string, osDisk bool) Disk {
	d := Disk{
		Name: filepath.Base(path), Path: path, Model: "TestDisk", Serial: serial,
		SizeBytes: 100 << 30, Type: "hdd", Transport: "sata", Rotational: true,
		SMART: SMART{Available: true, Healthy: "passed"},
	}
	applyIdentity(&d)
	d.OS = osDisk
	return d
}

func TestCreateRefusesWithoutExactSerial(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, aud := newSvc(t, []Disk{d}, true, nil)
	ctx := context.Background()
	_, err := s.CreatePool(ctx, CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{d.ID}, Confirm: []string{"SERIAL2"},
	})
	if err == nil {
		t.Fatal("created a pool with the wrong serial")
	}
	if run.saw("mkfs") || run.saw("zpool") {
		t.Fatal("a format command ran")
	}
	if len(*aud) != 1 || (*aud)[0].result != "error" || (*aud)[0].action != "storage.pool.create" {
		t.Fatalf("audit: %+v", *aud)
	}
	_, err = s.CreatePool(ctx, CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{d.ID}, Confirm: []string{},
	})
	if err == nil || run.saw("mkfs") {
		t.Fatal("empty confirmation formatted a disk")
	}
}

func TestCreateRefusesOSAndForeignDisks(t *testing.T) {
	osDisk := diskFix("/dev/vdc", "ROOTDISK", true)
	other := diskFix("/dev/vda", "HOSTDISK", false)
	loop := diskFix("/dev/loop5", "LOOP5", false)
	ctx := context.Background()

	s, run, _ := newSvc(t, []Disk{osDisk}, true, nil)
	_, err := s.CreatePool(ctx, CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{osDisk.ID}, Confirm: []string{osDisk.Confirm},
	})
	if err == nil || run.saw("mkfs") {
		t.Fatal("formatted the operating system disk")
	}

	s, run, _ = newSvc(t, []Disk{other, loop}, false, nil)
	_, err = s.CreatePool(ctx, CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{other.ID}, Confirm: []string{other.Confirm},
	})
	if err == nil || run.saw("mkfs") {
		t.Fatal("formatted a host disk without permission")
	}

	s, run, _ = newSvc(t, []Disk{other, loop}, false, []string{"/dev/loop5"})
	_, err = s.CreatePool(ctx, CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{other.ID}, Confirm: []string{other.Confirm},
	})
	if err == nil || run.saw("mkfs") || run.saw("/dev/vda") {
		t.Fatal("allow list did not stop /dev/vda")
	}
}

func TestCreateMountsByUUIDAndAudits(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, aud := newSvc(t, []Disk{d}, true, nil)
	uuid := "11111111-1111-1111-1111-111111111111"
	mount := filepath.Join(s.mountRoot, "data")
	run.fn = func(name string, args []string) (string, error) {
		switch name {
		case "blkid":
			return uuid, nil
		case "findmnt":
			return `UUID="` + uuid + `" FSTYPE="btrfs" SOURCE="/dev/loop9" TARGET="` + mount + `"`, nil
		case "btrfs":
			return "[/dev/loop9].write_io_errs 0\n[/dev/loop9].read_io_errs 0\n", nil
		default:
			return "", nil
		}
	}
	pool, err := s.CreatePool(context.Background(), CreatePool{
		Name: "data", FS: "btrfs", Layout: "single",
		Disks: []string{d.ID}, Confirm: []string{"SERIAL1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pool.UUID != uuid || !pool.Mounted || pool.Health != "healthy" {
		t.Fatalf("%+v", pool)
	}
	if !run.saw("mkfs.btrfs") || !run.saw("-U "+uuid) || !run.saw("-m dup") {
		t.Fatalf("commands: %v", run.calls)
	}
	ok := false
	for _, e := range *aud {
		if e.action == "storage.pool.create" && e.result == "ok" && e.target == "data" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("audit: %+v", *aud)
	}
	view, err := s.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pools) != 1 || view.Disks[0].UsedFor != "Pool data" {
		t.Fatalf("view: %+v disks %+v", view.Pools, view.Disks)
	}
}

func TestRestoreNeverFormats(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, _ := newSvc(t, []Disk{d}, true, nil)
	uuid := "11111111-1111-1111-1111-111111111111"
	mount := filepath.Join(s.mountRoot, "data")
	if err := s.update(func(f *file) error {
		f.Pools = append(f.Pools, poolRec{
			Name: "data", FS: FSBtrfs, Layout: LayoutSingle, UUID: uuid, Mount: mount,
			Members: []Member{memberFrom(d)},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n := 0
	run.fn = func(name string, _ []string) (string, error) {
		switch name {
		case "findmnt":
			n++
			if n == 1 {
				return "", errors.New("not a mountpoint")
			}
			return `UUID="` + uuid + `" FSTYPE="btrfs" SOURCE="/dev/loop9" TARGET="` + mount + `"`, nil
		case "blkid":
			return uuid, nil
		default:
			return "", nil
		}
	}
	if err := s.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if run.saw("mkfs") || run.saw("wipefs") || run.saw("zpool create") {
		t.Fatalf("restore formatted a disk: %v", run.calls)
	}
	if !run.saw("mount") || !run.saw("-U "+uuid) {
		t.Fatalf("did not mount by uuid: %v", run.calls)
	}
}

func TestShareCacheAndSpin(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, aud := newSvc(t, []Disk{d}, true, nil)
	mount := filepath.Join(s.mountRoot, "data")
	uuid := "11111111-1111-1111-1111-111111111111"
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.update(func(f *file) error {
		f.Pools = []poolRec{{
			Name: "data", FS: FSBtrfs, Layout: LayoutSingle, UUID: uuid, Mount: mount,
			Members: []Member{memberFrom(d)},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run.fn = func(name string, _ []string) (string, error) {
		if name == "findmnt" {
			return `UUID="` + uuid + `" FSTYPE="btrfs" SOURCE="/dev/loop9" TARGET="` + mount + `"`, nil
		}
		return "", nil
	}
	ctx := context.Background()
	root := filepath.Join(filepath.Dir(s.mountRoot), "shares")
	if err := os.MkdirAll(filepath.Join(root, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "media", "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindShare(ctx, "media", "data"); err == nil {
		t.Fatal("moved an existing share")
	}
	if _, err := os.Stat(filepath.Join(root, "media", "file")); err != nil {
		t.Fatal("existing file disappeared")
	}
	b, err := s.BindShare(ctx, "appdata", "data")
	if err != nil {
		t.Fatal(err)
	}
	if b.Path != filepath.Join(mount, "appdata") {
		t.Fatal(b.Path)
	}
	link, err := os.Readlink(filepath.Join(root, "appdata"))
	if err != nil || link != b.Path {
		t.Fatalf("link %s %v", link, err)
	}
	got, ok := s.SharePath("appdata")
	if !ok || got != b.Path {
		t.Fatalf("share path %s %v", got, ok)
	}
	cache, err := s.SetCache(ctx, "data")
	if err != nil || cache.Path != filepath.Join(mount, "appdata") {
		t.Fatal(err, cache)
	}
	if s.CachePath() != cache.Path {
		t.Fatal(s.CachePath())
	}
	if _, err := s.SetCache(ctx, ""); err != nil || s.CachePath() != "" {
		t.Fatal(err)
	}
	virt := Disk{Name: "vda", Path: "/dev/vda", Model: "Virtual disk", SizeBytes: 100 << 30, Type: "virtual", Transport: "virtio", ByID: "/dev/disk/by-id/virtio-1"}
	applyIdentity(&virt)
	s.probe = func(context.Context) ([]Disk, error) { return []Disk{virt, d}, nil }
	if err := s.SetSpinDown(ctx, virt.ID, 30); err == nil {
		t.Fatal("spun down a virtual disk")
	}
	if err := s.SetSpinDown(ctx, d.ID, 30); err != nil {
		t.Fatal(err)
	}
	if !run.saw("hdparm -S 241 " + d.Path) {
		t.Fatalf("hdparm: %v", run.calls)
	}
	if err := s.SetSpinDown(ctx, d.ID, 7); err == nil {
		t.Fatal("accepted 7 minutes")
	}
	seen := map[string]bool{}
	for _, e := range *aud {
		if e.result == "ok" {
			seen[e.action] = true
		}
	}
	for _, want := range []string{"storage.share.bind", "storage.cache.set", "storage.cache.clear", "storage.spindown"} {
		if !seen[want] {
			t.Fatalf("missing %s in %+v", want, *aud)
		}
	}
}

func TestScheduleUsesJobRunner(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, _ := newSvc(t, []Disk{d}, true, nil)
	mount := filepath.Join(s.mountRoot, "data")
	uuid := "11111111-1111-1111-1111-111111111111"
	if err := s.update(func(f *file) error {
		f.Pools = []poolRec{{
			Name: "data", FS: FSBtrfs, Layout: LayoutRAID1, UUID: uuid, Mount: mount,
			Members: []Member{memberFrom(d)},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run.fn = func(name string, args []string) (string, error) {
		if name == "findmnt" {
			return `UUID="` + uuid + `" FSTYPE="btrfs" SOURCE="/dev/loop9" TARGET="` + mount + `"`, nil
		}
		if name == "btrfs" && len(args) > 0 && args[0] == "scrub" {
			return "scrub done", nil
		}
		return "", nil
	}
	jobs := install.New(install.Deps{Now: func() time.Time { return time.Now() }})
	s.AttachJobs(jobs)
	ctx := context.Background()
	if _, err := s.RunJob(ctx, "data", KindBalance); err != nil {
		t.Fatal(err)
	}
	waitJob(t, jobs, jobName(KindBalance, "data"))
	if _, err := s.RunJob(ctx, "data", KindScrub); err != nil {
		t.Fatal(err)
	}
	waitJob(t, jobs, jobName(KindScrub, "data"))
	if !run.saw("btrfs scrub start -B " + mount) {
		t.Fatalf("scrub command: %v", run.calls)
	}
	if _, err := s.SetSchedule(ctx, Schedule{Pool: "data", Kind: KindTrim, Enabled: true, EveryHours: 12}); err != nil {
		t.Fatal(err)
	}
	// Not due yet: the next run is 12 hours out, so a tick must not start trim.
	before := len(run.calls)
	s.tick(ctx)
	if run.saw("fstrim") && len(run.calls) > before {
		// fstrim may only appear if the schedule was due. It is not.
	}
	for _, c := range run.calls[before:] {
		if c[0] == "fstrim" {
			t.Fatal("trim started before it was due")
		}
	}
	if err := s.update(func(f *file) error {
		f.Schedules[0].NextRun = s.now().Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx)
	waitJob(t, jobs, jobName(KindTrim, "data"))
	if !run.saw("fstrim -v " + mount) {
		t.Fatalf("trim: %v", run.calls)
	}
	if run.saw("mkfs") {
		t.Fatal("scheduler formatted a disk")
	}
}

func waitJob(t *testing.T, jobs *install.Runner, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := jobs.Latest(name)
		if ok && (job.State == install.Succeeded || job.State == install.Failed) {
			if job.State == install.Failed {
				t.Fatalf("%s failed: %s", name, job.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", name)
}

func TestXFSAndMissingZFS(t *testing.T) {
	a := diskFix("/dev/loop1", "A", false)
	b := diskFix("/dev/loop2", "B", false)
	s, run, _ := newSvc(t, []Disk{a, b}, true, nil)
	_, err := s.CreatePool(context.Background(), CreatePool{
		Name: "solo", FS: "xfs", Layout: "single",
		Disks: []string{a.ID, b.ID}, Confirm: []string{a.Confirm, b.Confirm},
	})
	if err == nil || run.saw("mkfs") {
		t.Fatal("xfs formatted two disks")
	}
	_, err = s.CreatePool(context.Background(), CreatePool{
		Name: "tank", FS: "zfs", Layout: "mirror",
		Disks: []string{a.ID, b.ID}, Confirm: []string{a.Confirm, b.Confirm},
	})
	if err == nil || run.saw("zpool") || run.saw("mkfs") {
		t.Fatal("zfs ran without the tool")
	}
}

func TestDestroyRequiresNameAndDoesNotWipe(t *testing.T) {
	d := diskFix("/dev/loop9", "SERIAL1", false)
	s, run, _ := newSvc(t, []Disk{d}, true, nil)
	mount := filepath.Join(s.mountRoot, "data")
	if err := s.update(func(f *file) error {
		f.Pools = []poolRec{{Name: "data", FS: FSBtrfs, Layout: LayoutSingle, UUID: "11111111-1111-1111-1111-111111111111", Mount: mount, Members: []Member{memberFrom(d)}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run.fn = func(name string, _ []string) (string, error) {
		if name == "findmnt" {
			return "", errors.New("not mounted")
		}
		return "", nil
	}
	if err := s.DestroyPool(context.Background(), "data", "nope"); err == nil {
		t.Fatal("destroyed without the pool name")
	}
	if err := s.DestroyPool(context.Background(), "data", "data"); err != nil {
		t.Fatal(err)
	}
	if run.saw("mkfs") || run.saw("wipefs") {
		t.Fatal("destroy wiped a disk")
	}
	if _, ok := s.snapshot().pool("data"); ok {
		t.Fatal("pool still stored")
	}
}

func TestDiskHealthForNotifications(t *testing.T) {
	d := diskFix("/dev/sda", "ABC", false)
	n := int64(3)
	d.SMART.Reallocated = &n
	d.TempC = intptr(41)
	s, _, _ := newSvc(t, []Disk{d}, true, nil)
	h, err := s.DiskHealth(context.Background())
	if err != nil || len(h) != 1 || h[0].Serial != "ABC" || h[0].SMARTHealthy != "passed" || h[0].Reallocated == nil || *h[0].Reallocated != 3 {
		t.Fatalf("%+v %v", h, err)
	}
}

func intptr(n int) *int { return &n }
