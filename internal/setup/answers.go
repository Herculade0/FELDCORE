package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	answerName   = "feldcore-install.json"
	appliedName  = "feldcore-install.applied"
	partialName  = "feldcore-install.partial"
	dataPoolName = "data-pool.json"
)

// InstallResult is the record of one answer-file run.
// Passwords, keys, and tokens are not included.
type InstallResult struct {
	Already  bool
	Applied  []string
	Skipped  []string
	Warnings []string
	Notes    []string
	failed   string
}

// installFeat overrides host package checks. A nil pointer asks the host.
type installFeat struct {
	wifi *bool
	ssh  *bool
	sudo *bool
}

type missingFeature struct{ Name string }

func (e missingFeature) Error() string { return e.Name + " is not installed" }

type optionalFailure struct{ Msg string }

func (e optionalFailure) Error() string { return e.Msg }

// answerFile is feldcore-install.json version 1.
// Empty strings and omitted objects mean that section is left alone.
type answerFile struct {
	Version    int            `json:"version"`
	Hostname   string         `json:"hostname,omitempty"`
	Timezone   string         `json:"timezone,omitempty"`
	Locale     string         `json:"locale,omitempty"`
	NTP        string         `json:"ntp,omitempty"`
	NTPServers string         `json:"ntpServers,omitempty"`
	Network    *answerNetwork `json:"network,omitempty"`
	Wifi       *answerWifi    `json:"wifi,omitempty"`
	SSH        *answerSSH     `json:"ssh,omitempty"`
	User       *answerUser    `json:"user,omitempty"`
	Admin      *answerAdmin   `json:"admin,omitempty"`
	SetupCode  string         `json:"setupCode,omitempty"`
	Remote     *answerRemote  `json:"remote,omitempty"`
	Notify     *answerNotify  `json:"notify,omitempty"`
	Backup     *answerBackup  `json:"backup,omitempty"`
	Firewall   *bool          `json:"firewall,omitempty"`
	DataPool   *bool          `json:"dataPool,omitempty"`
}

