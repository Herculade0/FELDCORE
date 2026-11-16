// Package vpn manages WireGuard tunnels, including configs downloaded from
// Proton VPN, and the routing decision that puts a container behind one.
package vpn

import (
	"encoding/base64"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Config is a parsed WireGuard client file. The private key and preshared
// key are secrets; callers must not log the struct or write it to state.
type Config struct {
	PrivateKey string
	Address    string
	DNS        string
	MTU        int
	ListenPort int
	PeerPublic string
	Preshared  string
	Endpoint   string
	AllowedIPs string
	Keepalive  int
}

// Parsed is a config plus what FELDCORE recognised in it.
type Parsed struct {
	Provider     string // wireguard or proton, after the preset is applied
	Detected     string // wireguard or proton, before the preset
	Server       string
	Country      string
	NATPMP       bool
	IgnoredHooks bool
	ExtraPeers   bool
	Config       Config
}

// Parse reads a WireGuard .conf. preset is "auto", "wireguard" or "proton".
// PostUp and PostDown lines are ignored: an imported file must not be able
// to run commands on the host.
func Parse(text, preset string) (Parsed, error) {
	text = strings.TrimPrefix(text, "\uFEFF")
	var iface, peer []string
	var ifaceC, peerC []string
	var peers int
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			c := strings.TrimSpace(strings.TrimLeft(line, "#;"))
			switch section {
			case "peer":
				peerC = append(peerC, c)
			default:
				ifaceC = append(ifaceC, c)
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "peer" {
				peers++
				if peers > 1 {
					// A client tunnel uses the first peer. Later peers are
					// reported and dropped rather than merged.
					section = "ignore"
				}
			}
			continue
		}
		switch section {
		case "interface":
			iface = append(iface, line)
		case "peer":
			peer = append(peer, line)
		}
	}
	if len(iface) == 0 || len(peer) == 0 {
		return Parsed{}, input("That is not a WireGuard configuration. It needs an [Interface] and a [Peer].")
	}
	cfg := Config{}
	hooks := false
	if err := applyKV(iface, &cfg, true, &hooks); err != nil {
		return Parsed{}, err
	}
	if err := applyKV(peer, &cfg, false, &hooks); err != nil {
		return Parsed{}, err
	}
	if err := validateConfig(cfg); err != nil {
		return Parsed{}, err
	}
	if strings.TrimSpace(cfg.AllowedIPs) == "" {
		cfg.AllowedIPs = "0.0.0.0/0"
	}
	detected, server, country, nat := recognise(cfg, ifaceC, peerC)
	provider := detected
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "", "auto":
	case "wireguard", "standard":
		provider = "wireguard"
	case "proton":
		provider = "proton"
	default:
		return Parsed{}, input("Choose Standard WireGuard or Proton.")
	}
	return Parsed{
		Provider: provider, Detected: detected,
		Server: server, Country: country, NATPMP: nat,
		IgnoredHooks: hooks, ExtraPeers: peers > 1, Config: cfg,
	}, nil
}

func applyKV(lines []string, cfg *Config, iface bool, hooks *bool) error {
	for _, line := range lines {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return input("Could not read a line in the WireGuard configuration.")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if i := strings.Index(val, "#"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		switch key {
		case "privatekey":
			if iface {
				cfg.PrivateKey = val
			}
		case "address":
			if iface && cfg.Address == "" {
				cfg.Address = val
			}
		case "dns":
			if iface {
				cfg.DNS = val
			}
		case "mtu":
			n, err := strconv.Atoi(val)
			if err != nil || n < 576 || n > 9000 {
				return input("MTU must be a number between 576 and 9000.")
			}
			cfg.MTU = n
		case "listenport":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 || n > 65535 {
				return input("Listen port must be between 1 and 65535.")
			}
			cfg.ListenPort = n
		case "publickey":
			if !iface {
				cfg.PeerPublic = val
			}
		case "presharedkey":
			if !iface {
				cfg.Preshared = val
			}
		case "endpoint":
			cfg.Endpoint = val
		case "allowedips":
			cfg.AllowedIPs = val
		case "persistentkeepalive":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 65535 {
				return input("Persistent keepalive must be a number of seconds.")
			}
			cfg.Keepalive = n
		case "postup", "preup", "postdown", "predown":
			*hooks = true
		}
	}
	return nil
}

func validateConfig(cfg Config) error {
	if err := checkKey("private key", cfg.PrivateKey); err != nil {
		return err
	}
	if err := checkKey("peer public key", cfg.PeerPublic); err != nil {
		return err
	}
	if cfg.Preshared != "" {
		if err := checkKey("preshared key", cfg.Preshared); err != nil {
			return err
		}
	}
	if cfg.Address == "" {
		return input("The WireGuard configuration needs an Address.")
	}
	for _, part := range strings.Split(cfg.Address, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(part); err != nil {
			return input("Address must look like 10.2.0.2/32.")
		}
	}
	if _, _, err := SplitEndpoint(cfg.Endpoint); err != nil {
		return err
	}
	return nil
}

func checkKey(what, key string) error {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return input(fmt.Sprintf("The %s is not a WireGuard key.", what))
	}
	return nil
}

