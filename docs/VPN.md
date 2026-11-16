# VPN

FELDCORE can run WireGuard tunnels and send chosen containers through them.
Everything here is off until you turn it on under **Settings → Network**, in the VPN card.

## Tunnels

A tunnel is a standard WireGuard client configuration. Add one by importing a
`.conf` file or by pasting it. You can keep several tunnels and turn each one
on or off.

FELDCORE reads `[Interface]` and the first `[Peer]`. It does **not** run
`PostUp`, `PreUp`, `PostDown` or `PreDown` from an imported file. Those lines
are dropped and the page says so.

The private key and any preshared key are stored in the secrets box
(`/var/lib/feldcore/secrets`), encrypted with a key that stays on this
machine. They are not written to `state.json` and the log scrubs them. See
[PROTON.md](PROTON.md) for the Proton preset.

## How a tunnel is connected

Turning a tunnel on starts a gateway container, `qmcgaw/gluetun`, named
`feldcore-vpn-<id>`. It needs `NET_ADMIN` and `/dev/net/tun`. Gluetun's
firewall is turned on (`FIREWALL=on`). That is the kill switch: if the
WireGuard session drops, traffic from anything sharing that container's
network stops with it.

Gluetun needs the server as an IP address. If the configuration has a
hostname, FELDCORE looks it up once when the gateway starts.

The page shows whether the tunnel is up, the public IP reported by the
gateway, and the latest WireGuard handshake when the gateway reports one. A
handshake older than three minutes is treated as down.

## Route through VPN

The container form has **Route through VPN**. Choosing a tunnel attaches that
container with Docker's `network_mode: container:<gateway>`, the same pattern
as running an app beside gluetun. The container has no network of its own.

- Published ports are moved onto the gateway. Docker cannot publish ports on
  a container that uses another container's network.
- If the tunnel is off or down, FELDCORE does not start the container. The
  container can still be created; it stays stopped.
- Changing the ports, or replacing the gateway, restarts containers that use
  it. Their files in mapped folders are kept.
- Name the container before routing it. FELDCORE uses the name to remember
  which ports belong to it.

The gateway container is listed on the Docker page and is edited from
Settings → VPN, not from the container form.

## What this does not do

FELDCORE does not install WireGuard on the host, and it does not route the
host's own traffic. Only containers you mark are affected. The gateway image
is pulled from Docker Hub the first time a tunnel starts.
