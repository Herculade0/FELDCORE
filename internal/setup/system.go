package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/Herculade0/feldcore/internal/appliance"
	"github.com/Herculade0/feldcore/internal/install"
	"github.com/Herculade0/feldcore/internal/store"
)

func realRoot() bool { return os.Geteuid() == 0 }

type realCmd struct{}

func (realCmd) Run(ctx context.Context, name string, args ...string) (string, error) {
	// apt can sit for several minutes on a first install. A short cap used to
	// kill a libvirt install that was still making progress.
	timeout := 3 * time.Minute
	if name == "apt-get" || name == "apt" {
		timeout = 20 * time.Minute
	}
	// An install job puts a log hook on ctx. Stream in that case so the page
	// can show apt and dpkg while they run. Other callers keep one blob.
	if install.WantsLines(ctx) {
		text, err := install.Run(ctx, timeout, name, args...)
		if err != nil {
			if text == "" {
				text = err.Error()
			}
			return text, fmt.Errorf("%s", text)
		}
		return text, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not installed", name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s", text)
	}
	return text, nil
}

func currentTZ() string {
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(b)); tz != "" {
			return tz
		}
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		const mark = "zoneinfo/"
		if i := strings.Index(link, mark); i >= 0 {
			return link[i+len(mark):]
		}
	}
	return "UTC"
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func timezones() []string {
	b, err := os.ReadFile("/usr/share/zoneinfo/zone.tab")
	if err != nil {
		return []string{"UTC"}
	}
	var out []string
	out = append(out, "UTC")
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 3 {
			out = append(out, f[2])
		}
	}
	return out
}

func (s *Service) virt() bool {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return false
	}
	return VirtAvailable(string(b))
}

func (s *Service) sshPorts(ctx context.Context) []int {
	sshd, _ := s.cmd.Run(ctx, "sshd", "-T")
	ss, _ := s.cmd.Run(ctx, "ss", "-H", "-ltnp")
	return sshPortsFrom(sshd, ss)
}

func (s *Service) mounts(ctx context.Context) []Mount {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	rootDev := ""
	var out []Mount
	for _, p := range parts {
		if p.Mountpoint == "" || strings.HasPrefix(p.Mountpoint, "/proc") || strings.HasPrefix(p.Mountpoint, "/sys") || strings.HasPrefix(p.Mountpoint, "/dev") || strings.HasPrefix(p.Mountpoint, "/run") {
			continue
		}
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		m := Mount{Path: p.Mountpoint, Device: p.Device, Free: u.Free, Total: u.Total}
		if p.Mountpoint == "/" {
			rootDev = p.Device
			m.OS = true
		}
		out = append(out, m)
	}
	for i := range out {
		if out[i].Device != "" && out[i].Device == rootDev {
			out[i].OS = true
		}
	}
	return out
}

func (s *Service) fillNet(v *View) {
	ifaces, _ := net.Interfaces()
	gw, gwIf := defaultGateway()
	v.Gateway = gw
	v.DNS = dnsServers()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if gwIf != "" && ifc.Name != gwIf {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			v.Iface = ifc.Name
			v.IP = ipn.IP.String()
			v.NetMode = "dhcp"
			v.Backend = s.backend()
			return
		}
	}
	v.NetMode = "dhcp"
	v.Backend = s.backend()
}

func (s *Service) backend() string {
	if out, err := s.cmd.Run(context.Background(), "systemctl", "is-active", "systemd-networkd"); err == nil && strings.TrimSpace(out) == "active" {
		return "networkd"
	}
	if _, err := os.Stat(s.etcPath("network", "interfaces")); err == nil {
		return "ifupdown"
	}
	return ""
}

func defaultGateway() (ip, iface string) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		n, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil {
			continue
		}
		g := net.IPv4(byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
		return g.String(), f[0]
	}
	return "", ""
}

func dnsServers() []string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			out = append(out, f[1])
		}
	}
	return out
}

