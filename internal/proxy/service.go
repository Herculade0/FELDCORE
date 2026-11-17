// Package proxy serves friendly names for containers with Caddy.
//
// Caddy runs as a container FELDCORE manages. The stock image issues a local
// certificate authority, which is what a home network needs, and renews those
// certificates itself. Public names use Let's Encrypt via the Cloudflare
// DNS-01 challenge, which needs a Caddy build that includes that module.
// FELDCORE builds that image the first time a public certificate is asked for.
// Nothing is published until a hostname is turned on.
package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/names"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

const secretAPI = "cf-api"

// InputError is safe to show to the person using the page.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a failed operation safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func input(msg string) error { return &InputError{Msg: msg} }
func op(msg string) error    { return &OpError{Msg: msg} }

// Engine runs the Caddy container.
type Engine interface {
	Ready(ctx context.Context) bool
	ApplySystem(ctx context.Context, spec docker.SystemSpec) error
	StopSystem(ctx context.Context, name string) error
	SystemState(ctx context.Context, name string) (string, error)
	EnsureNetwork(ctx context.Context, name string) error
	ConnectNetwork(ctx context.Context, network, container string) error
	NetworkMode(ctx context.Context, name string) (string, error)
	PublishedPorts(ctx context.Context) ([]docker.PublishedPort, error)
	BuildImage(ctx context.Context, tag, dir string) error
	ImagePresent(ctx context.Context, ref string) bool
}

// Service is the reverse proxy.
type Service struct {
	Store      *store.Store
	Secrets    *secrets.Box
	Engine     Engine
	DataDir    string
	ListenPort int
	// GatewayPort is feldcored's publish listener. Routes with Gateway set
	// are reverse-proxied there so protection and Citizen ID run before the
	// container. Zero keeps the older behaviour: Caddy dials the container.
	GatewayPort int
	Log         *slog.Logger
	// Listen reports host ports already in use. Nil uses the kernel tables.
	Listen func() []int
}

// SaveInput is the web-address form for the server itself.
type SaveInput struct {
	Email      string
	Domain     string
	UIHostname string
	UIEnabled  bool
	UITLS      string
}

// View is the web-address card.
type View struct {
	Enabled    bool             `json:"enabled"`
	Email      string           `json:"email"`
	Domain     string           `json:"domain"`
	UIHostname string           `json:"uiHostname"`
	UIEnabled  bool             `json:"uiEnabled"`
	UITLS      string           `json:"uiTls"`
	Status     string           `json:"status"`
	Summary    string           `json:"summary"`
	CAReady    bool             `json:"caReady"`
	Conflicts  []Conflict       `json:"conflicts"`
	Routes     []store.WebRoute `json:"routes"`
}

// RouteInput is one container's hostname.
type RouteInput struct {
	Container string
	Hostname  string
	Port      int
	TLS       string
	Enabled   bool
	// Gateway sends the hostname to feldcored's publish listener.
	Gateway bool
}

// View returns the current proxy settings.
func (s *Service) View(ctx context.Context) View {
	cfg := s.Store.Snapshot().Web
	domain := cfg.Domain
	if domain == "" {
		domain = "home.arpa"
	}
	uiHost := cfg.UIHostname
	if uiHost == "" {
		uiHost = "feldcore." + domain
	}
	tls := cfg.UITLS
	if tls == "" {
		tls = "internal"
	}
	routes := append([]store.WebRoute(nil), cfg.Routes...)
	if routes == nil {
		routes = []store.WebRoute{}
	}
	conflicts := s.conflicts(ctx)
	status, summary := s.status(ctx, cfg, conflicts)
	return View{
		Enabled: len(enabledRoutes(cfg)) > 0 || cfg.UIEnabled, Email: cfg.Email, Domain: domain,
		UIHostname: uiHost, UIEnabled: cfg.UIEnabled, UITLS: tls,
		Status: status, Summary: summary, CAReady: s.caReady(),
		Conflicts: conflicts, Routes: routes,
	}
}

