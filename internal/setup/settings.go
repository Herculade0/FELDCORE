package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Herculade0/feldcore/internal/appliance"
	"github.com/Herculade0/feldcore/internal/store"
)

// ServiceStatus is one daemon Settings can install and switch on or off.
type ServiceStatus struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Available bool   `json:"available"`
}

// SettingsView is everything the Settings page reads in one call.
type SettingsView struct {
	Hostname    string          `json:"hostname"`
	Timezone    string          `json:"timezone"`
	Zones       []string        `json:"zones"`
	NTPServers  string          `json:"ntpServers"`
	Iface       string          `json:"iface"`
	IP          string          `json:"ip"`
	Gateway     string          `json:"gateway"`
	DNS         []string        `json:"dns"`
	NetMode     string          `json:"netMode"`
	Backend     string          `json:"backend"`
	Services    []ServiceStatus `json:"services"`
	Firewall    bool            `json:"firewall"`
	SSHPorts    []int           `json:"sshPorts"`
	ListenPort  int             `json:"listenPort"`
	Rules       []string        `json:"rules"`
	Unattended  bool            `json:"unattended"`
	UpdateCheck bool            `json:"updateCheck"`
	RemoveImage bool            `json:"removeImage"`
	FeedURL     string          `json:"feedURL"`
}

type svcDef struct {
	name, label, unit string
	pkgs              []string
	needsVirt         bool
}

var managedServices = []svcDef{
	{"samba", "Samba", "smbd", []string{"samba"}, false},
	{"nfs", "NFS", "nfs-server", []string{"nfs-kernel-server"}, false},
	{"docker", "Docker", "docker", []string{"docker.io"}, false},
	{"libvirt", "libvirt / KVM", "libvirtd", []string{"qemu-kvm", "libvirt-daemon-system", "libvirt-clients", "qemu-utils", "ovmf", "swtpm", "swtpm-tools"}, true},
}

// Settings reads the current host, network, services and saved choices.
func (s *Service) Settings(ctx context.Context) SettingsView {
	var net View
	s.fillNet(&net)
	prefs := s.st.Snapshot().Prefs
	unattended, check, firewall := prefs.Unattended, prefs.UpdateCheck, prefs.Firewall
	if !prefs.Touched {
		if w := s.st.Snapshot().Wizard; w != nil {
			unattended, check, firewall = w.Unattended, w.UpdateCheck, w.Firewall
		}
	}
	ntp := prefs.NTPServers
	return SettingsView{
		Hostname: hostname(), Timezone: currentTZ(), Zones: timezones(), NTPServers: ntp,
		Iface: net.Iface, IP: net.IP, Gateway: net.Gateway, DNS: net.DNS,
		NetMode: net.NetMode, Backend: net.Backend,
		Services: s.serviceStatus(ctx),
		Firewall: firewall, SSHPorts: s.sshPorts(ctx), ListenPort: s.listenPort,
		Rules:      s.firewallRuleLines(ctx),
		Unattended: unattended, UpdateCheck: check, RemoveImage: prefs.RemoveImage,
		FeedURL: prefs.FeedURL,
	}
}

// SaveSystem sets the host name, time zone and NTP servers.
func (s *Service) SaveSystem(ctx context.Context, host, tz, ntp string) error {
	if _, err := HostPlan(host, tz, "systemd-timesyncd", ""); err != nil {
		return &InputError{err.Error()}
	}
	servers, err := parseNTP(ntp)
	if err != nil {
		return &InputError{err.Error()}
	}
	if !s.root() {
		return &OpError{"FELDCORE must run as root to change the host name, time zone or NTP servers."}
	}
	if err := s.applyHost(ctx, Request{Hostname: strings.TrimSpace(host), Timezone: strings.TrimSpace(tz), NTP: "systemd-timesyncd"}); err != nil {
		return err
	}
	if err := s.writeNTP(ctx, servers); err != nil {
		return err
	}
	return s.st.Update(func(st *store.State) error {
		st.Prefs.NTPServers = strings.Join(servers, " ")
		return nil
	})
}

