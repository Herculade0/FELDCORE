package publish

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/time/rate"

	"github.com/Herculade0/feldcore/internal/store"
)

// Result is the protection decision for one request.
type Result struct {
	Block   bool
	Status  int
	Reason  string
	Msg     string
	IP      string
	Country string
}

// Geo resolves a client address to an ISO country code.
// Ready is false until a database has been loaded. Lookup's boolean is
// false when the address has no country (private ranges, unknown).
type Geo interface {
	Ready() bool
	Lookup(net.IP) (code string, known bool)
}

// Protection is the per-app check that runs after route lookup and before
// Citizen ID: bots, optional scanner paths, optional country lists, then a
// token bucket per client IP.
type Protection struct {
	Policy func() store.ProtectPolicy
	// Trust is asked only for non-loopback peers. Loopback is always trusted.
	Trust func(context.Context, net.IP) bool
	Geo   Geo
	Now   func() time.Time

	limits ipLimit
}

// Check decides whether the request may continue.
// The client IP is the TCP peer unless that peer is loopback, Caddy, or
// cloudflared, in which case CF-Connecting-IP wins and then X-Forwarded-For.
func (p *Protection) Check(r *http.Request, app App) Result {
	pol := Effective(p.policy())
	ip := ClientIP(r, func(peer net.IP) bool {
		if p.Trust == nil {
			return false
		}
		return p.Trust(r.Context(), peer)
	})
	if pol.BlockBots {
		if emptyAgent(r.UserAgent()) {
			return Result{Block: true, Status: http.StatusForbidden, Reason: "empty-ua", Msg: "Blocked.", IP: ip}
		}
		if badAgent(r.UserAgent()) {
			return Result{Block: true, Status: http.StatusForbidden, Reason: "bot", Msg: "Blocked.", IP: ip}
		}
	}
	if pol.BlockScanners && scannerPath(r.URL.Path) {
		return Result{Block: true, Status: http.StatusForbidden, Reason: "scanner", Msg: "Blocked.", IP: ip}
	}
	if pol.GeoEnabled && pol.CountryMode != "" && pol.CountryMode != "off" {
		code, known := "", false
		ready := p.Geo != nil && p.Geo.Ready()
		if ready {
			if parsed := net.ParseIP(ip); parsed != nil {
				code, known = p.Geo.Lookup(parsed)
			}
		}
		if block, reason := GeoDecision(true, ready, pol.CountryMode, pol.Countries, code, known); block {
			res := Result{Block: true, Status: http.StatusForbidden, Reason: reason, Msg: "Blocked.", IP: ip}
			if known {
				res.Country = strings.ToUpper(code)
			}
			return res
		}
	}
	if !p.limits.allow(app.Hostname+"\n"+ip, pol.RatePerSec, pol.RateBurst, p.now()) {
		return Result{Block: true, Status: http.StatusTooManyRequests, Reason: "rate", Msg: "Too many requests.", IP: ip}
	}
	return Result{IP: ip}
}

func (p *Protection) policy() store.ProtectPolicy {
	if p == nil || p.Policy == nil {
		return Effective(store.ProtectPolicy{})
	}
	return p.Policy()
}

func (p *Protection) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Effective fills the defaults used until the protection form has been saved,
// and replaces empty limits after that. Country blocking stays off unless
// it was explicitly saved.
func Effective(p store.ProtectPolicy) store.ProtectPolicy {
	if !p.Configured {
		return store.ProtectPolicy{
			RatePerSec:    10,
			RateBurst:     30,
			CountryMode:   "off",
			BlockBots:     true,
			BlockScanners: false,
		}
	}
	if p.RatePerSec <= 0 {
		p.RatePerSec = 10
	}
	if p.RatePerSec > 10000 {
		p.RatePerSec = 10000
	}
	if p.RateBurst <= 0 {
		p.RateBurst = 30
	}
	if p.RateBurst > 100000 {
		p.RateBurst = 100000
	}
	switch p.CountryMode {
	case "allow", "block":
	default:
		p.CountryMode = "off"
	}
	return p
}

