package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestTerminalRequiresAdminSession(t *testing.T) {
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
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// No session: both shells refuse before opening a PTY or a container.
	for _, path := range []string{"/api/v1/terminal", "/api/v1/docker/containers/abc/terminal"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Origin", ts.URL)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session: %d", path, res.StatusCode)
		}
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Host = "127.0.0.1"
	login.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	foreign, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/terminal", nil)
	foreign.Header.Set("Origin", "http://evil.example")
	foreign.AddCookie(cookie)
	res, err := http.DefaultClient.Do(foreign)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/terminal"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {cookie.Name + "=" + cookie.Value},
			"Origin": {ts.URL},
		},
	})
	if err != nil {
		t.Fatalf("signed-in terminal: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}
