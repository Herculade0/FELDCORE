package notify

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

const secretPassword = "mail-password"

// Box stores the SMTP password or token.
type Box interface {
	Put(name, value string) error
	Get(name string) (string, bool, error)
}

// BridgeRuntime starts the optional Proton Mail Bridge container.
type BridgeRuntime interface {
	Apply(ctx context.Context, dataDir string) error
	Stop(ctx context.Context, name string) error
	Running(ctx context.Context, name string) (bool, error)
}

// Containers lists container run state for the unexpected-stop check.
type Containers func(ctx context.Context) ([]Container, error)

// Latest returns the newest published version, or "" when the check fails.
type Latest func(ctx context.Context) (string, error)

// Service sends notification email.
type Service struct {
	Store    *store.Store
	Secrets  Box
	Bridge   BridgeRuntime
	DataDir  string
	Version  string
	Log      *slog.Logger
	Now      func() time.Time
	SendMail func(ctx context.Context, m Message) error
	// Bus receives container and update events when set. The dispatcher
	// decides which channels to use. Without a bus, Emit sends mail directly,
	// which is how this service behaved before the event bus existed.
	Bus *events.Bus

	mu    sync.Mutex
	quiet map[string]time.Time
	seen  map[string]bool
}

// Settings is the form. Password is write-only.
type Settings struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	From            string `json:"from"`
	To              string `json:"to"`
	TLS             string `json:"tls"`
	Password        string `json:"password,omitempty"`
	BridgeManaged   bool   `json:"bridgeManaged"`
	BridgeHost      string `json:"bridgeHost"`
	BridgePort      int    `json:"bridgePort"`
	OnContainerStop bool   `json:"onContainerStop"`
	OnUpdate        bool   `json:"onUpdate"`
}

