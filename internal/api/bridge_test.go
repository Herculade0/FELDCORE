package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
	"github.com/Herculade0/feldcore/internal/vm"
)

func TestBridgeCredentialsStayOutOfState(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{
		Auth:    accounts,
		System:  system.NewCollector(),
		Setup:   setup.New(setup.Deps{Store: st, DataDir: dir, Root: func() bool { return false }}),
		Docker:  docker.NewManager(filepath.Join(dir, "missing.sock"), st, nil),
		Secrets: box,
	})
	srv.vmProbe = vm.Probe{Look: func(string) (string, bool) { return "", false }}
	cookie := loginCookie(t, srv)

	denied := call(srv, http.MethodGet, "/api/v1/integrations/proton-bridge", "", bearerOf(t, srv, "read-bot", []string{auth.ScopeRead}), nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read token: %d %s", denied.Code, denied.Body.String())
	}

	saved := call(srv, http.MethodPut, "/api/v1/integrations/proton-bridge",
		`{"username":"bridge-user","password":"bridge-secret-value"}`, "", cookie)
	if saved.Code != http.StatusOK {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	var stBody struct {
		Purpose    string `json:"purpose"`
		Configured bool   `json:"configured"`
		NeedsLogin bool   `json:"needsLogin"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		Plan       string `json:"plan"`
		Login      struct {
			Commands []string `json:"commands"`
			After    []string `json:"after"`
		} `json:"login"`
		Reauth []string `json:"reauth"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &stBody); err != nil {
		t.Fatal(err)
	}
	if stBody.Purpose != "AI integration" || !stBody.Configured || stBody.NeedsLogin || stBody.Username != "bridge-user" || stBody.Password != "bridge-secret-value" {
		t.Fatalf("saved status %+v", stBody)
	}
	if !strings.Contains(stBody.Plan, "paid") || len(stBody.Login.Commands) != 3 || len(stBody.Reauth) == 0 {
		t.Fatalf("guide %+v", stBody)
	}
	if len(stBody.Login.After) == 0 || stBody.Login.After[0] != "login" || stBody.Login.Commands[2] != "/protonmail/entrypoint.sh init" {
		t.Fatalf("commands %+v", stBody.Login)
	}

	again := call(srv, http.MethodGet, "/api/v1/integrations/proton-bridge", "", bearerOf(t, srv, "mail-bot", []string{auth.ScopeMail}), nil)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "bridge-secret-value") {
		t.Fatalf("mail token get: %d %s", again.Code, again.Body.String())
	}

	state, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "bridge-secret-value") || strings.Contains(string(state), "bridge-user") {
		t.Fatal("credential landed in state.json")
	}

	bad := call(srv, http.MethodPut, "/api/v1/integrations/proton-bridge", `{"username":"has space","password":"bridge-secret-value"}`, "", cookie)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad username: %d %s", bad.Code, bad.Body.String())
	}

	unread := call(srv, http.MethodPost, "/api/v1/integrations/proton-bridge/read", `{}`, "", cookie)
	if unread.Code != http.StatusInternalServerError || strings.Contains(unread.Body.String(), "bridge-secret-value") {
		t.Fatalf("read without docker: %d %s", unread.Code, unread.Body.String())
	}
}

func bearerOf(t *testing.T, srv *Server, name string, scopes []string) string {
	t.Helper()
	secret, _, err := srv.auth.CreateToken(name, scopes, 0)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}
