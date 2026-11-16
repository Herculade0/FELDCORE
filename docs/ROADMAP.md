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

### M2b — App Store ✅
- [x] Cache the Unraid Community Applications catalogue on the server, with refresh
- [x] Browse / search apps, with icons, categories and descriptions
- [x] Choosing an app opens the container setup page, prefilled from the template
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

### GPU selection
Specified in [GPU-SELECTION.md](GPU-SELECTION.md). Containers first. A GPU
assigned to a VM is locked to that VM (boot-time stub on the VM manager
branch), and both sides use the same PCI address.
- [ ] First slice: live inventory, a read-only Settings list (model, vendor,
      driver, VRAM, assigned to), a container picker that starts with nothing
      selected, and a Settings button to install the NVIDIA Container Toolkit
- [ ] Dashboard usage, ARM boards, and the NVIDIA driver as a sysext on FELDCORE OS

## M2c — Agent access, phase 1 ✅
The owner's own agents (Claude Code, a Grok assistant, or any MCP client) can read the server without a browser session.
- [x] Named API tokens with scopes `read`, `containers`, `vms` and `admin`, optional expiry, last-used time, and revoke
- [x] The secret is shown once. Only a SHA-256 hash is stored
- [x] Bearer auth on `/api/v1`, beside the session cookie, with scopes enforced on the server
- [x] **Revoke all agent access**
- [x] Read-only MCP server at `/mcp` (streamable HTTP, bearer token): system info and stats, shares, containers (status, ports, image), container logs, App Store list and search, VMs when that layer is installed
- [x] No shell or exec tool. Agent tokens cannot open the host terminal or a container console
- [x] Audit log of every token-authenticated API and MCP call, with a page in the UI. Newest 1,000 entries, 90 days
- See [AGENTS.md](AGENTS.md)

## M2d — Agent access, phase 2
Write tools, behind an approval queue. Phase 1 tokens and the read-only MCP server stay as they are.
- [ ] MCP write tools: install an app from the catalogue, and start, stop and restart containers and VMs
- [ ] An approval queue for destructive actions. Delete, network and firewall changes, and any shell are not applied by the agent. The request shows in the UI as **pending approval**, and the administrator approves or denies it
- [ ] The queue records who asked, which token, the exact action, and the decision in the audit log
- [ ] A token still cannot open a shell on its own. If a shell is ever offered to an agent, that request waits for approval like the other destructive actions

## M2e — Internal Affairs
A locked cabinet of Markdown case files. Agents file a report when they change something, hit an error, or write up what they found. Design only: [INTERNAL-AFFAIRS.md](INTERNAL-AFFAIRS.md).
- [ ] Locked share of Obsidian-compatible Markdown under the shares root. Agents append; they cannot rewrite or delete
- [ ] Token scopes `ia:file` and `ia:read`, not included in `admin`
- [ ] MCP and REST: file a report, append, search, get, list recent. Scrub secrets before saving. Also file an incident stub when a token call errors
- [ ] Internal Affairs page: timeline, filters, report viewer, and a note the administrator can file
- [ ] Keep every report until Proton Drive backups exist. Pruning after that requires a backed-up copy; the age window is not chosen yet
- Later: SMB reading room, withdrawing a report, git history, Proton Drive backup (the cabinet is in the first backup set)

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

## FELDCORE OS (the FELDCORE distro)

Long-term, after the Debian package is the normal way to run FELDCORE. An Unraid-style immutable appliance, booting from a dedicated disk partition instead of a USB stick. This is a design note, not something the current daemon does.

The running system is a Debian image (kernel plus a squashfs root), built with debootstrap, mkosi, or live-build. Each boot loads that image into RAM or mounts it read-only. A small persistent config partition, in the role of Unraid's `/boot/config`, holds FELDCORE state, network settings, share definitions, and container templates. Data, appdata, and VM disks stay on the storage disks. Updates write a new image into the inactive A/B slot and boot it; boot counting falls back to the last good image if that boot is not healthy. An installer ISO partitions the target disk. First boot reuses the first-start wizard.

### OS-1 — A bootable image
- [ ] Build a Debian root with debootstrap, mkosi, or live-build
- [ ] Kernel plus squashfs, mounted read-only or copied into RAM on boot
- [ ] Boot from a disk partition with systemd-boot, UEFI only, not from USB
- [ ] Persistent config partition, mounted at a fixed path, holding FELDCORE state

### OS-2 — Installer ISO
- [ ] ISO that partitions one chosen OS disk: boot partition, A and B image slots, config partition
- [ ] Write only that disk. Data disks are left untouched
- [ ] First boot runs the existing setup wizard

### OS-3 — Atomic updates
- [ ] Update the OS only by writing a new image into the inactive slot and booting it. The running system has no apt
- [ ] Use systemd-boot boot counting so a failed boot falls back to the previous slot
- [ ] Keep the config partition out of the image, so an update does not wipe state

### OS-4 — Appliance layout
- [ ] Data, appdata, and VM disks live on the storage disks, not on the OS slots
- [ ] Share definitions and container templates stay on the config partition
- [ ] Drivers and extra software ship as systemd-sysext images
- [ ] Apps run as Docker containers. No apt on the running system

### Decisions

- **Bootloader.** systemd-boot, UEFI only. A/B slots use boot counting.
- **Secure Boot.** Off for the first image. Signing the kernel and the image comes later.
- **Drivers and extras.** systemd-sysext images. Apps are Docker containers.
- **Packages.** Image only. The running system has no apt. The OS changes only when an image is swapped.

### In use

The machine you run is the booted image plus the config partition.

- **Updates.** A new OS image is written into the inactive slot and used on the next boot. Nothing is upgraded in place.
- **Rollback.** Boot counting returns to the previous image if the new one does not come up healthy. The boot menu can select that previous slot by hand.
- **Config backup and recovery.** State, network, shares, and container templates live on the config partition. Restoring a copy of that partition puts the settings back. Disks that hold data, appdata, and VMs are not part of that copy.
- **Drivers.** A driver or other extra is a systemd-sysext image on the config partition. It is merged at boot and is not built into the OS image.

## Cross-cutting
- [ ] Packaging: .deb, and a one-line curl installer
- [ ] Config persistence (embedded db or on-disk state in /var/lib/feldcore)
- [x] Audit log of calls made with an agent token (see M2c)
- [ ] Audit log of changes FELDCORE makes to the system
- [ ] Tests + CI
