package docker

import "testing"

func TestPlanNetworkDefaultsToPrivate(t *testing.T) {
	s := Spec{Name: "plex", Image: "plex", Network: "bridge"}
	p, err := planNetwork(s, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Isolate != IsolatePrivate || p.Spec.Network != "feldcore-app-plex" || p.Ensure != "feldcore-app-plex" {
		t.Fatalf("plan %+v", p)
	}
	if p.ConnectSelf || p.LinkNetwork != "feldcore-app-plex" {
		t.Fatalf("links %+v", p)
	}
}

func TestPlanNetworkSharedKeepsBridge(t *testing.T) {
	s := Spec{Name: "plex", Image: "plex", Network: "bridge", Isolate: IsolateShared}
	p, err := planNetwork(s, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "bridge" || p.Spec.Isolate != IsolateShared || p.Ensure != "" {
		t.Fatalf("plan %+v", p)
	}
}

func TestPlanNetworkHostAndCustomAreExplicit(t *testing.T) {
	host := Spec{Name: "plex", Image: "plex", Network: "host"}
	p, err := planNetwork(host, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "host" || p.Spec.Isolate != IsolateShared {
		t.Fatalf("host plan %+v", p.Spec)
	}

	lan := Spec{Name: "plex", Image: "plex", Network: "lan"}
	p, err = planNetwork(lan, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "lan" || p.Spec.Isolate != IsolateShared || p.Ensure != "" {
		t.Fatalf("custom plan %+v", p)
	}
}

func TestPlanNetworkSkipsVPNAndGateway(t *testing.T) {
	s := Spec{Name: "web", Image: "nginx", Network: "host", VPN: "proton-nl", Isolate: IsolatePrivate, Links: []string{"db"}}
	p, err := planNetwork(s, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "host" || p.Spec.Isolate != IsolateShared || p.Spec.Links != nil || p.Ensure != "" {
		t.Fatalf("vpn plan %+v", p)
	}

	g := Spec{Name: "feldcore-vpn-proton-nl", Image: "qmcgaw/gluetun", Network: "bridge", Gateway: true}
	p, err = planNetwork(g, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "bridge" || p.Ensure != "" {
		t.Fatalf("gateway plan %+v", p)
	}

	shared := Spec{Name: "web", Image: "nginx", Network: "container:feldcore-vpn-proton-nl"}
	p, err = planNetwork(shared, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "container:feldcore-vpn-proton-nl" || p.Ensure != "" {
		t.Fatalf("container mode plan %+v", p.Spec)
	}
}

func TestPlanNetworkDoesNotMigrateExisting(t *testing.T) {
	s := Spec{Name: "plex", Image: "plex", Network: "bridge"}
	p, err := planNetwork(s, false, "plex", "bridge", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "bridge" || p.Spec.Isolate != IsolateShared || p.Ensure != "" {
		t.Fatalf("existing bridge was moved: %+v", p)
	}

	p, err = planNetwork(s, false, "plex", "feldcore-app-plex", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "feldcore-app-plex" || p.Spec.Isolate != IsolatePrivate {
		t.Fatalf("existing private network was dropped: %+v", p.Spec)
	}
}

func TestPlanNetworkIsolateButtonAndLinks(t *testing.T) {
	s := Spec{Name: "plex", Image: "plex", Network: "bridge", Isolate: IsolatePrivate, Links: []string{"db", "db", " "}}
	p, err := planNetwork(s, false, "plex", "bridge", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "feldcore-app-plex" || len(p.Spec.Links) != 1 || p.Spec.Links[0] != "db" {
		t.Fatalf("isolate %+v", p.Spec)
	}

	off := Spec{Name: "plex", Image: "plex", Network: "bridge", Isolate: IsolateShared}
	p, err = planNetwork(off, false, "plex", "feldcore-app-plex", []string{"db"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "bridge" || p.Retire != "feldcore-app-plex" {
		t.Fatalf("un-isolate %+v", p)
	}

	linked := Spec{Name: "plex", Image: "plex", Network: "bridge", Isolate: IsolateShared, Links: []string{"db"}}
	p, err = planNetwork(linked, true, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "bridge" || !p.ConnectSelf || p.LinkNetwork != "feldcore-app-plex" || p.Ensure != "feldcore-app-plex" {
		t.Fatalf("shared links %+v", p)
	}
}

func TestPlanNetworkRejectsBadLinks(t *testing.T) {
	self := Spec{Name: "plex", Image: "plex", Links: []string{"plex"}}
	if _, err := planNetwork(self, true, "", "", nil); err == nil {
		t.Fatal("a container linked to itself")
	}
	host := Spec{Name: "plex", Image: "plex", Network: "host", Links: []string{"db"}}
	if _, err := planNetwork(host, true, "", "", nil); err == nil {
		t.Fatal("host network accepted a link")
	}
	anon := Spec{Image: "nginx", Isolate: IsolatePrivate}
	if _, err := planNetwork(anon, true, "", "", nil); err == nil {
		t.Fatal("private network without a name")
	}
}

func TestPlanNetworkRenameRetiresOldBridge(t *testing.T) {
	s := Spec{Name: "plex2", Image: "plex", Isolate: IsolatePrivate}
	p, err := planNetwork(s, false, "plex", "feldcore-app-plex", []string{"db"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Network != "feldcore-app-plex2" || p.Retire != "feldcore-app-plex" {
		t.Fatalf("rename %+v", p)
	}
}

func TestSpecRoundTripKeepsIsolation(t *testing.T) {
	s := fullSpec()
	s.Isolate = IsolatePrivate
	s.Links = []string{"db", "cache"}
	cfg, host := map[string]any{}, map[string]any{}
	applySpec(cfg, host, s, nil)
	labels := cfg["Labels"].(map[string]string)
	if labels[netLabel] != IsolatePrivate || labels[linksLabel] != "cache,db" {
		t.Fatalf("labels %v", labels)
	}

	in := inspectFixture(t)
	in.Name = "/web"
	in.HostConfig["NetworkMode"] = "feldcore-app-web"
	in.Config["Labels"] = map[string]any{
		netLabel:   IsolatePrivate,
		linksLabel: "cache,db",
	}
	got := specFromInspect(in, imageFixture(t), nil)
	if got.Isolate != IsolatePrivate || got.Network != "bridge" {
		t.Fatalf("form network %+v isolate %s", got.Network, got.Isolate)
	}
	if len(got.Links) != 2 || got.Links[0] != "cache" || got.Links[1] != "db" {
		t.Fatalf("links %+v", got.Links)
	}
}
