// Package remote manages a Cloudflare Tunnel so FELDCORE can publish
// hostnames without opening ports. Nothing is published until a person asks.
package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/appliance"
	"github.com/Herculade0/feldcore/internal/cfapi"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/names"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	secretAPI    = "cf-api"
	secretTunnel = "cf-tunnel"

	// AdminWarning is shown whenever the FELDCORE page itself is published.
	AdminWarning = "This publishes the FELDCORE admin page on the internet. Anyone who can open that hostname can try to sign in. Put Cloudflare Access in front of it (Zero Trust → Access → Applications) so visitors pass an identity check before they reach the login page. No other container is exposed unless you turn that on for it."
)

// InputError is a validation failure safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a failed operation safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func input(msg string) error { return &InputError{Msg: msg} }
func op(msg string) error    { return &OpError{Msg: msg} }

// Engine starts the cloudflared container on a host that is not FELDCORE OS
// and does not have the baked connector. It also attaches apps to the proxy
// network so the publish listener can reach them.
type Engine interface {
	Ready(ctx context.Context) bool
	ApplySystem(ctx context.Context, spec docker.SystemSpec) error
	StopSystem(ctx context.Context, name string) error
	SystemState(ctx context.Context, name string) (string, error)
	EnsureNetwork(ctx context.Context, name string) error
	ConnectNetwork(ctx context.Context, network, container string) error
	NetworkMode(ctx context.Context, name string) (string, error)
}

// Binary runs a cloudflared program. FELDCORE OS always uses it. Other hosts
// use it when the baked program is installed, and when Docker is not running.
type Binary interface {
	Available() bool
	Running() bool
	Start(ctx context.Context, token string) error
	Stop() error
	Install(ctx context.Context) error
	Detail() string
}

// Service is the tunnel.
type Service struct {
	Store      *store.Store
	Secrets    *secrets.Box
	Engine     Engine
	Binary     Binary
	DataDir    string
	ListenPort int
	// GatewayPort is feldcored's publish listener. A route with Gateway set
	// is published at that port instead of at the container, so the tunnel
	// passes through protection and Citizen ID. The host binary dials
	// loopback; the container dials host.docker.internal.
	GatewayPort int
	Log         *slog.Logger
	// PreferHost forces the baked cloudflared program instead of a container.
	// Nil selects it on FELDCORE OS, and on any host where that program is installed.
	PreferHost func() bool
	// NewCF overrides the Cloudflare client. Tests point it at a fake server.
	NewCF func(token string) *cfapi.Client
}

// SaveInput is the Remote access form. Empty token fields keep the stored value.
type SaveInput struct {
	Mode             string
	APIToken         string
	TunnelToken      string
	ClearAPIToken    bool
	ClearTunnelToken bool
	AccountID        string
	TunnelName       string
	ExposeAdmin      bool
	AdminHostname    string
}

// Health is the connector's current state.
type Health struct {
	State       string `json:"state"`
	Summary     string `json:"summary"`
	Connections int    `json:"connections"`
	Connector   string `json:"connector"`
	Detail      string `json:"detail,omitempty"`
}

// View is the Remote access page. It never contains a token.
type View struct {
	Mode           string              `json:"mode"`
	TunnelID       string              `json:"tunnelId,omitempty"`
	TunnelName     string              `json:"tunnelName,omitempty"`
	AccountID      string              `json:"accountId,omitempty"`
	APITokenSet    bool                `json:"apiTokenSet"`
	TunnelTokenSet bool                `json:"tunnelTokenSet"`
	ExposeAdmin    bool                `json:"exposeAdmin"`
	AdminHostname  string              `json:"adminHostname,omitempty"`
	AdminWarning   string              `json:"adminWarning,omitempty"`
	RunsOnHost     bool                `json:"runsOnHost"`
	Routes         []store.PublicRoute `json:"routes"`
	Status         Health              `json:"status"`
}

