// Package setup is the guided setup wizard: claim the server, then host,
// network, administrator, storage, remote access, notifications and backups.
package setup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
)

// InputError is a validation failure safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is an operational failure safe to show the user.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

// Commander runs a system command. Tests substitute a fake.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Service runs the wizard.
type Service struct {
	st         *store.Store
	shares     *shares.Manager
	log        *slog.Logger
	dataDir    string
	listenPort int
	cmd        Commander
	root       func() bool
	now        func() time.Time
	releaseURL string

	lim codeLimiter

	mu         sync.Mutex
	pkgLog     strings.Builder
	pkgRunning bool
	pkgErr     string

	skip     bool
	accounts *auth.Service
	auditor  Auditor
	disks    storage.Volume
	tunnel   TunnelSaver
	vpn      VPNSaver
	mail     MailSaver
	drive    DriveSaver
	box      secretBox
	check    func(context.Context, []string) ConnStatus

	// etcRoot and efiRoot redirect /etc and /efi in tests. Empty uses the real paths.
	etcRoot string
	efiRoot string
	// feat overrides package detection for the answer file. Nil asks the host.
	feat installFeat
	// unixPass sets a Unix password without putting it on a command line.
	unixPass func(context.Context, string, string) error
	// lookupIface supplies the static-address interface when the file omits one.
	lookupIface func(context.Context) string
}

// Deps wires the wizard to the rest of the daemon.
type Deps struct {
	Store      *store.Store
	Shares     *shares.Manager
	DataDir    string
	ListenPort int
	Log        *slog.Logger
	Cmd        Commander
	Root       func() bool
	Skip       bool
}

// New returns a wizard service.
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Cmd == nil {
		d.Cmd = realCmd{}
	}
	if d.Root == nil {
		d.Root = realRoot
	}
	if d.ListenPort == 0 {
		d.ListenPort = 8787
	}
	s := &Service{
		st: d.Store, shares: d.Shares, log: d.Log, dataDir: d.DataDir,
		listenPort: d.ListenPort, cmd: d.Cmd, root: d.Root, now: time.Now,
		releaseURL: "https://api.github.com/repos/Herculade0/FELDCORE/releases/latest",
		skip:       d.Skip,
	}
	s.lim.fails = map[string][]time.Time{}
	return s
}

// Pending reports whether a signed-in admin still has the wizard to finish.
func (s *Service) Pending() bool {
	st := s.st.Snapshot()
	return st.Admin != nil && st.Wizard != nil && !st.Wizard.Done
}

// Begin starts the wizard. Skip marks it finished immediately for a scripted
// install, even when a browser visit already opened the first page.
func (s *Service) Begin(skip bool) error {
	return s.st.Update(func(st *store.State) error {
		if skip {
			done := store.Wizard{Done: true, Step: "summary", Samba: true, NFS: true, Docker: true, Libvirt: true, Unattended: true, ShareNames: []string{"appdata", "system", "domains", "isos"}}
			if st.Wizard != nil {
				done.Hostname, done.Timezone, done.Locale = st.Wizard.Hostname, st.Wizard.Timezone, st.Wizard.Locale
				done.NetMode = st.Wizard.NetMode
			}
			st.Wizard = &done
			return nil
		}
		if st.Wizard != nil {
			return nil
		}
		st.Wizard = &store.Wizard{
			Step:       "welcome",
			Samba:      true,
			NFS:        true,
			Docker:     true,
			Libvirt:    true,
			Unattended: true,
			ShareNames: []string{"appdata", "system", "domains", "isos"},
		}
		return nil
	})
}

func (s *Service) ensureStarted() error {
	if s.st.Snapshot().Wizard != nil {
		return nil
	}
	return s.Begin(s.skip)
}

func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func ntpOr(v string) string {
	if v == "" {
		return "systemd-timesyncd"
	}
	return v
}

func nextStep(cur string) string {
	for i, s := range Steps {
		if s == cur && i+1 < len(Steps) {
			return Steps[i+1]
		}
	}
	return cur
}

func prevStep(cur string) string {
	for i, s := range Steps {
		if s == cur && i > 0 {
			return Steps[i-1]
		}
	}
	return cur
}

func filterShareNames(names []string) []string {
	allow := map[string]bool{"appdata": true, "system": true, "domains": true, "isos": true}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if allow[n] && !seen[n] {
			out = append(out, n)
			seen[n] = true
		}
	}
	return out
}

func joinPorts(ports []int) string {
	var b strings.Builder
	for i, p := range ports {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%d", p))
	}
	return b.String()
}
