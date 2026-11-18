package setup

import (
	"context"
	"errors"
	"strings"

	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
)

// View is the wizard page.
type View struct {
	Done      bool     `json:"done"`
	Step      string   `json:"step"`
	Steps     []string `json:"steps"`
	Preview   []string `json:"preview,omitempty"`
	Warning   string   `json:"warning,omitempty"`
	Error     string   `json:"error,omitempty"`
	Optional  bool     `json:"optional"`
	Rerun     bool     `json:"rerun"`
	NeedsCode bool     `json:"needsCode"`

	Hostname string   `json:"hostname"`
	Timezone string   `json:"timezone"`
	Locale   string   `json:"locale"`
	Locales  []string `json:"locales,omitempty"`
	Zones    []string `json:"zones,omitempty"`
	NTP      string   `json:"ntp"`

	Iface         string              `json:"iface"`
	IP            string              `json:"ip"`
	Gateway       string              `json:"gateway"`
	DNS           []string            `json:"dns"`
	NetMode       string              `json:"netMode"`
	Backend       string              `json:"backend"`
	NewURL        string              `json:"newUrl,omitempty"`
	Mounts        []Mount             `json:"mounts"`
	Disks         []storage.SetupDisk `json:"disks,omitempty"`
	SharesRoot    string              `json:"sharesRoot"`
	OnOSDisk      bool                `json:"onOsDisk"`
	Destructive   bool                `json:"destructive,omitempty"`
	ConfirmPhrase string              `json:"confirmPhrase,omitempty"`
	Connectivity  *ConnStatus         `json:"connectivity,omitempty"`

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

	AdminUser     string   `json:"adminUser,omitempty"`
	AdminExists   bool     `json:"adminExists"`
	TOTPEnabled   bool     `json:"totpEnabled"`
	TOTPSecret    string   `json:"totpSecret,omitempty"`
	TOTPURL       string   `json:"totpUrl,omitempty"`
	RecoveryCodes []string `json:"recoveryCodes,omitempty"`
	RemoteMode    string   `json:"remoteMode,omitempty"`
	NotifyKind    string   `json:"notifyKind,omitempty"`
	BackupKind    string   `json:"backupKind,omitempty"`
	StorageMode   string   `json:"storageMode,omitempty"`
	StorageDevice string   `json:"storageDevice,omitempty"`
	StoragePool   string   `json:"storagePool,omitempty"`
	Summary       []string `json:"summary,omitempty"`
}

// Request is one wizard submission. Confirm false only previews.
// Secrets on this struct (passwords, tokens, keys) are never stored.
type Request struct {
	Step         string `json:"step"`
	Confirm      bool   `json:"confirm"`
	Back         bool   `json:"back"`
	Skip         bool   `json:"skip"`
	Check        bool   `json:"check"`
	Action       string `json:"action"`
	BeginRerun   bool   `json:"beginRerun"`
	DiscardRerun bool   `json:"discardRerun"`
	Code         string `json:"code"`

	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
	NTP      string `json:"ntp"`

	NetMode string `json:"netMode"`
	Iface   string `json:"iface"`
	Address string `json:"address"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"`

	SharesRoot     string `json:"sharesRoot"`
	StorageMode    string `json:"storageMode"`
	StorageDevice  string `json:"storageDevice"`
	StorageMount   string `json:"storageMount"`
	StorageFormat  bool   `json:"storageFormat"`
	StorageFS      string `json:"storageFs"`
	StoragePool    string `json:"storagePool"`
	StorageConfirm string `json:"storageConfirm"`

	Username        string `json:"username"`
	Password        string `json:"password"`
	Password2       string `json:"password2"`
	CurrentPassword string `json:"currentPassword"`
	TOTPCode        string `json:"totpCode"`
	CurrentTOTP     string `json:"currentTotp"`
	SkipTOTP        bool   `json:"skipTotp"`
	DisableTOTP     bool   `json:"disableTotp"`

	RemoteMode string `json:"remoteMode"`
	CFMode     string `json:"cfMode"`
	CFToken    string `json:"cfToken"`
	CFAPI      string `json:"cfApi"`
	WGConf     string `json:"wgConf"`

	NotifyKind string `json:"notifyKind"`
	MailHost   string `json:"mailHost"`
	MailPort   int    `json:"mailPort"`
	MailUser   string `json:"mailUser"`
	MailPass   string `json:"mailPass"`
	MailFrom   string `json:"mailFrom"`
	MailTo     string `json:"mailTo"`
	MailTLS    string `json:"mailTls"`
	NtfyURL    string `json:"ntfyUrl"`
	NtfyTopic  string `json:"ntfyTopic"`
	NtfyToken  string `json:"ntfyToken"`

	BackupKind     string `json:"backupKind"`
	BackupUser     string `json:"backupUser"`
	BackupPass     string `json:"backupPass"`
	BackupSchedule string `json:"backupSchedule"`

	// Kept so a client that still sends the older software step is not
	// rejected as unknown JSON. The guided setup no longer applies them.
	Samba       bool     `json:"samba"`
	NFS         bool     `json:"nfs"`
	Docker      bool     `json:"docker"`
	Libvirt     bool     `json:"libvirt"`
	ShareNames  []string `json:"shareNames"`
	Unattended  bool     `json:"unattended"`
	Firewall    bool     `json:"firewall"`
	UpdateCheck bool     `json:"updateCheck"`

	Client string `json:"-"`
	Actor  string `json:"-"`
}

