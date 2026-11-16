package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

// SecretBox is the encrypted store. Values are used only to redact text.
type SecretBox interface {
	Put(name, value string) error
	Get(name string) (string, bool, error)
	Has(name string) bool
	Values() []string
}

// Dispatcher routes bus events onto channels.
type Dispatcher struct {
	Store   *store.Store
	Secrets SecretBox
	Mail    *Service
	Inbox   *Inbox
	Log     *slog.Logger
	Now     func() time.Time
	Client  *http.Client

	mu    sync.Mutex
	rate  map[string][]time.Time
	seen  map[string]seenItem
	dpath string
}

type seenItem struct {
	At       time.Time `json:"at"`
	Severity string    `json:"severity"`
}

// NewDispatcher returns a dispatcher. dedupePath remembers recent sends
// across a restart so a reboot does not repeat a message inside the window.
func NewDispatcher(store *store.Store, secrets SecretBox, mail *Service, inbox *Inbox, dedupePath string, log *slog.Logger) *Dispatcher {
	d := &Dispatcher{
		Store: store, Secrets: secrets, Mail: mail, Inbox: inbox, Log: log,
		rate: map[string][]time.Time{}, seen: map[string]seenItem{}, dpath: dedupePath,
	}
	d.loadDedupe()
	return d
}

func (d *Dispatcher) now() time.Time {
	if d != nil && d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Dispatcher) client() *http.Client {
	if d != nil && d.Client != nil {
		return d.Client
	}
	return &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return op("The notification server tried to redirect the message.")
		},
	}
}

// Handle is the bus subscription.
func (d *Dispatcher) Handle(ev events.Event) {
	if d == nil || d.Store == nil {
		return
	}
	ev = d.clean(ev)
	if ev.Name == "" {
		return
	}
	n := d.Store.Snapshot().Notify
	now := d.now()
	quiet := inQuiet(n.Quiet, now)
	for _, id := range targets(n, ev) {
		if err := d.send(context.Background(), n, id, ev, false, quiet, now); err != nil && d.Log != nil {
			d.Log.Warn("notification was not delivered", "channel", id, "event", ev.Name, "err", err.Error())
		}
	}
}

// Test sends one message to a single channel, ignoring quiet hours, the
// rate limit, and whether the channel is enabled.
func (d *Dispatcher) Test(ctx context.Context, id string) error {
	if d == nil {
		return op("Notifications are not available.")
	}
	ev := d.clean(events.Event{
		Name: "notify.test", Severity: events.Info, Source: "notify",
		Title: "FELDCORE test",
		Body:  "This is a test message from FELDCORE. This channel is working.",
		Key:   "notify.test",
	})
	n := d.Store.Snapshot().Notify
	return d.send(ctx, n, id, ev, true, false, d.now())
}

func (d *Dispatcher) send(ctx context.Context, n store.Notify, id string, ev events.Event, test, quiet bool, now time.Time) error {
	if !test && quiet && id != "inapp" {
		if ev.Severity != events.Critical || !n.Quiet.AllowCritical {
			return nil
		}
	}
	key := ev.Key
	if key == "" {
		key = ev.Name + "\n" + ev.Title
	}
	if !test && !d.allow(id, key, string(ev.Severity), now, dedupeWindow(n)) {
		return nil
	}
	if !test && id != "inapp" && !d.allowRate(id, now, rateLimit(n)) {
		if d.Log != nil {
			d.Log.Info("notification rate limit", "channel", id, "event", ev.Name)
		}
		return nil
	}
	switch id {
	case "inapp":
		if !test && !inAppOn(n) {
			return nil
		}
		if d.Inbox != nil {
			d.Inbox.Add(Note{
				Name: ev.Name, Severity: string(ev.Severity), Title: ev.Title, Body: ev.Body, At: ev.At,
			})
		}
		return nil
	case "email":
		if d.Mail == nil {
			return op("Mail is not available.")
		}
		if !test && !n.Enabled {
			return nil
		}
		return d.Mail.deliver(ctx, Event{Kind: ev.Name, Title: ev.Title, Body: ev.Body}, true)
	default:
		var ch store.NotifyChannel
		found := false
		for _, c := range n.Channels {
			if c.ID == id {
				ch = c
				found = true
				break
			}
		}
		if !found {
			return input("That channel was not found.")
		}
		if !test && !ch.Enabled {
			return nil
		}
		secret := ""
		if d.Mail != nil {
			secret, _ = d.Mail.channelSecret(id)
		} else if d.Secrets != nil {
			secret, _, _ = d.Secrets.Get(secretName(id))
		}
		if secretRequired(ch.Kind) && secret == "" {
			return input("That channel has no token stored.")
		}
		return deliverChannel(ctx, d.client(), ch, secret, ev)
	}
}

