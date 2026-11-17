package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

type fakeEngine struct {
	ready    bool
	present  map[string]bool
	specs    []docker.SystemSpec
	built    bool
	ports    []docker.PublishedPort
	state    string
	mode     string
	joined   []string
	stopped  []string
	fail     error
	upstream string
}

func (f *fakeEngine) Ready(context.Context) bool { return f.ready }
func (f *fakeEngine) ApplySystem(_ context.Context, spec docker.SystemSpec) error {
	if f.fail != nil {
		return f.fail
	}
	f.specs = append(f.specs, spec)
	f.state = "running"
	return nil
}
func (f *fakeEngine) StopSystem(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	f.state = "missing"
	return nil
}
func (f *fakeEngine) Upstream(context.Context, string, int) (string, error) {
	if f.upstream == "" {
		return "", errors.New("no address")
	}
	return f.upstream, nil
}
func (f *fakeEngine) SystemState(context.Context, string) (string, error) {
	if f.state == "" {
		return "missing", nil
	}
	return f.state, nil
}
func (f *fakeEngine) EnsureNetwork(context.Context, string) error { return nil }
func (f *fakeEngine) ConnectNetwork(_ context.Context, network, container string) error {
	f.joined = append(f.joined, network+" "+container)
	return nil
}
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
		PreferHost: func() bool { return false },
		Log:        slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&buf, nil), box)),
		Listen:     func() []int { return nil },
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
	spec := eng.specs[0]
	argv := strings.Join(append(append([]string{}, spec.Entrypoint...), spec.Command...), " ")
	if len(spec.Entrypoint) != 1 || spec.Entrypoint[0] != "caddy" || len(spec.Command) < 2 || spec.Command[0] != "run" {
		t.Fatalf("argv %s", argv)
	}
	if strings.Contains(argv, "sh") {
		t.Fatalf("shell %s", argv)
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
		PreferHost: func() bool { return false },
		Log:        slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&buf, nil), box)),
		Listen:     func() []int { return nil },
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
	svc := &Service{Store: st, Secrets: box, Engine: eng, DataDir: dir, PreferHost: func() bool { return false }, Listen: func() []int { return nil }}
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

func TestCitizenGate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{
			{Container: "files", Hostname: "files.home.arpa", Port: 80, TLS: "off", Enabled: true},
			{Container: "open", Hostname: "open.home.arpa", Port: 80, TLS: "internal", Enabled: true},
		}
		s.AppGrants = []store.AppGrant{{Container: "files", Level: "users"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, DataDir: dir, ListenPort: 8787, PreferHost: func() bool { return false }}
	sites, err := svc.sites(context.Background(), st.Snapshot().Web)
	if err != nil {
		t.Fatal(err)
	}
	file, err := Build("", sites)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"reverse_proxy /citizen/* host.docker.internal:8787",
		"forward_auth host.docker.internal:8787",
		"uri /citizen/check",
		"copy_headers Cookie",
		"header_up X-Forwarded-Host {http.request.host}",
		"reverse_proxy files:80",
		"tls internal",
		"reverse_proxy open:80",
		"header_up -Authorization",
		"feldcore_citizen",
	} {
		if !strings.Contains(file, want) {
			t.Fatalf("missing %q in\n%s", want, file)
		}
	}
	if strings.Count(file, "forward_auth") != 1 {
		t.Fatalf("public site was gated:\n%s", file)
	}
	open := file[strings.Index(file, "open.home.arpa"):]
	if strings.Contains(open, "forward_auth") || strings.Contains(open, "route {") {
		t.Fatalf("open site changed shape:\n%s", open)
	}
	if !strings.Contains(open, "header_up -Authorization") {
		t.Fatalf("open site still forwards Authorization:\n%s", open)
	}
}

func TestGatewayRoutePointsAtDaemon(t *testing.T) {
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
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir, GatewayPort: 8791,
		PreferHost: func() bool { return false },
		Listen:     func() []int { return nil },
	}
	if err := svc.SetRoute(RouteInput{
		Container: "plex", Hostname: "plex.home.arpa", Port: 32400, TLS: "internal", Enabled: true, Gateway: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dir + "/caddy/Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "host.docker.internal:8791") || strings.Contains(text, "plex:32400") {
		t.Fatalf("caddyfile %s", text)
	}
	if !strings.Contains(text, "tls internal") {
		t.Fatalf("caddyfile %s", text)
	}
}

