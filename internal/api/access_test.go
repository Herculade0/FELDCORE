package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/snapshots"
	"github.com/Herculade0/feldcore/internal/store"
)

type readyEngine struct{ applied int }

func (readyEngine) Ready(context.Context) bool { return true }
func (e *readyEngine) ApplySystem(context.Context, docker.SystemSpec) error {
	e.applied++
	return nil
}
func (readyEngine) StopSystem(context.Context, string) error             { return nil }
func (readyEngine) SystemState(context.Context, string) (string, error)  { return "running", nil }
func (readyEngine) EnsureNetwork(context.Context, string) error          { return nil }
func (readyEngine) ConnectNetwork(context.Context, string, string) error { return nil }
func (readyEngine) NetworkMode(context.Context, string) (string, error)  { return "bridge", nil }

type refuseSnap struct{}

func (refuseSnap) SnapshotSource(context.Context, string) (docker.SnapSource, error) {
	return docker.SnapSource{}, &snapshots.OpError{Msg: "snapshot failed on purpose"}
}
func (refuseSnap) Stop(context.Context, string) error                     { return nil }
func (refuseSnap) Start(context.Context, string) error                    { return nil }
func (refuseSnap) TagImage(context.Context, string, string) error         { return nil }
func (refuseSnap) RemoveImageRef(context.Context, string) error           { return nil }
func (refuseSnap) ApplySpec(context.Context, docker.Spec) (string, error) { return "", nil }

func TestRemoteAndSnapshotAPI(t *testing.T) {
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
	var logs bytes.Buffer
	log := slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&logs, nil), box))
	eng := &readyEngine{}
	remoteSvc := &remote.Service{
		Store: st, Secrets: box, Engine: eng, DataDir: dir, ListenPort: 8787, Log: log,
		PreferHost: func() bool { return false },
	}
	snaps := snapshots.Open(st, refuseSnap{}, dir, nil)
	srv := New(Deps{Auth: accounts, Remote: remoteSvc, Snaps: snaps, Log: log})

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Host = "127.0.0.1"
	login.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		r.Host = "127.0.0.1"
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		return w
	}

	open := httptest.NewRequest(http.MethodGet, "/api/v1/remote", nil)
	if rec := httptest.NewRecorder(); func() int {
		srv.Router().ServeHTTP(rec, open)
		return rec.Code
	}() != http.StatusUnauthorized {
		t.Fatal("remote access must require a session")
	}

	got := call(http.MethodGet, "/api/v1/remote", "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"mode":"off"`) || !strings.Contains(got.Body.String(), `"exposeAdmin":false`) {
		t.Fatalf("default remote: %d %s", got.Code, got.Body.String())
	}

	const token = "super-secret-tunnel-token"
	saved := call(http.MethodPut, "/api/v1/remote", `{"mode":"token","tunnelToken":"`+token+`"}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save %d %s", saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), token) || bytes.Contains(logs.Bytes(), []byte(token)) {
		t.Fatalf("token leaked body=%s log=%s", saved.Body.String(), logs.Bytes())
	}
	if eng.applied != 1 {
		t.Fatalf("connector starts %d", eng.applied)
	}
	if !strings.Contains(saved.Body.String(), `"tunnelTokenSet":true`) {
		t.Fatalf("flag missing %s", saved.Body.String())
	}

	usage := call(http.MethodGet, "/api/v1/snapshots", "")
	if usage.Code != http.StatusOK || !strings.Contains(usage.Body.String(), `"keepN":5`) {
		t.Fatalf("usage %d %s", usage.Code, usage.Body.String())
	}
	kept := call(http.MethodPut, "/api/v1/snapshots", `{"keepN":3,"keepDays":14,"dir":""}`)
	if kept.Code != http.StatusOK || !strings.Contains(kept.Body.String(), `"keepN":3`) {
		t.Fatalf("retention %d %s", kept.Code, kept.Body.String())
	}

	// An edit must not proceed when the automatic snapshot fails.
	upd := call(http.MethodPut, "/api/v1/docker/containers/abc", `{"name":"abc","image":"nginx:latest"}`)
	if upd.Code != http.StatusInternalServerError || !strings.Contains(upd.Body.String(), "snapshot failed on purpose") {
		t.Fatalf("update %d %s", upd.Code, upd.Body.String())
	}
}

type hostBin struct {
	err error
	up  bool
}

func (b *hostBin) Available() bool { return true }
func (b *hostBin) Running() bool   { return b.up }
func (b *hostBin) Start(context.Context, string) error {
	if b.err != nil {
		return b.err
	}
	b.up = true
	return nil
}
func (b *hostBin) Stop() error                   { b.up = false; return nil }
func (b *hostBin) Install(context.Context) error { return nil }
func (b *hostBin) Detail() string {
	if b.err != nil && !b.up {
		return b.err.Error()
	}
	return ""
}

func TestRemoteOSSaveSkipsContainer(t *testing.T) {
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
	eng := &readyEngine{}
	bin := &hostBin{}
	remoteSvc := &remote.Service{
		Store: st, Secrets: box, Engine: eng, Binary: bin, DataDir: dir, ListenPort: 8787,
		PreferHost: func() bool { return true },
	}
	srv := New(Deps{Auth: accounts, Remote: remoteSvc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Host = "127.0.0.1"
	login.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]
	const token = "super-secret-tunnel-token"
	put := httptest.NewRequest(http.MethodPut, "/api/v1/remote", strings.NewReader(`{"mode":"token","tunnelToken":"`+token+`"}`))
	put.Host = "127.0.0.1"
	put.Header.Set("Content-Type", "application/json")
	put.AddCookie(cookie)
	saved := httptest.NewRecorder()
	srv.Router().ServeHTTP(saved, put)
	if saved.Code != http.StatusOK {
		t.Fatalf("save %d %s", saved.Code, saved.Body.String())
	}
	body := saved.Body.String()
	if eng.applied != 0 || !bin.up || strings.Contains(body, token) || !strings.Contains(body, `"connector":"binary"`) || !strings.Contains(body, `"runsOnHost":true`) {
		t.Fatalf("applied %d up %v body %s", eng.applied, bin.up, body)
	}

	bin.up = false
	bin.err = errors.New(`feldcore-cloudflared was created but did not start: exec: "sh": executable file not found in $PATH`)
	put = httptest.NewRequest(http.MethodPut, "/api/v1/remote", strings.NewReader(`{"mode":"token","tunnelToken":"`+token+`-next"}`))
	put.Host = "127.0.0.1"
	put.Header.Set("Content-Type", "application/json")
	put.AddCookie(cookie)
	failed := httptest.NewRecorder()
	srv.Router().ServeHTTP(failed, put)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("status %d %s", failed.Code, failed.Body.String())
	}
	msg := failed.Body.String()
	if strings.Contains(msg, "exec:") || strings.Contains(msg, "sh") || strings.Contains(msg, "PATH") || !strings.Contains(msg, "could not start") {
		t.Fatalf("error body %s", msg)
	}
	got := httptest.NewRequest(http.MethodGet, "/api/v1/remote", nil)
	got.Host = "127.0.0.1"
	got.AddCookie(cookie)
	view := httptest.NewRecorder()
	srv.Router().ServeHTTP(view, got)
	if view.Code != http.StatusOK || strings.Contains(view.Body.String(), "exec:") || strings.Contains(view.Body.String(), `"sh"`) {
		t.Fatalf("view %d %s", view.Code, view.Body.String())
	}
	if !strings.Contains(view.Body.String(), `"state":"down"`) {
		t.Fatalf("view %s", view.Body.String())
	}
}
