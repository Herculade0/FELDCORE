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

## M2 — Docker ✅ (container management)
- [x] Detect Docker; install it (docker.io) from the UI
- [x] Container list with state, live CPU/memory, ports, managed flag
- [x] Lifecycle: start / stop / restart / remove (force when running)
- [x] Logs viewer (demuxed stdout/stderr, auto-refresh)
- [x] Create container (image pull, name, ports, volumes, env, restart policy)
- [x] Engine API client over the unix socket (no heavy SDK dependency)
- [x] Edit a container, Unraid-style: read its settings back, recreate with
      the changes, and roll back to the original if the new one fails
- [x] "Add configuration" with Path, Port, Variable, Label and Device, each
      with a name and description
- [x] Host path picker: shares first, then live folder suggestions
- [x] Network type per container (bridge, host, none, user-defined)
- [x] GPU sharing switch for AMD/Intel-style GPUs (/dev/dri)
- [x] Allocations table: network, address and host ports per container

### M2b — Docker app store 🔜
- [ ] Ingest the Unraid Community Applications template catalogue
- [ ] Browse / search apps, with icons and categories
- [ ] One-click install from a template → running container
- [ ] Update notifications for managed containers

Follow-ups
- [ ] Live log/stat streaming over WebSocket (today: polled)
- [ ] Container detail view (full config, inspect)
- [ ] Image management (remove unused, prune)
- [ ] docker-compose stack import
- [ ] NVIDIA GPU sharing: offer to install the NVIDIA Container Toolkit, only
      when an NVIDIA card is present
- [ ] Intel-specific extras (e.g. QuickSync tooling), only when an Intel GPU
      is present
- [ ] Choose a specific GPU when a server has more than one
- [ ] Warn when a chosen host port is already allocated, before applying

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

## FELDCORE-STORE

A private catalogue merged on top of Community Apps: game profiles, VM templates, app bundles, SDR apps, and fixed versions of broken templates. It builds on the App Store (M2b). Design and scope: [FELDCORE-STORE.md](FELDCORE-STORE.md).

- [ ] MVP — one sign-in for the private `Herculade0/feldcore-store` feed, Community Apps and FELDCORE Store toggles, manual and automatic refresh, merge and override, FELDCORE badge
- [ ] Games, bundles, and SDR device claims
- [ ] One-click VMs from store templates, after M4

## Cross-cutting
- [ ] Packaging: .deb, and a one-line curl installer
- [ ] Config persistence (embedded db or on-disk state in /var/lib/feldcore)
- [ ] Audit log of changes FELDCORE makes to the system
- [ ] Tests + CI
