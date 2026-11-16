package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/vm"
)

func TestVNCProxyRequiresAdminSession(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("RFB 003.008\n"))
		buf := make([]byte, 16)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _ := c.Read(buf)
		got <- string(buf[:n])
	}()

	mgr := vm.New(vm.Deps{
		Store: st, SharesRoot: dir,
		Runner: vncRunner{addr: ln.Addr().String()},
	})
	srv := New(Deps{Auth: accounts, VMs: mgr})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	anon, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/vms/box/console", nil)
	anon.Header.Set("Origin", ts.URL)
	res, err := http.DefaultClient.Do(anon)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("console without a session: %d", res.StatusCode)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	foreign, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/vms/box/console", nil)
	foreign.Header.Set("Origin", "http://evil.example")
	foreign.AddCookie(cookie)
	res, err = http.DefaultClient.Do(foreign)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/vms/box/console"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {cookie.Name + "=" + cookie.Value},
			"Origin": {ts.URL},
		},
	})
	if err != nil {
		t.Fatalf("signed-in console: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "RFB 003.008\n" {
		t.Fatalf("proxied %q", msg)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "ok" {
			t.Fatalf("guest saw %q", s)
		}
	case <-ctx.Done():
		t.Fatal("guest did not receive the keystroke")
	}
}

type vncRunner struct{ addr string }

func (v vncRunner) Has(string) bool { return true }

func (v vncRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	if name == "virsh" {
		for _, a := range args {
			if a == "vncdisplay" {
				return v.addr, nil
			}
		}
	}
	return "", io.ErrUnexpectedEOF
}