type answerNetwork struct {
	Mode    string `json:"mode,omitempty"`
	Iface   string `json:"iface,omitempty"`
	Address string `json:"address,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	DNS     string `json:"dns,omitempty"`
}

type answerWifi struct {
	SSID    string `json:"ssid,omitempty"`
	PSK     string `json:"psk,omitempty"`
	Country string `json:"country,omitempty"`
}

type answerSSH struct {
	Enable         bool     `json:"enable"`
	PermitRoot     bool     `json:"permitRoot"`
	AuthorizedKeys []string `json:"authorizedKeys,omitempty"`
}

type answerUser struct {
	Name           string   `json:"name,omitempty"`
	Password       string   `json:"password,omitempty"`
	Sudo           bool     `json:"sudo"`
	AuthorizedKeys []string `json:"authorizedKeys,omitempty"`
}

type answerAdmin struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	SkipTOTP *bool  `json:"skipTotp,omitempty"`
}

type answerRemote struct {
	Mode    string `json:"mode,omitempty"`
	CFMode  string `json:"cfMode,omitempty"`
	CFToken string `json:"cfToken,omitempty"`
	CFAPI   string `json:"cfApi,omitempty"`
	WGConf  string `json:"wgConf,omitempty"`
}

type answerNotify struct {
	Kind      string `json:"kind,omitempty"`
	MailHost  string `json:"mailHost,omitempty"`
	MailPort  int    `json:"mailPort,omitempty"`
	MailUser  string `json:"mailUser,omitempty"`
	MailPass  string `json:"mailPass,omitempty"`
	MailFrom  string `json:"mailFrom,omitempty"`
	MailTo    string `json:"mailTo,omitempty"`
	MailTLS   string `json:"mailTls,omitempty"`
	NtfyURL   string `json:"ntfyUrl,omitempty"`
	NtfyTopic string `json:"ntfyTopic,omitempty"`
	NtfyToken string `json:"ntfyToken,omitempty"`
}

type answerBackup struct {
	Kind     string `json:"kind,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

var (
	unixUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	countryRe  = regexp.MustCompile(`^[A-Za-z]{2}$`)
	hexPSKRe   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	// Same shape as internal/vm/spec.go sshKeyRe.
	answerKeyRe = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ssh-dss|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/=]+( .*)?$`)
)

// ApplyInstallFile reads feldcore-install.json from the ESP or the state
// directory and applies it once. A missing file is not an error.
// A later boot with the applied marker does not run the sections again.
func (s *Service) ApplyInstallFile(ctx context.Context) (*InstallResult, error) {
	defer s.unmountAnswer(ctx)
	if s.installApplied() {
		s.removeAnswerCopies(ctx)
		return &InstallResult{Already: true}, nil
	}
	raw, src, err := s.readAnswerFile(ctx)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	file, err := parseAnswer(raw)
	if err != nil {
		return nil, err
	}
	if err := file.validate(); err != nil {
		return nil, err
	}
	if !s.root() {
		return nil, &OpError{"FELDCORE must run as root to apply the install answer file."}
	}
	secrets := file.secretValues()
	if s.log != nil {
		s.log.Info("reading install answer file", "path", src, "sections", strings.Join(file.present(), ","))
	}
	if err := writeFileMode(filepath.Join(s.dataDir, answerName), raw, 0o600); err != nil {
		return nil, err
	}
	res := &InstallResult{}
	if err := s.applyAnswer(ctx, file, res, secrets); err != nil {
		s.writePartial(res, err.Error())
		s.audit("setup", "answer.apply", res.failed, "error")
		return res, err
	}
	if err := s.finishAnswer(ctx, file, res); err != nil {
		return res, err
	}
	if err := s.writeMarker(res); err != nil {
		return res, err
	}
	_ = os.Remove(filepath.Join(s.dataDir, partialName))
	s.removeAnswerCopies(ctx)
	s.audit("setup", "answer.apply", strings.Join(res.Applied, ","), "ok")
	return res, nil
}

func (s *Service) applyAnswer(ctx context.Context, f answerFile, res *InstallResult, secrets []string) error {
	if f.hasHost() {
		if err := s.step(res, secrets, "host", func() error { return s.applyAnswerHost(ctx, f) }); err != nil {
			return err
		}
	}
	if strings.TrimSpace(f.NTPServers) != "" {
		if err := s.step(res, secrets, "ntp", func() error { return s.applyAnswerNTP(ctx, f.NTPServers) }); err != nil {
			return err
		}
	}
	if f.Network.active() {
		if err := s.step(res, secrets, "network", func() error { return s.applyAnswerNet(ctx, f) }); err != nil {
			return err
		}
	}
	if f.adminActive() {
		if err := s.step(res, secrets, "admin", func() error { return s.applyAnswerAdmin(f) }); err != nil {
			return err
		}
	} else if code := normalizeCode(f.SetupCode); code != "" {
		if err := s.step(res, secrets, "setupCode", func() error { return s.applyAnswerCode(code) }); err != nil {
			return err
		}
	}
	if f.User.active() {
		if err := s.step(res, secrets, "user", func() error { return s.applyAnswerUser(ctx, f, res) }); err != nil {
			return err
		}
	}
	if f.SSH != nil {
		if err := s.step(res, secrets, "ssh", func() error { return s.applyAnswerSSH(ctx, f) }); err != nil {
			return err
		}
	}
	if f.Wifi.active() {
		if err := s.step(res, secrets, "wifi", func() error { return s.applyAnswerWifi(ctx, f) }); err != nil {
			return err
		}
	}
	if f.Remote.active() {
		if err := s.step(res, secrets, "remote", func() error { return s.applyAnswerRemote(ctx, f) }); err != nil {
			return err
		}
	}
	if f.Backup.active() {
		if err := s.step(res, secrets, "backup", func() error { return s.applyAnswerBackup(f) }); err != nil {
			return err
		}
	}
	if f.Firewall != nil && *f.Firewall {
		if err := s.step(res, secrets, "firewall", func() error { return s.SaveFirewall(ctx, true) }); err != nil {
			return err
		}
	}
	if f.DataPool != nil {
		if err := s.step(res, secrets, "dataPool", func() error { return s.applyAnswerDataPool(*f.DataPool, res) }); err != nil {
			return err
		}
	}
	if f.Notify.active() {
		if err := s.step(res, secrets, "notify", func() error { return s.applyAnswerNotify(ctx, f) }); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) step(res *InstallResult, secrets []string, name string, fn func() error) error {
	err := fn()
	if err == nil {
		res.Applied = append(res.Applied, name)
		return nil
	}
	var miss missingFeature
	if errors.As(err, &miss) {
		res.Skipped = append(res.Skipped, name)
		res.Notes = append(res.Notes, name+" skipped: "+miss.Error())
		if s.log != nil {
			s.log.Warn("install answer section skipped", "section", name, "reason", miss.Error())
		}
		return nil
	}
	var opt optionalFailure
	if errors.As(err, &opt) {
		msg := redact(opt.Error(), secrets)
		res.Warnings = append(res.Warnings, name+": "+msg)
		res.Notes = append(res.Notes, name+" failed: "+msg)
		if s.log != nil {
			s.log.Warn("install answer section failed", "section", name, "err", msg)
		}
		return nil
	}
	msg := redact(err.Error(), secrets)
	res.failed = name
	if s.log != nil {
		s.log.Error("install answer section failed", "section", name, "err", msg)
	}
	return &OpError{name + " was not applied: " + msg}
}

func (s *Service) applyAnswerHost(ctx context.Context, f answerFile) error {
	ntp := strings.TrimSpace(f.NTP)
	if ntp == "" {
		ntp = "systemd-timesyncd"
	}
	return s.applyHost(ctx, Request{
		Hostname: strings.TrimSpace(f.Hostname),
		Timezone: strings.TrimSpace(f.Timezone),
		Locale:   strings.TrimSpace(f.Locale),
		NTP:      ntp,
	})
}

func (s *Service) applyAnswerNTP(ctx context.Context, raw string) error {
	servers, err := parseNTP(raw)
	if err != nil {
		return &InputError{err.Error()}
	}
	if err := s.writeNTP(ctx, servers); err != nil {
		return err
	}
	return s.st.Update(func(st *store.State) error {
		st.Prefs.NTPServers = strings.Join(servers, " ")
		return nil
	})
}

func (s *Service) applyAnswerNet(ctx context.Context, f answerFile) error {
	n := f.Network
	mode := strings.TrimSpace(n.Mode)
	if mode == "" {
		mode = "dhcp"
	}
	if mode == "dhcp" {
		// 20-wired.network already gives DHCP to interfaces named e*.
		return nil
	}
	iface := strings.TrimSpace(n.Iface)
	if f.Wifi.active() && iface == "" {
		if !s.hasFeat("wifi") {
			return missingFeature{Name: "wpa_supplicant"}
		}
		return s.writeWLANStatic(ctx, *n)
	}
	if iface == "" {
		if s.lookupIface != nil {
			iface = strings.TrimSpace(s.lookupIface(ctx))
		} else {
			iface = s.resolveIface(ctx)
		}
	}
	if iface == "" {
		return &InputError{"No network interface was detected for the static address. Set network.iface. eth0 is not assumed."}
	}
	return s.SaveNetwork(ctx, "static", iface, n.Address, n.Gateway, n.DNS)
}

func (s *Service) resolveIface(ctx context.Context) string {
	var v View
	s.fillNet(&v)
	if v.Iface != "" {
		return v.Iface
	}
	_, _ = s.cmd.Run(ctx, "systemctl", "start", "network-online.target")
	s.fillNet(&v)
	return v.Iface
}

func (s *Service) applyAnswerAdmin(f answerFile) error {
	req := Request{
		Username:  strings.TrimSpace(f.Admin.Username),
		Password:  f.Admin.Password,
		Password2: f.Admin.Password,
		SkipTOTP:  true,
	}
	if _, _, err := s.applyAdmin(req); err != nil {
		return err
	}
	return nil
}

func (s *Service) applyAnswerCode(code string) error {
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return err
	}
	return writeFileMode(s.codePath(), []byte(code+"\n"), 0o600)
}

func (s *Service) applyAnswerUser(ctx context.Context, f answerFile, res *InstallResult) error {
	u := f.User
	name := strings.TrimSpace(u.Name)
	home := filepath.Join(s.dataDir, "home", name)
	if _, err := s.cmd.Run(ctx, "id", "-u", name); err != nil {
		if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
			return err
		}
		if _, err := s.cmd.Run(ctx, "useradd", "--create-home", "--home-dir", home, "--shell", "/bin/bash", name); err != nil {
			if notInstalled(err) {
				return missingFeature{Name: "useradd"}
			}
			return &OpError{"Could not create the Unix user: " + err.Error()}
		}
		if strings.TrimSpace(u.Password) == "" {
			_, _ = s.cmd.Run(ctx, "passwd", "-l", name)
		}
	}
	if pw := u.Password; pw != "" {
		set := s.unixPass
		if set == nil {
			set = chpasswd
		}
		if err := set(ctx, name, pw); err != nil {
			if notInstalled(err) {
				res.Notes = append(res.Notes, "user password skipped: chpasswd is not installed")
			} else {
				return err
			}
		}
	}
	if len(u.AuthorizedKeys) > 0 {
		if err := s.writeUserKeys(ctx, name, home, u.AuthorizedKeys); err != nil {
			return err
		}
	}
	if u.Sudo {
		if !s.hasFeat("sudo") {
			res.Notes = append(res.Notes, "user sudo skipped: sudo is not installed")
			return nil
		}
		path := s.etcPath("sudoers.d", "feldcore-"+name)
		if err := writeFileMode(path, []byte(name+" ALL=(ALL) ALL\n"), 0o440); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyAnswerSSH(ctx context.Context, f answerFile) error {
	if !s.hasFeat("ssh") {
		return missingFeature{Name: "sshd"}
	}
	path := s.etcPath("ssh", "sshd_config.d", "00-feldcore.conf")
	if err := writeFileMode(path, []byte(sshDropIn(f.SSH.PermitRoot)), 0o644); err != nil {
		return err
	}
	if len(f.SSH.AuthorizedKeys) > 0 {
		keys := s.etcPath("ssh", "authorized_keys", "root")
		if err := writeFileMode(keys, []byte(strings.Join(f.SSH.AuthorizedKeys, "\n")+"\n"), 0o600); err != nil {
			return err
		}
	}
	if !f.SSH.Enable {
		_, _ = s.cmd.Run(ctx, "systemctl", "disable", "--now", "ssh")
		_, _ = s.cmd.Run(ctx, "systemctl", "disable", "--now", "sshd")
		return nil
	}
	if _, err := s.cmd.Run(ctx, "systemctl", "unmask", "ssh"); err != nil && s.log != nil {
		s.log.Info("ssh unmask was not required", "err", err.Error())
	}
	if _, err := s.cmd.Run(ctx, "systemctl", "enable", "--now", "ssh"); err != nil {
		if _, err2 := s.cmd.Run(ctx, "systemctl", "enable", "--now", "sshd"); err2 != nil {
			return &OpError{"Could not start sshd: " + err.Error()}
		}
	}
	return nil
}

func (s *Service) applyAnswerWifi(ctx context.Context, f answerFile) error {
	if !s.hasFeat("wifi") {
		return missingFeature{Name: "wpa_supplicant"}
	}
	body, err := wpaConfig(*f.Wifi)
	if err != nil {
		return err
	}
	if err := writeFileMode(s.etcPath("wpa_supplicant", "feldcore.conf"), []byte(body), 0o600); err != nil {
		return err
	}
	staticWLAN := f.Network != nil && strings.TrimSpace(f.Network.Mode) == "static" && strings.TrimSpace(f.Network.Iface) == ""
	namedWLAN := f.Network != nil && strings.TrimSpace(f.Network.Mode) == "static" && wirelessName(f.Network.Iface)
	if !staticWLAN && !namedWLAN {
		dhcp := "[Match]\nType=wlan\n\n[Network]\nDHCP=yes\nLLMNR=no\nMulticastDNS=no\n"
		if err := s.writeNetFile(ctx, s.etcPath("systemd", "network", "25-wifi.network"), dhcp); err != nil {
			return err
		}
	}
	if _, err := s.cmd.Run(ctx, "systemctl", "enable", "--now", "feldcore-wifi.service"); err != nil {
		return &OpError{"Could not start WiFi: " + err.Error()}
	}
	return nil
}

func (s *Service) applyAnswerRemote(ctx context.Context, f answerFile) error {
	r := f.Remote
	return s.applyRemote(ctx, Request{
		RemoteMode: r.Mode,
		CFMode:     r.CFMode,
		CFToken:    r.CFToken,
		CFAPI:      r.CFAPI,
		WGConf:     r.WGConf,
	})
}

func (s *Service) applyAnswerBackup(f answerFile) error {
	b := f.Backup
	return s.applyBackup(Request{
		BackupKind:     b.Kind,
		BackupUser:     b.User,
		BackupPass:     b.Password,
		BackupSchedule: b.Schedule,
	})
}

func (s *Service) applyAnswerNotify(ctx context.Context, f answerFile) error {
	n := f.Notify
	err := s.applyNotify(ctx, Request{
		NotifyKind: n.Kind,
		MailHost:   n.MailHost,
		MailPort:   n.MailPort,
		MailUser:   n.MailUser,
		MailPass:   n.MailPass,
		MailFrom:   n.MailFrom,
		MailTo:     n.MailTo,
		MailTLS:    n.MailTLS,
		NtfyURL:    n.NtfyURL,
		NtfyTopic:  n.NtfyTopic,
		NtfyToken:  n.NtfyToken,
	})
	if err != nil {
		return optionalFailure{Msg: err.Error()}
	}
	return nil
}

func (s *Service) applyAnswerDataPool(want bool, res *InstallResult) error {
	path := filepath.Join(s.dataDir, dataPoolName)
	if !want {
		if err := writeFileMode(path, []byte("{\"declined\": true}\n"), 0o600); err != nil {
			return err
		}
		res.Notes = append(res.Notes, "dataPool declined; no data partition is created")
		return nil
	}
	if b, err := os.ReadFile(path); err == nil {
		var cur struct {
			Declined bool `json:"declined"`
		}
		if json.Unmarshal(b, &cur) == nil && cur.Declined {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	res.Notes = append(res.Notes, "dataPool reserved; the install-disk partition is not created by this milestone")
	return nil
}

func (s *Service) finishAnswer(ctx context.Context, f answerFile, res *InstallResult) error {
	w := s.wizard()
	if containsStr(res.Applied, "host") {
		w.Hostname = strings.TrimSpace(f.Hostname)
		w.Timezone = strings.TrimSpace(f.Timezone)
		w.Locale = strings.TrimSpace(f.Locale)
		if strings.TrimSpace(f.NTP) != "" {
			w.NTP = strings.TrimSpace(f.NTP)
		} else {
			w.NTP = "systemd-timesyncd"
		}
	}
	if containsStr(res.Applied, "network") && f.Network != nil {
		mode := strings.TrimSpace(f.Network.Mode)
		if mode == "" {
			mode = "dhcp"
		}
		w.NetMode = mode
	}
	if containsStr(res.Applied, "remote") && f.Remote != nil {
		w.RemoteMode = blankNone(f.Remote.Mode)
	}
	if containsStr(res.Applied, "backup") && f.Backup != nil {
		w.BackupKind = blankNone(f.Backup.Kind)
	}
	if containsStr(res.Applied, "notify") && f.Notify != nil {
		w.NotifyKind = blankNone(f.Notify.Kind)
		w.NotifyTo = first(f.Notify.MailTo, f.Notify.NtfyTopic)
	}
	if f.Firewall != nil && containsStr(res.Applied, "firewall") {
		w.Firewall = true
	}
	if f.adminActive() && containsStr(res.Applied, "admin") {
		if admin := s.st.Snapshot().Admin; admin != nil {
			w.Done = true
			w.Step = "summary"
			w.AdminUser = admin.Username
			w.TOTP = false
			w.ShareNames = []string{"appdata", "system", "domains", "isos"}
			if !f.Remote.active() {
				addSkip(&w, "remote")
			}
			if !f.Notify.active() {
				addSkip(&w, "notify")
			}
			if !f.Backup.active() {
				addSkip(&w, "backup")
			}
			addSkip(&w, "storage")
		}
	}
	if err := s.saveWizard(w); err != nil {
		return err
	}
	if w.Done && s.shares != nil {
		s.shares.EnsureDefaults(ctx)
	}
	return nil
}

func (s *Service) writeWLANStatic(ctx context.Context, n answerNetwork) error {
	dns := strings.Join(strings.Fields(strings.ReplaceAll(n.DNS, ",", " ")), " ")
	body := "[Match]\nType=wlan\n\n[Network]\nAddress=" + n.Address + "\nGateway=" + n.Gateway + "\n"
	if dns != "" {
		body += "DNS=" + dns + "\n"
	}
	return s.writeNetFile(ctx, s.etcPath("systemd", "network", "10-feldcore-wifi.network"), body)
}

func (s *Service) writeNetFile(ctx context.Context, path, body string) error {
	if err := writeFileMode(path, []byte(body), 0o644); err != nil {
		return &OpError{"Could not write the network settings: " + err.Error()}
	}
	if _, err := s.cmd.Run(ctx, "networkctl", "reload"); err != nil {
		return &OpError{"The address was not applied: " + err.Error()}
	}
	return nil
}

func (s *Service) writeUserKeys(ctx context.Context, name, home string, keys []string) error {
	body := []byte(strings.Join(keys, "\n") + "\n")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(sshDir, 0o700); err != nil {
		return err
	}
	if err := writeFileMode(filepath.Join(sshDir, "authorized_keys"), body, 0o600); err != nil {
		return err
	}
	etcKeys := s.etcPath("ssh", "authorized_keys", name)
	if err := writeFileMode(etcKeys, body, 0o600); err != nil {
		return err
	}
	owner := name + ":" + name
	if _, err := s.cmd.Run(ctx, "chown", "-R", owner, home); err != nil && !notInstalled(err) {
		return &OpError{"Could not give the Unix user its home directory: " + err.Error()}
	}
	if _, err := s.cmd.Run(ctx, "chown", owner, etcKeys); err != nil && !notInstalled(err) {
		return &OpError{"Could not set the owner of the SSH key: " + err.Error()}
	}
	return nil
}

func sshDropIn(permitRoot bool) string {
	login := "no"
	if permitRoot {
		login = "prohibit-password"
	}
	return "# Written by FELDCORE. Root password login is not offered.\n" +
		"PermitRootLogin " + login + "\n" +
		"PasswordAuthentication no\n" +
		"KbdInteractiveAuthentication no\n" +
		"PubkeyAuthentication yes\n" +
		"AuthorizedKeysFile /etc/ssh/authorized_keys/%u .ssh/authorized_keys\n"
}

func wpaConfig(w answerWifi) (string, error) {
	ssid, err := wpaQuote(strings.TrimSpace(w.SSID))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("ctrl_interface=DIR=/run/wpa_supplicant GROUP=netdev\nupdate_config=0\n")
	if c := strings.ToUpper(strings.TrimSpace(w.Country)); c != "" {
		fmt.Fprintf(&b, "country=%s\n", c)
	}
	b.WriteString("\nnetwork={\n")
	fmt.Fprintf(&b, "\tssid=%s\n\tscan_ssid=1\n", ssid)
	psk := strings.TrimSpace(w.PSK)
	switch {
	case psk == "":
		b.WriteString("\tkey_mgmt=NONE\n")
	case hexPSKRe.MatchString(psk):
		fmt.Fprintf(&b, "\tpsk=%s\n", psk)
	default:
		quoted, err := wpaQuote(psk)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\tpsk=%s\n", quoted)
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func wpaQuote(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n") || strings.ContainsRune(s, 0) {
		return "", &InputError{"The WiFi name and password cannot contain a new line."}
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", &InputError{"The WiFi name and password cannot contain control characters."}
		}
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String(), nil
}

func parseAnswer(raw []byte) (answerFile, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var file answerFile
	if err := dec.Decode(&file); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return answerFile{}, &InputError{"The install answer file has an unknown field: " + unknownField(err)}
		}
		return answerFile{}, &InputError{"The install answer file is not valid JSON."}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return answerFile{}, &InputError{"The install answer file must be one JSON object."}
	}
	return file, nil
}

func unknownField(err error) string {
	msg := err.Error()
	i := strings.Index(msg, `"`)
	j := strings.LastIndex(msg, `"`)
	if i >= 0 && j > i {
		return msg[i : j+1]
	}
	return "see the file"
}

