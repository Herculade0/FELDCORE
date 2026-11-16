# FELDCORE roadmap

Ordered by build sequence. Each milestone is shippable on its own.

## M0 — Foundation ✅
- [x] Go daemon + embedded React UI, single-binary build
- [x] Live dashboard: CPU, per-core, memory, load, uptime, storage, network
- [x] WebSocket stream with auto-reconnect
- [x] install.sh + systemd unit

## M1 — Shares ✅
The headline Unraid-like feature.
- [x] Auth gate: first-run administrator setup, sign-in, sessions, rate limiting
- [x] Create / edit / delete a share (managed folder + metadata)
- [x] Per-share SMB: public, read-only or private; render Samba include, reload
- [x] Share users for private SMB shares
- [x] Per-share NFS: allowed clients, read-only; exports.d entry, exportfs -ra
- [x] Live per-protocol status on each share
- [x] Dependency handling: install samba / nfs-kernel-server from the UI (apt)

Follow-ups
- [ ] Share an existing folder anywhere on disk (today: under the shares root)
- [ ] Show who is connected, and space used per share
- [ ] Warn when a parent folder's permissions block access to a share
- [ ] Rename a share
- [ ] HTTPS (the session cookie is only marked Secure when served over TLS)
- [ ] Non-apt distributions: install support for dnf/pacman

## M2 — Docker
- [ ] Container list with state, stats, logs, start/stop/restart
- [ ] Create container (image, ports, volumes, env)
- [ ] App store: ingest Unraid Community Applications template catalog
- [ ] One-click install from a template → running container
- [ ] docker-compose stack import (stretch)

## M3 — Settings
- [ ] Users & authentication management
- [ ] Network overview / hostname
- [ ] Service toggles (which daemons FELDCORE manages)
- [ ] Appearance (theme)
- [ ] Self-update

## M4 — VMs (later)
- [ ] libvirt/KVM domain list + lifecycle
- [ ] Create VM wizard (ISO, disk, CPU, RAM, virtio)
- [ ] Browser console (VNC/SPICE)

## Cross-cutting
- [ ] Packaging: .deb, and a one-line curl installer
- [ ] Config persistence (embedded db or on-disk state in /var/lib/feldcore)
- [ ] Audit log of changes FELDCORE makes to the system
- [ ] Tests + CI
