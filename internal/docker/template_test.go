package docker

import (
	"strings"
	"testing"
)

const plexXML = `<?xml version="1.0"?>
<Container>
  <Name>Plex Media Server</Name>
  <Repository>lscr.io/linuxserver/plex:latest</Repository>
  <Network>host</Network>
  <Privileged>false</Privileged>
  <WebUI>http://[IP]:[PORT:32400]/web</WebUI>
  <Icon>https://example.com/plex.png</Icon>
  <ExtraParams>--restart unless-stopped --cap-add=NET_ADMIN --device /dev/net/tun --gpus all</ExtraParams>
  <Config Name="Web UI" Target="32400" Default="" Mode="tcp" Description="Browser port" Type="Port" Display="always" Required="true" Mask="false">32400</Config>
  <Config Name="Appdata" Target="/config" Default="/mnt/user/appdata/plex" Mode="rw" Description="Config files" Type="Path" Display="always" Required="true" Mask="false">/mnt/user/appdata/plex</Config>
  <Config Name="Media" Target="/media" Default="/mnt/user/media" Mode="ro" Description="Library" Type="Path" Display="always" Required="false" Mask="false"></Config>
  <Config Name="Movies" Target="/movies" Default="/mnt/disk1/films/hd" Mode="rw,slave" Description="Films on a disk" Type="Path" Display="advanced" Required="false" Mask="false">/mnt/disk1/films/hd</Config>
  <Config Name="PUID" Target="PUID" Default="99" Mode="" Description="User" Type="Variable" Display="advanced-hide" Required="true" Mask="false">99</Config>
  <Config Name="PGID" Target="PGID" Default="100" Mode="" Description="Group" Type="Variable" Display="advanced" Required="true" Mask="false"></Config>
  <Config Name="Claim" Target="PLEX_CLAIM" Default="" Mode="" Description="Claim token" Type="Variable" Display="always" Required="false" Mask="true"></Config>
  <Config Name="Token" Target="PLEX_TOKEN" Default="s3cret" Mode="" Description="API token" Type="Variable" Display="always" Required="true" Mask="true">s3cret</Config>
  <Config Name="Version" Target="VERSION" Default="docker" Mode="" Description="Build" Type="Variable" Display="hidden" Required="false" Mask="false">docker</Config>
  <Config Name="Transcode" Target="/dev/dri" Default="" Mode="" Description="GPU" Type="Device" Display="advanced" Required="false" Mask="false">/dev/dri</Config>
  <Config Name="Role" Target="app.role" Default="media" Mode="" Description="A label" Type="Label" Display="always" Required="false" Mask="false">media</Config>
  <Config Name="Empty optional" Target="OPTIONAL" Default="" Mode="" Description="Skip me" Type="Variable" Display="always" Required="false" Mask="false"></Config>
  <Config Name="Needed" Target="NEEDED" Default="" Mode="" Description="Must be set" Type="Variable" Display="always" Required="true" Mask="false"></Config>
</Container>`

func testPaths() PathMap {
	return PathMap{
		Appdata:    "/srv/feldcore/appdata",
		SharesRoot: "/srv/feldcore",
		Shares:     map[string]string{"media": "/srv/feldcore/media"},
	}
}

func TestParseTemplateXML(t *testing.T) {
	tmpl, err := ParseTemplateXML([]byte(plexXML))
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name != "Plex Media Server" || tmpl.Repository != "lscr.io/linuxserver/plex:latest" {
		t.Fatalf("identity: %+v", tmpl)
	}
	if tmpl.Network != "host" || tmpl.Privileged {
		t.Fatalf("network/privileged: %+v", tmpl)
	}
	if len(tmpl.Configs) != 13 {
		t.Fatalf("configs: %d", len(tmpl.Configs))
	}
	claim := tmpl.Configs[6]
	if !claim.Mask || claim.Target != "PLEX_CLAIM" || claim.Value != "" {
		t.Fatalf("claim config: %+v", claim)
	}
}

