package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/osupdate"
)

func TestOSReadTokenIsAdmin(t *testing.T) {
	srv, _ := newAgentServer(t)
	created := call(srv, http.MethodPost, "/api/v1/agent/tokens", `{"name":"reader","scopes":["read"]}`, "", loginCookie(t, srv))
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/os", "/api/v1/os/check"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/check") {
			method = http.MethodPost
		}
		if rec := call(srv, method, path, "", minted.Token, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
	}
}

func TestOSUpdateJobAudits(t *testing.T) {
	srv, st := newAgentServer(t)
	root := t.TempDir()
	data := t.TempDir()
	esp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "feldcore-os-release"), []byte("ID=feldcore\nVERSION_ID=0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc", "cmdline"), []byte("feldcore.slot=a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	slotA := filepath.Join(root, "a")
	slotB := filepath.Join(root, "b")
	for _, p := range []string{slotA, slotB} {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(1 << 20); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	key, err := osupdate.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "update.pub"), []byte(osupdate.MarshalPublic(key.Public())), 0o644); err != nil {
		t.Fatal(err)
	}
	bodies := map[string][]byte{
		"feldcore-root.img": []byte("root-bytes"),
		"vmlinuz":           []byte("kernel-bytes"),
		"initrd.img":        []byte("initrd-bytes"),
	}
	m := osupdate.Manifest{Release: osupdate.Release{Version: "0.2.0", Notes: "Second image.", Arch: "amd64"}}
	for _, item := range []struct{ name, kind string }{
		{"feldcore-root.img", "root"},
		{"vmlinuz", "kernel"},
		{"initrd.img", "initrd"},
	} {
		sum := sha256.Sum256(bodies[item.name])
		m.Files = append(m.Files, osupdate.Artifact{
			Name: item.name, Kind: item.kind, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(bodies[item.name])),
		})
	}
	raw, err := osupdate.MarshalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := osupdate.Sign(key, "feldcore-os.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	bodies["feldcore-os.json"] = raw
	bodies["feldcore-os.json.minisig"] = sig
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := bodies[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer httpSrv.Close()

	srv.os = &osupdate.Service{
		Root: root, ESP: esp, DataDir: data, Store: st,
		SlotA: slotA, SlotB: slotB,
		ManifestURL: httpSrv.URL + "/feldcore-os.json",
		AllowLocal:  true,
	}
	srv.registerOSJobs()

	cookie := loginCookie(t, srv)
	status := call(srv, http.MethodGet, "/api/v1/os", "", "", cookie)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"appliance":true`) {
		t.Fatalf("status %d %s", status.Code, status.Body.String())
	}
	started := call(srv, http.MethodPost, "/api/v1/os/update", "", "", cookie)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start %d %s", started.Code, started.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		rec := call(srv, http.MethodGet, "/api/v1/installs/"+job.ID, "", "", cookie)
		last = rec.Body.String()
		if strings.Contains(last, `"state":"succeeded"`) {
			break
		}
		if strings.Contains(last, `"state":"failed"`) {
			t.Fatalf("job failed: %s", last)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(last, `"state":"succeeded"`) {
		t.Fatalf("job did not finish: %s", last)
	}
	foundStage, foundStaged := false, false
	for _, ev := range srv.auth.Audit() {
		if ev.Action == "os.update.stage" && ev.Result == "ok" && ev.TokenName == "admin" {
			foundStage = true
		}
		if ev.Action == "os.update.staged" && ev.Target == "0.2.0" && ev.Result == "ok" {
			foundStaged = true
		}
	}
	if !foundStage || !foundStaged {
		t.Fatalf("audit %+v", srv.auth.Audit())
	}
}
