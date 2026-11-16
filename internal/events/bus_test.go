package events

import (
	"sync"
	"testing"
	"time"
)

func TestPublishDeliversACopy(t *testing.T) {
	b := New()
	var got Event
	b.Subscribe(func(e Event) { got = e })
	fields := map[string]string{"disk": "sda"}
	b.Publish(Event{
		Name: "backup.failed", Severity: "nope", Title: " Backup failed ",
		Body: "the archive was refused", Source: "backup", Fields: fields,
	})
	if got.Name != "backup.failed" || got.Severity != Info || got.Title != "Backup failed" {
		t.Fatalf("event %+v", got)
	}
	fields["disk"] = "changed"
	if got.Fields["disk"] != "sda" {
		t.Fatal("subscriber must not share the caller's map")
	}
	if got.At.IsZero() {
		t.Fatal("missing time")
	}
}

func TestUnsubscribeAndPanicIsolation(t *testing.T) {
	b := New()
	stop := b.Subscribe(func(Event) { panic("subscriber bug") })
	var n int
	b.Subscribe(func(Event) { n++ })
	b.Publish(Event{Name: "health.disk", Severity: Warning})
	if n != 1 {
		t.Fatalf("n %d", n)
	}
	stop()
	b.Publish(Event{Name: "health.disk", Severity: Warning})
	if n != 2 {
		t.Fatalf("after unsubscribe n %d", n)
	}
}

func TestDefaultBus(t *testing.T) {
	SetDefault(nil)
	t.Cleanup(func() { SetDefault(nil) })
	Publish(Event{Name: "backup.failed"}) // no bus yet
	b := New()
	SetDefault(b)
	var mu sync.Mutex
	var names []string
	b.Subscribe(func(e Event) {
		mu.Lock()
		names = append(names, e.Name)
		mu.Unlock()
	})
	Publish(Event{Name: "backup.failed", Severity: Critical, At: time.Unix(10, 0)})
	mu.Lock()
	defer mu.Unlock()
	if len(names) != 1 || names[0] != "backup.failed" {
		t.Fatalf("names %v", names)
	}
	if Rank(Critical) <= Rank(Warning) || Rank(Info) <= 0 {
		t.Fatal("rank")
	}
}
