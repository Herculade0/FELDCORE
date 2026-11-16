package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
	"github.com/Herculade0/feldcore/internal/vm"
)

func TestScopeFor(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{http.MethodGet, "/api/v1/system/info", auth.ScopeRead},
		{http.MethodGet, "/api/v1/docker/containers/abc/logs", auth.ScopeRead},
		{http.MethodGet, "/api/v1/apps/plex", auth.ScopeRead},
		{http.MethodPost, "/api/v1/docker/containers/abc/start", auth.ScopeContainers},
		{http.MethodDelete, "/api/v1/docker/containers/abc", auth.ScopeContainers},
		{http.MethodPost, "/api/v1/docker/containers", auth.ScopeContainers},
		{http.MethodPost, "/api/v1/docker/install", auth.ScopeAdmin},
		{http.MethodGet, "/api/v1/terminal", "shell"},
		{http.MethodGet, "/api/v1/docker/containers/abc/terminal", "shell"},
		{http.MethodGet, "/api/v1/vms/box/console", "shell"},
		{http.MethodGet, "/api/v1/settings", auth.ScopeAdmin},
		{http.MethodGet, "/api/v1/vpn", auth.ScopeAdmin},
		{http.MethodPut, "/api/v1/backups", auth.ScopeAdmin},
		{http.MethodPut, "/api/v1/notifications", auth.ScopeAdmin},
		{http.MethodGet, "/api/v1/audit", auth.ScopeAdmin},
		{http.MethodGet, "/api/v1/vms", auth.ScopeRead},
		{http.MethodPost, "/api/v1/vms/abc/start", auth.ScopeVMs},
		{http.MethodPut, "/api/v1/settings/firewall", auth.ScopeAdmin},
		{http.MethodGet, "/api/v1/remote", auth.ScopeRead},
		{http.MethodGet, "/api/v1/proxy/ca", auth.ScopeRead},
		{http.MethodGet, "/api/v1/docker/containers/abc/snapshots", auth.ScopeRead},
		{http.MethodPost, "/api/v1/docker/containers/abc/snapshots", auth.ScopeContainers},
		{http.MethodPost, "/api/v1/docker/containers/abc/snapshots/s1/restore", auth.ScopeContainers},
		{http.MethodPut, "/api/v1/remote", auth.ScopeAdmin},
		{http.MethodPut, "/api/v1/docker/containers/abc/address", auth.ScopeAdmin},
		{http.MethodGet, "/api/v1/shares/media/files", auth.ScopeFilesRead},
		{http.MethodGet, "/api/v1/shares/media/files/content", auth.ScopeFilesRead},
		{http.MethodPost, "/api/v1/shares/media/files/zip", auth.ScopeFilesRead},
		{http.MethodPost, "/api/v1/shares/media/files/mkdir", auth.ScopeFilesWrite},
		{http.MethodPatch, "/api/v1/shares/media/uploads/abc", auth.ScopeFilesWrite},
		{http.MethodPost, "/api/v1/shares/media/fetch", auth.ScopeFilesWrite},
		{http.MethodGet, "/api/v1/settings/files", auth.ScopeAdmin},
	}
	for _, c := range cases {
		if got := scopeFor(c.method, c.path); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.method, c.path, got, c.want)
		}
	}
	if auth.Allows([]string{auth.ScopeAdmin}, "shell") {
		t.Fatal("admin must not grant a shell")
	}
}

func newAgentServer(t *testing.T) (*Server, *store.Store) {
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
		Auth:   accounts,
		System: system.NewCollector(),
		Setup:  wiz,
		Docker: docker.NewManager(filepath.Join(dir, "missing.sock"), st, nil),
	})
	srv.vmProbe = vm.Probe{Look: func(string) (string, bool) { return "", false }}
	return srv, st
}

func loginCookie(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie")
	}
	return cookies[0]
}

func call(srv *Server, method, path, body, bearer string, cookie *http.Cookie) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	return w
}

