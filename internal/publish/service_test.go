package publish

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/store"
)

type fakeProxy struct {
	routes  []proxy.RouteInput
	applied int
	view    proxy.View
	err     error
}

func (f *fakeProxy) SetRoute(in proxy.RouteInput) error {
	f.routes = append(f.routes, in)
	return nil
}
func (f *fakeProxy) Apply(context.Context) error { f.applied++; return f.err }
func (f *fakeProxy) View(context.Context) proxy.View {
	return f.view
}

type fakeTunnel struct {
	mode   string
	inputs []remote.PublicInput
	synced int
	view   remote.View
}

func (f *fakeTunnel) SetPublicInput(in remote.PublicInput) error {
	f.inputs = append(f.inputs, in)
	return nil
}
func (f *fakeTunnel) Sync(context.Context) error { f.synced++; return nil }
func (f *fakeTunnel) View(context.Context) remote.View {
	v := f.view
	if v.Mode == "" {
		v.Mode = f.mode
	}
	if v.Routes == nil {
		for _, in := range f.inputs {
			if in.Enabled {
				v.Routes = append(v.Routes, store.PublicRoute{Container: in.Container, Hostname: in.Hostname, Port: in.Port, Enabled: true, Gateway: in.Gateway})
			}
		}
	}
	return v
}

type resolverFunc func(context.Context, string, int) (string, error)

func (f resolverFunc) Upstream(ctx context.Context, container string, port int) (string, error) {
	return f(ctx, container, port)
}

