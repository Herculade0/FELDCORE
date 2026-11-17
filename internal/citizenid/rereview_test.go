package citizenid

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasskeyFactorIsNotADeskSession(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	id, ok := g.lookup("ada")
	if !ok {
		t.Fatal("ada")
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Host = "localhost"
	req.RemoteAddr = "192.0.2.90:9"
	rec := httptest.NewRecorder()
	if err := g.startSession(rec, req, id, "passkey"); err != nil {
		t.Fatal(err)
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
	asDesk := httptest.NewRequest(http.MethodGet, "/", nil)
	asDesk.AddCookie(&http.Cookie{Name: "feldcore_desk", Value: citizen.Value})
	if _, ok := g.DeskIdentity(asDesk); ok {
		t.Fatal("a passkey session was accepted as the desk session")
	}
	asCitizen := httptest.NewRequest(http.MethodGet, "/", nil)
	asCitizen.AddCookie(citizen)
	if got, ok := g.Identity(asCitizen); !ok || got.Username != "ada" {
		t.Fatalf("passkey session was not a citizen session: %+v %v", got, ok)
	}
}

func TestLockedSignInIsAggregated(t *testing.T) {
	g, st := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "203.0.113.40:9"
	for i := 0; i < 8; i++ {
		if _, err := g.Login(httptest.NewRecorder(), req, "ada", "wrong-password", ""); err == nil {
			t.Fatal("accepted a wrong password")
		}
	}
	for i := 0; i < 15; i++ {
		_, _ = g.Login(httptest.NewRecorder(), req, "ada", "wrong-password", "")
	}
	n := 0
	for _, ev := range st.Snapshot().Audit {
		if ev.Action == "sign-in failed" && ev.Target == "locked" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("locked sign-in was written %d times", n)
	}
}

func TestProxyCannotSpoofConnectingIP(t *testing.T) {
	g, _ := newGateway(t)
	caddy := net.ParseIP("172.17.0.2")
	tunnel := net.ParseIP("172.17.0.3")
	g.trust = func(_ context.Context, ip net.IP) bool { return ip != nil && ip.Equal(caddy) }
	g.tunnel = func(_ context.Context, ip net.IP) bool { return ip != nil && ip.Equal(tunnel) }
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.2:9"
	req.Header.Set("CF-Connecting-IP", "127.0.0.1")
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.50")
	if got := g.clientIP(req); got != "203.0.113.50" {
		t.Fatalf("caddy peer client %s", got)
	}
	req.RemoteAddr = "172.17.0.3:9"
	req.Header.Set("CF-Connecting-IP", "198.51.100.4")
	if got := g.clientIP(req); got != "198.51.100.4" {
		t.Fatalf("tunnel peer client %s", got)
	}
	req.RemoteAddr = "203.0.113.8:9"
	if got := g.clientIP(req); got != "203.0.113.8" {
		t.Fatalf("direct peer client %s", got)
	}
}

func TestCleartextDenialIsAggregated(t *testing.T) {
	g, st := newGateway(t)
	app := AppAccess{ID: "wiki", Host: "wiki.example.com", Level: LevelUsers}
	req := httptest.NewRequest(http.MethodGet, "http://wiki.example.com/", nil)
	req.Host = "wiki.example.com"
	req.RemoteAddr = "203.0.113.77:9"
	for i := 0; i < 12; i++ {
		if _, err := g.Authorize(req, app); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, ev := range st.Snapshot().Audit {
		if ev.Action == "access denied" && ev.TokenName == "Citizen ID" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("cleartext denials written %d times", n)
	}
}
