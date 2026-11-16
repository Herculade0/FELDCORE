// Package setup is the first-start wizard: claim the server, then hostname,
// network, storage, packages, default shares, firewall and update checks.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/shares"
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

	mu          sync.Mutex
	pkgLog      strings.Builder
	pkgRunning  bool
	pkgErr      string
	netToken    string
	netURL      string
	netDeadline time.Time
	netOK       bool
	rollback    func()
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
	}
	s.lim.fails = map[string][]time.Time{}
	s.recoverNet()
	return s
}

// Pending reports whether a signed-in admin still has the wizard to finish.
func (s *Service) Pending() bool {
	st := s.st.Snapshot()
	return st.Admin != nil && st.Wizard != nil && !st.Wizard.Done
}

// Begin starts the wizard after the admin account is claimed. Skip marks it
// finished immediately for a scripted install.
func (s *Service) Begin(skip bool) error {
	return s.st.Update(func(st *store.State) error {
		if st.Wizard != nil {
			return nil
		}
		if skip {
			st.Wizard = &store.Wizard{Done: true, Step: "summary"}
			return nil
		}
		st.Wizard = &store.Wizard{
			Step:       "host",
			Samba:      true,
			NFS:        true,
			Docker:     true,
			Libvirt:    true, // the software step turns this off when the CPU cannot virtualise
			Unattended: true,
			ShareNames: []string{"appdata", "system", "domains", "isos"},
		}
		return nil
	})
}

// View is the wizard page.
type View struct {
	Done    bool     `json:"done"`
	Step    string   `json:"step"`
	Steps   []string `json:"steps"`
	Preview []string `json:"preview,omitempty"`
	Warning string   `json:"warning,omitempty"`
	Error   string   `json:"error,omitempty"`

	Hostname string   `json:"hostname"`
	Timezone string   `json:"timezone"`
	Zones    []string `json:"zones,omitempty"`
	NTP      string   `json:"ntp"`

	Iface    string   `json:"iface"`
	IP       string   `json:"ip"`
	Gateway  string   `json:"gateway"`
	DNS      []string `json:"dns"`
	NetMode  string   `json:"netMode"`
	Backend  string   `json:"backend"`
	NewURL   string   `json:"newUrl,omitempty"`
	NetToken string   `json:"netToken,omitempty"`
	NetWait  bool     `json:"netWait"`

	Mounts     []Mount `json:"mounts"`
	SharesRoot string  `json:"sharesRoot"`
	OnOSDisk   bool    `json:"onOsDisk"`

	Virt     bool   `json:"virt"`
	Samba    bool   `json:"samba"`
	NFS      bool   `json:"nfs"`
	Docker   bool   `json:"docker"`
	Libvirt  bool   `json:"libvirt"`
	PkgLog   string `json:"pkgLog,omitempty"`
	PkgRun   bool   `json:"pkgRunning"`
	PkgError string `json:"pkgError,omitempty"`

	ShareNames  []string `json:"shareNames"`
	SSHPorts    []int    `json:"sshPorts"`
	ListenPort  int      `json:"listenPort"`
	Unattended  bool     `json:"unattended"`
	Firewall    bool     `json:"firewall"`
	UpdateCheck bool     `json:"updateCheck"`
	Summary     []string `json:"summary,omitempty"`
}

// Request is one wizard submission. Confirm false only previews.
type Request struct {
	Step        string   `json:"step"`
	Confirm     bool     `json:"confirm"`
	Back        bool     `json:"back"`
	Hostname    string   `json:"hostname"`
	Timezone    string   `json:"timezone"`
	NTP         string   `json:"ntp"`
	NetMode     string   `json:"netMode"`
	Iface       string   `json:"iface"`
	Address     string   `json:"address"`
	Gateway     string   `json:"gateway"`
	DNS         string   `json:"dns"`
	SharesRoot  string   `json:"sharesRoot"`
	Samba       bool     `json:"samba"`
	NFS         bool     `json:"nfs"`
	Docker      bool     `json:"docker"`
	Libvirt     bool     `json:"libvirt"`
	ShareNames  []string `json:"shareNames"`
	Unattended  bool     `json:"unattended"`
	Firewall    bool     `json:"firewall"`
	UpdateCheck bool     `json:"updateCheck"`
}

