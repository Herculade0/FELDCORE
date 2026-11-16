---
id: troubleshooting
kind: guide
title: Troubleshooting
summary: Where the logs are, and the errors you are most likely to hit.
icon: wrench
order: 100
popular: true
related:
  - getting-started
  - run-docker
references:
  - docker
  - audit
  - remote-access
  - dashboard
---

Start with the sentence on the page. FELDCORE usually names the container, the port, or the missing package instead of a code number.

## Logs

| What | Where |
| --- | --- |
| This daemon | `journalctl -u feldcore -b` |
| Setup code | That journal, and `/var/lib/feldcore/setup-code` (root only) |
| A container | **Logs** on its row. Stdout and stderr, refreshed on a timer. |
| This machine | **Host terminal** on the dashboard. An agent token cannot open it. |
| Agent calls | The audit log. Browser sign-in is not listed. |

[[route:docker|Open Docker]]

[[route:audit|Open the audit log]]

[[route:dashboard|Open the dashboard]]

> [!TIP]
> Secrets are scrubbed from the daemon log. If a token or a WireGuard key was pasted into a command, do not expect to read it back from `journalctl`.

## Common errors

**Can't reach FELDCORE.** The page retries on its own. Check that `feldcored` is running and that you are using port 8787. The dashboard pill says Offline when the live stats socket drops. It reconnects by itself.

**FELDCORE isn't running as root.** Shares, installs, and service changes will not apply. The systemd unit runs as root. A copy you started by hand has to be root too.

**Could not install Docker / Automatic install needs apt.** The button installs `docker.io` with apt. On a distro without apt, install Docker yourself and reload the page.

**Port 80 or 443 is already used.** Caddy will not start. The Web addresses card names the other container or program. Stop it or move its host port, then save again.

[[route:settings.web|Open Web addresses]]

**The container has no network of its own.** It is on a VPN gateway or on network `none`, so a published name cannot reach it. Turn off **Route through VPN**, or give it bridge or host, if you want a web address.

**The container stays stopped after you route it through a VPN.** The tunnel is off or down. Start the tunnel first. The kill switch is doing that on purpose.

[[route:settings.vpn|Open VPN]]

**This server has no GPU that FELDCORE can share.** The **Use the GPU** switch is on and no Intel or AMD render device is usable. NVIDIA shows "NVIDIA sharing isn't supported yet." Turn the switch off, or see [[help:gpu|GPU]].

**libvirt is not installed / KVM device missing.** Use **Install libvirt** on the VMs page. If `/dev/kvm` is missing, the page explains whether the firmware switch is off, the module is not loaded, or you are on WSL without nested virtualisation.

**This token does not include the … scope.** The bearer token is valid and too narrow. 401 means the token is missing or unknown. A shell attempt says "Agent tokens cannot open a shell" and is audited as denied.

**A required setting could not be read from the template.** The App Store note means a required field did not parse. Read the form before you apply. Unraid's `PUID` 99 and `PGID` 100 are the nobody account there. On Debian, change them to a user that owns the folders.
