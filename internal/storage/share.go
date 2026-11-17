package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SetCache marks one pool as the cache that holds appdata.
// An empty name clears it. Files already in appdata are not moved.
func (s *Service) SetCache(ctx context.Context, pool string) (CacheResult, error) {
	pool = strings.TrimSpace(pool)
	s.opMu.Lock()
	defer s.opMu.Unlock()
	res, err := s.setCache(ctx, pool)
	action := "storage.cache.set"
	if pool == "" {
		action = "storage.cache.clear"
	}
	if err != nil {
		s.note(ctx, action, pool, "error")
		return CacheResult{}, err
	}
	s.note(ctx, action, pool, "ok")
	return res, nil
}

func (s *Service) setCache(ctx context.Context, pool string) (CacheResult, error) {
	if pool == "" {
		if err := s.update(func(f *file) error {
			f.CachePool = ""
			return nil
		}); err != nil {
			return CacheResult{}, err
		}
		if s.setAppdata != nil {
			if err := s.setAppdata(""); err != nil {
				return CacheResult{}, op("The cache flag was cleared, but the appdata path could not be reset.")
			}
		}
		return CacheResult{Note: "The cache pool was cleared. Files were left where they are."}, nil
	}
	rec, ok := s.snapshot().pool(pool)
	if !ok {
		return CacheResult{}, input("That pool does not exist.")
	}
	info := s.mountInfo(ctx, rec.Mount)
	if !info.Mounted {
		return CacheResult{}, input("Mount the pool before using it as the cache.")
	}
	dir := filepath.Join(rec.Mount, "appdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CacheResult{}, op("Could not create the appdata folder on the cache pool.")
	}
	if err := s.update(func(f *file) error {
		if _, ok := f.pool(pool); !ok {
			return input("That pool does not exist.")
		}
		f.CachePool = pool
		return nil
	}); err != nil {
		return CacheResult{}, err
	}
	if s.setAppdata != nil {
		if err := s.setAppdata(dir); err != nil {
			_ = s.update(func(f *file) error {
				if f.CachePool == pool {
					f.CachePool = ""
				}
				return nil
			})
			return CacheResult{}, op("The appdata path could not be saved, so the cache was not changed.")
		}
	}
	return CacheResult{
		Pool: pool,
		Path: dir,
		Note: "Appdata now points at this pool. Existing files were not moved.",
	}, nil
}

// CachePath is the appdata directory on the cache pool, or empty when no
// cache is set.
func (s *Service) CachePath() string {
	return cachePath(s.snapshot())
}

// BindShare places a share on a pool. It creates the folder on the pool and,
// when the share folder is missing, links it from the shares root. A folder
// that already has files is left alone.
func (s *Service) BindShare(ctx context.Context, share, pool string) (Binding, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	b, err := s.bind(ctx, share, pool)
	if err != nil {
		s.note(ctx, "storage.share.bind", share, "error")
		return Binding{}, err
	}
	s.note(ctx, "storage.share.bind", share, "ok")
	return b, nil
}

func (s *Service) bind(ctx context.Context, share, pool string) (Binding, error) {
	share = strings.TrimSpace(share)
	pool = strings.TrimSpace(pool)
	if !shareNameRe.MatchString(share) {
		return Binding{}, input("Share names use letters, numbers, hyphens or underscores (up to 32 characters).")
	}
	rec, ok := s.snapshot().pool(pool)
	if !ok {
		return Binding{}, input("That pool does not exist.")
	}
	info := s.mountInfo(ctx, rec.Mount)
	if !info.Mounted {
		return Binding{}, input("Mount the pool before placing a share on it.")
	}
	dest := filepath.Join(rec.Mount, share)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Binding{}, op("Could not create the share folder on the pool.")
	}
	if root := s.root(); root != "" {
		link := filepath.Join(root, share)
		if err := linkShare(link, dest); err != nil {
			return Binding{}, err
		}
	}
	b := Binding{Share: share, Pool: pool, Path: dest}
	if err := s.update(func(f *file) error {
		for i := range f.Shares {
			if strings.EqualFold(f.Shares[i].Share, share) {
				f.Shares[i] = b
				return nil
			}
		}
		f.Shares = append(f.Shares, b)
		return nil
	}); err != nil {
		return Binding{}, err
	}
	return b, nil
}

// UnbindShare removes the placement. A symlink FELDCORE created is replaced
// with an empty folder on the shares root. Files on the pool are left there.
func (s *Service) UnbindShare(ctx context.Context, share string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	err := s.unbind(share)
	if err != nil {
		s.note(ctx, "storage.share.unbind", share, "error")
		return err
	}
	s.note(ctx, "storage.share.unbind", share, "ok")
	return nil
}

func (s *Service) unbind(share string) error {
	share = strings.TrimSpace(share)
	st := s.snapshot()
	var b Binding
	found := false
	for _, cur := range st.Shares {
		if strings.EqualFold(cur.Share, share) {
			b = cur
			found = true
			break
		}
	}
	if !found {
		return input("That share is not placed on a pool.")
	}
	if root := s.root(); root != "" {
		link := filepath.Join(root, b.Share)
		if err := unlinkShare(link, b.Path); err != nil {
			return err
		}
	}
	return s.update(func(f *file) error {
		next := make([]Binding, 0, len(f.Shares))
		for _, cur := range f.Shares {
			if !strings.EqualFold(cur.Share, share) {
				next = append(next, cur)
			}
		}
		f.Shares = next
		return nil
	})
}