func TestTemplateToSpecMapsConfigs(t *testing.T) {
	tmpl, err := ParseTemplateXML([]byte(plexXML))
	if err != nil {
		t.Fatal(err)
	}
	spec, notes := TemplateToSpec(tmpl, testPaths())

	if spec.Name != "Plex-Media-Server" {
		t.Errorf("name %q", spec.Name)
	}
	if spec.Image != "lscr.io/linuxserver/plex:latest" || spec.Network != "host" {
		t.Errorf("image/network: %+v", spec)
	}
	if spec.WebUI != "http://[IP]:[PORT:32400]/web" || spec.Icon != "https://example.com/plex.png" {
		t.Errorf("webui/icon: %q %q", spec.WebUI, spec.Icon)
	}
	if !spec.Autostart || spec.RestartPolicy != "unless-stopped" {
		t.Errorf("restart/autostart: %+v", spec)
	}
	// --restart was lifted onto the policy; --gpus is not applied.
	if strings.Contains(spec.ExtraParams, "--restart") || !strings.Contains(spec.ExtraParams, "--cap-add=NET_ADMIN") {
		t.Errorf("extra params %q", spec.ExtraParams)
	}
	if strings.Contains(spec.ExtraParams, "--device") {
		t.Errorf("device flag should be a setting, still in extra: %q", spec.ExtraParams)
	}

	by := map[string]Config{}
	var devs []Config
	for _, c := range spec.Configs {
		switch c.Type {
		case TypePath:
			by["path:"+c.Container] = c
		case TypePort:
			by["port"] = c
		case TypeVariable:
			by["var:"+c.Key] = c
		case TypeLabel:
			by["label:"+c.Key] = c
		case TypeDevice:
			devs = append(devs, c)
		}
	}

	appdata := by["path:/config"]
	if appdata.Host != "/srv/feldcore/appdata/plex" || appdata.Mode != "rw" || !appdata.Required || appdata.Display != "always" {
		t.Errorf("appdata: %+v", appdata)
	}
	media := by["path:/media"]
	if media.Host != "/srv/feldcore/media" || media.Mode != "ro" {
		t.Errorf("share path: %+v", media)
	}
	films := by["path:/movies"]
	if films.Host != "/srv/feldcore/films/hd" || films.Mode != "rw,slave" || films.Display != "advanced" {
		t.Errorf("disk path: %+v", films)
	}
	port := by["port"]
	if port.HostPort != 32400 || port.ContainerPort != 32400 || port.Proto != "tcp" || !port.Required {
		t.Errorf("port: %+v", port)
	}
	puid := by["var:PUID"]
	if puid.Value != "99" || puid.Display != "hidden" || !puid.Required {
		t.Errorf("puid display/value: %+v", puid)
	}
	pgid := by["var:PGID"]
	if pgid.Value != "100" || pgid.Display != "advanced" {
		t.Errorf("pgid should use Default when value is empty: %+v", pgid)
	}
	token := by["var:PLEX_TOKEN"]
	if !token.Mask || token.Value != "s3cret" {
		t.Errorf("masked value must stay masked and keep its value: %+v", token)
	}
	if _, ok := by["var:PLEX_CLAIM"]; ok {
		t.Error("optional empty variable should be omitted")
	}
	needed := by["var:NEEDED"]
	if !needed.Required || needed.Value != "" {
		t.Errorf("required empty variable should be kept for the user: %+v", needed)
	}
	if by["var:VERSION"].Display != "hidden" || by["var:VERSION"].Value != "docker" {
		t.Errorf("hidden variable: %+v", by["var:VERSION"])
	}
	if by["label:app.role"].Value != "media" {
		t.Errorf("label: %+v", by["label:app.role"])
	}
	foundTun := false
	for _, d := range devs {
		if d.Host == "/dev/dri" && d.Container == "/dev/dri" {
			if d.Display != "advanced" {
				t.Errorf("dri device: %+v", d)
			}
		}
		if d.Host == "/dev/net/tun" {
			foundTun = true
		}
	}
	if !foundTun {
		t.Errorf("extra --device not mapped: %+v", devs)
	}

	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "/srv/feldcore/appdata") || !strings.Contains(joined, "PUID 99") {
		t.Errorf("notes: %q", notes)
	}
	if !strings.Contains(joined, "--gpus") {
		t.Errorf("unparsed extra not mentioned: %q", notes)
	}

	// The required empty variable blocks apply until the user fills it in.
	if err := normaliseSpec(&spec); err == nil {
		t.Fatal("expected validation to reject the empty required variable")
	}
	for i := range spec.Configs {
		if spec.Configs[i].Key == "NEEDED" {
			spec.Configs[i].Value = "set"
		}
	}
	if err := normaliseSpec(&spec); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateValueBeatsDefaultAndCustomNetwork(t *testing.T) {
	tmpl := Template{
		Name:       "Ledgerly",
		Repository: "example/ledgerly:1",
		Network:    "br0",
		Configs: []TemplateConfig{
			{Name: "Web", Target: "8080", Default: "80", Value: "5005", Mode: "tcp", Type: "Port", Display: "always", Required: true},
			{Name: "Both", Target: "53", Default: "53", Value: "", Mode: "tcp,udp", Type: "Port", Display: "always"},
			{Name: "Climb", Target: "/data", Default: "/mnt/user/appdata/plex/../../secrets", Type: "Path", Display: "always", Required: true},
		},
	}
	spec, notes := TemplateToSpec(tmpl, testPaths())
	if spec.Network != "bridge" {
		t.Errorf("network %q", spec.Network)
	}
	if !strings.Contains(strings.Join(notes, " "), "br0") {
		t.Errorf("network note missing: %q", notes)
	}
	var web, tcp, udp Config
	for _, c := range spec.Configs {
		switch {
		case c.ContainerPort == 8080:
			web = c
		case c.ContainerPort == 53 && c.Proto == "tcp":
			tcp = c
		case c.ContainerPort == 53 && c.Proto == "udp":
			udp = c
		}
	}
	if web.HostPort != 5005 {
		t.Errorf("value should beat default, got %+v", web)
	}
	if tcp.HostPort != 53 || udp.HostPort != 53 {
		t.Errorf("dual proto: %+v %+v", tcp, udp)
	}
	for _, c := range spec.Configs {
		if c.Type == TypePath && c.Host != "/srv/feldcore/secrets" {
			t.Errorf("path with .. = %+v, want the cleaned share", c)
		}
	}
	if got := joinUnder("/srv/feldcore/appdata", "../../etc"); got != "/srv/feldcore/appdata" {
		t.Errorf("joinUnder escaped: %s", got)
	}
}