func (s *Service) checkRoot(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return fmt.Errorf("choose an absolute folder, for example /srv/feldcore")
	}
	return nil
}

func (s *Service) applyHost(ctx context.Context, req Request) error {
	if _, err := s.cmd.Run(ctx, "hostnamectl", "set-hostname", strings.TrimSpace(req.Hostname)); err != nil {
		return &OpError{"Could not set the host name: " + err.Error()}
	}
	if _, err := s.cmd.Run(ctx, "timedatectl", "set-timezone", strings.TrimSpace(req.Timezone)); err != nil {
		return &OpError{"Could not set the time zone: " + err.Error()}
	}
	if loc := strings.TrimSpace(req.Locale); loc != "" {
		if _, err := s.cmd.Run(ctx, "localectl", "set-locale", "LANG="+loc); err != nil {
			path := s.etcPath("default", "locale")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return &OpError{"Could not set the locale: " + err.Error()}
			}
			if err := os.WriteFile(path, []byte("LANG="+loc+"\n"), 0o644); err != nil {
				return &OpError{"Could not set the locale: " + err.Error()}
			}
		}
	}
	switch req.NTP {
	case "chrony":
		if appliance.Is() {
			return &OpError{"FELDCORE OS keeps time with systemd-timesyncd. chrony is not installed on the image."}
		}
		if _, err := s.cmd.Run(ctx, "apt-get", "install", "-y", "chrony"); err != nil {
			return &OpError{"Could not install chrony: " + err.Error()}
		}
		_, _ = s.cmd.Run(ctx, "systemctl", "enable", "--now", "chrony")
	default:
		_, _ = s.cmd.Run(ctx, "systemctl", "enable", "--now", "systemd-timesyncd")
		if _, err := s.cmd.Run(ctx, "timedatectl", "set-ntp", "true"); err != nil {
			return &OpError{"Could not turn NTP on: " + err.Error()}
		}
	}
	return nil
}

func (s *Service) packagePlan(ctx context.Context, req Request) []string {
	if appliance.Is() {
		return []string{"FELDCORE OS includes Docker, Samba, and NFS. libvirt is an extension, installed from Settings."}
	}
	var lines []string
	add := func(on bool, pkg string) {
		if !on {
			return
		}
		if s.installed(ctx, pkg) {
			lines = append(lines, pkg+" is already installed.")
		} else {
			lines = append(lines, "Install "+pkg+".")
		}
	}
	add(req.Samba, "samba")
	add(req.NFS, "nfs-kernel-server")
	add(req.Docker, "docker.io")
	if req.Libvirt {
		if !s.virt() {
			lines = append(lines, "libvirt will not be installed: this CPU has no virtualisation.")
		} else if def, ok := lookupService("libvirt"); ok {
			for _, pkg := range def.pkgs {
				add(true, pkg)
			}
		}
	}
	if len(lines) == 0 {
		lines = []string{"Nothing extra will be installed."}
	}
	return lines
}

func (s *Service) installed(ctx context.Context, pkg string) bool {
	out, err := s.cmd.Run(ctx, "dpkg-query", "-W", "-f", "${Status}", pkg)
	return err == nil && strings.Contains(out, "install ok installed")
}

func (s *Service) startPackages(ctx context.Context, w *store.Wizard, req Request) error {
	s.mu.Lock()
	if s.pkgRunning {
		s.mu.Unlock()
		return &OpError{"An install is already running."}
	}
	s.pkgRunning = true
	s.pkgErr = ""
	s.pkgLog.Reset()
	s.mu.Unlock()
	if err := s.saveWizard(*w); err != nil {
		return err
	}
	go s.runPackages(ctx, w, req)
	return nil
}