// Status is the current wizard page.
func (s *Service) Status(ctx context.Context, req *Request) (View, error) {
	if err := s.ensureStarted(); err != nil {
		return View{}, err
	}
	if req != nil && !req.Confirm && !req.Back && !req.Skip {
		if err := s.authorize(*req); err != nil {
			return View{}, err
		}
		v, _, err := s.preview(ctx, *req)
		return v, err
	}
	return s.view(ctx, nil, ""), nil
}

// Apply previews or performs the current step. Grant is set when the admin
// step creates the session.
func (s *Service) Apply(ctx context.Context, req Request) (View, *Grant, error) {
	if err := s.ensureStarted(); err != nil {
		return View{}, nil, err
	}
	if err := s.authorize(req); err != nil {
		return View{}, nil, err
	}
	w := s.wizard()
	if req.BeginRerun {
		if s.st.Snapshot().Admin == nil {
			return View{}, nil, &InputError{"Sign in before running setup again."}
		}
		if !w.Rerun {
			w.Rerun = true
			w.RerunStep = "host"
			if err := s.saveWizard(w); err != nil {
				return View{}, nil, err
			}
			s.audit(req.Actor, "wizard.rerun", "start", "ok")
		}
		return s.view(ctx, nil, ""), nil, nil
	}
	if req.DiscardRerun {
		w.Rerun = false
		w.RerunStep = ""
		if err := s.saveWizard(w); err != nil {
			return View{}, nil, err
		}
		s.audit(req.Actor, "wizard.rerun", "discard", "ok")
		return s.view(ctx, nil, ""), nil, nil
	}
	if !w.Rerun && w.Done {
		return s.view(ctx, nil, ""), nil, nil
	}
	if req.Action == "totp" {
		secret, uri, err := s.beginTOTP(req)
		if err != nil {
			return View{}, nil, err
		}
		v := s.view(ctx, []string{"An authenticator secret was created. It is not enabled until a code from the app is accepted."}, "")
		v.TOTPSecret = secret
		v.TOTPURL = uri
		s.audit(req.Actor, "wizard.totp", "begin", "ok")
		return v, nil, nil
	}
	cur := cursor(w)
	if req.Back {
		retreat(&w)
		if err := s.saveWizard(w); err != nil {
			return View{}, nil, err
		}
		return s.view(ctx, nil, ""), nil, nil
	}
	if req.Step == "" {
		req.Step = cur
	}
	if req.Step != cur {
		return View{}, nil, &InputError{"That step is not the one in progress. Continue from where you left off."}
	}
	if req.Check {
		v := s.view(ctx, nil, "")
		conn := s.connectivity(ctx, first(req.DNS, strings.Join(v.DNS, " ")))
		v.Connectivity = &conn
		if !conn.OK {
			v.Warning = conn.Detail
		}
		return v, nil, nil
	}
	if req.Skip {
		if !optional(cur) {
			return View{}, nil, &InputError{"This step can't be skipped."}
		}
		rememberSkip(&w, cur)
		if err := s.skipStep(&w, req); err != nil {
			return View{}, nil, err
		}
		advance(&w)
		if err := s.saveWizard(w); err != nil {
			return View{}, nil, err
		}
		s.audit(req.Actor, "wizard.skip", cur, "ok")
		return s.view(ctx, nil, ""), nil, nil
	}
	if !req.Confirm {
		return s.preview(ctx, req)
	}
	if needsRoot(cur, req) && !s.root() {
		return View{}, nil, &OpError{"FELDCORE must run as root to change this server. The preview above is what it would do."}
	}
	recovery, grant, err := s.apply(ctx, &w, req)
	if err != nil {
		s.audit(req.Actor, "wizard."+cur, auditTarget(cur, req), "error")
		if grant != nil {
			v := s.view(ctx, nil, "")
			v.RecoveryCodes = recovery
			return v, grant, err
		}
		return View{}, nil, err
	}
	if cur != "summary" {
		advance(&w)
	}
	if err := s.saveWizard(w); err != nil {
		return View{}, nil, err
	}
	s.audit(req.Actor, "wizard."+cur, auditTarget(cur, req), "ok")
	v := s.view(ctx, nil, "")
	v.RecoveryCodes = recovery
	if cur == "network" && strings.TrimSpace(req.NetMode) == "static" {
		conn := s.connectivity(ctx, req.DNS)
		v.Connectivity = &conn
		if !conn.OK {
			v.Warning = "The address was applied. The connectivity check did not succeed: " + conn.Detail
		}
	}
	return v, grant, nil
}

