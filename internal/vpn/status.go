package vpn

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// Sample is what the gateway reported. Bodies are the raw control-server
// JSON. WGDump is `wg show all dump` when the gateway has the wg tool.
type Sample struct {
	Running     bool
	WGDump      string
	VPNStatus   string
	PublicBody  string
	ForwardBody string
	NATPMPPort  int
}

// Status is what the VPN page shows. It never includes key material.
type Status struct {
	ID            string `json:"id"`
	State         string `json:"state"` // off, down, up
	PublicIP      string `json:"publicIp,omitempty"`
	Handshake     int64  `json:"handshake,omitempty"`
	HandshakeAge  int    `json:"handshakeAge,omitempty"` // seconds; -1 when the tunnel is up but no timestamp was reported
	ForwardedPort int    `json:"forwardedPort,omitempty"`
	KillSwitch    bool   `json:"killSwitch"`
	Server        string `json:"server,omitempty"`
	Country       string `json:"country,omitempty"`
	Provider      string `json:"provider"`
	Message       string `json:"message,omitempty"`
}

// handshakeStale is how long a WireGuard session can go without a handshake
// before FELDCORE treats the tunnel as down. Keepalives are usually 25s.
const handshakeStale = 180

// Interpret turns a probe into the status the UI shows.
func Interpret(t store.VPNTunnel, master bool, sample Sample, now time.Time) Status {
	st := Status{
		ID: t.ID, Provider: t.Provider, Server: t.Server, Country: t.Country,
		KillSwitch: true, HandshakeAge: -1,
	}
	if !master || !t.Enabled {
		st.State = "off"
		st.HandshakeAge = 0
		st.Message = "This tunnel is off. Containers routed through it are not started."
		return st
	}
	if !sample.Running {
		st.State = "down"
		st.HandshakeAge = 0
		st.Message = "The VPN gateway is not running. The kill switch leaves routed containers with no network."
		return st
	}
	if hs, ok := parseHandshake(sample.WGDump); ok {
		st.Handshake = hs
		if hs > 0 {
			age := int(now.Sub(time.Unix(hs, 0)).Seconds())
			if age < 0 {
				age = 0
			}
			st.HandshakeAge = age
		} else {
			st.HandshakeAge = 0
		}
	}
	st.PublicIP = parsePublicIP(sample.PublicBody)
	st.ForwardedPort = sample.NATPMPPort
	if st.ForwardedPort == 0 {
		st.ForwardedPort = parseForwardPort(sample.ForwardBody)
	}
	vpnState := parseVPNState(sample.VPNStatus)
	hasHS := st.Handshake > 0
	stale := hasHS && st.HandshakeAge > handshakeStale
	switch {
	case stale:
		st.State = "down"
		st.Message = "The last WireGuard handshake is too old. The kill switch is holding traffic."
	case vpnState == "stopped":
		st.State = "down"
		st.Message = "The tunnel is stopped inside the gateway. Routed containers have no network."
	case hasHS && !stale:
		st.State = "up"
		st.Message = "Tunnel is up. Routed containers use this gateway and lose their network if it drops."
	case vpnState == "running" || st.PublicIP != "":
		st.State = "up"
		if st.HandshakeAge < 0 {
			st.Message = "The gateway reports the tunnel is up. It did not report a handshake time."
		}
	default:
		st.State = "down"
		st.HandshakeAge = 0
		st.Message = "The gateway is running but the tunnel has not come up. Routed containers have no network."
	}
	return st
}

// parseHandshake reads `wg show all dump`. Peer lines carry the latest
// handshake as a unix time at field 5. Zero means there has never been one.
func parseHandshake(dump string) (int64, bool) {
	var best int64
	found := false
	for _, line := range strings.Split(dump, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		n, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil {
			continue
		}
		found = true
		if n > best {
			best = n
		}
	}
	return best, found
}

func parseVPNState(body string) string {
	var m struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(body), &m) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m.Status))
}

func parsePublicIP(body string) string {
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return ""
	}
	for _, k := range []string{"public_ip", "publicIp", "ip"} {
		s, _ := m[k].(string)
		if net.ParseIP(strings.TrimSpace(s)) != nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func parseForwardPort(body string) int {
	body = strings.TrimSpace(body)
	if body == "" {
		return 0
	}
	if n, err := strconv.Atoi(body); err == nil && n > 0 && n <= 65535 {
		return n
	}
	var m struct {
		Port  int   `json:"port"`
		Ports []int `json:"ports"`
	}
	if json.Unmarshal([]byte(body), &m) != nil {
		return 0
	}
	if m.Port > 0 && m.Port <= 65535 {
		return m.Port
	}
	for _, p := range m.Ports {
		if p > 0 && p <= 65535 {
			return p
		}
	}
	return 0
}
