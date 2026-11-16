package citizenid

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func fastArgon() argonParams {
	return argonParams{time: 1, memory: 8, threads: 1, keyLen: 32}
}

func newGateway(t *testing.T) (*Gateway, *store.Store) {
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
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	g := Open(Deps{Store: st, Secrets: box, Audit: accounts})
	g.argon = fastArgon()
	return g, st
}

func signIn(t *testing.T, g *Gateway, username, password, code, remote string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", nil)
	req.RemoteAddr = remote
	req.Host = "feldcore.home.arpa"
	rec := httptest.NewRecorder()
	if _, err := g.Login(rec, req, username, password, code); err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			if !c.HttpOnly {
				t.Fatal("session cookie must be HttpOnly")
			}
			return c
		}
	}
	t.Fatal("no citizen session cookie")
	return nil
}

func authed(cookie *http.Cookie, host string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/library", nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.40:9"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func TestAccessDecisions(t *testing.T) {
	g, st := newGateway(t)
	family, err := g.CreateGroup("Family", "People in the house")
	if err != nil {
		t.Fatal(err)
	}
	ada, err := g.CreateUser("ada", "Ada", "correct horse", []string{family.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateUser("bob", "Bob", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Domain = "home.arpa"
		s.Web.Routes = []store.WebRoute{
			{Container: "files", Hostname: "files.home.arpa", Port: 80, TLS: "off", Enabled: true},
			{Container: "adminpanel", Hostname: "admin.home.arpa", Port: 80, TLS: "internal", Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.SetAppAccess("files", LevelGroups, []string{family.ID}); err != nil {
		t.Fatal(err)
	}
	if err := g.SetAppAccess("adminpanel", LevelAdmin, nil); err != nil {
		t.Fatal(err)
	}

	public := AppAccess{ID: "poster", Host: "poster.home.arpa", Level: LevelPublic}
	if d, err := g.Authorize(authed(nil, "poster.home.arpa"), public); err != nil || !d.Allow || d.Reason != "public" {
		t.Fatalf("public %+v %v", d, err)
	}

	users := AppAccess{ID: "wiki", Host: "wiki.home.arpa", Level: LevelUsers}
	anon, err := g.Authorize(authed(nil, "wiki.home.arpa"), users)
	if err != nil || anon.Allow || anon.Reason != "unauthenticated" || !strings.HasPrefix(anon.Location, "/citizen/signin?next=") {
		t.Fatalf("anon %+v %v", anon, err)
	}
	if strings.Contains(anon.Location, "evil.example") {
		t.Fatal(anon.Location)
	}
	before := len(st.Snapshot().Audit)
	if _, err := g.Authorize(authed(nil, "wiki.home.arpa"), users); err != nil {
		t.Fatal(err)
	}
	if len(st.Snapshot().Audit) != before {
		t.Fatal("an anonymous challenge was written to the audit log")
	}

	adaCookie := signIn(t, g, "ada", "correct horse", "", "192.0.2.10:9")
	if d, err := g.Authorize(authed(adaCookie, "wiki.home.arpa"), users); err != nil || !d.Allow || d.Identity == nil || d.Identity.Username != "ada" {
		t.Fatalf("signed-in %+v %v", d, err)
	}
	files, ok := g.AppForHost("files.home.arpa")
	if !ok || files.Level != LevelGroups {
		t.Fatalf("app %+v %v", files, ok)
	}
	if d, err := g.Authorize(authed(adaCookie, "files.home.arpa"), files); err != nil || !d.Allow {
		t.Fatalf("group member %+v %v", d, err)
	}

	bobCookie := signIn(t, g, "bob", "correct horse", "", "192.0.2.11:9")
	denied, err := g.Authorize(authed(bobCookie, "files.home.arpa"), files)
	if err != nil || denied.Allow || denied.Reason != "forbidden" {
		t.Fatalf("outsider %+v %v", denied, err)
	}
	audited := false
	for _, ev := range st.Snapshot().Audit {
		if ev.TokenName == "bob" && ev.Action == "access denied" && ev.Result == "denied" && ev.TokenID == "" {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("denial not audited: %+v", st.Snapshot().Audit)
	}

	empty := AppAccess{ID: "secret", Level: LevelGroups}
	if d, err := g.Authorize(authed(bobCookie, "secret.home.arpa"), empty); err != nil || d.Allow {
		t.Fatalf("empty group list %+v %v", d, err)
	}
	unknown := AppAccess{ID: "secret", Level: "nope"}
	if d, err := g.Authorize(authed(bobCookie, "secret.home.arpa"), unknown); err != nil || d.Allow || d.Reason != "forbidden" {
		t.Fatalf("unknown level %+v %v", d, err)
	}

	panel, _ := g.AppForHost("admin.home.arpa")
	if d, err := g.Authorize(authed(adaCookie, "admin.home.arpa"), panel); err != nil || d.Allow || d.Reason != "forbidden" {
		t.Fatalf("admin app %+v %v", d, err)
	}
	adminCookie := signIn(t, g, "admin", "correct horse", "", "192.0.2.12:9")
	for _, app := range []AppAccess{users, files, panel, unknown} {
		d, err := g.Authorize(authed(adminCookie, "admin.home.arpa"), app)
		if err != nil || !d.Allow || d.Identity == nil || !d.Identity.Admin {
			t.Fatalf("admin bypass %+v %+v %v", app, d, err)
		}
	}

	off := false
	if _, err := g.UpdateUser(ada.ID, UserPatch{Disabled: &off}); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := g.UpdateUser(ada.ID, UserPatch{Disabled: &on}); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Identity(authed(adaCookie, "feldcore.home.arpa")); ok {
		t.Fatal("disabled account kept its session")
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.0.2.13:9"
	if _, err := g.Login(httptest.NewRecorder(), req, "ada", "correct horse", ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled login: %v", err)
	}

	raw, err := json.Marshal(st.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), adminCookie.Value) || adminCookie.Value == "" {
		t.Fatal("session token is present in the state file")
	}
	for _, se := range st.Snapshot().CitizenSessions {
		if se.TokenHash == adminCookie.Value {
			t.Fatal("cookie stored without hashing")
		}
	}

	id, ok := g.Identity(authed(bobCookie, "feldcore.home.arpa"))
	if !ok {
		t.Fatal("bob session did not load")
	}
	if err := g.SetPassword(id.ID, "another horse"); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Identity(authed(bobCookie, "feldcore.home.arpa")); ok {
		t.Fatal("password reset left the session valid")
	}
}

func TestSignInPageCSRFAndRedirect(t *testing.T) {
	g, st := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Domain = "home.arpa"
		s.Web.Routes = []store.WebRoute{{Container: "files", Hostname: "files.home.arpa", Port: 80, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	get := httptest.NewRequest(http.MethodGet, "/citizen/signin?next=https://evil.example/phish", nil)
	get.Host = "feldcore.home.arpa"
	page := httptest.NewRecorder()
	g.ServeSignIn(page, get)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "<h1>Citizen ID</h1>") {
		t.Fatalf("page %d %s", page.Code, body)
	}
	if hidden(t, body, "next") != "/" {
		t.Fatalf("open redirect kept: %s", hidden(t, body, "next"))
	}
	csrf := hidden(t, body, "csrf")
	var csrfCookie *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == csrfCookieName() {
			csrfCookie = c
		}
	}
	if csrfCookie == nil || csrf == "" {
		t.Fatal("csrf cookie or token missing")
	}

	bad := postSignIn(g, csrfCookie, "", "ada", "correct horse", "/")
	if bad.Code != http.StatusForbidden {
		t.Fatalf("missing csrf %d", bad.Code)
	}
	wrong := postSignIn(g, csrfCookie, csrf, "ada", "nope", "/")
	if wrong.Code != http.StatusUnauthorized || citizenCookie(wrong) != nil {
		t.Fatalf("bad password %d cookies=%v", wrong.Code, wrong.Result().Cookies())
	}

	// The failed attempt issued a fresh token.
	csrf = hidden(t, wrong.Body.String(), "csrf")
	for _, c := range wrong.Result().Cookies() {
		if c.Name == csrfCookieName() {
			csrfCookie = c
		}
	}
	ok := postSignIn(g, csrfCookie, csrf, "ada", "correct horse", "https://files.home.arpa/library")
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("sign-in %d %s", ok.Code, ok.Body.String())
	}
	if loc := ok.Header().Get("Location"); loc != "https://files.home.arpa/library" {
		t.Fatalf("location %s", loc)
	}
	if citizenCookie(ok) == nil {
		t.Fatal("successful form sign-in did not set a session")
	}

	safe := httptest.NewRequest(http.MethodGet, "/citizen/signin?next=/apps", nil)
	safe.Host = "feldcore.home.arpa"
	again := httptest.NewRecorder()
	g.ServeSignIn(again, safe)
	if hidden(t, again.Body.String(), "next") != "/apps" {
		t.Fatalf("relative next: %s", again.Body.String())
	}
}

func TestLockout(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "198.51.100.8:9"
	for i := 0; i < 8; i++ {
		_, err := g.Login(httptest.NewRecorder(), req, "ada", "wrong-password", "")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	other := httptest.NewRequest(http.MethodPost, "/login", nil)
	other.RemoteAddr = "198.51.100.9:9"
	if _, err := g.Login(httptest.NewRecorder(), other, "ada", "correct horse", ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("user lockout: %v", err)
	}
}

func TestSessionCookieDomain(t *testing.T) {
	g, st := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Domain = "home.arpa"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.0.2.50:9"
	req.Host = "files.home.arpa"
	rec := httptest.NewRecorder()
	if _, err := g.Login(rec, req, "ada", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	c := citizenCookie(rec)
	if c == nil || c.Domain != "home.arpa" {
		t.Fatalf("domain cookie %+v", c)
	}

	local := httptest.NewRequest(http.MethodPost, "/login", nil)
	local.RemoteAddr = "192.0.2.51:9"
	local.Host = "localhost:8787"
	rec = httptest.NewRecorder()
	if _, err := g.Login(rec, local, "ada", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	c = citizenCookie(rec)
	if c == nil || c.Domain != "" {
		t.Fatalf("localhost cookie %+v", c)
	}
}

func csrfCookieName() string { return csrfCookie }

func hidden(t *testing.T, body, name string) string {
	t.Helper()
	key := `name="` + name + `" value="`
	i := strings.Index(body, key)
	if i < 0 {
		t.Fatalf("no %s field in %s", name, body)
	}
	rest := body[i+len(key):]
	j := strings.Index(rest, `"`)
	return rest[:j]
}

func postSignIn(g *Gateway, csrf *http.Cookie, token, username, password, next string) *httptest.ResponseRecorder {
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	form.Set("csrf", token)
	form.Set("next", next)
	req := httptest.NewRequest(http.MethodPost, "/citizen/signin", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.20:9"
	req.Host = "feldcore.home.arpa"
	if csrf != nil {
		req.AddCookie(csrf)
	}
	rec := httptest.NewRecorder()
	g.ServeSignIn(rec, req)
	return rec
}

func citizenCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestMiddlewareRedirects(t *testing.T) {
	g, _ := newGateway(t)
	h := g.Middleware(AppAccess{ID: "wiki", Host: "wiki.home.arpa", Level: LevelUsers})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "secret")
	}))
	req := httptest.NewRequest(http.MethodGet, "http://wiki.home.arpa/", nil)
	req.Host = "wiki.home.arpa"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/citizen/signin?next=") {
		t.Fatalf("middleware %d %s", rec.Code, rec.Header().Get("Location"))
	}
}
