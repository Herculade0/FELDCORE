package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestScrubCookiesRemovesEveryCredential(t *testing.T) {
	in := "app=1; feldcore_citizen=one; theme=dark; feldcore_citizen=two; __Host-feldcore_session=s; feldcore_passkey=p; __Host-feldcore_desk=d; keep=yes"
	got := ScrubCookies(in, false)
	for _, leak := range []string{"feldcore_citizen", "feldcore_session", "feldcore_passkey", "feldcore_desk", "one", "two"} {
		if strings.Contains(got, leak) {
			t.Fatalf("scrub kept %q in %q", leak, got)
		}
	}
	if !strings.Contains(got, "app=1") || !strings.Contains(got, "theme=dark") || !strings.Contains(got, "keep=yes") {
		t.Fatalf("app cookies dropped: %q", got)
	}
	kept := ScrubCookies(in, true)
	if !strings.Contains(kept, "feldcore_citizen=one") || !strings.Contains(kept, "feldcore_citizen=two") {
		t.Fatalf("listener hop dropped the citizen cookie: %q", kept)
	}
	if strings.Contains(kept, "feldcore_session") || strings.Contains(kept, "feldcore_passkey") || strings.Contains(kept, "feldcore_desk") {
		t.Fatalf("listener hop kept a management cookie: %q", kept)
	}
}

func TestCaddyfileScrubsEveryProxiedRoute(t *testing.T) {
	file, err := Build("", []Site{
		{Hostname: "open.home.arpa", Upstream: "open:80", TLS: "off"},
		{Hostname: "plex.home.arpa", Upstream: "host.docker.internal:8791", TLS: "internal", KeepCookies: true},
		{Hostname: "feldcore.home.arpa", Upstream: "host.docker.internal:8787", TLS: "internal", Passthrough: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ui := file[strings.Index(file, "feldcore.home.arpa"):strings.Index(file, "open.home.arpa")]
	open := file[strings.Index(file, "open.home.arpa"):strings.Index(file, "plex.home.arpa")]
	for _, want := range []string{
		"feldcore_citizen", "feldcore_passkey", "feldcore_desk", "feldcore_session",
		"header_up -CF-Connecting-IP",
		"header_up X-Forwarded-For {http.request.remote.host}",
		"header_up -Authorization",
	} {
		if !strings.Contains(open, want) {
			t.Fatalf("open site missing %q\n%s", want, open)
		}
	}
	plex := file[strings.Index(file, "plex.home.arpa"):]
	if strings.Contains(plex, "feldcore_citizen|") || strings.Contains(plex, "(?:feldcore_citizen|") {
		t.Fatalf("listener hop strips the citizen cookie\n%s", plex)
	}
	if !strings.Contains(plex, "feldcore_passkey") || !strings.Contains(plex, "header_up -CF-Connecting-IP") {
		t.Fatalf("listener hop\n%s", plex)
	}
	if strings.Contains(ui, "feldcore_citizen") || strings.Contains(ui, "header_up -Authorization") {
		t.Fatalf("management UI scrubbed its own session\n%s", ui)
	}
	if !strings.Contains(ui, "header_up -CF-Connecting-IP") {
		t.Fatalf("management UI\n%s", ui)
	}
}

func TestPublishAccessGatesTheProxy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{
			{Container: "plex", Hostname: "plex.home.arpa", Port: 32400, TLS: "internal", Enabled: true},
			{Container: "open", Hostname: "open.home.arpa", Port: 80, TLS: "internal", Enabled: true},
		}
		s.Publish.Apps = []store.PublishedApp{{
			Container: "plex", Hostname: "plex.home.arpa", Port: 32400, Access: "signed-in", Enabled: true,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, DataDir: dir, ListenPort: 8787}
	sites, err := svc.sites(context.Background(), st.Snapshot().Web)
	if err != nil {
		t.Fatal(err)
	}
	file, err := Build("", sites)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(file, "forward_auth") != 1 {
		t.Fatalf("publish access did not gate exactly the plex site:\n%s", file)
	}
	if !strings.Contains(file[strings.Index(file, "plex.home.arpa"):], "forward_auth") {
		t.Fatalf("plex\n%s", file)
	}
}
