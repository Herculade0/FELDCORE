package proxy

import (
	"fmt"
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
// the directives.
func writeProxy(b *strings.Builder, site Site) {
	if site.Gate == "" {
		fmt.Fprintf(b, "\treverse_proxy %s\n", site.Upstream)
		return
	}
	fmt.Fprintf(b, "\troute {\n")
	fmt.Fprintf(b, "\t\treverse_proxy /citizen/* %s\n", site.Gate)
	fmt.Fprintf(b, "\t\tforward_auth %s {\n", site.Gate)
	fmt.Fprintf(b, "\t\t\turi /citizen/check\n")
	fmt.Fprintf(b, "\t\t\tcopy_headers Cookie\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Host {http.request.host}\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Uri {http.request.uri}\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Proto {http.request.scheme}\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\treverse_proxy %s\n", site.Upstream)
	fmt.Fprintf(b, "\t}\n")
}

// dockerfile builds Caddy with the Cloudflare DNS challenge module.
// The stock caddy image cannot do DNS-01; LAN certificates do not need this.
const dockerfile = `FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/cloudflare
FROM caddy:2
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`
