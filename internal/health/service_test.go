package health

import (
	"context"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
)

func TestCollectEmitsOnTransition(t *testing.T) {
	bus := events.New()
	var names []string
	bus.Subscribe(func(e events.Event) { names = append(names, e.Name) })
	dir := t.TempDir()
	s := &Service{
		Bus:    bus,
		series: OpenSeries(""),
		prev:   map[string]Level{},
		Host: func(context.Context) (HostSample, error) {
			return HostSample{CPUPercent: 99, MemPercent: 10, Load1: 0.2, Cores: 2}, nil
		},
		SMART: func(context.Context) ([]DiskSMART, error) {
			return nil, Unavailable("smartctl is not installed, so disk health is not being read.")
		},
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	s.series = OpenSeries(dir + "/health-series.json")
	s.Collect(context.Background())
	rep := s.Report()
	if rep.Overall != Crit {
		t.Fatalf("report %+v", rep)
	}
	if len(names) != 1 || names[0] != "health.cpu" {
		t.Fatalf("events %v", names)
	}
	s.Collect(context.Background())
	if len(names) != 1 {
		t.Fatalf("repeat should not emit again: %v", names)
	}
	s.Host = func(context.Context) (HostSample, error) {
		return HostSample{CPUPercent: 5, MemPercent: 10, Load1: 0.2, Cores: 2}, nil
	}
	s.Collect(context.Background())
	if len(names) != 2 || names[1] != "health.recovered" {
		t.Fatalf("events %v", names)
	}
	points := s.History(time.Time{}, 10)
	if len(points) != 3 || points[0].CPU != 99 {
		t.Fatalf("points %+v", points)
	}
}

func TestRestartWindow(t *testing.T) {
	s := &Service{restarts: map[string]*restartHist{}}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	first := s.trackRestarts([]ContainerSnap{{Name: "plex", State: "running", RestartsInWindow: 2}}, now, 10*time.Minute)
	if first[0].RestartsInWindow != 0 {
		t.Fatalf("baseline should not alert: %+v", first[0])
	}
	next := s.trackRestarts([]ContainerSnap{{Name: "plex", State: "running", RestartsInWindow: 6}}, now.Add(time.Minute), 10*time.Minute)
	if next[0].RestartsInWindow != 4 {
		t.Fatalf("delta %+v", next[0])
	}
	later := s.trackRestarts([]ContainerSnap{{Name: "plex", State: "running", RestartsInWindow: 6}}, now.Add(30*time.Minute), 10*time.Minute)
	if later[0].RestartsInWindow != 0 {
		t.Fatalf("old bumps should expire: %+v", later[0])
	}
}

func TestNormaliseHealth(t *testing.T) {
	if normaliseHealth("running", "Up 2 hours (unhealthy)") != "unhealthy" {
		t.Fatal("unhealthy")
	}
	if normaliseHealth("restarting", "Restarting") != "restarting" {
		t.Fatal("restarting")
	}
	if normaliseHealth("running", "healthy") != "healthy" {
		t.Fatal("healthy")
	}
}