// Status is the current wizard page, including a preview when req is set
// and Confirm is false.
func (s *Service) Status(ctx context.Context, req *Request) (View, error) {
	if req != nil && !req.Confirm && !req.Back {
		return s.preview(ctx, *req)
	}
	return s.view(ctx, nil, ""), nil
}

// Apply previews or performs the current step.
func (s *Service) Apply(ctx context.Context, req Request) (View, error) {
	w := s.wizard()
	if w.Done {
		return s.view(ctx, nil, ""), nil
	}
	if req.Back {
		w.Step = prevStep(w.Step)
		if err := s.saveWizard(w); err != nil {
			return View{}, err
		}
		return s.view(ctx, nil, ""), nil
	}
	if req.Step == "" {
		req.Step = w.Step
	}
	if req.Step != w.Step {
		return View{}, &InputError{"That step is not the one in progress. Continue from where you left off."}
	}
	if !req.Confirm {
		return s.preview(ctx, req)
	}
	if !s.root() {
		return View{}, &OpError{"FELDCORE must run as root to change this server. The preview above is what it would do."}
	}
	if err := s.apply(ctx, &w, req); err != nil {
		return View{}, err
	}
	return s.view(ctx, nil, ""), nil
}

func (s *Service) preview(ctx context.Context, req Request) (View, error) {
	lines, warn, err := s.plan(ctx, req)
	if err != nil {
		return View{}, &InputError{err.Error()}
	}
	v := s.view(ctx, lines, warn)
	if req.Step == "network" && req.NetMode == "static" {
		v.NewURL = NewURL(req.Address, s.listenPort)
	}
	return v, nil
}

func (s *Service) plan(ctx context.Context, req Request) (lines []string, warn string, err error) {
	switch req.Step {
	case "host":
		lines, err = HostPlan(req.Hostname, req.Timezone, req.NTP)
	case "network":
		if err = ValidateNet(NetInput{req.NetMode, req.Iface, req.Address, req.Gateway, req.DNS}); err != nil {
			return nil, "", err
		}
		if req.NetMode == "" || req.NetMode == "dhcp" {
			lines = []string{"Stay on DHCP. The address of this server will not change."}
		} else {
			lines = []string{
				"Give " + req.Iface + " the address " + req.Address + ".",
				"Gateway " + req.Gateway + ", DNS " + req.DNS + ".",
				"FELDCORE will move to " + NewURL(req.Address, s.listenPort) + ".",
				"If that page does not confirm within 90 seconds, the previous network settings are put back.",
			}
		}
	case "storage":
		if err = s.checkRoot(req.SharesRoot); err != nil {
			return nil, "", err
		}
		mounts := s.mounts(ctx)
		lines = []string{"Store shares under " + req.SharesRoot + ". Nothing will be formatted."}
		if OnOSDisk(req.SharesRoot, mounts) {
			warn = "That folder is on the operating-system disk. Filling it up can stop the server booting. A separate disk is safer."
		}
	case "packages":
		lines = s.packagePlan(ctx, req)
	case "shares":
		if len(req.ShareNames) == 0 {
			lines = []string{"No default shares will be created."}
		} else {
			lines = []string{"Create these shares, with SMB and NFS left off: " + strings.Join(req.ShareNames, ", ") + "."}
		}
	case "security":
		ports := s.sshPorts(ctx)
		lines = []string{}
		if req.Unattended {
			lines = append(lines, "Install unattended-upgrades so security updates install themselves.")
		} else {
			lines = append(lines, "Leave automatic security updates off.")
		}
		if req.Firewall {
			lines = append(lines, fmt.Sprintf("Turn on ufw. Allow TCP %d (FELDCORE) and SSH on %s. Do not change sshd.", s.listenPort, joinPorts(ports)))
			if req.Samba {
				lines = append(lines, "Also allow Samba, because it was selected.")
			}
			if req.NFS {
				lines = append(lines, "Also allow NFS, because it was selected.")
			}
		} else {
			lines = append(lines, "Do not change the firewall.")
		}
	case "updates":
		if req.UpdateCheck {
			lines = []string{"FELDCORE will check GitHub for a newer release and tell you. It will not install one by itself."}
		} else {
			lines = []string{"FELDCORE will not check for its own updates."}
		}
	case "summary":
		lines = []string{"Finish setup and open the dashboard."}
	default:
		err = fmt.Errorf("unknown step")
	}
	return lines, warn, err
}