func (s *Service) authorize(req Request) error {
	if s.st.Snapshot().Admin != nil {
		return nil
	}
	if req.BeginRerun || req.DiscardRerun {
		return &InputError{"Sign in before running setup again."}
	}
	return s.CheckCode(req.Client, req.Code)
}

func (s *Service) preview(ctx context.Context, req Request) (View, *Grant, error) {
	lines, warn, phrase, destructive, err := s.plan(ctx, req)
	if err != nil {
		return View{}, nil, asInput(err)
	}
	v := s.view(ctx, lines, warn)
	v.Destructive = destructive
	v.ConfirmPhrase = phrase
	if req.Step == "network" && req.NetMode == "static" {
		v.NewURL = NewURL(req.Address, s.listenPort)
	}
	return v, nil, nil
}

func (s *Service) plan(ctx context.Context, req Request) (lines []string, warn, phrase string, destructive bool, err error) {
	switch req.Step {
	case "welcome":
		lines = []string{"Continue to the host name. Nothing is changed on this page."}
	case "host":
		lines, err = HostPlan(req.Hostname, req.Timezone, req.NTP, req.Locale)
	case "network":
		if err = ValidateNet(NetInput{req.NetMode, req.Iface, req.Address, req.Gateway, req.DNS}); err != nil {
			return
		}
		if req.NetMode == "" || req.NetMode == "dhcp" {
			lines = []string{"Stay on DHCP. The address of this server will not change."}
			if strings.TrimSpace(req.DNS) != "" {
				lines = append(lines, "DNS servers for the connectivity check: "+req.DNS+".")
			}
		} else {
			lines = []string{
				"Give " + req.Iface + " the address " + req.Address + ".",
				"Gateway " + req.Gateway + ", DNS " + req.DNS + ".",
				"Open FELDCORE at " + NewURL(req.Address, s.listenPort) + " after this.",
				"A wrong address drops this page. Fix it from the console, or start setup over.",
			}
		}
	case "admin":
		if s.st.Snapshot().Admin != nil {
			lines = []string{"An administrator already exists. You can add an authenticator, or continue."}
			return
		}
		lines = []string{"Create the administrator account."}
		if req.SkipTOTP {
			lines = append(lines, "Two-factor authentication stays off. You can turn it on later from Settings.")
		} else {
			lines = append(lines, "Turn on an authenticator after the code from the app matches.")
		}
	case "signin":
		if req.DisableTOTP {
			lines = []string{"Turn the authenticator off for the administrator sign-in."}
		} else if strings.TrimSpace(req.TOTPCode) != "" {
			lines = []string{"Turn on an authenticator for the administrator sign-in."}
		} else {
			lines = []string{"Leave sign-in as it is."}
		}
	case "storage":
		lines, warn, phrase, destructive, err = s.planStorage(ctx, req)
	case "remote":
		lines, err = remotePlan(req)
	case "notify":
		lines, err = notifyPlan(req)
	case "backup":
		lines, err = backupPlan(req)
	case "summary":
		lines = []string{"Finish setup and open the dashboard."}
		if s.wizard().Rerun {
			lines = []string{"Save these choices and return to Settings."}
		}
	default:
		err = errors.New("unknown step")
	}
	return
}

