package files

import (
	"net"
	"net/url"
	"strings"
)

// ForbiddenURL is a download the server will not perform. The message is
// safe to show to the person who asked for it.
type ForbiddenURL struct{ Msg string }

func (e *ForbiddenURL) Error() string { return e.Msg }

const (
	msgScheme = "Only http and https URLs can be downloaded."
	msgLocal  = "That address is blocked. Localhost, link-local and metadata addresses are never downloaded."
	msgLAN    = "That address is on a private network. An administrator can allow LAN URLs in the file browser."
	msgBad    = "That URL is not valid."
)

// blockedHosts are names that always resolve to the machine itself or to a
// cloud metadata service, whatever the DNS answer says.
var blockedHosts = map[string]bool{
	"localhost":                  true,
	"localhost.localdomain":      true,
	"metadata":                   true,
	"metadata.google.internal":   true,
	"metadata.goog":              true,
	"instance-data":              true,
	"instance-data.ec2.internal": true,
}

// CheckIP decides whether a resolved address may be dialed.
// Loopback, link-local, unspecified and metadata addresses are always
// refused. Private and shared (CGNAT) addresses are refused unless allowLAN
// is set. Metadata addresses that sit inside those ranges stay refused.
func CheckIP(ip net.IP, allowLAN bool) error {
	ip = normalizeIP(ip)
	if ip == nil || alwaysBlocked(ip) {
		return &ForbiddenURL{Msg: msgLocal}
	}
	if isLAN(ip) && !allowLAN {
		return &ForbiddenURL{Msg: msgLAN}
	}
	return nil
}

// ValidateURL accepts an http or https URL and rejects hosts that are
// obviously local or metadata. Hostnames are checked again, against every
// address they resolve to, when the connection is dialed.
func ValidateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsRune(raw, 0) {
		return nil, &ForbiddenURL{Msg: msgBad}
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return nil, &ForbiddenURL{Msg: msgBad}
	}
	if u.User != nil {
		return nil, &ForbiddenURL{Msg: msgBad}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, &ForbiddenURL{Msg: msgScheme}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return nil, &ForbiddenURL{Msg: msgBad}
	}
	if blockedHost(host) {
		return nil, &ForbiddenURL{Msg: msgLocal}
	}
	if disguisedIP(host) {
		// 127.1, 2130706433 and 0x7f000001 are IPs in some stacks and hostnames in others.
		return nil, &ForbiddenURL{Msg: msgLocal}
	}
	if ip := net.ParseIP(host); ip != nil {
		// A literal is checked with LAN disabled here only for the always-blocked
		// set. Private literals are decided by the caller, which knows the setting.
		if err := CheckIP(ip, true); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// ScreenIP checks a literal or resolved address with the LAN policy applied.
func ScreenIP(ip net.IP, allowLAN bool) error {
	return CheckIP(ip, allowLAN)
}

func blockedHost(host string) bool {
	if blockedHosts[host] {
		return true
	}
	if strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".localhost.localdomain") {
		return true
	}
	if host == "metadata.google.internal" || strings.HasSuffix(host, ".metadata.google.internal") {
		return true
	}
	if strings.HasSuffix(host, ".metadata.goog") {
		return true
	}
	return false
}

func disguisedIP(host string) bool {
	if host == "" {
		return false
	}
	if numericHost(host) && net.ParseIP(host) == nil {
		return true
	}
	h := strings.ToLower(host)
	if !strings.Contains(h, "0x") {
		return false
	}
	stripped := strings.ReplaceAll(strings.ReplaceAll(h, "0x", ""), ".", "")
	if stripped == "" {
		return true
	}
	for _, r := range stripped {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func numericHost(host string) bool {
	for _, r := range host {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return host != ""
}

func normalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

func alwaysBlocked(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8 is treated as "this host" on many stacks.
		if v4[0] == 0 || (v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255) {
			return true
		}
	}
	return isMetadata(ip)
}

func isMetadata(ip net.IP) bool {
	for _, raw := range []string{
		"169.254.169.254", // AWS, GCP, Azure, OpenStack, DigitalOcean
		"169.254.170.2",   // AWS ECS task role
		"169.254.169.253",
		"100.100.100.200", // Alibaba
		"fd00:ec2::254",   // AWS IPv6 metadata, inside the private range
	} {
		if ip.Equal(net.ParseIP(raw)) {
			return true
		}
	}
	return false
}

func isLAN(ip net.IP) bool {
	// IsPrivate is RFC 1918 and IPv6 unique-local (fc00::/7). It does not
	// include loopback or link-local, which are handled above.
	if ip.IsPrivate() {
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// Carrier-grade NAT, 100.64.0.0/10.
	return v4[0] == 100 && v4[1]&0xc0 == 64
}

// SafeURL is the URL with user info and query stripped, for logs and the UI.
func SafeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	out := *u
	out.User = nil
	out.RawQuery = ""
	out.ForceQuery = false
	out.Fragment = ""
	return out.String()
}
