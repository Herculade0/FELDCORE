package health

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/store"
)

// Service collects health on a timer and keeps the latest report.
type Service struct {
	Dir        string
	Store      *store.Store
	Bus        *events.Bus
	Log        *slog.Logger
	Now        func() time.Time
	Interval   time.Duration
	Thresholds func() store.HealthThresholds
	Scrub      func(string) string

	Host       func(context.Context) (HostSample, error)
	Disks      func(context.Context) ([]Space, error)
	SMART      func(context.Context) ([]DiskSMART, error)
	Temps      func(context.Context) ([]Temp, error)
	Units      func(context.Context) ([]Unit, error)
	Certs      func(context.Context) ([]Cert, error)
	Containers func(context.Context) ([]ContainerSnap, error)
	VMs        func(context.Context) ([]VMSnap, error)
	Tunnels    func(context.Context) ([]TunnelSnap, error)

	mu       sync.Mutex
	report   Report
	series   *Series
	prev     map[string]Level
	restarts map[string]*restartHist
}

type restartHist struct {
	Count int
	At    time.Time
	Bumps []time.Time
}

// New prepares collectors that read this machine. Container, VM, and tunnel
// sources are left unset until the daemon wires them, because those live in
// other packages.
func New(dir string, log *slog.Logger) *Service {
	s := &Service{
		Dir:      dir,
		Log:      log,
		Interval: time.Minute,
		series:   OpenSeries(seriesPath(dir)),
		prev:     map[string]Level{},
		restarts: map[string]*restartHist{},
		Host:     ReadHost,
		Disks:    ReadSpace,
		SMART:    ReadSMART,
		Temps:    ReadTemps,
		Units:    ReadUnits,
		Certs:    func(ctx context.Context) ([]Cert, error) { return ReadCerts(ctx, nil) },
	}
	s.report = Report{Overall: Unknown, Summary: "No health check yet.", Checks: []Check{}}
	return s
}

func seriesPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "health-series.json")
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) thresholds() Thresholds {
	if s.Thresholds != nil {
		return Effective(s.Thresholds())
	}
	if s.Store != nil {
		return Effective(s.Store.Snapshot().Health.Thresholds)
	}
	return Defaults()
}

// ThresholdSettings is the marks the page edits, with defaults filled in.
func (s *Service) ThresholdSettings() store.HealthThresholds {
	return ToStore(s.thresholds())
}

// SaveThresholds stores a valid set. Empty fields keep the defaults.
func (s *Service) SaveThresholds(in store.HealthThresholds) error {
	if s == nil || s.Store == nil {
		return &InputError{Msg: "Health monitoring is not available."}
	}
	if err := Validate(in); err != nil {
		return err
	}
	stored := ToStore(Effective(in))
	return s.Store.Update(func(st *store.State) error {
		st.Health.Thresholds = stored
		return nil
	})
}

func (s *Service) scrub(msg string) string {
	msg = strings.TrimSpace(msg)
	if s.Scrub != nil {
		msg = s.Scrub(msg)
	}
	return oneLine(msg, 300)
}

// Run collects immediately, then on the interval, until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	s.Collect(ctx)
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.Collect(ctx)
		}
	}
}

// Report returns the latest checks.
func (s *Service) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyReport(s.report)
}

// History returns graph points since the given time, capped for the page.
func (s *Service) History(since time.Time, max int) []Point {
	if s.series == nil {
		return []Point{}
	}
	return s.series.Since(since, max)
}