// SaveNetwork applies DHCP or a static address. A wrong static address can
// drop the page; it is not rolled back.
func (s *Service) SaveNetwork(ctx context.Context, mode, iface, address, gateway, dns string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "dhcp"
	}
	if mode == "static" {
		if err := ValidateNet(NetInput{Mode: "static", Iface: iface, Address: address, Gateway: gateway, DNS: dns}); err != nil {
			return &InputError{err.Error()}
		}
	} else if mode != "dhcp" {
		return &InputError{"Network mode must be dhcp or static."}
	} else if iface != "" && !ifaceRe.MatchString(iface) {
		return &InputError{"That network interface name is not valid."}
	}
	if !s.root() {
		return &OpError{"FELDCORE must run as root to change the network."}
	}
	if mode == "dhcp" {
		if iface == "" {
			var net View
			s.fillNet(&net)
			iface = net.Iface
		}
		if err := s.applyDHCP(ctx, iface); err != nil {
			return err
		}
	} else if err := s.applyNet(ctx, Request{NetMode: "static", Iface: iface, Address: address, Gateway: gateway, DNS: dns}); err != nil {
		return err
	}
	return s.st.Update(func(st *store.State) error {
		if st.Wizard != nil {
			st.Wizard.NetMode = mode
		}
		return nil
	})
}

// SaveService installs a missing daemon if needed, then enables or disables it.
func (s *Service) SaveService(ctx context.Context, name string, enable bool) error {
	def, ok := lookupService(name)
	if !ok {
		return &InputError{"Unknown service."}
	}
	if def.needsVirt && enable && !s.virt() {
		return &InputError{"This CPU has no virtualisation, so libvirt stays off."}
	}
	if !s.root() {
		return &OpError{"FELDCORE must run as root to change services."}
	}
	ctx = context.WithoutCancel(ctx)
	present := s.servicePresent(ctx, def)
	if !enable {
		if !present {
			return nil
		}
		if _, err := s.cmd.Run(ctx, "systemctl", "disable", "--now", def.unit); err != nil {
			return &OpError{"Could not disable " + def.label + ": " + err.Error()}
		}
		return nil
	}
	if !present && appliance.Is() {
		if def.name == "libvirt" {
			return &OpError{"The libvirt extension is not installed. Install it from Settings."}
		}
		return &OpError{def.label + " is part of FELDCORE OS and is not installed from this page."}
	}
	var missing []string
	if !present {
		for _, pkg := range def.pkgs {
			if !s.installed(ctx, pkg) {
				missing = append(missing, pkg)
			}
		}
	}
	if len(missing) > 0 {
		_, _ = s.cmd.Run(ctx, "apt-get", "update")
		args := append([]string{"install", "-y"}, missing...)
		if _, err := s.cmd.Run(ctx, "apt-get", args...); err != nil {
			return &OpError{"Could not install " + def.label + ": " + err.Error()}
		}
	}
	if _, err := s.cmd.Run(ctx, "systemctl", "enable", "--now", def.unit); err != nil {
		return &OpError{def.label + " would not start: " + err.Error()}
	}
	return nil
}

// SaveFirewall turns ufw on or off. Enabling always allows the SSH ports
// that are listening. sshd itself is never edited.
func (s *Service) SaveFirewall(ctx context.Context, on bool) error {
	if !s.root() {
		return &OpError{"FELDCORE must run as root to change the firewall."}
	}
	ctx = context.WithoutCancel(ctx)
	if !on {
		if _, err := s.cmd.Run(ctx, "ufw", "disable"); err != nil {
			return &OpError{"Could not turn the firewall off: " + err.Error()}
		}
		return s.setFlags(func(p *store.Prefs) { p.Firewall = false })
	}
	if !haveBin("ufw") {
		if appliance.Is() {
			return &OpError{"ufw is part of FELDCORE OS and was not found in this image."}
		}
		if _, err := s.cmd.Run(ctx, "apt-get", "install", "-y", "ufw"); err != nil {
			return &OpError{"Could not install ufw: " + err.Error()}
		}
	}
	for _, c := range s.firewallCommands(ctx) {
		if _, err := s.cmd.Run(ctx, c[0], c[1:]...); err != nil {
			return &OpError{"Firewall change failed (" + strings.Join(c, " ") + "): " + err.Error()}
		}
	}
	return s.setFlags(func(p *store.Prefs) { p.Firewall = true })
}

