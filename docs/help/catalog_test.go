package help

import (
	"strings"
	"testing"
)

func TestCatalog(t *testing.T) {
	cat, err := load()
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]Topic{}
	for _, topic := range cat.Topics {
		byID[topic.ID] = topic
	}

	guides := []string{
		"getting-started", "expose-site", "backup-data", "run-docker", "setup-vm",
		"pass-through-gpu", "connect-vpn", "share-files", "secure-your-box",
		"get-alerts", "troubleshooting", "ai-agents",
	}
	for _, id := range guides {
		topic, ok := byID[id]
		if !ok || topic.Kind != "guide" {
			t.Fatalf("%s should be a guide", id)
		}
	}

	soon := []string{
		"internal-affairs",
	}
	ready := []string{
		"backup-data", "get-alerts", "citizen-id", "backups", "notifications-health",
		"compose-stacks", "first-run-wizard", "disk-management", "updates-rollback",
	}
	for _, id := range ready {
		topic, ok := byID[id]
		if !ok || topic.ComingSoon {
			t.Fatalf("%s should be a finished article", id)
		}
	}
	for _, id := range soon {
		topic, ok := byID[id]
		if !ok || !topic.ComingSoon {
			t.Fatalf("%s should be marked coming soon", id)
		}
	}

	agents := byID["ai-agents"]
	for _, needle := range []string{
		"[[route:settings.agent|",
		"[[route:audit|",
		"[[route:internal-affairs|",
	} {
		if !strings.Contains(agents.Body, needle) {
			t.Fatalf("AI Agents guide missing %s", needle)
		}
	}
	if cat.Routes["internal-affairs"].Available {
		t.Fatal("internal-affairs route should be unavailable")
	}
	if cat.Routes["internal-affairs"].Help != "internal-affairs" {
		t.Fatal("internal-affairs route should open its article")
	}
	if !cat.Routes["settings.citizen"].Available || cat.Routes["settings.citizen"].Tab != "citizen" {
		t.Fatal("Citizen ID should open the settings tab")
	}
	if !cat.Routes["storage"].Available || !cat.Routes["backups"].Available || !cat.Routes["stacks"].Available || !cat.Routes["health"].Available || !cat.Routes["updates"].Available {
		t.Fatal("storage, backups, stacks, health, and updates routes should be available")
	}

	for _, page := range []string{"dashboard", "storage", "shares", "docker", "stacks", "apps", "vms", "backups", "health", "audit", "updates", "settings"} {
		found := false
		for _, topic := range cat.Topics {
			if topic.Page == page && !topic.ComingSoon {
				found = true
			}
		}
		if !found {
			t.Fatalf("no ready article for page %s", page)
		}
	}
}

func TestUnknownRouteRejected(t *testing.T) {
	err := checkBody("sample", "[[route:not-a-page|Open it]]", map[string]Route{}, map[string]bool{})
	if err == nil {
		t.Fatal("expected an unknown route to fail")
	}
}
