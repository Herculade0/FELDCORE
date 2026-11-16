// Package shares manages FELDCORE shares: folders under a shares root that can
// be exported over SMB (Samba) and NFS.
//
// The state file is the source of truth. After every change the manager
// re-renders the Samba and NFS configuration from that state and asks the
// services to reload, so the system always converges on what FELDCORE holds.
// FELDCORE only writes files it owns (an included Samba file and an
// exports.d drop-in); hand-written system configuration is left alone.
package shares

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// InputError is a validation failure whose message is safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func invalid(format string, a ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, a...)}
}

// OpError is an operational failure (a command or file write went wrong)
// whose message is safe to show the user.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

// ErrNotFound means the named share or user does not exist.
var ErrNotFound = errors.New("not found")

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	clientRe = regexp.MustCompile(`^[A-Za-z0-9*?][A-Za-z0-9.*?:/_-]*$`)
	reserved = map[string]bool{
		"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true,
	}
)

// Paths are the filesystem locations the manager works with.
type Paths struct {
	SharesRoot   string
	SambaConf    string
	SambaInclude string
	ExportsFile  string
}

// identity is the unix account that owns share contents. Every SMB and NFS
// client is mapped onto it, so files stay accessible whichever way they
// arrive.
type identity struct {
	User, Group string
	UID, GID    int
}

// Manager owns share definitions and applies them to the system.
type Manager struct {
	st  *store.Store
	p   Paths
	id  identity
	log *slog.Logger

	mu sync.Mutex // serialises mutations and applies

	statusMu sync.RWMutex
	smbErr   string
	nfsErr   string

	// asRoot reports whether this process may create share folders.
	// Tests replace it; production uses the real effective uid.
	asRoot func() bool
}

// New returns a Manager.
func New(st *store.Store, p Paths, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{st: st, p: p, id: lookupIdentity(), log: log, asRoot: func() bool { return os.Geteuid() == 0 }}
}

func lookupIdentity() identity {
	id := identity{User: "nobody", Group: "nogroup", UID: 65534, GID: 65534}
	if u, err := user.Lookup("nobody"); err == nil {
		if n, err := strconv.Atoi(u.Uid); err == nil {
			id.UID = n
		}
	}
	for _, name := range []string{"nogroup", "nobody"} {
		if g, err := user.LookupGroup(name); err == nil {
			if n, err := strconv.Atoi(g.Gid); err == nil {
				id.Group, id.GID = name, n
				break
			}
		}
	}
	return id
}

// Input is what a client submits to create or update a share.
type Input struct {
	Name    string    `json:"name"`
	Comment string    `json:"comment"`
	SMB     store.SMB `json:"smb"`
	NFS     store.NFS `json:"nfs"`
}

// ProtoStatus is the live state of one protocol on one share.
type ProtoStatus struct {
	State   string `json:"state"` // "off", "active" or "error"
	Message string `json:"message,omitempty"`
}

// View is a share plus derived, read-only details.
type View struct {
	store.Share
	Path      string      `json:"path"`
	SMBStatus ProtoStatus `json:"smbStatus"`
	NFSStatus ProtoStatus `json:"nfsStatus"`
}

// Overview is everything the Shares page needs in one response.
type Overview struct {
	Shares           []View   `json:"shares"`
	Root             string   `json:"root"`
	Users            []string `json:"users"`
	Services         Services `json:"services"`
	SuggestedClients string   `json:"suggestedClients"`
}

func (m *Manager) dir(name string) string { return filepath.Join(m.p.SharesRoot, name) }

// Folder is a share's name and where it lives on disk.
type Folder struct {
	Name string
	Path string
}

