package snapshots

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
)

type fakeRT struct {
	src      docker.SnapSource
	stops    int
	starts   int
	tags     []string
	removed  []string
	applied  docker.Spec
	applyN   int
	untagErr error
}

func (f *fakeRT) SnapshotSource(context.Context, string) (docker.SnapSource, error) {
	return f.src, nil
}
func (f *fakeRT) Stop(context.Context, string) error  { f.stops++; f.src.Running = false; return nil }
func (f *fakeRT) Start(context.Context, string) error { f.starts++; f.src.Running = true; return nil }
func (f *fakeRT) TagImage(_ context.Context, _, ref string) error {
	f.tags = append(f.tags, ref)
	return nil
}
func (f *fakeRT) RemoveImageRef(_ context.Context, ref string) error {
	f.removed = append(f.removed, ref)
	return f.untagErr
}
func (f *fakeRT) ApplySpec(_ context.Context, spec docker.Spec) (string, error) {
	f.applied = spec
	f.applyN++
	return "new", nil
}

type memShell struct {
	fs       string
	failSnap bool
	calls    []string
}

func (m *memShell) FSType(string) (string, error) { return m.fs, nil }
func (m *memShell) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	m.calls = append(m.calls, line)
	if m.failSnap && strings.Contains(line, "subvolume snapshot") {
		return []byte("not a subvolume"), errSnap
	}
	if name == "zfs" && len(args) > 0 && args[0] == "list" {
		return []byte("pool/appdata\t" + args[len(args)-1] + "\n"), nil
	}
	return []byte("ok"), nil
}

type snapErr string

func (e snapErr) Error() string { return string(e) }

const errSnap snapErr = "snapshot failed"

