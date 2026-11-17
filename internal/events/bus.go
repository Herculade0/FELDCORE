// Package events is the in-process bus other FELDCORE packages use to report
// something an administrator might want to hear about.
//
// Publish through events.Publish after the daemon has called SetDefault, or
// through a Bus it handed you. Do not put passwords, tokens, webhook URLs, or
// other secrets in Name, Title, Body, or Fields. The notification dispatcher
// scrubs known secrets, but it can only redact values it already stores.
//
// The wire format and the event names other agents should emit are documented
// in docs/EVENTS.md.
package events

import (
	"strings"
	"sync"
	"time"
)

// Severity is how loud an event is. Routing and quiet hours compare these.
type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

// Event is one thing that happened. Name is a dotted identifier such as
// backup.failed. Key is the de-duplication identity; when it is empty the
// dispatcher uses Name and Title.
type Event struct {
	Name     string
	Severity Severity
	Title    string
	Body     string
	Source   string
	Key      string
	At       time.Time
	Fields   map[string]string
}

// Handler receives a copy of each published event.
type Handler func(Event)

// Bus delivers events to subscribers in the order they subscribed.
type Bus struct {
	mu   sync.RWMutex
	next int
	subs map[int]Handler
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: map[int]Handler{}}
}

// Subscribe registers h. The returned function removes it.
func (b *Bus) Subscribe(h Handler) (unsubscribe func()) {
	if b == nil || h == nil {
		return func() {}
	}
	b.mu.Lock()
	id := b.next
	b.next++
	if b.subs == nil {
		b.subs = map[int]Handler{}
	}
	b.subs[id] = h
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

// Publish delivers a normalised copy of e. A panicking handler is recovered
// so one subscriber cannot stop the others. A nil bus does nothing, which
// lets callers publish before the daemon has wired notifications.
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}
	e = normalise(e)
	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.subs))
	for _, h := range b.subs {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()
	for _, h := range handlers {
		func() {
			defer func() { _ = recover() }()
			h(e)
		}()
	}
}

func normalise(e Event) Event {
	e.Name = strings.TrimSpace(e.Name)
	e.Title = strings.TrimSpace(e.Title)
	e.Body = strings.TrimSpace(e.Body)
	e.Source = strings.TrimSpace(e.Source)
	e.Key = strings.TrimSpace(e.Key)
	switch e.Severity {
	case Info, Warning, Critical:
	default:
		e.Severity = Info
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if len(e.Fields) > 0 {
		cp := make(map[string]string, len(e.Fields))
		for k, v := range e.Fields {
			cp[k] = v
		}
		e.Fields = cp
	}
	return e
}

// Rank orders severities. Info is 1, warning is 2, critical is 3.
func Rank(s Severity) int {
	switch s {
	case Critical:
		return 3
	case Warning:
		return 2
	case Info:
		return 1
	default:
		return 0
	}
}

var (
	defMu sync.RWMutex
	def   *Bus
)

// SetDefault installs the bus events.Publish delivers to. The daemon calls
// this once at startup. Tests may replace it.
func SetDefault(b *Bus) {
	defMu.Lock()
	def = b
	defMu.Unlock()
}

// Default returns the process bus, or nil before SetDefault.
func Default() *Bus {
	defMu.RLock()
	defer defMu.RUnlock()
	return def
}

// Publish sends e to the default bus. Before SetDefault it is a no-op, so a
// package can emit during early startup without a nil check.
func Publish(e Event) {
	if b := Default(); b != nil {
		b.Publish(e)
	}
}
