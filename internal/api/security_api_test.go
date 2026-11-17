package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestAppAccessReportsProxyFailure(t *testing.T) {
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
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{{Container: "files", Hostname: "files.home.arpa", Port: 80, TLS: "internal", Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{
		Auth: accounts, Citizen: g,
		Proxy: &proxy.Service{Store: st, DataDir: dir, ListenPort: 8787, Listen: func() []int { return nil }},
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Host = "127.0.0.1"
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.90:9"
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, login)
	if lrec.Code != http.StatusOK {
		t.Fatalf("login %d %s", lrec.Code, lrec.Body.String())
	}
	var session *http.Cookie
	for _, c := range lrec.Result().Cookies() {
		if strings.Contains(c.Name, "feldcore_session") {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no management session")
	}
	save := httptest.NewRequest(http.MethodPut, "/api/v1/citizen/apps/files", strings.NewReader(`{"level":"users"}`))
	save.Host = "127.0.0.1"
	save.Header.Set("Content-Type", "application/json")
	save.AddCookie(session)
	srec := httptest.NewRecorder()
	srv.Router().ServeHTTP(srec, save)
	if srec.Code != http.StatusInternalServerError || !strings.Contains(srec.Body.String(), "Docker") {
		t.Fatalf("apply %d %s", srec.Code, srec.Body.String())
	}
	found := false
	for _, grant := range st.Snapshot().AppGrants {
		if grant.Container == "files" && grant.Level == "users" {
			found = true
		}
	}
	if !found {
		t.Fatal("access was not saved before the proxy failure")
	}
}

func TestChangingAFactorRequiresReauth(t *testing.T) {
	srv, _ := citizenServer(t)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Host = "127.0.0.1"
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.91:9"
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, login)
	if lrec.Code != http.StatusOK {
		t.Fatalf("login %d %s", lrec.Code, lrec.Body.String())
	}
	var session *http.Cookie
	for _, c := range lrec.Result().Cookies() {
		if strings.Contains(c.Name, "feldcore_session") {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no management session")
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1"
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(session)
		req.RemoteAddr = "192.0.2.91:9"
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, req)
		return w
	}
	begin := call(http.MethodPost, "/api/v1/citizen/totp/begin", "")
	if begin.Code != http.StatusOK || !strings.Contains(begin.Body.String(), "secret") {
		t.Fatalf("begin %d %s", begin.Code, begin.Body.String())
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(begin.Body.Bytes(), &setup); err != nil || setup.Secret == "" {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodPost, "/api/v1/citizen/totp/confirm", `{"code":"`+code+`"}`); got.Code != http.StatusOK {
		t.Fatalf("confirm %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPost, "/api/v1/citizen/totp/begin", `{}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("replace without reauth %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPost, "/api/v1/citizen/users/admin/totp/reset", `{}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("reset without reauth %d %s", got.Code, got.Body.String())
	}
	next, err := totp.GenerateCode(setup.Secret, time.Now().Add(30*time.Second))
	if err != nil || next == code {
		next, err = totp.GenerateCode(setup.Secret, time.Now().Add(60*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	body := `{"password":"correct horse","totp":"` + next + `"}`
	if got := call(http.MethodPost, "/api/v1/citizen/users/admin/totp/reset", body); got.Code != http.StatusOK {
		t.Fatalf("reset with reauth %d %s", got.Code, got.Body.String())
	}
}