func (f answerFile) validate() error {
	if f.Version != 1 {
		return &InputError{"The install answer file version must be 1."}
	}
	if f.hasHost() {
		if strings.TrimSpace(f.Hostname) == "" || strings.TrimSpace(f.Timezone) == "" {
			return &InputError{"hostname and timezone are both required when host settings are present."}
		}
		ntp := strings.TrimSpace(f.NTP)
		if ntp == "" {
			ntp = "systemd-timesyncd"
		}
		if ntp == "chrony" {
			return &InputError{"chrony is not installed on FELDCORE OS. Use systemd-timesyncd."}
		}
		if _, err := HostPlan(f.Hostname, f.Timezone, ntp, f.Locale); err != nil {
			return &InputError{err.Error()}
		}
	}
	if strings.TrimSpace(f.NTPServers) != "" {
		if _, err := parseNTP(f.NTPServers); err != nil {
			return &InputError{err.Error()}
		}
	}
	if f.Network.active() {
		if err := f.Network.validate(f.Wifi.active()); err != nil {
			return err
		}
	}
	if f.Wifi.active() {
		if err := f.Wifi.validate(); err != nil {
			return err
		}
	}
	if f.SSH != nil {
		if err := validateKeys(f.SSH.AuthorizedKeys); err != nil {
			return err
		}
	}
	if f.User.active() {
		if err := f.User.validate(); err != nil {
			return err
		}
	}
	code := normalizeCode(f.SetupCode)
	if strings.TrimSpace(f.SetupCode) != "" && code == "" {
		return &InputError{"setupCode must be 8 characters from the setup alphabet, for example ABCD-2345."}
	}
	if f.adminActive() {
		if code != "" {
			return &InputError{"Set the administrator or a setup code, not both."}
		}
		if err := f.Admin.validate(); err != nil {
			return err
		}
	}
	if f.Remote.active() {
		if _, err := remotePlan(Request{RemoteMode: f.Remote.Mode, CFMode: f.Remote.CFMode, WGConf: f.Remote.WGConf}); err != nil {
			return &InputError{err.Error()}
		}
	}
	if f.Backup.active() {
		if _, err := backupPlan(Request{BackupKind: f.Backup.Kind, BackupUser: f.Backup.User, BackupSchedule: f.Backup.Schedule}); err != nil {
			return &InputError{err.Error()}
		}
	}
	if f.Notify.active() {
		if _, err := notifyPlan(Request{
			NotifyKind: f.Notify.Kind, MailTo: f.Notify.MailTo,
			NtfyURL: f.Notify.NtfyURL, NtfyTopic: f.Notify.NtfyTopic,
		}); err != nil {
			return &InputError{err.Error()}
		}
	}
	return nil
}