func TestAttachJoinsTheProxyNetwork(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	eng := &fakeEngine{ready: true}
	svc := &Service{Store: st, Secrets: box, Engine: eng, DataDir: dir, PreferHost: func() bool { return false }, Listen: func() []int { return nil }}
	if err := svc.SetRoute(RouteInput{Container: "plex", Hostname: "plex.home.arpa", Port: 32400, TLS: "internal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.joined) != 1 || eng.joined[0] != docker.ProxyNetwork+" plex" {
		t.Fatalf("joined %+v", eng.joined)
	}

	eng.mode = "container:feldcore-vpn-proton-nl"
	eng.joined = nil
	if err := svc.Attach(context.Background(), "plex"); err != nil {
		t.Fatal(err)
	}
	if len(eng.joined) != 0 {
		t.Fatalf("vpn workload was attached: %+v", eng.joined)
	}
	if err := svc.Attach(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	if len(eng.joined) != 0 {
		t.Fatal("unrouted container was attached")
	}
}

func TestContainerExecErrorIsHidden(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	eng := &fakeEngine{
		ready: true,
		fail:  errors.New(`feldcore-caddy was created but did not start: exec: "run": executable file not found in $PATH`),
	}
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir,
		PreferHost: func() bool { return false },
		Listen:     func() []int { return nil },
	}
	if err := svc.SetRoute(RouteInput{Container: "plex", Hostname: "plex.home.arpa", Port: 32400, TLS: "internal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(context.Background())
	if err == nil {
		t.Fatal("expected start failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "exec:") || strings.Contains(msg, "PATH") || strings.Contains(msg, `"run"`) {
		t.Fatalf("cryptic exec leaked: %s", msg)
	}
	if !strings.Contains(msg, "failed to start") && !strings.Contains(msg, "could not start") {
		t.Fatalf("err %s", msg)
	}
	if len(eng.specs) != 0 {
		t.Fatal("failed start was recorded as applied")
	}
}

func TestHostProxyRetiresContainerAndDialsLoopback(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.AppGrants = []store.AppGrant{{Container: "plex", Level: "users"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("printf '%%s\\n' \"$0 $*\" > %q\nenv > %q\necho $$ >> %q\nsleep 30\n", argsPath, envPath, countPath))
	eng := &fakeEngine{ready: true, state: "running"}
	h := &HostCaddy{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir, ListenPort: 8787, GatewayPort: 8791,
		Host: h, PreferHost: func() bool { return true },
		Listen: func() []int { return nil },
	}
	if err := svc.SetRoute(RouteInput{
		Container: "plex", Hostname: "plex.home.arpa", Port: 32400, TLS: "internal", Enabled: true, Gateway: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.specs) != 0 || len(eng.joined) != 0 {
		t.Fatalf("container was used specs=%+v joined=%+v", eng.specs, eng.joined)
	}
	if len(eng.stopped) == 0 || eng.stopped[0] != docker.ProxyContainer {
		t.Fatalf("stopped %+v", eng.stopped)
	}
	if !h.Running() {
		t.Fatal("caddy did not stay up")
	}
	raw := readFile(t, filepath.Join(dir, "caddy", "Caddyfile"))
	for _, want := range []string{
		"tls internal",
		"reverse_proxy /citizen/* 127.0.0.1:8787",
		"forward_auth 127.0.0.1:8787",
		"uri /citizen/check",
		"reverse_proxy 127.0.0.1:8791",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q in\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "host.docker.internal") || strings.Contains(raw, "plex:32400") {
		t.Fatalf("container address in\n%s", raw)
	}
	args := readFile(t, argsPath)
	env := readFile(t, envPath)
	for _, want := range []string{bin, "run", "--config", filepath.Join(dir, "caddy", "Caddyfile"), "--adapter", "caddyfile"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %s missing %s", args, want)
		}
	}
	if strings.Contains(args, "sh -c") || strings.Contains(env, "CF_API_TOKEN=") {
		t.Fatalf("args %s\nenv %s", args, env)
	}
	if !strings.Contains(env, "XDG_DATA_HOME="+filepath.Join(dir, "caddy", "data")) {
		t.Fatalf("env %s", env)
	}
	view := svc.View(context.Background())
	if !view.RunsOnHost || view.Status != "up" {
		t.Fatalf("%+v", view)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Count(readFile(t, countPath), "\n") != 1 {
		t.Fatalf("restarted %s", readFile(t, countPath))
	}
}

func TestHostProxyDialsContainerIP(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("echo $$ >> %q\nsleep 30\n", countPath))
	eng := &fakeEngine{ready: true, upstream: "10.0.0.8:32400"}
	h := &HostCaddy{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir,
		Host: h, PreferHost: func() bool { return true },
		Listen: func() []int { return nil },
	}
	if err := svc.SetRoute(RouteInput{Container: "plex", Hostname: "plex.home.arpa", Port: 32400, TLS: "internal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(dir, "caddy", "Caddyfile"))
	if !strings.Contains(raw, "reverse_proxy 10.0.0.8:32400") || strings.Contains(raw, "plex:32400") {
		t.Fatalf("caddyfile %s", raw)
	}
	eng.upstream = "10.0.0.9:32400"
	if err := svc.Attach(context.Background(), "plex"); err != nil {
		t.Fatal(err)
	}
	raw = readFile(t, filepath.Join(dir, "caddy", "Caddyfile"))
	if !strings.Contains(raw, "reverse_proxy 10.0.0.9:32400") {
		t.Fatalf("caddyfile %s", raw)
	}
	if strings.Count(readFile(t, countPath), "\n") != 2 {
		t.Fatalf("address change did not restart %s", readFile(t, countPath))
	}
	if len(eng.specs) != 0 {
		t.Fatal("container started")
	}
}

func TestHostACMENeedsDNSModule(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	if err := box.Put(secretAPI, "super-secret-api-token-value"); err != nil {
		t.Fatal(err)
	}
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("case \"$1\" in list-modules) echo http.handlers.reverse_proxy; exit 0;; esac\necho $$ >> %q\nsleep 30\n", countPath))
	h := &HostCaddy{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	eng := &fakeEngine{ready: true}
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir, GatewayPort: 8791,
		Host: h, PreferHost: func() bool { return true },
		Listen: func() []int { return nil },
	}
	if err := svc.Save(context.Background(), SaveInput{Email: "admin@example.com", Domain: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRoute(RouteInput{
		Container: "plex", Hostname: "plex.example.com", Port: 32400, TLS: "acme", Enabled: true, Gateway: true,
	}); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Cloudflare DNS") || h.Running() {
		t.Fatal(err)
	}
	if readMaybe(countPath) != "" || len(eng.specs) != 0 || eng.built {
		t.Fatalf("started anyway count=%q specs=%d built=%v", readMaybe(countPath), len(eng.specs), eng.built)
	}
}

func TestHostACMEPassesTokenInEnv(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	box, _ := secrets.Open(dir)
	const token = "super-secret-api-token-value"
	if err := box.Put(secretAPI, token); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	bin := writeStandIn(t, fmt.Sprintf("case \"$1\" in list-modules) echo dns.providers.cloudflare; exit 0;; esac\nprintf '%%s\\n' \"$0 $*\" > %q\nenv > %q\nsleep 30\n", argsPath, envPath))
	h := &HostCaddy{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	var buf bytes.Buffer
	eng := &fakeEngine{ready: true}
	svc := &Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir, GatewayPort: 8791,
		Host: h, PreferHost: func() bool { return true },
		Log:    slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&buf, nil), box)),
		Listen: func() []int { return nil },
	}
	if err := svc.Save(context.Background(), SaveInput{Email: "admin@example.com", Domain: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRoute(RouteInput{
		Container: "plex", Hostname: "plex.example.com", Port: 32400, TLS: "acme", Enabled: true, Gateway: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(dir, "caddy", "Caddyfile"))
	args := readFile(t, argsPath)
	env := readFile(t, envPath)
	if strings.Contains(raw, token) || strings.Contains(args, token) || bytes.Contains(buf.Bytes(), []byte(token)) {
		t.Fatalf("token leaked file=%s args=%s log=%s", raw, args, buf.Bytes())
	}
	if !strings.Contains(raw, "{env.CF_API_TOKEN}") || !strings.Contains(env, "CF_API_TOKEN="+token) {
		t.Fatalf("file %s\nenv has token %v", raw, strings.Contains(env, "CF_API_TOKEN="+token))
	}
	if len(eng.specs) != 0 || eng.built {
		t.Fatal("container path used for a host proxy")
	}
}
