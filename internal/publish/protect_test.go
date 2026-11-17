package publish

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestRateLimitIsPerClientAndPerApp(t *testing.T) {
	var lim ipLimit
	now := time.Unix(1_700_000_000, 0)
	if !lim.allow("plex.example.com\n203.0.113.8", 1, 1, now) {
		t.Fatal("first request for this client was limited")
	}
	if lim.allow("plex.example.com\n203.0.113.8", 1, 1, now) {
		t.Fatal("second request for the same client was allowed")
	}
	if !lim.allow("plex.example.com\n203.0.113.9", 1, 1, now) {
		t.Fatal("a different client shared the bucket")
	}
	if !lim.allow("sonarr.example.com\n203.0.113.8", 1, 1, now) {
		t.Fatal("a different app shared the bucket")
	}
	if !lim.allow("plex.example.com\n203.0.113.8", 1, 1, now.Add(time.Second)) {
		t.Fatal("the bucket did not refill")
	}
}

func TestGeoDecision(t *testing.T) {
	cases := []struct {
		name           string
		enabled, ready bool
		mode, code     string
		known          bool
		countries      []string
		wantBlock      bool
		wantReason     string
	}{
		{name: "off ignores lists", enabled: true, ready: true, mode: "off", code: "CN", known: true, countries: []string{"US"}, wantBlock: false},
		{name: "disabled ignores lists", enabled: false, ready: true, mode: "block", code: "CN", known: true, countries: []string{"CN"}, wantBlock: false},
		{name: "allow listed", enabled: true, ready: true, mode: "allow", code: "us", known: true, countries: []string{"US", "DE"}, wantBlock: false},
		{name: "allow misses", enabled: true, ready: true, mode: "allow", code: "CN", known: true, countries: []string{"US"}, wantBlock: true, wantReason: "country"},
		{name: "allow unknown", enabled: true, ready: true, mode: "allow", known: false, countries: []string{"US"}, wantBlock: true, wantReason: "country"},
		{name: "block listed", enabled: true, ready: true, mode: "block", code: "CN", known: true, countries: []string{"cn"}, wantBlock: true, wantReason: "country"},
		{name: "block misses", enabled: true, ready: true, mode: "block", code: "US", known: true, countries: []string{"CN"}, wantBlock: false},
		{name: "block unknown stays open", enabled: true, ready: true, mode: "block", known: false, countries: []string{"CN"}, wantBlock: false},
		{name: "allow without a database fails closed", enabled: true, ready: false, mode: "allow", countries: []string{"US"}, wantBlock: true, wantReason: "geo-unavailable"},
		{name: "block without a database does nothing", enabled: true, ready: false, mode: "block", code: "CN", known: true, countries: []string{"CN"}, wantBlock: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block, reason := GeoDecision(c.enabled, c.ready, c.mode, c.countries, c.code, c.known)
			if block != c.wantBlock || reason != c.wantReason {
				t.Fatalf("block=%v reason=%q, want block=%v reason=%q", block, reason, c.wantBlock, c.wantReason)
			}
		})
	}
}

type fakeGeo struct {
	ready bool
	code  string
	known bool
}

func (f fakeGeo) Ready() bool                  { return f.ready }
func (f fakeGeo) Lookup(net.IP) (string, bool) { return f.code, f.known }

func TestProtectionGeoBlock(t *testing.T) {
	p := &Protection{
		Policy: func() store.ProtectPolicy {
			return store.ProtectPolicy{
				Configured: true, RatePerSec: 100, RateBurst: 100,
				GeoEnabled: true, CountryMode: "block", Countries: []string{"CN"},
			}
		},
		Geo: fakeGeo{ready: true, code: "CN", known: true},
	}
	req := httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil)
	req.RemoteAddr = "203.0.113.4:1000"
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res := p.Check(req, App{Hostname: "plex.example.com"})
	if !res.Block || res.Reason != "country" || res.Country != "CN" || res.Status != http.StatusForbidden {
		t.Fatalf("%+v", res)
	}

	p.Geo = fakeGeo{ready: true, code: "US", known: true}
	res = p.Check(req, App{Hostname: "plex.example.com"})
	if res.Block {
		t.Fatalf("US should pass a CN block list: %+v", res)
	}
}