func (n *answerNetwork) active() bool {
	if n == nil {
		return false
	}
	return strings.TrimSpace(n.Mode) != "" || strings.TrimSpace(n.Iface) != "" ||
		strings.TrimSpace(n.Address) != "" || strings.TrimSpace(n.Gateway) != "" || strings.TrimSpace(n.DNS) != ""
}

func (n *answerNetwork) validate(wifi bool) error {
	mode := strings.TrimSpace(n.Mode)
	if mode == "" {
		mode = "dhcp"
	}
	switch mode {
	case "dhcp":
		if strings.TrimSpace(n.Address) != "" || strings.TrimSpace(n.Gateway) != "" {
			return &InputError{"network.address is only used when mode is static."}
		}
		if iface := strings.TrimSpace(n.Iface); iface != "" && !ifaceRe.MatchString(iface) {
			return &InputError{"That network interface name is not valid."}
		}
		return nil
	case "static":
	default:
		return &InputError{"network.mode must be dhcp or static."}
	}
	iface := strings.TrimSpace(n.Iface)
	if wifi && iface != "" && !wirelessName(iface) {
		return &InputError{"When WiFi is set, a static address is applied to the wireless interface. Leave network.iface empty, or name the wireless interface."}
	}
	check := iface
	if check == "" {
		check = "wlan0"
	}
	if err := ValidateNet(NetInput{Mode: "static", Iface: check, Address: n.Address, Gateway: n.Gateway, DNS: n.DNS}); err != nil {
		return &InputError{err.Error()}
	}
	return nil
}

