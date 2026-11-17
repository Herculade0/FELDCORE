package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// TestLoopDevices formats files attached as loop devices. It refuses any
// command that names a real host disk. When the test is not root it re-runs
// itself under sudo -n, because losetup and mount need root.
func TestLoopDevices(t *testing.T) {
	if os.Geteuid() != 0 {
		bin, err := exec.LookPath("sudo")
		if err != nil {
			t.Skip("loop device tests need root or sudo")
		}
		cmd := exec.Command(bin, "-n", "-E", os.Args[0], "-test.run", "^TestLoopDevices$", "-test.v", "-test.count=1", "-test.timeout", "8m")
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err = cmd.Run()
		if buf.Len() > 0 {
			t.Log(buf.String())
		}
		if err != nil {
			t.Fatalf("loop device test: %v", err)
		}
		return
	}
	if !kernelFS("xfs") && !kernelFS("btrfs") {
		t.Skip("this kernel cannot mount xfs or btrfs")
	}
	loopDevices(t)
}

func kernelFS(name string) bool {
	raw, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[len(fields)-1] == name {
			return true
		}
	}
	return false
}

type recordingRun struct {
	inner Executor
	mu    sync.Mutex
	bad   []string
	root  string
}

func (r *recordingRun) Look(name string) bool { return r.inner.Look(name) }

func (r *recordingRun) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	base := filepath.Base(name)
	switch base {
	case "lsblk", "smartctl", "findmnt", "blkid":
	default:
		if strings.Contains(line, "/dev/vd") || strings.Contains(line, "/dev/sd") || strings.Contains(line, "/dev/nvme") || strings.Contains(line, "/dev/mmc") {
			r.mu.Lock()
			r.bad = append(r.bad, line)
			r.mu.Unlock()
		}
		if r.root != "" && strings.Contains(strings.ToLower(line), strings.ToLower(r.root)) {
			r.mu.Lock()
			r.bad = append(r.bad, "root uuid: "+line)
			r.mu.Unlock()
		}
	}
	return r.inner.Run(ctx, timeout, name, args...)
}

