package banner

import (
	"bytes"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

// nic is one interface and the addresses currently on it.
type nic struct {
	name string
	up   bool
	ips  []net.IP
}

// HostIPv4s returns global unicast IPv4 addresses. The default-route
// interface comes first. Loopback, link-local, and virtual interfaces
// that are not the default route are left out.
func HostIPv4s() []string {
	return selectIPv4(readNics(), defaultRouteInterface(readRouteTable()))
}

func readRouteTable() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	return string(b)
}

func readNics() []nic {
	list, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]nic, 0, len(list))
	for _, ifi := range list {
		n := nic{name: ifi.Name, up: ifi.Flags&net.FlagUp != 0}
		addrs, err := ifi.Addrs()
		if err != nil {
			out = append(out, n)
			continue
		}
		for _, a := range addrs {
			if ip := addrIP(a); ip != nil {
				n.ips = append(n.ips, ip)
			}
		}
		out = append(out, n)
	}
	return out
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

// defaultRouteInterface reads /proc/net/route and returns the interface
// of the IPv4 default route with the lowest metric. An empty table
// returns "".
func defaultRouteInterface(table string) string {
	best := ""
	bestMetric := 0
	found := false
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 7 || f[0] == "Iface" {
			continue
		}
		if !strings.EqualFold(f[1], "00000000") {
			continue
		}
		metric, err := strconv.Atoi(f[6])
		if err != nil {
			continue
		}
		if !found || metric < bestMetric {
			best = f[0]
			bestMetric = metric
			found = true
		}
	}
	return best
}

// selectIPv4 orders addresses for the login banner.
func selectIPv4(nics []nic, defaultName string) []string {
	sorted := append([]nic(nil), nics...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	var primary, rest []string
	seen := map[string]bool{}
	add := func(dst *[]string, n nic) {
		ips := append([]net.IP(nil), n.ips...)
		sort.Slice(ips, func(i, j int) bool {
			return bytes.Compare(ips[i].To4(), ips[j].To4()) < 0
		})
		for _, ip := range ips {
			v4 := ip.To4()
			if v4 == nil || !v4.IsGlobalUnicast() {
				continue
			}
			s := v4.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			*dst = append(*dst, s)
		}
	}
	for _, n := range sorted {
		if n.up && n.name == defaultName {
			add(&primary, n)
		}
	}
	for _, n := range sorted {
		if !n.up || n.name == defaultName || virtualIface(n.name) {
			continue
		}
		add(&rest, n)
	}
	return append(primary, rest...)
}

// virtualIface reports names that are not a place to browse to the web UI
// unless they own the default route (the caller handles that case).
func virtualIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range []string{
		"docker", "br-", "veth", "virbr", "cni", "flannel",
		"dummy", "tunl", "sit", "podman", "nerdctl",
	} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
