package api

import (
	"bytes"
	"context"
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
	remoteSvc := &remote.Service{Store: st, Secrets: box, Engine: eng, DataDir: dir, ListenPort: 8787, Log: log}
	snaps := snapshots.Open(st, refuseSnap{}, dir, nil)
	srv := New(Deps{Auth: accounts, Remote: remoteSvc, Snaps: snaps, Log: log})

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
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