// Collect runs one pass. It is safe to call from a test.
func (s *Service) Collect(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil && s.Log != nil {
			s.Log.Error("health check failed")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	th := s.thresholds()
	now := s.now()
	snap := Snapshot{Asked: map[string]bool{}}

	if s.Host != nil {
		h, err := s.Host(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("cpu", "Processor", err, s.scrub))
		} else {
			snap.Host = &h
		}
	}
	if s.Disks != nil {
		disks, err := s.Disks(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("space", "Storage", err, s.scrub))
		} else {
			snap.Disks = disks
		}
	}
	var smartTemps []Temp
	if s.SMART != nil {
		disks, err := s.SMART(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("smart", "SMART", err, s.scrub))
		} else {
			snap.SMART = disks
			for _, d := range disks {
				if d.TempC == nil || d.Asleep {
					continue
				}
				smartTemps = append(smartTemps, Temp{Name: d.Device, Kind: "disk", Celsius: *d.TempC})
			}
		}
	}
	if s.Temps != nil {
		temps, err := s.Temps(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("temp", "Temperatures", err, s.scrub))
		} else {
			snap.Temps = append(temps, smartTemps...)
		}
	} else {
		snap.Temps = smartTemps
	}
	if s.Units != nil {
		snap.Asked["systemd"] = true
		units, err := s.Units(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("systemd", "Services", err, s.scrub))
			snap.Asked["systemd"] = false
		} else {
			snap.Units = units
		}
	}
	if s.Certs != nil {
		snap.Asked["certs"] = true
		certs, err := s.Certs(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("cert", "Certificates", err, s.scrub))
			snap.Asked["certs"] = false
		} else {
			snap.Certs = certs
		}
	}
	if s.Containers != nil {
		snap.Asked["containers"] = true
		list, err := s.Containers(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("container", "Containers", err, s.scrub))
			snap.Asked["containers"] = false
		} else {
			snap.Containers = s.trackRestarts(list, now, th.RestartWindow)
		}
	}
	if s.VMs != nil {
		snap.Asked["vms"] = true
		list, err := s.VMs(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("vm", "Virtual machines", err, s.scrub))
			snap.Asked["vms"] = false
		} else {
			snap.VMs = list
		}
	}
	if s.Tunnels != nil {
		snap.Asked["tunnels"] = true
		list, err := s.Tunnels(ctx)
		if err != nil {
			snap.Notes = append(snap.Notes, note("tunnel", "Tunnels", err, s.scrub))
			snap.Asked["tunnels"] = false
		} else {
			for i := range list {
				list[i].Detail = s.scrub(list[i].Detail)
			}
			snap.Tunnels = list
		}
	}
	for i := range snap.Notes {
		snap.Notes[i].Summary = s.scrub(snap.Notes[i].Summary)
	}

	report := Eval(snap, th, now)
	s.mu.Lock()
	prev := s.prev
	s.report = report
	s.prev = levelsOf(report)
	series := s.series
	s.mu.Unlock()

	if series != nil && snap.Host != nil {
		series.Add(pointFrom(now, snap))
	}
	s.emit(prev, report)
}

func note(category, name string, err error, scrub func(string) string) Check {
	msg := err.Error()
	var u *UnavailableError
	if errors.As(err, &u) && u.Msg != "" {
		msg = u.Msg
	}
	if scrub != nil {
		msg = scrub(msg)
	}
	return Check{ID: category + ":unavailable", Category: category, Name: name, Level: Unknown, Summary: msg}
}

