# Events

FELDCORE packages tell each other that something happened by publishing on
an in-process bus, `internal/events`. Notifications, and anything added
later, subscribe. There is no network hop and no queue on disk: if the
daemon is not running, the event is not kept.

The daemon installs the process bus at startup with `events.SetDefault`.
After that, any package can emit without being handed a pointer.

## Emit

```go
events.Publish(events.Event{
    Name:     "backup.failed",
    Severity: events.Critical,
    Title:    "Backup failed: Nightly",
    Body:     "Nightly failed. The repository could not be reached.",
    Source:   "backup",
    Key:      "backup.failed:<destination id>",
    Fields:   map[string]string{"destination": "<id>", "job": "<id>", "kind": "backup"},
})
```

`Name` is a dotted identifier. Routing matches it exactly, matches `*` for
every event, or matches a prefix ending in `.*` (`backup.*` matches
`backup.failed`, `backup.check_failed`, and `backup.restore_failed`).

`Severity` is `info`, `warning`, or `critical`. Quiet hours hold info and
warning. Critical still goes out while quiet hours allow it, which is the
default.

`Key` is the de-duplication identity. The same key is not sent again on the
same channel inside the de-duplication window (15 minutes unless an
administrator changes it), unless the new severity is higher. Leave `Key`
empty to use the name plus the title.

`Fields` is optional string metadata. It is stored and may be shown. It
must not contain passwords, keys, or the repository password.

## Do not put secrets in an event

`Title`, `Body`, `Key`, and `Fields` are written to the in-app inbox and to
whatever channels are routed. They are not a place for passwords, SMTP
tokens, rclone config, webhook URLs, or API tokens. The dispatcher redacts
secrets it already holds, and it will not invent ones it has never seen.

Say what failed in plain language. "The repository could not be reached" is
enough. The credential that was used is not.

## Names already used

| Name | When |
| --- | --- |
| `container.stopped` | A container that was running is not, and FELDCORE did not stop it. |
| `update.available` | A newer FELDCORE release is published. |
| `notify.test` | An administrator pressed Send test. |
| `health.cpu` | Processor use crossed a threshold. |
| `health.memory` | Memory use crossed a threshold. |
| `health.load` | Load per thread crossed a threshold. |
| `health.temperature` | A CPU, disk, or GPU temperature crossed a threshold. |
| `health.disk` | A filesystem or pool crossed a space threshold, or a pool is degraded. |
| `health.smart` | SMART failed, or a disk reported reallocated, pending, or media errors. |
| `health.container` | A container is unhealthy or was killed for running out of memory. |
| `health.container.restart` | A container is restarting in a loop. |
| `health.vm` | A virtual machine crashed or is paused. |
| `health.systemd` | A systemd unit is failed. |
| `health.cert` | A certificate FELDCORE stores is near expiry or already expired. |
| `health.tunnel` | A Cloudflare or VPN tunnel that is turned on is down or still starting. |
| `health.recovered` | A check that was warning or critical is healthy again. |
| `backup.failed` | A backup job did not complete. |
| `backup.check_failed` | A verify job did not complete. |
| `backup.restore_failed` | A restore job did not complete. |

The backup package publishes those three at severity `critical`, with
source `backup` and a key of the name plus the destination id
(`backup.failed:<destination id>`). A retry of the same destination is
de-duplicated. A different destination still alerts. `backup.succeeded` is
not published. A route can still name it, or `backup.*`, if a later change
emits it at `info`.

## What listens

The notification dispatcher subscribes to the default bus. A failed backup
is recorded in the in-app inbox. Channel delivery follows the routes on the
Settings page: a route of `backup.*` matches all three failure names. Quiet
hours hold info and warning; critical still sends when critical alerts are
allowed during quiet hours. De-duplication is per key. Email still uses the
SMTP, Proton Mail Bridge, and Proton SMTP token settings already on that
page. A test message does not require a live Proton account to prove the
bus works; delivering mail does, and Bridge's first login stays in the
Bridge container's console.
