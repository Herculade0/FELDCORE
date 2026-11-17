package publish

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/names"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

// InputError is a validation failure safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a failed operation safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func input(msg string) error { return &InputError{Msg: msg} }
func op(msg string) error    { return &OpError{Msg: msg} }

// Proxy is the existing web-address service. Publish reuses it for the
// route and the certificate; it does not start a second reverse proxy.
type Proxy interface {
	SetRoute(proxy.RouteInput) error
	Apply(context.Context) error
	View(context.Context) proxy.View
}

// Tunnel is the existing Cloudflare Tunnel service.
type Tunnel interface {
	SetPublicInput(remote.PublicInput) error
	Sync(context.Context) error
	View(context.Context) remote.View
}

// Resolver finds where the daemon can dial a container.
type Resolver interface {
	Upstream(ctx context.Context, container string, port int) (string, error)
}

// Auditor appends a structured event to the existing audit log.
type Auditor interface {
	Record(tokenID string, ev store.AuditEvent) error
}

// SetInput is the Publish switch on a container.
type SetInput struct {
	Container string
	Subdomain string
	Port      int
	Access    string
	Groups    []string
	Enabled   bool
	Brand     store.SignInBrand
}

// SettingsInput is the protection form. An empty licence key keeps the
// stored one. The key is never returned.
type SettingsInput struct {
	BaseDomain    string
	RatePerSec    float64
	RateBurst     int
	CountryMode   string
	Countries     []string
	GeoEnabled    bool
	LicenseKey    string
	ClearLicense  bool
	BlockBots     bool
	BlockScanners bool
}

// Outcome is the result of saving the Publish switch.
type Outcome struct {
	App     App    `json:"app"`
	Warning string `json:"warning,omitempty"`
}

// Suggestion is the name and port offered before the user edits them.
type Suggestion struct {
	Subdomain string `json:"subdomain"`
	Port      int    `json:"port"`
	From      string `json:"from"`
}

// Form is the Publish section of a container.
type Form struct {
	Container  string            `json:"container"`
	Domain     string            `json:"domain"`
	Suggestion Suggestion        `json:"suggestion"`
	Ports      []int             `json:"ports"`
	Enabled    bool              `json:"enabled"`
	Subdomain  string            `json:"subdomain"`
	Hostname   string            `json:"hostname"`
	Port       int               `json:"port"`
	Access     string            `json:"access"`
	Groups     []string          `json:"groups"`
	TunnelMode string            `json:"tunnelMode"`
	From       string            `json:"from"`
	Brand      store.SignInBrand `json:"brand"`
}

// Item is one row of the Published apps overview.
type Item struct {
	Container    string   `json:"container"`
	Subdomain    string   `json:"subdomain"`
	Hostname     string   `json:"hostname"`
	Port         int      `json:"port"`
	Target       string   `json:"target"`
	Access       string   `json:"access"`
	AccessLabel  string   `json:"accessLabel"`
	Groups       []string `json:"groups,omitempty"`
	Enabled      bool     `json:"enabled"`
	Cert         string   `json:"cert"`
	CertStatus   string   `json:"certStatus"`
	Tunnel       string   `json:"tunnel"`
	TunnelDetail string   `json:"tunnelDetail"`
}

// ProtectView is the protection form. The licence key is not included.
type ProtectView struct {
	Configured    bool     `json:"configured"`
	RatePerSec    float64  `json:"ratePerSec"`
	RateBurst     int      `json:"rateBurst"`
	CountryMode   string   `json:"countryMode"`
	Countries     []string `json:"countries"`
	GeoEnabled    bool     `json:"geoEnabled"`
	GeoReady      bool     `json:"geoReady"`
	GeoError      string   `json:"geoError,omitempty"`
	LicenseSet    bool     `json:"licenseSet"`
	BlockBots     bool     `json:"blockBots"`
	BlockScanners bool     `json:"blockScanners"`
}

// Overview is Settings → Publishing.
type Overview struct {
	BaseDomain string      `json:"baseDomain"`
	Domain     string      `json:"domain"`
	WebDomain  string      `json:"webDomain"`
	Apps       []Item      `json:"apps"`
	Protect    ProtectView `json:"protect"`
}

