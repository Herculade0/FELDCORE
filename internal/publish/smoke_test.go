package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestCitizenPath(t *testing.T) {
	for _, p := range []string{"/citizen", "/citizen/signin", "/citizen/check", "/citizen/webauthn/login/begin", "/citizen/webauthn/login/finish"} {
		if !isCitizenPath(p) {
			t.Fatalf("smoke: %s should pass through", p)
		}
	}
	for _, p := range []string{"/", "/api/v1/citizen/login", "/citizenhood", "/citizen/../api", "/citizen/signin/../../api/v1"} {
		if isCitizenPath(p) {
			t.Fatalf("smoke: %s must not pass through", p)
		}
	}
}

func TestCitizenPassthroughKeepsThePublicHost(t *testing.T) {
	type seen struct {
		path, query, host, xfHost, xfProto, xfFor, cf string
	}
	var got seen
	var hits atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		got = seen{
			path: r.URL.Path, query: r.URL.RawQuery, host: r.Host,
			xfHost: r.Header.Get("X-Forwarded-Host"), xfProto: r.Header.Get("X-Forwarded-Proto"),
			xfFor: r.Header.Get("X-Forwarded-For"), cf: r.Header.Get("CF-Connecting-IP"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()
	svc := &Service{APIAddr: api.URL}
	req := httptest.NewRequest(http.MethodGet, "http://plex.example.com/citizen/check?next=/", nil)
	req.Host = "plex.example.com"
	req.RemoteAddr = "127.0.0.1:9"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("passthrough %d %s", rec.Code, rec.Body.String())
	}
	if got.path != "/citizen/check" || got.query != "next=/" {
		t.Fatalf("upstream url %s?%s", got.path, got.query)
	}
	if got.host != "plex.example.com" || got.xfHost != "plex.example.com" {
		t.Fatalf("host %s forwarded %s", got.host, got.xfHost)
	}
	if got.xfProto != "https" {
		t.Fatal(got.xfProto)
	}
	if got.xfFor != "203.0.113.9" || got.cf != "" {
		t.Fatalf("forwarded %q cf %q", got.xfFor, got.cf)
	}

	spoof := httptest.NewRequest(http.MethodGet, "http://plex.example.com/citizen/signin", nil)
	spoof.Host = "plex.example.com"
	spoof.RemoteAddr = "192.0.2.10:9"
	spoof.Header.Set("CF-Connecting-IP", "203.0.113.9")
	spoof.Header.Set("X-Forwarded-For", "198.51.100.4")
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, spoof)
	if hits.Load() != 2 || got.xfFor != "192.0.2.10" || got.cf != "" {
		t.Fatalf("untrusted peer forwarded %q cf %q hits %d", got.xfFor, got.cf, hits.Load())
	}

	escaped := httptest.NewRequest(http.MethodGet, "http://plex.example.com/citizen/../api/v1/citizen/login", nil)
	escaped.Host = "plex.example.com"
	escaped.URL.Path = "/citizen/../api/v1/citizen/login"
	escaped.RemoteAddr = "192.0.2.10:9"
	escaped.Header.Set("CF-Connecting-IP", "203.0.113.9")
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, escaped)
	if hits.Load() != 2 || rec.Code == http.StatusNoContent {
		t.Fatalf("escaped path was proxied: hits %d code %d path %s", hits.Load(), rec.Code, got.path)
	}
}

