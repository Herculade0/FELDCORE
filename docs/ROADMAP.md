# FELDCORE roadmap

Ordered by build sequence. Each milestone is shippable on its own.

## M0 — Foundation ✅
- [x] Go daemon + embedded React UI, single-binary build
- [x] Live dashboard: CPU, per-core, memory, load, uptime, storage, network
- [x] WebSocket stream with auto-reconnect
- [x] install.sh + systemd unit

## M1 — Shares 🔜
The headline Unraid-like feature.
- [ ] Auth gate (login) — prerequisite before any write feature
- [ ] Create / edit / delete a share (managed directory + metadata)
- [ ] Per-share SMB toggle → render Samba include, reload smbd
- [ ] Per-share NFS toggle → write /etc/exports.d entry, exportfs -ra
- [ ] Show live share status (clients connected, enabled protocols)
- [ ] Dependency handling: offer to `apt install` samba / nfs-kernel-server

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