func (s *Service) runPackages(ctx context.Context, w *store.Wizard, req Request) {
	defer func() {
		s.mu.Lock()
		s.pkgRunning = false
		s.mu.Unlock()
	}()
	var pkgs []string
	if req.Samba {
		pkgs = append(pkgs, "samba")
	}
	if req.NFS {
		pkgs = append(pkgs, "nfs-kernel-server")
	}
	if req.Docker {
		pkgs = append(pkgs, "docker.io")
	}
	if req.Libvirt && s.virt() {
		if def, ok := lookupService("libvirt"); ok {
			pkgs = append(pkgs, def.pkgs...)
		}
	}
	s.appendLog("Checking packages…")
	if appliance.Is() {
		s.appendLog("FELDCORE OS already includes these packages.")
		if req.Docker {
			_, _ = s.cmd.Run(ctx, "systemctl", "enable", "--now", "docker")
		}
		if req.Libvirt && s.virt() {
			_, _ = s.cmd.Run(ctx, "systemctl", "enable", "--now", "libvirtd")
		}
		s.appendLog("Done.")
		w.Step = nextStep(w.Step)
		_ = s.saveWizard(*w)
		return
	}
	var need []string
	for _, p := range pkgs {
		if s.installed(ctx, p) {
			s.appendLog(p + " is already installed.")
			continue
		}
		need = append(need, p)
	}
	if len(need) > 0 {
		s.appendLog("apt-get update")
		_, _ = s.cmd.Run(ctx, "apt-get", "update")
		s.appendLog("Installing " + strings.Join(need, ", "))
		args := append([]string{"install", "-y"}, need...)
		if _, err := s.cmd.Run(ctx, "apt-get", args...); err != nil {
			s.mu.Lock()
			s.pkgErr = "Install failed: " + err.Error()
			s.mu.Unlock()
			s.appendLog(s.pkgErr)
			return
		}
	}
	if req.Docker {
		_, _ = s.cmd.Run(ctx, "systemctl", "enable", "--now", "docker")
	}
	if req.Libvirt && s.virt() {
		_, _ = s.cmd.Run(ctx, "systemctl", "enable", "--now", "libvirtd")
	}
	s.appendLog("Done.")
	w.Step = nextStep(w.Step)
	_ = s.saveWizard(*w)
}

func (s *Service) appendLog(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pkgLog.Len() > 0 {
		s.pkgLog.WriteByte('\n')
	}
	s.pkgLog.WriteString(line)
}

func (s *Service) applySecurity(ctx context.Context, req Request) error {
	if req.Unattended {
		if appliance.Is() {
			return &OpError{"FELDCORE OS updates by replacing the image. Automatic apt upgrades are not used."}
		}
		if _, err := s.cmd.Run(ctx, "apt-get", "install", "-y", "unattended-upgrades"); err != nil {
			return &OpError{"Could not install unattended-upgrades: " + err.Error()}
		}
		body := []byte("APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n")
		if err := os.MkdirAll("/etc/apt/apt.conf.d", 0o755); err != nil {
			return &OpError{"Could not enable security updates: " + err.Error()}
		}
		if err := os.WriteFile("/etc/apt/apt.conf.d/20auto-upgrades", body, 0o644); err != nil {
			return &OpError{"Could not enable security updates: " + err.Error()}
		}
	}
	if !req.Firewall {
		return nil
	}
	if !haveBin("ufw") {
		if appliance.Is() {
			return &OpError{"ufw is part of FELDCORE OS and was not found in this image."}
		}
		if _, err := s.cmd.Run(ctx, "apt-get", "install", "-y", "ufw"); err != nil {
			return &OpError{"Could not install ufw: " + err.Error()}
		}
	}
	// SSH allow rules are first, and sshd's configuration is never edited.
	for _, c := range UFWCommands(s.listenPort, s.sshPorts(ctx), req.Samba, req.NFS) {
		if _, err := s.cmd.Run(ctx, c[0], c[1:]...); err != nil {
			return &OpError{"Firewall change failed (" + strings.Join(c, " ") + "): " + err.Error()}
		}
	}
	return nil
}