func (w *answerWifi) active() bool {
	if w == nil {
		return false
	}
	return strings.TrimSpace(w.SSID) != "" || strings.TrimSpace(w.PSK) != "" || strings.TrimSpace(w.Country) != ""
}

func (w *answerWifi) validate() error {
	ssid := strings.TrimSpace(w.SSID)
	if ssid == "" {
		return &InputError{"wifi.ssid is required when wifi is set."}
	}
	if len(ssid) > 32 {
		return &InputError{"wifi.ssid must be at most 32 bytes."}
	}
	if _, err := wpaQuote(ssid); err != nil {
		return err
	}
	psk := strings.TrimSpace(w.PSK)
	if psk != "" && !hexPSKRe.MatchString(psk) {
		if len(psk) < 8 || len(psk) > 63 {
			return &InputError{"wifi.psk must be 8 to 63 characters, or 64 hex digits."}
		}
		if _, err := wpaQuote(psk); err != nil {
			return err
		}
	}
	if c := strings.TrimSpace(w.Country); c != "" && !countryRe.MatchString(c) {
		return &InputError{"wifi.country must be a two-letter country code."}
	}
	return nil
}

func (u *answerUser) active() bool {
	if u == nil {
		return false
	}
	return strings.TrimSpace(u.Name) != "" || u.Password != "" || u.Sudo || len(u.AuthorizedKeys) > 0
}

