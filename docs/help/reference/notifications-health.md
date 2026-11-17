---
id: notifications-health
kind: reference
title: Health
summary: Checks for disks, heat, space, containers, machines, services, certificates, and tunnels, plus where alerts go.
icon: bell
order: 355
popular: true
page: health
related:
  - get-alerts
  - troubleshooting
references:
  - dashboard
  - proton
  - backups
---

Health reads the machine and marks each check OK, Warning, Critical, or Unknown. A missing tool is Unknown, not a failure.

[[route:health|Open Health]]

**Check now** runs the checks immediately. Otherwise they run on their own, and the dashboard uses the latest summary for the line under the hostname. The Health card on the dashboard opens this page.

The groups are Processor, Memory, Load, Storage, Temperatures, SMART, Containers, Virtual machines, Services, Certificates, and Tunnels. A SMART check can be opened to see the attributes.

## Thresholds

[[route:health.thresholds|Open thresholds]]

Each warning mark has to sit below its critical mark. Certificate warning is a larger number of days than the critical window. **Save thresholds** stores them on this server. The fields are processor, memory, load per thread, disk percent full, CPU, disk, and GPU temperature, certificate days, and how many container restarts count inside the restart window.

## Where the messages go

A check that crosses a mark, a backup that fails, a container that stops unexpectedly, and a newer FELDCORE release are events. They show in the bell, and they can be sent on from Settings → Network.

[[route:settings.mail|Open Notifications]]

**Notifications** is still the email card: generic SMTP, a Proton Mail Bridge, or a Proton SMTP token. **Notification channels** adds ntfy, Gotify, a Discord or Slack webhook, or any JSON webhook. Tokens stay encrypted and are not written to the log.

On that card:

- **In-app bell** keeps a copy in the header even when a channel is quiet.
- **Quiet hours** use this server's clock. **Allow critical** lets a critical message out during those hours. Info and warning wait.
- **De-duplication** is how long the same event is held back unless it gets more severe. The default window is 15 minutes.
- **Rate limit** is messages per hour, per external channel. The bell is not limited.
- **Routing** matches an event name (`backup.failed`), a prefix (`backup.*` or `health.*`), or `*` for every event. Email still follows the switches on the mail card unless a route names it.

[[help:get-alerts|Get alerts]] is the short version of the same path. [[help:proton|Proton Drive and Mail]] is the field-by-field mail card.
