package publish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

// recordEngine is the Docker surface the host proxy must not use, except to
// remove a leftover feldcore-caddy.
type recordEngine struct {
	ready   bool
	specs   []docker.SystemSpec
	stopped []string
}

func (e *recordEngine) Ready(context.Context) bool { return e.ready }
func (e *recordEngine) ApplySystem(_ context.Context, spec docker.SystemSpec) error {
	e.specs = append(e.specs, spec)
	return nil
}
func (e *recordEngine) StopSystem(_ context.Context, name string) error {
	e.stopped = append(e.stopped, name)
	return nil
}
func (e *recordEngine) SystemState(context.Context, string) (string, error) {
	return "missing", nil
}
func (e *recordEngine) EnsureNetwork(context.Context, string) error { return nil }
func (e *recordEngine) ConnectNetwork(context.Context, string, string) error {
	return nil
}
func (e *recordEngine) NetworkMode(context.Context, string) (string, error) {
	return "bridge", nil
}
func (e *recordEngine) PublishedPorts(context.Context) ([]docker.PublishedPort, error) {
	return nil, nil
}
func (e *recordEngine) BuildImage(context.Context, string, string) error { return nil }
func (e *recordEngine) ImagePresent(context.Context, string) bool        { return false }

// TestPublishCitizenIDStartsHostProxy is the appliance path: Publish with
// Citizen ID required starts the baked Caddy, points it at the publish
// listener, and the listener refuses anonymous clients.
func TestPublishCitizenIDStartsHostProxy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Domain = "home.arpa"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	gate := citizenid.Open(citizenid.Deps{Store: st, Secrets: box, Audit: accounts})

	argsPath := filepath.Join(dir, "args")
	countPath := filepath.Join(dir, "count")
	bin := writeCaddyStandIn(t, fmt.Sprintf("printf '%%s\\n' \"$0 $*\" > %q\necho $$ >> %q\nsleep 30\n", argsPath, countPath))
	eng := &recordEngine{ready: true}
	host := &proxy.HostCaddy{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = host.Stop() })
	px := &proxy.Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir,
		ListenPort: 8787, GatewayPort: 8791,
		Host: host, PreferHost: func() bool { return true },
		Listen: func() []int { return nil },
	}

	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.Header.Get("Cookie"), "feldcore") || r.Header.Get("Authorization") != "" {
			t.Errorf("app saw credentials: cookie %q auth %q", r.Header.Get("Cookie"), r.Header.Get("Authorization"))
		}
		io.WriteString(w, "plex")
	}))
	defer upstream.Close()
	addr := strings.TrimPrefix(upstream.URL, "http://")

	svc := &Service{
		Store: st, Secrets: box, Proxy: px, GatewayPort: 8791,
		Resolve: resolverFunc(func(context.Context, string, int) (string, error) { return addr, nil }),
	}
	svc.SetAuth(CitizenAuth(gate))

	out, err := svc.Set(context.Background(), SetInput{
		Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessSignedIn, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.App.Hostname != "plex.home.arpa" || out.App.Access != AccessSignedIn {
		t.Fatalf("%+v", out)
	}
	if !host.Running() {
		t.Fatal("proxy did not stay up")
	}
	if len(eng.specs) != 0 {
		t.Fatalf("container started %+v", eng.specs)
	}
	retired := false
	for _, name := range eng.stopped {
		if name == docker.ProxyContainer {
			retired = true
		}
	}
	if !retired {
		t.Fatalf("stale container was not removed: %+v", eng.stopped)
	}
	view := px.View(context.Background())
	if !view.RunsOnHost || view.Status != "up" {
		t.Fatalf("%+v", view)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "caddy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	file := string(raw)
	for _, want := range []string{
		"tls internal",
		"reverse_proxy /citizen/* 127.0.0.1:8787",
		"forward_auth 127.0.0.1:8787",
		"uri /citizen/check",
		"reverse_proxy 127.0.0.1:8791",
	} {
		if !strings.Contains(file, want) {
			t.Fatalf("missing %q in\n%s", want, file)
		}
	}
	if strings.Contains(file, "host.docker.internal") || strings.Contains(file, "plex:32400") {
		t.Fatalf("container address in\n%s", file)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	argv := string(args)
	for _, want := range []string{bin, " run ", "--config", filepath.Join(dir, "caddy", "Caddyfile"), "--adapter", "caddyfile"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("args %s missing %s", argv, want)
		}
	}
	if strings.Contains(argv, "sh -c") {
		t.Fatalf("shell %s", argv)
	}
	if strings.Count(readCount(countPath), "\n") != 1 {
		t.Fatalf("restarted %s", readCount(countPath))
	}

	anon := httptest.NewRequest(http.MethodGet, "http://plex.home.arpa/", nil)
	anon.Host = "plex.home.arpa"
	anon.RemoteAddr = "127.0.0.1:9"
	anon.Header.Set("User-Agent", "Mozilla/5.0")
	check := httptest.NewRecorder()
	gate.ServeCheck(check, anon)
	if check.Code != http.StatusFound || !strings.Contains(check.Header().Get("Location"), "/citizen/signin") {
		t.Fatalf("check %d %s", check.Code, check.Header().Get("Location"))
	}
	page := httptest.NewRecorder()
	svc.Handler().ServeHTTP(page, anon)
	if hits != 0 || page.Code != http.StatusFound || !strings.Contains(page.Header().Get("Location"), "/citizen/signin") {
		t.Fatalf("handler proxied=%d %d %s", hits, page.Code, page.Header().Get("Location"))
	}

	login := httptest.NewRequest(http.MethodPost, "/citizen/login", nil)
	login.Host = "plex.home.arpa"
	login.RemoteAddr = "127.0.0.1:9"
	login.Header.Set("User-Agent", "Mozilla/5.0")
	lrec := httptest.NewRecorder()
	if _, err := gate.Login(lrec, login, "admin", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range lrec.Result().Cookies() {
		if strings.Contains(c.Name, "citizen") {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no citizen cookie")
	}

	authed := httptest.NewRequest(http.MethodGet, "http://plex.home.arpa/", nil)
	authed.Host = "plex.home.arpa"
	authed.RemoteAddr = "127.0.0.1:9"
	authed.Header.Set("User-Agent", "Mozilla/5.0")
	authed.AddCookie(cookie)
	okCheck := httptest.NewRecorder()
	gate.ServeCheck(okCheck, authed)
	if okCheck.Code != http.StatusOK {
		t.Fatalf("signed-in check %d %s", okCheck.Code, okCheck.Body.String())
	}
	okPage := httptest.NewRecorder()
	svc.Handler().ServeHTTP(okPage, authed)
	if hits != 1 || okPage.Code != http.StatusOK || okPage.Body.String() != "plex" {
		t.Fatalf("signed-in proxied=%d %d %s", hits, okPage.Code, okPage.Body.String())
	}

	if _, err := svc.Set(context.Background(), SetInput{
		Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessSignedIn, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if host.Running() {
		t.Fatal("proxy stayed up after publish was turned off")
	}
}

func writeCaddyStandIn(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCount(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
