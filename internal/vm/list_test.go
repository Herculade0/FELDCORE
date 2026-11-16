package vm

import (
	"context"
	"strings"
	"testing"
)

const sample = `
 Id    Name                           State
----------------------------------------------------
 2     router                         running
 -     oldbox                         shut off
`

func TestParseVirshList(t *testing.T) {
	vms, err := parseVirshList(sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 || vms[0].ID != "2" || vms[0].Name != "router" || vms[0].State != "running" {
		t.Fatalf("first: %+v", vms)
	}
	if vms[1].ID != "" || vms[1].Name != "oldbox" || vms[1].State != "shut off" {
		t.Fatalf("second: %+v", vms[1])
	}
	if _, err := parseVirshList("not a table"); err == nil {
		t.Fatal("accepted output that is not a domain list")
	}
}

func TestProbeList(t *testing.T) {
	missing := Probe{Look: func(string) (string, bool) { return "", false }}
	got := missing.List(context.Background())
	if got.Available || got.VMs == nil || !strings.Contains(got.Message, "not installed") {
		t.Fatalf("missing virsh: %+v", got)
	}

	ran := false
	present := Probe{
		Look: func(string) (string, bool) { return "/usr/bin/virsh", true },
		Run: func(_ context.Context, path string, args ...string) (string, error) {
			ran = true
			if path != "/usr/bin/virsh" || len(args) == 0 || args[0] != "-r" {
				t.Fatalf("ran %s %v", path, args)
			}
			return sample, nil
		},
	}
	got = present.List(context.Background())
	if !ran || !got.Available || len(got.VMs) != 2 {
		t.Fatalf("list: %+v ran=%v", got, ran)
	}
}
