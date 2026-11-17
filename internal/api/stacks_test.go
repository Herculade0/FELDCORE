package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
)

func newStackServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	wiz := setup.New(setup.Deps{Store: st, DataDir: dir, Root: func() bool { return false }})
	srv := New(Deps{
		Auth:       accounts,
		System:     system.NewCollector(),
		Setup:      wiz,
		DataDir:    dir,
		SharesRoot: filepath.Join(dir, "shares"),
	})
	return srv, dir
}

func TestStacksPreviewCreateAndGate(t *testing.T) {
	bare, _ := newAgentServer(t)
	if rec := call(bare, http.MethodGet, "/api/v1/stacks", "", "", loginCookie(t, bare)); rec.Code != http.StatusNotFound {
		t.Fatalf("stacks without a data dir: %d %s", rec.Code, rec.Body.String())
	}

	srv, dir := newStackServer(t)
	cookie := loginCookie(t, srv)

	bad := call(srv, http.MethodPost, "/api/v1/stacks/preview", `{"name":"media","compose":"services: [","isolate":true}`, "", cookie)
	if bad.Code != http.StatusOK || !strings.Contains(bad.Body.String(), `"valid":false`) {
		t.Fatalf("bad yaml: %d %s", bad.Code, bad.Body.String())
	}
	ok := call(srv, http.MethodPost, "/api/v1/stacks/preview", `{"name":"media","compose":"services:\n  web:\n    image: nginx:alpine\n","env":"TZ=UTC\n","isolate":true}`, "", cookie)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"valid":true`) || !strings.Contains(ok.Body.String(), "feldcore-stack-media") {
		t.Fatalf("preview: %d %s", ok.Code, ok.Body.String())
	}

	body := `{"name":"media","compose":"services:\n  web:\n    image: nginx:alpine\n","env":"TZ=UTC\n","isolate":true,"source":"paste"}`
	created := call(srv, http.MethodPost, "/api/v1/stacks", body, "", cookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	composePath := filepath.Join(dir, "compose", "media", "docker-compose.yml")
	if _, err := os.Stat(composePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose", "README.txt")); err != nil {
		t.Fatal("backup readme missing", err)
	}

	changed := `{"compose":"services:\n  web:\n    image: nginx:1.27\n","env":"TZ=UTC\n","isolate":true}`
	gated := call(srv, http.MethodPost, "/api/v1/stacks/media/deploy", changed, "", cookie)
	if gated.Code != http.StatusConflict || !strings.Contains(gated.Body.String(), `"confirm":true`) || !strings.Contains(gated.Body.String(), "nginx:1.27") {
		t.Fatalf("diff gate: %d %s", gated.Code, gated.Body.String())
	}
	risky := `{"compose":"services:\n  web:\n    image: nginx\n    privileged: true\n","env":"","isolate":true,"confirm":true}`
	ack := call(srv, http.MethodPost, "/api/v1/stacks/media/deploy", risky, "", cookie)
	if ack.Code != http.StatusConflict || !strings.Contains(ack.Body.String(), `"ack":true`) || !strings.Contains(ack.Body.String(), "privileged") {
		t.Fatalf("risk gate: %d %s", ack.Code, ack.Body.String())
	}

	readTok, _, err := srv.auth.CreateToken("reader", []string{auth.ScopeRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(srv, http.MethodGet, "/api/v1/stacks", "", readTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("read list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodPost, "/api/v1/stacks/preview", `{"compose":"services:\n  web:\n    image: nginx\n"}`, readTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read token previewed: %d", rec.Code)
	}
	stackTok, _, err := srv.auth.CreateToken("stacks", []string{auth.ScopeStacks}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(srv, http.MethodPost, "/api/v1/stacks/preview", `{"compose":"services:\n  web:\n    image: nginx\n"}`, stackTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("stacks preview: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodGet, "/api/v1/stacks", "", stackTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("stacks token listed without read: %d", rec.Code)
	}

	audit := call(srv, http.MethodGet, "/api/v1/audit", "", "", cookie)
	if audit.Code != http.StatusOK {
		t.Fatal(audit.Body.String())
	}
	text := audit.Body.String()
	if !strings.Contains(text, "stack.create") || !strings.Contains(text, `"result":"denied"`) {
		t.Fatalf("audit missing stack events: %s", text)
	}
	var view struct {
		Events []struct {
			Action string `json:"action"`
			Result string `json:"result"`
		} `json:"events"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var sawCreate, sawDeny bool
	for _, e := range view.Events {
		if e.Action == "stack.create" && e.Result == "ok" {
			sawCreate = true
		}
		if e.Action == "stack.deploy" && e.Result == "denied" {
			sawDeny = true
		}
	}
	if !sawCreate || !sawDeny {
		t.Fatalf("events %+v", view.Events)
	}
}