func TestClientIPTrustsForwardedHeadersOnlyFromTrustedPeers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("CF-Connecting-IP", "198.51.100.9")
	req.Header.Set("X-Forwarded-For", "198.51.100.8")
	if got := ClientIP(req, func(net.IP) bool { return false }, nil); got != "203.0.113.5" {
		t.Fatalf("untrusted peer used a forwarded header: %s", got)
	}

	req.RemoteAddr = "127.0.0.1:1234"
	if got := ClientIP(req, nil, nil); got != "198.51.100.9" {
		t.Fatalf("loopback should prefer CF-Connecting-IP, got %s", got)
	}
	req.Header.Del("CF-Connecting-IP")
	if got := ClientIP(req, nil, nil); got != "198.51.100.8" {
		t.Fatalf("loopback should use X-Forwarded-For, got %s", got)
	}

	req.RemoteAddr = "172.17.0.2:1234"
	req.Header.Set("CF-Connecting-IP", "127.0.0.1")
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	proxy := func(ip net.IP) bool { return ip.Equal(net.ParseIP("172.17.0.2")) }
	if got := ClientIP(req, proxy, func(net.IP) bool { return false }); got != "203.0.113.9" {
		t.Fatalf("caddy must use the rightmost forwarded address, got %s", got)
	}
	if got := ClientIP(req, func(net.IP) bool { return false }, nil); got != "172.17.0.2" {
		t.Fatalf("same headers from an untrusted peer, got %s", got)
	}
	tunnel := func(ip net.IP) bool { return ip.Equal(net.ParseIP("172.17.0.3")) }
	req.RemoteAddr = "172.17.0.3:1234"
	req.Header.Set("CF-Connecting-IP", "198.51.100.4")
	if got := ClientIP(req, func(net.IP) bool { return false }, tunnel); got != "198.51.100.4" {
		t.Fatalf("tunnel should use CF-Connecting-IP, got %s", got)
	}
}

func TestBotsAndScannerPaths(t *testing.T) {
	p := &Protection{Policy: func() store.ProtectPolicy {
		return store.ProtectPolicy{Configured: true, RatePerSec: 100, RateBurst: 100, BlockBots: true, BlockScanners: true, CountryMode: "off"}
	}}
	app := App{Hostname: "plex.example.com"}
	empty := httptest.NewRequest(http.MethodGet, "/", nil)
	empty.RemoteAddr = "203.0.113.1:1"
	if res := p.Check(empty, app); !res.Block || res.Reason != "empty-ua" {
		t.Fatalf("empty ua %+v", res)
	}
	nmap := httptest.NewRequest(http.MethodGet, "/", nil)
	nmap.RemoteAddr = "203.0.113.1:1"
	nmap.Header.Set("User-Agent", "Nmap Scripting Engine")
	if res := p.Check(nmap, app); !res.Block || res.Reason != "bot" {
		t.Fatalf("nmap %+v", res)
	}
	scan := httptest.NewRequest(http.MethodGet, "/.env", nil)
	scan.RemoteAddr = "203.0.113.1:1"
	scan.Header.Set("User-Agent", "Mozilla/5.0")
	if res := p.Check(scan, app); !res.Block || res.Reason != "scanner" {
		t.Fatalf("scanner %+v", res)
	}
	p.Policy = func() store.ProtectPolicy {
		return store.ProtectPolicy{Configured: true, RatePerSec: 100, RateBurst: 100, BlockBots: true, CountryMode: "off"}
	}
	if res := p.Check(scan, app); res.Block {
		t.Fatalf("scanner paths are optional: %+v", res)
	}
}
