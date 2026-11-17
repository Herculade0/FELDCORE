---
id: updates-rollback
kind: reference
title: Updates and rollback
summary: FELDCORE can check for a release. It does not install one or roll the OS back.
icon: refresh
order: 860
comingSoon: true
related:
  - secure-your-box
references:
  - settings
---

This topic is a placeholder for installing a FELDCORE update and for rolling back an OS image. Neither is in this build.

What exists today is on Settings → General → **Updates**:

- **Security updates** turns on Debian's unattended-upgrades, limited to security updates.
- **Check for FELDCORE updates** looks for a newer release and can email you when notifications are on. It does not download or install the release.

[[route:settings.updates|Open Updates]]

An appliance image with an A/B slot and boot counting is a design for a future FELDCORE OS. This daemon does not do that.