func loopDevices(t *testing.T) {
	base := t.TempDir()
	byID := filepath.Join(base, "by-id")
	if err := os.MkdirAll(byID, 0o755); err != nil {
		t.Fatal(err)
	}
	nDisks := 2
	if kernelFS("btrfs") {
		nDisks = 4
	}
	devs := make([]string, 0, nDisks)
	confirms := make([]string, 0, nDisks)
	t.Cleanup(func() {
		_ = exec.Command("umount", filepath.Join(base, "mnt", "data")).Run()
		_ = exec.Command("umount", "-l", filepath.Join(base, "mnt", "data")).Run()
		_ = exec.Command("umount", filepath.Join(base, "mnt", "solo")).Run()
		_ = exec.Command("umount", "-l", filepath.Join(base, "mnt", "solo")).Run()
		for _, d := range devs {
			_ = exec.Command("losetup", "-d", d).Run()
		}
	})
	for i := 0; i < nDisks; i++ {
		img := filepath.Join(base, fmt.Sprintf("disk%d.img", i))
		if out, err := exec.Command("truncate", "-s", "512M", img).CombinedOutput(); err != nil {
			t.Fatalf("truncate: %v %s", err, out)
		}
		out, err := exec.Command("losetup", "--find", "--show", img).CombinedOutput()
		if err != nil {
			t.Fatalf("losetup: %v %s", err, out)
		}
		dev := strings.TrimSpace(string(out))
		confirm := fmt.Sprintf("virtio-LOOPSERIAL%d", i+1)
		devs = append(devs, dev)
		confirms = append(confirms, confirm)
		if err := os.Symlink(dev, filepath.Join(byID, confirm)); err != nil {
			t.Fatal(err)
		}
	}

	rootUUID := strings.TrimSpace(commandOut("findmnt", "-n", "-o", "UUID", "/"))
	host := newHost(false, nil)
	rec := &recordingRun{inner: host, root: rootUUID}
	allow := map[string]bool{}
	for _, d := range devs {
		allow[d] = true
	}
	host.g.allow = allow

	before := map[string]string{}
	for _, real := range []string{"/dev/vda", "/dev/vdb", "/dev/vdc"} {
		before[real] = strings.TrimSpace(commandOut("blkid", "-p", "-o", "value", "-s", "UUID", real))
	}

	s, err := Open(Deps{
		DataDir: filepath.Join(base, "state"), MountRoot: filepath.Join(base, "mnt"),
		SharesRoot: func() string { return filepath.Join(base, "shares") },
		ByIDDir:    byID, Allow: devs, PermitHostDisks: false, ApplySystem: false,
		Run: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	var audits []string
	s.SetAudit(func(_ context.Context, action, target, result string) {
		audits = append(audits, action+" "+target+" "+result)
	})
	jobs := install.New(install.Deps{})
	s.AttachJobs(jobs)
	ctx := context.Background()

	view, err := s.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Disk{}
	for _, d := range view.Disks {
		found[d.Path] = d
	}
	ids := make([]string, len(devs))
	for i, dev := range devs {
		d, ok := found[dev]
		if !ok {
			t.Fatalf("loop %s missing from inventory", dev)
		}
		if d.Confirm != confirms[i] || d.ConfirmKind != "by-id" || !d.Eligible || d.OS {
			t.Fatalf("loop identity: %+v", d)
		}
		if d.SizeBytes < 200<<20 {
			t.Fatalf("size: %d", d.SizeBytes)
		}
		ids[i] = d.ID
		t.Logf("loop %s model=%q serial=%q byid=%s type=%s smart=%s", dev, d.Model, d.Serial, d.ByID, d.Type, d.SMART.Healthy)
	}

	if kernelFS("btrfs") && exec.Command("mkfs.btrfs", "--version").Run() == nil {
		btrfsLoops(t, s, jobs, ctx, base, devs, ids, confirms)
	} else {
		t.Log("skipping live btrfs: this kernel cannot mount it")
	}
	if kernelFS("xfs") && exec.Command("mkfs.xfs", "-V").Run() == nil {
		xfsLoop(t, s, jobs, ctx, base, devs[0], ids[0], confirms[0])
	}

	if _, err := s.CreatePool(ctx, CreatePool{
		Name: "nope", FS: FSXFS, Layout: LayoutSingle,
		Disks: []string{"/dev/vda"}, Confirm: []string{"whatever"},
	}); err == nil {
		t.Fatal("formatted /dev/vda")
	}
	for dev, uuid := range before {
		after := strings.TrimSpace(commandOut("blkid", "-p", "-o", "value", "-s", "UUID", dev))
		if after != uuid {
			t.Fatalf("%s uuid changed from %q to %q", dev, uuid, after)
		}
	}
	rec.mu.Lock()
	bad := append([]string(nil), rec.bad...)
	rec.mu.Unlock()
	if len(bad) > 0 {
		t.Fatalf("touched a real disk:\n%s", strings.Join(bad, "\n"))
	}
	if len(audits) == 0 {
		t.Fatal("no audit events")
	}
}

func btrfsLoops(t *testing.T, s *Service, jobs *install.Runner, ctx context.Context, base string, devs, ids, confirms []string) {
	t.Helper()
	_, err := s.CreatePool(ctx, CreatePool{
		Name: "data", FS: FSBtrfs, Layout: LayoutRAID1,
		Disks: []string{ids[0], ids[1]}, Confirm: []string{"wrong", "wrong"},
	})
	if err == nil {
		t.Fatal("wrong confirmation created a pool")
	}
	if out := strings.TrimSpace(commandOut("blkid", devs[0])); out != "" {
		t.Fatalf("disk was formatted without a matching serial: %s", out)
	}
	pool, err := s.CreatePool(ctx, CreatePool{
		Name: "data", FS: FSBtrfs, Layout: LayoutRAID1,
		Disks: []string{ids[0], ids[1]}, Confirm: []string{confirms[0], confirms[1]},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMounted(t, pool, "btrfs")
	if err := os.WriteFile(filepath.Join(pool.Mount, "hello"), []byte("feldcore"), 0o644); err != nil {
		t.Fatal(err)
	}
	runMaint(t, s, jobs, ctx, "data", KindScrub, false)
	runMaint(t, s, jobs, ctx, "data", KindTrim, true)
	if _, err := s.SetSchedule(ctx, Schedule{Pool: "data", Kind: KindScrub, Enabled: true, EveryHours: 168}); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddDisk(ctx, AddDisk{Pool: "data", Disk: ids[2], Confirm: confirms[2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Pool.Members) != 3 {
		t.Fatalf("members: %+v", added.Pool.Members)
	}
	if added.Job != nil {
		if bal := waitLoopJob(t, jobs, added.Job.Name); bal.State != install.Succeeded {
			t.Fatalf("balance: %s", bal.Error)
		}
	}
	replaced, err := s.ReplaceDisk(ctx, ReplaceDisk{Pool: "data", Old: ids[0], Disk: ids[3], Confirm: confirms[3]})
	if err != nil {
		t.Fatal(err)
	}
	if rep := waitLoopJob(t, jobs, replaced.Name); rep.State != install.Succeeded {
		t.Fatalf("replace: %s", rep.Error)
	}
	placeAndCache(t, s, ctx, base, "media", "data")
	if err := s.DestroyPool(ctx, "data", "NO"); err == nil {
		t.Fatal("destroyed without the pool name")
	}
	if err := s.DestroyPool(ctx, "data", "data"); err != nil {
		t.Fatal(err)
	}
	left := strings.TrimSpace(commandOut("blkid", "-p", "-o", "value", "-s", "UUID", devs[1]))
	if !strings.EqualFold(left, pool.UUID) {
		t.Fatalf("destroy wiped or changed the filesystem: %q", left)
	}
}

func xfsLoop(t *testing.T, s *Service, jobs *install.Runner, ctx context.Context, base, dev, id, confirm string) {
	t.Helper()
	_, err := s.CreatePool(ctx, CreatePool{
		Name: "solo", FS: FSXFS, Layout: LayoutSingle,
		Disks: []string{id}, Confirm: []string{"wrong"},
	})
	if err == nil {
		t.Fatal("wrong confirmation created an xfs pool")
	}
	if out := strings.TrimSpace(commandOut("blkid", dev)); out != "" && !strings.Contains(out, "btrfs") {
		// A previous btrfs scenario may have left a signature on a disk that
		// was replaced out. A blank disk must still be blank.
		if !kernelFS("btrfs") {
			t.Fatalf("disk was formatted without a matching serial: %s", out)
		}
	}
	pool, err := s.CreatePool(ctx, CreatePool{
		Name: "solo", FS: FSXFS, Layout: LayoutSingle,
		Disks: []string{id}, Confirm: []string{confirm},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMounted(t, pool, "xfs")
	if err := os.WriteFile(filepath.Join(pool.Mount, "hello"), []byte("feldcore"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.run.Look("xfs_scrub") {
		// xfs_scrub exits non-zero when the kernel has no metadata scrub
		// facility. The job records that. It must not format anything.
		job, err := s.RunJob(ctx, "solo", KindScrub)
		if err != nil {
			t.Fatal(err)
		}
		done := waitLoopJob(t, jobs, job.Name)
		if done.State == install.Failed {
			msg := strings.ToLower(done.Error)
			if !strings.Contains(msg, "not available") && !strings.Contains(msg, "not detected") && !strings.Contains(msg, "not supported") {
				t.Fatalf("scrub solo: %s", done.Error)
			}
			t.Logf("xfs scrub is not supported by this kernel: %s", done.Error)
		}
	}
	runMaint(t, s, jobs, ctx, "solo", KindTrim, true)
	if _, err := s.SetSchedule(ctx, Schedule{Pool: "solo", Kind: KindTrim, Enabled: true, EveryHours: 24}); err != nil {
		t.Fatal(err)
	}
	placeAndCache(t, s, ctx, base, "films", "solo")
	if err := s.DestroyPool(ctx, "solo", "solo"); err != nil {
		t.Fatal(err)
	}
	left := strings.TrimSpace(commandOut("blkid", "-p", "-o", "value", "-s", "UUID", dev))
	if !strings.EqualFold(left, pool.UUID) {
		t.Fatalf("destroy wiped the xfs filesystem: %q", left)
	}
}

func placeAndCache(t *testing.T, s *Service, ctx context.Context, base, share, pool string) {
	t.Helper()
	b, err := s.BindShare(ctx, share, pool)
	if err != nil {
		t.Fatal(err)
	}
	if link, err := os.Readlink(filepath.Join(base, "shares", share)); err != nil || link != b.Path {
		t.Fatalf("share link %s %v", link, err)
	}
	cache, err := s.SetCache(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache.Path); err != nil {
		t.Fatal(err)
	}
	if err := s.UnbindShare(ctx, share); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCache(ctx, ""); err != nil {
		t.Fatal(err)
	}
}

func assertMounted(t *testing.T, pool Pool, fs string) {
	t.Helper()
	if pool.UUID == "" || !pool.Mounted {
		t.Fatalf("pool: %+v", pool)
	}
	gotUUID := strings.TrimSpace(commandOut("findmnt", "-n", "-o", "UUID", pool.Mount))
	gotFS := strings.TrimSpace(commandOut("findmnt", "-n", "-o", "FSTYPE", pool.Mount))
	if !strings.EqualFold(gotUUID, pool.UUID) || gotFS != fs {
		t.Fatalf("mounted %s %s, want %s %s", gotFS, gotUUID, fs, pool.UUID)
	}
	if pool.TotalBytes == 0 || pool.TotalBytes > 2<<30 {
		t.Fatalf("usage looks like the host disk: %d", pool.TotalBytes)
	}
}

func runMaint(t *testing.T, s *Service, jobs *install.Runner, ctx context.Context, pool, kind string, trim bool) {
	t.Helper()
	job, err := s.RunJob(ctx, pool, kind)
	if err != nil {
		t.Fatal(err)
	}
	done := waitLoopJob(t, jobs, job.Name)
	if done.State == install.Succeeded {
		return
	}
	if trim {
		msg := strings.ToLower(done.Error)
		if strings.Contains(msg, "discard") || strings.Contains(msg, "not supported") || strings.Contains(msg, "fitrim") {
			t.Logf("trim reported no discard support: %s", done.Error)
			return
		}
	}
	t.Fatalf("%s %s: %s", kind, pool, done.Error)
}

func waitLoopJob(t *testing.T, jobs *install.Runner, name string) install.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		job, ok := jobs.Latest(name)
		if ok && (job.State == install.Succeeded || job.State == install.Failed) {
			return job
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", name)
	return install.Job{}
}

func commandOut(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	return string(out)
}
