package vpn

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

// Box is the encrypted secret store. *secrets.Box satisfies it.
type Box interface {
	Put(name, value string) error
	Get(name string) (string, bool, error)
}

// ApplyMode says whether a running gateway should be replaced.
type ApplyMode int

const (
	// ApplyEnsure starts a missing or stopped gateway and leaves a running one.
	ApplyEnsure ApplyMode = iota
	// ApplyReplace recreates the gateway so ports and keys are picked up.
	ApplyReplace
)

// Runtime talks to Docker. Implementations must not log Gateway.Env.
type Runtime interface {
	Apply(ctx context.Context, g Gateway, mode ApplyMode) (recreated bool, err error)
	Stop(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) error
	Probe(ctx context.Context, name string) (Sample, error)
	// InNamespace runs fn inside the gateway's network namespace.
	InNamespace(ctx context.Context, name string, fn func() error) error
	// SetForwardedPort tells the gateway firewall about a NAT-PMP port.
	SetForwardedPort(ctx context.Context, name string, port int) error
	Restart(ctx context.Context, name string) error
}

// Service stores tunnels and drives their gateway containers.
type Service struct {
	Store   *store.Store
	Secrets Box
	RT      Runtime
	Log     *slog.Logger
	Now     func() time.Time
	Lookup  func(ctx context.Context, host string) (string, error)

	mu    sync.Mutex
	ports map[string]cachedPort
}

