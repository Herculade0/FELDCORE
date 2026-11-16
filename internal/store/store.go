// Package store persists FELDCORE's own state as a single JSON file.
//
// The state is small (one admin account, sessions, share definitions), so a
// file written atomically on every change is simpler and more inspectable than
// a database, and keeps the binary free of native dependencies.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SMB access modes for a share.
const (
	SMBPublic   = "public"   // anyone on the network can read and write
	SMBReadOnly = "readonly" // anyone on the network can read
	SMBPrivate  = "private"  // only the listed share users, read and write
)

// User is a FELDCORE login account.
type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
}

// Session is a logged-in browser session. Only a hash of the token is stored.
type Session struct {
	TokenHash string    `json:"tokenHash"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// SMB is a share's Windows file sharing configuration.
type SMB struct {
	Enabled bool     `json:"enabled"`
	Access  string   `json:"access"`
	Users   []string `json:"users"`
}

// NFS is a share's NFS export configuration.
type NFS struct {
	Enabled  bool   `json:"enabled"`
	Clients  string `json:"clients"`
	ReadOnly bool   `json:"readOnly"`
}

// Share is a managed folder and how it is exported.
type Share struct {
	Name      string    `json:"name"`
	Comment   string    `json:"comment"`
	SMB       SMB       `json:"smb"`
	NFS       NFS       `json:"nfs"`
	CreatedAt time.Time `json:"createdAt"`
}

// ConfigMeta is the human labelling of one container setting (a path, port,
// variable...). Docker has nowhere to keep it, so FELDCORE does. Display,
// Required and Mask are copied from an Unraid template so a later edit still
// knows which values to hide.
type ConfigMeta struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Display     string `json:"display,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Mask        bool   `json:"mask,omitempty"`
}

// State is everything FELDCORE persists.
type State struct {
	Version  int       `json:"version"`
	Admin    *User     `json:"admin,omitempty"`
	Sessions []Session `json:"sessions,omitempty"`
	Shares   []Share   `json:"shares"`
	// ShareUsers are the SMB accounts FELDCORE created and therefore owns.
	ShareUsers []string `json:"shareUsers"`
	// ContainerMeta holds setting names/descriptions per container name,
	// keyed by a stable id for the setting (e.g. "path:/config").
	ContainerMeta map[string]map[string]ConfigMeta `json:"containerMeta,omitempty"`
	// Appdata is the host folder Unraid's /mnt/user/appdata paths are rewritten
	// to. Empty means "a share named appdata, or <shares root>/appdata".
	Appdata string `json:"appdata,omitempty"`
}

func (s State) clone() State {
	c := s
	if s.Admin != nil {
		a := *s.Admin
		c.Admin = &a
	}
	c.Sessions = append([]Session(nil), s.Sessions...)
	c.Shares = make([]Share, len(s.Shares))
	for i, sh := range s.Shares {
		sh.SMB.Users = append([]string(nil), sh.SMB.Users...)
		c.Shares[i] = sh
	}
	c.ShareUsers = append([]string(nil), s.ShareUsers...)
	if s.ContainerMeta != nil {
		c.ContainerMeta = make(map[string]map[string]ConfigMeta, len(s.ContainerMeta))
		for name, items := range s.ContainerMeta {
			cp := make(map[string]ConfigMeta, len(items))
			for k, v := range items {
				cp[k] = v
			}
			c.ContainerMeta[name] = cp
		}
	}
	return c
}

// ContainerMeta returns the stored setting labels for a container.
func (s *Store) ContainerMeta(name string) map[string]ConfigMeta {
	return s.Snapshot().ContainerMeta[name]
}

// SetContainerMeta replaces the stored setting labels for a container.
func (s *Store) SetContainerMeta(name string, meta map[string]ConfigMeta) error {
	return s.Update(func(st *State) error {
		if len(meta) == 0 {
			delete(st.ContainerMeta, name)
			return nil
		}
		if st.ContainerMeta == nil {
			st.ContainerMeta = map[string]map[string]ConfigMeta{}
		}
		st.ContainerMeta[name] = meta
		return nil
	})
}

// Appdata returns the configured app-data folder, or "" for the default.
func (s *Store) Appdata() string {
	return s.Snapshot().Appdata
}

// SetAppdata stores the app-data folder. An empty path clears it.
func (s *Store) SetAppdata(path string) error {
	return s.Update(func(st *State) error {
		st.Appdata = path
		return nil
	})
}

// DeleteContainerMeta forgets a container's setting labels.
func (s *Store) DeleteContainerMeta(name string) error {
	if _, ok := s.Snapshot().ContainerMeta[name]; !ok {
		return nil
	}
	return s.Update(func(st *State) error {
		delete(st.ContainerMeta, name)
		return nil
	})
}

// Store guards the state file.
type Store struct {
	mu    sync.Mutex
	path  string
	state State
}

// Open loads (or initialises) the state file inside dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	s := &Store{path: filepath.Join(dir, "state.json"), state: State{Version: 1}}
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// First run: make sure we can actually write here before going on.
		if err := s.save(s.state); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("read state: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.state); err != nil {
			return nil, fmt.Errorf("state file %s is corrupt: %w", s.path, err)
		}
	}
	return s, nil
}

// Snapshot returns an independent copy of the current state.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.clone()
}

// Update applies fn to a copy of the state and persists it. If fn or the save
// fails, the in-memory state is left untouched.
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state.clone()
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

// save writes the state atomically: temp file, fsync, rename.
func (s *Store) save(st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("write state: %w", err)
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
	return os.Rename(tmp.Name(), s.path)
}
