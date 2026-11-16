package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/cfapi"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

type fakeEngine struct {
	ready     bool
	specs     []docker.SystemSpec
	stopped   []string
	state     string
	mode      string
	connected []string
}

func (f *fakeEngine) Ready(context.Context) bool { return f.ready }
func (f *fakeEngine) ApplySystem(_ context.Context, spec docker.SystemSpec) error {
	f.specs = append(f.specs, spec)
	f.state = "running"
	return nil
}
func (f *fakeEngine) StopSystem(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	f.state = "missing"
	return nil
}
func (f *fakeEngine) SystemState(context.Context, string) (string, error) {
	if f.state == "" {
		return "missing", nil
	}
	return f.state, nil
}
func (f *fakeEngine) EnsureNetwork(context.Context, string) error { return nil }
func (f *fakeEngine) ConnectNetwork(_ context.Context, _, container string) error {
	f.connected = append(f.connected, container)
	return nil
}
func (f *fakeEngine) NetworkMode(context.Context, string) (string, error) { return f.mode, nil }

func newSvc(t *testing.T) (*Service, *fakeEngine, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&buf, nil), box))
	eng := &fakeEngine{ready: true, mode: "bridge"}
	return &Service{Store: st, Secrets: box, Engine: eng, DataDir: dir, ListenPort: 8787, Log: log}, eng, &buf
}

func TestDefaultIsOff(t *testing.T) {
	svc, eng, _ := newSvc(t)
	v := svc.View(context.Background())
	if v.Mode != "off" || v.ExposeAdmin || len(v.Routes) != 0 || v.Status.State != "off" {
		t.Fatalf("view %+v", v)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.specs) != 0 {
		t.Fatal("connector started while off")
	}
}

func TestTokenModeHidesToken(t *testing.T) {
	svc, eng, logs := newSvc(t)
	const token = "super-secret-tunnel-token"
	if err := svc.Save(context.Background(), SaveInput{Mode: "token", TunnelToken: token}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(svc.View(context.Background()))
	if bytes.Contains(raw, []byte(token)) || bytes.Contains(logs.Bytes(), []byte(token)) {
		t.Fatalf("token leaked view=%s log=%s", raw, logs.Bytes())
	}
	if len(eng.specs) != 1 {
		t.Fatalf("specs %d", len(eng.specs))
	}
	spec := eng.specs[0]
	joined := strings.Join(append(spec.Command, spec.Env...), " ")
	if strings.Contains(joined, token) {
		t.Fatalf("token in container spec: %+v", spec.Command)
	}
	if spec.Role != "tunnel" || len(spec.Binds) != 1 || !strings.Contains(spec.Binds[0], "tunnel.token") {
		t.Fatalf("spec %+v", spec)
	}
	fi, err := os.Stat(svc.tokenPath())
	if err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token file mode %v %v", fi, err)
	}
}

func TestAPIModeCreatesTunnel(t *testing.T) {
	svc, eng, logs := newSvc(t)
	const apiTok = "super-secret-api-token-value"
	const connector = "super-secret-connector-token"
	var ingress string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/accounts":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"acc","name":"Home"}]}`))
		case r.URL.Path == "/accounts/acc/cfd_tunnel" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"tun","name":"feldcore","token":"` + connector + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/configurations"):
			raw, _ := io.ReadAll(r.Body)
			ingress = string(raw)
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		case r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == "example.com" {
				_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone","name":"example.com"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec"}}`))
		case r.URL.Path == "/accounts/acc/cfd_tunnel/tun":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"tun","connections":[{"id":"c"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()
	svc.NewCF = func(token string) *cfapi.Client { return &cfapi.Client{Base: srv.URL, Token: token} }
	if err := svc.Save(context.Background(), SaveInput{Mode: "api", APIToken: apiTok, ExposeAdmin: true, AdminHostname: "feld.example.com"}); err != nil {
		t.Fatal(err)
	}
	view := svc.View(context.Background())
	raw, _ := json.Marshal(view)
	if bytes.Contains(raw, []byte(apiTok)) || bytes.Contains(raw, []byte(connector)) || bytes.Contains(logs.Bytes(), []byte(apiTok)) || bytes.Contains(logs.Bytes(), []byte(connector)) {
		t.Fatalf("secret leaked: %s %s", raw, logs.Bytes())
	}
	if view.TunnelID != "tun" || !view.ExposeAdmin || view.AdminWarning == "" || view.Status.State != "healthy" {
		t.Fatalf("view %+v", view)
	}
	if !strings.Contains(ingress, "feld.example.com") || !strings.Contains(ingress, "http_status:404") || !strings.Contains(ingress, "host.docker.internal:8787") {
		t.Fatalf("ingress %s", ingress)
	}
	if len(eng.specs) != 1 {
		t.Fatal("connector not started")
	}
}

func TestPublishContainerRoute(t *testing.T) {
	svc, eng, _ := newSvc(t)
	payload, _ := json.Marshal(map[string]string{"a": "acc", "t": "tun", "s": "nope"})
	token := base64.StdEncoding.EncodeToString(payload)
	const apiTok = "super-secret-api-token-value"
	var ingress, dns string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/configurations"):
			raw, _ := io.ReadAll(r.Body)
			ingress = string(raw)
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		case r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == "example.com" {
				_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone","name":"example.com"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			dns = string(raw)
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec"}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"tun","connections":[{"id":"c"}]}}`))
		}
	}))
	defer srv.Close()
	svc.NewCF = func(tok string) *cfapi.Client { return &cfapi.Client{Base: srv.URL, Token: tok} }
	if err := svc.Save(context.Background(), SaveInput{Mode: "token", TunnelToken: token, APIToken: apiTok}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPublic("plex", "plex.example.com", 32400, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ingress, "http://host.docker.internal:8791") || strings.Contains(ingress, "http://plex:32400") || !strings.Contains(dns, "tun.cfargotunnel.com") {
		t.Fatalf("ingress %s dns %s", ingress, dns)
	}
	if len(eng.connected) == 0 || eng.connected[0] != "plex" {
		t.Fatalf("connected %+v", eng.connected)
	}
	if err := svc.SetPublic("plex", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(svc.View(context.Background()).Routes) != 0 {
		t.Fatal("route still published")
	}
}

func TestPublishGatewayOrigin(t *testing.T) {
	svc, _, _ := newSvc(t)
	svc.GatewayPort = 8791
	payload, _ := json.Marshal(map[string]string{"a": "acc", "t": "tun", "s": "nope"})
	token := base64.StdEncoding.EncodeToString(payload)
	const apiTok = "super-secret-api-token-value"
	var ingress string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/configurations"):
			raw, _ := io.ReadAll(r.Body)
			ingress = string(raw)
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		case r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == "example.com" {
				_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone","name":"example.com"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec"}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"tun","connections":[{"id":"c"}]}}`))
		}
	}))
	defer srv.Close()
	svc.NewCF = func(tok string) *cfapi.Client { return &cfapi.Client{Base: srv.URL, Token: tok} }
	if err := svc.Save(context.Background(), SaveInput{Mode: "token", TunnelToken: token, APIToken: apiTok}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPublicInput(PublicInput{Container: "plex", Hostname: "plex.example.com", Port: 32400, Enabled: true, Gateway: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ingress, "http://host.docker.internal:8791") || strings.Contains(ingress, "http://plex:32400") {
		t.Fatalf("ingress %s", ingress)
	}
}

func TestCommandArgsOmitToken(t *testing.T) {
	args := strings.Join(commandArgs("/usr/bin/cloudflared"), " ")
	if strings.Contains(args, "token") {
		t.Fatal(args)
	}
}
