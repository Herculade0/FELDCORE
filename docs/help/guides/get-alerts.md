---
id: get-alerts
kind: guide
title: Get alerts
summary: Mail for two events exists. A health and notification centre does not yet.
icon: bell
order: 120
comingSoon: true
popular: false
related:
  - troubleshooting
  - backup-data
references:
  - proton
  - notifications-health
---

A full alerts page, with health checks and more than mail, is still being built. This tile stays marked coming soon until that lands.

The card that exists today is **Notifications** on Settings → Network. It can email you in two cases:

- a container that was running stops, and you did not stop, restart, or edit it
- GitHub has a newer FELDCORE release, and update checks are turned on

It can send through generic SMTP, a Proton Mail Bridge, or a Proton SMTP token. Turning notifications off stops delivery. It does not delete the stored password.

[[route:settings.mail|Open Notifications]]

The field-by-field description is in [[help:proton|Proton Drive and Mail]]. [[help:notifications-health|Notifications and health]] is the placeholder for the rest.