func (s *Service) apply(ctx context.Context, w *store.Wizard, req Request) error {
	switch req.Step {
	case "host":
		if _, _, err := s.plan(ctx, req); err != nil {
			return &InputError{err.Error()}
		}
		if err := s.applyHost(ctx, req); err != nil {
			return err
		}
		w.Hostname, w.Timezone, w.NTP = req.Hostname, req.Timezone, req.NTP
	case "network":
		if err := ValidateNet(NetInput{req.NetMode, req.Iface, req.Address, req.Gateway, req.DNS}); err != nil {
			return &InputError{err.Error()}
		}
		w.NetMode = req.NetMode
		if req.NetMode == "static" {
			return s.scheduleNet(ctx, w, req)
		}
	case "storage":
		if err := s.checkRoot(req.SharesRoot); err != nil {
			return &InputError{err.Error()}
		}
		if err := s.st.Update(func(st *store.State) error {
			st.SharesRoot = req.SharesRoot
			return nil
		}); err != nil {
			return err
		}
		if s.shares != nil {
			s.shares.SetRoot(req.SharesRoot)
		}
	case "packages":
		w.Samba, w.NFS, w.Docker, w.Libvirt = req.Samba, req.NFS, req.Docker, req.Libvirt && s.virt()
		return s.startPackages(context.WithoutCancel(ctx), w, req)
	case "shares":
		w.ShareNames = filterShareNames(req.ShareNames)
		if s.shares != nil {
			s.shares.EnsureShares(ctx, w.ShareNames)
		}
	case "security":
		w.Unattended, w.Firewall = req.Unattended, req.Firewall
		if err := s.applySecurity(ctx, req); err != nil {
			return err
		}
	case "updates":
		w.UpdateCheck = req.UpdateCheck
	case "summary":
		w.Done = true
	default:
		return &InputError{"Unknown step."}
	}
	if req.Step != "summary" && req.Step != "network" {
		w.Step = nextStep(w.Step)
	}
	if req.Step == "network" && req.NetMode != "static" {
		w.Step = nextStep(w.Step)
	}
	return s.saveWizard(*w)
}

func (s *Service) wizard() store.Wizard {
	st := s.st.Snapshot()
	if st.Wizard == nil {
		return store.Wizard{Step: "host", Samba: true, NFS: true, Docker: true, Libvirt: true, Unattended: true, ShareNames: []string{"appdata", "system", "domains", "isos"}}
	}
	w := *st.Wizard
	if w.Step == "" {
		w.Step = "host"
	}
	return w
}

func (s *Service) saveWizard(w store.Wizard) error {
	return s.st.Update(func(st *store.State) error {
		cp := w
		cp.ShareNames = append([]string(nil), w.ShareNames...)
		st.Wizard = &cp
		return nil
	})
}

func (s *Service) view(ctx context.Context, preview []string, warn string) View {
	w := s.wizard()
	mounts := s.mounts(ctx)
	root := s.st.Snapshot().SharesRoot
	if root == "" {
		if s.shares != nil {
			root = s.shares.Root()
		}
	}
	s.mu.Lock()
	log, running, pkgErr := s.pkgLog.String(), s.pkgRunning, s.pkgErr
	wait := s.netToken != "" && !s.netOK && s.now().Before(s.netDeadline)
	token, newURL := "", ""
	if wait {
		token, newURL = s.netToken, s.netURL
	}
	s.mu.Unlock()
	names := w.ShareNames
	if names == nil {
		names = []string{"appdata", "system", "domains", "isos"}
	}
	v := View{
		Done: w.Done, Step: w.Step, Steps: Steps, Preview: preview, Warning: warn,
		Hostname: first(w.Hostname, hostname()), Timezone: first(w.Timezone, currentTZ()), Zones: timezones(), NTP: ntpOr(w.NTP),
		Mounts: mounts, SharesRoot: root, OnOSDisk: OnOSDisk(root, mounts),
		Virt: s.virt(), Samba: w.Samba, NFS: w.NFS, Docker: w.Docker, Libvirt: w.Libvirt,
		PkgLog: log, PkgRun: running, PkgError: pkgErr,
		ShareNames: names, SSHPorts: s.sshPorts(ctx), ListenPort: s.listenPort,
		Unattended: w.Unattended, Firewall: w.Firewall, UpdateCheck: w.UpdateCheck,
		NewURL: newURL, NetToken: token, NetWait: wait,
	}
	s.fillNet(&v)
	if w.Done || w.Step == "summary" {
		v.Summary = s.summary(w, v)
	}
	return v
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

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
