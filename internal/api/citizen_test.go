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
	accounts.SetSecondFactor(g.AdminFactor)
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
	var citizen, desk *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "feldcore_citizen" {
			citizen = c
		}
		if c.Name == "feldcore_desk" {
			desk = c
		}
	}
	if citizen == nil || desk == nil {
		t.Fatal("login did not set both cookies")
	}
	call := func(method, path string, c *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		return w
	}
	// The parent-domain cookie is not a management session, even for a replay.
	for _, path := range []string{"/api/v1/audit", "/api/v1/citizen", "/api/v1/citizen/me", "/api/v1/launcher"} {
		got := call(http.MethodGet, path, citizen)
		if got.Code != http.StatusUnauthorized {
			t.Fatalf("citizen cookie %s %d %s", path, got.Code, got.Body.String())
		}
	}
	if got := call(http.MethodGet, "/api/v1/audit", desk); got.Code != http.StatusUnauthorized {
		t.Fatalf("desk cookie is not an administrator session %d %s", got.Code, got.Body.String())
	}
	me := call(http.MethodGet, "/api/v1/citizen/me", desk)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"ada"`) {
		t.Fatalf("me %d %s", me.Code, me.Body.String())
	}
	launch := call(http.MethodGet, "/api/v1/launcher", desk)
	if launch.Code != http.StatusOK || !strings.Contains(launch.Body.String(), `"apps"`) {
		t.Fatalf("launcher %d %s", launch.Code, launch.Body.String())
	}

	legacy := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	legacy.Host = "127.0.0.1"
	legacy.Header.Set("Content-Type", "application/json")
	legacy.RemoteAddr = "192.0.2.81:9"
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, legacy)
	if lrec.Code != http.StatusOK {
		t.Fatalf("legacy %d %s", lrec.Code, lrec.Body.String())
	}
	admin := lrec.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	r.Host = "127.0.0.1"
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
	legacy.Host = "127.0.0.1"
	legacy.Header.Set("Content-Type", "application/json")
	legacy.RemoteAddr = "192.0.2.82:9"
	lrec := httptest.NewRecorder()
	srv.Router().ServeHTTP(lrec, legacy)
	if lrec.Code != http.StatusUnauthorized || !strings.Contains(lrec.Body.String(), "authenticator") {
		t.Fatalf("legacy login without a code: %d %s", lrec.Code, lrec.Body.String())
	}

	first := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	first.Host = "127.0.0.1"
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

	next, err := totp.GenerateCode(setup.Secret, time.Now().Add(30*time.Second))
	if err != nil || next == code {
		next, err = totp.GenerateCode(setup.Secret, time.Now().Add(60*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	second := httptest.NewRequest(http.MethodPost, "/api/v1/citizen/login", strings.NewReader(`{"username":"admin","password":"correct horse","totp":"`+next+`"}`))
	second.Host = "127.0.0.1"
	second.Header.Set("Content-Type", "application/json")
	second.RemoteAddr = "192.0.2.84:9"
	srec := httptest.NewRecorder()
	srv.Router().ServeHTTP(srec, second)
	if srec.Code != http.StatusOK || !strings.Contains(srec.Body.String(), `"admin":true`) {
		t.Fatalf("totp login %d %s", srec.Code, srec.Body.String())
	}
}
