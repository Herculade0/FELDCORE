package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
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

func spec(configs ...Config) Spec {
	return Spec{Name: "web", Image: "nginx:1.27", Configs: configs}
}

func TestNormaliseSpecRejectsBadInput(t *testing.T) {
	bad := map[string]Spec{
		"empty image":        {Image: ""},
		"bad name":           {Name: "has space", Image: "nginx"},
		"unknown restart":    {Image: "nginx", RestartPolicy: "sometimes"},
		"unknown type":       spec(Config{Type: "socket"}),
		"relative host path": spec(Config{Type: TypePath, Host: "data/x", Container: "/data"}),
		"relative ctr path":  spec(Config{Type: TypePath, Host: "/srv/x", Container: "data"}),
		"colon in path":      spec(Config{Type: TypePath, Host: "/a:b", Container: "/data"}),
		"bind option inject": spec(Config{Type: TypePath, Host: "/a", Container: "/b", Mode: "rw:/etc:/host"}),
		"missing host path":  spec(Config{Type: TypePath, Container: "/data"}),
		"port out of range":  spec(Config{Type: TypePort, HostPort: 70000, ContainerPort: 80}),
		"port zero":          spec(Config{Type: TypePort, HostPort: 80, ContainerPort: 0}),
		"bad proto":          spec(Config{Type: TypePort, HostPort: 80, ContainerPort: 80, Proto: "icmp"}),
		"env name with =":    spec(Config{Type: TypeVariable, Key: "A=B", Value: "x"}),
		"env name space":     spec(Config{Type: TypeVariable, Key: "A B", Value: "x"}),
		"reserved label":     spec(Config{Type: TypeLabel, Key: "feldcore.managed", Value: "false"}),
		"device not in /dev": spec(Config{Type: TypeDevice, Host: "/etc/shadow"}),
		"duplicate host port": spec(
			Config{Type: TypePort, HostPort: 8080, ContainerPort: 80},
			Config{Type: TypePort, HostPort: 8080, ContainerPort: 81},
		),
		"duplicate container path": spec(
			Config{Type: TypePath, Host: "/a", Container: "/data"},
			Config{Type: TypePath, Host: "/b", Container: "/data"},
		),
		"duplicate variable": spec(
			Config{Type: TypeVariable, Key: "TZ", Value: "UTC"},
			Config{Type: TypeVariable, Key: "TZ", Value: "AEST"},
		),
	}
	for name, s := range bad {
		var ie *InputError
		if err := normaliseSpec(&s); err == nil {
			t.Errorf("%s: expected a validation error", name)
		} else if !asInput(err, &ie) {
			t.Errorf("%s: expected an InputError, got %T", name, err)
		}
	}
}

func asInput(err error, target **InputError) bool {
	ie, ok := err.(*InputError)
	*target = ie
	return ok
}

func TestNormaliseSpecTidies(t *testing.T) {
	s := spec(
		Config{Type: TypePath, Name: "  Media\nlibrary ", Host: "/srv/feldcore/media/", Container: "/media/"},
		Config{Type: TypePort, HostPort: 8080, ContainerPort: 80, Proto: "TCP"},
		Config{Type: TypeDevice, Host: "/dev/ttyUSB0"},
		Config{Type: TypePath, Host: "appdata", Container: "/config"}, // named volume
	)
	if err := normaliseSpec(&s); err != nil {
		t.Fatal(err)
	}
	p := s.Configs[0]
	if p.Name != "Media library" || p.Host != "/srv/feldcore/media" || p.Container != "/media" || p.Mode != "rw" {
		t.Errorf("path not tidied: %+v", p)
	}
	if s.Configs[1].Proto != "tcp" {
		t.Errorf("proto not lowercased: %q", s.Configs[1].Proto)
	}
	if d := s.Configs[2]; d.Container != "/dev/ttyUSB0" || d.Mode != "rwm" {
		t.Errorf("device defaults not applied: %+v", d)
	}
	if s.Network != "bridge" || s.RestartPolicy != "no" {
		t.Errorf("defaults not applied: network=%q restart=%q", s.Network, s.RestartPolicy)
	}
}

