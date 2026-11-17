---
id: networks
kind: reference
title: Isolated networks
summary: Bridge, host, none, the feldcore network, and what is checked before an app is installed.
icon: network
order: 230
popular: false
related:
  - run-docker
  - expose-site
  - connect-vpn
references: []
---

Each container has a network type. The default, **bridge**, is the isolated one: the container is not on your LAN, and it is reachable through the host ports you map.

| Type | What it means |
| --- | --- |
| Bridge | Isolated from your LAN. Mapped ports are how you reach it. |
| Host | Uses this server's network stack. Port mappings are ignored. |
| None | No network at all. |
| A name Docker already has | A user-defined network that exists on this machine. |
| Route through VPN | The container has no network of its own. It shares the tunnel gateway. |

## The feldcore network

FELDCORE creates a Docker bridge named `feldcore` for containers it owns. `feldcore-caddy` is one of them. `feldcore-cloudflared` is the tunnel connector only on a machine that is not FELDCORE OS and does not have the OS copy of cloudflared. On FELDCORE OS the connector is the host program. When you publish an app, that app is attached to this network so the proxy can reach it by its container address. You do not create this network yourself. A container on the host network is reached via `127.0.0.1` instead.

A container on `none`, or one that shares a VPN gateway's network, has no address of its own. A published name cannot be pointed at it.

## Templates and networks they name

A Community Applications template that asks for its own Docker network is not given that network. The form starts on bridge and adds a note: "The template uses Docker network …. This setup uses bridge instead, which you can change below." Change it only if that network already exists. This build does not create a network from a template. A Compose file brings its own network on the Stacks page. [[help:compose-stacks|Compose stacks]].

## Before you apply

There is no separate "secure install" scanner that scores a template and blocks it. What happens instead:

- Choosing an app only fills the form. Nothing is pulled or started until you apply.
- Notes call out Unraid paths that were rewritten, `PUID` 99 / `PGID` 100, extra parameters that are kept but not applied, and a required field that did not parse.
- **Privileged** is off unless the template set it or you turn it on. The form says it gives the container full access to this server.
- **Use the GPU** fails the create, before Docker is called, when no shareable GPU exists.

Read those notes. They are the check.

[[route:docker|Open Docker]]

[[route:apps|Open the App Store]]
