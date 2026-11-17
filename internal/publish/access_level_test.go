package publish

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestPublishEnforcesTheStricterAccess(t *testing.T) {
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
	if err := g.SetAppAccess("plex", citizenid.LevelUsers, nil); err != nil {
		t.Fatal(err)
	}
	proxied := false
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			return App{Container: "plex", Hostname: "plex.example.com", Port: 32400, Access: AccessPublic}, true
		},
		Auth: CitizenAuth(g),
		Proxy: func(http.ResponseWriter, *http.Request, App) {
			proxied = true
		},
	}
	req := httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil)
	req.Host = "plex.example.com"
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if proxied || rec.Code == http.StatusOK {
		t.Fatalf("grant was ignored because publish access is public: %d", rec.Code)
	}

	proxied = false
	empty := Handler{
		Lookup: func(*http.Request) (App, bool) {
			return App{Container: "wiki", Hostname: "wiki.example.com", Port: 80, Access: ""}, true
		},
		Auth: CitizenAuth(g),
		Proxy: func(http.ResponseWriter, *http.Request, App) {
			proxied = true
		},
	}
	req = httptest.NewRequest(http.MethodGet, "http://wiki.example.com/", nil)
	req.Host = "wiki.example.com"
	req.RemoteAddr = "127.0.0.1:9"
	rec = httptest.NewRecorder()
	empty.ServeHTTP(rec, req)
	if proxied {
		t.Fatal("an empty publish access was treated as public")
	}

	proxied = false
	open := Handler{
		Lookup: func(*http.Request) (App, bool) {
			return App{Container: "poster", Hostname: "poster.example.com", Port: 80, Access: AccessPublic}, true
		},
		Auth:  CitizenAuth(g),
		Proxy: func(http.ResponseWriter, *http.Request, App) { proxied = true },
	}
	req = httptest.NewRequest(http.MethodGet, "http://poster.example.com/", nil)
	req.Host = "poster.example.com"
	req.RemoteAddr = "127.0.0.1:9"
	rec = httptest.NewRecorder()
	open.ServeHTTP(rec, req)
	if !proxied {
		t.Fatalf("explicit public app was closed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPublishBlockIsAggregated(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	svc := &Service{Audit: accounts}
	app := App{Container: "plex", Hostname: "plex.example.com"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.8:9"
	for i := 0; i < 20; i++ {
		svc.recordBlock(req, app, Result{Reason: "rate", IP: "203.0.113.8"})
	}
	n := 0
	for _, ev := range accounts.Audit() {
		if ev.Action == "publish.block" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("publish blocks written %d times", n)
	}
}

func TestStripRemovesDuplicatePasskeyCookies(t *testing.T) {
	got := stripCookies("app=1; feldcore_passkey=a; feldcore_passkey=b; __Host-feldcore_passkey=c; theme=dark",
		"feldcore_citizen", "__Host-feldcore_citizen",
		"feldcore_desk", "__Host-feldcore_desk",
		"feldcore_session", "__Host-feldcore_session",
		"feldcore_passkey", "__Host-feldcore_passkey",
	)
	if strings.Contains(got, "passkey") || !strings.Contains(got, "app=1") || !strings.Contains(got, "theme=dark") {
		t.Fatalf("cookie %q", got)
	}
}
