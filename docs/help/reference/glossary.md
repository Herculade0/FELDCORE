---
id: glossary
kind: reference
title: Glossary
summary: Short meanings for the words the rest of Help uses.
icon: book
order: 360
popular: true
related:
  - getting-started
references: []
---

**Administrator.** The one account that signs in to this website. Its session cookie is not limited by token scopes.

**Appdata.** The usual folder for a container's own files. A default share of that name is `appdata`.

**Bearer token.** A secret sent as `Authorization: Bearer`. It identifies an agent, not a browser session. See [[help:api-tokens|API tokens and scopes]].

**Bridge.** The default Docker network. The container is isolated from your LAN and reached through ports you publish on the host.

**Caddy.** The proxy that serves names and certificates. On FELDCORE OS it is `/usr/bin/caddy`. On other systems it is the container `feldcore-caddy`.

**Cloudflared.** The Cloudflare Tunnel connector. On FELDCORE OS it is `/usr/bin/cloudflared`. On other systems it is the container `feldcore-cloudflared` when Docker is running.

**DNS-01.** A way to prove you control a domain by a DNS record. Let's Encrypt can issue a certificate without port 80 open to the world. Visitors can still need port 443 unless you use the tunnel.

**Gluetun.** The gateway image a WireGuard tunnel runs in. Containers you route through the VPN share its network.

**home.arpa.** A name that is meant to stay on your network. Cloudflare will not publish it.

**Host network.** A container that uses the server's network stack directly. Port mappings on that container are ignored.

**IOMMU group.** Devices the hardware isolates together. Passing one device from a group to a VM and leaving the others on the host usually fails.

**Kill switch.** Gluetun's firewall. If the WireGuard session drops, containers on that tunnel lose their network.

**KVM.** The Linux hypervisor. `/dev/kvm` has to exist before a guest can start.

**libvirt.** The tool FELDCORE uses to define and control KVM guests. `virsh` applies the XML.

**MCP.** The read-only agent endpoint at `/mcp`. It needs a bearer token with the `read` scope.

**OVMF.** UEFI firmware for a guest. Required if you want a TPM.

**Publish.** The switch that gives one container an HTTPS name. Off until you turn it on.

**qcow2.** A disk image format libvirt can snapshot internally. A raw VM disk cannot.

**Scope.** A permission on a token: `read`, `containers`, `vms`, or `admin`. Shell is not a scope.

**Share.** A folder under the shares root, exported over SMB, NFS, or both.

**Snapshot (container).** A copy of a container's template, image tag, and mapped folders.

**Snapshot (VM).** A libvirt domain snapshot. Separate from container snapshots.

**virtio.** The fast disk and network devices for a guest. Windows needs the virtio driver ISO to use them during install.