func fullSpec() Spec {
	return Spec{
		Name:          "web",
		Image:         "nginx:1.27",
		Network:       "bridge",
		RestartPolicy: "unless-stopped",
		Configs: []Config{
			{Type: TypePath, Name: "Site", Host: "/srv/feldcore/web", Container: "/usr/share/nginx/html", Mode: "ro"},
			{Type: TypePort, Name: "Web UI", HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
			{Type: TypeVariable, Key: "TZ", Value: "Australia/Brisbane"},
			{Type: TypeLabel, Key: "com.example.role", Value: "frontend"},
			{Type: TypeDevice, Host: "/dev/ttyUSB0", Container: "/dev/ttyUSB0", Mode: "rwm"},
		},
	}
}

func TestApplySpec(t *testing.T) {
	cfg, host := map[string]any{}, map[string]any{}
	applySpec(cfg, host, fullSpec(), nil)

	if cfg["Image"] != "nginx:1.27" {
		t.Errorf("image = %v", cfg["Image"])
	}
	if got := cfg["Env"].([]string); !reflect.DeepEqual(got, []string{"TZ=Australia/Brisbane"}) {
		t.Errorf("env = %v", got)
	}
	labels := cfg["Labels"].(map[string]string)
	if labels[ManagedLabel] != "true" || labels["com.example.role"] != "frontend" {
		t.Errorf("labels = %v", labels)
	}
	if _, ok := labels[GPULabel]; ok {
		t.Error("GPU label set without a GPU grant")
	}
	if got := host["Binds"].([]string); !reflect.DeepEqual(got, []string{"/srv/feldcore/web:/usr/share/nginx/html:ro"}) {
		t.Errorf("binds = %v", got)
	}
	pb := host["PortBindings"].(map[string][]portBinding)["80/tcp"]
	if len(pb) != 1 || pb[0].HostPort != "8080" {
		t.Errorf("port bindings = %#v", pb)
	}
	if devs := host["Devices"].([]deviceMapping); len(devs) != 1 || devs[0].PathOnHost != "/dev/ttyUSB0" {
		t.Errorf("devices = %#v", devs)
	}
	if rp := host["RestartPolicy"].(map[string]any); rp["Name"] != "unless-stopped" {
		t.Errorf("restart policy = %v", rp)
	}
}

func TestApplySpecRestartNoIsEmptyName(t *testing.T) {
	// Docker requires the "no" policy to be sent as an empty name.
	cfg, host := map[string]any{}, map[string]any{}
	s := spec()
	s.RestartPolicy = "no"
	applySpec(cfg, host, s, nil)
	if name := host["RestartPolicy"].(map[string]any)["Name"]; name != "" {
		t.Errorf("restart 'no' should serialise to an empty name, got %q", name)
	}
}

func TestApplySpecGPU(t *testing.T) {
	grant := &gpuGrant{Device: "/dev/dri", Groups: []string{"44", "991"}}

	// Turning the GPU on adds the device, the groups and the marker labels,
	// and keeps a group the user had added themselves.
	cfg := map[string]any{}
	host := map[string]any{"GroupAdd": []any{"1000"}}
	s := spec()
	s.GPU = true
	applySpec(cfg, host, s, grant)

	devs := host["Devices"].([]deviceMapping)
	if len(devs) != 1 || devs[0].PathOnHost != "/dev/dri" {
		t.Fatalf("GPU device not added: %#v", devs)
	}
	if got := host["GroupAdd"].([]string); !reflect.DeepEqual(got, []string{"1000", "44", "991"}) {
		t.Errorf("groups = %v", got)
	}
	labels := cfg["Labels"].(map[string]string)
	if labels[GPULabel] != "true" || labels[gpuGroupsLabel] != "44,991" {
		t.Errorf("GPU labels = %v", labels)
	}

	// Turning it off again removes exactly what the grant added.
	cfg2 := map[string]any{"Labels": map[string]any{gpuGroupsLabel: "44,991", GPULabel: "true"}}
	host2 := map[string]any{"GroupAdd": []any{"1000", "44", "991"}}
	s.GPU = false
	applySpec(cfg2, host2, s, nil)
	if got := host2["GroupAdd"].([]string); !reflect.DeepEqual(got, []string{"1000"}) {
		t.Errorf("groups after disabling GPU = %v, want only the user's own", got)
	}
	if devs := host2["Devices"].([]deviceMapping); len(devs) != 0 {
		t.Errorf("GPU device not removed: %#v", devs)
	}
	if _, ok := cfg2["Labels"].(map[string]string)[GPULabel]; ok {
		t.Error("GPU label not removed")
	}
}

// inspectFixture is what the daemon reports for a container made from
// fullSpec(), including values inherited from the image.
func inspectFixture(t *testing.T) *inspectContainer {
	t.Helper()
	raw := `{
	  "Id": "0123456789abcdef0123456789abcdef",
	  "Name": "/web",
	  "Image": "sha256:feedface",
	  "State": {"Running": true},
	  "Config": {
	    "Hostname": "0123456789ab",
	    "Image": "nginx:1.27",
	    "Env": ["PATH=/usr/local/sbin:/usr/bin", "NGINX_VERSION=1.27.0", "TZ=Australia/Brisbane"],
	    "Cmd": ["nginx", "-g", "daemon off;"],
	    "Entrypoint": ["/docker-entrypoint.sh"],
	    "WorkingDir": "",
	    "Labels": {
	      "maintainer": "NGINX Docker Maintainers",
	      "feldcore.managed": "true",
	      "com.example.role": "frontend"
	    },
	    "ExposedPorts": {"80/tcp": {}}
	  },
	  "HostConfig": {
	    "NetworkMode": "default",
	    "Binds": ["/srv/feldcore/web:/usr/share/nginx/html:ro"],
	    "PortBindings": {"80/tcp": [{"HostIp": "", "HostPort": "8080"}]},
	    "Devices": [{"PathOnHost": "/dev/ttyUSB0", "PathInContainer": "/dev/ttyUSB0", "CgroupPermissions": "rwm"}],
	    "RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0},
	    "Memory": 9223372036854775807,
	    "CapAdd": ["NET_ADMIN"],
	    "Privileged": false,
	    "ShmSize": 67108864
	  },
	  "NetworkSettings": {"Networks": {"bridge": {"IPAddress": "172.17.0.2"}}}
	}`
	var in inspectContainer
	if err := decodeRaw([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	return &in
}

func imageFixture(t *testing.T) map[string]any {
	t.Helper()
	var cfg map[string]any
	raw := `{
	  "Env": ["PATH=/usr/local/sbin:/usr/bin", "NGINX_VERSION=1.27.0"],
	  "Cmd": ["nginx", "-g", "daemon off;"],
	  "Entrypoint": ["/docker-entrypoint.sh"],
	  "WorkingDir": "",
	  "Labels": {"maintainer": "NGINX Docker Maintainers"}
	}`
	if err := decodeRaw([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSpecFromInspect(t *testing.T) {
	meta := map[string]store.ConfigMeta{
		"path:/usr/share/nginx/html": {Name: "Site", Description: "Static files"},
		"port:80/tcp":                {Name: "Web UI"},
	}
	got := specFromInspect(inspectFixture(t), imageFixture(t), meta)

	want := fullSpec()
	want.Configs[0].Description = "Static files"
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("derived spec differs.\n--- got ---\n%s\n--- want ---\n%s", gj, wj)
	}

	// Image defaults must not appear as this container's settings.
	for _, c := range got.Configs {
		if c.Key == "PATH" || c.Key == "NGINX_VERSION" || c.Key == "maintainer" {
			t.Errorf("image default %q leaked into the spec", c.Key)
		}
		if strings.HasPrefix(c.Key, "feldcore.") {
			t.Errorf("internal label %q leaked into the spec", c.Key)
		}
	}
}

func TestSpecFromInspectHidesGPUDevice(t *testing.T) {
	in := inspectFixture(t)
	in.Config["Labels"].(map[string]any)[GPULabel] = "true"
	in.HostConfig["Devices"] = append(in.HostConfig["Devices"].([]any),
		map[string]any{"PathOnHost": "/dev/dri", "PathInContainer": "/dev/dri", "CgroupPermissions": "rwm"})

	got := specFromInspect(in, imageFixture(t), nil)
	if !got.GPU {
		t.Error("GPU flag not derived from the label")
	}
	for _, c := range got.Configs {
		if c.Type == TypeDevice && c.Host == "/dev/dri" {
			t.Error("the GPU device should be shown as the GPU switch, not as a raw device")
		}
	}
}

func TestRecreateBodyPreservesAndCleans(t *testing.T) {
	in := inspectFixture(t)
	s := fullSpec()
	s.Configs[1].HostPort = 9090 // the edit: move the web port
	body := recreateBody(in, imageFixture(t), s, nil)

	// Settings FELDCORE does not model survive the round trip untouched,
	// including a number too large for a float64 to hold exactly.
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Memory":9223372036854775807`, `"CapAdd":["NET_ADMIN"]`, `"ShmSize":67108864`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("recreate body lost %s", want)
		}
	}
	// Image defaults are not pinned onto the new container.
	for _, k := range []string{"Cmd", "Entrypoint", "WorkingDir"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s equals the image default and should have been dropped", k)
		}
	}
	// Neither is the old container id as hostname.
	if _, ok := body["Hostname"]; ok {
		t.Error("the old container id should not be carried over as the hostname")
	}
	// The edit took effect.
	if !strings.Contains(string(raw), `"HostPort":"9090"`) || strings.Contains(string(raw), `"HostPort":"8080"`) {
		t.Errorf("port edit not applied: %s", raw)
	}
	// The source container's maps were not modified.
	if in.Config["Cmd"] == nil || in.HostConfig["Binds"] == nil {
		t.Error("recreateBody must not mutate the inspected container")
	}
}

func TestRecreateBodyKeepsOverriddenCommand(t *testing.T) {
	in := inspectFixture(t)
	in.Config["Cmd"] = []any{"nginx-debug", "-g", "daemon off;"} // user override
	body := recreateBody(in, imageFixture(t), fullSpec(), nil)
	if body["Cmd"] == nil {
		t.Error("a command that differs from the image default must be kept")
	}
}

func TestRecreateBodyCustomNetworkKeepsAddress(t *testing.T) {
	in := inspectFixture(t)
	in.HostConfig["NetworkMode"] = "lan"
	in.NetworkSettings.Networks = map[string]map[string]any{
		"lan": {
			"IPAMConfig": map[string]any{"IPv4Address": "192.168.0.50"},
			"Aliases":    []any{"web", "0123456789ab"},
		},
	}
	s := fullSpec()
	s.Network = "lan"
	body := recreateBody(in, imageFixture(t), s, nil)
	raw, _ := json.Marshal(body["NetworkingConfig"])
	if !strings.Contains(string(raw), `"IPv4Address":"192.168.0.50"`) {
		t.Errorf("static address lost: %s", raw)
	}
	if strings.Contains(string(raw), "0123456789ab") {
		t.Errorf("old container id kept as a network alias: %s", raw)
	}
}

func TestMetaOfAndConfigKey(t *testing.T) {
	meta := metaOf(fullSpec())
	if meta["path:/usr/share/nginx/html"].Name != "Site" || meta["port:80/tcp"].Name != "Web UI" {
		t.Errorf("meta = %v", meta)
	}
	if _, ok := meta["variable:TZ"]; ok {
		t.Error("settings without a name or description should not be stored")
	}
}

func TestToContainer(t *testing.T) {
	rc := apiContainer{
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
	}
	c := toContainer(rc, "192.168.0.10")
	if c.ID != "abcdef123456" || c.Name != "web" || !c.Managed {
		t.Errorf("basic fields wrong: %+v", c)
	}
	if len(c.Ports) != 1 {
		t.Fatalf("expected 1 published port after de-duping, got %v", c.Ports)
	}
	if c.WebURL != "http://HOST:8080" {
		t.Errorf("web url = %q", c.WebURL)
	}
	// Default bridge: reachable on the server's own address.
	if c.Network != "bridge" || !reflect.DeepEqual(c.IPs, []string{"192.168.0.10"}) {
		t.Errorf("bridge allocation = %q %v", c.Network, c.IPs)
	}
}

func TestToContainerCustomNetworkShowsOwnAddress(t *testing.T) {
	rc := apiContainer{ID: "x", Names: []string{"/cam"}, State: "running"}
	rc.HostConfig.NetworkMode = "frigate_default"
	rc.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
	}{"frigate_default": {IPAddress: "172.19.0.3"}}
	c := toContainer(rc, "192.168.0.10")
	if c.Network != "frigate_default" || !reflect.DeepEqual(c.IPs, []string{"172.19.0.3"}) {
		t.Errorf("custom network allocation = %q %v", c.Network, c.IPs)
	}
}

func TestToContainerSlicesNeverNil(t *testing.T) {
	// A stopped container reports no published ports. Slices must serialise
	// as [] not null, or the UI crashes reading .length. Regression test.
	c := toContainer(apiContainer{ID: "x", Names: []string{"/stopped"}, State: "exited"}, "")
	for name, v := range map[string]any{"ports": c.Ports, "ips": c.IPs} {
		if b, _ := json.Marshal(v); string(b) != "[]" {
			t.Errorf("%s marshalled to %s, want []", name, b)
		}
	}
}

func TestDetectGPUsMarksNvidiaUnsupported(t *testing.T) {
	dir := t.TempDir()
	for card, vendor := range map[string]string{"card0": "0x10de", "card1": "0x1002"} {
		dev := filepath.Join(dir, card, "device")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, "vendor"), []byte(vendor+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A connector entry must not be counted as a card.
	_ = os.MkdirAll(filepath.Join(dir, "card0-HDMI-A-1"), 0o755)

	old := sysDRM
	sysDRM = dir
	defer func() { sysDRM = old }()

	gpus := DetectGPUs()
	if len(gpus) != 2 {
		t.Fatalf("expected 2 GPUs, got %d: %+v", len(gpus), gpus)
	}
	if gpus[0].Vendor != "nvidia" || gpus[0].Supported || gpus[0].Note == "" {
		t.Errorf("NVIDIA should be detected but not supported, with a note: %+v", gpus[0])
	}
	if gpus[1].Vendor != "amd" {
		t.Errorf("second GPU should be AMD: %+v", gpus[1])
	}
}

func TestDemux(t *testing.T) {
	// Two stdout frames (stream id 1) with an 8-byte header each.
	frame := func(s string) string {
		n := len(s)
		return string([]byte{1, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}) + s
	}
	got, err := demux(strings.NewReader(frame("hello\n") + frame("world\n")))
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

func TestConfiguredPortsForStoppedContainer(t *testing.T) {
	// Docker lists no ports for a stopped container, but it still lays claim
	// to the ones it is configured with; the allocations table needs them.
	got := configuredPorts(inspectFixture(t))
	want := []Port{{Host: 8080, Container: 80, Proto: "tcp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configuredPorts = %v, want %v", got, want)
	}
	// And never nil, so it marshals to [] for the UI.
	empty := configuredPorts(&inspectContainer{HostConfig: map[string]any{}})
	if b, _ := json.Marshal(empty); string(b) != "[]" {
		t.Errorf("no ports marshalled to %s, want []", b)
	}
}
