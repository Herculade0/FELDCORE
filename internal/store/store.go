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

// User is a FELDCORE login account. The authenticator secret is not stored
// here; TOTPEnabled only records that sign-in must include a code.
type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	TOTPEnabled  bool   `json:"totpEnabled,omitempty"`
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

// AuditEvent is one recorded action. Agent-token calls and blocks from the
// publish proxy share this log so a SIEM such as Wazuh can ingest one JSON
// array. A publish block sets Action to "publish.block", Result to "denied",
// and Detail["reason"] to rate, country, geo-unavailable, bot, empty-ua or
// scanner. IP, Host and Detail are omitted for ordinary token calls.
type AuditEvent struct {
	ID        string            `json:"id"`
	Time      time.Time         `json:"time"`
	TokenID   string            `json:"tokenId,omitempty"`
	TokenName string            `json:"tokenName"`
	Action    string            `json:"action"`
	Target    string            `json:"target,omitempty"`
	Result    string            `json:"result"`
	IP        string            `json:"ip,omitempty"`
	Host      string            `json:"host,omitempty"`
	Detail    map[string]string `json:"detail,omitempty"`
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
	// Publish is the one-switch exposure of a container. Access levels are
	// stored here; Citizen ID enforces them. Empty means nothing is published.
	Publish Publish `json:"publish,omitempty"`
	// Snapshots is retention and where container copies are kept.
	Snapshots SnapshotConfig `json:"snapshots,omitempty"`
	// VPN, Backup and Notify stay off until an administrator turns them on.
	// Passwords and keys for them are not stored here; they live in the
	// secrets box, encrypted.
	VPN    VPN    `json:"vpn,omitempty"`
	Backup Backup `json:"backup,omitempty"`
	Notify Notify `json:"notify,omitempty"`
}

// PublicRoute publishes one container through the Cloudflare Tunnel.
// Nothing is published until Enabled is set.
type PublicRoute struct {
	Container string `json:"container"`
	Hostname  string `json:"hostname"`
	Port      int    `json:"port"`
	Enabled   bool   `json:"enabled"`
	RecordID  string `json:"recordId,omitempty"`
	// Gateway means the tunnel dials FELDCORE's publish listener, which
	// applies protection and Citizen ID before proxying to the container.
	Gateway bool `json:"gateway,omitempty"`
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
	// Gateway means Caddy sends the request to FELDCORE's publish listener
	// instead of straight to the container. Caddy still terminates TLS.
	Gateway bool `json:"gateway,omitempty"`
}

// Publish is the one-switch publish feature and the protection policy that
// sits in front of every published app. Tokens and the GeoLite2 licence key
// are not stored here.
type Publish struct {
	// BaseDomain overrides the web-address domain for new names. Empty means
	// use Web.Domain, or home.arpa when that is empty too.
	BaseDomain string         `json:"baseDomain,omitempty"`
	Apps       []PublishedApp `json:"apps,omitempty"`
	Protect    ProtectPolicy  `json:"protect,omitempty"`
}

// PublishedApp is one container exposed as subdomain.<base domain>.
// Access is "public", "signed-in", "groups" or "admins". Groups is only
// meaningful when Access is "groups". Citizen ID enforces Access; this
// record only stores it.
type PublishedApp struct {
	Container string   `json:"container"`
	Subdomain string   `json:"subdomain"`
	Hostname  string   `json:"hostname"`
	Port      int      `json:"port"`
	Access    string   `json:"access"`
	Groups    []string `json:"groups,omitempty"`
	Enabled   bool     `json:"enabled"`
}

// ProtectPolicy is the per-request check in front of every published app.
// Zero means "not chosen yet": the proxy then uses its built-in defaults
// (a modest rate limit and bot blocking, no country list).
type ProtectPolicy struct {
	Configured bool `json:"configured,omitempty"`
	// RatePerSec is requests per second for one client IP on one app.
	RatePerSec float64 `json:"ratePerSec,omitempty"`
	RateBurst  int     `json:"rateBurst,omitempty"`
	// CountryMode is "off", "allow" or "block".
	CountryMode   string   `json:"countryMode,omitempty"`
	Countries     []string `json:"countries,omitempty"`
	GeoEnabled    bool     `json:"geoEnabled,omitempty"`
	BlockBots     bool     `json:"blockBots,omitempty"`
	BlockScanners bool     `json:"blockScanners,omitempty"`
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
	// AllowLANURLs lets a URL download reach private network addresses.
	// Localhost, link-local and cloud metadata addresses stay blocked.
	AllowLANURLs bool `json:"allowLANURLs,omitempty"`
}

// VPN is the WireGuard feature. Enabled is the master switch and defaults off.
type VPN struct {
	Enabled bool        `json:"enabled"`
	Tunnels []VPNTunnel `json:"tunnels,omitempty"`
}

// VPNTunnel is one WireGuard tunnel, without its private key.
type VPNTunnel struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Provider   string       `json:"provider"` // wireguard or proton
	Enabled    bool         `json:"enabled"`
	Address    string       `json:"address,omitempty"`
	DNS        string       `json:"dns,omitempty"`
	MTU        int          `json:"mtu,omitempty"`
	PeerKey    string       `json:"peerKey,omitempty"`
	Endpoint   string       `json:"endpoint,omitempty"`
	AllowedIPs string       `json:"allowedIPs,omitempty"`
	Keepalive  int          `json:"keepalive,omitempty"`
	Server     string       `json:"server,omitempty"`
	Country    string       `json:"country,omitempty"`
	NATPMP     bool         `json:"natpmp,omitempty"`
	HasKey     bool         `json:"hasKey"`
	HasPSK     bool         `json:"hasPsk"`
	Forwards   []VPNForward `json:"forwards,omitempty"`
}