func TestAgentTokenScopesAndAudit(t *testing.T) {
	srv, st := newAgentServer(t)
	cookie := loginCookie(t, srv)

	if rec := call(srv, http.MethodGet, "/api/v1/system/info", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: %d", rec.Code)
	}
	if rec := call(srv, http.MethodGet, "/api/v1/system/info", "", "fc_not-a-real-token", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d %s", rec.Code, rec.Body.String())
	}

	created := call(srv, http.MethodPost, "/api/v1/agent/tokens", `{"name":"Claude Code","scopes":["read"],"expiresInDays":30}`, "", cookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var minted struct {
		Token  string   `json:"token"`
		ID     string   `json:"id"`
		Prefix string   `json:"prefix"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(minted.Token, "fc_") || minted.ID == "" {
		t.Fatalf("minted %+v", minted)
	}
	raw, _ := json.Marshal(st.Snapshot())
	if strings.Contains(string(raw), minted.Token) {
		t.Fatal("plaintext token was stored")
	}

	listed := call(srv, http.MethodGet, "/api/v1/agent/tokens", "", "", cookie)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), minted.Token) {
		t.Fatalf("list leaked or failed: %d %s", listed.Code, listed.Body.String())
	}

	info := call(srv, http.MethodGet, "/api/v1/system/info", "", minted.Token, nil)
	if info.Code != http.StatusOK || !strings.Contains(info.Body.String(), "hostname") {
		t.Fatalf("read: %d %s", info.Code, info.Body.String())
	}
	start := call(srv, http.MethodPost, "/api/v1/docker/containers/abc/start", "", minted.Token, nil)
	if start.Code != http.StatusForbidden {
		t.Fatalf("read token started a container: %d %s", start.Code, start.Body.String())
	}
	shell := call(srv, http.MethodGet, "/api/v1/terminal", "", minted.Token, nil)
	if shell.Code != http.StatusForbidden || !strings.Contains(shell.Body.String(), "shell") {
		t.Fatalf("shell: %d %s", shell.Code, shell.Body.String())
	}
	if code := call(srv, http.MethodGet, "/api/v1/settings", "", minted.Token, nil).Code; code != http.StatusForbidden {
		t.Fatalf("read token saw settings: %d", code)
	}

	adminSecret, _, err := srv.auth.CreateToken("owner", []string{auth.ScopeAdmin}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(srv, http.MethodGet, "/api/v1/terminal", "", adminSecret, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("admin token shell: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodGet, "/api/v1/settings", "", adminSecret, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin settings: %d %s", rec.Code, rec.Body.String())
	}

	boxSecret, _, err := srv.auth.CreateToken("boxes", []string{auth.ScopeContainers}, 0)
	if err != nil {
		t.Fatal(err)
	}
	box := call(srv, http.MethodPost, "/api/v1/docker/containers/abc/start", "", boxSecret, nil)
	if box.Code == http.StatusForbidden || box.Code == http.StatusUnauthorized {
		t.Fatalf("containers scope rejected: %d %s", box.Code, box.Body.String())
	}
	if code := call(srv, http.MethodGet, "/api/v1/system/info", "", boxSecret, nil).Code; code != http.StatusForbidden {
		t.Fatalf("containers token read the system: %d", code)
	}

	// A signed-in browser is still the administrator.
	if rec := call(srv, http.MethodGet, "/api/v1/settings", "", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("session settings: %d", rec.Code)
	}

	audit := call(srv, http.MethodGet, "/api/v1/audit", "", "", cookie)
	if audit.Code != http.StatusOK {
		t.Fatalf("audit: %d %s", audit.Code, audit.Body.String())
	}
	body := audit.Body.String()
	if !strings.Contains(body, "Claude Code") || !strings.Contains(body, "GET /system/info") || !strings.Contains(body, `"result":"ok"`) {
		t.Fatalf("audit missing the read: %s", body)
	}
	if !strings.Contains(body, `"result":"denied"`) || !strings.Contains(body, "/terminal") {
		t.Fatalf("audit missing the denial: %s", body)
	}
	var view struct {
		Events []struct {
			TokenName string `json:"tokenName"`
			Action    string `json:"action"`
			Result    string `json:"result"`
		} `json:"events"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Events) == 0 || view.Events[0].TokenName == "" {
		t.Fatalf("events %+v", view.Events)
	}

	tokens := srv.auth.Tokens()
	var sawUsed bool
	for _, tok := range tokens {
		if tok.Name == "Claude Code" && tok.LastUsedAt != nil {
			sawUsed = true
		}
	}
	if !sawUsed {
		t.Fatal("last used was not recorded")
	}

	if rec := call(srv, http.MethodDelete, "/api/v1/agent/tokens/"+minted.ID, "", minted.Token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read token revoked itself: %d", rec.Code)
	}
	if rec := call(srv, http.MethodDelete, "/api/v1/agent/tokens/"+minted.ID, "", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodGet, "/api/v1/system/info", "", minted.Token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", rec.Code)
	}
	if rec := call(srv, http.MethodPost, "/api/v1/agent/tokens/revoke-all", "", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("revoke all: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodGet, "/api/v1/settings", "", adminSecret, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin token survived revoke all: %d", rec.Code)
	}
}

type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

func TestMCPAuthAndReadOnlyTools(t *testing.T) {
	srv, _ := newAgentServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	post := func(token string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	noAuth := post("")
	noAuth.Body.Close()
	if noAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mcp without a token: %d", noAuth.StatusCode)
	}
	bad := post("fc_nope")
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mcp bad token: %d", bad.StatusCode)
	}

	cookie := loginCookie(t, srv)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session cookie on mcp: %d", res.StatusCode)
	}

	boxes, _, err := srv.auth.CreateToken("boxes", []string{auth.ScopeContainers}, 0)
	if err != nil {
		t.Fatal(err)
	}
	denied := post(boxes)
	deniedBody, _ := io.ReadAll(denied.Body)
	denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("mcp without read: %d %s", denied.StatusCode, deniedBody)
	}

	secret, _, err := srv.auth.CreateToken("Claude Code", []string{auth.ScopeRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{
			base:  http.DefaultTransport,
			token: secret,
		}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"system_info": true, "system_stats": true, "list_shares": true,
		"list_containers": true, "container_logs": true, "list_apps": true, "list_vms": true,
	}
	for _, tool := range tools.Tools {
		lower := strings.ToLower(tool.Name)
		for _, banned := range []string{"shell", "exec", "terminal", "bash"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("phase 1 exposed %s", tool.Name)
			}
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s is not marked read-only", tool.Name)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools %v", want)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "system_info"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("system_info error: %+v", result)
	}
	blob, _ := json.Marshal(result)
	if !strings.Contains(string(blob), "hostname") {
		t.Fatalf("system_info result %s", blob)
	}
	vms, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_vms"})
	if err != nil {
		t.Fatal(err)
	}
	if vms.IsError {
		t.Fatalf("list_vms error: %+v", vms)
	}
	vblob, _ := json.Marshal(vms)
	if !strings.Contains(string(vblob), `"available":false`) && !strings.Contains(string(vblob), `"available": false`) {
		t.Fatalf("list_vms %s", vblob)
	}

	found := false
	for _, ev := range srv.auth.Audit() {
		if ev.TokenName == "Claude Code" && strings.Contains(ev.Action, "system_info") && ev.Result == "ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit %s", mustJSON(srv.auth.Audit()))
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