func (u *answerUser) validate() error {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		return &InputError{"user.name is required when a Unix user is set."}
	}
	if name == "root" || !unixUserRe.MatchString(name) {
		return &InputError{"Unix user names start with a lowercase letter or underscore and use lowercase letters, numbers, hyphens and underscores. root is not allowed."}
	}
	if u.Password != "" {
		if err := auth.ValidateStrong(name, u.Password); err != nil {
			return &InputError{err.Error()}
		}
	}
	return validateKeys(u.AuthorizedKeys)
}

func (a *answerAdmin) validate() error {
	if a.SkipTOTP == nil || !*a.SkipTOTP {
		return &InputError{"admin.skipTotp must be true. Unattended setup cannot enrol an authenticator."}
	}
	user := strings.TrimSpace(a.Username)
	if user == "" || a.Password == "" {
		return &InputError{"admin.username and admin.password are both required."}
	}
	if err := auth.ValidateStrong(user, a.Password); err != nil {
		return &InputError{err.Error()}
	}
	return nil
}

func (f answerFile) adminActive() bool {
	return f.Admin != nil && (strings.TrimSpace(f.Admin.Username) != "" || f.Admin.Password != "")
}

func (r *answerRemote) active() bool {
	return r != nil && strings.TrimSpace(r.Mode) != ""
}

