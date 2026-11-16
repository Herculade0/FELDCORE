package remote

import (
	"context"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestPublishedTunnelUsesTheGateway(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Remote.Routes = []store.PublicRoute{
			{Container: "plex", Hostname: "plex.example.com", Port: 32400, Enabled: true},
			{Container: "files", Hostname: "files.example.com", Port: 80, Enabled: true, Gateway: false},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, GatewayPort: 8791}
	origin := svc.appOrigin(context.Background())
	if origin != "http://127.0.0.1:8791" {
		t.Fatalf("origin %s", origin)
	}
	for _, route := range st.Snapshot().Remote.Routes {
		got := svc.appOrigin(context.Background())
		if strings.Contains(got, route.Container) || strings.Contains(got, "32400") || strings.HasSuffix(got, ":80") {
			t.Fatalf("tunnel for %s dials the container at %s", route.Hostname, got)
		}
	}
}
