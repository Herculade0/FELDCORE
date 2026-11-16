package notify

import (
	"context"

	"github.com/Herculade0/feldcore/internal/events"
)

// SubscribeBackups delivers backup, check, and restore failures through the
// existing mail path. Notifications have to be on. There is no separate
// backup toggle: a kind this package does not special-case is sent when the
// master switch is on. The notifications agent can subscribe to the same bus
// instead of, or as well as, this hook. See docs/EVENTS.md.
func SubscribeBackups(bus *events.Bus, s *Service) {
	if bus == nil || s == nil {
		return
	}
	deliver := func(ctx context.Context, ev events.Event) {
		s.Emit(ctx, Event{Kind: ev.Kind, Title: ev.Title, Body: ev.Body})
	}
	bus.Subscribe(events.KindBackupFailed, deliver)
	bus.Subscribe(events.KindBackupCheckFailed, deliver)
	bus.Subscribe(events.KindBackupRestoreFailed, deliver)
}
