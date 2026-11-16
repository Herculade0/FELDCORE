// Package store persists FELDCORE's own state as a single JSON file.
//
// The state is small (one admin account, sessions, agent tokens, share
// definitions), so a file written atomically on every change is simpler and
// more inspectable than a database, and keeps the binary free of native
// dependencies.
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

// APIToken is a named bearer credential for an agent. Only a hash of the
// secret is stored. Prefix is the non-secret start of the token, so a person
// can tell tokens apart after the secret has been shown once.
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hash       string     `json:"hash"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Revoked    bool       `json:"revoked,omitempty"`
}

// AuditEvent is one call made with an agent token.
type AuditEvent struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	TokenID   string    `json:"tokenId,omitempty"`
	TokenName string    `json:"tokenName"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	Result    string    `json:"result"`
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
	// APITokens are bearer credentials for the owner's agents.
	APITokens []APIToken `json:"apiTokens,omitempty"`
	// Audit is the capped log of calls authenticated with those tokens.
	Audit  []AuditEvent `json:"audit,omitempty"`
	Shares []Share      `json:"shares"`
	// ShareUsers are the SMB accounts FELDCORE created and therefore owns.
	ShareUsers []string `json:"shareUsers"`
	// ContainerMeta holds setting names/descriptions per container name,
	// keyed by a stable id for the setting (e.g. "path:/config").
	ContainerMeta map[string]map[string]ConfigMeta `json:"containerMeta,omitempty"`
	// Appdata is the host folder Unraid's /mnt/user/appdata paths are rewritten
	// to. Empty means "a share named appdata, or <shares root>/appdata".
	Appdata string `json:"appdata,omitempty"`
	// SharesRoot is the wizard's chosen shares directory. Empty means the
	// daemon flag/default is still in force.
	SharesRoot string `json:"sharesRoot,omitempty"`
	// Wizard is the first-start setup. Nil means this install predates the
	// wizard (or it has not been claimed yet) and should not be forced through it.
	Wizard *Wizard `json:"wizard,omitempty"`
	// Prefs are choices made on the Settings page.
	Prefs Prefs `json:"prefs,omitempty"`
	// Remote is Cloudflare Tunnel. Tokens are not stored here.
	Remote RemoteConfig `json:"remote,omitempty"`
	// Web is the built-in name and HTTPS proxy.
	Web WebConfig `json:"web,omitempty"`
	// Snapshots is retention and where container copies are kept.
	Snapshots SnapshotConfig `json:"snapshots,omitempty"`
}

// PublicRoute publishes one container through the Cloudflare Tunnel.
// Nothing is published until Enabled is set.
type PublicRoute struct {
	Container string `json:"container"`
	Hostname  string `json:"hostname"`
	Port      int    `json:"port"`
	Enabled   bool   `json:"enabled"`
	RecordID  string `json:"recordId,omitempty"`
}

// RemoteConfig is the tunnel. Mode is "off", "token" or "api".
// Off is the only default: no connector is started and no hostname is published.
type RemoteConfig struct {
	Mode          string        `json:"mode,omitempty"`
	TunnelID      string        `json:"tunnelId,omitempty"`
	TunnelName    string        `json:"tunnelName,omitempty"`
	AccountID     string        `json:"accountId,omitempty"`
	ExposeAdmin   bool          `json:"exposeAdmin,omitempty"`
	AdminHostname string        `json:"adminHostname,omitempty"`
	AdminRecordID string        `json:"adminRecordId,omitempty"`
	Routes        []PublicRoute `json:"routes,omitempty"`
}

// WebRoute is a friendly name for a container, served by the built-in proxy.
// TLS is "off" (plain HTTP), "internal" (the FELDCORE local CA) or "acme"
// (Let's Encrypt via Cloudflare DNS-01).
type WebRoute struct {
	Container string `json:"container"`
	Hostname  string `json:"hostname"`
	Port      int    `json:"port"`
	TLS       string `json:"tls"`
	Enabled   bool   `json:"enabled"`
}

