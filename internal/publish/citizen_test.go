package publish

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestStripUpstreamDropsCitizenCredentialsOnly(t *testing.T) {
	got := stripCookies("app=1; feldcore_citizen=secret; theme=dark; __Host-feldcore_session=nope",
		"feldcore_citizen", "__Host-feldcore_citizen",
		"feldcore_desk", "__Host-feldcore_desk",
		"feldcore_session", "__Host-feldcore_session",
	)
	if strings.Contains(got, "feldcore") || !strings.Contains(got, "app=1") || !strings.Contains(got, "theme=dark") {
		t.Fatalf("cookie %q", got)
	}
	if stripCookies("feldcore_citizen=only", "feldcore_citizen") != "" {
		t.Fatal("sole citizen cookie was kept")
	}
}

func TestGatedPublishIsNotProxiedWithoutAuth(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	g := citizenid.Open(citizenid.Deps{Store: st, Secrets: box, Audit: accounts})
	family, err := g.CreateGroup("Family", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateUser("ada", "Ada", "correct horse", []string{family.ID}); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest(http.MethodPost, "/login", nil)
	login.Host = "localhost"
	login.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	ada, err := g.Login(rec, login, "ada", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "citizen") {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no citizen cookie")
	}

	proxied := false
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			return App{Container: "plex", Hostname: "plex.example.com", Port: 32400, Access: AccessGroups, Groups: []string{"Family"}}, true
		},
		Auth: CitizenAuth(g),
		Proxy: func(http.ResponseWriter, *http.Request, App) {
			proxied = true
		},
	}
	anon := httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil)
	anon.Host = "plex.example.com"
	anon.RemoteAddr = "127.0.0.1:9"
	out := httptest.NewRecorder()
	h.ServeHTTP(out, anon)
	if proxied || out.Code == http.StatusOK {
		t.Fatalf("gated app was proxied without a session: %d", out.Code)
	}
	if out.Code != http.StatusFound || !strings.Contains(out.Header().Get("Location"), "/citizen/signin") {
		t.Fatalf("anon %d %s", out.Code, out.Header().Get("Location"))
	}

	proxied = false
	bob := httptest.NewRequest(http.MethodPost, "/login", nil)
	bob.Host = "localhost"
	bob.RemoteAddr = "127.0.0.1:10"
	if _, err := g.CreateUser("bob", "Bob", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	brec := httptest.NewRecorder()
	if _, err := g.Login(brec, bob, "bob", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	var bobCookie *http.Cookie
	for _, c := range brec.Result().Cookies() {
		if strings.Contains(c.Name, "citizen") {
			bobCookie = c
		}
	}
	denied := httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil)
	denied.Host = "plex.example.com"
	denied.RemoteAddr = "127.0.0.1:10"
	denied.AddCookie(bobCookie)
	out = httptest.NewRecorder()
	h.ServeHTTP(out, denied)
	if proxied || out.Code != http.StatusForbidden {
		t.Fatalf("group miss proxied=%v %d %s", proxied, out.Code, out.Body.String())
	}

	okReq := httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil)
	okReq.Host = "plex.example.com"
	okReq.RemoteAddr = "127.0.0.1:9"
	okReq.AddCookie(cookie)
	out = httptest.NewRecorder()
	h.ServeHTTP(out, okReq)
	if !proxied {
		t.Fatal("a member of the group was not proxied")
	}
	if ada.Username != "ada" {
		t.Fatal(ada.Username)
	}
}

func TestProxyStripsCitizenCookieBeforeTheApp(t *testing.T) {
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		io.WriteString(w, "app")
	}))
	defer upstream.Close()
	addr := strings.TrimPrefix(upstream.URL, "http://")
	svc := &Service{Resolve: staticUpstream{addr: addr}}
	req := httptest.NewRequest(http.MethodGet, "http://plex.example.com/web", nil)
	req.Host = "plex.example.com"
	req.RemoteAddr = "203.0.113.4:9"
	req.Header.Set("Cookie", "feldcore_citizen=stolen; app=1")
	req.Header.Set("Authorization", "Bearer stolen")
	rec := httptest.NewRecorder()
	svc.proxyTo(rec, req, App{Container: "plex", Hostname: "plex.example.com", Port: 32400, Access: AccessSignedIn})
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy %d %s", rec.Code, rec.Body.String())
	}
	if seen.Get("Authorization") != "" || strings.Contains(seen.Get("Cookie"), "feldcore_citizen") {
		t.Fatalf("upstream saw credentials: auth %q cookie %q", seen.Get("Authorization"), seen.Get("Cookie"))
	}
	if !strings.Contains(seen.Get("Cookie"), "app=1") {
		t.Fatalf("app cookie dropped: %q", seen.Get("Cookie"))
	}
}

type staticUpstream struct{ addr string }

func (s staticUpstream) Upstream(context.Context, string, int) (string, error) {
	return s.addr, nil
}
