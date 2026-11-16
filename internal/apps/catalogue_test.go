package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
)

const sampleFeed = `{
  "categories": [
    {"Cat": "MediaApp:", "Des": "Media Applications", "Sub": [{"Cat": "MediaApp:Video", "Des": "Video"}]},
    {"Cat": "Tools:", "Des": "Tools / Utilities", "Sub": [{"Cat": "Tools:Utilities", "Des": "Utilities"}]}
  ],
  "blacklisted": {"dead/app:latest": "gone"},
  "deprecated": {"old/app:latest": "Deprecated application"},
  "applist": [
    {
      "Name": "Plex Media Server",
      "Repository": "lscr.io/linuxserver/plex:latest",
      "Network": "host",
      "Privileged": "false",
      "Overview": "Organises your media.",
      "CategoryList": ["MediaApp-Video"],
      "WebUI": "http://[IP]:[PORT:32400]/web",
      "TemplateURL": "https://example.com/plex.xml",
      "Icon": "https://example.com/plex.png",
      "ExtraParams": "--cap-add=NET_ADMIN",
      "Repo": "linuxserver.io's Repository",
      "downloads": 1000,
      "stars": 12,
      "Config": [
        {"@attributes": {"Name": "Appdata", "Target": "/config", "Default": "/mnt/user/appdata/plex", "Mode": "rw", "Description": "Config", "Type": "Path", "Display": "always", "Required": "true", "Mask": "false"}, "value": ""},
        {"@attributes": {"Name": "Token", "Target": "TOKEN", "Default": "s3cret", "Mode": "", "Description": "Secret", "Type": "Variable", "Display": "always", "Required": "true", "Mask": "true"}, "value": "s3cret"}
      ]
    },
    {
      "Name": "Old Thing",
      "Repository": "old/app:latest",
      "Overview": "Deprecated upstream.",
      "TemplateURL": "https://example.com/old.xml",
      "Repo": "Someone",
      "downloads": 5,
      "Config": {"@attributes": {"Name": "Port", "Target": "80", "Default": "8080", "Mode": "tcp", "Description": "", "Type": "Port", "Display": "advanced", "Required": "true", "Mask": "false"}, "value": ""}
    },
    {
      "Name": "A Plugin",
      "Repository": "https://example.com/plugin.plg",
      "Plugin": true,
      "TemplateURL": "https://example.com/plugin.xml"
    },
    {
      "Name": "Dead",
      "Repository": "dead/app:latest",
      "Blacklist": true,
      "TemplateURL": "https://example.com/dead.xml"
    },
    {
      "Name": "Language pack",
      "TemplateURL": "https://example.com/lang.xml"
    },
    {
      "Name": "Kodbox",
      "Repository": "example/kodbox:latest",
      "Privileged": ["false", "false"],
      "Beta": "true",
      "Overview": "A file manager.",
      "TemplateURL": "https://example.com/kod.xml",
      "Repo": "Author Repository",
      "downloads": 50,
      "CategoryList": ["Tools-Utilities"]
    }
  ]
}`

func TestParseFeedFiltersAndMaps(t *testing.T) {
	parsed, err := parseFeed([]byte(sampleFeed))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.apps) != 3 {
		t.Fatalf("apps %d, want plex, old, kodbox", len(parsed.apps))
	}
	plex := parsed.apps[0].app
	if plex.Name != "Plex Media Server" || plex.Author != "linuxserver.io" || plex.Downloads != 1000 {
		t.Fatalf("plex summary: %+v", plex)
	}
	if plex.Deprecated {
		t.Fatal("plex should not be deprecated")
	}
	for _, rec := range parsed.apps {
		if rec.app.Categories == nil {
			t.Fatalf("%s has a null category list", rec.app.Name)
		}
	}
	if parsed.labels["MediaApp-Video"] != "Media Applications / Video" {
		t.Errorf("label %q", parsed.labels["MediaApp-Video"])
	}
	if parsed.labels["Tools-Utilities"] != "Tools / Utilities" {
		t.Errorf("tools label %q", parsed.labels["Tools-Utilities"])
	}
	spec, notes := docker.TemplateToSpec(parsed.apps[0].tmpl, docker.PathMap{
		Appdata: "/srv/feldcore/appdata", SharesRoot: "/srv/feldcore",
	})
	if spec.Network != "host" || spec.Image != "lscr.io/linuxserver/plex:latest" {
		t.Fatalf("spec %+v", spec)
	}
	var path, token docker.Config
	for _, c := range spec.Configs {
		if c.Type == docker.TypePath {
			path = c
		}
		if c.Key == "TOKEN" {
			token = c
		}
	}
	if path.Host != "/srv/feldcore/appdata/plex" || !path.Required {
		t.Errorf("path %+v", path)
	}
	if !token.Mask || token.Value != "s3cret" {
		t.Errorf("masked token %+v", token)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "/srv/feldcore/appdata") {
		t.Errorf("notes %q", notes)
	}

	// Deprecated apps sort after popular ones. The single-object Config still parses.
	var old App
	for _, rec := range parsed.apps {
		if rec.app.Name == "Old Thing" {
			old = rec.app
			if len(rec.tmpl.Configs) != 1 || rec.tmpl.Configs[0].Display != "advanced" {
				t.Fatalf("single config: %+v", rec.tmpl.Configs)
			}
		}
	}
	if !old.Deprecated {
		t.Fatal("old/app should be deprecated via the feed map")
	}
	if parsed.apps[len(parsed.apps)-1].app.Name != "Old Thing" {
		t.Fatalf("deprecated app not last: %s", parsed.apps[len(parsed.apps)-1].app.Name)
	}
	for _, rec := range parsed.apps {
		if rec.app.Name == "Kodbox" && (!rec.app.Beta || rec.tmpl.Privileged) {
			t.Fatalf("kodbox %+v privileged %v", rec.app, rec.tmpl.Privileged)
		}
	}
}

