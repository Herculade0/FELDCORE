---
id: vpn
kind: reference
title: VPN
summary: WireGuard tunnels in a gluetun gateway, and the kill switch.
icon: shield
order: 270
popular: false
related:
  - connect-vpn
references:
  - proton
  - networks
---

The VPN card is under Settings → Network. The short version is [[help:connect-vpn|Connect a VPN]].

[[route:settings.vpn|Open VPN]]

A tunnel is a standard WireGuard client. You can keep several and enable each one on its own. Turning the card's master switch off stops the feature.

## The gateway

Enabling a tunnel starts `qmcgaw/gluetun`, named `feldcore-vpn-<id>`, with `NET_ADMIN` and `/dev/net/tun`. `FIREWALL=on` is the kill switch. Gluetun wants the peer as an IP. If the config has a hostname, FELDCORE looks it up once when the gateway starts. The image is pulled from Docker Hub the first time.

The card shows whether the tunnel is up, the public IP the gateway reports, and the latest handshake. A handshake older than three minutes is treated as down.

`PostUp` and the other hook scripts in an imported file are not run. They are dropped, and the page says so. Keys are encrypted in `/var/lib/feldcore/secrets` and scrubbed from the log.

## Routing a container

**Route through VPN** sets Docker's network mode to `container:<gateway>`. Published ports are moved onto the gateway, because Docker cannot publish them on the attached container. Replacing the gateway, or changing those ports, restarts the containers that use it. Files in mapped folders stay.

If the tunnel is down, FELDCORE will not start that container.

The gateway is listed on the Docker page. Change it from the VPN card.

## What it does not do

It does not install WireGuard on the host. It does not move the host's default route. Only containers you mark are affected. Proton-specific port forwarding is in [[help:proton|Proton Drive and Mail]].