// applyNet writes a static address and reloads that interface. A wrong
// address can lock the browser out; that is recovered from the console,
// not by an automatic rollback.
func (s *Service) applyNet(ctx context.Context, req Request) error {
	if s.backend() == "" {
		return &OpError{"This server is not using systemd-networkd or ifupdown, so FELDCORE will not change the address. Stay on DHCP."}
	}
	path, body := s.netFile(req)
	return s.commitNetFile(ctx, path, body, req.Iface)
}

func (s *Service) netFile(req Request) (string, string) {
	dns := strings.Join(strings.Fields(strings.ReplaceAll(req.DNS, ",", " ")), " ")
	if s.backend() == "networkd" {
		body := "[Match]\nName=" + req.Iface + "\n\n[Network]\nAddress=" + req.Address + "\nGateway=" + req.Gateway + "\n"
		if dns != "" {
			body += "DNS=" + dns + "\n"
		}
		return s.etcPath("systemd", "network", "10-feldcore.network"), body
	}
	body := "auto " + req.Iface + "\niface " + req.Iface + " inet static\n    address " + req.Address + "\n    gateway " + req.Gateway + "\n"
	if dns != "" {
		body += "    dns-nameservers " + dns + "\n"
	}
	return s.etcPath("network", "interfaces.d", "feldcore"), body
}

func (s *Service) reloadNet(ctx context.Context, iface string) error {
	if s.backend() == "networkd" {
		_, err := s.cmd.Run(ctx, "networkctl", "reconfigure", iface)
		return err
	}
	_, _ = s.cmd.Run(ctx, "ifdown", iface)
	_, err := s.cmd.Run(ctx, "ifup", iface)
	return err
}

func (s *Service) summary(w store.Wizard, v View) []string {
	admin := orDash(w.AdminUser)
	if v.AdminUser != "" {
		admin = v.AdminUser
	}
	totp := "off"
	if w.TOTP || v.TOTPEnabled {
		totp = "on"
	}
	lines := []string{
		"Host name: " + orDash(first(w.Hostname, v.Hostname)),
		"Time zone: " + orDash(first(w.Timezone, v.Timezone)) + ", locale " + orDash(first(w.Locale, v.Locale)),
		"Network: " + orDash(first(w.NetMode, v.NetMode)),
		"Administrator: " + admin + ", authenticator " + totp,
		"Data folder: " + orDash(first(w.StorageMount, v.SharesRoot)),
		"Remote access: " + orDash(w.RemoteMode),
		"Notifications: " + orDash(w.NotifyKind),
		"Backups: " + orDash(w.BackupKind),
		"Default shares appdata, system, domains and isos are created if they are missing. SMB and NFS stay off.",
	}
	if len(w.Skipped) > 0 {
		lines = append(lines, "Skipped: "+strings.Join(w.Skipped, ", "))
	}
	return lines
}

func currentLocale() string {
	if b, err := os.ReadFile("/etc/default/locale"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "LANG=") {
				return strings.Trim(strings.TrimPrefix(line, "LANG="), `"`)
			}
		}
	}
	if v := os.Getenv("LANG"); v != "" && v != "C" {
		return v
	}
	return "en_US.UTF-8"
}

func locales() []string {
	cur := currentLocale()
	base := []string{"en_US.UTF-8", "en_GB.UTF-8", "en_AU.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8", "es_ES.UTF-8", "pt_BR.UTF-8", "nl_NL.UTF-8", "it_IT.UTF-8", "sv_SE.UTF-8", "pl_PL.UTF-8", "C.UTF-8"}
	found := false
	for _, l := range base {
		if l == cur {
			found = true
			break
		}
	}
	if !found && cur != "" {
		return append([]string{cur}, base...)
	}
	return base
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// LatestRelease asks GitHub for the newest tag. Failure is not fatal.
func (s *Service) LatestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "FELDCORE")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var body struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	return body.Tag, nil
}