// PublicHost is the internet hostname configured for a container, or "".
func (s *Service) PublicHost(container string) string {
	for _, r := range s.Store.Snapshot().Remote.Routes {
		if r.Enabled && r.Container == container {
			return r.Hostname
		}
	}
	return ""
}
func (s *Service) View(ctx context.Context) View {
	cfg := s.Store.Snapshot().Remote
	mode := cfg.Mode
	if mode == "" {
		mode = "off"
	}
	routes := make([]store.PublicRoute, 0)
	for _, r := range cfg.Routes {
		if r.Enabled {
			routes = append(routes, store.PublicRoute{
				Container: r.Container, Hostname: r.Hostname, Port: r.Port, Enabled: true,
			})
		}
	}
	v := View{
		Mode: mode, TunnelID: cfg.TunnelID, TunnelName: cfg.TunnelName, AccountID: cfg.AccountID,
		APITokenSet: s.Secrets.Has(secretAPI), TunnelTokenSet: s.Secrets.Has(secretTunnel),
		ExposeAdmin: cfg.ExposeAdmin, AdminHostname: cfg.AdminHostname,
		RunsOnHost: s.preferHost(),
		Routes:     routes, Status: s.health(ctx),
	}
	if cfg.ExposeAdmin {
		v.AdminWarning = AdminWarning
	}
	return v
}

// Save stores the form and applies it. Tokens are encrypted and never logged.
func (s *Service) Save(ctx context.Context, in SaveInput) error {
	switch in.Mode {
	case "", "off", "token", "api":
	default:
		return input("Choose off, a tunnel token, or an API token.")
	}
	if in.Mode == "" {
		in.Mode = "off"
	}
	if in.ExposeAdmin {
		if err := names.Hostname(in.AdminHostname); err != nil {
			return input(err.Error() + ".")
		}
		if names.Local(in.AdminHostname) {
			return input("Cloudflare cannot publish " + in.AdminHostname + ". Use a domain you manage in Cloudflare.")
		}
	}
	if in.Mode == "token" && strings.TrimSpace(in.TunnelToken) == "" && !s.Secrets.Has(secretTunnel) {
		return input("Paste the tunnel token from the Cloudflare dashboard (Zero Trust → Networks → Tunnels).")
	}
	if in.Mode == "api" && strings.TrimSpace(in.APIToken) == "" && !s.Secrets.Has(secretAPI) {
		return input("Paste a Cloudflare API token with permission to edit tunnels and DNS.")
	}
	if tok := strings.TrimSpace(in.APIToken); tok != "" {
		if err := s.Secrets.Put(secretAPI, tok); err != nil {
			return op("Could not store the API token.")
		}
	}
	if in.ClearAPIToken {
		_ = s.Secrets.Put(secretAPI, "")
	}
	if tok := strings.TrimSpace(in.TunnelToken); tok != "" {
		if err := s.Secrets.Put(secretTunnel, tok); err != nil {
			return op("Could not store the tunnel token.")
		}
	}
	if in.ClearTunnelToken {
		_ = s.Secrets.Put(secretTunnel, "")
	}
	name := strings.TrimSpace(in.TunnelName)
	if name == "" {
		name = "feldcore"
	}
	if err := s.Store.Update(func(st *store.State) error {
		st.Remote.Mode = in.Mode
		if st.Remote.TunnelName == "" || in.TunnelName != "" {
			st.Remote.TunnelName = name
		}
		if id := strings.TrimSpace(in.AccountID); id != "" {
			st.Remote.AccountID = id
		}
		st.Remote.ExposeAdmin = in.ExposeAdmin
		st.Remote.AdminHostname = names.Normalise(in.AdminHostname)
		return nil
	}); err != nil {
		return err
	}
	if s.Log != nil {
		s.Log.Info("remote access saved", "mode", in.Mode, "exposeAdmin", in.ExposeAdmin)
	}
	return s.Sync(ctx)
}

// PublicInput is one container's tunnel hostname.
// Gateway sends the tunnel to FELDCORE's publish listener instead of the
// container, so protection and Citizen ID run before the app.
type PublicInput struct {
	Container string
	Hostname  string
	Port      int
	Enabled   bool
	Gateway   bool
}

// SetPublic records one container's public hostname. Sync publishes it.
func (s *Service) SetPublic(container, host string, port int, enabled bool) error {
	return s.SetPublicInput(PublicInput{Container: container, Hostname: host, Port: port, Enabled: enabled})
}

