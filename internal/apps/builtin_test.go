package apps

import (
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/bridge"
	"github.com/Herculade0/feldcore/internal/docker"
)

func TestProtonBridgeTemplate(t *testing.T) {
	svc := New(Deps{
		DataDir:    t.TempDir(),
		SharesRoot: "/srv/feldcore",
		SharePaths: func() map[string]string {
			return map[string]string{"appdata": "/srv/feldcore/appdata"}
		},
	})
	app, spec, notes, err := svc.Spec(bridge.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "Proton Bridge" || app.Purpose != bridge.Purpose || app.Origin != "feldcore" {
		t.Fatalf("app %+v", app)
	}
	if spec.Image != bridge.ImageRef || spec.RestartPolicy != "unless-stopped" || !spec.Autostart || spec.Privileged {
		t.Fatalf("spec defaults %+v", spec)
	}
	if spec.App != bridge.AppID || spec.Purpose != bridge.Purpose {
		t.Fatalf("labels %+v %q", spec.App, spec.Purpose)
	}
	var data, imap, smtp bool
	for _, c := range spec.Configs {
		switch c.Type {
		case docker.TypePath:
			if c.Container == "/root" && c.Host == "/srv/feldcore/appdata/proton-bridge" && c.Mode == "rw" {
				data = true
			}
		case docker.TypePort:
			if c.ContainerPort == 143 && c.HostPort == 1143 && c.HostIP == "" {
				imap = true
			}
			if c.ContainerPort == 25 && c.HostPort == 1025 && c.HostIP == "" {
				smtp = true
			}
		}
	}
	if !data || !imap || !smtp {
		t.Fatalf("configs data %v imap %v smtp %v: %+v", data, imap, smtp, spec.Configs)
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"paid", "Mail Plus", "console", "data folder", "127.0.0.1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes missing %q: %s", want, joined)
		}
	}
}
