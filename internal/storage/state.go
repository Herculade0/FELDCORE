package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// file is the on-disk record. Live usage and health are not stored.
type file struct {
	Version   int        `json:"version"`
	Pools     []poolRec  `json:"pools"`
	Schedules []Schedule `json:"schedules"`
	SpinDown  []SpinRule `json:"spinDown"`
	Shares    []Binding  `json:"shares"`
	CachePool string     `json:"cachePool,omitempty"`
}

type poolRec struct {
	Name      string    `json:"name"`
	FS        string    `json:"fs"`
	Layout    string    `json:"layout"`
	UUID      string    `json:"uuid"`
	Mount     string    `json:"mount"`
	Members   []Member  `json:"members"`
	CreatedAt time.Time `json:"createdAt"`
	BootMount bool      `json:"bootMount,omitempty"`
}

func newFile() file {
	return file{Version: 1, Pools: []poolRec{}, Schedules: []Schedule{}, SpinDown: []SpinRule{}, Shares: []Binding{}}
}

func (f file) clone() file {
	out := f
	out.Pools = append([]poolRec(nil), f.Pools...)
	for i := range out.Pools {
		out.Pools[i].Members = append([]Member(nil), f.Pools[i].Members...)
	}
	out.Schedules = append([]Schedule(nil), f.Schedules...)
	out.SpinDown = append([]SpinRule(nil), f.SpinDown...)
	out.Shares = append([]Binding(nil), f.Shares...)
	return out
}

func loadFile(path string) (file, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newFile(), nil
	}
	if err != nil {
		return file{}, fmt.Errorf("read storage state: %w", err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return file{}, fmt.Errorf("storage state %s is corrupt: %w", path, err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Pools == nil {
		f.Pools = []poolRec{}
	}
	if f.Schedules == nil {
		f.Schedules = []Schedule{}
	}
	if f.SpinDown == nil {
		f.SpinDown = []SpinRule{}
	}
	if f.Shares == nil {
		f.Shares = []Binding{}
	}
	return f, nil
}

func saveFile(path string, f file) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}
	f.Version = 1
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".storage-*.tmp")
	if err != nil {
		return fmt.Errorf("write storage state: %w", err)
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (f file) pool(name string) (poolRec, bool) {
	for _, p := range f.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return poolRec{}, false
}
