// Package names validates hostnames and suggests them from a container.
package names

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hostRe is a single DNS hostname with at least two labels (app.example.com).
var hostRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

var portTag = regexp.MustCompile(`\[PORT:(\d+)\]`)
var urlPort = regexp.MustCompile(`:(\d+)(?:/|$)`)

// localSuffixes are names that only make sense on a LAN. Cloudflare DNS and
// Let's Encrypt will not serve them.
var localSuffixes = []string{".home.arpa", ".local", ".lan", ".home", ".internal", ".localdomain"}

// Hostname reports why s cannot be used as a public or LAN name.
func Hostname(s string) error {
	s = Normalise(s)
	if s == "" || len(s) > 253 || !hostRe.MatchString(s) {
		return fmt.Errorf("%q is not a hostname. Use a name like app.home.arpa or app.example.com", s)
	}
	return nil
}

// Normalise trims and lower-cases a hostname.
func Normalise(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ".")))
}

// Local reports whether host lives under a LAN-only suffix.
func Local(host string) bool {
	h := Normalise(host)
	for _, s := range localSuffixes {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// Port reports why n cannot be a TCP port.
func Port(n int) error {
	if n < 1 || n > 65535 {
		return fmt.Errorf("enter a port between 1 and 65535")
	}
	return nil
}

// Slug turns a container name into a DNS label.
func Slug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	dash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "app"
	}
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "app"
	}
	return s
}

// Suggest builds a hostname and the web port implied by an Unraid WebUI
// template. domain defaults to home.arpa. port is 0 when the template has none.
func Suggest(container, webUI, domain string) (host string, port int, from string) {
	domain = Normalise(domain)
	if domain == "" || strings.ContainsAny(domain, " /") || Hostname(domain) != nil && !strings.Contains(domain, ".") {
		domain = "home.arpa"
	}
	if strings.ContainsAny(domain, " /") || domain == "" {
		domain = "home.arpa"
	}
	host = Slug(container) + "." + domain
	if m := portTag.FindStringSubmatch(webUI); len(m) == 2 {
		p, _ := strconv.Atoi(m[1])
		if p > 0 && p <= 65535 {
			return host, p, "Taken from the app's web port."
		}
	}
	if m := urlPort.FindStringSubmatch(webUI); len(m) == 2 {
		p, _ := strconv.Atoi(m[1])
		if p > 0 && p <= 65535 {
			return host, p, "Taken from the app's web address."
		}
	}
	return host, 0, "Based on the container name. Pick the port the app's web page uses."
}

// Fingerprint is a short stable id for a config. It is not a secret and
// cannot be turned back into one.
func Fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:8])
}
