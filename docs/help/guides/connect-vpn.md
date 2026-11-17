---
id: connect-vpn
kind: guide
title: Connect a VPN
summary: Import a WireGuard config, including Proton, and send chosen containers through it.
icon: shield
order: 60
popular: false
related:
  - run-docker
  - expose-site
references:
  - vpn
  - proton
  - networks
---

VPN here is for containers you pick. It does not install WireGuard on the host, and it does not route the server's own traffic. Everything stays off until you turn a tunnel on.

## Add a tunnel

1. Open the VPN card and turn **VPN** on.
2. Import a WireGuard `.conf`, or paste it. Proton's download page can generate one. The card labels it Proton when the file looks like theirs. You can force **Standard WireGuard** if you want it treated as generic.
3. Turn that tunnel on.

[[route:settings.vpn|Open VPN]]

FELDCORE reads `[Interface]` and the first `[Peer]`. It drops `PostUp`, `PreUp`, `PostDown`, and `PreDown`, and the page says so. The private key is encrypted under `/var/lib/feldcore/secrets`. It is not written to the log or to `state.json`.

The first start pulls `qmcgaw/gluetun` and runs it as `feldcore-vpn-<id>`. Gluetun's firewall is the kill switch: if the WireGuard session drops, containers on that tunnel lose their network too.

## Send a container through it

1. Name the container first. FELDCORE uses the name to remember which ports belong to it.
2. Edit it and set **Route through VPN** to the tunnel.
3. Apply.

[[route:docker|Open Docker]]

Published ports move onto the gateway container. Docker cannot publish ports on a container that borrows another container's network. If the tunnel is off or down, the container can be created, and it stays stopped.

> [!TIP]
> For a forwarded port on Proton, generate the config with NAT-PMP. The public port shows on the tunnel while it is up. A free server, or a file made without that option, still connects. It just has no forwarded port. [[help:proton|Proton Drive and Mail]] covers the Proton preset. Mail and Drive are separate switches.

The gateway itself is edited from the VPN card, not from the container form.
