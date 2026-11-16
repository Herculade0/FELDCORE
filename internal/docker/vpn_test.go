package docker

import (
	"testing"

	"github.com/Herculade0/feldcore/internal/vpn"
)

func TestApplySpecRoutesThroughVPN(t *testing.T) {
	s := fullSpec()
	s.VPN = "proton-nl"
	s.Network = "host"
	cfg, host := map[string]any{}, map[string]any{}
	applySpec(cfg, host, s, nil)

	if host["NetworkMode"] != "container:feldcore-vpn-proton-nl" {
		t.Fatalf("network = %v", host["NetworkMode"])
	}
	if len(host["PortBindings"].(map[string][]portBinding)) != 0 {
		t.Fatalf("routed container still publishes ports: %#v", host["PortBindings"])
	}
	labels := cfg["Labels"].(map[string]string)
	if labels[vpn.LabelTunnel] != "proton-nl" || labels[vpn.LabelKill] != "true" || labels[vpn.LabelNetwork] != "host" {
		t.Fatalf("labels %v", labels)
	}
	if labels[vpn.LabelPorts] == "" {
		t.Fatal("ports were not recorded for the gateway")
	}

	in := inspectFixture(t)
	in.Config["Labels"] = map[string]any{
		"feldcore.managed": "true",
		vpn.LabelTunnel:    "proton-nl",
		vpn.LabelKill:      "true",
		vpn.LabelNetwork:   "bridge",
		vpn.LabelPorts:     labels[vpn.LabelPorts],
		"com.example.role": "frontend",
	}
	in.HostConfig["NetworkMode"] = "container:feldcore-vpn-proton-nl"
	in.HostConfig["PortBindings"] = map[string]any{}
	got := specFromInspect(in, imageFixture(t), nil)
	if got.VPN != "proton-nl" || got.Network != "bridge" {
		t.Fatalf("restored vpn=%q network=%q", got.VPN, got.Network)
	}
	var port *Config
	for i := range got.Configs {
		if got.Configs[i].Type == TypePort {
			port = &got.Configs[i]
		}
	}
	if port == nil || port.HostPort != 8080 || port.ContainerPort != 80 {
		t.Fatalf("ports not restored: %+v", got.Configs)
	}
}

func TestNormaliseRequiresNameForVPN(t *testing.T) {
	s := spec()
	s.Name = ""
	s.VPN = "proton-nl"
	if err := normaliseSpec(&s); err == nil {
		t.Fatal("a routed container needs a name")
	}
}
