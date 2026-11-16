package events

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestSubscribeEmitAndCancel(t *testing.T) {
	bus := New()
	var n atomic.Int32
	cancel := bus.Subscribe(KindBackupFailed, func(ctx context.Context, e Event) {
		if e.Kind != KindBackupFailed || e.Fields["job"] != "abc" || e.Title == "" {
			t.Errorf("event %+v", e)
		}
		n.Add(1)
	})
	var all atomic.Int32
	bus.Subscribe("*", func(context.Context, Event) { all.Add(1) })

	bus.Emit(context.Background(), Event{
		Kind: KindBackupFailed, Title: "Backup failed", Body: "disk full",
		Fields: map[string]string{"job": "abc"},
	})
	bus.Emit(context.Background(), Event{Kind: KindBackupCheckFailed, Title: "Check failed"})
	if n.Load() != 1 {
		t.Fatalf("specific handler called %d times", n.Load())
	}
	if all.Load() != 2 {
		t.Fatalf("wildcard called %d times", all.Load())
	}
	cancel()
	bus.Emit(context.Background(), Event{Kind: KindBackupFailed, Title: "again"})
	if n.Load() != 1 {
		t.Fatal("cancelled handler was called")
	}
	if all.Load() != 3 {
		t.Fatalf("wildcard after cancel: %d", all.Load())
	}
}

func TestNilBus(t *testing.T) {
	var b *Bus
	b.Emit(context.Background(), Event{Kind: KindBackupFailed})
	cancel := b.Subscribe(KindBackupFailed, func(context.Context, Event) {})
	cancel()
}
