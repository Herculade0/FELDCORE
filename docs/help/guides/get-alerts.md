---
id: get-alerts
kind: guide
title: Get alerts
summary: See health and backup failures in the bell, and send the ones you care about by email or a webhook.
icon: bell
order: 120
popular: true
related:
  - troubleshooting
  - backup-data
references:
  - notifications-health
  - proton
  - backups
---

Health checks and backup jobs publish events. The bell in the header keeps a copy. Mail and other channels send the ones you route.

## See it on this server

[[route:health|Open Health]]

**Check now** runs the checks. A warning or a critical result is the thing an alert is about: processor, memory, load, disk space, temperature, SMART, a container, a virtual machine, a failed service, a certificate, or a tunnel. A missing tool stays Unknown and is not treated as a failure.

Thresholds are on the same page. A warning mark has to sit below its critical mark.

[[route:health.thresholds|Open thresholds]]

The dashboard line under the hostname uses the latest health summary once a check has reported.

## Choose where it is sent

[[route:settings.mail|Open Notifications]]

1. On **Notifications**, pick generic SMTP, a Proton Mail Bridge, or a Proton SMTP token, and send a test if you want mail. Turning notifications off stops delivery. It does not delete the stored password. The mail card still covers a container that stops unexpectedly and a newer FELDCORE release.
2. On **Notification channels**, leave **In-app bell** on.
3. Add a channel if you want one: ntfy, Gotify, a Discord or Slack webhook, or any JSON webhook.
4. Add a route. The event is a name, a prefix, or `*`.

| Route | Matches |
| --- | --- |
| `health.*` | Health checks, including a recovery. |
| `health.disk` | A filesystem or pool crossed a space mark, or a pool is degraded. |
| `health.smart` | SMART failed, or a disk reported reallocated, pending, or media errors. |
| `backup.*` | A backup, check, or restore that did not finish. These are critical. |
| `container.stopped` | A container that was running stopped, and you did not stop, restart, or edit it. |
| `update.available` | GitHub has a newer FELDCORE release, and update checks are on. |
| `*` | Every event. |

**Quiet hours** hold info and warning. Critical still goes out when **Allow critical** is on, which is the default. The same event is not sent again inside the de-duplication window unless it gets more severe.

[[help:notifications-health|Health]] lists every check. [[help:proton|Proton Drive and Mail]] is the mail card itself.
