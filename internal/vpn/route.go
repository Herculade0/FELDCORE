package vpn

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Labels stored on a container so an edit can put the form back together.
// Docker has nowhere else to keep "this container is behind that tunnel".
const (
	LabelTunnel  = "feldcore.vpn"
	LabelKill    = "feldcore.vpn.killswitch"
	LabelNetwork = "feldcore.vpn.network"
	LabelPorts   = "feldcore.vpn.ports"
	LabelGateway = "feldcore.vpn.gateway"
)

// GatewayPrefix is the container name prefix for a FELDCORE tunnel.
const GatewayPrefix = "feldcore-vpn-"

// Port is a published port that must move onto the VPN gateway. A container
// that joins another container's network cannot publish ports itself.
type Port struct {
	HostIP    string `json:"hostIp,omitempty"`
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Proto     string `json:"proto"`
}

// Workload is the routing choice on a container form.
type Workload struct {
	TunnelID string
	Network  string
	Ports    []Port
}

// Plan is how that container is attached. KillSwitch is true whenever the
// container is routed: it has no network of its own, so a dead tunnel means
// no traffic.
type Plan struct {
	ThroughVPN  bool
	KillSwitch  bool
	NetworkMode string
	Publish     []Port
	PortLabel   string
}

// Choice is one tunnel in the container form's list.
type Choice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Server   string `json:"server,omitempty"`
	Country  string `json:"country,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// ValidateID checks a tunnel id. Ids are also used as secret file names.
func ValidateID(id string) error {
	if id == "" || len(id) > 32 {
		return input("Choose a VPN tunnel, or leave routing off.")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return input("Choose a VPN tunnel, or leave routing off.")
		}
	}
	return nil
}

// GatewayName is the Docker name of the tunnel's gateway container.
func GatewayName(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return GatewayPrefix + id, nil
}

// Route decides the network mode for a container. An empty tunnel id keeps
// the container on the network the user picked. A tunnel id always attaches
// it to that tunnel's gateway and drops its own port publishes.
func Route(w Workload) (Plan, error) {
	if w.TunnelID == "" {
		mode := strings.TrimSpace(w.Network)
		if mode == "" {
			mode = "bridge"
		}
		return Plan{NetworkMode: mode}, nil
	}
	name, err := GatewayName(w.TunnelID)
	if err != nil {
		return Plan{}, err
	}
	var publish []Port
	for _, p := range w.Ports {
		if p.Host <= 0 {
			continue
		}
		if p.Proto == "" {
			p.Proto = "tcp"
		}
		publish = append(publish, p)
	}
	label := ""
	if len(publish) > 0 {
		raw, err := json.Marshal(publish)
		if err != nil {
			return Plan{}, err
		}
		label = string(raw)
	}
	return Plan{
		ThroughVPN:  true,
		KillSwitch:  true,
		NetworkMode: "container:" + name,
		Publish:     publish,
		PortLabel:   label,
	}, nil
}

// DecodePorts reads the port label written by Route.
func DecodePorts(label string) ([]Port, error) {
	if strings.TrimSpace(label) == "" {
		return nil, nil
	}
	var ports []Port
	if err := json.Unmarshal([]byte(label), &ports); err != nil {
		return nil, fmt.Errorf("port label: %w", err)
	}
	return ports, nil
}

// Blocked reports whether the kill switch must refuse to start a container.
// The tunnel has to be switched on and reporting up. There is no fallback
// onto the host network.
func Blocked(masterOn, tunnelOn, tunnelUp bool) bool {
	return !masterOn || !tunnelOn || !tunnelUp
}
