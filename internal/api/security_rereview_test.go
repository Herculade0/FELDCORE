package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestCitizenCookieIsNotADeskSession(t *testing.T) {
	srv, g := citizenServer(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"ada","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.40:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	var citizen *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "citizen") {
			citizen = c
		}
	}
	if citizen == nil {
		t.Fatal("no citizen cookie")
	}
	replay := httptest.NewRequest(http.MethodGet, "/api/v1/citizen/me", nil)
	replay.AddCookie(&http.Cookie{Name: "feldcore_desk", Value: citizen.Value})
	replay.RemoteAddr = "192.0.2.40:9"
	out := httptest.NewRecorder()
	srv.Router().ServeHTTP(out, replay)
	if out.Code != http.StatusUnauthorized {
		t.Fatalf("citizen cookie accepted as the desk cookie: %d %s", out.Code, out.Body.String())
	}
}

func TestPasskeyAddRequiresPassword(t *testing.T) {
	srv, _ := citizenServer(t)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.41:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	var desk *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "desk") {
			desk = c
		}
	}
	if desk == nil {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	begin := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/webauthn/register/begin", strings.NewReader(`{}`))
	begin.Header.Set("Content-Type", "application/json")
	begin.AddCookie(desk)
	begin.RemoteAddr = "192.0.2.41:9"
	out := httptest.NewRecorder()
	srv.Router().ServeHTTP(out, begin)
	if out.Code != http.StatusUnauthorized {
		t.Fatalf("passkey enrolment without a password: %d %s", out.Code, out.Body.String())
	}
}

func TestPasskeyOriginIsPinned(t *testing.T) {
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
	g := citizenid.Open(citizenid.Deps{
		Store: st, Secrets: box, Audit: accounts,
		Trust: func(context.Context, net.IP) bool { return true },
	})
	srv := New(Deps{Auth: accounts, Citizen: g})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/webauthn/login/begin", nil)
	req.Host = "localhost"
	req.RemoteAddr = "203.0.113.9:9"
	req.Header.Set("X-Forwarded-Host", "evil.example")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("unconfigured origin was accepted: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not available") {
		t.Fatalf("origin %d %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyLoginSharesCitizenLockout(t *testing.T) {
	srv, _ := citizenServer(t)
	for i := 0; i < 8; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.50:9"
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	legacy := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	legacy.Header.Set("Content-Type", "application/json")
	legacy.RemoteAddr = "192.0.2.50:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, legacy)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("legacy login bypassed the citizen lockout: %d %s", rec.Code, rec.Body.String())
	}
	other := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	other.Header.Set("Content-Type", "application/json")
	other.RemoteAddr = "192.0.2.51:9"
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, other)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("per-user lockout did not follow the account: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyLoginRejectsOverlongPassword(t *testing.T) {
	srv, _ := citizenServer(t)
	body := `{"username":"admin","password":"` + strings.Repeat("a", 80) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.52:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("over-long password: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHostCookieLogoutEndsTheSession(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Auth: accounts})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	login.TLS = &tls.ConnectionState{}
	login.RemoteAddr = "192.0.2.60:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-feldcore_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("HTTPS login did not set __Host-feldcore_session: %v", rec.Result().Cookies())
	}
	out := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	out.TLS = &tls.ConnectionState{}
	out.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, out)
	if w.Code != http.StatusOK {
		t.Fatalf("logout %d %s", w.Code, w.Body.String())
	}
	if _, ok := accounts.Authenticate(cookie.Value); ok {
		t.Fatal("HTTPS sign-out left the server session alive")
	}
}

func TestPasswordChangeRevokesOtherAdminSessions(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Auth: accounts})
	signIn := func(addr string) *http.Cookie {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || len(rec.Result().Cookies()) == 0 {
			t.Fatalf("login %s %d %s", addr, rec.Code, rec.Body.String())
		}
		return rec.Result().Cookies()[0]
	}
	keep := signIn("192.0.2.70:9")
	other := signIn("192.0.2.71:9")
	change := httptest.NewRequest(http.MethodPut, "/api/v1/settings/password", strings.NewReader(`{"current":"correct horse","next":"a new password"}`))
	change.Header.Set("Content-Type", "application/json")
	change.AddCookie(keep)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, change)
	if rec.Code != http.StatusOK {
		t.Fatalf("change %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := accounts.Authenticate(other.Value); ok {
		t.Fatal("the other admin session survived the password change")
	}
	if _, ok := accounts.Authenticate(keep.Value); !ok {
		t.Fatal("the session that changed the password was revoked")
	}
}

