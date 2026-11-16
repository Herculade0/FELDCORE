package cfapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTunnelAndDNS(t *testing.T) {
	const token = "super-secret-api-token-value"
	var created bool
	var ingress string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/accounts" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"acc","name":"Home"}]}`))
		case r.URL.Path == "/accounts/acc/cfd_tunnel" && r.Method == http.MethodPost:
			created = true
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"tun","name":"feldcore","token":"` + token + `-connector"}}`))
		case strings.HasSuffix(r.URL.Path, "/configurations") && r.Method == http.MethodPut:
			raw, _ := io.ReadAll(r.Body)
			ingress = string(raw)
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		case r.URL.Path == "/zones" && r.URL.Query().Get("name") == "example.com":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone","name":"example.com"}]}`))
		case r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case strings.Contains(r.URL.Path, "/dns_records") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec","type":"CNAME","name":"app.example.com","content":"tun.cfargotunnel.com"}}`))
		case strings.HasSuffix(r.URL.Path, "/cfd_tunnel/tun") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"tun","status":"healthy","connections":[{"id":"c1"}]}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"no"}]}`))
		}
	}))
	defer srv.Close()

	c := &Client{Base: srv.URL, Token: token}
	accs, err := c.Accounts(context.Background())
	if err != nil || len(accs) != 1 || accs[0].ID != "acc" {
		t.Fatalf("accounts: %+v %v", accs, err)
	}
	tun, err := c.CreateTunnel(context.Background(), "acc", "feldcore")
	if err != nil || !created || tun.ID != "tun" || tun.Token == "" {
		t.Fatalf("create: %+v %v", tun, err)
	}
	if err := c.PutIngress(context.Background(), "acc", "tun", []Rule{
		{Hostname: "app.example.com", Service: "http://plex:32400"},
		{Service: "http_status:404"},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ingress, "app.example.com") || !strings.Contains(ingress, "http_status:404") {
		t.Fatalf("ingress: %s", ingress)
	}
	if strings.Contains(ingress, token) {
		t.Fatal("token leaked into ingress body")
	}
	id, err := c.UpsertCNAME(context.Background(), "app.example.com", "tun.cfargotunnel.com")
	if err != nil || id != "rec" {
		t.Fatalf("dns %s %v", id, err)
	}
	got, err := c.GetTunnel(context.Background(), "acc", "tun")
	if err != nil || len(got.Connections) != 1 {
		t.Fatalf("health %+v %v", got, err)
	}
}

func TestErrorRedactsToken(t *testing.T) {
	const token = "super-secret-api-token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token ` + token + `"}]}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: token}
	_, err := c.Accounts(context.Background())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked or missing: %v", err)
	}
}
