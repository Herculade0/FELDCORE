// Package snapshots copies a container's settings, image and mapped folders
// so they can be put back later. The image is tagged feldcore-snap/<name>:<id>
// so a prune does not delete it. Folders are a btrfs or zfs snapshot when the
// filesystem supports that, and a tar.zst archive otherwise.
package snapshots

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/names"
)

// InputError is safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a failed operation safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func input(msg string) error { return &InputError{Msg: msg} }
func op(msg string) error    { return &OpError{Msg: msg} }

// Runtime is the Docker operations a snapshot needs.
type Runtime interface {
	SnapshotSource(ctx context.Context, id string) (docker.SnapSource, error)
	Stop(ctx context.Context, id string) error
	Start(ctx context.Context, id string) error
	TagImage(ctx context.Context, imageID, ref string) error
	RemoveImageRef(ctx context.Context, ref string) error
	ApplySpec(ctx context.Context, spec docker.Spec) (string, error)
}

// Service stores and restores snapshots.
type Service struct {
	Runtime Runtime
	Store   interface {
		Snapshot() (keepN, keepDays int, configured bool, dir string)
		SetRetention(keepN, keepDays int, dir string) error
	}
	DataDir string
	Locate  func() string
	Log     *slog.Logger
	Now     func() time.Time
	Shell   shell
}

// StoreKeeper is the subset of store.Store the service uses. The real store
// is adapted in main; tests can use memStore.
type memStore struct {
	keepN, keepDays int
	configured      bool
	dir             string
}

func (m *memStore) Snapshot() (int, int, bool, string) {
	return m.keepN, m.keepDays, m.configured, m.dir
}
func (m *memStore) SetRetention(keepN, keepDays int, dir string) error {
	m.keepN, m.keepDays, m.configured, m.dir = keepN, keepDays, true, dir
	return nil
}