type cachedPort struct {
	port int
	at   time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// View is the tunnels without any key material.
func (s *Service) View() store.VPN {
	v := s.Store.Snapshot().VPN
	if v.Tunnels == nil {
		v.Tunnels = []store.VPNTunnel{}
	}
	return v
}

// Choices is the container form's list.
func (s *Service) Choices() []Choice {
	v := s.View()
	out := make([]Choice, 0, len(v.Tunnels))
	for _, t := range v.Tunnels {
		out = append(out, Choice{
			ID: t.ID, Name: t.Name, Provider: t.Provider,
			Server: t.Server, Country: t.Country, Enabled: v.Enabled && t.Enabled,
		})
	}
	return out
}

// SetEnabled is the master switch. Off stops every gateway.
func (s *Service) SetEnabled(ctx context.Context, on bool) error {
	if err := s.Store.Update(func(st *store.State) error {
		st.VPN.Enabled = on
		return nil
	}); err != nil {
		return err
	}
	s.log().Info("vpn master switch", "enabled", on)
	if !on {
		s.stopAll(ctx)
		return nil
	}
	for _, t := range s.View().Tunnels {
		if t.Enabled {
			if err := s.Reconcile(ctx, t.ID, ApplyEnsure); err != nil {
				s.log().Warn("vpn tunnel did not start", "id", t.ID, "err", err)
			}
		}
	}
	return nil
}

// SaveRequest is a new or edited tunnel. Secrets are optional on edit: an
// empty private key keeps the stored one.
type SaveRequest struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Preset     string `json:"preset,omitempty"`
	Conf       string `json:"conf,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Preshared  string `json:"preshared,omitempty"`
	ClearPSK   bool   `json:"clearPsk,omitempty"`
	Address    string `json:"address,omitempty"`
	DNS        string `json:"dns,omitempty"`
	PeerKey    string `json:"peerKey,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	AllowedIPs string `json:"allowedIPs,omitempty"`
	Keepalive  int    `json:"keepalive,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
	NATPMP     *bool  `json:"natpmp,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

// Save stores a tunnel. The private key goes to the secrets box, not state.
func (s *Service) Save(ctx context.Context, in SaveRequest) (store.VPNTunnel, error) {
	var parsed *Parsed
	if strings.TrimSpace(in.Conf) != "" {
		p, err := Parse(in.Conf, in.Preset)
		if err != nil {
			return store.VPNTunnel{}, err
		}
		parsed = &p
	}
	id := strings.TrimSpace(in.ID)
	creating := id == ""
	if !creating {
		if err := ValidateID(id); err != nil {
			return store.VPNTunnel{}, err
		}
	}
	existing, found := s.find(id)
	if !creating && !found {
		return store.VPNTunnel{}, input("That VPN tunnel no longer exists.")
	}
	t := existing
	if creating {
		t = store.VPNTunnel{Provider: "wireguard"}
	}
	if parsed != nil {
		c := parsed.Config
		t.Provider = parsed.Provider
		t.Address = c.Address
		t.DNS = c.DNS
		t.PeerKey = c.PeerPublic
		t.Endpoint = c.Endpoint
		t.AllowedIPs = c.AllowedIPs
		t.Keepalive = c.Keepalive
		t.MTU = c.MTU
		t.Server = parsed.Server
		t.Country = parsed.Country
		t.NATPMP = parsed.NATPMP
		in.PrivateKey = firstNonEmpty(in.PrivateKey, c.PrivateKey)
		if c.Preshared != "" {
			in.Preshared = firstNonEmpty(in.Preshared, c.Preshared)
		}
	}
	if in.Preset == "proton" {
		t.Provider = "proton"
	}
	if in.Preset == "wireguard" || in.Preset == "standard" {
		t.Provider = "wireguard"
	}
	if t.Provider != "proton" && t.Provider != "wireguard" {
		t.Provider = "wireguard"
	}
	applyOverrides(&t, in)
	if strings.TrimSpace(t.Name) == "" {
		if t.Provider == "proton" && t.Server != "" {
			t.Name = "Proton " + t.Server
		} else if t.Provider == "proton" {
			t.Name = "Proton VPN"
		} else {
			return store.VPNTunnel{}, input("Name the tunnel.")
		}
	}
	if err := finishTunnel(&t); err != nil {
		return store.VPNTunnel{}, err
	}
	if creating {
		t.ID = newID(t.Name, func(id string) bool {
			_, ok := s.find(id)
			return ok
		})
		t.Enabled = false
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	key := strings.TrimSpace(in.PrivateKey)
	if key == "" && !t.HasKey {
		return store.VPNTunnel{}, input("Paste a private key or a WireGuard configuration.")
	}
	if key != "" {
		if err := checkKey("private key", key); err != nil {
			return store.VPNTunnel{}, err
		}
		if err := s.Secrets.Put(keyName(t.ID), key); err != nil {
			return store.VPNTunnel{}, op("Could not store the private key.")
		}
		t.HasKey = true
	}
	psk := strings.TrimSpace(in.Preshared)
	if in.ClearPSK {
		_ = s.Secrets.Put(pskName(t.ID), "")
		t.HasPSK = false
	} else if psk != "" {
		if err := checkKey("preshared key", psk); err != nil {
			return store.VPNTunnel{}, err
		}
		if err := s.Secrets.Put(pskName(t.ID), psk); err != nil {
			return store.VPNTunnel{}, op("Could not store the preshared key.")
		}
		t.HasPSK = true
	}
	if err := s.upsert(t); err != nil {
		return store.VPNTunnel{}, err
	}
	s.log().Info("vpn tunnel saved", "id", t.ID, "provider", t.Provider, "server", t.Server)
	if err := s.Reconcile(ctx, t.ID, ApplyReplace); err != nil {
		return t, err
	}
	return t, nil
}

func applyOverrides(t *store.VPNTunnel, in SaveRequest) {
	if v := strings.TrimSpace(in.Name); v != "" {
		t.Name = v
	}
	if v := strings.TrimSpace(in.Address); v != "" {
		t.Address = v
	}
	if v := strings.TrimSpace(in.DNS); v != "" {
		t.DNS = v
	}
	if v := strings.TrimSpace(in.PeerKey); v != "" {
		t.PeerKey = v
	}
	if v := strings.TrimSpace(in.Endpoint); v != "" {
		t.Endpoint = v
	}
	if v := strings.TrimSpace(in.AllowedIPs); v != "" {
		t.AllowedIPs = v
	}
	if in.Keepalive > 0 {
		t.Keepalive = in.Keepalive
	}
	if in.MTU > 0 {
		t.MTU = in.MTU
	}
	if in.NATPMP != nil {
		t.NATPMP = *in.NATPMP
	}
}

func finishTunnel(t *store.VPNTunnel) error {
	if err := checkKey("peer public key", t.PeerKey); err != nil {
		return err
	}
	if _, _, err := net.ParseCIDR(firstAddr(t.Address)); err != nil {
		return input("Address must look like 10.2.0.2/32.")
	}
	if _, _, err := SplitEndpoint(t.Endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(t.AllowedIPs) == "" {
		t.AllowedIPs = "0.0.0.0/0"
	}
	t.Name = oneLine(t.Name, 80)
	if t.Name == "" {
		return input("Name the tunnel.")
	}
	return nil
}

// Delete removes a tunnel, its secrets and its gateway.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	t, ok := s.find(id)
	if !ok {
		return input("That VPN tunnel no longer exists.")
	}
	name, _ := GatewayName(id)
	if s.RT != nil && name != "" {
		_ = s.RT.Remove(ctx, name)
	}
	_ = s.Secrets.Put(keyName(id), "")
	_ = s.Secrets.Put(pskName(id), "")
	if err := s.Store.Update(func(st *store.State) error {
		out := st.VPN.Tunnels[:0]
		for _, tun := range st.VPN.Tunnels {
			if tun.ID != id {
				out = append(out, tun)
			}
		}
		st.VPN.Tunnels = out
		return nil
	}); err != nil {
		return err
	}
	s.log().Info("vpn tunnel removed", "id", id, "name", t.Name)
	return nil
}

// SetTunnelEnabled turns one tunnel on or off. The master switch still has
// to be on before a gateway is started.
func (s *Service) SetTunnelEnabled(ctx context.Context, id string, on bool) error {
	if _, ok := s.find(id); !ok {
		return input("That VPN tunnel no longer exists.")
	}
	if err := s.Store.Update(func(st *store.State) error {
		for i := range st.VPN.Tunnels {
			if st.VPN.Tunnels[i].ID == id {
				st.VPN.Tunnels[i].Enabled = on
				return nil
			}
		}
		return input("That VPN tunnel no longer exists.")
	}); err != nil {
		return err
	}
	return s.Reconcile(ctx, id, ApplyEnsure)
}

// Reconcile makes the gateway match the stored tunnel.
func (s *Service) Reconcile(ctx context.Context, id string, mode ApplyMode) error {
	if s.RT == nil {
		return nil
	}
	t, ok := s.find(id)
	if !ok {
		return input("That VPN tunnel no longer exists.")
	}
	name, err := GatewayName(id)
	if err != nil {
		return err
	}
	master := s.Store.Snapshot().VPN.Enabled
	key, psk, err := s.keys(t)
	if err != nil {
		return err
	}
	g, err := BuildGateway(t, key, psk)
	if err != nil {
		return err
	}
	g.Start = master && t.Enabled
	if g.EndpointHost != "" {
		ip, err := s.lookup(ctx, g.EndpointHost)
		if err != nil {
			return opf("Could not look up the VPN server %s.", g.EndpointHost)
		}
		if err := SetEndpointIP(&g, ip); err != nil {
			return err
		}
	}
	if !g.Start {
		if len(t.Forwards) == 0 || mode == ApplyEnsure {
			if err := s.RT.Stop(ctx, name); err != nil {
				s.log().Warn("vpn gateway did not stop", "id", id, "err", err)
			}
			return nil
		}
		// A container is being attached. Create the gateway so Docker has a
		// network namespace to point at, then leave it stopped.
		if _, err := s.RT.Apply(ctx, g, ApplyReplace); err != nil {
			return opf("The VPN gateway did not start: %s", scrubErr(err, key, psk))
		}
		_ = s.RT.Stop(ctx, name)
		return nil
	}
	recreated, err := s.RT.Apply(ctx, g, mode)
	if err != nil {
		return opf("The VPN gateway did not start: %s", scrubErr(err, key, psk))
	}
	if recreated {
		s.restartUsers(ctx, t)
	}
	return nil
}

// Restore starts tunnels that were left on. Called once at startup.
func (s *Service) Restore(ctx context.Context) {
	v := s.View()
	if !v.Enabled {
		return
	}
	for _, t := range v.Tunnels {
		if !t.Enabled {
			continue
		}
		if err := s.Reconcile(ctx, t.ID, ApplyEnsure); err != nil {
			s.log().Warn("vpn tunnel did not restore", "id", t.ID, "err", err)
		}
	}
}

// KeepForwarded refreshes Proton NAT-PMP mappings so they do not expire.
func (s *Service) KeepForwarded(ctx context.Context) {
	tick := time.NewTicker(45 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.refreshAll(ctx)
		}
	}
}

func (s *Service) refreshAll(ctx context.Context) {
	v := s.View()
	if !v.Enabled {
		return
	}
	for _, t := range v.Tunnels {
		if t.Enabled && t.Provider == "proton" && t.NATPMP {
			_, _ = s.forwardedPort(ctx, t, false)
		}
	}
}

// Status probes one tunnel.
func (s *Service) Status(ctx context.Context, id string) (Status, error) {
	t, ok := s.find(id)
	if !ok {
		return Status{}, input("That VPN tunnel no longer exists.")
	}
	master := s.Store.Snapshot().VPN.Enabled
	var sample Sample
	if s.RT != nil && master && t.Enabled {
		name, err := GatewayName(id)
		if err != nil {
			return Status{}, err
		}
		probed, err := s.RT.Probe(ctx, name)
		if err != nil {
			s.log().Warn("vpn probe failed", "id", id, "err", err)
		} else {
			sample = probed
		}
		if t.Provider == "proton" && t.NATPMP && sample.Running {
			if p, err := s.forwardedPort(ctx, t, true); err == nil && p > 0 {
				sample.NATPMPPort = p
			}
		}
	}
	return Interpret(t, master, sample, s.now()), nil
}

// AllowStart is the kill switch consulted before a routed container starts.
func (s *Service) AllowStart(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	master := s.Store.Snapshot().VPN.Enabled
	t, ok := s.find(id)
	up := false
	if master && ok && t.Enabled {
		st, err := s.Status(ctx, id)
		up = err == nil && st.State == "up"
	}
	if !Blocked(master, ok && t.Enabled, up) {
		return nil
	}
	if !master {
		return input("VPN kill switch: VPN is turned off, so this container was not started.")
	}
	if !ok || !t.Enabled {
		return input("VPN kill switch: that tunnel is off, so this container was not started.")
	}
	return input("VPN kill switch: the tunnel is down, so this container was not started and has no network.")
}

// Sync records which ports a container publishes through a tunnel and
// refreshes the gateways involved. An empty tunnelID detaches the container.
func (s *Service) Sync(ctx context.Context, container, tunnelID string, ports []Port) error {
	container = strings.TrimSpace(container)
	if container == "" {
		return input("Name the container before routing it through a VPN.")
	}
	if tunnelID != "" {
		if _, ok := s.find(tunnelID); !ok {
			return input("That VPN tunnel no longer exists.")
		}
	}
	var affected []string
	err := s.Store.Update(func(st *store.State) error {
		for i := range st.VPN.Tunnels {
			next := st.VPN.Tunnels[i].Forwards[:0]
			changed := false
			for _, f := range st.VPN.Tunnels[i].Forwards {
				if f.Container == container {
					changed = true
					continue
				}
				next = append(next, f)
			}
			if tunnelID == st.VPN.Tunnels[i].ID {
				for _, p := range ports {
					if p.Host <= 0 {
						continue
					}
					proto := p.Proto
					if proto == "" {
						proto = "tcp"
					}
					for _, already := range next {
						if already.HostPort == p.Host && already.Proto == proto {
							return input(fmt.Sprintf("Host port %d/%s is already published on this VPN.", p.Host, proto))
						}
					}
					next = append(next, store.VPNForward{
						Container: container, HostIP: p.HostIP,
						HostPort: p.Host, ContainerPort: p.Container, Proto: proto,
					})
					changed = true
				}
			}
			if changed {
				affected = append(affected, st.VPN.Tunnels[i].ID)
			}
			st.VPN.Tunnels[i].Forwards = next
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range affected {
		if err := s.Reconcile(ctx, id, ApplyReplace); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) forwardedPort(ctx context.Context, t store.VPNTunnel, allowCache bool) (int, error) {
	if allowCache {
		s.mu.Lock()
		c, ok := s.ports[t.ID]
		s.mu.Unlock()
		if ok && s.now().Sub(c.at) < 40*time.Second && c.port > 0 {
			return c.port, nil
		}
	}
	if s.RT == nil {
		return 0, nil
	}
	gw, err := GatewayIP(t.Address)
	if err != nil {
		return 0, err
	}
	name, err := GatewayName(t.ID)
	if err != nil {
		return 0, err
	}
	var res MapResult
	err = s.RT.InNamespace(ctx, name, func() error {
		r, qerr := DialMap(gw, OpTCP, 443, 60)
		if qerr != nil {
			r, qerr = DialMap(gw, OpUDP, 443, 60)
		}
		res = r
		return qerr
	})
	if err != nil || res.External == 0 {
		return 0, err
	}
	port := int(res.External)
	s.mu.Lock()
	if s.ports == nil {
		s.ports = map[string]cachedPort{}
	}
	s.ports[t.ID] = cachedPort{port: port, at: s.now()}
	s.mu.Unlock()
	if err := s.RT.SetForwardedPort(ctx, name, port); err != nil {
		s.log().Warn("could not open the forwarded port on the gateway firewall", "id", t.ID, "err", err)
	}
	return port, nil
}

func (s *Service) keys(t store.VPNTunnel) (key, psk string, err error) {
	key, _, err = s.Secrets.Get(keyName(t.ID))
	if err != nil || key == "" {
		return "", "", input("This tunnel's private key could not be read.")
	}
	if t.HasPSK {
		psk, _, err = s.Secrets.Get(pskName(t.ID))
		if err != nil {
			return "", "", input("This tunnel's preshared key could not be read.")
		}
	}
	return key, psk, nil
}

func (s *Service) find(id string) (store.VPNTunnel, bool) {
	for _, t := range s.Store.Snapshot().VPN.Tunnels {
		if t.ID == id {
			return t, true
		}
	}
	return store.VPNTunnel{}, false
}

func (s *Service) upsert(t store.VPNTunnel) error {
	return s.Store.Update(func(st *store.State) error {
		for i := range st.VPN.Tunnels {
			if st.VPN.Tunnels[i].ID == t.ID {
				t.Forwards = st.VPN.Tunnels[i].Forwards
				st.VPN.Tunnels[i] = t
				return nil
			}
		}
		st.VPN.Tunnels = append(st.VPN.Tunnels, t)
		return nil
	})
}

func (s *Service) stopAll(ctx context.Context) {
	if s.RT == nil {
		return
	}
	for _, t := range s.View().Tunnels {
		name, err := GatewayName(t.ID)
		if err != nil {
			continue
		}
		_ = s.RT.Stop(ctx, name)
	}
}

func (s *Service) restartUsers(ctx context.Context, t store.VPNTunnel) {
	seen := map[string]bool{}
	for _, f := range t.Forwards {
		if f.Container == "" || seen[f.Container] {
			continue
		}
		seen[f.Container] = true
		if err := s.RT.Restart(ctx, f.Container); err != nil {
			s.log().Warn("routed container did not restart after the gateway was replaced", "container", f.Container, "err", err)
		}
	}
}

func (s *Service) lookup(ctx context.Context, host string) (string, error) {
	if s.Lookup != nil {
		return s.Lookup(ctx, host)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return "", err
	}
	return ips[0].IP.String(), nil
}

func keyName(id string) string { return "vpn-" + id + "-key" }
func pskName(id string) string { return "vpn-" + id + "-psk" }

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

func firstAddr(address string) string {
	part := strings.TrimSpace(address)
	if i := strings.Index(part, ","); i >= 0 {
		part = strings.TrimSpace(part[:i])
	}
	return part
}

func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n]
	}
	return s
}

func newID(name string, taken func(string) bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "tun"
	}
	if len(id) > 20 {
		id = strings.Trim(id[:20], "-")
	}
	base := id
	for i := 2; taken(id); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

func scrubErr(err error, hidden ...string) string {
	if err == nil {
		return ""
	}
	return secrets.Redact(err.Error(), hidden...)
}
