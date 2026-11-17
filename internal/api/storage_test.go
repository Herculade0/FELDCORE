package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/storage"
)

type apiExec struct {
	mu    sync.Mutex
	calls []string
	look  map[string]bool
	mount string
	uuid  string
}

func (f *apiExec) Look(name string) bool { return f.look[name] }

func (f *apiExec) Run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.mu.Unlock()
	switch name {
	case "blkid":
		return f.uuid, nil
	case "findmnt":
		return `UUID="` + f.uuid + `" FSTYPE="btrfs" SOURCE="/dev/loop9" TARGET="` + f.mount + `"`, nil
	case "btrfs":
		return "[/dev/loop9].write_io_errs 0\n", nil
	default:
		return "", nil
	}
}

func (f *apiExec) saw(part string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, part) {
			return true
		}
	}
	return false
}

func TestStorageAPIRefusesThenFormatsOnlyTheNamedDisk(t *testing.T) {
	srv, st := newAgentServer(t)
	if rec := call(srv, http.MethodGet, "/api/v1/storage", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: %d %s", rec.Code, rec.Body.String())
	}
	cookie := loginCookie(t, srv)
	if rec := call(srv, http.MethodGet, "/api/v1/storage", "", "", cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("missing storage: %d %s", rec.Code, rec.Body.String())
	}

	dir := t.TempDir()
	mount := filepath.Join(dir, "mnt", "data")
	uuid := "11111111-1111-1111-1111-111111111111"
	run := &apiExec{
		look:  map[string]bool{"mkfs.btrfs": true, "btrfs": true, "fstrim": true},
		mount: mount, uuid: uuid,
	}
	disks, err := storage.Open(storage.Deps{
		DataDir: dir, MountRoot: filepath.Join(dir, "mnt"),
		PermitHostDisks: false, Allow: []string{"/dev/loop9"}, ApplySystem: false,
		Probe: func(context.Context) ([]storage.Disk, error) {
			return []storage.Disk{{
				ID: "serial:SERIAL9", Name: "loop9", Path: "/dev/loop9",
				Model: "TestDisk", Serial: "SERIAL9", Confirm: "SERIAL9", ConfirmKind: "serial",
				SizeBytes: 100 << 30, Type: "hdd", Rotational: true,
				SMART: storage.SMART{Available: true, Healthy: "passed"},
			}}, nil
		},
		Run: run,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.storage = disks
	disks.SetAudit(srv.auditStorage)
	disks.AttachJobs(srv.installs)

	got := call(srv, http.MethodGet, "/api/v1/storage", "", "", cookie)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "SERIAL9") {
		t.Fatalf("inventory: %d %s", got.Code, got.Body.String())
	}
	health := call(srv, http.MethodGet, "/api/v1/storage/disks", "", "", cookie)
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"smartHealthy":"passed"`) {
		t.Fatalf("health: %d %s", health.Code, health.Body.String())
	}

	readTok := mintToken(t, srv, cookie, "reader", []string{auth.ScopeRead})
	if rec := call(srv, http.MethodGet, "/api/v1/storage/disks", "", readTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("read token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodPost, "/api/v1/storage/pools", `{"name":"data","fs":"btrfs","layout":"single","disks":["serial:SERIAL9"],"confirm":["SERIAL9"]}`, readTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read token create: %d %s", rec.Code, rec.Body.String())
	}
	if run.saw("mkfs") {
		t.Fatal("a read token formatted a disk")
	}

	diskTok := mintToken(t, srv, cookie, "disks", []string{auth.ScopeStorage})
	bad := call(srv, http.MethodPost, "/api/v1/storage/pools", `{"name":"data","fs":"btrfs","layout":"single","disks":["serial:SERIAL9"],"confirm":["NOPE"]}`, diskTok, nil)
	if bad.Code != http.StatusBadRequest || run.saw("mkfs") {
		t.Fatalf("wrong serial: %d %s calls=%v", bad.Code, bad.Body.String(), run.calls)
	}
	created := call(srv, http.MethodPost, "/api/v1/storage/pools", `{"name":"data","fs":"btrfs","layout":"single","disks":["serial:SERIAL9"],"confirm":["SERIAL9"]}`, diskTok, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if !run.saw("mkfs.btrfs") || !run.saw("-U "+uuid) || run.saw("/dev/vda") || run.saw("wipefs") {
		t.Fatalf("commands: %v", run.calls)
	}
	var pool struct {
		UUID    string `json:"uuid"`
		Mounted bool   `json:"mounted"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	if pool.UUID != uuid || !pool.Mounted {
		t.Fatalf("pool: %+v", pool)
	}

	found := false
	for _, ev := range st.Snapshot().Audit {
		if ev.Action == "storage.pool.create" && ev.Result == "ok" && ev.Target == "data" && ev.TokenName == "disks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit: %+v", st.Snapshot().Audit)
	}

	if rec := call(srv, http.MethodPost, "/api/v1/storage/pools/data/destroy", `{"confirm":"nope"}`, diskTok, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("destroy: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodPost, "/api/v1/storage/pools/data/destroy", `{"confirm":"data"}`, diskTok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("destroy: %d %s", rec.Code, rec.Body.String())
	}
	if run.saw("wipefs") || run.saw("mkfs.xfs") {
		t.Fatalf("destroy wiped a disk: %v", run.calls)
	}
}

func mintToken(t *testing.T, srv *Server, cookie *http.Cookie, name string, scopes []string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "scopes": scopes, "expiresInDays": 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := call(srv, http.MethodPost, "/api/v1/agent/tokens", string(body), "", cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatal(err, rec.Body.String())
	}
	return out.Token
}