func TestAppHostnameDoesNotReceiveManagementSession(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Publish.Apps = []store.PublishedApp{{
			Container: "plex", Hostname: "plex.example.com", Port: 32400, Access: "public", Enabled: true,
		}}
		return nil
	}); err != nil {
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
	srv := New(Deps{Auth: accounts, Citizen: g})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Host = "plex.example.com"
	login.RemoteAddr = "192.0.2.80:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	citizen := false
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "desk") || strings.Contains(c.Name, "session") {
			t.Fatalf("app hostname minted a management cookie %s", c.Name)
		}
		if strings.Contains(c.Name, "citizen") {
			citizen = true
		}
	}
	if !citizen {
		t.Fatal("the app sign-in did not set the citizen cookie")
	}
}

func TestWizardRejectsAgentTokenAfterSetup(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	wiz := setup.New(setup.Deps{Store: st, DataDir: dir, Root: func() bool { return false }})
	wiz.UseAccounts(accounts)
	srv := New(Deps{Auth: accounts, Setup: wiz})
	token, _, err := accounts.CreateToken("owner", []string{auth.ScopeAdmin}, 0)
	if err != nil {
		t.Fatal(err)
	}
	rec := call(srv, http.MethodGet, "/api/v1/setup/wizard", "", token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin token reached the wizard: %d %s", rec.Code, rec.Body.String())
	}
	cookie := loginCookie(t, srv)
	rec = call(srv, http.MethodGet, "/api/v1/setup/wizard", "", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("session wizard %d %s", rec.Code, rec.Body.String())
	}
}

func TestBackupSecretsNeedAnAdministrator(t *testing.T) {
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
	b := &backup.Service{Store: st, Secrets: box, DataDir: dir}
	srv := New(Deps{Auth: accounts, Backups: b})
	cookie := loginCookie(t, srv)
	writeTok, _, err := accounts.CreateToken("writer", []string{auth.ScopeBackupsWrite}, 0)
	if err != nil {
		t.Fatal(err)
	}
	secretBody := `{"name":"Nightly","kind":"local","enabled":true,"path":"` + filepath.Join(dir, "dest") + `","scheduleMode":"manual","sets":{"config":true,"secrets":true}}`
	rec := call(srv, http.MethodPost, "/api/v1/backup/destinations", secretBody, writeTok, nil)
	if rec.Code == http.StatusCreated || !strings.Contains(rec.Body.String(), "administrator") {
		t.Fatalf("writer included secrets: %d %s", rec.Code, rec.Body.String())
	}
	plain := `{"name":"Nightly","kind":"local","enabled":true,"path":"` + filepath.Join(dir, "dest") + `","scheduleMode":"manual","sets":{"config":true}}`
	rec = call(srv, http.MethodPost, "/api/v1/backup/destinations", plain, "", cookie)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), `"secrets":true`) {
		t.Fatalf("default secrets %d %s", rec.Code, rec.Body.String())
	}
	var dest struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dest); err != nil || dest.ID == "" {
		t.Fatalf("dest %v %s", err, rec.Body.String())
	}
	restore := `{"destinationId":"` + dest.ID + `","snapshotId":"abcdef0123456789","paths":["/var/lib/feldcore/state.json"]}`
	rec = call(srv, http.MethodPost, "/api/v1/backup/restore", restore, writeTok, nil)
	if rec.Code == http.StatusAccepted || !strings.Contains(rec.Body.String(), "administrator") {
		t.Fatalf("writer restored state: %d %s", rec.Code, rec.Body.String())
	}
	opt := `{"name":"Secrets","kind":"local","enabled":true,"path":"` + filepath.Join(dir, "secret-dest") + `","scheduleMode":"manual","sets":{"config":true,"secrets":true}}`
	rec = call(srv, http.MethodPost, "/api/v1/backup/destinations", opt, "", cookie)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"secrets":true`) {
		t.Fatalf("admin opt-in %d %s", rec.Code, rec.Body.String())
	}
}