func (b *answerBackup) active() bool {
	return b != nil && strings.TrimSpace(b.Kind) != "" && strings.TrimSpace(b.Kind) != "none"
}

func (n *answerNotify) active() bool {
	return n != nil && strings.TrimSpace(n.Kind) != "" && strings.TrimSpace(n.Kind) != "none"
}

func (f answerFile) hasHost() bool {
	return strings.TrimSpace(f.Hostname) != "" || strings.TrimSpace(f.Timezone) != "" ||
		strings.TrimSpace(f.Locale) != "" || strings.TrimSpace(f.NTP) != ""
}

func (f answerFile) present() []string {
	var out []string
	if f.hasHost() {
		out = append(out, "host")
	}
	if strings.TrimSpace(f.NTPServers) != "" {
		out = append(out, "ntp")
	}
	if f.Network.active() {
		out = append(out, "network")
	}
	if f.adminActive() {
		out = append(out, "admin")
	} else if normalizeCode(f.SetupCode) != "" {
		out = append(out, "setupCode")
	}
	if f.User.active() {
		out = append(out, "user")
	}
	if f.SSH != nil {
		out = append(out, "ssh")
	}
	if f.Wifi.active() {
		out = append(out, "wifi")
	}
	if f.Remote.active() {
		out = append(out, "remote")
	}
	if f.Backup.active() {
		out = append(out, "backup")
	}
	if f.Firewall != nil && *f.Firewall {
		out = append(out, "firewall")
	}
	if f.DataPool != nil {
		out = append(out, "dataPool")
	}
	if f.Notify.active() {
		out = append(out, "notify")
	}
	return out
}

func (f answerFile) secretValues() []string {
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) >= 4 {
			out = append(out, v)
		}
	}
	if f.Admin != nil {
		add(f.Admin.Password)
	}
	add(f.SetupCode)
	add(normalizeCode(f.SetupCode))
	if f.User != nil {
		add(f.User.Password)
	}
	if f.Wifi != nil {
		add(f.Wifi.PSK)
	}
	if f.Remote != nil {
		add(f.Remote.CFToken)
		add(f.Remote.CFAPI)
		add(f.Remote.WGConf)
	}
	if f.Backup != nil {
		add(f.Backup.Password)
	}
	if f.Notify != nil {
		add(f.Notify.MailPass)
		add(f.Notify.NtfyToken)
	}
	return out
}

func validateKeys(keys []string) error {
	for _, key := range keys {
		line := strings.TrimSpace(key)
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToUpper(line), "PRIVATE KEY") {
			return &InputError{"SSH public keys only. A private key was refused."}
		}
		if !answerKeyRe.MatchString(line) {
			return &InputError{"That SSH public key was not recognised."}
		}
	}
	return nil
}

func wirelessName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "wl")
}

func (s *Service) hasFeat(name string) bool {
	var p *bool
	var bins []string
	switch name {
	case "wifi":
		p, bins = s.feat.wifi, []string{"wpa_supplicant"}
	case "ssh":
		p, bins = s.feat.ssh, []string{"sshd"}
	case "sudo":
		p, bins = s.feat.sudo, []string{"sudo"}
	default:
		return false
	}
	if p != nil {
		return *p
	}
	for _, bin := range bins {
		if binExists(bin) {
			return true
		}
	}
	return false
}

