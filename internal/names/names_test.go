package names

import "testing"

func TestHostname(t *testing.T) {
	if err := Hostname("valheim.home.arpa"); err != nil {
		t.Fatal(err)
	}
	if err := Hostname("app.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "nosuffix", "has space.com", "-bad.com", "http://x.com"} {
		if err := Hostname(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestLocal(t *testing.T) {
	if !Local("valheim.home.arpa") || !Local("a.local") {
		t.Fatal("lan names")
	}
	if Local("app.example.com") {
		t.Fatal("public name marked local")
	}
}

func TestSuggestWebUI(t *testing.T) {
	host, port, from := Suggest("Valheim Server", "http://[IP]:[PORT:2456]/", "")
	if host != "valheim-server.home.arpa" || port != 2456 {
		t.Fatalf("got %s %d", host, port)
	}
	if from == "" {
		t.Fatal("missing explanation")
	}
	host, port, _ = Suggest("plex", "http://[IP]:[PORT:32400]/web", "example.com")
	if host != "plex.example.com" || port != 32400 {
		t.Fatalf("got %s %d", host, port)
	}
	_, port, _ = Suggest("plain", "", "home.arpa")
	if port != 0 {
		t.Fatalf("port %d", port)
	}
}