// Save stores the server-wide web address settings and applies them.
func (s *Service) Save(ctx context.Context, in SaveInput) error {
	if in.UIEnabled {
		if err := names.Hostname(in.UIHostname); err != nil {
			return input(err.Error() + ".")
		}
		if err := checkTLS(in.UIHostname, in.UITLS); err != nil {
			return err
		}
	}
	domain := names.Normalise(in.Domain)
	if domain == "" {
		domain = "home.arpa"
	}
	if strings.ContainsAny(domain, " /") {
		return input("The home domain should look like home.arpa or example.com.")
	}
	if err := s.Store.Update(func(st *store.State) error {
		st.Web.Email = strings.TrimSpace(in.Email)
		st.Web.Domain = domain
		st.Web.UIHostname = names.Normalise(in.UIHostname)
		st.Web.UIEnabled = in.UIEnabled
		if in.UITLS != "" {
			st.Web.UITLS = in.UITLS
		} else if st.Web.UITLS == "" {
			st.Web.UITLS = "internal"
		}
		st.Web.Enabled = st.Web.UIEnabled || len(enabledRoutes(st.Web)) > 0
		return nil
	}); err != nil {
		return err
	}
	return s.Apply(ctx)
}

// SetRoute stores one container's hostname and applies the proxy.
func (s *Service) SetRoute(in RouteInput) error {
	if in.Container == "" {
		return input("Choose a container.")
	}
	host := names.Normalise(in.Hostname)
	tls := in.TLS
	if tls == "" {
		tls = "internal"
	}
	if in.Enabled {
		if err := names.Hostname(host); err != nil {
			return input(err.Error() + ".")
		}
		if err := names.Port(in.Port); err != nil {
			return input(err.Error() + ".")
		}
		if err := checkTLS(host, tls); err != nil {
			return err
		}
	}
	return s.Store.Update(func(st *store.State) error {
		var next []store.WebRoute
		for _, r := range st.Web.Routes {
			if r.Container != in.Container {
				next = append(next, r)
			}
		}
		if in.Enabled {
			next = append(next, store.WebRoute{
				Container: in.Container, Hostname: host, Port: in.Port, TLS: tls, Enabled: true,
				Gateway: in.Gateway,
			})
		}
		st.Web.Routes = next
		st.Web.Enabled = st.Web.UIEnabled || len(enabledRoutes(st.Web)) > 0
		return nil
	})
}