// SetPublicInput is SetPublic with the option to enter through the publish proxy.
func (s *Service) SetPublicInput(in PublicInput) error {
	host := names.Normalise(in.Hostname)
	if in.Enabled {
		if err := names.Hostname(host); err != nil {
			return input(err.Error() + ".")
		}
		if names.Local(host) {
			return input(host + " only works on your home network. To expose it, use a domain Cloudflare manages.")
		}
		if err := names.Port(in.Port); err != nil {
			return input(err.Error() + ".")
		}
	}
	if in.Container == "" {
		return input("Choose a container.")
	}
	return s.Store.Update(func(st *store.State) error {
		var next []store.PublicRoute
		for _, r := range st.Remote.Routes {
			if r.Container != in.Container {
				next = append(next, r)
				continue
			}
			if in.Enabled && r.Hostname != host && r.RecordID != "" {
				old := r
				old.Enabled = false
				next = append(next, old)
			}
			if !in.Enabled {
				r.Enabled = false
				next = append(next, r)
			}
		}
		if in.Enabled {
			next = append(next, store.PublicRoute{
				Container: in.Container, Hostname: host, Port: in.Port, Enabled: true, Gateway: in.Gateway,
			})
		}
		st.Remote.Routes = next
		return nil
	})
}

// Sync starts or stops the connector and writes ingress and DNS.
func (s *Service) Sync(ctx context.Context) error {
	cfg := s.Store.Snapshot().Remote
	if cfg.Mode == "" || cfg.Mode == "off" {
		return s.stop(ctx)
	}
	if cfg.Mode == "api" {
		if _, ok, _ := s.Secrets.Get(secretTunnel); !ok || cfg.TunnelID == "" {
			if err := s.ensureTunnel(ctx); err != nil {
				return err
			}
		}
	}
	token, ok, err := s.Secrets.Get(secretTunnel)
	if err != nil {
		return op("Could not read the stored tunnel token.")
	}
	if !ok || token == "" {
		return input("There is no tunnel token yet.")
	}
	if account, id := tunnelIDs(token); id != "" {
		_ = s.Store.Update(func(st *store.State) error {
			if st.Remote.TunnelID == "" {
				st.Remote.TunnelID = id
			}
			if st.Remote.AccountID == "" {
				st.Remote.AccountID = account
			}
			return nil
		})
	}
	if err := s.writeToken(token); err != nil {
		return err
	}
	if err := s.start(ctx, token); err != nil {
		return err
	}
	api, has, err := s.Secrets.Get(secretAPI)
	if err != nil {
		return op("Could not read the stored API token.")
	}
	if !has {
		return nil
	}
	return s.publish(ctx, api)
}

// Install downloads cloudflared when this host runs the connector as a
// container and Docker is not up. FELDCORE OS already has the program.
func (s *Service) Install(ctx context.Context) error {
	if s.preferHost() {
		if s.Binary == nil || !s.Binary.Available() {
			return op("FELDCORE OS includes cloudflared " + BakedCloudflaredVersion + " at " + bakedCloudflared + ", and that program is missing.")
		}
		return s.Sync(ctx)
	}
	if s.Engine != nil && s.Engine.Ready(ctx) {
		return input("Docker is running, so FELDCORE uses a cloudflared container. There is nothing to install on the host.")
	}
	if s.Binary == nil {
		return op("This install cannot download cloudflared.")
	}
	if err := s.Binary.Install(ctx); err != nil {
		return op(err.Error())
	}
	return s.Sync(ctx)
}

func (s *Service) ensureTunnel(ctx context.Context) error {
	api, ok, err := s.Secrets.Get(secretAPI)
	if err != nil || !ok {
		return input("Paste a Cloudflare API token so FELDCORE can create the tunnel.")
	}
	cfg := s.Store.Snapshot().Remote
	cf := s.cf(api)
	account := cfg.AccountID
	if account == "" {
		accs, err := cf.Accounts(ctx)
		if err != nil {
			return op(err.Error())
		}
		switch len(accs) {
		case 0:
			return input("That API token cannot see a Cloudflare account.")
		case 1:
			account = accs[0].ID
		default:
			parts := make([]string, 0, len(accs))
			for _, a := range accs {
				parts = append(parts, a.Name+" ("+a.ID+")")
			}
			return input("This token can see more than one account: " + strings.Join(parts, ", ") + ". Enter the account ID to use.")
		}
	}
	name := cfg.TunnelName
	if name == "" {
		name = "feldcore"
	}
	tun, err := cf.CreateTunnel(ctx, account, name)
	if err != nil {
		return op(err.Error())
	}
	if tun.ID == "" || tun.Token == "" {
		return op("Cloudflare created the tunnel but did not return a connector token.")
	}
	if err := s.Secrets.Put(secretTunnel, tun.Token); err != nil {
		return op("Could not store the connector token.")
	}
	return s.Store.Update(func(st *store.State) error {
		st.Remote.TunnelID = tun.ID
		st.Remote.AccountID = account
		st.Remote.TunnelName = name
		return nil
	})
}