func TestRewriteUnraidPath(t *testing.T) {
	m := testPaths()
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"/mnt/user/appdata/plex/", "/srv/feldcore/appdata/plex", true},
		{"/mnt/cache/appdata/plex", "/srv/feldcore/appdata/plex", true},
		{"/mnt/user0/appdata", "/srv/feldcore/appdata", true},
		{"/mnt/user/media/tv", "/srv/feldcore/media/tv", true},
		{"/mnt/user/photos", "/srv/feldcore/photos", true},
		{"/opt/data", "/opt/data", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, changed := rewriteUnraidPath(tc.in, m)
		if got != tc.want || changed != tc.changed {
			t.Errorf("rewrite %q = %q,%v want %q,%v", tc.in, got, changed, tc.want, tc.changed)
		}
	}
}

func TestResolveWebUI(t *testing.T) {
	ports := []Port{{Host: 32400, Container: 32400, Proto: "tcp"}}
	if got := resolveWebUI("http://[IP]:[PORT:32400]/web", ports); got != "http://HOST:32400/web" {
		t.Errorf("resolved %q", got)
	}
	if got := resolveWebUI("http://[IP]:[PORT:9999]/", nil); got != "http://HOST:9999/" {
		t.Errorf("fallback port %q", got)
	}
	if got := resolveWebUI("http://[IP]/[SOMETHING]", ports); got != "" {
		t.Errorf("unresolved placeholder returned %q", got)
	}
}

