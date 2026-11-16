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
		switch site.TLS {
		case "off":
			fmt.Fprintf(&b, "\nhttp://%s {\n\treverse_proxy %s\n}\n", site.Hostname, site.Upstream)
		case "acme":
			fmt.Fprintf(&b, "\n%s {\n\ttls {\n\t\tdns cloudflare {env.CF_API_TOKEN}\n\t}\n\treverse_proxy %s\n}\n", site.Hostname, site.Upstream)
		default:
			fmt.Fprintf(&b, "\n%s {\n\ttls internal\n\treverse_proxy %s\n}\n", site.Hostname, site.Upstream)
		}
	}
	return b.String(), nil
}

// dockerfile builds Caddy with the Cloudflare DNS challenge module.
// The stock caddy image cannot do DNS-01; LAN certificates do not need this.
const dockerfile = `FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/cloudflare
FROM caddy:2
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`
