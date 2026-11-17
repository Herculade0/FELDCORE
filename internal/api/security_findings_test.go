package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
)

func TestManagementHostRefusesForeignCookie(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.UIHostname = "feldcore.home.arpa"
		s.Remote.ExposeAdmin = true
		s.Remote.AdminHostname = "admin.home.arpa"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{
		Auth:   accounts,
		System: system.NewCollector(),
		Proxy:  &proxy.Service{Store: st},
	})

	evil := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	evil.Host = "evil.example"
	evil.RemoteAddr = "203.0.113.8:9"
	evil.Header.Set("Content-Type", "application/json")
	evil.Header.Set("X-Forwarded-Host", "127.0.0.1")
	evil.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, evil)
	if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("foreign login %d cookies %d body %s", rec.Code, len(rec.Result().Cookies()), rec.Body.String())
	}

	cookie := loginCookie(t, srv)
	stolen := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	stolen.Host = "evil.example"
	stolen.RemoteAddr = "203.0.113.8:9"
	stolen.Header.Set("X-Forwarded-Host", "127.0.0.1")
	stolen.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, stolen)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stolen cookie on a foreign host: %d %s", w.Code, w.Body.String())
	}

	lan := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	lan.Host = "192.168.1.20"
	lan.RemoteAddr = "192.168.1.20:9"
	lan.Header.Set("Content-Type", "application/json")
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, lan)
	if lrec.Code != http.StatusOK {
		t.Fatalf("lan login %d %s", lrec.Code, lrec.Body.String())
	}
	var plain *http.Cookie
	for _, c := range lrec.Result().Cookies() {
		if strings.Contains(c.Name, "feldcore_session") {
			plain = c
		}
	}
	if plain == nil || plain.Name != "feldcore_session" || plain.Secure {
		t.Fatalf("lan cookie %+v", plain)
	}

	ui := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	ui.Host = "feldcore.home.arpa"
	ui.RemoteAddr = "203.0.113.9:9"
	ui.Header.Set("Content-Type", "application/json")
	urec := httptest.NewRecorder()
	srv.Router().ServeHTTP(urec, ui)
	if urec.Code != http.StatusOK {
		t.Fatalf("ui login %d %s", urec.Code, urec.Body.String())
	}
	var hostCookie *http.Cookie
	for _, c := range urec.Result().Cookies() {
		if strings.Contains(c.Name, "feldcore_session") {
			hostCookie = c
		}
	}
	if hostCookie == nil || hostCookie.Name != "__Host-feldcore_session" || !hostCookie.Secure || hostCookie.Domain != "" {
		t.Fatalf("ui cookie %+v", hostCookie)
	}
}

func TestTunnelPeerSetsSecureAdminCookie(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Remote.ExposeAdmin = true
		s.Remote.AdminHostname = "admin.home.arpa"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	tunnel := net.ParseIP("172.18.0.5")
	g := citizenid.Open(citizenid.Deps{
		Store: st,
		Trust: func(_ context.Context, ip net.IP) bool {
			return ip != nil && ip.Equal(tunnel)
		},
	})
	srv := New(Deps{Auth: accounts, Citizen: g, System: system.NewCollector()})

	login := func(remote string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
		req.Host = "host.docker.internal:8787"
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Host", "admin.home.arpa")
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	trusted := login("172.18.0.5:9")
	if trusted.Code != http.StatusOK {
		t.Fatalf("tunnel login %d %s", trusted.Code, trusted.Body.String())
	}
	var c *http.Cookie
	for _, cookie := range trusted.Result().Cookies() {
		if strings.Contains(cookie.Name, "feldcore_session") {
			c = cookie
		}
	}
	if c == nil || c.Name != "__Host-feldcore_session" || !c.Secure {
		t.Fatalf("tunnel cookie %+v", c)
	}
	ignored := login("203.0.113.8:9")
	if ignored.Code != http.StatusForbidden {
		t.Fatalf("untrusted forwarded host was accepted: %d %s", ignored.Code, ignored.Body.String())
	}
}

func TestHighRiskNeedsAdministrator(t *testing.T) {
	spec := docker.Spec{Name: "app", Image: "nginx", Privileged: true, DeployAnyway: true}
	tokenReq := httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers", nil)
	tokenReq = tokenReq.WithContext(withPrincipal(tokenReq.Context(), principal{
		ViaToken: true, Scopes: []string{auth.ScopeContainers},
	}))
	w := httptest.NewRecorder()
	if (&Server{}).allowRisk(w, tokenReq, &spec) || w.Code != http.StatusForbidden {
		t.Fatalf("token ack %d", w.Code)
	}

	adminReq := httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers", nil)
	adminReq = adminReq.WithContext(withPrincipal(adminReq.Context(), principal{Username: "admin"}))
	held := docker.Spec{Name: "app", Image: "nginx", Privileged: true}
	w = httptest.NewRecorder()
	if (&Server{}).allowRisk(w, adminReq, &held) || w.Code != http.StatusConflict {
		t.Fatalf("admin without ack %d %s", w.Code, w.Body.String())
	}
	acked := docker.Spec{Name: "app", Image: "nginx", Privileged: true, DeployAnyway: true}
	w = httptest.NewRecorder()
	if !(&Server{}).allowRisk(w, adminReq, &acked) {
		t.Fatalf("admin with ack %d %s", w.Code, w.Body.String())
	}

	srv, _ := newStackServer(t)
	cookie := loginCookie(t, srv)
	plain := `{"name":"media","compose":"services:\n  web:\n    image: nginx:alpine\n","isolate":true,"source":"paste"}`
	if rec := call(srv, http.MethodPost, "/api/v1/stacks", plain, "", cookie); rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	minted := call(srv, http.MethodPost, "/api/v1/agent/tokens", `{"name":"stacks","scopes":["stacks"]}`, "", cookie)
	if minted.Code != http.StatusCreated {
		t.Fatalf("token %d %s", minted.Code, minted.Body.String())
	}
	token := minted.Body.String()
	i := strings.Index(token, `"token":"`)
	if i < 0 {
		t.Fatal(token)
	}
	token = token[i+len(`"token":"`):]
	token = token[:strings.Index(token, `"`)]
	risky := `{"compose":"services:\n  web:\n    image: nginx\n    privileged: true\n","confirm":true,"deployAnyway":true}`
	if rec := call(srv, http.MethodPost, "/api/v1/stacks/media/deploy", risky, token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("stack token %d %s", rec.Code, rec.Body.String())
	}
}

func TestRawXMLRequiresAdminScope(t *testing.T) {
	srv, st := newAgentServer(t)
	cookie := loginCookie(t, srv)
	tok, _, err := auth.New(st).CreateToken("vms", []string{auth.ScopeVMs}, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"xml":"<domain type='kvm'><name>box</name></domain>"}`
	if rec := call(srv, http.MethodPut, "/api/v1/vms/box/xml", body, tok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("vms token %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodPut, "/api/v1/vms/box/xml", body, "", cookie); rec.Code == http.StatusForbidden {
		t.Fatalf("admin session was refused: %s", rec.Body.String())
	}
}
