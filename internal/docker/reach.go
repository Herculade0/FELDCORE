package docker

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Upstream is the host:port the daemon can dial to reach a container port.
// Host-network containers are on this machine. Every other mode uses the
// container's own address, preferring the FELDCORE proxy network.
func (m *Manager) Upstream(ctx context.Context, name string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", invalid("Enter a port between 1 and 65535.")
	}
	if name == "" {
		return "", invalid("Choose a container.")
	}
	in, err := m.inspect(ctx, name)
	if err != nil {
		return "", err
	}
	mode := strOf(in.HostConfig["NetworkMode"])
	if mode == "" || mode == "default" {
		mode = "bridge"
	}
	if mode == "none" || strings.HasPrefix(mode, "container:") {
		return "", invalid("%s has no network of its own, so a published name cannot reach it.", name)
	}
	if mode == "host" {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
	}
	ip := ""
	if n, ok := in.NetworkSettings.Networks[ProxyNetwork]; ok {
		ip = strOf(n["IPAddress"])
	}
	if ip == "" {
		names := make([]string, 0, len(in.NetworkSettings.Networks))
		for name := range in.NetworkSettings.Networks {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if v := strOf(in.NetworkSettings.Networks[name]["IPAddress"]); v != "" {
				ip = v
				break
			}
		}
	}
	if ip == "" {
		return "", &OpError{Msg: name + " has no IP address yet. Start it, then try again."}
	}
	return net.JoinHostPort(ip, strconv.Itoa(port)), nil
}

// TrustedPeer reports whether ip is this machine or one of the proxies
// FELDCORE runs (Caddy and cloudflared). Forwarded client headers are
// honoured only from those peers, so a client that dials the publish
// listener directly cannot spoof CF-Connecting-IP or X-Forwarded-For.
func (m *Manager) TrustedPeer(ctx context.Context, ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, name := range []string{ProxyContainer, TunnelContainer} {
		in, err := m.inspect(ctx, name)
		if err != nil {
			continue
		}
		for _, n := range in.NetworkSettings.Networks {
			if got := net.ParseIP(strOf(n["IPAddress"])); got != nil && got.Equal(ip) {
				return true
			}
		}
	}
	return false
}
