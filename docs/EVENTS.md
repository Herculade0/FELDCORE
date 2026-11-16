# Internal events

`internal/events` is the in-process bus FELDCORE uses when one part of the
server should tell another that something happened. A subscriber does not
import the package that emitted the event.

## Event

```go
events.Event{
    Kind:   "backup.failed",
    Title:  "Backup failed: nightly",
    Body:   "nightly failed. restic could not reach the repository.",
    Fields: map[string]string{"destination": "<id>", "job": "<id>", "kind": "backup"},
}
```

`Fields` is optional context. It must not contain passwords, keys, or the
repository password. `Emit` copies the map before handlers run.

## Kinds

| Kind | When |
| --- | --- |
| `backup.failed` | A backup job fails. |
| `backup.check_failed` | A verify job fails. |
| `backup.restore_failed` | A restore job fails. |

## Subscribe

```go
bus := events.New()
cancel := bus.Subscribe(events.KindBackupFailed, func(ctx context.Context, ev events.Event) {
    // deliver ev.Title and ev.Body
})
defer cancel()
bus.Subscribe("*", func(ctx context.Context, ev events.Event) {
    // every kind
})
```

A nil bus ignores `Emit` and `Subscribe`. Handlers run one after another on
the goroutine that called `Emit`, so a handler should return quickly.

The backup service emits on the bus stored in `Service.Events`. `cmd/feldcored`
creates one bus, gives it to the backup service, and calls
`notify.SubscribeBackups`. That hook sends the three failure kinds through
the existing notification mail when notifications are turned on. There is no
separate backup switch: `notify.Emit` already delivers a kind it does not
special-case while the master switch is on.

A notifications agent can subscribe to the same bus, or to the kinds above,
and send its own alert. It does not need to import `internal/backup`.
