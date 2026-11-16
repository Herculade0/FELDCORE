package docker

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSplitRef(t *testing.T) {
	cases := map[string][2]string{
		"nginx":                 {"nginx", "latest"},
		"nginx:1.27":            {"nginx", "1.27"},
		"library/redis:7":       {"library/redis", "7"},
		"ghcr.io/user/app:v2":   {"ghcr.io/user/app", "v2"},
		"registry:5000/app":     {"registry:5000/app", "latest"},
		"registry:5000/app:dev": {"registry:5000/app", "dev"},
		"nginx@sha256:abc":      {"nginx", "sha256:abc"},
	}
	for ref, want := range cases {
		repo, tag := splitRef(ref)
		if repo != want[0] || tag != want[1] {
			t.Errorf("splitRef(%q) = (%q,%q), want (%q,%q)", ref, repo, tag, want[0], want[1])
		}
	}
}

func TestBuildCreateValidation(t *testing.T) {
	bad := map[string]CreateRequest{
		"relative host path": {Volumes: []VolumeMapping{{Host: "data", Container: "/data"}}},
		"relative ctr path":  {Volumes: []VolumeMapping{{Host: "/srv/x", Container: "data"}}},
		"colon in path":      {Volumes: []VolumeMapping{{Host: "/a:b", Container: "/data"}}},
		"port out of range":  {Ports: []PortMapping{{Host: 70000, Container: 80}}},
		"bad env name":       {Env: []EnvVar{{Key: "A B", Value: "x"}}},
		"unknown restart":    {RestartPolicy: "sometimes"},
	}
	for name, req := range bad {
		if _, err := buildCreate(req, "nginx"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestBuildCreateProducesExpectedBody(t *testing.T) {
	req := CreateRequest{
		Name:          "web",
		Image:         "nginx:1.27",
		Ports:         []PortMapping{{Host: 8080, Container: 80, Proto: "tcp"}},
		Volumes:       []VolumeMapping{{Host: "/srv/feldcore/web", Container: "/usr/share/nginx/html", ReadOnly: true}},
		Env:           []EnvVar{{Key: "TZ", Value: "UTC"}},
		RestartPolicy: "unless-stopped",
	}
	b, err := buildCreate(req, "nginx:1.27")
	if err != nil {
		t.Fatal(err)
	}

	if b.Image != "nginx:1.27" {
		t.Errorf("image = %q", b.Image)
	}
	if b.Labels[ManagedLabel] != "true" {
		t.Error("managed label not set")
	}
	if got := sortedEnv(b.Env); !reflect.DeepEqual(got, []string{"TZ=UTC"}) {
		t.Errorf("env = %v", got)
	}
	if _, ok := b.ExposedPorts["80/tcp"]; !ok {
		t.Errorf("expected 80/tcp to be exposed, got %v", b.ExposedPorts)
	}
	pb := b.HostConfig.PortBindings["80/tcp"]
	if len(pb) != 1 || pb[0].HostPort != "8080" {
		t.Errorf("port binding = %#v", pb)
	}
	if len(b.HostConfig.Binds) != 1 || b.HostConfig.Binds[0] != "/srv/feldcore/web:/usr/share/nginx/html:ro" {
		t.Errorf("binds = %v", b.HostConfig.Binds)
	}
	if b.HostConfig.RestartPolicy.Name != "unless-stopped" {
		t.Errorf("restart policy = %q", b.HostConfig.RestartPolicy.Name)
	}
}

func TestBuildCreateRestartNoIsEmpty(t *testing.T) {
	// Docker requires the "no" policy to be sent as an empty name.
	b, err := buildCreate(CreateRequest{RestartPolicy: "no"}, "nginx")
	if err != nil {
		t.Fatal(err)
	}
	if b.HostConfig.RestartPolicy.Name != "" {
		t.Errorf("restart 'no' should serialise to an empty name, got %q", b.HostConfig.RestartPolicy.Name)
	}
}

func TestToContainer(t *testing.T) {
	c := toContainer(apiContainer{
		ID:     "abcdef1234567890",
		Names:  []string{"/web"},
		Image:  "nginx:1.27",
		State:  "running",
		Status: "Up 2 minutes",
		Labels: map[string]string{ManagedLabel: "true"},
		Ports: []apiPort{
			{PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
			{PrivatePort: 80, PublicPort: 8080, Type: "tcp"}, // duplicate (IPv4+IPv6)
			{PrivatePort: 9000, PublicPort: 0, Type: "tcp"},  // unpublished, dropped
		},
	})
	if c.ID != "abcdef123456" {
		t.Errorf("id not shortened: %q", c.ID)
	}
	if c.Name != "web" {
		t.Errorf("name = %q", c.Name)
	}
	if !c.Managed {
		t.Error("managed flag not set from label")
	}
	if len(c.Ports) != 1 {
		t.Fatalf("expected 1 published port after de-duping, got %d: %v", len(c.Ports), c.Ports)
	}
	if c.WebURL != "http://HOST:8080" {
		t.Errorf("web url = %q", c.WebURL)
	}
}

func TestToContainerPortsNeverNil(t *testing.T) {
	// A stopped container reports no published ports. The slice must serialise
	// as [] not null, or the UI crashes reading .length. Regression test.
	c := toContainer(apiContainer{ID: "x", Names: []string{"/stopped"}, State: "exited"})
	if c.Ports == nil {
		t.Fatal("Ports must be a non-nil slice so it marshals to [] not null")
	}
	if b, _ := json.Marshal(c.Ports); string(b) != "[]" {
		t.Errorf("Ports marshalled to %s, want []", b)
	}
}

func TestDemux(t *testing.T) {
	// Two stdout frames (stream id 1) with an 8-byte header each.
	frame := func(s string) string {
		n := len(s)
		return string([]byte{1, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}) + s
	}
	in := frame("hello\n") + frame("world\n")
	got, err := demux(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\nworld\n" {
		t.Errorf("demux = %q", got)
	}
}

func TestMemoryUsedSubtractsCache(t *testing.T) {
	var s apiStats
	s.MemoryStats.Usage = 500 << 20
	s.MemoryStats.Stats = map[string]int64{"inactive_file": 200 << 20}
	if got := s.memoryUsed(); got != 300<<20 {
		t.Errorf("memoryUsed = %d, want %d", got, 300<<20)
	}
}
