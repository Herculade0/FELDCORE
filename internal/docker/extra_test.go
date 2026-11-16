package docker

import (
	"strings"
	"testing"
)

func TestParseExtraLiftsConfigFlags(t *testing.T) {
	e := parseExtra("--restart=unless-stopped --privileged --cap-add=NET_RAW --shm-size=1gb -p 127.0.0.1:8080:80/tcp --device=/dev/dri -e TZ=UTC --gpus all")
	if e.Restart != "unless-stopped" || !e.Privileged {
		t.Fatalf("restart/privileged: %+v", e)
	}
	if e.shmSize == nil || *e.shmSize != 1<<30 {
		t.Fatalf("shm %+v", e.shmSize)
	}
	if len(e.capAdd) != 1 || e.capAdd[0] != "NET_RAW" {
		t.Fatalf("caps %+v", e.capAdd)
	}
	if strings.Contains(e.Cleaned, "--restart") || strings.Contains(e.Cleaned, "--privileged") || strings.Contains(e.Cleaned, "--device") {
		t.Errorf("lifted flags still in cleaned %q", e.Cleaned)
	}
	if !strings.Contains(e.Cleaned, "--cap-add=NET_RAW") || !strings.Contains(e.Cleaned, "--shm-size=1gb") {
		t.Errorf("host flags dropped from cleaned %q", e.Cleaned)
	}
	if len(e.Unparsed) != 1 || e.Unparsed[0] != "--gpus all" {
		t.Errorf("unparsed %+v", e.Unparsed)
	}
	var port, dev, env Config
	for _, c := range e.Configs {
		switch c.Type {
		case TypePort:
			port = c
		case TypeDevice:
			dev = c
		case TypeVariable:
			env = c
		}
	}
	if port.HostPort != 8080 || port.ContainerPort != 80 || port.HostIP != "127.0.0.1" || port.Proto != "tcp" {
		t.Errorf("port %+v", port)
	}
	if dev.Host != "/dev/dri" {
		t.Errorf("device %+v", dev)
	}
	if env.Key != "TZ" || env.Value != "UTC" {
		t.Errorf("env %+v", env)
	}
}

func TestApplyExtraDoesNotTouchUnrelatedKeys(t *testing.T) {
	cfg := map[string]any{}
	host := map[string]any{"Memory": "keep-me", "CapAdd": []any{"NET_ADMIN"}}
	applyExtra(cfg, host, "--shm-size=64m", "")
	if host["Memory"] != "keep-me" {
		t.Fatalf("memory changed: %+v", host["Memory"])
	}
	if _, ok := host["CapAdd"]; !ok {
		t.Fatal("existing cap-add was cleared")
	}
	if host["ShmSize"].(int64) != 64<<20 {
		t.Fatalf("shm %v", host["ShmSize"])
	}
	applyExtra(cfg, host, "--cap-add=SYS_ADMIN", "--shm-size=64m --cap-add=NET_ADMIN")
	if _, ok := host["ShmSize"]; ok {
		t.Fatal("shm-size was not removed with the previous extra params")
	}
	caps := host["CapAdd"].([]string)
	if len(caps) != 1 || caps[0] != "SYS_ADMIN" {
		t.Fatalf("caps %+v", caps)
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{
		"1g":   1 << 30,
		"1gb":  1 << 30,
		"1G":   1 << 30,
		"512m": 512 << 20,
		"64mb": 64 << 20,
		"1024": 1024,
	}
	for in, want := range cases {
		got, ok := parseBytes(in)
		if !ok || got != want {
			t.Errorf("parseBytes(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
}