// appOrigin is the publish listener every tunnel hostname dials. A
// containerised connector uses host.docker.internal. The baked program, and
// any other cloudflared on this machine, uses loopback. The port defaults
// to the publish listener.
func (s *Service) appOrigin(ctx context.Context) string {
	port := s.GatewayPort
	if port <= 0 {
		port = 8791
	}
	return fmt.Sprintf("http://%s:%d", s.dialHost(ctx), port)
}

// dialHost is the address the connector uses to reach a port on this machine.
// The container cannot use loopback: that is the container itself. The host
// program can, and it must not wait for Docker to answer first.
func (s *Service) dialHost(ctx context.Context) string {
	if !s.preferHost() && s.Engine != nil && s.Engine.Ready(ctx) {
		return "host.docker.internal"
	}
	return "127.0.0.1"
}

func (s *Service) publish(ctx context.Context, apiToken string) error {
	cfg := s.Store.Snapshot().Remote
	if cfg.AccountID == "" || cfg.TunnelID == "" {
		return input("FELDCORE could not tell which tunnel this token belongs to. Use API-token mode, which creates the tunnel and remembers it.")
	}
	cf := s.cf(apiToken)
	type pub struct {
		host, container string
		port            int
		record          string
		admin           bool
	}
	var want []pub
	rules := make([]cfapi.Rule, 0, len(cfg.Routes)+2)
	if cfg.ExposeAdmin {
		rules = append(rules, cfapi.Rule{
			Hostname: cfg.AdminHostname,
			Service:  fmt.Sprintf("http://%s:%d", s.dialHost(ctx), s.listen()),
		})
		want = append(want, pub{host: cfg.AdminHostname, record: cfg.AdminRecordID, admin: true})
	}
	seenHost := map[string]bool{}
	for _, r := range cfg.Routes {
		if !r.Enabled {
			continue
		}
		if seenHost[r.Hostname] {
			return input(r.Hostname + " is already used by another container.")
		}
		seenHost[r.Hostname] = true
		// The connector still joins the app to the proxy network so the
		// publish listener can reach it. The hostname itself always dials
		// that listener, never the container, so a gated app cannot skip
		// Citizen ID from the internet.
		if s.Engine != nil {
			mode, err := s.Engine.NetworkMode(ctx, r.Container)
			switch {
			case err == nil && mode == "none":
				return input(r.Container + " has no network, so a public name cannot reach it.")
			case err == nil && mode != "host":
				if err := s.Engine.ConnectNetwork(ctx, docker.ProxyNetwork, r.Container); err != nil && s.Log != nil {
					s.Log.Warn("could not attach container to the tunnel network", "container", r.Container, "err", err)
				}
			}
		}
		rules = append(rules, cfapi.Rule{Hostname: r.Hostname, Service: s.appOrigin(ctx)})
		want = append(want, pub{host: r.Hostname, container: r.Container, port: r.Port, record: r.RecordID})
	}
	rules = append(rules, cfapi.Rule{Service: "http_status:404"})
	if err := cf.PutIngress(ctx, cfg.AccountID, cfg.TunnelID, rules); err != nil {
		return op("Could not update the tunnel routes: " + err.Error())
	}
	target := cfg.TunnelID + ".cfargotunnel.com"
	kept := map[string]string{}
	for i := range want {
		id, err := cf.UpsertCNAME(ctx, want[i].host, target)
		if err != nil {
			return op(err.Error())
		}
		want[i].record = id
		kept[want[i].host] = id
	}
	if cfg.AdminRecordID != "" && kept[cfg.AdminHostname] == "" {
		_ = cf.DeleteRecord(ctx, cfg.AdminHostname, cfg.AdminRecordID)
	}
	for _, r := range cfg.Routes {
		if r.RecordID != "" && kept[r.Hostname] == "" {
			_ = cf.DeleteRecord(ctx, r.Hostname, r.RecordID)
		}
	}
	return s.Store.Update(func(st *store.State) error {
		st.Remote.AdminRecordID = ""
		for _, w := range want {
			if w.admin {
				st.Remote.AdminRecordID = w.record
			}
		}
		var next []store.PublicRoute
		for _, r := range st.Remote.Routes {
			if !r.Enabled {
				continue
			}
			for _, w := range want {
				if !w.admin && w.container == r.Container && w.host == r.Hostname {
					r.RecordID = w.record
				}
			}
			next = append(next, r)
		}
		st.Remote.Routes = next
		return nil
	})
}