// SaveUpdates stores the security-update and self-update choices.
func (s *Service) SaveUpdates(ctx context.Context, unattended, check bool) error {
	current, _, _ := s.flagState()
	if unattended != current && !s.root() {
		return &OpError{"FELDCORE must run as root to change automatic security updates."}
	}
	if unattended {
		if err := s.applySecurity(ctx, Request{Unattended: true}); err != nil {
			return err
		}
	} else if current && s.root() {
		body := []byte("APT::Periodic::Update-Package-Lists \"0\";\nAPT::Periodic::Unattended-Upgrade \"0\";\n")
		_ = os.MkdirAll("/etc/apt/apt.conf.d", 0o755)
		if err := os.WriteFile("/etc/apt/apt.conf.d/20auto-upgrades", body, 0o644); err != nil {
			return &OpError{"Could not turn security updates off: " + err.Error()}
		}
	}
	return s.setFlags(func(p *store.Prefs) {
		p.Unattended = unattended
		p.UpdateCheck = check
	})
}

// SetRemoveImage stores whether the delete-container dialog starts with
// "Also remove image" ticked.
func (s *Service) SetRemoveImage(on bool) error {
	return s.st.Update(func(st *store.State) error {
		st.Prefs.RemoveImage = on
		return nil
	})
}

// SetFeedPref remembers the catalogue URL. The apps service applies it.
func (s *Service) SetFeedPref(raw string) error {
	return s.st.Update(func(st *store.State) error {
		st.Prefs.FeedURL = raw
		return nil
	})
}

// RecreateShares creates any missing default shares and adopts folders that
// are already there.
func (s *Service) RecreateShares(ctx context.Context) {
	if s.shares != nil {
		s.shares.EnsureDefaults(ctx)
	}
}

func (s *Service) serviceStatus(ctx context.Context) []ServiceStatus {
	out := make([]ServiceStatus, 0, len(managedServices))
	for _, def := range managedServices {
		available := !def.needsVirt || s.virt()
		installed := s.servicePresent(ctx, def)
		en, _ := s.cmd.Run(ctx, "systemctl", "is-enabled", def.unit)
		run, runErr := s.cmd.Run(ctx, "systemctl", "is-active", def.unit)
		out = append(out, ServiceStatus{
			Name: def.name, Label: def.label, Installed: installed, Available: available,
			Enabled: strings.TrimSpace(en) == "enabled",
			Running: runErr == nil && strings.TrimSpace(run) == "active",
		})
	}
	return out
}

func (s *Service) firewallCommands(ctx context.Context) [][]string {
	return UFWCommands(s.listenPort, s.sshPorts(ctx), s.unitActive(ctx, "smbd"), s.unitActive(ctx, "nfs-server"))
}

func (s *Service) firewallRuleLines(ctx context.Context) []string {
	cmds := s.firewallCommands(ctx)
	lines := make([]string, 0, len(cmds))
	for _, c := range cmds {
		lines = append(lines, strings.Join(c, " "))
	}
	return lines
}

func (s *Service) unitActive(ctx context.Context, unit string) bool {
	out, err := s.cmd.Run(ctx, "systemctl", "is-active", unit)
	return err == nil && strings.TrimSpace(out) == "active"
}