func binExists(name string) bool {
	if haveBin(name) {
		return true
	}
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func (s *Service) etcPath(parts ...string) string {
	root := s.etcRoot
	if root == "" {
		root = "/etc"
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func (s *Service) espRoot() string {
	if s.efiRoot != "" {
		return s.efiRoot
	}
	return "/efi"
}

func (s *Service) installApplied() bool {
	st, err := os.Stat(filepath.Join(s.dataDir, appliedName))
	return err == nil && !st.IsDir()
}

func (s *Service) readAnswerFile(ctx context.Context) ([]byte, string, error) {
	esp := filepath.Join(s.espRoot(), "feldcore", answerName)
	if b, err := os.ReadFile(esp); err == nil {
		s.answerSrc = esp
		return b, esp, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	state := filepath.Join(s.dataDir, answerName)
	if b, err := os.ReadFile(state); err == nil {
		s.answerSrc = state
		return b, state, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	// A test sets efiRoot and must not mount the host or scan its disks.
	if s.efiRoot != "" {
		return nil, "", nil
	}
	if err := s.mountESP(ctx); err == nil {
		if b, err := os.ReadFile(esp); err == nil {
			s.answerSrc = esp
			return b, esp, nil
		} else if err != nil && !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	return s.readAnswerVolumes(ctx)
}

func (s *Service) mountESP(ctx context.Context) error {
	dev := "/dev/disk/by-label/FC-ESP"
	if _, err := os.Stat(dev); err != nil {
		return err
	}
	if out, err := s.cmd.Run(ctx, "findmnt", "-n", "-o", "TARGET", "/efi"); err == nil && strings.TrimSpace(out) != "" {
		return nil
	}
	if err := os.MkdirAll("/efi", 0o755); err != nil {
		return err
	}
	_, err := s.cmd.Run(ctx, "mount", "-o", "ro", dev, "/efi")
	return err
}

func (s *Service) removeAnswerCopies(ctx context.Context) {
	esp := filepath.Join(s.espRoot(), "feldcore", answerName)
	s.removeAnswerPath(ctx, esp)
	if s.answerSrc != "" && s.answerSrc != esp {
		s.removeAnswerPath(ctx, s.answerSrc)
	}
	_ = os.Remove(filepath.Join(s.dataDir, answerName))
	s.unmountAnswer(ctx)
}

func (s *Service) removeAnswerPath(ctx context.Context, path string) {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return
	}
	if s.answerMount != "" && s.answerMount != "/" && strings.HasPrefix(path, s.answerMount+"/") {
		if _, rerr := s.cmd.Run(ctx, "mount", "-o", "remount,rw", s.answerMount); rerr != nil {
			if s.log != nil {
				s.log.Warn("could not remove the install answer file", "path", path)
			}
			return
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && s.log != nil {
			s.log.Warn("could not remove the install answer file", "path", path)
		}
		return
	}
	if s.efiRoot != "" || !strings.HasPrefix(path, "/efi/") {
		if s.log != nil {
			s.log.Warn("could not remove the install answer file", "path", path)
		}
		return
	}
	if _, rerr := s.cmd.Run(ctx, "mount", "-o", "remount,rw", "/efi"); rerr != nil {
		if s.log != nil {
			s.log.Warn("could not remove the install answer file", "path", path)
		}
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) && s.log != nil {
		s.log.Warn("could not remove the install answer file", "path", path)
	}
}

func (s *Service) writeMarker(res *InstallResult) error {
	body, err := json.MarshalIndent(answerMarker{
		Version:  1,
		Sections: res.Applied,
		Skipped:  res.Skipped,
		Warnings: res.Warnings,
		Notes:    res.Notes,
	}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return writeFileMode(filepath.Join(s.dataDir, appliedName), body, 0o600)
}

func (s *Service) writePartial(res *InstallResult, msg string) {
	body, err := json.MarshalIndent(map[string]any{
		"version":  1,
		"sections": res.Applied,
		"failed":   res.failed,
		"error":    msg,
	}, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileMode(filepath.Join(s.dataDir, partialName), append(body, '\n'), 0o600)
}

type answerMarker struct {
	Version  int      `json:"version"`
	Sections []string `json:"sections"`
	Skipped  []string `json:"skipped,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

func writeFileMode(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func chpasswd(ctx context.Context, user, password string) error {
	cmd := exec.CommandContext(ctx, "chpasswd")
	cmd.Stdin = strings.NewReader(user + ":" + password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return &OpError{"Could not set the Unix password: " + text}
	}
	return nil
}

func notInstalled(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not installed") || strings.Contains(msg, "executable file not found") || strings.Contains(msg, "no such file")
}

func redact(msg string, secrets []string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, secret, "[redacted]")
	}
	return msg
}

func containsStr(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func addSkip(w *store.Wizard, name string) {
	for _, have := range w.Skipped {
		if have == name {
			return
		}
	}
	w.Skipped = append(w.Skipped, name)
}