func TestCatalogueCacheAndOffline(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()

	var saved string
	svc := New(Deps{
		DataDir: dir, SharesRoot: "/srv/feldcore", FeedURL: srv.URL,
		Appdata:    func() string { return saved },
		SetAppdata: func(p string) error { saved = p; return nil },
		SharePaths: func() map[string]string { return map[string]string{"appdata": "/srv/feldcore/appdata"} },
	})
	fixed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	// Opening the page must not download.
	cat := svc.Catalogue()
	if hits != 0 || !cat.Missing || len(cat.Apps) != 0 {
		t.Fatalf("catalogue fetched or was not missing: hits %d %+v", hits, cat.Message)
	}
	if cat.Appdata != "/srv/feldcore/appdata" || cat.AppdataCustom {
		t.Fatalf("default appdata should follow the appdata share: %+v", cat)
	}

	cat = svc.Refresh(context.Background())
	if hits != 1 || cat.Offline || cat.Missing || len(cat.Apps) != 3 {
		t.Fatalf("refresh: hits %d offline %v missing %v apps %d msg %q", hits, cat.Offline, cat.Missing, len(cat.Apps), cat.Message)
	}
	if cat.UpdatedAt == "" {
		t.Fatal("missing updatedAt")
	}

	// A second page load uses the cache, including a brand new process.
	_ = svc.Catalogue()
	if hits != 1 {
		t.Fatalf("catalogue read hit the network (%d)", hits)
	}
	svc2 := New(Deps{DataDir: dir, SharesRoot: "/srv/feldcore", FeedURL: srv.URL})
	svc2.now = func() time.Time { return fixed.Add(48 * time.Hour) }
	cat = svc2.Catalogue()
	if hits != 1 || len(cat.Apps) != 3 || !cat.Stale {
		t.Fatalf("reloaded cache: hits %d apps %d stale %v", hits, len(cat.Apps), cat.Stale)
	}

	srv.Close()
	cat = svc2.Refresh(context.Background())
	if !cat.Offline || len(cat.Apps) != 3 || cat.Message == "" {
		t.Fatalf("offline refresh should keep the saved catalogue: %+v", cat.Message)
	}

	empty := New(Deps{DataDir: t.TempDir(), SharesRoot: "/srv/feldcore", FeedURL: srv.URL})
	cat = empty.Refresh(context.Background())
	if !cat.Offline || !cat.Missing || len(cat.Apps) != 0 {
		t.Fatalf("offline with no cache: %+v apps %d", cat.Message, len(cat.Apps))
	}

	st, err := svc.SetAppdata("/var/lib/apps")
	if err != nil || !st.AppdataCustom || st.Appdata != "/var/lib/apps" {
		t.Fatalf("set appdata: %v %+v", err, st)
	}
	if _, err := svc.SetAppdata("relative"); err == nil {
		t.Fatal("relative appdata accepted")
	}
	id := catIDOf(t, svc, "Plex Media Server")
	_, spec, _, err := svc.Spec(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.Configs {
		if c.Type == docker.TypePath && c.Host != "/var/lib/apps/plex" {
			t.Errorf("custom appdata not applied: %+v", c)
		}
	}
	if _, _, _, err := svc.Spec("nope"); err != ErrNotFound {
		t.Fatalf("missing app: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, feedFileName)); err != nil {
		t.Fatal(err)
	}
}

func catIDOf(t *testing.T, svc *Service, name string) string {
	t.Helper()
	for _, a := range svc.Catalogue().Apps {
		if a.Name == name {
			return a.ID
		}
	}
	t.Fatalf("app %q not in catalogue", name)
	return ""
}

func TestRealCommunityApplicationsFeed(t *testing.T) {
	path := os.Getenv("FELDCORE_CA_FEED")
	if path == "" {
		t.Skip("set FELDCORE_CA_FEED to parse a real catalogue")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseFeed(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.apps) < 1000 {
		t.Fatalf("only %d docker apps", len(parsed.apps))
	}
	t.Logf("docker apps %d categories %d", len(parsed.apps), len(parsed.categories))
}