// Apply writes the Caddyfile and starts, reloads or stops the proxy container.
func (s *Service) Apply(ctx context.Context) error {
	cfg := s.Store.Snapshot().Web
	sites, err := s.sites(ctx, cfg)
	if err != nil {
		return err
	}
	if len(sites) == 0 {
		if s.Engine != nil && s.Engine.Ready(ctx) {
			if err := s.Engine.StopSystem(ctx, docker.ProxyContainer); err != nil {
				return op("Could not stop the web proxy.")
			}
		}
		return nil
	}
	if conflicts := s.conflicts(ctx); len(conflicts) > 0 {
		return input(conflicts[0].Explanation)
	}
	needsACME := false
	for _, site := range sites {
		if site.TLS == "acme" {
			needsACME = true
		}
	}
	var token string
	if needsACME {
		if !strings.Contains(cfg.Email, "@") {
			return input("Enter an email address on the web addresses page. Let's Encrypt uses it for expiry notices.")
		}
		var ok bool
		token, ok, err = s.Secrets.Get(secretAPI)
		if err != nil || !ok || token == "" {
			return input("A public certificate needs the Cloudflare API token from Remote access. Add it there, then try again.")
		}
	}
	file, err := Build(cfg.Email, sites)
	if err != nil {
		return input(err.Error())
	}
	if strings.Contains(file, token) && token != "" {
		return op("Refusing to write the proxy config because it would contain a token.")
	}
	if err := os.MkdirAll(s.dataPath(), 0o700); err != nil {
		return op("Could not create the proxy folder.")
	}
	if err := os.WriteFile(s.caddyfile(), []byte(file), 0o644); err != nil {
		return op("Could not write the proxy config.")
	}
	if s.Engine == nil {
		return op("Docker is not available, so the web proxy cannot start.")
	}
	if !s.Engine.Ready(ctx) {
		return op("Docker is not running. Web addresses need Docker so Caddy can run as a container.")
	}
	image := docker.CaddyImage
	var env []string
	if needsACME {
		image = docker.CaddyPluginImage
		env = []string{"CF_API_TOKEN=" + token}
		if !s.Engine.ImagePresent(ctx, image) {
			if err := os.WriteFile(filepath.Join(s.caddyDir(), "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
				return op("Could not write the proxy build file.")
			}
			if err := s.Engine.BuildImage(ctx, image, s.caddyDir()); err != nil {
				return op("Could not build Caddy with the Cloudflare DNS plugin. " + err.Error())
			}
		}
	}
	spec := docker.SystemSpec{
		Name:    docker.ProxyContainer,
		Image:   image,
		Command: []string{"run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"},
		Binds: []string{
			s.caddyfile() + ":/etc/caddy/Caddyfile:ro",
			s.dataPath() + ":/data",
			filepath.Join(s.caddyDir(), "config") + ":/config",
		},
		Env:        env,
		Ports:      []docker.SystemPort{{Host: 80, Container: 80}, {Host: 443, Container: 443}},
		Network:    docker.ProxyNetwork,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Role:       "proxy",
		Hash:       names.Fingerprint(file, image, names.Fingerprint(token)),
	}
	if err := os.MkdirAll(filepath.Join(s.caddyDir(), "config"), 0o700); err != nil {
		return op("Could not create the proxy folder.")
	}
	if err := s.Engine.ApplySystem(ctx, spec); err != nil {
		return op("Could not start the web proxy. " + secrets.Redact(err.Error(), token))
	}
	for _, r := range cfg.Routes {
		if !r.Enabled {
			continue
		}
		_ = s.Attach(ctx, r.Container)
	}
	if s.Log != nil {
		s.Log.Info("web proxy applied", "sites", len(sites), "acme", needsACME)
	}
	return nil
}

// Attach connects one container to the proxy network when a hostname points
// at it. A private app bridge is a normal Docker network, so the proxy reaches
// the app by name once the app is also on the proxy network. That is the same
// path a container on any user-defined network already uses.
//
// Host and none have no second network. A container that shares another
// container's network (a VPN workload, network mode container:<gateway>)
// cannot join one either; its published port stays on the gateway.
func (s *Service) Attach(ctx context.Context, name string) error {
	if s == nil || s.Engine == nil || name == "" || s.Store == nil {
		return nil
	}
	want := false
	for _, r := range s.Store.Snapshot().Web.Routes {
		if r.Enabled && r.Container == name {
			want = true
			break
		}
	}
	if !want {
		return nil
	}
	mode, err := s.Engine.NetworkMode(ctx, name)
	if err != nil || mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
		return nil
	}
	return s.Engine.ConnectNetwork(ctx, docker.ProxyNetwork, name)
}

// URLFor returns the browser URL for a container, or "" when it has no name.
func (s *Service) URLFor(container string) string {
	for _, r := range s.Store.Snapshot().Web.Routes {
		if r.Container == container && r.Enabled && r.Hostname != "" {
			if r.TLS == "off" {
				return "http://" + r.Hostname
			}
			return "https://" + r.Hostname
		}
	}
	return ""
}

// CAFile is the local CA root, once Caddy has created it.
func (s *Service) CAFile() string {
	return filepath.Join(s.dataPath(), "caddy", "pki", "authorities", "local", "root.crt")
}

func (s *Service) caReady() bool {
	fi, err := os.Stat(s.CAFile())
	return err == nil && fi.Size() > 0
}