func (s *Service) apply(ctx context.Context, w *store.Wizard, req Request) ([]string, *Grant, error) {
	switch req.Step {
	case "welcome":
		return nil, nil, nil
	case "host":
		if _, _, _, _, err := s.plan(ctx, req); err != nil {
			return nil, nil, asInput(err)
		}
		if err := s.applyHost(ctx, req); err != nil {
			return nil, nil, err
		}
		w.Hostname, w.Timezone, w.NTP, w.Locale = strings.TrimSpace(req.Hostname), strings.TrimSpace(req.Timezone), strings.TrimSpace(req.NTP), strings.TrimSpace(req.Locale)
	case "network":
		if err := ValidateNet(NetInput{req.NetMode, req.Iface, req.Address, req.Gateway, req.DNS}); err != nil {
			return nil, nil, &InputError{err.Error()}
		}
		mode := req.NetMode
		if mode == "" {
			mode = "dhcp"
		}
		w.NetMode = mode
		if mode == "static" {
			if err := s.applyNet(ctx, req); err != nil {
				return nil, nil, err
			}
		}
	case "admin":
		recovery, grant, err := s.applyAdmin(req)
		if err != nil {
			return recovery, grant, err
		}
		if admin := s.st.Snapshot().Admin; admin != nil {
			w.AdminUser = admin.Username
			w.TOTP = admin.TOTP
		}
		return recovery, grant, nil
	case "signin":
		recovery, err := s.applySignin(req)
		if err != nil {
			return nil, nil, err
		}
		if admin := s.st.Snapshot().Admin; admin != nil {
			w.TOTP = admin.TOTP
		}
		return recovery, nil, nil
	case "storage":
		mount, err := s.applyStorage(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		w.StorageMode = req.StorageMode
		w.StorageDevice = req.StorageDevice
		w.StorageMount = mount
		w.StoragePool = strings.TrimSpace(req.StoragePool)
	case "remote":
		if err := s.applyRemote(ctx, req); err != nil {
			return nil, nil, err
		}
		w.RemoteMode = blankNone(req.RemoteMode)
	case "notify":
		if err := s.applyNotify(ctx, req); err != nil {
			return nil, nil, err
		}
		w.NotifyKind = blankNone(req.NotifyKind)
		w.NotifyTo = first(req.MailTo, req.NtfyTopic)
		w.NtfyURL = strings.TrimSpace(req.NtfyURL)
	case "backup":
		if err := s.applyBackup(req); err != nil {
			return nil, nil, err
		}
		w.BackupKind = blankNone(req.BackupKind)
	case "summary":
		if w.Rerun {
			w.Rerun = false
			w.RerunStep = ""
		} else {
			w.Done = true
			w.ShareNames = []string{"appdata", "system", "domains", "isos"}
			if s.shares != nil {
				s.shares.EnsureDefaults(ctx)
			}
		}
	default:
		return nil, nil, &InputError{"Unknown step."}
	}
	return nil, nil, nil
}

func (s *Service) skipStep(w *store.Wizard, req Request) error {
	switch req.Step {
	case "host":
		if w.Hostname == "" {
			w.Hostname = hostname()
		}
		if w.Timezone == "" {
			w.Timezone = currentTZ()
		}
		if w.Locale == "" {
			w.Locale = currentLocale()
		}
	case "network":
		if w.NetMode == "" {
			w.NetMode = "dhcp"
		}
	case "storage":
		if w.StorageMode == "" {
			w.StorageMode = "keep"
		}
	case "remote":
		if w.RemoteMode == "" {
			w.RemoteMode = "none"
		}
	case "notify":
		if w.NotifyKind == "" {
			w.NotifyKind = "none"
		}
	case "backup":
		if w.BackupKind == "" {
			w.BackupKind = "none"
		}
	}
	return nil
}

func (s *Service) planStorage(ctx context.Context, req Request) (lines []string, warn, phrase string, destructive bool, err error) {
	if s.wizard().Rerun && req.StorageFormat {
		err = errors.New("formatting a disk is not part of running setup again")
		return
	}
	mode := strings.TrimSpace(req.StorageMode)
	if mode == "" {
		mode = "keep"
	}
	mount := strings.TrimSpace(req.StorageMount)
	if mount == "" {
		mount = strings.TrimSpace(req.SharesRoot)
	}
	if s.disks == nil {
		if req.StorageFormat {
			err = errors.New("disk management is not available")
			return
		}
		if err = s.checkRoot(mount); err != nil {
			return
		}
		if err = storage.CheckMountCreatable(mount); err != nil {
			return
		}
		lines = []string{"Store shares under " + mount + ". Nothing will be formatted."}
		if OnOSDisk(mount, s.mounts(ctx)) {
			warn = "That folder is on the operating-system disk. Filling it up can stop the server booting. A separate disk is safer."
		}
		return
	}
	p, err := s.disks.Plan(ctx, storage.Choice{
		Mode: mode, Device: req.StorageDevice, Mount: mount,
		Format: req.StorageFormat, FS: req.StorageFS, Pool: req.StoragePool, Confirm: req.StorageConfirm,
	})
	if err != nil {
		return
	}
	return p.Lines, p.Warning, p.ConfirmPhrase, p.Destructive, nil
}

func (s *Service) applyStorage(ctx context.Context, req Request) (string, error) {
	if s.wizard().Rerun && req.StorageFormat {
		return "", &InputError{"Formatting a disk is not part of running setup again."}
	}
	mode := strings.TrimSpace(req.StorageMode)
	if mode == "" {
		mode = "keep"
	}
	mount := strings.TrimSpace(req.StorageMount)
	if mount == "" {
		mount = strings.TrimSpace(req.SharesRoot)
	}
	if s.disks == nil {
		if req.StorageFormat {
			return "", &OpError{"Disk management is not available."}
		}
		if err := s.checkRoot(mount); err != nil {
			return "", &InputError{err.Error()}
		}
		if err := storage.CheckMountCreatable(mount); err != nil {
			return "", &InputError{err.Error()}
		}
		if err := s.setSharesRoot(mount); err != nil {
			return "", err
		}
		return mount, nil
	}
	got, err := s.disks.Apply(ctx, storage.Choice{
		Mode: mode, Device: req.StorageDevice, Mount: mount,
		Format: req.StorageFormat, FS: req.StorageFS, Pool: req.StoragePool, Confirm: req.StorageConfirm,
	})
	if err != nil {
		var ie *storage.InputError
		var oe *storage.OpError
		switch {
		case errors.As(err, &ie):
			return "", &InputError{ie.Msg}
		case errors.As(err, &oe):
			return "", &OpError{oe.Msg}
		default:
			return "", err
		}
	}
	if err := s.setSharesRoot(got); err != nil {
		return "", err
	}
	return got, nil
}

func (s *Service) setSharesRoot(path string) error {
	if err := s.st.Update(func(st *store.State) error {
		st.SharesRoot = path
		return nil
	}); err != nil {
		return err
	}
	if s.shares != nil {
		s.shares.SetRoot(path)
	}
	return nil
}

func remotePlan(req Request) ([]string, error) {
	switch strings.TrimSpace(req.RemoteMode) {
	case "", "none":
		return []string{"Do not publish this server. Cloudflare Tunnel and WireGuard stay off."}, nil
	case "cloudflare":
		mode := req.CFMode
		if mode == "" {
			mode = "token"
		}
		if mode == "api" {
			return []string{"Save a Cloudflare API token and connect the feldcore tunnel. The token is encrypted and is not written to the log."}, nil
		}
		return []string{"Save a Cloudflare tunnel token and start the connector. The token is encrypted and is not written to the log."}, nil
	case "wireguard":
		if strings.TrimSpace(req.WGConf) == "" {
			return nil, errors.New("paste a WireGuard configuration, or choose none")
		}
		return []string{"Import the WireGuard configuration and turn the tunnel on. The private key is encrypted and is not written to the log."}, nil
	default:
		return nil, errors.New("choose none, Cloudflare Tunnel, or WireGuard")
	}
}

func notifyPlan(req Request) ([]string, error) {
	switch strings.TrimSpace(req.NotifyKind) {
	case "", "none":
		return []string{"Do not send notifications yet."}, nil
	case "email":
		if strings.TrimSpace(req.MailTo) == "" {
			return nil, errors.New("enter the address that should receive notifications")
		}
		return []string{"Save the SMTP settings and send a test message to " + strings.TrimSpace(req.MailTo) + "."}, nil
	case "ntfy":
		if strings.TrimSpace(req.NtfyTopic) == "" {
			return nil, errors.New("enter an ntfy topic")
		}
		base := req.NtfyURL
		if strings.TrimSpace(base) == "" {
			base = "https://ntfy.sh"
		}
		return []string{"Send a test message to " + strings.TrimSpace(base) + "/" + strings.TrimSpace(req.NtfyTopic) + "."}, nil
	default:
		return nil, errors.New("choose email, ntfy, or skip notifications")
	}
}

func backupPlan(req Request) ([]string, error) {
	switch strings.TrimSpace(req.BackupKind) {
	case "", "none":
		return []string{"Do not configure backups yet."}, nil
	case "proton":
		if strings.TrimSpace(req.BackupUser) == "" {
			return nil, errors.New("enter the Proton account email")
		}
		when := "Manual only, until you set a time in Settings."
		if strings.TrimSpace(req.BackupSchedule) != "" {
			when = "Daily at " + strings.TrimSpace(req.BackupSchedule) + "."
		}
		return []string{
			"Save a Proton Drive backup of app data for " + strings.TrimSpace(req.BackupUser) + ".",
			when,
			"The password is encrypted. Proton Drive's rclone support is experimental.",
		}, nil
	default:
		return nil, errors.New("choose Proton Drive, or skip backups")
	}
}

func (s *Service) wizard() store.Wizard {
	st := s.st.Snapshot()
	if st.Wizard == nil {
		return store.Wizard{Step: "welcome", Samba: true, NFS: true, Docker: true, Libvirt: true, Unattended: true, ShareNames: []string{"appdata", "system", "domains", "isos"}}
	}
	w := *st.Wizard
	w.Step = canonical(w.Step, false)
	if w.Rerun {
		w.RerunStep = canonical(w.RerunStep, true)
	}
	if w.ShareNames == nil {
		w.ShareNames = []string{"appdata", "system", "domains", "isos"}
	}
	return w
}

func (s *Service) saveWizard(w store.Wizard) error {
	return s.st.Update(func(st *store.State) error {
		cp := w
		cp.ShareNames = append([]string(nil), w.ShareNames...)
		cp.Skipped = append([]string(nil), w.Skipped...)
		st.Wizard = &cp
		return nil
	})
}

func (s *Service) view(ctx context.Context, preview []string, warn string) View {
	w := s.wizard()
	mounts := s.mounts(ctx)
	root := s.st.Snapshot().SharesRoot
	if root == "" && s.shares != nil {
		root = s.shares.Root()
	}
	s.mu.Lock()
	log, running, pkgErr := s.pkgLog.String(), s.pkgRunning, s.pkgErr
	s.mu.Unlock()
	step := cursor(w)
	adminUser := w.AdminUser
	adminExists := false
	if admin := s.st.Snapshot().Admin; admin != nil {
		adminExists = true
		if adminUser == "" {
			adminUser = admin.Username
		}
	}
	totp := w.TOTP
	if s.accounts != nil && s.accounts.TOTPEnabled() {
		totp = true
	}
	v := View{
		Done: w.Done && !w.Rerun, Step: step, Steps: append([]string(nil), stepList(w)...),
		Preview: preview, Warning: warn, Optional: optional(step), Rerun: w.Rerun,
		NeedsCode: !adminExists,
		Hostname:  first(w.Hostname, hostname()), Timezone: first(w.Timezone, currentTZ()),
		Locale: first(w.Locale, currentLocale()), Locales: locales(), Zones: timezones(), NTP: ntpOr(w.NTP),
		Mounts: mounts, SharesRoot: first(w.StorageMount, root), OnOSDisk: OnOSDisk(first(w.StorageMount, root), mounts),
		Virt: s.virt(), Samba: w.Samba, NFS: w.NFS, Docker: w.Docker, Libvirt: w.Libvirt,
		PkgLog: log, PkgRun: running, PkgError: pkgErr,
		ShareNames: append([]string(nil), w.ShareNames...), SSHPorts: s.sshPorts(ctx), ListenPort: s.listenPort,
		Unattended: w.Unattended, Firewall: w.Firewall, UpdateCheck: w.UpdateCheck,
		AdminUser: adminUser, AdminExists: adminExists, TOTPEnabled: totp,
		RemoteMode: blankNone(w.RemoteMode), NotifyKind: blankNone(w.NotifyKind), BackupKind: blankNone(w.BackupKind),
		StorageMode: w.StorageMode, StorageDevice: w.StorageDevice, StoragePool: w.StoragePool,
	}
	// Disk inventory is only shown after the administrator exists. The
	// wizard page is reachable before that, and a disk list is not needed
	// to resume the host or network step.
	if s.disks != nil && adminExists {
		if disks, err := s.disks.Disks(ctx); err == nil {
			v.Disks = disks
		}
	}
	s.fillNet(&v)
	if w.NetMode != "" {
		v.NetMode = w.NetMode
	}
	if step == "summary" || w.Done {
		v.Summary = s.summary(w, v)
	}
	return v
}

func (s *Service) audit(actor, action, target, result string) {
	if s.auditor == nil {
		return
	}
	name := strings.TrimSpace(actor)
	if name == "" {
		name = "setup"
	}
	_ = s.auditor.Record("", store.AuditEvent{
		TokenName: name,
		Action:    action,
		Target:    target,
		Result:    result,
	})
}

func auditTarget(step string, req Request) string {
	switch step {
	case "host":
		return strings.TrimSpace(req.Hostname)
	case "network":
		if req.NetMode == "static" {
			return req.Iface + " " + req.Address
		}
		return "dhcp"
	case "admin", "signin":
		if req.DisableTOTP {
			return "totp off"
		}
		if req.SkipTOTP {
			return "totp skipped"
		}
		return strings.TrimSpace(req.Username)
	case "storage":
		return first(req.StorageDevice, first(req.StorageMount, req.SharesRoot))
	case "remote":
		return blankNone(req.RemoteMode)
	case "notify":
		return blankNone(req.NotifyKind)
	case "backup":
		return blankNone(req.BackupKind)
	default:
		return step
	}
}

func asInput(err error) error {
	if err == nil {
		return nil
	}
	var ie *InputError
	var oe *OpError
	if errors.As(err, &ie) || errors.As(err, &oe) {
		return err
	}
	return &InputError{err.Error()}
}

func blankNone(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "none"
	}
	return v
}
