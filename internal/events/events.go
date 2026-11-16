// Package events is the in-process bus other FELDCORE agents subscribe to.
//
// Backup failures are the first events. A notifications agent delivers them
// by subscribing here; it does not need to import the backup package.
// See docs/EVENTS.md.
package events

import (
	"context"
	"sync"
	"time"
)

const (
	// KindBackupFailed is emitted when a backup job fails.
	KindBackupFailed = "backup.failed"
	// KindBackupCheckFailed is emitted when a verify job fails.
	KindBackupCheckFailed = "backup.check_failed"
	// KindBackupRestoreFailed is emitted when a restore job fails.
	KindBackupRestoreFailed = "backup.restore_failed"
)

// Event is one thing that happened. Fields is optional context (destination
// id, job id). It must not contain passwords, keys, or repository secrets.
type Event struct {
	Kind   string
	Title  string
	Body   string
	At     time.Time
	Fields map[string]string
}

// Handler receives one event. It should return quickly; the bus calls
// handlers one after another on the emitter's goroutine.
type Handler func(context.Context, Event)

// Bus is an in-memory fan-out. A nil Bus ignores Emit and Subscribe.
type Bus struct {
	mu   sync.Mutex
	next int
	subs map[string]map[int]Handler
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: map[string]map[int]Handler{}}
}

// Subscribe registers h for kind. The kind "*" receives every event.
// The returned function removes the subscription.
func (b *Bus) Subscribe(kind string, h Handler) (cancel func()) {
	if b == nil || h == nil || kind == "" {
		return func() {}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	if b.subs[kind] == nil {
		b.subs[kind] = map[int]Handler{}
	}
	b.subs[kind][id] = h
	return func() {
		b.mu.Lock()
		delete(b.subs[kind], id)
		b.mu.Unlock()
	}
}

// Emit delivers e to subscribers of e.Kind and to subscribers of "*".
func (b *Bus) Emit(ctx context.Context, e Event) {
	if b == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.Fields != nil {
		cp := make(map[string]string, len(e.Fields))
		for k, v := range e.Fields {
			cp[k] = v
		}
		e.Fields = cp
	}
	b.mu.Lock()
	var hs []Handler
	for _, h := range b.subs[e.Kind] {
		hs = append(hs, h)
	}
	for _, h := range b.subs["*"] {
		hs = append(hs, h)
	}
	b.mu.Unlock()
	for _, h := range hs {
		h(ctx, e)
	}
}