// VPNForward is a host port published on the VPN gateway for one container.
type VPNForward struct {
	Container     string `json:"container"`
	HostIP        string `json:"hostIp,omitempty"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Proto         string `json:"proto"`
}

// Backup is the Proton Drive backup target. Credentials are not stored here.
type Backup struct {
	Enabled          bool      `json:"enabled"`
	RemotePath       string    `json:"remotePath,omitempty"`
	Schedule         string    `json:"schedule,omitempty"` // daily "HH:MM", empty is manual only
	Retention        int       `json:"retention,omitempty"`
	IncludeSnapshots bool      `json:"includeSnapshots"`
	IncludeAppdata   bool      `json:"includeAppdata"`
	Username         string    `json:"username,omitempty"`
	HasPassword      bool      `json:"hasPassword"`
	HasMailbox       bool      `json:"hasMailbox"`
	HasTwoFA         bool      `json:"hasTwoFA"`
	LastRun          time.Time `json:"lastRun,omitempty"`
	LastOK           bool      `json:"lastOk"`
	LastError        string    `json:"lastError,omitempty"`
	LastName         string    `json:"lastName,omitempty"`
}

// Notify is the email notification target. The SMTP secret is not stored here.
type Notify struct {
	Enabled         bool          `json:"enabled"`
	Mode            string        `json:"mode,omitempty"` // smtp, proton-bridge, proton-token
	Host            string        `json:"host,omitempty"`
	Port            int           `json:"port,omitempty"`
	Username        string        `json:"username,omitempty"`
	From            string        `json:"from,omitempty"`
	To              string        `json:"to,omitempty"`
	TLS             string        `json:"tls,omitempty"` // starttls, tls, none
	BridgeManaged   bool          `json:"bridgeManaged"`
	BridgeHost      string        `json:"bridgeHost,omitempty"`
	BridgePort      int           `json:"bridgePort,omitempty"`
	HasPassword     bool          `json:"hasPassword"`
	OnContainerStop bool          `json:"onContainerStop"`
	OnUpdate        bool          `json:"onUpdate"`
	NotifiedVersion string        `json:"notifiedVersion,omitempty"`
	Events          []NotifyEvent `json:"events,omitempty"`
}

// NotifyEvent is one notification FELDCORE tried to send.
type NotifyEvent struct {
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	Body  string    `json:"body"`
	At    time.Time `json:"at"`
	Sent  bool      `json:"sent"`
	Error string    `json:"error,omitempty"`
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

	// Locale is the LANG value chosen in setup, such as en_US.UTF-8.
	Locale string `json:"locale,omitempty"`
	// AdminUser is the name chosen for the administrator, once claimed.
	AdminUser string `json:"adminUser,omitempty"`
	// TOTP records that the administrator enrolled an authenticator.
	TOTP bool `json:"totp,omitempty"`

	// StorageMode is keep, disk, or pool. StorageDevice is the chosen
	// /dev path when a disk was selected. Nothing here is a passphrase.
	StorageMode   string `json:"storageMode,omitempty"`
	StorageDevice string `json:"storageDevice,omitempty"`
	StorageMount  string `json:"storageMount,omitempty"`
	StoragePool   string `json:"storagePool,omitempty"`

	// RemoteMode is none, cloudflare, or wireguard. Tokens are not stored.
	RemoteMode string `json:"remoteMode,omitempty"`
	// NotifyKind is none, email, or ntfy. NotifyTo is the mailbox or topic,
	// never a password or token.
	NotifyKind string `json:"notifyKind,omitempty"`
	NotifyTo   string `json:"notifyTo,omitempty"`
	NtfyURL    string `json:"ntfyUrl,omitempty"`
	// BackupKind is none or proton. The Proton password is not stored.
	BackupKind string `json:"backupKind,omitempty"`

	// Rerun is a settings replay of the steps that do not erase a disk.
	Rerun     bool   `json:"rerun,omitempty"`
	RerunStep string `json:"rerunStep,omitempty"`
	Skipped   []string `json:"skipped,omitempty"`
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
	if s.Audit != nil {
		c.Audit = make([]AuditEvent, len(s.Audit))
		for i, ev := range s.Audit {
			if ev.Detail != nil {
				d := make(map[string]string, len(ev.Detail))
				for k, v := range ev.Detail {
					d[k] = v
				}
				ev.Detail = d
			}
			c.Audit[i] = ev
		}
	}
	c.Shares = make([]Share, len(s.Shares))
	for i, sh := range s.Shares {
		sh.SMB.Users = append([]string(nil), sh.SMB.Users...)
		c.Shares[i] = sh
	}
	c.ShareUsers = append([]string(nil), s.ShareUsers...)
	if s.Wizard != nil {
		w := *s.Wizard
		w.ShareNames = append([]string(nil), s.Wizard.ShareNames...)
		w.Skipped = append([]string(nil), s.Wizard.Skipped...)
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
	c.Publish.Apps = make([]PublishedApp, len(s.Publish.Apps))
	for i, app := range s.Publish.Apps {
		app.Groups = append([]string(nil), app.Groups...)
		c.Publish.Apps[i] = app
	}
	c.Publish.Protect.Countries = append([]string(nil), s.Publish.Protect.Countries...)
	c.VPN.Tunnels = make([]VPNTunnel, len(s.VPN.Tunnels))
	for i, t := range s.VPN.Tunnels {
		t.Forwards = append([]VPNForward(nil), t.Forwards...)
		c.VPN.Tunnels[i] = t
	}
	c.Notify.Events = append([]NotifyEvent(nil), s.Notify.Events...)
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