func (s *Service) start(ctx context.Context, token string) error {
	// FELDCORE OS, and any host with the baked program, never starts the
	// container. Docker does not have to be up first.
	if s.preferHost() {
		err := s.startBinary(ctx, token)
		s.retireContainer(ctx)
		return err
	}
	if s.Engine != nil && s.Engine.Ready(ctx) {
		if s.Binary != nil {
			_ = s.Binary.Stop()
		}
		spec := connectorSpec(s.tokenPath(), fileStamp(s.tokenPath()))
		if err := s.Engine.ApplySystem(ctx, spec); err != nil {
			return s.connectorError(err, token)
		}
		return nil
	}
	return s.startBinary(ctx, token)
}

func (s *Service) startBinary(ctx context.Context, token string) error {
	if s.Binary == nil || !s.Binary.Available() {
		if s.preferHost() {
			return op("The tunnel connector could not start. FELDCORE OS runs cloudflared " + BakedCloudflaredVersion + " from " + bakedCloudflared + ", and that program is missing.")
		}
		return op("Docker is not running and cloudflared is not installed. Start Docker, or install the connector from this page.")
	}
	if h, ok := s.Binary.(*HostCloudflared); ok && h.TokenFile == "" {
		h.TokenFile = s.tokenPath()
	}
	already := s.Binary.Running()
	if err := s.Binary.Start(ctx, token); err != nil {
		return s.connectorError(err, token)
	}
	if s.Log != nil && !already {
		if s.preferHost() {
			s.Log.Info("starting tunnel connector", "path", bakedCloudflared, "pinned", BakedCloudflaredVersion)
		} else {
			s.Log.Info("starting tunnel connector", "via", "host")
		}
	}
	return nil
}

func (s *Service) stop(ctx context.Context) error {
	s.retireContainer(ctx)
	if s.Binary != nil {
		if err := s.Binary.Stop(); err != nil && s.Log != nil {
			s.Log.Warn("could not stop the tunnel connector", "err", err)
		}
	}
	_ = os.Remove(s.tokenPath())
	return nil
}

// retireContainer removes a leftover feldcore-cloudflared. On FELDCORE OS the
// container is never the connector; an old one would keep restarting into the
// shell error. A Docker daemon that is not up yet is skipped, and the host
// program has already been started.
func (s *Service) retireContainer(ctx context.Context) {
	if s.Engine == nil {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if !s.Engine.Ready(cctx) {
		return
	}
	if err := s.Engine.StopSystem(cctx, docker.TunnelContainer); err != nil && s.Log != nil {
		s.Log.Warn("could not remove the tunnel container", "err", err)
	}
}

func (s *Service) health(ctx context.Context) Health {
	cfg := s.Store.Snapshot().Remote
	if cfg.Mode == "" || cfg.Mode == "off" {
		return Health{State: "off", Summary: "Remote access is off. Nothing on this server is published.", Connector: "none"}
	}
	state, connector, detail := s.connectorState(ctx)
	detail = sanitizeConnectorDetail(detail, s.preferHost())
	connections := 0
	if api, ok, _ := s.Secrets.Get(secretAPI); ok && cfg.TunnelID != "" && cfg.AccountID != "" {
		tun, err := s.cf(api).GetTunnel(ctx, cfg.AccountID, cfg.TunnelID)
		if err != nil {
			if detail == "" {
				detail = err.Error()
			}
		} else {
			connections = len(tun.Connections)
		}
	}
	switch {
	case state == "running" && connections > 0:
		return Health{State: "healthy", Summary: "The tunnel is connected to Cloudflare.", Connections: connections, Connector: connector}
	case state == "running" && !s.Secrets.Has(secretAPI):
		return Health{State: "up", Summary: "The connector is running. Add an API token to publish hostnames and see Cloudflare's view of the tunnel.", Connector: connector, Detail: detail}
	case state == "running":
		return Health{State: "starting", Summary: "The connector is running. Waiting for Cloudflare to see it.", Connector: connector, Detail: detail}
	default:
		summary := "The connector is not running."
		if connector == "none" {
			summary = "The connector is not installed yet."
		}
		return Health{State: "down", Summary: summary, Connector: connector, Detail: detail}
	}
}

// connectorState is the process FELDCORE started. On the OS path the container
// is ignored, including one left behind that never reached running.
func (s *Service) connectorState(ctx context.Context) (state, connector, detail string) {
	if s.preferHost() {
		return s.hostState()
	}
	state, connector = "missing", "none"
	if s.Engine != nil {
		if st, err := s.Engine.SystemState(ctx, docker.TunnelContainer); err == nil {
			state = st
			if st != "missing" {
				connector = "container"
			}
		}
	}
	if state != "running" && s.Binary != nil && s.Binary.Running() {
		state, connector = "running", "binary"
	}
	if connector == "binary" && s.Binary != nil {
		detail = s.Binary.Detail()
	}
	if state != "running" && connector != "container" && s.Binary != nil && s.Binary.Detail() != "" {
		detail = s.Binary.Detail()
		connector = "binary"
		state = "down"
	}
	return state, connector, detail
}

func (s *Service) hostState() (state, connector, detail string) {
	if s.Binary == nil || !s.Binary.Available() {
		return "down", "none", "cloudflared is not installed at " + bakedCloudflared + "."
	}
	detail = s.Binary.Detail()
	if s.Binary.Running() {
		return "running", "binary", detail
	}
	return "down", "binary", detail
}

func (s *Service) preferHost() bool {
	if s.PreferHost != nil {
		return s.PreferHost()
	}
	if appliance.Is() {
		return true
	}
	return executable(bakedCloudflared)
}

func (s *Service) cf(token string) *cfapi.Client {
	if s.NewCF != nil {
		return s.NewCF(token)
	}
	return &cfapi.Client{Token: token}
}

func (s *Service) listen() int {
	if s.ListenPort > 0 {
		return s.ListenPort
	}
	return 8787
}

func (s *Service) tokenPath() string {
	return filepath.Join(s.DataDir, "run", "tunnel.token")
}

func (s *Service) writeToken(token string) error {
	path := s.tokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return op("Could not prepare the connector token file.")
	}
	if cur, err := os.ReadFile(path); err == nil && string(cur) == token {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return op("Could not write the connector token file.")
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return op("Could not write the connector token file.")
	}
	return os.Chmod(path, 0o600)
}