// Service turns the Publish switch into a proxy route, a certificate and,
// when the name is public and Remote access is on, a tunnel hostname.
// Requests for those names then pass through Handler.
type Service struct {
	Store       *store.Store
	Secrets     *secrets.Box
	Proxy       Proxy
	Remote      Tunnel
	Resolve     Resolver
	Trust       func(context.Context, net.IP) bool
	DataDir     string
	GatewayPort int
	Log         *slog.Logger
	Audit       Auditor

	// now freezes the rate limiter's clock in tests.
	now func() time.Time
	// geoOverride replaces the MaxMind file in tests.
	geoOverride Geo

	mu        sync.RWMutex
	auth      AuthHook
	guard     Protection
	once      sync.Once
	peerMu    sync.Mutex
	peers     map[string]peerHit
	geoMu     sync.Mutex
	countries *geoDB
	geoErr    string

	transportOnce sync.Once
	transport     *http.Transport
}

type peerHit struct {
	ok  bool
	exp time.Time
}

type geoSource struct{ s *Service }

func (g geoSource) Ready() bool                     { return g.s.geoReady() }
func (g geoSource) Lookup(ip net.IP) (string, bool) { return g.s.lookupCountry(ip) }

// SetAuth installs the Citizen ID hook. Nil restores AllowAll.
// It is safe to call while requests are being served.
func (s *Service) SetAuth(h AuthHook) {
	if h == nil {
		h = AllowAll{}
	}
	s.mu.Lock()
	s.auth = h
	s.mu.Unlock()
}

func (s *Service) hook() AuthHook {
	s.mu.RLock()
	h := s.auth
	s.mu.RUnlock()
	if h == nil {
		return AllowAll{}
	}
	return h
}

// Handler is the HTTP entry the proxy and the tunnel dial.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.protection()
		h := Handler{
			Lookup:  s.lookup,
			Protect: p.Check,
			Auth:    s.hook(),
			Proxy:   s.proxyTo,
			OnBlock: s.recordBlock,
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Service) protection() *Protection {
	s.once.Do(func() {
		s.guard.Policy = s.currentPolicy
		s.guard.Trust = s.trusted
		s.guard.Geo = geoSource{s}
		s.guard.Now = func() time.Time {
			if s.now != nil {
				return s.now()
			}
			return time.Now()
		}
	})
	return &s.guard
}

func (s *Service) currentPolicy() store.ProtectPolicy {
	if s.Store == nil {
		return Effective(store.ProtectPolicy{})
	}
	return Effective(s.Store.Snapshot().Publish.Protect)
}