func targets(n store.Notify, ev events.Event) []string {
	var out []string
	add := func(id string) {
		for _, have := range out {
			if have == id {
				return
			}
		}
		out = append(out, id)
	}
	if inAppOn(n) && (events.Rank(ev.Severity) >= events.Rank(events.Warning) || alwaysInbox(ev.Name)) {
		add("inapp")
	}
	if n.Enabled {
		switch ev.Name {
		case KindContainer:
			if n.OnContainerStop {
				add("email")
			}
		case KindUpdate:
			if n.OnUpdate {
				add("email")
			}
		}
	}
	for _, r := range n.Routes {
		if !matchEvent(r.Event, ev.Name) {
			continue
		}
		min := r.MinSeverity
		if min == "" {
			min = "warning"
		}
		if events.Rank(ev.Severity) < events.Rank(events.Severity(min)) {
			continue
		}
		for _, id := range r.Channels {
			add(id)
		}
	}
	return out
}

func alwaysInbox(name string) bool {
	switch name {
	case KindUpdate, "health.recovered", KindContainer, "backup.failed", "backup.succeeded":
		return true
	default:
		return false
	}
}

func (d *Dispatcher) clean(ev events.Event) events.Event {
	var hidden []string
	if d.Secrets != nil {
		hidden = d.Secrets.Values()
	}
	ev.Title = secrets.Redact(ev.Title, hidden...)
	ev.Body = secrets.Redact(ev.Body, hidden...)
	ev.Key = secrets.Redact(ev.Key, hidden...)
	if len(ev.Fields) > 0 {
		cp := make(map[string]string, len(ev.Fields))
		for k, v := range ev.Fields {
			cp[k] = secrets.Redact(v, hidden...)
		}
		ev.Fields = cp
	}
	return ev
}

func (d *Dispatcher) allow(channel, key, severity string, now time.Time, window time.Duration) bool {
	if window <= 0 {
		return true
	}
	id := channel + "\n" + key
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[string]seenItem{}
	}
	if prev, ok := d.seen[id]; ok && now.Sub(prev.At) < window && events.Rank(events.Severity(severity)) <= events.Rank(events.Severity(prev.Severity)) {
		return false
	}
	d.seen[id] = seenItem{At: now, Severity: severity}
	d.pruneSeen(now, window)
	d.saveDedupe()
	return true
}

func (d *Dispatcher) allowRate(channel string, now time.Time, limit int) bool {
	if limit <= 0 {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rate == nil {
		d.rate = map[string][]time.Time{}
	}
	cut := now.Add(-time.Hour)
	kept := d.rate[channel][:0]
	for _, ts := range d.rate[channel] {
		if ts.After(cut) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= limit {
		d.rate[channel] = kept
		return false
	}
	d.rate[channel] = append(kept, now)
	return true
}

func (d *Dispatcher) pruneSeen(now time.Time, window time.Duration) {
	for k, v := range d.seen {
		if now.Sub(v.At) > window*2 && now.Sub(v.At) > time.Hour {
			delete(d.seen, k)
		}
	}
}

func (d *Dispatcher) loadDedupe() {
	if d.dpath == "" {
		return
	}
	raw, err := os.ReadFile(d.dpath)
	if err != nil {
		return
	}
	var file struct {
		Seen map[string]seenItem `json:"seen"`
	}
	if json.Unmarshal(raw, &file) != nil || file.Seen == nil {
		return
	}
	d.seen = file.Seen
}

func (d *Dispatcher) saveDedupe() {
	if d.dpath == "" {
		return
	}
	raw, err := json.Marshal(struct {
		Seen map[string]seenItem `json:"seen"`
	}{Seen: d.seen})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(d.dpath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(d.dpath, raw, 0o600)
}