func TestTakeRestoreAndRef(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "appdata")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "save.db"), []byte("version-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	rt := &fakeRT{src: docker.SnapSource{
		ID: "abc", Name: "Valheim", Image: "valheim:latest", ImageID: "sha256:abc",
		Running: true,
		Spec:    docker.Spec{Name: "Valheim", Image: "valheim:latest", Configs: []docker.Config{{Type: "path", Host: app, Container: "/data"}}},
		Binds:   []docker.SnapBind{{Host: app, Container: "/data"}},
	}}
	svc := &Service{
		Runtime: rt, Store: &memStore{}, DataDir: dir,
		Now:   func() time.Time { return when },
		Shell: &memShell{fs: "ext4"},
	}
	item, err := svc.Take(context.Background(), TakeOptions{Ref: "abc", Note: "before the fight"})
	if err != nil {
		t.Fatal(err)
	}
	if item.SnapImage != "feldcore-snap/valheim:20261116-120000" {
		t.Fatalf("image %s", item.SnapImage)
	}
	if rt.stops != 1 || rt.starts != 1 {
		t.Fatalf("stop/start %d %d", rt.stops, rt.starts)
	}
	if len(item.Methods) != 1 || item.Methods[0] != "tar.zst" {
		t.Fatalf("methods %+v", item.Methods)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "snapshots", "valheim", item.ID, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "snapshots", "valheim", item.ID, "snapshot.json"))
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	if strings.Contains(string(raw), "secret-env-value") {
		t.Fatal("unexpected")
	}
	// The list view must not be how secrets would leak: Item has no spec.
	listed, err := svc.List(context.Background(), "abc")
	if err != nil || len(listed) != 1 || listed[0].Note != "before the fight" {
		t.Fatalf("list %+v %v", listed, err)
	}
	if err := os.WriteFile(filepath.Join(app, "save.db"), []byte("version-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Restore(context.Background(), "abc", item.ID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(app, "save.db"))
	if err != nil || string(got) != "version-1" {
		t.Fatalf("restored %q %v", got, err)
	}
	if rt.applied.Image != item.SnapImage || !rt.applied.Autostart {
		t.Fatalf("applied %+v", rt.applied)
	}
}

func TestLiveDoesNotStop(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	_ = os.MkdirAll(app, 0o755)
	_ = os.WriteFile(filepath.Join(app, "a"), []byte("x"), 0o644)
	rt := &fakeRT{src: docker.SnapSource{
		ID: "id", Name: "app", Image: "app:1", ImageID: "sha256:1", Running: true,
		Spec: docker.Spec{Name: "app", Image: "app:1"}, Binds: []docker.SnapBind{{Host: app, Container: "/a"}},
	}}
	svc := &Service{Runtime: rt, Store: &memStore{}, DataDir: dir, Shell: &memShell{fs: "ext4"}}
	item, err := svc.Take(context.Background(), TakeOptions{Ref: "id", Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if rt.stops != 0 {
		t.Fatalf("live snapshot stopped the container")
	}
	if len(item.Warnings) == 0 {
		t.Fatal("expected a live warning")
	}
}

func TestBtrfsThenFallback(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	_ = os.MkdirAll(app, 0o755)
	_ = os.WriteFile(filepath.Join(app, "a"), []byte("x"), 0o644)
	sh := &memShell{fs: "btrfs"}
	rt := &fakeRT{src: docker.SnapSource{
		ID: "id", Name: "app", Image: "app:1", ImageID: "sha256:1",
		Spec: docker.Spec{Name: "app", Image: "app:1"}, Binds: []docker.SnapBind{{Host: app, Container: "/a"}},
	}}
	svc := &Service{Runtime: rt, Store: &memStore{}, DataDir: dir, Shell: sh}
	item, err := svc.Take(context.Background(), TakeOptions{Ref: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Methods[0] != "btrfs" {
		t.Fatalf("methods %+v calls %v", item.Methods, sh.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "snapshots", "app", item.ID, "appdata-0.tar.zst")); !os.IsNotExist(err) {
		t.Fatal("btrfs snapshot also wrote an archive")
	}

	sh.failSnap = true
	app2 := filepath.Join(dir, "app2")
	_ = os.MkdirAll(app2, 0o755)
	_ = os.WriteFile(filepath.Join(app2, "a"), []byte("x"), 0o644)
	rt.src.Binds = []docker.SnapBind{{Host: app2, Container: "/a"}}
	item, err = svc.Take(context.Background(), TakeOptions{Ref: "id", Note: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Methods[0] != "tar.zst" {
		t.Fatalf("fallback methods %+v", item.Methods)
	}
}

func TestZFSChosen(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	_ = os.MkdirAll(app, 0o755)
	sh := &memShell{fs: "zfs"}
	rt := &fakeRT{src: docker.SnapSource{
		ID: "id", Name: "app", Image: "app:1", ImageID: "sha256:1",
		Spec: docker.Spec{Name: "app", Image: "app:1"}, Binds: []docker.SnapBind{{Host: app, Container: "/a"}},
	}}
	svc := &Service{Runtime: rt, Store: &memStore{}, DataDir: dir, Shell: sh}
	item, err := svc.Take(context.Background(), TakeOptions{Ref: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Methods[0] != "zfs" {
		t.Fatalf("methods %+v calls %v", item.Methods, sh.calls)
	}
}

func TestRetention(t *testing.T) {
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	var list []Meta
	for i := 0; i < 6; i++ {
		list = append(list, Meta{ID: string(rune('a' + i)), CreatedAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	list = append(list, Meta{ID: "old", CreatedAt: now.Add(-40 * 24 * time.Hour)})
	drop := expired(list, 5, 30, now)
	got := strings.Join(drop, ",")
	if !strings.Contains(got, "old") {
		t.Fatalf("age rule missed: %s", got)
	}
	// 6 recent + 1 old. Newest 5 of the recent ones stay; the 6th recent and the old go.
	// Sorted newest first: hours 0..5 then old. index >= 5 drops the 6th recent (id 'f'? i=5 is 'f' if i starts 0: a is newest).
	if !strings.Contains(got, "f") {
		t.Fatalf("count rule missed: %s", got)
	}
	if strings.Contains(got, "a") {
		t.Fatalf("newest deleted: %s", got)
	}
}

func TestAutoSkipsSystemContainer(t *testing.T) {
	rt := &fakeRT{src: docker.SnapSource{ID: "c", Name: "feldcore-caddy", Role: "proxy", ImageID: "sha256:1", Spec: docker.Spec{Name: "feldcore-caddy", Image: "caddy:2"}}}
	svc := &Service{Runtime: rt, Store: &memStore{}, DataDir: t.TempDir()}
	if err := svc.BeforeChange(context.Background(), "c", "before edit"); err != nil {
		t.Fatal(err)
	}
	if len(rt.tags) != 0 {
		t.Fatal("system container was snapshotted")
	}
}

func TestUsage(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{Store: &memStore{}, DataDir: dir}
	u := svc.Usage()
	if !strings.HasSuffix(u.Path, "/snapshots") || u.KeepN != 5 || u.KeepDays != 30 {
		t.Fatalf("%+v", u)
	}
	if err := svc.SetRetention(context.Background(), 3, 14, ""); err != nil {
		t.Fatal(err)
	}
	u = svc.Usage()
	if u.KeepN != 3 || u.KeepDays != 14 {
		t.Fatalf("%+v", u)
	}
}