func (s *Service) sites(ctx context.Context, cfg store.WebConfig) ([]Site, error) {
	var sites []Site
	if cfg.UIEnabled {
		host := cfg.UIHostname
		if host == "" {
			domain := cfg.Domain
			if domain == "" {
				domain = "home.arpa"
			}
			host = "feldcore." + domain
		}
		tls := cfg.UITLS
		if tls == "" {
			tls = "internal"
		}
		sites = append(sites, Site{
			Hostname: host, Upstream: fmt.Sprintf("host.docker.internal:%d", s.listen()), TLS: tls,
			Passthrough: true,
		})
	}
	for _, r := range cfg.Routes {
		if !r.Enabled {
			continue
		}
		up := fmt.Sprintf("%s:%d", r.Container, r.Port)
		if s.Engine != nil {
			mode, err := s.Engine.NetworkMode(ctx, r.Container)
			if err == nil && mode == "host" {
				up = fmt.Sprintf("host.docker.internal:%d", r.Port)
			}
			if err == nil && mode == "none" {
				return nil, input(r.Container + " has no network, so a hostname cannot reach it.")
			}
		}
		// Publish routes stop at feldcored. Caddy still obtains the certificate
		// and terminates TLS; the listener runs protection, Citizen ID, then
		// the proxy to the container. Nothing new is placed in front of Caddy.
		keep := false
		if r.Gateway && s.GatewayPort > 0 {
			up = fmt.Sprintf("host.docker.internal:%d", s.GatewayPort)
			keep = true
		}
		tls := r.TLS
		if tls == "" {
			tls = "internal"
		}
		sites = append(sites, Site{
			Hostname: r.Hostname, Upstream: up, TLS: tls, Container: r.Container,
			Gate: s.gate(r.Container), KeepCookies: keep,
		})
	}
	return sites, nil
}

func (s *Service) conflicts(ctx context.Context) []Conflict {
	var published []docker.PublishedPort
	running := false
	if s.Engine != nil {
		if ports, err := s.Engine.PublishedPorts(ctx); err == nil {
			published = ports
		}
		if st, err := s.Engine.SystemState(ctx, docker.ProxyContainer); err == nil && st == "running" {
			running = true
		}
	}
	listening := []int(nil)
	if s.Listen != nil {
		listening = s.Listen()
	} else {
		listening = TCPListeners(80, 443)
	}
	out := Explain(published, listening, running)
	if out == nil {
		out = []Conflict{}
	}
	return out
}

func (s *Service) status(ctx context.Context, cfg store.WebConfig, conflicts []Conflict) (string, string) {
	if !cfg.UIEnabled && len(enabledRoutes(cfg)) == 0 {
		return "off", "No hostnames yet. Nothing is published. Add a name on a container, or serve this page on HTTPS."
	}
	if len(conflicts) > 0 {
		return "blocked", conflicts[0].Explanation
	}
	if s.Engine == nil || !s.Engine.Ready(ctx) {
		return "down", "Docker is not running. The web proxy runs as a Caddy container, so it needs Docker."
	}
	st, err := s.Engine.SystemState(ctx, docker.ProxyContainer)
	if err != nil || st != "running" {
		return "down", "The web proxy is not running."
	}
	return "up", "Caddy is serving your hostnames."
}

func (s *Service) listen() int {
	if s.ListenPort > 0 {
		return s.ListenPort
	}
	return 8787
}

func (s *Service) caddyDir() string { return filepath.Join(s.DataDir, "caddy") }
func (s *Service) dataPath() string { return filepath.Join(s.caddyDir(), "data") }
func (s *Service) caddyfile() string {
	return filepath.Join(s.caddyDir(), "Caddyfile")
}

// gate is the Citizen ID forward_auth upstream when this container is not public.
// An open web route with no grant and no publish app stays open. A publish
// access or an app grant that is not public is gated, and an empty level on
// a record that exists is admin.
func (s *Service) gate(container string) string {
	if s.Store == nil {
		return ""
	}
	level, _, set := citizenid.Effective(s.Store.Snapshot(), container)
	if set && citizenid.Gated(level) {
		return fmt.Sprintf("host.docker.internal:%d", s.listen())
	}
	return ""
}

func enabledRoutes(cfg store.WebConfig) []store.WebRoute {
	var out []store.WebRoute
	for _, r := range cfg.Routes {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func checkTLS(host, tls string) error {
	switch tls {
	case "", "off", "internal", "acme":
	default:
		return input("Choose plain HTTP, the local certificate, or Let's Encrypt.")
	}
	if tls == "acme" && names.Local(host) {
		return input(host + " is a home-network name. Use the local certificate (or plain HTTP). Let's Encrypt only signs public domains.")
	}
	return nil
}
