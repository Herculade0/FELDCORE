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
	// KeepCookies is set when Upstream is the publish listener. That listener
	// still needs the citizen cookie, and it strips the cookie itself before
	// the request reaches the container. A direct upstream always strips.
	KeepCookies bool
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
	fmt.Fprintf(b, "\t\treverse_proxy /citizen/* %s\n", site.Gate)
	fmt.Fprintf(b, "\t\tforward_auth %s {\n", site.Gate)
	fmt.Fprintf(b, "\t\t\turi /citizen/check\n")
	fmt.Fprintf(b, "\t\t\tcopy_headers Cookie\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Host {http.request.host}\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Uri {http.request.uri}\n")
	fmt.Fprintf(b, "\t\t\theader_up X-Forwarded-Proto {http.request.scheme}\n")
	fmt.Fprintf(b, "\t\t}\n")
	writeAppProxy(b, site, "\t\t")
	fmt.Fprintf(b, "\t}\n")
}

func writeAppProxy(b *strings.Builder, site Site, pad string) {
	if site.KeepCookies {
		fmt.Fprintf(b, "%sreverse_proxy %s\n", pad, site.Upstream)
		return
	}
	writeCookieScrub(b, pad)
	fmt.Fprintf(b, "%sreverse_proxy %s {\n", pad, site.Upstream)
	fmt.Fprintf(b, "%s\theader_up -Authorization\n", pad)
	fmt.Fprintf(b, "%s\theader_up Cookie {clean_cookie}\n", pad)
	fmt.Fprintf(b, "%s}\n", pad)
}

// writeCookieScrub removes only the Citizen ID and management cookies.
// Other cookies belong to the app and are forwarded.
func writeCookieScrub(b *strings.Builder, pad string) {
	names := []string{"feldcore_citizen", "feldcore_desk", "feldcore_session"}
	in := "{http.request.header.Cookie}"
	for i, name := range names {
		out := fmt.Sprintf("{c%d}", i)
		if i == len(names)-1 {
			out = "{clean_cookie}"
		}
		fmt.Fprintf(b, "%smap %s %s {\n", pad, in, out)
		fmt.Fprintf(b, "%s\t~(?i)^(?:__Host-)?%s=[^;]*(?:;\\s*(.*))?$ \"{1}\"\n", pad, name)
		fmt.Fprintf(b, "%s\t~(?i)^(.*?);\\s*(?:__Host-)?%s=[^;]*(?:;\\s*(.*))?$ \"{1}; {2}\"\n", pad, name)
		fmt.Fprintf(b, "%s\tdefault %s\n", pad, in)
		fmt.Fprintf(b, "%s}\n", pad)
		in = out
	}
}

// dockerfile builds Caddy with the Cloudflare DNS challenge module.
// The stock caddy image cannot do DNS-01; LAN certificates do not need this.
const dockerfile = `FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/cloudflare
FROM caddy:2
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`
