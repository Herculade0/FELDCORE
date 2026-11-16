// Package notify sends email when a container stops unexpectedly or a
// FELDCORE update is published. Delivery is SMTP: a generic server, Proton
// Mail Bridge, or a Proton SMTP submission token.
package notify

import (
	"strings"
	"time"
)

const (
	KindContainer = "container.stopped"
	KindUpdate    = "update.available"
	KindTest      = "test"
)

// Event is one notification.
type Event struct {
	Kind  string
	Title string
	Body  string
}

// Container is the slice of a container the watcher compares.
type Container struct {
	Name    string
	Running bool
}

// UnexpectedStops returns events for containers that were running and are
// not any more, ignoring ones the administrator just stopped.
func UnexpectedStops(prev map[string]bool, next []Container, quiet map[string]time.Time, now time.Time) (events []Event, running map[string]bool) {
	running = map[string]bool{}
	seen := map[string]bool{}
	for _, c := range next {
		if c.Name == "" || ignoredContainer(c.Name) {
			continue
		}
		seen[c.Name] = true
		if c.Running {
			running[c.Name] = true
			continue
		}
		if !prev[c.Name] {
			continue
		}
		if until, ok := quiet[c.Name]; ok && now.Before(until) {
			continue
		}
		events = append(events, Event{
			Kind:  KindContainer,
			Title: c.Name + " stopped",
			Body:  c.Name + " stopped unexpectedly. FELDCORE did not stop it.",
		})
	}
	return events, running
}

func ignoredContainer(name string) bool {
	return strings.HasPrefix(name, "feldcore-vpn-") || name == "feldcore-proton-bridge"
}

// UpdateAvailable reports a newer release once. already is the version that
// was already emailed.
func UpdateAvailable(current, latest, already string) (Event, bool) {
	current = strings.TrimSpace(current)
	latest = strings.TrimSpace(latest)
	if latest == "" || latest == current || latest == strings.TrimSpace(already) {
		return Event{}, false
	}
	return Event{
		Kind:  KindUpdate,
		Title: "FELDCORE " + latest + " is available",
		Body:  "A newer FELDCORE release is available (" + latest + "). This server is running " + current + ". FELDCORE does not install it for you.",
	}, true
}
