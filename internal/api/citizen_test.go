package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func citizenServer(t *testing.T) (*Server, *citizenid.Gateway) {
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
	g := citizenid.Open(citizenid.Deps{Store: st, Secrets: box, Audit: accounts})
	return New(Deps{Auth: accounts, Citizen: g}), g
}

func TestDirectoryUserCannotManage(t *testing.T) {
	srv, g := citizenServer(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"ada","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.80:9"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"admin":true`) {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "feldcore_citizen" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session")
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		return w
	}
	settings := call(http.MethodGet, "/api/v1/audit")
	if settings.Code != http.StatusForbidden || !strings.Contains(settings.Body.String(), "administrator") {
		t.Fatalf("audit %d %s", settings.Code, settings.Body.String())
	}
	dir := call(http.MethodGet, "/api/v1/citizen")
	if dir.Code != http.StatusForbidden {
		t.Fatalf("directory %d %s", dir.Code, dir.Body.String())
	}
	me := call(http.MethodGet, "/api/v1/citizen/me")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"ada"`) {
		t.Fatalf("me %d %s", me.Code, me.Body.String())
	}
	launch := call(http.MethodGet, "/api/v1/launcher")
	if launch.Code != http.StatusOK || !strings.Contains(launch.Body.String(), `"apps"`) {
		t.Fatalf("launcher %d %s", launch.Code, launch.Body.String())
	}

	legacy := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	legacy.Header.Set("Content-Type", "application/json")
	legacy.RemoteAddr = "192.0.2.81:9"
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, legacy)
	if lrec.Code != http.StatusOK {
		t.Fatalf("legacy %d %s", lrec.Code, lrec.Body.String())
	}
	admin := lrec.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	r.AddCookie(admin)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin audit %d %s", w.Code, w.Body.String())
	}

	page := httptest.NewRequest(http.MethodGet, "/citizen/signin", nil)
	prec := httptest.NewRecorder()
	srv.Router().ServeHTTP(prec, page)
	if prec.Code != http.StatusOK || !strings.Contains(prec.Body.String(), "<h1>Citizen ID</h1>") {
		t.Fatalf("page %d", prec.Code)
	}
}

func TestCitizenLoginAsksForTOTP(t *testing.T) {
	srv, g := citizenServer(t)
	id, ok := g.IdentityForUsername("admin")
	if !ok {
		t.Fatal("admin")
	}
	setup, err := g.BeginTOTP(id)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ConfirmTOTP(id, code); err != nil {
		t.Fatal(err)
	}

	legacy := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	legacy.Header.Set("Content-Type", "application/json")
	legacy.RemoteAddr = "192.0.2.82:9"
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, legacy)
	if lrec.Code != http.StatusOK {
		t.Fatalf("legacy login still requires a code: %d %s", lrec.Code, lrec.Body.String())
	}

	first := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	first.Header.Set("Content-Type", "application/json")
	first.RemoteAddr = "192.0.2.83:9"
	frec := httptest.NewRecorder()
	srv.Router().ServeHTTP(frec, first)
	if frec.Code != http.StatusOK || !strings.Contains(frec.Body.String(), `"totpRequired":true`) {
		t.Fatalf("totp prompt %d %s", frec.Code, frec.Body.String())
	}
	if len(frec.Result().Cookies()) != 0 {
		t.Fatalf("prompt set a cookie: %v", frec.Result().Cookies())
	}

	second := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse","totp":"`+code+`"}`))
	second.Header.Set("Content-Type", "application/json")
	second.RemoteAddr = "192.0.2.84:9"
	srec := httptest.NewRecorder()
	srv.Router().ServeHTTP(srec, second)
	if srec.Code != http.StatusOK || !strings.Contains(srec.Body.String(), `"admin":true`) {
		t.Fatalf("totp login %d %s", srec.Code, srec.Body.String())
	}
}