// GeoDecision is the country-list rule.
// An allow list fails closed when the database is not loaded, because
// otherwise the list would not be a list. A block list does nothing until
// the database can answer. Unknown countries (private addresses, no record)
// are outside an allow list and outside a block list.
func GeoDecision(enabled, ready bool, mode string, countries []string, code string, known bool) (block bool, reason string) {
	if !enabled {
		return false, ""
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "off" {
		return false, ""
	}
	if mode != "allow" && mode != "block" {
		return false, ""
	}
	if !ready {
		if mode == "allow" {
			return true, "geo-unavailable"
		}
		return false, ""
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if !known {
		code = ""
	}
	listed := false
	if code != "" {
		for _, c := range countries {
			if strings.EqualFold(strings.TrimSpace(c), code) {
				listed = true
				break
			}
		}
	}
	switch mode {
	case "allow":
		if code == "" || !listed {
			return true, "country"
		}
	case "block":
		if listed {
			return true, "country"
		}
	}
	return false, ""
}

// ClientIP is the address protection and the audit log attribute to the
// caller. CF-Connecting-IP and X-Forwarded-For are used only when the TCP
// peer is loopback or trust reports it (Caddy and the Cloudflare Tunnel
// connector). Any other peer is itself the client, and those headers are
// ignored so they cannot be spoofed.
func ClientIP(r *http.Request, trust func(net.IP) bool) string {
	peer := remoteIP(r)
	trusted := peer != nil && (peer.IsLoopback() || (trust != nil && trust(peer)))
	if trusted {
		if ip := parseIPHeader(r.Header.Get("CF-Connecting-IP")); ip != nil {
			return ip.String()
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := parseIPHeader(first); ip != nil {
				return ip.String()
			}
		}
	}
	if peer != nil {
		return peer.String()
	}
	return ""
}

func remoteIP(r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func parseIPHeader(s string) net.IP {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	return net.ParseIP(s)
}

func emptyAgent(ua string) bool {
	ua = strings.TrimSpace(ua)
	return ua == "" || ua == "-"
}

// badAgents are scanners and aggressive crawlers, matched as substrings.
// Ordinary clients (browsers, curl used by a person, an app's own updater)
// are not in this list.
var badAgents = []string{
	"sqlmap",
	"nikto",
	"nmap",
	"masscan",
	"zgrab",
	"nuclei",
	"dirbuster",
	"gobuster",
	"wfuzz",
	"acunetix",
	"nessus",
	"openvas",
	"semrush",
	"ahrefsbot",
	"mj12bot",
	"dotbot",
	"petalbot",
	"bytespider",
	"gptbot",
	"ccbot",
	"scrapy",
	"libwww-perl",
	"wpscan",
	"censys",
	"shodan",
	"zmeu",
	"morfeus",
	"jorgee",
}

func badAgent(ua string) bool {
	u := strings.ToLower(ua)
	for _, n := range badAgents {
		if strings.Contains(u, n) {
			return true
		}
	}
	return false
}

// scannerPaths are probes that have no place on a published app. They are
// optional: BlockScanners is off until someone turns it on.
var scannerPaths = []string{
	"/wp-admin",
	"/wp-login.php",
	"/xmlrpc.php",
	"/.env",
	"/.git",
	"/.svn",
	"/.aws",
	"/phpmyadmin",
	"/vendor/phpunit",
	"/boaform",
	"/cgi-bin",
	"/actuator",
	"/server-status",
}

func scannerPath(path string) bool {
	p := strings.ToLower(path)
	if p == "" {
		p = "/"
	}
	for _, n := range scannerPaths {
		if p == n || strings.HasPrefix(p, n+"/") {
			return true
		}
		if strings.HasPrefix(n, "/.") && strings.HasPrefix(p, n) {
			return true
		}
	}
	return false
}

type slot struct {
	lim   *rate.Limiter
	per   rate.Limit
	burst int
	seen  time.Time
}

type ipLimit struct {
	mu    sync.Mutex
	m     map[string]*slot
	swept time.Time
}

func (l *ipLimit) allow(key string, perSec float64, burst int, now time.Time) bool {
	if perSec <= 0 {
		perSec = 10
	}
	if burst < 1 {
		burst = 1
	}
	lim := rate.Limit(perSec)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*slot{}
	}
	s := l.m[key]
	if s == nil || s.per != lim || s.burst != burst {
		s = &slot{lim: rate.NewLimiter(lim, burst), per: lim, burst: burst}
		l.m[key] = s
	}
	s.seen = now
	if l.swept.IsZero() || now.Sub(l.swept) > time.Minute {
		l.swept = now
		for k, v := range l.m {
			if now.Sub(v.seen) > 10*time.Minute {
				delete(l.m, k)
			}
		}
	}
	return s.lim.AllowN(now, 1)
}

func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
	if n > 0 && len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) && s != "" {
			s = s[:len(s)-1]
		}
	}
	return s
}