func (s *Service) lookup(r *http.Request) (App, bool) {
	if s.Store == nil {
		return App{}, false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = names.Normalise(host)
	for _, a := range s.Store.Snapshot().Publish.Apps {
		if a.Enabled && names.Normalise(a.Hostname) == host {
			return App{
				Container: a.Container,
				Subdomain: a.Subdomain,
				Hostname:  a.Hostname,
				Port:      a.Port,
				Access:    a.Access,
				Groups:    append([]string(nil), a.Groups...),
			}, true
		}
	}
	return App{}, false
}

func (s *Service) proxyTo(w http.ResponseWriter, r *http.Request, app App) {
	if s.Resolve == nil {
		http.Error(w, "This app is not reachable.", http.StatusBadGateway)
		return
	}
	addr, err := s.Resolve.Upstream(r.Context(), app.Container, app.Port)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("publish upstream failed", "app", app.Container, "err", err)
		}
		http.Error(w, "This app is not reachable.", http.StatusBadGateway)
		return
	}
	target, err := url.Parse("http://" + addr)
	if err != nil || target.Host == "" {
		http.Error(w, "This app is not reachable.", http.StatusBadGateway)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = s.appTransport()
	rp.FlushInterval = -1
	orig := rp.Director
	rp.Director = func(req *http.Request) {
		orig(req)
		// Keep the public name. Apps generate links from Host, and the
		// dial address is an internal IP the browser should never see.
		req.Host = r.Host
		stripUpstream(req)
		req.Header.Set("X-Forwarded-Host", r.Host)
		if proto := r.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
			req.Header.Set("X-Forwarded-Proto", proto)
		} else {
			req.Header.Set("X-Forwarded-Proto", "https")
		}
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		scrubSetCookies(resp.Header)
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		if s.Log != nil {
			s.Log.Warn("publish proxy failed", "app", app.Container, "host", app.Hostname, "err", err)
		}
		http.Error(w, "This app is not reachable.", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

func (s *Service) appTransport() *http.Transport {
	s.transportOnce.Do(func() {
		s.transport = &http.Transport{
			// Do not honour HTTP_PROXY for traffic that stays on this machine.
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			IdleConnTimeout:     90 * time.Second,
			MaxIdleConnsPerHost: 8,
			ForceAttemptHTTP2:   false,
		}
	})
	return s.transport
}

func (s *Service) trusted(ctx context.Context, ip net.IP) bool {
	if ip == nil || ip.IsLoopback() {
		return ip != nil && ip.IsLoopback()
	}
	key := ip.String()
	now := time.Now()
	s.peerMu.Lock()
	if hit, ok := s.peers[key]; ok && now.Before(hit.exp) {
		s.peerMu.Unlock()
		return hit.ok
	}
	s.peerMu.Unlock()
	ok := s.Trust != nil && s.Trust(ctx, ip)
	s.peerMu.Lock()
	if s.peers == nil {
		s.peers = map[string]peerHit{}
	}
	s.peers[key] = peerHit{ok: ok, exp: now.Add(5 * time.Second)}
	s.peerMu.Unlock()
	return ok
}

func (s *Service) recordBlock(r *http.Request, app App, res Result) {
	ip := res.IP
	if ip == "" {
		ip = ClientIP(r, nil)
	}
	path := "/"
	if r.URL != nil && r.URL.Path != "" {
		path = r.URL.Path
	}
	detail := map[string]string{
		"reason": res.Reason,
		"ip":     ip,
		"host":   app.Hostname,
		"path":   oneLine(path, 200),
		"method": r.Method,
		"app":    app.Container,
		"access": app.Access,
		"ua":     oneLine(r.UserAgent(), 200),
	}
	if res.Country != "" {
		detail["country"] = res.Country
	}
	target := app.Hostname
	if r.URL != nil {
		target += r.URL.RequestURI()
	}
	ev := store.AuditEvent{
		TokenName: "protection",
		Action:    "publish.block",
		Target:    oneLine(target, 200),
		Result:    "denied",
		IP:        ip,
		Host:      app.Hostname,
		Detail:    detail,
	}
	if s.Audit != nil {
		if err := s.Audit.Record("", ev); err != nil && s.Log != nil {
			s.Log.Warn("audit log write failed", "err", err)
		}
	}
	if s.Log != nil {
		s.Log.Info("publish blocked",
			"action", ev.Action,
			"result", ev.Result,
			"reason", res.Reason,
			"ip", ip,
			"host", app.Hostname,
			"path", detail["path"],
			"app", app.Container,
			"country", res.Country,
			"access", app.Access,
		)
	}
}

// Domain is the suffix appended to a subdomain. An explicit publish domain
// wins, then the web-address domain, then home.arpa.
func (s *Service) Domain() string {
	if s == nil || s.Store == nil {
		return "home.arpa"
	}
	st := s.Store.Snapshot()
	if d := names.Normalise(st.Publish.BaseDomain); d != "" {
		return d
	}
	if d := names.Normalise(st.Web.Domain); d != "" {
		return d
	}
	return "home.arpa"
}

// Form is the Publish section for one container.
func (s *Service) Form(ctx context.Context, container, webUI string, ports []int) Form {
	domain := s.Domain()
	_, port, from := names.Suggest(container, webUI, domain)
	sub := names.Slug(container)
	if port == 0 && len(ports) > 0 {
		port = ports[0]
	}
	out := Form{
		Container:  container,
		Domain:     domain,
		Suggestion: Suggestion{Subdomain: sub, Port: port, From: from},
		Ports:      append([]int(nil), ports...),
		Subdomain:  sub,
		Hostname:   sub + "." + domain,
		Port:       port,
		Access:     AccessPublic,
		Groups:     []string{},
		TunnelMode: s.tunnelMode(ctx),
		From:       from,
	}
	if app, ok := s.find(container); ok {
		out.Enabled = app.Enabled
		if app.Subdomain != "" {
			out.Subdomain = app.Subdomain
		}
		if app.Hostname != "" {
			out.Hostname = app.Hostname
		}
		if app.Port > 0 {
			out.Port = app.Port
		}
		if app.Access != "" {
			out.Access = app.Access
		}
		out.Groups = append([]string(nil), app.Groups...)
		if out.Groups == nil {
			out.Groups = []string{}
		}
		out.Brand = app.Brand
		if out.Brand.Links == nil {
			out.Brand.Links = []store.BrandLink{}
		}
	}
	if out.Ports == nil {
		out.Ports = []int{}
	}
	if out.Brand.Links == nil {
		out.Brand.Links = []store.BrandLink{}
	}
	return out
}

// Overview lists published apps and the protection policy.
func (s *Service) Overview(ctx context.Context) Overview {
	st := store.State{}
	if s.Store != nil {
		st = s.Store.Snapshot()
	}
	webDomain := names.Normalise(st.Web.Domain)
	if webDomain == "" {
		webDomain = "home.arpa"
	}
	var pv proxy.View
	haveProxy := s.Proxy != nil
	if haveProxy {
		pv = s.Proxy.View(ctx)
	}
	var rv remote.View
	haveRemote := s.Remote != nil
	if haveRemote {
		rv = s.Remote.View(ctx)
	}
	var items []Item
	for _, app := range st.Publish.Apps {
		if !app.Enabled {
			continue
		}
		items = append(items, s.item(app, pv, rv, haveProxy, haveRemote))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Hostname < items[j].Hostname })
	if items == nil {
		items = []Item{}
	}
	stored := st.Publish.Protect
	eff := Effective(stored)
	countries := append([]string(nil), eff.Countries...)
	if countries == nil {
		countries = []string{}
	}
	return Overview{
		BaseDomain: st.Publish.BaseDomain,
		Domain:     s.Domain(),
		WebDomain:  webDomain,
		Apps:       items,
		Protect: ProtectView{
			Configured:    stored.Configured,
			RatePerSec:    eff.RatePerSec,
			RateBurst:     eff.RateBurst,
			CountryMode:   eff.CountryMode,
			Countries:     countries,
			GeoEnabled:    stored.GeoEnabled,
			GeoReady:      s.geoReady(),
			GeoError:      s.getGeoErr(),
			LicenseSet:    s.Secrets != nil && s.Secrets.Has(secretGeo),
			BlockBots:     eff.BlockBots,
			BlockScanners: eff.BlockScanners,
		},
	}
}

func (s *Service) item(app store.PublishedApp, pv proxy.View, rv remote.View, haveProxy, haveRemote bool) Item {
	cert, certStatus := describeCert(app, pv, haveProxy)
	tunnel, detail := describeTunnel(app.Hostname, haveRemote, rv)
	return Item{
		Container:    app.Container,
		Subdomain:    app.Subdomain,
		Hostname:     app.Hostname,
		Port:         app.Port,
		Target:       fmt.Sprintf("%s:%d", app.Container, app.Port),
		Access:       app.Access,
		AccessLabel:  AccessLabel(app.Access),
		Groups:       append([]string(nil), app.Groups...),
		Enabled:      true,
		Cert:         cert,
		CertStatus:   certStatus,
		Tunnel:       tunnel,
		TunnelDetail: detail,
	}
}

// Set stores the switch and wires the proxy route, the certificate and the
// optional tunnel hostname. Turning it off removes that route and hostname.
func (s *Service) Set(ctx context.Context, in SetInput) (Outcome, error) {
	next, err := s.prepare(in)
	if err != nil {
		return Outcome{}, err
	}
	if err := s.saveApp(next); err != nil {
		return Outcome{}, err
	}
	if err := s.wire(ctx, next, in.Enabled); err != nil {
		return Outcome{}, err
	}
	var warning string
	if in.Enabled && !names.Local(next.Hostname) && !s.tunnelOn(ctx) {
		warning = "The name is published on this server with HTTPS. Remote access is off, so no Cloudflare Tunnel hostname was created. No ports were opened on your router."
	}
	return Outcome{App: toApp(next), Warning: warning}, nil
}

// SaveSettings stores the base domain and the protection policy. The
// GeoLite2 database is downloaded only when country lists are turned on.
func (s *Service) SaveSettings(ctx context.Context, in SettingsInput) (Overview, error) {
	domain, err := checkDomain(in.BaseDomain)
	if err != nil {
		return Overview{}, err
	}
	if in.RatePerSec < 0 || in.RateBurst < 0 {
		return Overview{}, input("Rate limits cannot be negative.")
	}
	if in.RatePerSec > 10000 || in.RateBurst > 100000 {
		return Overview{}, input("That rate limit is too high.")
	}
	mode := strings.ToLower(strings.TrimSpace(in.CountryMode))
	switch mode {
	case "", "off":
		mode = "off"
	case "allow", "block":
	default:
		return Overview{}, input("Country list is off, allow, or block.")
	}
	countries, err := cleanCountries(in.Countries)
	if err != nil {
		return Overview{}, err
	}
	if in.GeoEnabled && mode == "allow" && len(countries) == 0 {
		return Overview{}, input("Name at least one country to allow, or turn the country list off.")
	}
	if in.GeoEnabled && mode == "block" && len(countries) == 0 {
		return Overview{}, input("Name at least one country to block, or turn the country list off.")
	}
	keyChanged := strings.TrimSpace(in.LicenseKey) != ""
	if in.ClearLicense {
		if s.Secrets == nil {
			return Overview{}, op("The secrets store is not available.")
		}
		if err := s.Secrets.Put(secretGeo, ""); err != nil {
			return Overview{}, op("Could not remove the GeoLite2 licence key.")
		}
		keyChanged = false
	}
	if key := strings.TrimSpace(in.LicenseKey); key != "" {
		if s.Secrets == nil {
			return Overview{}, op("The secrets store is not available.")
		}
		if strings.ContainsAny(key, " \r\n") {
			return Overview{}, input("The licence key should not contain spaces.")
		}
		if err := s.Secrets.Put(secretGeo, key); err != nil {
			return Overview{}, op("Could not store the GeoLite2 licence key.")
		}
		in.LicenseKey = ""
	}
	if in.GeoEnabled && (s.Secrets == nil || !s.Secrets.Has(secretGeo)) {
		return Overview{}, input("A MaxMind licence key is required before the country database can be downloaded. Country blocking stays off until then.")
	}
	wasReady := s.geoReady()
	if s.Store == nil {
		return Overview{}, op("Publishing is not available.")
	}
	if err := s.Store.Update(func(st *store.State) error {
		st.Publish.BaseDomain = domain
		st.Publish.Protect = store.ProtectPolicy{
			Configured:    true,
			RatePerSec:    in.RatePerSec,
			RateBurst:     in.RateBurst,
			CountryMode:   mode,
			Countries:     countries,
			GeoEnabled:    in.GeoEnabled,
			BlockBots:     in.BlockBots,
			BlockScanners: in.BlockScanners,
		}
		return nil
	}); err != nil {
		return Overview{}, err
	}
	if !in.GeoEnabled {
		s.setGeoErr("")
	} else if keyChanged || !wasReady {
		if err := s.refreshGeo(ctx); err != nil {
			return Overview{}, err
		}
	}
	if s.Log != nil {
		s.Log.Info("publish protection saved", "geo", in.GeoEnabled, "country", mode, "bots", in.BlockBots, "scanners", in.BlockScanners)
	}
	return s.Overview(ctx), nil
}

func (s *Service) prepare(in SetInput) (store.PublishedApp, error) {
	container := strings.TrimSpace(in.Container)
	if container == "" {
		return store.PublishedApp{}, input("Choose a container.")
	}
	brand, err := citizenid.CleanBrand(in.Brand)
	if err != nil {
		return store.PublishedApp{}, err
	}
	if !in.Enabled {
		prev, _ := s.find(container)
		prev.Container = container
		prev.Enabled = false
		if sub, host, port, access, groups, err := s.fields(in); err == nil {
			if !s.taken(host, container) {
				prev.Subdomain, prev.Hostname, prev.Port = sub, host, port
				prev.Access, prev.Groups = access, groups
			}
		}
		prev.Brand = brand
		return prev, nil
	}
	sub, host, port, access, groups, err := s.fields(in)
	if err != nil {
		return store.PublishedApp{}, err
	}
	if s.taken(host, container) {
		return store.PublishedApp{}, input(host + " is already used by another container.")
	}
	return store.PublishedApp{
		Container: container, Subdomain: sub, Hostname: host, Port: port,
		Access: access, Groups: groups, Enabled: true, Brand: brand,
	}, nil
}

func (s *Service) fields(in SetInput) (sub, host string, port int, access string, groups []string, err error) {
	sub, err = subdomain(in.Subdomain)
	if err != nil {
		return
	}
	host = sub + "." + s.Domain()
	if err = names.Hostname(host); err != nil {
		err = input(err.Error() + ".")
		return
	}
	if err = names.Port(in.Port); err != nil {
		err = input(err.Error() + ".")
		return
	}
	port = in.Port
	access, groups, err = normalizeAccess(in.Access, in.Groups)
	return
}

func (s *Service) wire(ctx context.Context, app store.PublishedApp, enabled bool) error {
	if s.Proxy == nil {
		return op("Web addresses are not available, so this cannot be published.")
	}
	if enabled && s.GatewayPort <= 0 {
		return op("The publish proxy has no port configured.")
	}
	tlsMode := "internal"
	if enabled && !names.Local(app.Hostname) {
		tlsMode = "acme"
	}
	host := app.Hostname
	if !enabled {
		// SetRoute ignores the hostname when the route is being removed.
		if host == "" {
			host = "off.invalid"
		}
	}
	if err := s.Proxy.SetRoute(proxy.RouteInput{
		Container: app.Container,
		Hostname:  host,
		Port:      app.Port,
		TLS:       tlsMode,
		Enabled:   enabled,
		Gateway:   enabled,
	}); err != nil {
		return err
	}
	if err := s.Proxy.Apply(ctx); err != nil {
		return err
	}
	if s.Remote == nil {
		return nil
	}
	on := enabled && app.Hostname != "" && !names.Local(app.Hostname) && s.tunnelOn(ctx)
	if err := s.Remote.SetPublicInput(remote.PublicInput{
		Container: app.Container,
		Hostname:  app.Hostname,
		Port:      app.Port,
		Enabled:   on,
		Gateway:   true,
	}); err != nil {
		return err
	}
	return s.Remote.Sync(ctx)
}

func (s *Service) tunnelOn(ctx context.Context) bool {
	if s.Remote == nil {
		return false
	}
	mode := s.Remote.View(ctx).Mode
	return mode != "" && mode != "off"
}

func (s *Service) tunnelMode(ctx context.Context) string {
	if s.Remote == nil {
		return "off"
	}
	mode := s.Remote.View(ctx).Mode
	if mode == "" {
		return "off"
	}
	return mode
}

func (s *Service) saveApp(app store.PublishedApp) error {
	if s.Store == nil {
		return op("Publishing is not available.")
	}
	return s.Store.Update(func(st *store.State) error {
		var next []store.PublishedApp
		for _, cur := range st.Publish.Apps {
			if cur.Container != app.Container {
				next = append(next, cur)
			}
		}
		next = append(next, app)
		st.Publish.Apps = next
		return nil
	})
}

func (s *Service) find(container string) (store.PublishedApp, bool) {
	if s.Store == nil {
		return store.PublishedApp{}, false
	}
	for _, app := range s.Store.Snapshot().Publish.Apps {
		if app.Container == container {
			return app, true
		}
	}
	return store.PublishedApp{}, false
}

func (s *Service) taken(host, container string) bool {
	if s.Store == nil {
		return false
	}
	host = names.Normalise(host)
	st := s.Store.Snapshot()
	for _, app := range st.Publish.Apps {
		if app.Enabled && app.Container != container && names.Normalise(app.Hostname) == host {
			return true
		}
	}
	if st.Web.UIEnabled && names.Normalise(st.Web.UIHostname) == host {
		return true
	}
	for _, r := range st.Web.Routes {
		if r.Enabled && r.Container != container && names.Normalise(r.Hostname) == host {
			return true
		}
	}
	for _, r := range st.Remote.Routes {
		if r.Enabled && r.Container != container && names.Normalise(r.Hostname) == host {
			return true
		}
	}
	return false
}

func toApp(a store.PublishedApp) App {
	return App{
		Container: a.Container, Subdomain: a.Subdomain, Hostname: a.Hostname,
		Port: a.Port, Access: a.Access, Groups: append([]string(nil), a.Groups...),
	}
}

func describeCert(app store.PublishedApp, pv proxy.View, ok bool) (kind, status string) {
	if !ok {
		return "missing", "missing"
	}
	var route *store.WebRoute
	for i := range pv.Routes {
		if pv.Routes[i].Enabled && pv.Routes[i].Container == app.Container {
			route = &pv.Routes[i]
			break
		}
	}
	if route == nil {
		return "missing", "missing"
	}
	switch route.TLS {
	case "off":
		kind = "http"
	case "acme":
		kind = "letsencrypt"
	default:
		kind = "local"
	}
	switch pv.Status {
	case "up":
		if kind == "local" && !pv.CAReady {
			return kind, "pending"
		}
		if kind == "http" {
			return kind, "off"
		}
		return kind, "ready"
	case "blocked":
		return kind, "blocked"
	case "down":
		return kind, "down"
	default:
		return kind, "pending"
	}
}

func describeTunnel(host string, ok bool, rv remote.View) (state, detail string) {
	if names.Local(host) {
		return "skipped", "This name only works on your home network, so the tunnel is not used."
	}
	if !ok || rv.Mode == "" || rv.Mode == "off" {
		return "off", "Remote access is off. The name is served on this server only."
	}
	found := false
	for _, r := range rv.Routes {
		if r.Enabled && names.Normalise(r.Hostname) == names.Normalise(host) {
			found = true
		}
	}
	if !found {
		return "pending", "The tunnel hostname is not published yet."
	}
	switch rv.Status.State {
	case "healthy", "up":
		if rv.Status.Summary != "" {
			return "connected", rv.Status.Summary
		}
		return "connected", "The tunnel is connected."
	case "starting":
		if rv.Status.Summary != "" {
			return "pending", rv.Status.Summary
		}
		return "pending", "Waiting for the tunnel."
	default:
		if rv.Status.Summary != "" {
			return "down", rv.Status.Summary
		}
		return "down", "The tunnel is not connected."
	}
}

var labelRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var groupRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.@-]{0,63}$`)

func subdomain(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", input("Enter a subdomain, such as plex.")
	}
	if strings.Contains(s, ".") {
		return "", input("Enter the first label only, such as plex. The base domain is added for you.")
	}
	if !labelRe.MatchString(s) {
		return "", input(s + " is not a valid subdomain. Use letters, numbers and hyphens.")
	}
	return s, nil
}

func normalizeAccess(access string, groups []string) (string, []string, error) {
	switch strings.ToLower(strings.TrimSpace(access)) {
	case "", AccessPublic:
		return AccessPublic, nil, nil
	case AccessSignedIn, "signedin", "authenticated":
		return AccessSignedIn, nil, nil
	case AccessAdmins, "admin":
		return AccessAdmins, nil, nil
	case AccessGroups, "group":
		gs, err := cleanGroups(groups)
		if err != nil {
			return "", nil, err
		}
		if len(gs) == 0 {
			return "", nil, input("Name at least one Citizen ID group.")
		}
		return AccessGroups, gs, nil
	default:
		return "", nil, input("Citizen ID access is public, signed-in, groups, or admins.")
	}
}

func cleanGroups(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		for _, part := range strings.Split(raw, ",") {
			g := strings.TrimSpace(part)
			if g == "" {
				continue
			}
			if !groupRe.MatchString(g) {
				return nil, input(g + " is not a valid group name.")
			}
			key := strings.ToLower(g)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, g)
		}
	}
	return out, nil
}

func cleanCountries(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		parts := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\n'
		})
		for _, p := range parts {
			p = strings.ToUpper(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if len(p) != 2 || p[0] < 'A' || p[0] > 'Z' || p[1] < 'A' || p[1] > 'Z' {
				return nil, input(p + " is not a two-letter country code.")
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func checkDomain(d string) (string, error) {
	d = names.Normalise(d)
	if d == "" {
		return "", nil
	}
	if strings.ContainsAny(d, " /:") || names.Hostname(d) != nil {
		return "", input("The base domain should look like home.arpa or example.com.")
	}
	return d, nil
}