func TestIconLabelRoundTrip(t *testing.T) {
	s := spec()
	s.Icon = "https://example.com/icon.png"
	if err := normaliseSpec(&s); err != nil {
		t.Fatal(err)
	}
	cfg, host := map[string]any{}, map[string]any{}
	applySpec(cfg, host, s, nil)
	labels := cfg["Labels"].(map[string]string)
	if labels[unraidIconLabel] != s.Icon || labels[iconLabel] != s.Icon {
		t.Fatalf("icon labels = %+v", labels)
	}

	in := inspectFixture(t)
	in.Config["Labels"].(map[string]any)[unraidIconLabel] = s.Icon
	got := specFromInspect(in, imageFixture(t), nil)
	if got.Icon != s.Icon {
		t.Fatalf("icon from Unraid label = %q", got.Icon)
	}
	for _, c := range got.Configs {
		if c.Key == unraidIconLabel || c.Key == iconLabel {
			t.Fatalf("icon label shown as a setting: %+v", c)
		}
	}

	// A container created before the Unraid label was written still has one.
	delete(in.Config["Labels"].(map[string]any), unraidIconLabel)
	in.Config["Labels"].(map[string]any)[iconLabel] = "https://example.com/older.png"
	if got := specFromInspect(in, imageFixture(t), nil); got.Icon != "https://example.com/older.png" {
		t.Fatalf("fallback icon = %q", got.Icon)
	}

	s.Icon = ""
	applySpec(cfg, host, s, nil)
	labels = cfg["Labels"].(map[string]string)
	if _, ok := labels[unraidIconLabel]; ok {
		t.Fatal("clearing the icon left the label in place")
	}

	bad := spec()
	bad.Icon = "javascript:alert(1)"
	if err := normaliseSpec(&bad); err == nil {
		t.Fatal("a non-http icon was accepted")
	}

	rc := apiContainer{Labels: map[string]string{
		unraidIconLabel: "https://example.com/a.png",
		iconLabel:       "https://example.com/b.png",
	}}
	if icon := toContainer(rc, "").Icon; icon != "https://example.com/a.png" {
		t.Fatalf("list icon = %q", icon)
	}
}

func TestApplyExtraFromTemplate(t *testing.T) {
	tmpl, err := ParseTemplateXML([]byte(plexXML))
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := TemplateToSpec(tmpl, testPaths())
	for i := range spec.Configs {
		if spec.Configs[i].Key == "NEEDED" {
			spec.Configs[i].Value = "x"
		}
	}
	if err := normaliseSpec(&spec); err != nil {
		t.Fatal(err)
	}
	cfg, host := map[string]any{}, map[string]any{}
	applySpec(cfg, host, spec, nil)
	caps, _ := host["CapAdd"].([]string)
	if len(caps) != 1 || caps[0] != "NET_ADMIN" {
		t.Errorf("caps %+v", host["CapAdd"])
	}
	labels := cfg["Labels"].(map[string]string)
	if labels[webUILabel] != spec.WebUI || labels[extraLabel] != spec.ExtraParams {
		t.Errorf("labels %+v", labels)
	}
	// A later edit that drops the capability removes it, and leaves unrelated
	// host settings (a memory limit FELDCORE does not model) in place.
	spec.ExtraParams = ""
	host["Memory"] = "9223372036854775807"
	applySpec(cfg, host, spec, nil)
	if _, ok := host["CapAdd"]; ok {
		t.Errorf("cap-add survived being removed: %+v", host["CapAdd"])
	}
	if host["Memory"] != "9223372036854775807" {
		t.Errorf("memory was clobbered: %+v", host["Memory"])
	}
}
