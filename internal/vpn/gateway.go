package vpn

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

// Image is the gluetun-style gateway. Its firewall is the kill switch: when
// the tunnel drops, traffic from containers sharing its network stops too.
const Image = "qmcgaw/gluetun:latest"

// Gateway is the container FELDCORE asks Docker to run for one tunnel.
// Env may contain the private key. Do not log it.
type Gateway struct {
	Name         string
	Image        string
	Env          []string
	Ports        []Port
	CapAdd       []string
	Devices      []string
	EndpointHost string // set when the endpoint was a hostname; resolve before starting
	// Start is false when the tunnel is off. The container may still be
	// created so other containers can attach to it, but it is left stopped.
	Start bool
}

// BuildGateway builds a gluetun container for t. privateKey and psk are
// secrets and are placed only in Env.
func BuildGateway(t store.VPNTunnel, privateKey, psk string) (Gateway, error) {
	if err := ValidateID(t.ID); err != nil {
		return Gateway{}, err
	}
	if privateKey == "" {
		return Gateway{}, input("This tunnel has no private key yet.")
	}
	host, port, err := SplitEndpoint(t.Endpoint)
	if err != nil {
		return Gateway{}, err
	}
	name, err := GatewayName(t.ID)
	if err != nil {
		return Gateway{}, err
	}
	addrs := strings.TrimSpace(t.Address)
	if addrs == "" {
		return Gateway{}, input("This tunnel has no address.")
	}
	allowed := strings.TrimSpace(t.AllowedIPs)
	if allowed == "" {
		allowed = "0.0.0.0/0"
	}
	env := []string{
		"VPN_SERVICE_PROVIDER=custom",
		"VPN_TYPE=wireguard",
		"WIREGUARD_PRIVATE_KEY=" + privateKey,
		"WIREGUARD_PUBLIC_KEY=" + strings.TrimSpace(t.PeerKey),
		"WIREGUARD_ADDRESSES=" + addrs,
		"WIREGUARD_ALLOWED_IPS=" + allowed,
		"WIREGUARD_ENDPOINT_PORT=" + port,
		// On by default in gluetun; set it explicitly so the kill switch does
		// not depend on an image default.
		"FIREWALL=on",
		"FIREWALL_OUTBOUND_SUBNETS=",
	}
	g := Gateway{
		Name:    name,
		Image:   Image,
		CapAdd:  []string{"NET_ADMIN"},
		Devices: []string{"/dev/net/tun"},
		Ports:   forwardsToPorts(t.Forwards),
	}
	if net.ParseIP(host) != nil {
		env = append(env, "WIREGUARD_ENDPOINT_IP="+host)
	} else {
		// Gluetun refuses a hostname in WIREGUARD_ENDPOINT_IP. The caller
		// resolves it and fills the variable in before starting.
		g.EndpointHost = host
		env = append(env, "WIREGUARD_ENDPOINT_IP=")
	}
	if psk != "" {
		env = append(env, "WIREGUARD_PRESHARED_KEY="+psk)
	}
	if t.Keepalive > 0 {
		env = append(env, "WIREGUARD_PERSISTENT_KEEPALIVE_INTERVAL="+strconv.Itoa(t.Keepalive)+"s")
	}
	if ip := firstIP(t.DNS); ip != "" {
		env = append(env, "DNS_ADDRESS="+ip)
	}
	if ports := inputPorts(g.Ports); ports != "" {
		env = append(env, "FIREWALL_INPUT_PORTS="+ports)
	}
	// Proton's forwarded port is discovered with NAT-PMP after the tunnel is
	// up. Asking gluetun to do provider port-forwarding on a custom config
	// makes it refuse to start, so that flag is left off.
	g.Env = env
	return g, nil
}

// SetEndpointIP fills the endpoint variable after a hostname is resolved.
func SetEndpointIP(g *Gateway, ip string) error {
	if net.ParseIP(ip) == nil {
		return input("The VPN server address must be an IP. Gluetun does not accept a hostname.")
	}
	for i, e := range g.Env {
		if strings.HasPrefix(e, "WIREGUARD_ENDPOINT_IP=") {
			g.Env[i] = "WIREGUARD_ENDPOINT_IP=" + ip
			g.EndpointHost = ""
			return nil
		}
	}
	g.Env = append(g.Env, "WIREGUARD_ENDPOINT_IP="+ip)
	return nil
}

func forwardsToPorts(in []store.VPNForward) []Port {
	var out []Port
	seen := map[string]bool{}
	for _, f := range in {
		if f.HostPort <= 0 {
			continue
		}
		proto := f.Proto
		if proto == "" {
			proto = "tcp"
		}
		key := fmt.Sprintf("%s:%d/%s", f.HostIP, f.HostPort, proto)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Port{HostIP: f.HostIP, Host: f.HostPort, Container: f.ContainerPort, Proto: proto})
	}
	return out
}

func inputPorts(ports []Port) string {
	var nums []string
	seen := map[int]bool{}
	for _, p := range ports {
		if p.Host <= 0 || seen[p.Host] {
			continue
		}
		seen[p.Host] = true
		nums = append(nums, strconv.Itoa(p.Host))
	}
	return strings.Join(nums, ",")
}

func firstIP(dns string) string {
	for _, part := range strings.Split(dns, ",") {
		part = strings.TrimSpace(part)
		if net.ParseIP(part) != nil {
			return part
		}
	}
	return ""
}
