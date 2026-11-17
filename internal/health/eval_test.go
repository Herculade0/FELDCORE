package health

import (
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestEvalLevels(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	th := Defaults()
	passed := true
	failed := false
	temp := 42.0
	rep := Eval(Snapshot{
		Host: &HostSample{CPUPercent: 96, MemPercent: 50, Load1: 1, Cores: 4},
		Disks: []Space{
			{Name: "/", Kind: "disk", UsedPercent: 93},
			{Name: "tank", Kind: "pool", UsedPercent: 10, Health: "DEGRADED"},
		},
		Temps: []Temp{{Name: "coretemp", Kind: "cpu", Celsius: 91}},
		SMART: []DiskSMART{
			{Device: "/dev/sda", Available: true, Passed: &passed, Model: "HDD", TempC: &temp, Attributes: []Attribute{{Name: "Reallocated_Sector_Ct", Raw: "0"}}},
			{Device: "/dev/sdb", Available: true, Passed: &failed, Pending: 2},
		},
		Asked: map[string]bool{"containers": true, "vms": true, "systemd": true, "certs": true, "tunnels": true},
		Containers: []ContainerSnap{
			{Name: "plex", State: "running", Health: "unhealthy"},
			{Name: "web", State: "running", Health: "healthy", RestartsInWindow: 9},
		},
		VMs:   []VMSnap{{Name: "win", State: "crashed"}, {Name: "linux", State: "running"}},
		Units: []Unit{{Name: "nginx.service", Description: "web server"}},
		Certs: []Cert{{Name: "home.example", NotAfter: now.Add(3 * 24 * time.Hour)}},
		Tunnels: []TunnelSnap{
			{Name: "Cloudflare", Kind: "cloudflare", State: "down", Detail: "connector stopped"},
			{Name: "NL", Kind: "vpn", State: "up"},
		},
	}, th, now)

	if rep.Overall != Crit {
		t.Fatalf("overall %s summary %s", rep.Overall, rep.Summary)
	}
	want := map[string]Level{
		"cpu":                          Crit,
		"memory":                       OK,
		"load":                         OK,
		"disk:/":                       Crit,
		"pool:tank":                    Warn,
		"temp:cpu:coretemp":            Crit,
		"smart:/dev/sda":               OK,
		"smart:/dev/sdb":               Crit,
		"container:plex:health":        Crit,
		"container:web:restart":        Crit,
		"vm:win":                       Crit,
		"vm:linux":                     OK,
		"systemd:nginx.service":        Crit,
		"cert:home.example":            Crit,
		"tunnel:cloudflare:Cloudflare": Crit,
		"tunnel:vpn:NL":                OK,
	}
	got := map[string]Level{}
	for _, c := range rep.Checks {
		got[c.ID] = c.Level
	}
	for id, level := range want {
		if got[id] != level {
			t.Errorf("%s: got %s want %s", id, got[id], level)
		}
	}
	if len(findCheck(rep, "smart:/dev/sda").Attributes) != 1 {
		t.Fatal("expected SMART attributes on a healthy disk")
	}
}

func TestEvalQuietUnknowns(t *testing.T) {
	rep := Eval(Snapshot{
		Host:  &HostSample{CPUPercent: 3, MemPercent: 10, Load1: 0.1, Cores: 2},
		Notes: []Check{{ID: "smart:unavailable", Category: "smart", Name: "SMART", Level: Unknown, Summary: "smartctl is not installed"}},
		Asked: map[string]bool{"systemd": true, "tunnels": true, "certs": true, "containers": true, "vms": true},
	}, Defaults(), time.Now())
	if rep.Overall != OK {
		t.Fatalf("unknown must not turn the machine red: %s %s", rep.Overall, rep.Summary)
	}
	if !strings.Contains(rep.Summary, "could not be read") {
		t.Fatal(rep.Summary)
	}
}

func TestValidateThresholds(t *testing.T) {
	if err := Validate(ToStore(Defaults())); err != nil {
		t.Fatal(err)
	}
	bad := Defaults()
	bad.CPUWarn = 99
	bad.CPUCrit = 90
	if err := Validate(ToStore(bad)); err == nil {
		t.Fatal("expected warning below critical")
	}
	if err := Validate(store.HealthThresholds{}); err != nil {
		t.Fatal("empty means defaults")
	}
}

func TestAsleepDiskIsNotCritical(t *testing.T) {
	rep := Eval(Snapshot{SMART: []DiskSMART{{Device: "/dev/sda", Available: true, Asleep: true}}}, Defaults(), time.Now())
	c := findCheck(rep, "smart:/dev/sda")
	if c.Level != Unknown || !strings.Contains(c.Summary, "asleep") {
		t.Fatalf("%+v", c)
	}
	if rep.Overall != Unknown {
		t.Fatalf("only unknown checks, overall %s", rep.Overall)
	}
}

func findCheck(r Report, id string) Check {
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	return Check{}
}