// Folders lists every share's folder, sorted by name.
func (m *Manager) Folders() []Folder {
	shares := m.st.Snapshot().Shares
	out := make([]Folder, 0, len(shares))
	for _, sh := range shares {
		out = append(out, Folder{Name: sh.Name, Path: m.dir(sh.Name)})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Overview returns all shares with their current status.
func (m *Manager) Overview(ctx context.Context) Overview {
	st := m.st.Snapshot()
	svc := m.Services(ctx)

	views := make([]View, 0, len(st.Shares))
	for _, sh := range st.Shares {
		views = append(views, m.view(sh, svc))
	}
	sort.Slice(views, func(i, j int) bool {
		return strings.ToLower(views[i].Name) < strings.ToLower(views[j].Name)
	})
	users := append([]string{}, st.ShareUsers...)
	sort.Strings(users)
	return Overview{
		Shares:           views,
		Root:             m.p.SharesRoot,
		Users:            users,
		Services:         svc,
		SuggestedClients: suggestedClients(),
	}
}

func (m *Manager) view(sh store.Share, svc Services) View {
	if sh.SMB.Users == nil {
		sh.SMB.Users = []string{}
	}
	return View{
		Share:     sh,
		Path:      m.dir(sh.Name),
		SMBStatus: protoStatus(sh.SMB.Enabled, svc.SMB, "Samba"),
		NFSStatus: protoStatus(sh.NFS.Enabled, svc.NFS, "The NFS server"),
	}
}

func protoStatus(enabled bool, svc ServiceStatus, label string) ProtoStatus {
	switch {
	case !enabled:
		return ProtoStatus{State: "off"}
	case svc.Error != "":
		return ProtoStatus{State: "error", Message: svc.Error}
	case !svc.Installed:
		return ProtoStatus{State: "error", Message: label + " is not installed."}
	case !svc.Active:
		return ProtoStatus{State: "error", Message: label + " is not running."}
	}
	return ProtoStatus{State: "active"}
}

// normalise validates in and returns the share fields to store.
func normalise(in Input, shareUsers []string) (store.Share, error) {
	sh := store.Share{Comment: cleanComment(in.Comment)}

	// SMB
	access := in.SMB.Access
	if access == "" {
		access = store.SMBPublic
	}
	if access != store.SMBPublic && access != store.SMBReadOnly && access != store.SMBPrivate {
		return sh, invalid("Unknown SMB access mode %q.", access)
	}
	sh.SMB = store.SMB{Enabled: in.SMB.Enabled, Access: access, Users: []string{}}
	if access == store.SMBPrivate {
		known := map[string]bool{}
		for _, u := range shareUsers {
			known[u] = true
		}
		seen := map[string]bool{}
		for _, u := range in.SMB.Users {
			if !known[u] {
				return sh, invalid("%q is not a share user.", u)
			}
			if !seen[u] {
				seen[u] = true
				sh.SMB.Users = append(sh.SMB.Users, u)
			}
		}
		sort.Strings(sh.SMB.Users)
		if in.SMB.Enabled && len(sh.SMB.Users) == 0 {
			return sh, invalid("Choose at least one user for a private share.")
		}
	}

	// NFS
	clients := strings.Fields(strings.ReplaceAll(in.NFS.Clients, ",", " "))
	for _, c := range clients {
		if !clientRe.MatchString(c) {
			return sh, invalid("%q is not a valid NFS client. Use an address, a network like 192.168.0.0/24, a hostname, or *.", c)
		}
	}
	if in.NFS.Enabled && len(clients) == 0 {
		return sh, invalid("Say which computers may connect over NFS, for example 192.168.0.0/24.")
	}
	sh.NFS = store.NFS{
		Enabled:  in.NFS.Enabled,
		Clients:  strings.Join(clients, " "),
		ReadOnly: in.NFS.ReadOnly,
	}
	return sh, nil
}

// cleanComment makes free text safe to embed in smb.conf: one line, no
// control characters, no backslashes (a trailing one continues the line).
func cleanComment(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 100 {
		s = strings.TrimSpace(string(r[:100]))
	}
	return s
}

// Create adds a share, makes its folder, and applies the configuration.
func (m *Manager) Create(ctx context.Context, in Input) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := strings.TrimSpace(in.Name)
	if !nameRe.MatchString(name) {
		return View{}, invalid("Share names use letters, numbers, hyphens and underscores, up to 32 characters.")
	}
	if reserved[strings.ToLower(name)] {
		return View{}, invalid("%q is a reserved name. Choose another.", name)
	}

	var created store.Share
	err := m.st.Update(func(s *store.State) error {
		for _, ex := range s.Shares {
			if strings.EqualFold(ex.Name, name) {
				return invalid("A share named %q already exists.", ex.Name)
			}
		}
		sh, err := normalise(in, s.ShareUsers)
		if err != nil {
			return err
		}
		sh.Name = name
		sh.CreatedAt = time.Now().UTC()
		if err := m.makeDir(name); err != nil {
			return err
		}
		s.Shares = append(s.Shares, sh)
		created = sh
		return nil
	})
	if err != nil {
		return View{}, err
	}
	m.log.Info("share created", "name", name)
	m.applyLocked(ctx)
	return m.view(created, m.Services(ctx)), nil
}

// Update changes a share's settings. The name is fixed.
func (m *Manager) Update(ctx context.Context, name string, in Input) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var updated store.Share
	err := m.st.Update(func(s *store.State) error {
		for i, ex := range s.Shares {
			if ex.Name != name {
				continue
			}
			sh, err := normalise(in, s.ShareUsers)
			if err != nil {
				return err
			}
			sh.Name, sh.CreatedAt = ex.Name, ex.CreatedAt
			s.Shares[i] = sh
			updated = sh
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return View{}, err
	}
	m.log.Info("share updated", "name", name)
	m.applyLocked(ctx)
	return m.view(updated, m.Services(ctx)), nil
}

// Delete stops sharing a folder. The folder is removed only if it is empty;
// folderKept reports when data was left in place.
func (m *Manager) Delete(ctx context.Context, name string) (folderKept bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	err = m.st.Update(func(s *store.State) error {
		for i, ex := range s.Shares {
			if ex.Name == name {
				s.Shares = append(s.Shares[:i], s.Shares[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
	if err != nil {
		return false, err
	}
	m.log.Info("share deleted", "name", name)
	m.applyLocked(ctx)

	// os.Remove refuses to delete a non-empty directory, which is exactly
	// the safety we want: never destroy data as a side effect.
	if rmErr := os.Remove(m.dir(name)); rmErr != nil && fileExists(m.dir(name)) {
		return true, nil
	}
	return false, nil
}

// makeDir creates the folder for a share (adopting it if it already exists).
func (m *Manager) makeDir(name string) error {
	if err := os.MkdirAll(m.p.SharesRoot, 0o755); err != nil {
		return &OpError{fmt.Sprintf("FELDCORE could not create %s: %v", m.p.SharesRoot, osReason(err))}
	}
	dir := m.dir(name)
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return &OpError{fmt.Sprintf("%s exists and is not a folder.", dir)}
		}
		return nil // adopt the existing folder as-is
	}
	if err := os.Mkdir(dir, 0o775); err != nil {
		return &OpError{fmt.Sprintf("FELDCORE could not create %s: %v", dir, osReason(err))}
	}
	// Mkdir is subject to the umask; set the mode explicitly.
	_ = os.Chmod(dir, 0o775)
	if os.Geteuid() == 0 {
		_ = os.Chown(dir, m.id.UID, m.id.GID)
	}
	return nil
}

func osReason(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "permission denied (is FELDCORE running as root?)"
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// Apply re-renders all service configuration from the stored state.
func (m *Manager) Apply(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyLocked(ctx)
}

func (m *Manager) applyLocked(ctx context.Context) {
	// Finish applying even if the HTTP request that triggered it goes away.
	ctx = context.WithoutCancel(ctx)
	shares := m.st.Snapshot().Shares

	smbErr := m.applySMB(ctx, shares)
	nfsErr := m.applyNFS(ctx, shares)

	m.statusMu.Lock()
	m.smbErr, m.nfsErr = errText(smbErr), errText(nfsErr)
	m.statusMu.Unlock()

	if smbErr != nil {
		m.log.Warn("samba configuration not applied", "err", smbErr)
	}
	if nfsErr != nil {
		m.log.Warn("nfs configuration not applied", "err", nfsErr)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// suggestedClients guesses the local network (e.g. "192.168.0.0/24") to
// pre-fill the NFS "allowed clients" field.
func suggestedClients() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if hasAnyPrefix(ifc.Name, "docker", "veth", "br-", "virbr") {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			return fmt.Sprintf("%s/%d", ip4.Mask(ipnet.Mask), ones)
		}
	}
	return ""
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
