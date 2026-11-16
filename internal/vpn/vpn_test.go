package vpn

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	priv = "wOEI9rqqbDwnN8/Bpp22sVz48T71vJ4fYmFWujulwUU="
	pub  = "wAUaJMhAq3NFutLHIdF8AN0B5WG8RndfQKLPTEDHal0="
)

const protonConf = `[Interface]
# Bouncing = 8
# NAT-PMP (Port Forwarding) = on
# VPN Accelerator = on
PrivateKey = wOEI9rqqbDwnN8/Bpp22sVz48T71vJ4fYmFWujulwUU=
Address = 10.2.0.2/32
DNS = 10.2.0.1

[Peer]
# NL-FREE#85
PublicKey = wAUaJMhAq3NFutLHIdF8AN0B5WG8RndfQKLPTEDHal0=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 185.159.158.1:51820
`

func TestParseProtonConfig(t *testing.T) {
	got, err := Parse(protonConf, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "proton" || got.Server != "NL-FREE#85" || got.Country != "Netherlands" || !got.NATPMP {
		t.Fatalf("recognised %+v", got)
	}
	if got.Config.PrivateKey != priv || got.Config.Address != "10.2.0.2/32" {
		t.Fatalf("config %+v", got.Config)
	}
	if got.Config.Endpoint != "185.159.158.1:51820" || got.Config.PeerPublic != pub {
		t.Fatalf("peer %+v", got.Config)
	}
}

func TestParseStandardAndPresetOverride(t *testing.T) {
	raw := "[Interface]\nPrivateKey = " + priv + "\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = " + pub + "\nEndpoint = 203.0.113.4:51820\nAllowedIPs = 0.0.0.0/0\n"
	got, err := Parse(raw, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "wireguard" || got.Country != "" || got.NATPMP {
		t.Fatalf("plain config labelled %+v", got)
	}
	forced, err := Parse(protonConf, "wireguard")
	if err != nil {
		t.Fatal(err)
	}
	if forced.Provider != "wireguard" || forced.Detected != "proton" {
		t.Fatalf("preset should keep the detection but label standard: %+v", forced)
	}
}

func TestParseIgnoresHooks(t *testing.T) {
	raw := "[Interface]\nPrivateKey = " + priv + "\nAddress = 10.0.0.2/32\nPostUp = rm -rf /\n\n[Peer]\nPublicKey = " + pub + "\nEndpoint = 203.0.113.4:51820\n"
	got, err := Parse(raw, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if !got.IgnoredHooks {
		t.Fatal("PostUp should be reported and not executed")
	}
	if _, err := Parse("not a config", "auto"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRouteKillSwitch(t *testing.T) {
	plain, err := Route(Workload{Network: "bridge"})
	if err != nil || plain.ThroughVPN || plain.NetworkMode != "bridge" {
		t.Fatalf("plain %+v %v", plain, err)
	}
	plan, err := Route(Workload{
		TunnelID: "proton-nl", Network: "host",
		Ports: []Port{{Host: 8080, Container: 80, Proto: "tcp"}, {Host: 0, Container: 9, Proto: "tcp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ThroughVPN || !plan.KillSwitch || plan.NetworkMode != "container:feldcore-vpn-proton-nl" {
		t.Fatalf("plan %+v", plan)
	}
	if len(plan.Publish) != 1 || plan.Publish[0].Host != 8080 {
		t.Fatalf("publish %+v", plan.Publish)
	}
	back, err := DecodePorts(plan.PortLabel)
	if err != nil || len(back) != 1 || back[0].Container != 80 {
		t.Fatalf("round trip %+v %v", back, err)
	}
	if !Blocked(false, true, true) || !Blocked(true, false, true) || !Blocked(true, true, false) {
		t.Fatal("kill switch should block when the tunnel is off or down")
	}
	if Blocked(true, true, true) {
		t.Fatal("an up tunnel may start")
	}
	if _, err := Route(Workload{TunnelID: "Bad_ID"}); err == nil {
		t.Fatal("bad id should fail")
	}
}

func TestGatewayFirewallAndRedaction(t *testing.T) {
	g, err := BuildGateway(store.VPNTunnel{
		ID: "nl", Provider: "proton", Address: "10.2.0.2/32", DNS: "10.2.0.1",
		PeerKey: pub, Endpoint: "185.159.158.1:51820", AllowedIPs: "0.0.0.0/0",
		NATPMP: true, Keepalive: 25,
		Forwards: []store.VPNForward{{Container: "plex", HostPort: 32400, ContainerPort: 32400, Proto: "tcp"}},
	}, priv, "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(g.Env, "\n")
	if !strings.Contains(joined, "FIREWALL=on") {
		t.Fatal("kill switch firewall not set")
	}
	if strings.Contains(joined, "VPN_PORT_FORWARDING=") {
		t.Fatal("custom configs must not ask gluetun for provider port forwarding")
	}
	if !strings.Contains(joined, "WIREGUARD_PRIVATE_KEY="+priv) {
		t.Fatal("private key missing from the gateway env")
	}
	if secrets.Redact(joined, priv) != strings.ReplaceAll(joined, priv, "[redacted]") {
		t.Fatal("redaction did not hide the private key")
	}
	if strings.Contains(secrets.Redact(joined, priv), priv) {
		t.Fatal("redacted env still contains the key")
	}
	if g.Ports[0].Host != 32400 || g.Image != Image {
		t.Fatalf("gateway %+v", g)
	}
	host, err := BuildGateway(store.VPNTunnel{
		ID: "home", Address: "10.0.0.2/32", PeerKey: pub, Endpoint: "vpn.example.com:51820",
	}, priv, "")
	if err != nil || host.EndpointHost != "vpn.example.com" {
		t.Fatalf("hostname endpoint %+v %v", host, err)
	}
	if err := SetEndpointIP(&host, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if host.EndpointHost != "" || !strings.Contains(strings.Join(host.Env, "\n"), "WIREGUARD_ENDPOINT_IP=203.0.113.9") {
		t.Fatalf("resolved %+v", host.Env)
	}
}

func TestInterpretStatus(t *testing.T) {
	tun := store.VPNTunnel{ID: "nl", Provider: "proton", Enabled: true, Server: "NL-FREE#85", Country: "Netherlands"}
	now := time.Unix(1_700_000_000, 0)
	off := Interpret(tun, false, Sample{Running: true}, now)
	if off.State != "off" {
		t.Fatalf("master off: %+v", off)
	}
	down := Interpret(tun, true, Sample{}, now)
	if down.State != "down" || !down.KillSwitch {
		t.Fatalf("gateway down: %+v", down)
	}
	fresh := now.Add(-20 * time.Second).Unix()
	dump := "priv pub 51820 off\n" + pub + " none 185.159.158.1:51820 0.0.0.0/0 " + strconv.FormatInt(fresh, 10) + " 1 2 25\n"
	up := Interpret(tun, true, Sample{
		Running: true, WGDump: dump,
		PublicBody:  `{"public_ip":"203.0.113.8"}`,
		ForwardBody: `{"port":4242}`,
	}, now)
	if up.State != "up" || up.PublicIP != "203.0.113.8" || up.ForwardedPort != 4242 || up.HandshakeAge < 19 || up.HandshakeAge > 21 {
		t.Fatalf("up: %+v", up)
	}
	staleDump := pub + " none 185.159.158.1:51820 0.0.0.0/0 " + strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10) + " 1 2 25 extra\n"
	stale := Interpret(tun, true, Sample{Running: true, WGDump: staleDump}, now)
	if stale.State != "down" {
		t.Fatalf("stale handshake should be down: %+v", stale)
	}
	live := Interpret(tun, true, Sample{Running: true, VPNStatus: `{"status":"running"}`}, now)
	if live.State != "up" || live.HandshakeAge != -1 {
		t.Fatalf("control server up without a timestamp: %+v", live)
	}
}

func TestSaveKeepsKeyOutOfState(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, Secrets: box}
	saved, err := svc.Save(t.Context(), SaveRequest{Conf: protonConf, Preset: "proton"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Provider != "proton" || !saved.HasKey || !saved.NATPMP {
		t.Fatalf("saved %+v", saved)
	}
	rawb, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawb)
	if strings.Contains(raw, priv) || strings.Contains(raw, "PrivateKey") {
		t.Fatal("state file contains the private key")
	}
	secretb, err := os.ReadFile(dir + "/secrets/vpn-" + saved.ID + "-key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretb), priv) {
		t.Fatal("secret file contains plaintext")
	}
	got, _, err := box.Get("vpn-" + saved.ID + "-key")
	if err != nil || got != priv {
		t.Fatalf("round trip %q %v", got, err)
	}
}