// WebConfig is the reverse proxy. Disabled, with no routes, is the default.
type WebConfig struct {
	Enabled    bool       `json:"enabled,omitempty"`
	Email      string     `json:"email,omitempty"`
	Domain     string     `json:"domain,omitempty"`
	UIHostname string     `json:"uiHostname,omitempty"`
	UITLS      string     `json:"uiTls,omitempty"`
	UIEnabled  bool       `json:"uiEnabled,omitempty"`
	Routes     []WebRoute `json:"routes,omitempty"`
}

// SnapshotConfig is how many copies to keep. Zero values mean "not chosen
// yet"; the service then uses its defaults (5 copies, 30 days).
type SnapshotConfig struct {
	Configured bool   `json:"configured,omitempty"`
	KeepN      int    `json:"keepN"`
	KeepDays   int    `json:"keepDays"`
	Dir        string `json:"dir,omitempty"`
}

// Prefs holds Settings choices that are not part of a share or a container.
type Prefs struct {
	SessionHours int    `json:"sessionHours,omitempty"`
	RemoveImage  bool   `json:"removeImage,omitempty"`
	FeedURL      string `json:"feedURL,omitempty"`
	NTPServers   string `json:"ntpServers,omitempty"`
	Unattended   bool   `json:"unattended,omitempty"`
	UpdateCheck  bool   `json:"updateCheck,omitempty"`
	Firewall     bool   `json:"firewall,omitempty"`
	// Touched is set once Settings has saved update or firewall choices, so
	// those win over the values the wizard stored.
	Touched bool `json:"touched,omitempty"`
	// VMDomains and VMISOs override the VM Manager's disk and ISO folders.
	// Empty means the domains and isos shares under the shares root.
	VMDomains string `json:"vmDomains,omitempty"`
	VMISOs    string `json:"vmISOs,omitempty"`
}

// Wizard is how far first-start setup has got, and the choices made so far.
type Wizard struct {
	Done        bool     `json:"done"`
	Step        string   `json:"step"`
	Hostname    string   `json:"hostname,omitempty"`
	Timezone    string   `json:"timezone,omitempty"`
	NTP         string   `json:"ntp,omitempty"`
	NetMode     string   `json:"netMode,omitempty"`
	Samba       bool     `json:"samba"`
	NFS         bool     `json:"nfs"`
	Docker      bool     `json:"docker"`
	Libvirt     bool     `json:"libvirt"`
	ShareNames  []string `json:"shareNames,omitempty"`
	Unattended  bool     `json:"unattended"`
	Firewall    bool     `json:"firewall"`
	UpdateCheck bool     `json:"updateCheck"`
}

func (s State) clone() State {
	c := s
	if s.Admin != nil {
		a := *s.Admin
		c.Admin = &a
	}
	c.Sessions = append([]Session(nil), s.Sessions...)
	if s.APITokens != nil {
		c.APITokens = make([]APIToken, len(s.APITokens))
		for i, t := range s.APITokens {
			t.Scopes = append([]string(nil), t.Scopes...)
			if t.ExpiresAt != nil {
				exp := *t.ExpiresAt
				t.ExpiresAt = &exp
			}
			if t.LastUsedAt != nil {
				used := *t.LastUsedAt
				t.LastUsedAt = &used
			}
			c.APITokens[i] = t
		}
	}
	c.Audit = append([]AuditEvent(nil), s.Audit...)
	c.Shares = make([]Share, len(s.Shares))
	for i, sh := range s.Shares {
		sh.SMB.Users = append([]string(nil), sh.SMB.Users...)
		c.Shares[i] = sh
	}
	c.ShareUsers = append([]string(nil), s.ShareUsers...)
	if s.Wizard != nil {
		w := *s.Wizard
		w.ShareNames = append([]string(nil), s.Wizard.ShareNames...)
		c.Wizard = &w
	}
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
	c.Remote.Routes = append([]PublicRoute(nil), s.Remote.Routes...)
	c.Web.Routes = append([]WebRoute(nil), s.Web.Routes...)
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
