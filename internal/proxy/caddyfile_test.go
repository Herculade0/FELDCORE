package proxy

import (
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/docker"
)

func TestBuildModes(t *testing.T) {
	const token = "super-secret-api-token-value"
	file, err := Build("admin@example.com", []Site{
		{Hostname: "valheim.home.arpa", Upstream: "valheim:2456", TLS: "internal"},
		{Hostname: "files.home.arpa", Upstream: "files:80", TLS: "off"},
		{Hostname: "app.example.com", Upstream: "app:32400", TLS: "acme"},
		{Hostname: "feldcore.home.arpa", Upstream: "host.docker.internal:8787", TLS: "internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tls internal",
		"http://files.home.arpa",
		"dns cloudflare {env.CF_API_TOKEN}",
		"reverse_proxy valheim:2456",
		"reverse_proxy host.docker.internal:8787",
		"email admin@example.com",
		"admin off",
	} {
		if !strings.Contains(file, want) {
			t.Fatalf("missing %q in\n%s", want, file)
		}
	}
	if strings.Contains(file, token) {
		t.Fatal("token written into the Caddyfile")
	}
}

func TestExplainConflict(t *testing.T) {
	got := Explain([]docker.PublishedPort{{Port: 80, Proto: "tcp", Container: "nginx"}}, nil, false)
	if len(got) != 1 || got[0].Port != 80 || !strings.Contains(got[0].Explanation, "nginx") || !strings.Contains(got[0].Explanation, "80 and 443") {
		t.Fatalf("%+v", got)
	}
	self := Explain([]docker.PublishedPort{{Port: 80, Proto: "tcp", Container: docker.ProxyContainer, Role: "proxy"}}, []int{80, 443}, true)
	if len(self) != 0 {
		t.Fatalf("self conflict %+v", self)
	}
	got = Explain(nil, []int{443}, false)
	if len(got) != 1 || got[0].Port != 443 || !strings.Contains(got[0].Explanation, "another program") {
		t.Fatalf("%+v", got)
	}
	if c := Explain(nil, []int{80, 443}, true); len(c) != 0 {
		t.Fatalf("running caddy %+v", c)
	}
}