func TestPublishWiresProxyCertAndTunnel(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Domain = "example.com"
		s.Web.Email = "admin@example.com"
		s.Remote.Mode = "api"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	px := &fakeProxy{}
	tn := &fakeTunnel{mode: "api", view: remote.View{Mode: "api", Status: remote.Health{State: "healthy", Summary: "The tunnel is connected."}}}
	svc := &Service{Store: st, Proxy: px, Remote: tn, GatewayPort: 8791}
	out, err := svc.Set(context.Background(), SetInput{
		Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessSignedIn, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Warning != "" || out.App.Hostname != "plex.example.com" || out.App.Access != AccessSignedIn {
		t.Fatalf("%+v", out)
	}
	if px.applied != 1 || len(px.routes) != 1 {
		t.Fatalf("proxy %+v", px)
	}
	route := px.routes[0]
	if !route.Enabled || !route.Gateway || route.TLS != "acme" || route.Hostname != "plex.example.com" || route.Port != 32400 {
		t.Fatalf("route %+v", route)
	}
	if tn.synced != 1 || len(tn.inputs) != 1 || !tn.inputs[0].Enabled || !tn.inputs[0].Gateway || tn.inputs[0].Hostname != "plex.example.com" {
		t.Fatalf("tunnel %+v", tn.inputs)
	}

	px.view = proxy.View{Status: "up", CAReady: true, Routes: []store.WebRoute{{
		Container: "plex", Hostname: "plex.example.com", Port: 32400, TLS: "acme", Enabled: true, Gateway: true,
	}}}
	tn.view.Routes = []store.PublicRoute{{Container: "plex", Hostname: "plex.example.com", Port: 32400, Enabled: true}}
	overview := svc.Overview(context.Background())
	if len(overview.Apps) != 1 {
		t.Fatalf("%+v", overview.Apps)
	}
	app := overview.Apps[0]
	if app.Target != "plex:32400" || app.AccessLabel != "Citizen ID: signed-in" || app.Cert != "letsencrypt" || app.CertStatus != "ready" || app.Tunnel != "connected" {
		t.Fatalf("%+v", app)
	}

	if _, err := svc.Set(context.Background(), SetInput{Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessSignedIn, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	off := px.routes[len(px.routes)-1]
	if off.Enabled || off.Container != "plex" {
		t.Fatalf("disable route %+v", off)
	}
	last := tn.inputs[len(tn.inputs)-1]
	if last.Enabled {
		t.Fatal("tunnel hostname was not removed")
	}
	if len(svc.Overview(context.Background()).Apps) != 0 {
		t.Fatal("disabled app still listed")
	}
}

func TestPublishLocalNameSkipsTunnel(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.Update(func(s *store.State) error {
		s.Web.Domain = "home.arpa"
		s.Remote.Mode = "api"
		return nil
	})
	px := &fakeProxy{}
	tn := &fakeTunnel{mode: "api"}
	svc := &Service{Store: st, Proxy: px, Remote: tn, GatewayPort: 8791}
	out, err := svc.Set(context.Background(), SetInput{Container: "Plex", Subdomain: "plex", Port: 32400, Access: AccessPublic, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Warning != "" || out.App.Hostname != "plex.home.arpa" {
		t.Fatalf("%+v", out)
	}
	if px.routes[0].TLS != "internal" || !px.routes[0].Gateway {
		t.Fatalf("route %+v", px.routes[0])
	}
	if tn.inputs[0].Enabled {
		t.Fatal("a home-network name was sent to the tunnel")
	}
	item := svc.item(store.PublishedApp{Container: "Plex", Hostname: "plex.home.arpa", Port: 32400, Access: AccessPublic, Enabled: true}, proxy.View{}, remote.View{}, false, true)
	if item.Tunnel != "skipped" {
		t.Fatal(item.Tunnel, item.TunnelDetail)
	}
}

func TestPublishPublicWithoutTunnelWarns(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Domain = "example.com"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	px := &fakeProxy{}
	tn := &fakeTunnel{mode: "off"}
	svc := &Service{Store: st, Proxy: px, Remote: tn, GatewayPort: 8791}
	out, err := svc.Set(context.Background(), SetInput{Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessAdmins, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Warning == "" || !strings.Contains(out.Warning, "Remote access is off") {
		t.Fatal(out.Warning)
	}
	if tn.inputs[0].Enabled {
		t.Fatal("tunnel was published while remote access is off")
	}
	if px.routes[0].TLS != "acme" {
		t.Fatalf("public name should ask Caddy for a certificate, tls %s", px.routes[0].TLS)
	}
}

func TestAccessStoredForCitizenID(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.Update(func(s *store.State) error {
		s.Publish.BaseDomain = "example.com"
		return nil
	})
	svc := &Service{Store: st, Proxy: &fakeProxy{}, Remote: &fakeTunnel{mode: "off"}, GatewayPort: 8791}
	_, err := svc.Set(context.Background(), SetInput{Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessGroups, Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("groups without names: %v", err)
	}
	if _, err := svc.Set(context.Background(), SetInput{
		Container: "plex", Subdomain: "plex", Port: 32400, Access: AccessGroups, Groups: []string{"Family", "kids"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	app, ok := svc.find("plex")
	if !ok || app.Access != AccessGroups || len(app.Groups) != 2 {
		t.Fatalf("%+v %v", app, ok)
	}
}

func TestGatewayProxiesAfterChecksAndAuditsBlocks(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	if err := st.Update(func(s *store.State) error {
		s.Publish.Apps = []store.PublishedApp{{
			Container: "plex", Subdomain: "plex", Hostname: "plex.example.com", Port: 32400,
			Access: AccessAdmins, Enabled: true,
		}}
		s.Publish.Protect = store.ProtectPolicy{Configured: true, RatePerSec: 1, RateBurst: 1, CountryMode: "off"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var hits int
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host != "plex.example.com" {
			t.Errorf("host %s", r.Host)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer back.Close()
	audit := &recordingAudit{}
	now := time.Unix(1_700_000_000, 0)
	svc := &Service{
		Store: st,
		Resolve: resolverFunc(func(context.Context, string, int) (string, error) {
			return strings.TrimPrefix(back.URL, "http://"), nil
		}),
		Audit:       audit,
		now:         func() time.Time { return now },
		GatewayPort: 8791,
	}
	gw := httptest.NewServer(svc.Handler())
	defer gw.Close()

	do := func() *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, gw.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "plex.example.com"
		req.Header.Set("User-Agent", "Mozilla/5.0")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := do()
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || hits != 1 {
		t.Fatalf("first %d hits %d body %s", res.StatusCode, hits, body)
	}
	res = do()
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests || hits != 1 {
		t.Fatalf("second %d hits %d", res.StatusCode, hits)
	}
	if len(audit.ev) != 1 || audit.ev[0].Action != "publish.block" || audit.ev[0].Result != "denied" || audit.ev[0].Detail["reason"] != "rate" {
		t.Fatalf("audit %+v", audit.ev)
	}
	if audit.ev[0].IP == "" || audit.ev[0].Host != "plex.example.com" || audit.ev[0].Detail["access"] != AccessAdmins {
		t.Fatalf("audit detail %+v", audit.ev[0])
	}
}

type recordingAudit struct{ ev []store.AuditEvent }

func (m *recordingAudit) Record(_ string, ev store.AuditEvent) error {
	m.ev = append(m.ev, ev)
	return nil
}
