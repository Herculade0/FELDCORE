package publish

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// isCitizenPath reports whether p is the Citizen ID surface feldcored serves
// on its own port: the sign-in page, the sign-in POST, /citizen/check, and
// the passkey login posts. The publish listener does not implement those
// pages. It forwards them.
func isCitizenPath(p string) bool {
	p = cleanURLPath(p)
	return p == "/citizen" || strings.HasPrefix(p, "/citizen/")
}

func cleanURLPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// proxyCitizen forwards /citizen/* to feldcored. Tunnel ingress points at
// this listener, so the browser's sign-in request arrives here rather than
// on the management port. The public Host is preserved so the session
// cookie and the return URL match the name the browser used. The client
// address is the one this listener already accepted; the raw
// CF-Connecting-IP header is not forwarded, because feldcored trusts that
// header from loopback and this hop is loopback.
func (s *Service) proxyCitizen(w http.ResponseWriter, r *http.Request) {
	target, err := s.citizenTarget()
	if err != nil {
		http.Error(w, "Citizen ID is not reachable.", http.StatusBadGateway)
		return
	}
	host := strings.TrimSpace(r.Host)
	if strings.ContainsAny(host, " \r\n") {
		http.Error(w, "That hostname cannot be proxied.", http.StatusBadRequest)
		return
	}
	proto := ""
	if r != nil {
		proto = strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]))
	}
	switch proto {
	case "http", "https":
	default:
		proto = "http"
		if r != nil && r.TLS != nil {
			proto = "https"
		}
	}
	ip := ""
	if s != nil && r != nil {
		ip = ClientIP(r, func(peer net.IP) bool {
			return s.trusted(r.Context(), peer)
		}, func(peer net.IP) bool {
			return s.tunnelPeer(r.Context(), peer)
		})
	}
	cleaned := "/"
	query := ""
	if r != nil && r.URL != nil {
		cleaned = cleanURLPath(r.URL.Path)
		query = r.URL.RawQuery
	}
	rp := &httputil.ReverseProxy{FlushInterval: -1}
	if s != nil {
		rp.Transport = s.appTransport()
	}
	// Rewrite, not Director. Director appends this hop to X-Forwarded-For
	// after it returns, and a client can strip headers the Director added.
	// feldcored trusts forwarded headers from loopback, and this hop is
	// loopback, so the outbound headers are only the values already accepted.
	rp.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.URL.Path = cleaned
		pr.Out.URL.RawPath = ""
		pr.Out.URL.RawQuery = query
		if host != "" {
			pr.Out.Host = host
			pr.Out.Header.Set("X-Forwarded-Host", host)
		}
		pr.Out.Header.Set("X-Forwarded-Proto", proto)
		if ip != "" {
			pr.Out.Header.Set("X-Forwarded-For", ip)
		}
		pr.Out.Header.Del("CF-Connecting-IP")
	}
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		if s != nil && s.Log != nil {
			s.Log.Warn("citizen sign-in proxy failed", "err", err)
		}
		http.Error(w, "Citizen ID is not reachable.", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

func (s *Service) citizenTarget() (*url.URL, error) {
	raw := ""
	port := 8787
	if s != nil {
		raw = strings.TrimSpace(s.APIAddr)
		if s.APIPort > 0 {
			port = s.APIPort
		}
	}
	if raw == "" {
		raw = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("citizen upstream")
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}