func (s *Service) trackRestarts(list []ContainerSnap, now time.Time, window time.Duration) []ContainerSnap {
	if window <= 0 {
		window = 10 * time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restarts == nil {
		s.restarts = map[string]*restartHist{}
	}
	seen := map[string]bool{}
	for i := range list {
		c := &list[i]
		c.Health = normaliseHealth(c.State, c.Health)
		seen[c.Name] = true
		h := s.restarts[c.Name]
		if h == nil {
			h = &restartHist{}
			s.restarts[c.Name] = h
		}
		// RestartCount is carried in RestartsInWindow by the docker adapter
		// before this pass rewrites it. The field is the raw counter on the
		// way in and the count inside the window on the way out.
		count := c.RestartsInWindow
		if h.At.IsZero() {
			h.Count = count
			h.At = now
		} else if count > h.Count && now.Sub(h.At) < 2*window {
			delta := count - h.Count
			if delta > 50 {
				delta = 50
			}
			for n := 0; n < delta; n++ {
				h.Bumps = append(h.Bumps, now)
			}
			h.Count = count
			h.At = now
		} else {
			h.Count = count
			h.At = now
		}
		cut := now.Add(-window)
		kept := h.Bumps[:0]
		for _, ts := range h.Bumps {
			if !ts.Before(cut) {
				kept = append(kept, ts)
			}
		}
		h.Bumps = kept
		c.RestartsInWindow = len(h.Bumps)
	}
	for name := range s.restarts {
		if !seen[name] {
			delete(s.restarts, name)
		}
	}
	return list
}

// normaliseHealth prefers an explicit health string, then the status text
// Docker puts in the state field.
func normaliseHealth(state, health string) string {
	h := strings.ToLower(strings.TrimSpace(health))
	switch h {
	case "healthy", "unhealthy", "starting", "restarting", "none", "":
	default:
		h = ""
	}
	blob := strings.ToLower(state + " " + health)
	switch {
	case strings.Contains(blob, "unhealthy"):
		return "unhealthy"
	case strings.Contains(blob, "health: starting"), h == "starting":
		return "starting"
	case state == "restarting" || strings.Contains(blob, "restarting"):
		return "restarting"
	case strings.Contains(blob, "healthy"):
		return "healthy"
	default:
		if h == "" {
			return "none"
		}
		return h
	}
}

func levelsOf(r Report) map[string]Level {
	out := make(map[string]Level, len(r.Checks))
	for _, c := range r.Checks {
		out[c.ID] = c.Level
	}
	return out
}

func pointFrom(now time.Time, snap Snapshot) Point {
	p := Point{T: now.Unix(), Disk: map[string]float64{}, Temp: map[string]float64{}}
	if snap.Host != nil {
		p.CPU = round1(snap.Host.CPUPercent)
		p.Mem = round1(snap.Host.MemPercent)
		cores := snap.Host.Cores
		if cores < 1 {
			cores = 1
		}
		p.Load = round1(snap.Host.Load1 / float64(cores))
	}
	n := 0
	for _, d := range snap.Disks {
		if d.Name == "" || n >= 8 {
			break
		}
		p.Disk[d.Name] = round1(d.UsedPercent)
		n++
	}
	if len(p.Disk) == 0 {
		p.Disk = nil
	}
	n = 0
	for _, t := range snap.Temps {
		if t.Name == "" || n >= 8 {
			break
		}
		p.Temp[t.Kind+":"+t.Name] = round1(t.Celsius)
		n++
	}
	if len(p.Temp) == 0 {
		p.Temp = nil
	}
	return p
}

func (s *Service) emit(prev map[string]Level, report Report) {
	if s.Bus == nil {
		return
	}
	current := map[string]Level{}
	var pending []events.Event
	for _, c := range report.Checks {
		current[c.ID] = c.Level
		old := prev[c.ID]
		if c.Level != Warn && c.Level != Crit {
			continue
		}
		if old == c.Level {
			continue
		}
		pending = append(pending, events.Event{
			Name:     eventName(c),
			Severity: sev(c.Level),
			Title:    c.Name,
			Body:     c.Summary,
			Source:   "health",
			Key:      eventName(c) + ":" + c.ID,
			Fields:   map[string]string{"check": c.ID, "level": string(c.Level)},
		})
	}
	for id, old := range prev {
		if old != Warn && old != Crit {
			continue
		}
		if current[id] == OK {
			pending = append(pending, events.Event{
				Name:     "health.recovered",
				Severity: events.Info,
				Title:    id + " recovered",
				Body:     id + " is healthy again.",
				Source:   "health",
				Key:      "health.recovered:" + id,
				Fields:   map[string]string{"check": id},
			})
		}
	}
	if len(pending) > 15 {
		extra := len(pending) - 14
		pending = pending[:14]
		pending = append(pending, events.Event{
			Name: "health.summary", Severity: events.Warning, Source: "health",
			Title: "More health checks changed",
			Body:  strings.TrimSpace(strings.ReplaceAll(report.Summary, ".", "") + ". " + itoa(extra) + " further changes were not sent individually."),
			Key:   "health.summary",
		})
	}
	for _, ev := range pending {
		s.Bus.Publish(ev)
	}
}

func eventName(c Check) string {
	switch c.Category {
	case "cpu":
		return "health.cpu"
	case "memory":
		return "health.memory"
	case "load":
		return "health.load"
	case "temp":
		return "health.temperature"
	case "space":
		return "health.disk"
	case "smart":
		return "health.smart"
	case "container":
		if strings.Contains(c.ID, ":restart") {
			return "health.container.restart"
		}
		return "health.container"
	case "vm":
		return "health.vm"
	case "systemd":
		return "health.systemd"
	case "cert":
		return "health.cert"
	case "tunnel":
		return "health.tunnel"
	default:
		return "health." + c.Category
	}
}

func sev(l Level) events.Severity {
	if l == Crit {
		return events.Critical
	}
	return events.Warning
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func copyReport(r Report) Report {
	r.Checks = append([]Check(nil), r.Checks...)
	for i := range r.Checks {
		r.Checks[i].Attributes = append([]Attribute(nil), r.Checks[i].Attributes...)
	}
	return r
}
