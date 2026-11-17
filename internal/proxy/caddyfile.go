package proxy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Site is one hostname the proxy should serve.
type Site struct {
	Hostname  string
	Upstream  string // host:port, no scheme
	TLS       string // off, internal, acme
	Container string
	// Gate, when set, is the Citizen ID forward_auth upstream (host:port).
	// Empty leaves the site public, which is the default.
	Gate string
	// KeepCookies is set when Upstream is the publish listener. That hop still
	// needs the citizen cookie. Desk, session and passkey cookies are stripped
	// here, and the listener strips every credential cookie before the app.
	KeepCookies bool
	// Passthrough is the management UI. Its cookies reach feldcored. Forwarded
	// headers are still overwritten so a browser cannot supply them.
	Passthrough bool
}

// Build renders a Caddyfile. The Cloudflare token is never written here;
// ACME sites refer to the CF_API_TOKEN environment variable.
func Build(email string, sites []Site) (string, error) {
	if len(sites) == 0 {
		return "", nil
	}
	ordered := append([]Site(nil), sites...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Hostname < ordered[j].Hostname })
	var b strings.Builder
	b.WriteString("{\n\tadmin off\n")
	if email != "" {
		if strings.ContainsAny(email, "\n{} ") {
			return "", fmt.Errorf("the certificate email must not contain spaces")
		}
		fmt.Fprintf(&b, "\temail %s\n", email)
	}
	b.WriteString("}\n")
	for _, site := range ordered {
		if strings.ContainsAny(site.Hostname, "\n {}") || strings.ContainsAny(site.Upstream, "\n {}") {
			return "", fmt.Errorf("refusing to write a hostname that could change the proxy config")
		}
		if site.Gate != "" && strings.ContainsAny(site.Gate, "\n {}") {
			return "", fmt.Errorf("refusing to write a gate address that could change the proxy config")
		}
		switch site.TLS {
		case "off":
			fmt.Fprintf(&b, "\nhttp://%s {\n", site.Hostname)
		case "acme":
			fmt.Fprintf(&b, "\n%s {\n\ttls {\n\t\tdns cloudflare {env.CF_API_TOKEN}\n\t}\n", site.Hostname)
		default:
			fmt.Fprintf(&b, "\n%s {\n\ttls internal\n", site.Hostname)
		}
		writeProxy(&b, site)
		b.WriteString("}\n")
	}
	return b.String(), nil
}

// writeProxy emits the upstream. A gated site asks Citizen ID first, and
// still serves the sign-in page itself so the browser can authenticate on
// this hostname. route keeps that order; Caddy would otherwise reorder
// the directives. The app upstream drops the Citizen ID cookie and
// Authorization so a published app cannot replay them.
func writeProxy(b *strings.Builder, site Site) {
	if site.Gate == "" {
		writeAppProxy(b, site, "\t")
		return
	}
	fmt.Fprintf(b, "\troute {\n")
	fmt.Fprintf(b, "\t\treverse_proxy /citizen/* %s {\n", site.Gate)
	writeForwarded(b, "\t\t\t")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tforward_auth %s {\n", site.Gate)
	fmt.Fprintf(b, "\t\t\turi /citizen/check\n")
	fmt.Fprintf(b, "\t\t\tcopy_headers Cookie\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Uri {http.request.uri}\n")
	writeForwarded(b, "\t\t\t")
	fmt.Fprintf(b, "\t\t}\n")
	writeAppProxy(b, site, "\t\t")
	fmt.Fprintf(b, "\t}\n")
}

func writeAppProxy(b *strings.Builder, site Site, pad string) {
	fmt.Fprintf(b, "%sreverse_proxy %s {\n", pad, site.Upstream)
	writeForwarded(b, pad+"\t")
	if !site.Passthrough {
		fmt.Fprintf(b, "%s\theader_up -Authorization\n", pad)
		writeCookieReplace(b, pad+"\t", site.KeepCookies)
	}
	fmt.Fprintf(b, "%s}\n", pad)
}

// writeForwarded overwrites the headers a browser can set. CF-Connecting-IP
// is removed on every Caddy hop. The tunnel connector, which dials the
// publish listener itself, is the only path that may send it.
func writeForwarded(b *strings.Builder, pad string) {
	fmt.Fprintf(b, "%sheader_up -CF-Connecting-IP\n", pad)
	fmt.Fprintf(b, "%sheader_up X-Forwarded-For {http.request.remote.host}\n", pad)
	fmt.Fprintf(b, "%sheader_up X-Forwarded-Proto {http.request.scheme}\n", pad)
	fmt.Fprintf(b, "%sheader_up X-Forwarded-Host {http.request.host}\n", pad)
}

// cookieScrubPattern matches every FELDCORE credential cookie. Caddy's
// header_up replacement uses the same RE2 expression and replaces every match.
func cookieScrubPattern(keepCitizen bool) string {
	names := "feldcore_citizen|feldcore_desk|feldcore_session|feldcore_passkey"
	if keepCitizen {
		names = "feldcore_desk|feldcore_session|feldcore_passkey"
	}
	return `(?i)(?:^|;\s*)(?:__Host-)?(?:` + names + `)=[^;]*`
}

func writeCookieReplace(b *strings.Builder, pad string, keepCitizen bool) {
	fmt.Fprintf(b, "%sheader_up Cookie \"%s\" \"\"\n", pad, cookieScrubPattern(keepCitizen))
	fmt.Fprintf(b, "%sheader_up Cookie \"^\\s*;\\s*|\\s*;\\s*$\" \"\"\n", pad)
	fmt.Fprintf(b, "%sheader_up Cookie \";\\s*;\" \";\"\n", pad)
}

// ScrubCookies applies the same replacements the Caddyfile emits.
func ScrubCookies(header string, keepCitizen bool) string {
	header = regexp.MustCompile(cookieScrubPattern(keepCitizen)).ReplaceAllString(header, "")
	header = regexp.MustCompile(`^\s*;\s*|\s*;\s*$`).ReplaceAllString(header, "")
	header = regexp.MustCompile(`;\s*;`).ReplaceAllString(header, ";")
	return strings.TrimSpace(header)
}

// dockerfile builds Caddy with the Cloudflare DNS challenge module.
// The stock caddy image cannot do DNS-01; LAN certificates do not need this.
const dockerfile = `FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/cloudflare
FROM caddy:2
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`
