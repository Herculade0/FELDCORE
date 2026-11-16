package docker

import (
	"strings"
	"testing"
)

func TestReviewCleanSpec(t *testing.T) {
	got := Review(fullSpec())
	if len(got) != 0 {
		t.Fatalf("clean spec flagged: %+v", got)
	}
	if NeedsAck(got) {
		t.Fatal("clean spec needs acknowledgement")
	}
}

func TestEveryRuleFiresAndCanBeFixed(t *testing.T) {
	for _, rule := range securityRules {
		sample := sampleFor(rule)
		got := Review(sample)
		if !hasFinding(got, rule.ID) {
			t.Errorf("%s did not fire on %+v extra %q -> %+v", rule.ID, sample.Configs, sample.ExtraParams, ids(got))
			continue
		}
		for _, f := range got {
			if f.ID != rule.ID {
				continue
			}
			if f.Title == "" || f.Reason == "" || f.Suggestion == "" || !f.Fix {
				t.Errorf("%s incomplete %+v", rule.ID, f)
			}
			if f.Severity != rule.Severity {
				t.Errorf("%s severity %s", rule.ID, f.Severity)
			}
		}
		next := ApplySafer(sample, []string{rule.ID})
		if hasFinding(Review(next), rule.ID) {
			t.Errorf("%s survived ApplySafer: network %q privileged %v extra %q mounts %+v",
				rule.ID, next.Network, next.Privileged, next.ExtraParams, next.Configs)
		}
	}
}

func TestReviewReadsExtraParamsAndMountShapes(t *testing.T) {
	s := Spec{
		Name: "watch", Image: "watchtower",
		Network:     "bridge",
		ExtraParams: "--cap-add=CAP_SYS_ADMIN,net_admin --pid host --ipc=host --privileged --volume /var/run/docker.sock:/var/run/docker.sock --mount type=bind,source=/etc,target=/etc",
		Configs: []Config{
			{Type: TypePath, Host: "/root/", Container: "/root"},
			{Type: TypePath, Host: "/etc/hostname", Container: "/etc/hostname"},
			{Type: TypePath, Host: "/srv/app", Container: "/data"},
			{Type: TypeDevice, Host: "/run/docker.sock", Container: "/run/docker.sock"},
		},
	}
	got := Review(s)
	for _, id := range []string{"privileged", "docker.sock", "host-pid", "host-ipc", "cap:SYS_ADMIN", "cap:NET_ADMIN", "mount:/etc", "mount:/root"} {
		if !hasFinding(got, id) {
			t.Errorf("missing %s in %+v", id, ids(got))
		}
	}
	if hasFinding(got, "mount:/") {
		t.Fatal("a specific file under /etc was treated as the whole filesystem")
	}
	if !NeedsAck(got) {
		t.Fatal("high-risk spec did not require acknowledgement")
	}

	warn := Spec{Name: "ping", Image: "busybox", ExtraParams: "--cap-add=NET_RAW"}
	w := Review(warn)
	if len(w) != 1 || w[0].Severity != SeverityWarn || NeedsAck(w) {
		t.Fatalf("warn-only %+v", w)
	}
}

func TestApplySaferKeepsOrdinarySettings(t *testing.T) {
	s := Spec{
		Name: "web", Image: "nginx", Network: "host", Privileged: true,
		ExtraParams: "--cap-add=SYS_ADMIN --cap-add=NET_RAW --pid=host --ipc=host -e KEEP=1",
		Configs: []Config{
			{Type: TypePath, Host: "/", Container: "/host"},
			{Type: TypePath, Host: "/srv/web", Container: "/usr/share/nginx/html", Mode: "ro"},
			{Type: TypePort, HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
		},
	}
	var ids []string
	for _, f := range Review(s) {
		ids = append(ids, f.ID)
	}
	next := ApplySafer(s, ids)
	if next.Privileged || next.Network != "bridge" || next.Isolate != IsolatePrivate {
		t.Fatalf("safer network %+v privileged %v", next.Network, next.Privileged)
	}
	left := Review(next)
	if len(left) != 0 {
		t.Fatalf("still risky %+v extra %q", idsOf(left), next.ExtraParams)
	}
	if len(next.Configs) != 2 || next.Configs[0].Host != "/srv/web" || next.Configs[1].Type != TypePort {
		t.Fatalf("ordinary settings changed: %+v", next.Configs)
	}
	if !strings.Contains(next.ExtraParams, "KEEP=1") {
		t.Fatalf("unrelated extra flag dropped: %q", next.ExtraParams)
	}
}

func TestHostNamespaceRoundTrip(t *testing.T) {
	in := inspectFixture(t)
	in.HostConfig["PidMode"] = "host"
	in.HostConfig["IpcMode"] = "host"
	got := specFromInspect(in, imageFixture(t), nil)
	if !strings.Contains(got.ExtraParams, "--pid=host") || !strings.Contains(got.ExtraParams, "--ipc=host") || !strings.Contains(got.ExtraParams, "--cap-add=NET_ADMIN") {
		t.Fatalf("echo %q", got.ExtraParams)
	}
	cfg, host := map[string]any{}, map[string]any{"PidMode": "host", "IpcMode": "host"}
	applySpec(cfg, host, got, nil)
	if host["PidMode"] != "host" || host["IpcMode"] != "host" {
		t.Fatalf("unchanged edit dropped modes: %#v %#v", host["PidMode"], host["IpcMode"])
	}
	cleared := ApplySafer(got, []string{"host-pid", "host-ipc"})
	applySpec(cfg, host, cleared, nil)
	if _, ok := host["PidMode"]; ok {
		t.Fatal("pid host survived the safer setting")
	}
	if _, ok := host["IpcMode"]; ok {
		t.Fatal("ipc host survived the safer setting")
	}
}

func TestUnrequestedCapabilityIsDropped(t *testing.T) {
	cfg := map[string]any{}
	host := map[string]any{"PidMode": "host", "CapAdd": []any{"SYS_ADMIN"}, "Memory": "keep"}
	applySpec(cfg, host, fullSpec(), nil)
	if _, ok := host["PidMode"]; ok {
		t.Fatal("pid mode survived a spec that does not ask for it")
	}
	if _, ok := host["CapAdd"]; ok {
		t.Fatal("capability survived a spec that does not ask for it")
	}
	if host["Memory"] != "keep" {
		t.Fatalf("unrelated host setting changed: %v", host["Memory"])
	}
}

func sampleFor(rule rule) Spec {
	s := Spec{Name: "app", Image: "nginx", Network: "bridge"}
	switch rule.Kind {
	case "privileged":
		s.Privileged = true
	case "sock":
		s.Configs = []Config{{Type: TypePath, Host: "/var/run/docker.sock", Container: "/var/run/docker.sock"}}
	case "network":
		s.Network = rule.Arg
	case "pid":
		s.ExtraParams = "--pid=" + rule.Arg
	case "ipc":
		s.ExtraParams = "--ipc=" + rule.Arg
	case "cap":
		s.ExtraParams = "--cap-add=" + rule.Arg
	case "mount":
		s.Configs = []Config{{Type: TypePath, Host: rule.Arg, Container: "/mnt"}}
	default:
		panic("unknown kind " + rule.Kind)
	}
	return s
}

func hasFinding(in []Finding, id string) bool {
	for _, f := range in {
		if f.ID == id {
			return true
		}
	}
	return false
}

func ids(in []Finding) []string {
	out := make([]string, len(in))
	for i, f := range in {
		out[i] = f.ID
	}
	return out
}

func idsOf(in []Finding) []string { return ids(in) }
