---
id: dashboard
kind: reference
title: Dashboard
summary: Live CPU, memory, disks, and network, plus the host terminal.
icon: layout
order: 200
popular: true
page: dashboard
related:
  - getting-started
  - troubleshooting
references: []
---

The dashboard is the home page after you sign in. Figures update over a WebSocket. If it drops, the client reconnects on its own. The pill in the corner is Online, Connecting, or Offline.

## What the cards show

**Processor** is total usage, the model, and a sparkline. **Threads** is one cell per logical CPU, coloured by load.

**Memory** is how much is in use against the total installed. **System** shows uptime, the OS, the kernel, and the 1, 5, and 15 minute load.

**Network** is the sum of download and upload across the interfaces the collector sees, in bytes per second.

**Storage** is each mounted filesystem: size, used percent, and the mount point. This is a view of mounts. It is not a disk manager. Partitioning, pools, and SMART are not on this page. [[help:disk-management|Disk management]] is coming soon.

The line under the hostname is a one-line health note. It calls out a processor over 90%, memory over 90%, or a filesystem over 90% full. Otherwise it says all systems nominal.

## Host terminal

**Host terminal** opens a shell on this machine in the browser. It needs your signed-in session. An agent token is refused, and that refusal is audited.

The terminal is the right place to run something you would run over SSH. It is not a file browser.

[[route:dashboard|Open the dashboard]]