func (s *Service) applyDHCP(ctx context.Context, iface string) error {
	if strings.TrimSpace(iface) == "" {
		return &OpError{"No network interface was detected, so DHCP was left as it is."}
	}
	if s.backend() == "" {
		return &OpError{"This server is not using systemd-networkd or ifupdown, so FELDCORE will not change the address. Stay on DHCP."}
	}
	var path, body string
	if s.backend() == "networkd" {
		path = "/etc/systemd/network/10-feldcore.network"
		body = "[Match]\nName=" + iface + "\n\n[Network]\nDHCP=yes\n"
	} else {
		path = "/etc/network/interfaces.d/feldcore"
		body = "auto " + iface + "\niface " + iface + " inet dhcp\n"
	}
	return s.commitNetFile(ctx, path, body, iface)
}

func (s *Service) commitNetFile(ctx context.Context, path, body, iface string) error {
	prev, readErr := os.ReadFile(path)
	missing := os.IsNotExist(readErr)
	if readErr != nil && !missing {
		return &OpError{"Could not read the current network settings: " + readErr.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &OpError{"Could not write the network settings: " + err.Error()}
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return &OpError{"Could not write the network settings: " + err.Error()}
	}
	if err := s.reloadNet(ctx, iface); err != nil {
		if missing {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, prev, 0o644)
		}
		return &OpError{"The address was not applied: " + err.Error()}
	}
	return nil
}

func (s *Service) writeNTP(ctx context.Context, servers []string) error {
	dir := "/etc/systemd/timesyncd.conf.d"
	path := filepath.Join(dir, "feldcore.conf")
	if len(servers) == 0 {
		_ = os.Remove(path)
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return &OpError{"Could not save the NTP servers: " + err.Error()}
		}
		body := "[Time]\nNTP=" + strings.Join(servers, " ") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return &OpError{"Could not save the NTP servers: " + err.Error()}
		}
	}
	_, _ = s.cmd.Run(ctx, "systemctl", "try-restart", "systemd-timesyncd")
	return nil
}

func parseNTP(raw string) ([]string, error) {
	fields := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	if len(fields) > 8 {
		return nil, fmt.Errorf("use at most 8 NTP servers")
	}
	for _, f := range fields {
		if !hostRe.MatchString(f) && !ifaceRe.MatchString(f) {
			return nil, fmt.Errorf("%q is not an NTP server name", f)
		}
	}
	return fields, nil
}

func (s *Service) servicePresent(ctx context.Context, def svcDef) bool {
	ok := len(def.pkgs) > 0
	for _, pkg := range def.pkgs {
		if !s.installed(ctx, pkg) {
			ok = false
			break
		}
	}
	if ok {
		return true
	}
	if !appliance.Is() {
		return false
	}
	return binsPresent(def.name)
}

func binsPresent(name string) bool {
	switch name {
	case "docker":
		return haveBin("dockerd")
	case "samba":
		return haveBin("smbd") && haveBin("smbpasswd")
	case "nfs":
		return haveBin("exportfs")
	case "libvirt":
		if !haveBin("virsh") || !haveBin("qemu-img") || !haveBin("swtpm") {
			return false
		}
		if !haveBin("qemu-system-x86_64") && !haveBin("qemu-kvm") {
			return false
		}
		for _, p := range []string{
			"/usr/share/OVMF/OVMF_CODE_4M.fd",
			"/usr/share/OVMF/OVMF_CODE.fd",
			"/usr/share/ovmf/OVMF_CODE_4M.fd",
		} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func haveBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func lookupService(name string) (svcDef, bool) {
	for _, d := range managedServices {
		if d.name == name {
			return d, true
		}
	}
	return svcDef{}, false
}

func (s *Service) flagState() (unattended, check, firewall bool) {
	st := s.st.Snapshot()
	if st.Prefs.Touched {
		return st.Prefs.Unattended, st.Prefs.UpdateCheck, st.Prefs.Firewall
	}
	if st.Wizard != nil {
		return st.Wizard.Unattended, st.Wizard.UpdateCheck, st.Wizard.Firewall
	}
	return false, false, false
}

func (s *Service) setFlags(fn func(*store.Prefs)) error {
	return s.st.Update(func(st *store.State) error {
		fn(&st.Prefs)
		st.Prefs.Touched = true
		return nil
	})
}