// Volume is one mapped folder inside a snapshot.
type Volume struct {
	Host       string `json:"host"`
	Container  string `json:"container,omitempty"`
	Method     string `json:"method,omitempty"`
	Archive    string `json:"archive,omitempty"`
	FSSnapshot string `json:"fsSnapshot,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Skipped    string `json:"skipped,omitempty"`
}

// Meta is the on-disk record. Spec is kept so a restore can recreate the
// container. List responses omit it.
type Meta struct {
	ID        string      `json:"id"`
	Container string      `json:"container"`
	CreatedAt time.Time   `json:"createdAt"`
	Note      string      `json:"note,omitempty"`
	Image     string      `json:"image"`
	ImageID   string      `json:"imageId"`
	SnapImage string      `json:"snapImage"`
	Live      bool        `json:"live"`
	Auto      bool        `json:"auto"`
	Running   bool        `json:"running"`
	Spec      docker.Spec `json:"spec"`
	Volumes   []Volume    `json:"volumes"`
	Size      int64       `json:"size"`
	Warnings  []string    `json:"warnings,omitempty"`
}

// Item is one snapshot as the UI lists it.
type Item struct {
	ID        string    `json:"id"`
	Container string    `json:"container"`
	CreatedAt time.Time `json:"createdAt"`
	Note      string    `json:"note,omitempty"`
	Image     string    `json:"image"`
	SnapImage string    `json:"snapImage"`
	Live      bool      `json:"live"`
	Auto      bool      `json:"auto"`
	Size      int64     `json:"size"`
	Methods   []string  `json:"methods"`
	Warnings  []string  `json:"warnings,omitempty"`
}

// Usage is disk use of the snapshot folder.
type Usage struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Free     int64  `json:"free"`
	Total    int64  `json:"total"`
	KeepN    int    `json:"keepN"`
	KeepDays int    `json:"keepDays"`
}

// TakeOptions describes a new snapshot.
type TakeOptions struct {
	Ref  string
	Note string
	Live bool
	Auto bool
}

// Take copies a container. Unless Live is set, a running container is stopped
// for the copy and started again afterwards.
func (s *Service) Take(ctx context.Context, opt TakeOptions) (Item, error) {
	ctx = context.WithoutCancel(ctx)
	if s.Runtime == nil {
		return Item{}, op("Docker is not available.")
	}
	src, err := s.Runtime.SnapshotSource(ctx, opt.Ref)
	if err != nil {
		return Item{}, err
	}
	if src.Role != "" && opt.Auto {
		return Item{}, nil
	}
	if src.Name == "" || src.ImageID == "" {
		return Item{}, op("This container has no image to pin, so it cannot be snapshotted.")
	}
	now := s.now()
	id := s.allocID(src.Name, now)
	ref := ImageRef(src.Name, id)
	if err := s.Runtime.TagImage(ctx, src.ImageID, ref); err != nil {
		return Item{}, op("Could not pin the container's image. " + err.Error())
	}
	fail := func(err error) (Item, error) {
		_ = s.Runtime.RemoveImageRef(ctx, ref)
		return Item{}, err
	}
	stopped := false
	if !opt.Live && src.Running {
		if err := s.Runtime.Stop(ctx, src.ID); err != nil {
			return fail(op("Could not stop the container for a consistent snapshot. " + err.Error()))
		}
		stopped = true
	}
	if stopped {
		defer func() {
			if err := s.Runtime.Start(ctx, src.ID); err != nil && s.Log != nil {
				s.Log.Warn("snapshot saved but the container did not start again", "name", src.Name, "err", err)
			}
		}()
	}
	dir := s.snapDir(src.Name, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(op("Could not create the snapshot folder."))
	}
	meta := Meta{
		ID: id, Container: src.Name, CreatedAt: now, Note: cleanNote(opt.Note),
		Image: src.Image, ImageID: src.ImageID, SnapImage: ref,
		Live: opt.Live, Auto: opt.Auto, Running: src.Running, Spec: src.Spec,
	}
	if opt.Live && src.Running {
		meta.Warnings = append(meta.Warnings, "Taken while the container was running, so a file may have been copied mid-write.")
	}
	sh := s.shell()
	mapped := false
	for i, b := range src.Binds {
		if b.Named {
			meta.Volumes = append(meta.Volumes, Volume{
				Host: b.Host, Container: b.Container, Skipped: "Docker named volumes are not copied. The volume is still attached when you restore.",
			})
			meta.Warnings = append(meta.Warnings, "Named volume "+b.Host+" was not copied.")
			continue
		}
		if b.Host == "" {
			continue
		}
		if _, err := os.Stat(b.Host); err != nil {
			meta.Warnings = append(meta.Warnings, b.Host+" is not on this server, so it was not copied.")
			continue
		}
		mapped = true
		vol, err := capture(ctx, sh, b.Host, dir, id, i)
		if err != nil {
			_ = os.RemoveAll(dir)
			return fail(op("Could not copy " + b.Host + ". " + err.Error()))
		}
		vol.Container = b.Container
		meta.Volumes = append(meta.Volumes, vol)
		meta.Size += vol.Bytes
	}
	if !mapped && len(src.Binds) == 0 {
		meta.Warnings = append(meta.Warnings, "This container has no mapped folders. The snapshot keeps its settings and its image.")
	}
	if err := writeMeta(dir, &meta); err != nil {
		_ = os.RemoveAll(dir)
		return fail(op("Could not save the snapshot record."))
	}
	meta.Size = dirSize(dir)
	_ = writeMeta(dir, &meta)
	s.prune(ctx, src.Name)
	if s.Log != nil {
		s.Log.Info("snapshot taken", "container", src.Name, "id", id, "image", ref, "live", opt.Live)
	}
	return meta.item(), nil
}

// BeforeChange takes the automatic snapshot that runs ahead of an edit.
// FELDCORE's own proxy and tunnel containers are skipped.
func (s *Service) BeforeChange(ctx context.Context, ref, note string) error {
	if s == nil || s.Runtime == nil {
		return nil
	}
	src, err := s.Runtime.SnapshotSource(ctx, ref)
	if err != nil {
		return err
	}
	if src.Role != "" {
		return nil
	}
	_, err = s.Take(ctx, TakeOptions{Ref: ref, Note: note, Auto: true})
	return err
}

// List returns a container's snapshots, newest first.
func (s *Service) List(ctx context.Context, ref string) ([]Item, error) {
	name := s.containerName(ctx, ref)
	all, err := s.readAll(name)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(all))
	for _, m := range all {
		out = append(out, m.item())
	}
	return out, nil
}

// Restore puts a snapshot's folders back and recreates the container from
// the pinned image and the saved settings.
func (s *Service) Restore(ctx context.Context, ref, id string) error {
	ctx = context.WithoutCancel(ctx)
	if err := checkID(id); err != nil {
		return err
	}
	name := s.containerName(ctx, ref)
	meta, dir, err := s.load(name, id)
	if err != nil {
		return err
	}
	var current docker.SnapSource
	var have bool
	if src, err := s.Runtime.SnapshotSource(ctx, ref); err == nil {
		current, have = src, true
	} else if src, err := s.Runtime.SnapshotSource(ctx, meta.Container); err == nil {
		current, have = src, true
	}
	if have && current.Running {
		if err := s.Runtime.Stop(ctx, current.ID); err != nil {
			return op("Could not stop the container before restoring. " + err.Error())
		}
	}
	sh := s.shell()
	for _, vol := range meta.Volumes {
		if err := restoreVolume(ctx, sh, dir, vol); err != nil {
			if have && current.Running {
				_ = s.Runtime.Start(ctx, current.ID)
			}
			return op(err.Error())
		}
	}
	spec := meta.Spec
	spec.Name = meta.Container
	spec.Image = meta.SnapImage
	spec.Autostart = meta.Running
	if _, err := s.Runtime.ApplySpec(ctx, spec); err != nil {
		return op("The saved files were put back, but the container could not be recreated. " + err.Error())
	}
	if s.Log != nil {
		s.Log.Info("snapshot restored", "container", meta.Container, "id", meta.ID)
	}
	return nil
}

// Delete removes a snapshot, its archives and, when nothing still uses it,
// the pinned image.
func (s *Service) Delete(ctx context.Context, ref, id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	name := s.containerName(ctx, ref)
	meta, dir, err := s.load(name, id)
	if err != nil {
		return err
	}
	sh := s.shell()
	for _, vol := range meta.Volumes {
		deleteVolume(ctx, sh, vol)
	}
	if err := os.RemoveAll(dir); err != nil {
		return op("Could not delete the snapshot files.")
	}
	if meta.SnapImage != "" && s.Runtime != nil {
		if err := s.Runtime.RemoveImageRef(ctx, meta.SnapImage); err != nil {
			return op("The snapshot files were deleted, but the saved image is still used by a container, so it was left installed.")
		}
	}
	return nil
}

// Usage reports how much space snapshots use and how many are kept.
func (s *Service) Usage() Usage {
	root := s.root()
	n, days := s.retention()
	u := Usage{Path: root, KeepN: n, KeepDays: days, Bytes: dirSize(root)}
	if d, err := disk.Usage(root); err == nil {
		u.Free, u.Total = int64(d.Free), int64(d.Total)
	} else if d, err := disk.Usage(filepath.Dir(root)); err == nil {
		u.Free, u.Total = int64(d.Free), int64(d.Total)
	}
	return u
}

// SetRetention stores keep-N and keep-days. Zero means that limit is off.
// dir, when set, must be an absolute folder.
func (s *Service) SetRetention(ctx context.Context, keepN, keepDays int, dir string) error {
	if keepN < 0 || keepN > 1000 || keepDays < 0 || keepDays > 3650 {
		return input("Keep between 0 and 1000 snapshots, for between 0 and 3650 days. Zero means no limit.")
	}
	dir = strings.TrimSpace(dir)
	if dir != "" {
		if !filepath.IsAbs(dir) || strings.Contains(dir, "..") {
			return input("The snapshot folder must be an absolute path.")
		}
	}
	if s.Store == nil {
		return op("Settings cannot be saved.")
	}
	if err := s.Store.SetRetention(keepN, keepDays, dir); err != nil {
		return err
	}
	s.prune(ctx, "")
	return nil
}

// ImageRef is the tag used so Docker will not prune a snapshot's image.
func ImageRef(name, id string) string {
	return "feldcore-snap/" + names.Slug(name) + ":" + id
}

func (s *Service) retention() (int, int) {
	if s.Store == nil {
		return 5, 30
	}
	n, days, configured, _ := s.Store.Snapshot()
	if !configured {
		return 5, 30
	}
	return n, days
}

func (s *Service) root() string {
	if s.Store != nil {
		_, _, _, dir := s.Store.Snapshot()
		if strings.TrimSpace(dir) != "" {
			return dir
		}
	}
	if s.Locate != nil {
		if d := s.Locate(); d != "" {
			return d
		}
	}
	if s.DataDir != "" {
		return filepath.Join(s.DataDir, "snapshots")
	}
	return filepath.Join(os.TempDir(), "feldcore-snapshots")
}

func (s *Service) snapDir(container, id string) string {
	return filepath.Join(s.root(), names.Slug(container), id)
}

func (s *Service) allocID(container string, now time.Time) string {
	base := now.UTC().Format("20060102-150405")
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(s.snapDir(container, id)); os.IsNotExist(err) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) shell() shell {
	if s.Shell != nil {
		return s.Shell
	}
	return osShell{}
}

func (s *Service) containerName(ctx context.Context, ref string) string {
	if s.Runtime != nil {
		if src, err := s.Runtime.SnapshotSource(ctx, ref); err == nil && src.Name != "" {
			return src.Name
		}
	}
	return ref
}

func (s *Service) load(container, id string) (Meta, string, error) {
	dir := s.snapDir(container, id)
	meta, err := readMeta(dir)
	if err != nil {
		// The caller may have passed a container id while the folder is the name.
		if found, fdir, ferr := s.findByID(id); ferr == nil {
			return found, fdir, nil
		}
		return Meta{}, "", input("That snapshot no longer exists.")
	}
	return meta, dir, nil
}

func (s *Service) findByID(id string) (Meta, string, error) {
	root := s.root()
	entries, err := os.ReadDir(root)
	if err != nil {
		return Meta{}, "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name(), id)
		if meta, err := readMeta(dir); err == nil {
			return meta, dir, nil
		}
	}
	return Meta{}, "", fs.ErrNotExist
}

func (s *Service) readAll(container string) ([]Meta, error) {
	dir := filepath.Join(s.root(), names.Slug(container))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := readMeta(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) prune(ctx context.Context, only string) {
	n, days := s.retention()
	root := s.root()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if only != "" && e.Name() != names.Slug(only) {
			continue
		}
		all, err := s.readAll(e.Name())
		if err != nil {
			continue
		}
		for _, id := range expired(all, n, days, s.now()) {
			meta, dir, err := s.load(e.Name(), id)
			if err != nil {
				continue
			}
			for _, vol := range meta.Volumes {
				deleteVolume(ctx, s.shell(), vol)
			}
			_ = os.RemoveAll(dir)
			if meta.SnapImage != "" && s.Runtime != nil {
				_ = s.Runtime.RemoveImageRef(ctx, meta.SnapImage)
			}
		}
	}
}

func (m Meta) item() Item {
	seen := map[string]bool{}
	var methods []string
	for _, v := range m.Volumes {
		if v.Method != "" && !seen[v.Method] {
			seen[v.Method] = true
			methods = append(methods, v.Method)
		}
	}
	if methods == nil {
		methods = []string{}
	}
	return Item{
		ID: m.ID, Container: m.Container, CreatedAt: m.CreatedAt, Note: m.Note,
		Image: m.Image, SnapImage: m.SnapImage, Live: m.Live, Auto: m.Auto,
		Size: m.Size, Methods: methods, Warnings: m.Warnings,
	}
}

func writeMeta(dir string, meta *Meta) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "snapshot.json")
	tmp, err := os.CreateTemp(dir, ".snap-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func readMeta(dir string) (Meta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		return Meta{}, err
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func cleanNote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func checkID(id string) error {
	if id == "" || len(id) > 80 || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return input("That snapshot id is not valid.")
	}
	return nil
}

// expired returns snapshot ids that fall outside the retention rules.
// keepN or keepDays of 0 means that rule is not applied.
func expired(list []Meta, keepN, keepDays int, now time.Time) []string {
	sorted := append([]Meta(nil), list...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	var drop []string
	for i, m := range sorted {
		old := keepDays > 0 && now.Sub(m.CreatedAt) > time.Duration(keepDays)*24*time.Hour
		extra := keepN > 0 && i >= keepN
		if old || extra {
			drop = append(drop, m.ID)
		}
	}
	return drop
}
