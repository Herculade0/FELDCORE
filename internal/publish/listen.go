package publish

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ListenAddrs is where the publish listener binds. The default is loopback
// plus the Docker bridge addresses containers use to reach the host.
// env is FELDCORE_PUBLISH_ADDR, a comma-separated list of IPs. 0.0.0.0 and
// :: are refused. An empty env uses loopback and bridges.
func ListenAddrs(port int, env string, bridges []string) ([]string, error) {
	if port < 1 || port > 65535 {
		port = 8791
	}
	env = strings.TrimSpace(env)
	if env == "" {
		out := []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
		seen := map[string]bool{"127.0.0.1": true}
		for _, raw := range bridges {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			if seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			out = append(out, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		}
		return out, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(env, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host := raw
		p := port
		if h, portText, err := net.SplitHostPort(raw); err == nil {
			host = h
			n, nerr := strconv.Atoi(portText)
			if nerr != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("publish address %s has a bad port", raw)
			}
			p = n
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return nil, fmt.Errorf("publish address %s is not an IP", raw)
		}
		if ip.IsUnspecified() {
			return nil, fmt.Errorf("publish address %s listens on every interface", raw)
		}
		key := net.JoinHostPort(ip.String(), strconv.Itoa(p))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("publish address list is empty")
	}
	return out, nil
}

// DockerBridges returns IPv4 addresses on docker0 and br-* interfaces.
func DockerBridges() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Name != "docker0" && !strings.HasPrefix(iface.Name, "br-") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip == nil || ip.To4() == nil {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}