func connectorSpec(tokenFile, stamp string) docker.SystemSpec {
	// cloudflare/cloudflared is distroless: the binary is at this path and
	// there is no shell. Docker execs this argv. The image user cannot read a
	// mode-0600 file owned by root, so the fallback container runs as root
	// and reads the mounted token. The token is not an environment variable
	// and not part of the command line. "direct" is in the hash so a container
	// created with the old shell command is replaced.
	entry := []string{"/usr/local/bin/cloudflared"}
	cmd := []string{"--no-autoupdate", "tunnel", "run", "--token-file", "/run/feldcore/token"}
	return docker.SystemSpec{
		Name:       docker.TunnelContainer,
		Image:      docker.CloudflaredImage,
		Entrypoint: entry,
		Command:    cmd,
		User:       "0:0",
		Binds:      []string{tokenFile + ":/run/feldcore/token:ro"},
		Network:    docker.ProxyNetwork,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Role:       "tunnel",
		Hash:       names.Fingerprint("tunnel", "direct", strings.Join(entry, " "), strings.Join(cmd, " "), stamp, tokenFile),
	}
}

func crypticExec(msg string) bool {
	return strings.Contains(msg, "executable file not found") || strings.Contains(msg, "exec:")
}

func sanitizeConnectorDetail(msg string, host bool) string {
	msg = strings.TrimSpace(msg)
	if msg == "" || !crypticExec(msg) {
		return msg
	}
	if host {
		return "cloudflared could not be started at " + bakedCloudflared + "."
	}
	return "The cloudflared container failed to start."
}

func (s *Service) connectorError(err error, token string) error {
	msg := sanitizeConnectorDetail(secrets.Redact(strings.TrimSpace(err.Error()), token), s.preferHost())
	switch {
	case msg == "":
		msg = "The tunnel connector could not start."
	case strings.HasPrefix(msg, "The tunnel connector"), strings.HasPrefix(msg, "Docker is not running"), strings.HasPrefix(msg, "FELDCORE OS"):
	default:
		msg = "The tunnel connector could not start. " + msg
	}
	return op(msg)
}

func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return fi.ModTime().UTC().Format("20060102T150405.000000000") + ":" + fmt.Sprint(fi.Size())
}

// tunnelIDs reads the account and tunnel id baked into a dashboard token.
// The secret half of the token is ignored.
func tunnelIDs(token string) (account, id string) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(token))
		if err != nil {
			return "", ""
		}
	}
	var body struct {
		A string `json:"a"`
		T string `json:"t"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", ""
	}
	return body.A, body.T
}