// TestPublishSmoke is the publish-listener exercise: an anonymous browser is
// sent to sign-in, a signed-in group member is proxied to the app, and an
// administrator is too. Administrators are allowed on a groups app.
// /citizen/* on this listener is passed through to feldcored rather than
// looping back to the sign-in redirect.
func TestPublishSmoke(t *testing.T) {
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
	if _, err := g.CreateUser("bob", "Bob", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{{
			Container: "plex", Hostname: "plex.example.com", Port: 32400, TLS: "internal", Enabled: true, Gateway: true,
		}}
		s.Publish.Apps = []store.PublishedApp{{
			Container: "plex", Hostname: "plex.example.com", Port: 32400,
			Access: AccessGroups, Groups: []string{"Family"}, TLS: "internal", Enabled: true,
		}}
		s.Publish.Protect = store.ProtectPolicy{Configured: true, RatePerSec: 100, RateBurst: 100, CountryMode: "off", BlockBots: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	var leaked atomic.Bool
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(strings.ToLower(r.Header.Get("Cookie")), "feldcore_citizen") {
			leaked.Store(true)
		}
		_, _ = io.WriteString(w, "plex-ok")
	}))
	defer app.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/citizen/signin":
			g.ServeSignIn(w, r)
		case "/citizen/check":
			g.ServeCheck(w, r)
		case "/citizen/webauthn/login/begin":
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			opts, err := g.BeginPasskeyLogin(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(opts)
		case "/citizen/webauthn/login/finish":
			if _, err := g.FinishPasskeyLogin(w, r); err != nil {
				http.Error(w, "passkey", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	svc := &Service{
		Store: st,
		Resolve: resolverFunc(func(context.Context, string, int) (string, error) {
			return strings.TrimPrefix(app.URL, "http://"), nil
		}),
		APIAddr:     api.URL,
		GatewayPort: 8791,
	}
	svc.SetAuth(CitizenAuth(g))
	gw := httptest.NewServer(svc.Handler())
	defer gw.Close()

	const host = "plex.example.com"
	anon := newSmokeClient()
	code, body, hdr := smokeDo(t, anon, http.MethodGet, gw.URL+"/", host, nil, "")
	if code != http.StatusFound || !strings.Contains(hdr.Get("Location"), "/citizen/signin") || strings.Contains(body, "plex-ok") {
		t.Fatalf("smoke: anonymous %d %s %s", code, hdr.Get("Location"), body)
	}
	if hits.Load() != 0 {
		t.Fatal("smoke: anonymous request reached the app")
	}

	ada := newSmokeClient()
	smokeSignIn(t, ada, gw.URL, host, "ada", "correct horse")
	code, body, hdr = smokeDo(t, ada, http.MethodGet, gw.URL+"/", host, nil, "")
	if code != http.StatusOK || body != "plex-ok" || hdr.Get("Location") != "" {
		t.Fatalf("smoke: signed-in member %d loc %q body %s", code, hdr.Get("Location"), body)
	}
	if leaked.Load() {
		t.Fatal("smoke: the app received the citizen cookie")
	}
	if hits.Load() != 1 {
		t.Fatalf("smoke: app hits %d, want 1", hits.Load())
	}

	code, body, _ = smokeDo(t, ada, http.MethodGet, gw.URL+"/citizen/check", host, nil, "")
	if code != http.StatusOK || strings.Contains(body, "plex-ok") {
		t.Fatalf("smoke: /citizen/check passthrough %d %s", code, body)
	}
	if hits.Load() != 1 {
		t.Fatal("smoke: /citizen/check was proxied to the app")
	}
	code, body, _ = smokeDo(t, ada, http.MethodPost, gw.URL+"/citizen/webauthn/login/begin", host, nil, "")
	if code != http.StatusOK || strings.Contains(body, "plex-ok") {
		t.Fatalf("smoke: passkey begin passthrough %d %s", code, body)
	}
	code, body, _ = smokeDo(t, ada, http.MethodPost, gw.URL+"/citizen/webauthn/login/finish", host, strings.NewReader("{}"), "application/json")
	if strings.Contains(body, "plex-ok") || hits.Load() != 1 || code == http.StatusFound {
		t.Fatalf("smoke: passkey finish passthrough %d hits %d %s", code, hits.Load(), body)
	}

	code, body, _ = smokeDo(t, ada, http.MethodGet, gw.URL+"/api/v1/citizen/me", host, nil, "")
	if code != http.StatusOK || body != "plex-ok" {
		t.Fatalf("smoke: a non-citizen path was not proxied to the app: %d %s", code, body)
	}

	admin := newSmokeClient()
	smokeSignIn(t, admin, gw.URL, host, "admin", "correct horse")
	code, body, hdr = smokeDo(t, admin, http.MethodGet, gw.URL+"/", host, nil, "")
	if code != http.StatusOK || body != "plex-ok" || hdr.Get("Location") != "" {
		t.Fatalf("smoke: administrator on a groups app %d loc %q body %s", code, hdr.Get("Location"), body)
	}

	bob := newSmokeClient()
	smokeSignIn(t, bob, gw.URL, host, "bob", "correct horse")
	hitsBefore := hits.Load()
	code, body, _ = smokeDo(t, bob, http.MethodGet, gw.URL+"/", host, nil, "")
	if code != http.StatusForbidden || strings.Contains(body, "plex-ok") || hits.Load() != hitsBefore {
		t.Fatalf("smoke: outsider %d hits %d body %s", code, hits.Load(), body)
	}
}

func newSmokeClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func smokeSignIn(t *testing.T, c *http.Client, base, host, user, pass string) {
	t.Helper()
	code, page, _ := smokeDo(t, c, http.MethodGet, base+"/citizen/signin?next=/", host, nil, "")
	if code != http.StatusOK || !strings.Contains(page, `action="/citizen/signin"`) {
		t.Fatalf("smoke: sign-in page looped for %s: %d %s", user, code, page)
	}
	token := hiddenValue(page, "csrf")
	if token == "" {
		t.Fatalf("smoke: sign-in page had no csrf token")
	}
	form := url.Values{}
	form.Set("username", user)
	form.Set("password", pass)
	form.Set("csrf", token)
	form.Set("next", "/")
	code, body, hdr := smokeDo(t, c, http.MethodPost, base+"/citizen/signin", host, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if code != http.StatusSeeOther || hdr.Get("Location") != "/" {
		t.Fatalf("smoke: sign-in for %s %d loc %q %s", user, code, hdr.Get("Location"), body)
	}
	if !strings.Contains(strings.ToLower(strings.Join(hdr.Values("Set-Cookie"), "\n")), "feldcore_citizen") {
		t.Fatalf("smoke: sign-in for %s did not set the citizen cookie: %v", user, hdr.Values("Set-Cookie"))
	}
}

func smokeDo(t *testing.T, c *http.Client, method, rawURL, host string, body io.Reader, contentType string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(buf), res.Header
}

func hiddenValue(body, name string) string {
	needle := `name="` + name + `" value="`
	i := strings.Index(body, needle)
	if i < 0 {
		return ""
	}
	rest := body[i+len(needle):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