func linkShare(link, dest string) error {
	fi, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return op("Could not create the shares folder.")
		}
		if err := os.Symlink(dest, link); err != nil {
			return op("Could not link the share onto the pool.")
		}
		return nil
	}
	if err != nil {
		return op("Could not look at the share folder.")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(link)
		if err != nil {
			return op("Could not read the share link.")
		}
		if filepath.Clean(target) == filepath.Clean(dest) {
			return nil
		}
		return input("That share already points somewhere else. Unplace it first. FELDCORE will not replace the link.")
	}
	return input("That share already has a folder on the main disk. FELDCORE will not move or delete those files.")
}

func unlinkShare(link, dest string) error {
	fi, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return op("Could not look at the share folder.")
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, err := os.Readlink(link)
	if err != nil {
		return op("Could not read the share link.")
	}
	if filepath.Clean(target) != filepath.Clean(dest) {
		return input("That share link was not created for this pool, so it was left in place.")
	}
	if err := os.Remove(link); err != nil {
		return op("Could not remove the share link.")
	}
	if err := os.Mkdir(link, 0o775); err != nil && !errors.Is(err, fs.ErrExist) {
		return op("The pool link was removed, but the share folder could not be recreated. The files are still on the pool.")
	}
	return nil
}

// SharePath returns the pool directory for a share, when one is placed.
func (s *Service) SharePath(share string) (string, bool) {
	share = strings.TrimSpace(share)
	for _, b := range s.snapshot().Shares {
		if strings.EqualFold(b.Share, share) && b.Path != "" {
			return b.Path, true
		}
	}
	return "", false
}

func (s *Service) root() string {
	if s.sharesRoot == nil {
		return ""
	}
	return strings.TrimSpace(s.sharesRoot())
}

func (s *Service) shareRows(st file) []ShareRow {
	byName := map[string]Binding{}
	var order []string
	if s.shareNames != nil {
		for _, n := range s.shareNames() {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if _, ok := byName[strings.ToLower(n)]; ok {
				continue
			}
			byName[strings.ToLower(n)] = Binding{Share: n}
			order = append(order, strings.ToLower(n))
		}
	}
	for _, b := range st.Shares {
		key := strings.ToLower(b.Share)
		byName[key] = b
		seen := false
		for _, k := range order {
			if k == key {
				seen = true
				break
			}
		}
		if !seen {
			order = append(order, key)
		}
	}
	rows := make([]ShareRow, 0, len(order))
	root := s.root()
	for _, key := range order {
		b := byName[key]
		path := b.Path
		if path == "" && root != "" {
			path = filepath.Join(root, b.Share)
		}
		rows = append(rows, ShareRow{Name: b.Share, Pool: b.Pool, Path: path})
	}
	return rows
}

// SetSpinDown stores and applies how long a spinning disk waits before it
// parks. minutes 0 turns spin-down off. Solid-state and virtual disks are refused.
func (s *Service) SetSpinDown(ctx context.Context, id string, minutes int) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	err := s.spin(ctx, id, minutes, false)
	if err != nil {
		s.note(ctx, "storage.spindown", id, "error")
		return err
	}
	s.note(ctx, "storage.spindown", id, "ok")
	return nil
}

// SpinDownNow parks one spinning disk immediately. The idle setting is unchanged.
func (s *Service) SpinDownNow(ctx context.Context, id string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	err := s.spin(ctx, id, -1, true)
	if err != nil {
		s.note(ctx, "storage.spindown.now", id, "error")
		return err
	}
	s.note(ctx, "storage.spindown.now", id, "ok")
	return nil
}

func (s *Service) spin(ctx context.Context, id string, minutes int, now bool) error {
	if !s.run.Look("hdparm") {
		return input("hdparm is not installed. FELDCORE will not install it for you.")
	}
	disks, err := s.probe(ctx)
	if err != nil {
		return err
	}
	d, err := resolveDisk(s.annotate(ctx, disks, s.snapshot()), id)
	if err != nil {
		return err
	}
	if d.Type != "hdd" {
		return input("Only spinning hard disks are spun down. Solid-state, NVMe and virtual disks are left running.")
	}
	if err := s.requireDevice(d); err != nil {
		return err
	}
	if now {
		_, err := s.cmd(ctx, 15*time.Second, "hdparm", "-y", d.Path)
		return err
	}
	standby, err := hdparmStandby(minutes)
	if err != nil {
		return err
	}
	if _, err := s.cmd(ctx, 15*time.Second, "hdparm", "-S", itoa(standby), d.Path); err != nil {
		return err
	}
	rule := SpinRule{ID: d.ID, Serial: d.Serial, Minutes: minutes, Applied: true}
	return s.update(func(f *file) error {
		for i := range f.SpinDown {
			if f.SpinDown[i].ID == d.ID {
				f.SpinDown[i] = rule
				return nil
			}
		}
		f.SpinDown = append(f.SpinDown, rule)
		return nil
	})
}

func (s *Service) applySpin(ctx context.Context, st file) {
	if len(st.SpinDown) == 0 || !s.run.Look("hdparm") {
		return
	}
	disks, err := s.probe(ctx)
	if err != nil {
		return
	}
	for _, rule := range st.SpinDown {
		d, err := resolveDisk(disks, rule.ID)
		if err != nil || d.Type != "hdd" {
			continue
		}
		if err := s.requireDevice(d); err != nil {
			continue
		}
		standby, err := hdparmStandby(rule.Minutes)
		if err != nil {
			continue
		}
		if _, err := s.cmd(ctx, 15*time.Second, "hdparm", "-S", itoa(standby), d.Path); err != nil && s.log != nil {
			s.log.Warn("spin-down was not applied", "disk", d.Path, "err", err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
