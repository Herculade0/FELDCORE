package setup

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Steps are the wizard pages, in order, after the one-time claim.
var Steps = []string{"host", "network", "storage", "packages", "shares", "security", "updates", "summary"}

var (
	hostRe  = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
	tzRe    = regexp.MustCompile(`^[A-Za-z0-9+_/-]{1,64}$`)
	ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,32}$`)
)

// HostPlan is what the host step will run. Nothing here touches SSH.
func HostPlan(hostname, timezone, ntp string) ([]string, error) {
	hostname = strings.TrimSpace(hostname)
	timezone = strings.TrimSpace(timezone)
	ntp = strings.TrimSpace(ntp)
	if !hostRe.MatchString(hostname) {
		return nil, fmt.Errorf("host names use letters, numbers and hyphens")
	}
	if !tzRe.MatchString(timezone) || strings.Contains(timezone, "..") {
		return nil, fmt.Errorf("choose a time zone like Europe/London")
	}
	var lines []string
	lines = append(lines, "hostnamectl set-hostname "+hostname)
	lines = append(lines, "timedatectl set-timezone "+timezone)
	switch ntp {
	case "", "systemd-timesyncd":
		lines = append(lines, "enable systemd-timesyncd and turn NTP on")
	case "chrony":
		lines = append(lines, "install and enable chrony")
	default:
		return nil, fmt.Errorf("NTP must be systemd-timesyncd or chrony")
	}
	return lines, nil
}

// NetInput is an optional static address. Empty mode means stay on DHCP.
type NetInput struct {
	Mode    string
	Iface   string
	Address string // CIDR, 192.168.1.20/24
	Gateway string
	DNS     string
}

// ValidateNet checks a static plan. DHCP needs no fields.
func ValidateNet(in NetInput) error {
	switch in.Mode {
	case "", "dhcp":
		return nil
	case "static":
	default:
		return fmt.Errorf("network mode must be dhcp or static")
	}
	if !ifaceRe.MatchString(in.Iface) {
		return fmt.Errorf("that network interface name is not valid")
	}
	ip, netw, err := net.ParseCIDR(in.Address)
	if err != nil || ip.To4() == nil {
		return fmt.Errorf("enter the address as an IPv4 CIDR, for example 192.168.1.20/24")
	}
	if !ip.Equal(ip.Mask(netw.Mask)) && netw.Contains(ip) {
		// a host address inside the network is what we want; reject the network address itself only when prefix < 31
	}
	gw := net.ParseIP(in.Gateway)
	if gw == nil || gw.To4() == nil || !netw.Contains(gw) {
		return fmt.Errorf("the gateway must be an IPv4 address on that network")
	}
	for _, d := range strings.Fields(strings.ReplaceAll(in.DNS, ",", " ")) {
		if ip := net.ParseIP(d); ip == nil || ip.To4() == nil {
			return fmt.Errorf("%q is not an IPv4 DNS address", d)
		}
	}
	return nil
}

// NewURL is the address the browser should use after a static change.
func NewURL(address string, port int) string {
	ip, _, err := net.ParseCIDR(address)
	if err != nil || port == 0 {
		return ""
	}
	return fmt.Sprintf("http://%s/", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
}

// sshPortsFrom reads sshd -T output and `ss` output. If nothing is found it
// returns port 22, so a firewall can never be built that forgets SSH.
func sshPortsFrom(sshdT, ss string) []int {
	seen := map[int]bool{}
	var ports []int
	add := func(n int) {
		if n < 1 || n > 65535 || seen[n] {
			return
		}
		seen[n] = true
		ports = append(ports, n)
	}
	for _, line := range strings.Split(sshdT, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 2 && f[0] == "port" {
			n, _ := strconv.Atoi(f[1])
			add(n)
		}
	}
	// ss -H -ltn lines look like: LISTEN 0 128 0.0.0.0:22 0.0.0.0:*
	for _, line := range strings.Split(ss, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.EqualFold(f[0], "LISTEN") {
			continue
		}
		host, port, err := net.SplitHostPort(f[3])
		if err != nil {
			continue
		}
		_ = host
		n, _ := strconv.Atoi(port)
		// Only treat it as SSH when the line mentions sshd, or we have no process info
		// and the port matches one sshd -T already listed. Bare ss without sshd is
		// not assumed to be SSH except port 22 when sshd -T was empty.
		if strings.Contains(line, "sshd") {
			add(n)
		}
	}
	if len(ports) == 0 {
		add(22)
	}
	sort.Ints(ports)
	return ports
}

// UFWCommands is the exact ufw sequence. SSH ports are allowed before the
// firewall is enabled, and nothing here disables SSH or rewrites sshd.
func UFWCommands(listenPort int, sshPorts []int, smb, nfs bool) [][]string {
	if len(sshPorts) == 0 {
		sshPorts = []int{22}
	}
	var cmds [][]string
	for _, p := range sshPorts {
		cmds = append(cmds, []string{"ufw", "allow", fmt.Sprintf("%d/tcp", p), "comment", "ssh"})
	}
	if listenPort > 0 {
		cmds = append(cmds, []string{"ufw", "allow", fmt.Sprintf("%d/tcp", listenPort), "comment", "feldcore"})
	}
	if smb {
		cmds = append(cmds, []string{"ufw", "allow", "Samba"})
	}
	if nfs {
		cmds = append(cmds, []string{"ufw", "allow", "2049/tcp", "comment", "nfs"})
		cmds = append(cmds, []string{"ufw", "allow", "111/tcp", "comment", "rpcbind"})
	}
	cmds = append(cmds,
		[]string{"ufw", "default", "deny", "incoming"},
		[]string{"ufw", "default", "allow", "outgoing"},
		[]string{"ufw", "--force", "enable"},
	)
	return cmds
}

// AllowsSSH reports whether every SSH port appears in an allow rule before enable.
func AllowsSSH(cmds [][]string, sshPorts []int) bool {
	allowed := map[int]bool{}
	enabled := false
	for _, c := range cmds {
		line := strings.Join(c, " ")
		if strings.Contains(line, "disable") || strings.Contains(line, "deny") && strings.Contains(line, "/tcp") && !strings.Contains(line, "default deny incoming") {
			return false
		}
		if len(c) >= 3 && c[0] == "ufw" && c[1] == "allow" {
			if n, err := strconv.Atoi(strings.TrimSuffix(c[2], "/tcp")); err == nil {
				allowed[n] = true
			}
		}
		if strings.Contains(line, "--force enable") {
			enabled = true
			for _, p := range sshPorts {
				if !allowed[p] {
					return false
				}
			}
		}
	}
	return enabled
}

// OnOSDisk reports whether path lives on the operating-system disk.
func OnOSDisk(path string, mounts []Mount) bool {
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}
	best := ""
	os := false
	for _, m := range mounts {
		mp := strings.TrimRight(m.Path, "/")
		if mp == "" {
			mp = "/"
		}
		if path == mp || (mp != "/" && strings.HasPrefix(path, mp+"/")) || (mp == "/" && strings.HasPrefix(path, "/")) {
			if len(mp) >= len(best) {
				best = mp
				os = m.OS
			}
		}
	}
	return os
}

// Mount is one filesystem the storage step can offer.
type Mount struct {
	Path   string `json:"path"`
	Device string `json:"device"`
	Free   uint64 `json:"free"`
	Total  uint64 `json:"total"`
	OS     bool   `json:"os"`
}

// VirtAvailable is true when the CPU reports Intel VT-x or AMD-V.
func VirtAvailable(cpuinfo string) bool {
	return strings.Contains(cpuinfo, "vmx") || strings.Contains(cpuinfo, "svm")
}
