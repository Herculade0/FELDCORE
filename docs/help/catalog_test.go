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
		"backup-data", "get-alerts", "citizen-id", "backups", "notifications-health",
		"compose-stacks", "first-run-wizard", "disk-management", "updates-rollback",
		"internal-affairs",
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

	for _, page := range []string{"dashboard", "shares", "docker", "apps", "vms", "audit", "settings"} {
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
