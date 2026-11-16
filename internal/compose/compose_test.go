package compose

import (
	"context"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/docker"
)

const risky = `
services:
  web:
    image: nginx:alpine
    privileged: true
    network_mode: host
    pid: host
    cap_add:
      - SYS_ADMIN
      - NET_RAW
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /etc:/host-etc:ro
      - ./data:/config
`

func TestParseReviewAndFix(t *testing.T) {
	p, err := Prepare(risky, "", true, PathInfo{SharesRoot: "/srv/feldcore"}, "/var/lib/feldcore/compose/demo", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid {
		t.Fatal(p.Error)
	}
	if !NeedsAck(p.Findings) {
		t.Fatalf("findings %+v", p.Findings)
	}
	ids := map[string]bool{}
	for _, f := range p.Findings {
		if f.Service != "web" {
			t.Fatalf("service %s", f.Service)
		}
		ids[f.ID] = true
	}
	for _, id := range []string{"privileged", "host-network", "host-pid", "cap:SYS_ADMIN", "docker.sock", "mount:/etc", "cap:NET_RAW"} {
		if !ids[id] {
			t.Errorf("missing %s in %v", id, ids)
		}
	}
	if ids["mount:/"] {
		t.Fatal("relative data mount was treated as the whole disk")
	}

	fixed, err := Prepare(risky, "", true, PathInfo{}, "/tmp/demo", false, []Fix{
		{Service: "web", ID: "privileged"},
		{Service: "web", ID: "host-network"},
		{Service: "web", ID: "docker.sock"},
		{Service: "web", ID: "cap:SYS_ADMIN"},
	})
	if err != nil || !fixed.Changed {
		t.Fatalf("fix %+v %v", fixed, err)
	}
	if strings.Contains(fixed.Compose, "privileged") || strings.Contains(fixed.Compose, "docker.sock") || strings.Contains(fixed.Compose, "network_mode") {
		t.Fatalf("fix left risky keys:\n%s", fixed.Compose)
	}
	if strings.Contains(fixed.Compose, "NET_RAW") == false || strings.Contains(fixed.Compose, "/etc") == false {
		t.Fatalf("unrelated settings dropped:\n%s", fixed.Compose)
	}
	again, err := Prepare(fixed.Compose, "", true, PathInfo{}, "/tmp/demo", false, nil)
	if err != nil || !again.Valid {
		t.Fatal(again.Error, err)
	}
	for _, f := range again.Findings {
		if f.ID == "privileged" || f.ID == "host-network" || f.ID == "docker.sock" || f.ID == "cap:SYS_ADMIN" {
			t.Fatalf("still present: %+v", f)
		}
	}
}

func TestUnraidAndPortainerImport(t *testing.T) {
	in := "version: \"3.8\"\nservices:\n  app:\n    image: linuxserver/plex\n    volumes:\n      - /mnt/user/appdata/plex:/config\n      - /mnt/user/media/tv:/tv:ro\n    labels:\n      net.unraid.docker.managed: composeman\n"
	p, err := Prepare(in, "", true, PathInfo{SharesRoot: "/srv/feldcore"}, "/var/lib/feldcore/compose/plex", true, nil)
	if err != nil || !p.Valid || !p.Changed {
		t.Fatalf("%+v %v", p, err)
	}
	if strings.Contains(p.Compose, "/mnt/user") {
		t.Fatalf("path not rewritten:\n%s", p.Compose)
	}
	if !strings.Contains(p.Compose, "/srv/feldcore/appdata/plex:/config") || !strings.Contains(p.Compose, "/srv/feldcore/media/tv:/tv:ro") {
		t.Fatalf("rewrite:\n%s", p.Compose)
	}
	joined := strings.Join(p.Notes, " ")
	if !strings.Contains(joined, "Unraid") || !strings.Contains(joined, "version") {
		t.Fatalf("notes %v", p.Notes)
	}
}

func TestEnvHidesSocketUntilInterpolated(t *testing.T) {
	in := "services:\n  web:\n    image: nginx\n    volumes:\n      - ${SOCK}:/var/run/docker.sock\n"
	p, err := Prepare(in, "SOCK=/var/run/docker.sock\n", true, PathInfo{}, "/tmp/stack", false, nil)
	if err != nil || !p.Valid {
		t.Fatal(p.Error, err)
	}
	if !NeedsAck(p.Findings) {
		t.Fatal("interpolated socket was not reviewed")
	}
	bad, err := Prepare(in, "", true, PathInfo{}, "/tmp/stack", false, nil)
	if err != nil || !bad.Valid || NeedsAck(bad.Findings) {
		t.Fatalf("empty var should not invent a socket: %+v %v", bad.Findings, err)
	}
	req := "services:\n  web:\n    image: nginx\n    environment:\n      NEED: ${MISSING:?Set MISSING}\n"
	miss, err := Prepare(req, "", true, PathInfo{}, "/tmp/stack", false, nil)
	if err != nil || miss.Valid || !strings.Contains(miss.Error, "Set MISSING") {
		t.Fatalf("required var: %+v %v", miss, err)
	}
}

func TestRejectsIncludeAndBadYAML(t *testing.T) {
	p, err := Prepare("include:\n  - other.yml\nservices:\n  web:\n    image: nginx\n", "", true, PathInfo{}, "", false, nil)
	if err != nil || p.Valid || !strings.Contains(p.Error, "include") {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = Prepare("services: [\n", "", true, PathInfo{}, "", false, nil)
	if err != nil || p.Valid || !strings.Contains(p.Error, "YAML") {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = Prepare("[]\n", "", true, PathInfo{}, "", false, nil)
	if err != nil || p.Valid {
		t.Fatalf("list accepted %+v", p)
	}
}

func TestJSONComposeAndOverride(t *testing.T) {
	in := `{"services":{"web":{"image":"nginx:alpine","ports":["8080:80"]}}}`
	p, err := Prepare(in, "", true, PathInfo{}, "/tmp/web", false, nil)
	if err != nil || !p.Valid || len(p.Services) != 1 || p.Services[0].Name != "web" {
		t.Fatalf("%+v %v", p, err)
	}
	ov, net, err := Override(in, "web", true)
	if err != nil {
		t.Fatal(err)
	}
	if net != "feldcore-stack-web" || !strings.Contains(ov, "external: true") || !strings.Contains(ov, "feldcore.stack: \"web\"") {
		t.Fatalf("override %s\n%s", net, ov)
	}
	own := "services:\n  web:\n    image: nginx\nnetworks:\n  front:\n    driver: bridge\n"
	ov, net, err = Override(own, "web", true)
	if err != nil || net != "" || strings.Contains(ov, "networks:") {
		t.Fatalf("own networks should be left alone: %q\n%s %v", net, ov, err)
	}
	ov, net, err = Override(in, "web", false)
	if err != nil || net != "bridge" {
		t.Fatalf("shared %s %v", net, err)
	}
}

func TestDiff(t *testing.T) {
	if Diff("a\n", "a\n") != "" {
		t.Fatal("identical files produced a diff")
	}
	got := Diff("services:\n  web:\n    image: nginx\n", "services:\n  web:\n    image: nginx:alpine\n")
	if !strings.Contains(got, "-    image: nginx\n") || !strings.Contains(got, "+    image: nginx:alpine\n") {
		t.Fatalf("diff:\n%s", got)
	}
}

func TestConvertContainer(t *testing.T) {
	body, notes, err := FromSpec(docker.Spec{
		Name: "Plex", Image: "lscr.io/linuxserver/plex", RestartPolicy: "unless-stopped",
		Network: "bridge", Isolate: docker.IsolatePrivate, GPU: true,
		Privileged: false,
		Configs: []docker.Config{
			{Type: docker.TypePort, HostPort: 32400, ContainerPort: 32400, Proto: "tcp"},
			{Type: docker.TypePath, Host: "/srv/feldcore/appdata/plex", Container: "/config", Mode: "rw"},
			{Type: docker.TypeVariable, Key: "TZ", Value: "UTC"},
		},
		ExtraParams: "--cap-add=NET_ADMIN --shm-size=1g --gpus all",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"image: lscr.io/linuxserver/plex", "container_name: Plex", "32400:32400/tcp", "/srv/feldcore/appdata/plex:/config:rw", "TZ: UTC", "/dev/dri:/dev/dri:rwm", "NET_ADMIN", "shm_size:"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in\n%s", want, body)
		}
	}
	if !strings.Contains(strings.Join(notes, " "), "gpus") {
		t.Fatalf("notes %v", notes)
	}
	if _, _, err := FromSpec(docker.Spec{Name: "vpn", Image: "x", VPN: "abc"}); err == nil {
		t.Fatal("vpn container converted")
	}
}

func TestStoreDeployAndGate(t *testing.T) {
	dir := t.TempDir()
	var calls []string
	svc := New(Deps{DataDir: dir, Run: func(ctx context.Context, cmd command, line func(string)) (string, error) {
		calls = append(calls, cmd.Name+" "+strings.Join(cmd.Args, " "))
		joined := strings.Join(cmd.Args, " ")
		switch {
		case strings.Contains(joined, "version"):
			return "2.29.7", nil
		case strings.Contains(joined, "network inspect"):
			return "", context.DeadlineExceeded
		case strings.Contains(joined, "network create"):
			if line != nil {
				line("created")
			}
			return "id", nil
		case strings.Contains(joined, "up"):
			if line != nil {
				line("started web")
			}
			return "ok", nil
		case strings.Contains(joined, "ps"):
			return `{"ID":"abc","Name":"feldcore-media-web-1","Service":"web","State":"running","Status":"Up","Image":"nginx:alpine","Publishers":[{"TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}`, nil
		default:
			return "", nil
		}
	}})
	body := "services:\n  web:\n    image: nginx:alpine\n    ports:\n      - 8080:80\n"
	if _, err := svc.Check("media", body, "TZ=UTC\n", true, false, false); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Create(Write{Name: "media", Compose: body, Env: "TZ=UTC\n", Isolate: true, Source: "paste"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Network != "feldcore-stack-media" || st.Project != "feldcore-media" {
		t.Fatalf("network %s project %s", st.Network, st.Project)
	}
	changed := strings.Replace(body, "nginx:alpine", "nginx:1.27", 1)
	if _, err := svc.Check("media", changed, "TZ=UTC\n", true, false, false); err == nil {
		t.Fatal("redeploy without confirm")
	} else {
		g, ok := err.(*Gate)
		if !ok || !g.Confirm || g.Diff == "" {
			t.Fatalf("gate %#v", err)
		}
	}
	riskyBody := "services:\n  web:\n    image: nginx\n    privileged: true\n"
	if _, err := svc.Check("media", riskyBody, "", true, true, false); err == nil {
		t.Fatal("privileged deploy without acknowledgement")
	}
	if _, err := svc.Save(Write{Name: "media", Compose: changed, Env: "TZ=UTC\n", Isolate: true, Source: "paste"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deploy(context.Background(), "media", discard{}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), "media")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "running" || len(got.Services) != 1 || got.Services[0].State != "running" || got.Services[0].ID != "abc" {
		t.Fatalf("live %+v", got.Services)
	}
	cat, err := svc.Catalog(context.Background())
	if err != nil || !cat.ComposeOK || len(cat.Stacks) != 1 {
		t.Fatalf("catalog %+v %v", cat, err)
	}
	if !strings.Contains(cat.Root, "/compose") {
		t.Fatal(cat.Root)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "network create") || !strings.Contains(joined, "feldcore-stack-media") {
		t.Fatalf("network was not created:\n%s", joined)
	}
	if !strings.Contains(joined, "--project-name feldcore-media") || !strings.Contains(joined, "up -d") {
		t.Fatalf("compose up:\n%s", joined)
	}
	if err := svc.Stop(context.Background(), "media", discard{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Update(context.Background(), "media", discard{}); err != nil {
		t.Fatal(err)
	}
	logs, err := svc.Logs(context.Background(), "media", "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	_ = logs
	if err := svc.Remove(context.Background(), "media", false, discard{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), "media"); err == nil {
		t.Fatal("removed stack still reads")
	}
}

func TestParsePSDedupesAddresses(t *testing.T) {
	got := parsePS(`{"ID":"abc","Name":"web-1","Service":"web","State":"running","Publishers":[{"TargetPort":80,"PublishedPort":8088,"Protocol":"tcp"},{"TargetPort":80,"PublishedPort":8088,"Protocol":"tcp"},{"TargetPort":443,"PublishedPort":8443,"Protocol":"tcp"}]}`)
	if len(got) != 1 || len(got[0].Ports) != 2 || got[0].Ports[0] != "8088→80/tcp" || got[0].Ports[1] != "8443→443/tcp" {
		t.Fatalf("%+v", got)
	}
}

func TestGitURLAndName(t *testing.T) {
	if _, err := vetGitURL(context.Background(), "http://github.com/org/repo.git", false); err == nil {
		t.Fatal("http accepted")
	}
	if _, err := vetGitURL(context.Background(), "https://user:pw@github.com/org/repo.git", false); err == nil {
		t.Fatal("credentials accepted")
	}
	if _, err := vetGitURL(context.Background(), "https://127.0.0.1/repo.git", false); err == nil {
		t.Fatal("loopback accepted")
	}
	if _, err := CleanName("../etc"); err == nil {
		t.Fatal("path accepted as a name")
	}
	if _, err := CleanName("jobs"); err == nil {
		t.Fatal("reserved name")
	}
	rel, err := cleanRel("deploy/docker-compose.yml")
	if err != nil || rel != "deploy/docker-compose.yml" {
		t.Fatal(rel, err)
	}
	if _, err := cleanRel("/etc/passwd"); err == nil {
		t.Fatal("absolute path")
	}
}

type discard struct{}

func (discard) Step(string) {}
func (discard) Line(string) {}