// SplitEndpoint splits host:port. IPv6 addresses use [addr]:port.
func SplitEndpoint(endpoint string) (host, port string, err error) {
	endpoint = strings.TrimSpace(endpoint)
	h, p, e := net.SplitHostPort(endpoint)
	if e != nil {
		return "", "", input("Endpoint must be a host and port, for example 185.159.158.1:51820.")
	}
	n, conv := strconv.Atoi(p)
	if conv != nil || n < 1 || n > 65535 {
		return "", "", input("Endpoint port must be between 1 and 65535.")
	}
	if h == "" {
		return "", "", input("Endpoint needs a host.")
	}
	return h, p, nil
}

var (
	serverComment = regexp.MustCompile(`(?i)^([a-z]{2})(?:-[a-z0-9]+)?#\d+$`)
	protonHost    = regexp.MustCompile(`(?i)(?:^|[.-])([a-z]{2})(?:-[a-z0-9]+)*\d*\.protonvpn\.(?:net|com)`)
	natLine       = regexp.MustCompile(`(?i)(?:nat-?pmp|port forwarding)\b.*`)
)

func recognise(cfg Config, ifaceC, peerC []string) (provider, server, country string, nat bool) {
	provider = "wireguard"
	comments := append(append([]string{}, ifaceC...), peerC...)
	blob := strings.ToLower(strings.Join(comments, "\n"))
	if strings.Contains(blob, "proton") || dnsHas(cfg.DNS, "10.2.0.1") || strings.Contains(strings.ToLower(cfg.Endpoint), "protonvpn") || strings.Contains(strings.ToLower(cfg.Endpoint), "proton.me") {
		provider = "proton"
	}
	for _, c := range peerC {
		if m := serverComment.FindStringSubmatch(strings.TrimSpace(c)); m != nil {
			server = strings.TrimSpace(c)
			country = countryName(m[1])
			provider = "proton"
			break
		}
	}
	if country == "" {
		if h, _, err := SplitEndpoint(cfg.Endpoint); err == nil {
			if m := protonHost.FindStringSubmatch(h); m != nil {
				country = countryName(m[1])
				provider = "proton"
				if server == "" {
					server = h
				}
			}
		}
	}
	if country == "" {
		for _, c := range comments {
			if name, ok := countryByName(c); ok {
				country = name
				provider = "proton"
				break
			}
		}
	}
	natSet := false
	for _, c := range comments {
		if !natLine.MatchString(c) {
			continue
		}
		low := strings.ToLower(c)
		if strings.Contains(low, "= off") || strings.Contains(low, "=off") || strings.Contains(low, "disabled") {
			nat = false
			natSet = true
		}
		if strings.Contains(low, "= on") || strings.Contains(low, "=on") || strings.Contains(low, "enabled") {
			nat = true
			natSet = true
		}
	}
	_ = natSet
	return provider, strings.TrimSpace(server), country, nat
}

func dnsHas(dns, ip string) bool {
	for _, part := range strings.Split(dns, ",") {
		if strings.TrimSpace(part) == ip {
			return true
		}
	}
	return false
}

func countryName(code string) string {
	if name, ok := countries[strings.ToUpper(code)]; ok {
		return name
	}
	return strings.ToUpper(code)
}

func countryByName(comment string) (string, bool) {
	c := strings.TrimSpace(comment)
	for _, name := range countries {
		if strings.EqualFold(c, name) {
			return name, true
		}
	}
	return "", false
}

// countries covers the codes Proton uses in WireGuard server names.
var countries = map[string]string{
	"AE": "United Arab Emirates",
	"AR": "Argentina",
	"AT": "Austria",
	"AU": "Australia",
	"BE": "Belgium",
	"BR": "Brazil",
	"CA": "Canada",
	"CH": "Switzerland",
	"CL": "Chile",
	"CZ": "Czechia",
	"DE": "Germany",
	"DK": "Denmark",
	"ES": "Spain",
	"FI": "Finland",
	"FR": "France",
	"HK": "Hong Kong",
	"HU": "Hungary",
	"IE": "Ireland",
	"IL": "Israel",
	"IN": "India",
	"IS": "Iceland",
	"IT": "Italy",
	"JP": "Japan",
	"KR": "South Korea",
	"LU": "Luxembourg",
	"MX": "Mexico",
	"NL": "Netherlands",
	"NO": "Norway",
	"NZ": "New Zealand",
	"PL": "Poland",
	"PT": "Portugal",
	"RO": "Romania",
	"RS": "Serbia",
	"SE": "Sweden",
	"SG": "Singapore",
	"SI": "Slovenia",
	"SK": "Slovakia",
	"TW": "Taiwan",
	"UA": "Ukraine",
	"UK": "United Kingdom",
	"US": "United States",
	"ZA": "South Africa",
}
