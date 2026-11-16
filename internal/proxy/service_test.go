package proxy

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

type fakeEngine struct {
	ready   bool
	present map[string]bool
	specs   []docker.SystemSpec
	built   bool
	ports   []docker.PublishedPort
	state   string
	mode    string
}

func (f *fakeEngine) Ready(context.Context) bool { return f.ready }
func (f *fakeEngine) ApplySystem(_ context.Context, spec docker.SystemSpec) error {
	f.specs = append(f.specs, spec)
	f.state = "running"
	return nil
}
func (f *fakeEngine) StopSystem(context.Context, string) error { f.state = "missing"; return nil }
func (f *fakeEngine) SystemState(context.Context, string) (string, error) {
	if f.state == "" {
		return "missing", nil
	}
	return f.state, nil
}
func (f *fakeEngine) EnsureNetwork(context.Context, string) error          { return nil }
func (f *fakeEngine) ConnectNetwork(context.Context, string, string) error { return nil }
func (f *fakeEngine) NetworkMode(context.Context, string) (string, error) {
	if f.mode == "" {
		return "bridge", nil
	}
	return f.mode, nil
}
func (f *fakeEngine) PublishedPorts(context.Context) ([]docker.PublishedPort, error) {
	return f.ports, nil
}
func (f *fakeEngine) BuildImage(context.Context, string, string) error { f.built = true; return nil }
func (f *fakeEngine) ImagePresent(_ context.Context, ref string) bool  { return f.present[ref] }

func TestApplyInternalDoesNotNeedToken(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{ready: true}
	var buf bytes.Buffer
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir, ListenPort: 8787,
		Log:    slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&buf, nil), box)),
		Listen: func() []int { return nil },
	}
	if err := svc.SetRoute(RouteInput{Container: "valheim", Hostname: "valheim.home.arpa", Port: 2456, TLS: "internal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.specs) != 1 || eng.specs[0].Image != docker.CaddyImage {
		t.Fatalf("spec %+v", eng.specs)
	}
	raw, err := os.ReadFile(dir + "/caddy/Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "tls internal") || !strings.Contains(string(raw), "valheim:2456") {
		t.Fatalf("caddyfile %s", raw)
	}
	if svc.URLFor("valheim") != "https://valheim.home.arpa" {
		t.Fatal(svc.URLFor("valheim"))
	}
	if len(eng.specs[0].Env) != 0 {
		t.Fatal("LAN proxy should not receive a token")
	}
}

func TestACMEUsesEnvNotFile(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	const token = "super-secret-api-token-value"
	if err := box.Put(secretAPI, token); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{ready: true}
	var buf bytes.Buffer
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir,
		Log:    slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&buf, nil), box)),
		Listen: func() []int { return nil },
	}
	if err := svc.Save(context.Background(), SaveInput{Email: "admin@example.com", Domain: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRoute(RouteInput{Container: "plex", Hostname: "plex.example.com", Port: 32400, TLS: "acme", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dir + "/caddy/Caddyfile")
	if strings.Contains(string(raw), token) || bytes.Contains(buf.Bytes(), []byte(token)) {
		t.Fatalf("token leaked file=%s log=%s", raw, buf.Bytes())
	}
	if !strings.Contains(string(raw), "{env.CF_API_TOKEN}") {
		t.Fatalf("caddyfile %s", raw)
	}
	if !eng.built || eng.specs[0].Image != docker.CaddyPluginImage {
		t.Fatalf("built %v image %s", eng.built, eng.specs[0].Image)
	}
	joined := strings.Join(eng.specs[0].Env, " ")
	if !strings.Contains(joined, token) {
		t.Fatal("container env should carry the token for Caddy, without it being written to the Caddyfile")
	}
}

func TestPortConflictBlocksStart(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	eng := &fakeEngine{ready: true, ports: []docker.PublishedPort{{Port: 80, Proto: "tcp", Container: "nginx"}}}
	svc := &Service{Store: st, Secrets: box, Engine: eng, DataDir: dir, Listen: func() []int { return nil }}
	if err := svc.SetRoute(RouteInput{Container: "app", Hostname: "app.home.arpa", Port: 8080, TLS: "internal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nginx") {
		t.Fatalf("err %v", err)
	}
	if len(eng.specs) != 0 {
		t.Fatal("proxy started despite the conflict")
	}
}