// View is the page's read model.
type View struct {
	Enabled         bool                  `json:"enabled"`
	Configured      bool                  `json:"configured"`
	Mode            string                `json:"mode"`
	Host            string                `json:"host"`
	Port            int                   `json:"port"`
	Username        string                `json:"username"`
	From            string                `json:"from"`
	To              string                `json:"to"`
	TLS             string                `json:"tls"`
	HasPassword     bool                  `json:"hasPassword"`
	BridgeManaged   bool                  `json:"bridgeManaged"`
	BridgeHost      string                `json:"bridgeHost"`
	BridgePort      int                   `json:"bridgePort"`
	BridgeRunning   bool                  `json:"bridgeRunning"`
	OnContainerStop bool                  `json:"onContainerStop"`
	OnUpdate        bool                  `json:"onUpdate"`
	Events          []store.NotifyEvent   `json:"events"`
	Channels        []store.NotifyChannel `json:"channels"`
	Routes          []store.NotifyRoute   `json:"routes"`
	Quiet           store.NotifyQuiet     `json:"quiet"`
	DedupeSeconds   int                   `json:"dedupeSeconds"`
	RatePerHour     int                   `json:"ratePerHour"`
	InAppEnabled    bool                  `json:"inAppEnabled"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// View returns settings and the recent events.
func (s *Service) View(ctx context.Context) View {
	n := s.Store.Snapshot().Notify
	v := View{
		Enabled: n.Enabled, Mode: n.Mode, Host: n.Host, Port: n.Port,
		Username: n.Username, From: n.From, To: n.To, TLS: n.TLS,
		HasPassword: n.HasPassword, BridgeManaged: n.BridgeManaged,
		BridgeHost: n.BridgeHost, BridgePort: n.BridgePort,
		OnContainerStop: n.OnContainerStop, OnUpdate: n.OnUpdate,
		Events: n.Events,
	}
	if v.Events == nil {
		v.Events = []store.NotifyEvent{}
	}
	v.Configured = n.To != "" || n.HasPassword || n.Enabled
	if v.Mode == "" {
		v.Mode = ModeSMTP
	}
	if !v.Configured {
		v.OnContainerStop = true
		v.OnUpdate = true
		v.TLS = "starttls"
		v.Port = 587
	}
	if v.BridgeHost == "" {
		v.BridgeHost = BridgeHost
	}
	if v.BridgePort == 0 {
		v.BridgePort = BridgePort
	}
	if s.Bridge != nil && n.BridgeManaged {
		running, err := s.Bridge.Running(ctx, BridgeName)
		if err == nil {
			v.BridgeRunning = running
		}
	}
	decorate(&v, n)
	return v
}

// Save stores the mail target. An empty password keeps the stored one.
func (s *Service) Save(ctx context.Context, in Settings) error {
	mode := in.Mode
	if mode == "" {
		mode = ModeSMTP
	}
	switch mode {
	case ModeSMTP, ModeBridge, ModeToken:
	default:
		return input("Choose generic SMTP, Proton Mail Bridge, or a Proton SMTP token.")
	}
	n := s.Store.Snapshot().Notify
	if pw := in.Password; pw != "" {
		if err := s.Secrets.Put(secretPassword, pw); err != nil {
			return op("Could not store the mail password.")
		}
		n.HasPassword = true
	}
	if in.Enabled && !n.HasPassword {
		return input("Enter the SMTP password or token before turning notifications on.")
	}
	if in.Enabled && strings.TrimSpace(in.To) == "" {
		return input("Enter the address that should receive notifications.")
	}
	n.Enabled = in.Enabled
	n.Mode = mode
	n.Host = strings.TrimSpace(in.Host)
	n.Port = in.Port
	n.Username = strings.TrimSpace(in.Username)
	n.From = strings.TrimSpace(in.From)
	n.To = strings.TrimSpace(in.To)
	n.TLS = in.TLS
	n.BridgeManaged = in.BridgeManaged
	n.BridgeHost = strings.TrimSpace(in.BridgeHost)
	n.BridgePort = in.BridgePort
	n.OnContainerStop = in.OnContainerStop
	n.OnUpdate = in.OnUpdate
	if err := s.Store.Update(func(st *store.State) error {
		n.Events = st.Notify.Events
		n.NotifiedVersion = st.Notify.NotifiedVersion
		n.Channels = st.Notify.Channels
		n.Routes = st.Notify.Routes
		n.Quiet = st.Notify.Quiet
		n.DedupeSeconds = st.Notify.DedupeSeconds
		n.RatePerHour = st.Notify.RatePerHour
		n.InAppSet = st.Notify.InAppSet
		n.InAppEnabled = st.Notify.InAppEnabled
		st.Notify = n
		return nil
	}); err != nil {
		return err
	}
	if mode == ModeBridge && in.BridgeManaged && in.Enabled && s.Bridge != nil {
		if err := s.Bridge.Apply(ctx, s.DataDir); err != nil {
			return op("Proton Mail Bridge did not start. You can point FELDCORE at a Bridge you already run instead.")
		}
	}
	if mode == ModeBridge && in.BridgeManaged && !in.Enabled && s.Bridge != nil {
		_ = s.Bridge.Stop(ctx, BridgeName)
	}
	return nil
}

// Test sends one message. It works before notifications are turned on, so
// the settings can be checked first. The caller passes enabled from the
// saved record; a test does not require the master switch.
func (s *Service) Test(ctx context.Context) error {
	return s.deliver(ctx, Event{
		Kind:  KindTest,
		Title: "FELDCORE test message",
		Body:  "This is a test message from FELDCORE. Notifications are working.",
	}, true)
}

// Emit sends an event when notifications are on.
func (s *Service) Emit(ctx context.Context, ev Event) {
	n := s.Store.Snapshot().Notify
	if !n.Enabled {
		return
	}
	switch ev.Kind {
	case KindContainer:
		if !n.OnContainerStop {
			return
		}
	case KindUpdate:
		if !n.OnUpdate {
			return
		}
	}
	_ = s.deliver(ctx, ev, false)
}

// ExpectQuiet ignores an unexpected-stop for this container for a few minutes.
// Call it before a stop, restart, or edit that FELDCORE itself is doing.
func (s *Service) ExpectQuiet(name string) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "/")
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quiet == nil {
		s.quiet = map[string]time.Time{}
	}
	s.quiet[name] = s.now().Add(2 * time.Minute)
}

// Watch polls containers and, less often, the latest release.
func (s *Service) Watch(ctx context.Context, containers Containers, latest Latest, updatesEnabled func() bool) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	var lastUpdate time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			// Poll even when mail is off. The bus lets the bell and other
			// channels hear about a stop; mail still respects its own switches.
			if containers != nil {
				s.pollContainers(ctx, containers, now)
			}
			if latest != nil && updatesEnabled != nil && updatesEnabled() && now.Sub(lastUpdate) > 6*time.Hour {
				lastUpdate = now
				s.pollUpdate(ctx, latest)
			}
		}
	}
}

func (s *Service) pollContainers(ctx context.Context, containers Containers, now time.Time) {
	list, err := containers(ctx)
	if err != nil {
		return
	}
	s.mu.Lock()
	events, running := UnexpectedStops(s.seen, list, s.quiet, now)
	s.seen = running
	s.mu.Unlock()
	for _, ev := range events {
		s.emit(ctx, ev)
	}
}

func (s *Service) pollUpdate(ctx context.Context, latest Latest) {
	ver, err := latest(ctx)
	if err != nil {
		return
	}
	ev, ok := UpdateAvailable(s.Version, ver, s.Store.Snapshot().Notify.NotifiedVersion)
	if !ok {
		return
	}
	s.emit(ctx, ev)
	_ = s.Store.Update(func(st *store.State) error {
		st.Notify.NotifiedVersion = ver
		return nil
	})
}

// emit publishes when a bus is attached, and otherwise sends mail the way
// this service did before routing existed.
func (s *Service) emit(ctx context.Context, ev Event) {
	if s.Bus != nil {
		sev := events.Warning
		if ev.Kind == KindUpdate || ev.Kind == KindTest {
			sev = events.Info
		}
		s.Bus.Publish(events.Event{
			Name: ev.Kind, Severity: sev, Title: ev.Title, Body: ev.Body,
			Source: "feldcore", Key: ev.Kind + ":" + ev.Title,
		})
		return
	}
	s.Emit(ctx, ev)
}

func (s *Service) deliver(ctx context.Context, ev Event, force bool) error {
	n := s.Store.Snapshot().Notify
	if !force && !n.Enabled {
		return nil
	}
	pass, _, err := s.Secrets.Get(secretPassword)
	if err != nil || pass == "" {
		return s.note(ev, false, "No mail password is stored.")
	}
	msg, err := Resolve(n, pass, ev.Title, ev.Body)
	if err != nil {
		return s.note(ev, false, err.Error())
	}
	send := s.SendMail
	if send == nil {
		send = Send
	}
	if err := send(ctx, msg); err != nil {
		safe := secrets.Redact(err.Error(), pass)
		return s.note(ev, false, safe)
	}
	return s.note(ev, true, "")
}

func (s *Service) note(ev Event, sent bool, errMsg string) error {
	item := store.NotifyEvent{
		Kind: ev.Kind, Title: ev.Title, Body: ev.Body, At: s.now(), Sent: sent, Error: errMsg,
	}
	_ = s.Store.Update(func(st *store.State) error {
		st.Notify.Events = append(st.Notify.Events, item)
		if len(st.Notify.Events) > 30 {
			st.Notify.Events = st.Notify.Events[len(st.Notify.Events)-30:]
		}
		return nil
	})
	if errMsg != "" {
		return op(errMsg)
	}
	return nil
}
