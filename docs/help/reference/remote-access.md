---
id: remote-access
kind: reference
title: Cloudflare Tunnel and HTTPS
summary: How the tunnel, Caddy, and the local certificate authority fit together.
icon: cloud
order: 260
popular: true
related:
  - expose-site
references:
  - publish
  - networks
---

Two containers do the publishing, and both stay off until you ask. `feldcore-cloudflared` is the tunnel. `feldcore-caddy` is the proxy that obtains certificates. The walkthrough is [[help:expose-site|How to expose your site]].

[[route:settings.remote|Open Remote access]]

## Tunnel modes

| Mode | You provide | FELDCORE does |
| --- | --- | --- |
| Off | Nothing | Nothing is published. This is the default. |
| Tunnel token | A token from Zero Trust → Networks → Tunnels | Runs the connector. You add DNS names yourself, unless you also add an API token. |
| API token | A Cloudflare API token | Creates a tunnel named `feldcore` (or the name you set), stores the connector token, and writes ingress and DNS. |

If the API token can see more than one account, the page asks for the account ID.

Create the API token with:

- Account → Cloudflare Tunnel → Edit
- Account → Account Settings → Read
- Zone → DNS → Edit
- Zone → Zone → Read

The token is encrypted under `/var/lib/feldcore/secrets/` (mode `0600`). It is not written to the log, to `state.json`, or onto the container command line. The connector reads a mode `0600` file mounted into the container.

FELDCORE prefers to run cloudflared as the container `feldcore-cloudflared` on the `feldcore` network. If Docker is not running, **Install cloudflared on the host** downloads the official binary into FELDCORE's data directory and runs it only while remote access is on.

Turning remote access off stops the connector and deletes the runtime token file. It does not delete the tunnel in Cloudflare, so turning it back on uses the same tunnel.

The last ingress rule is always `http_status:404`. An unknown hostname does not fall through to another app. The tunnel does not carry a game or UDP port.

## HTTPS on your network

[[route:settings.web|Open Web addresses]]

Caddy runs as `feldcore-caddy`. On a home name it uses an internal certificate authority. Download the root from the Web addresses card (`pki/authorities/local/root.crt` under the proxy data) and trust it:

- Windows: Local machine → Trusted Root Certification Authorities
- Mac: System keychain → Always Trust
- iPhone: install the profile, then enable it under Settings → General → About → Certificate Trust Settings
- Android: Settings → Security → Install a certificate → CA certificate
- Linux: copy to `/usr/local/share/ca-certificates/feldcore.crt` and run `update-ca-certificates`

Public certificates use DNS-01 and the `caddy-dns/cloudflare` module. The stock `caddy:2` image does not include that module. The first time you ask for Let's Encrypt, FELDCORE builds a local image `feldcore/caddy:2` that adds it. The API token is passed as `CF_API_TOKEN`. The Caddyfile contains `{env.CF_API_TOKEN}`, not the token.

**Serve this page on a hostname** publishes the FELDCORE UI itself on the home name or, with the tunnel, on a public name. Pair a public admin hostname with Cloudflare Access.

Caddy needs host ports 80 and 443. `feldcore-caddy` is not treated as a conflict with itself. Anything else holding those ports is named, and the proxy is not started.

## Where the files are

| Item | Where |
| --- | --- |
| Encrypted API token and tunnel token | `/var/lib/feldcore/secrets/` |
| Encryption key | `/var/lib/feldcore/secrets/master.key` |
| Runtime tunnel token | `/var/lib/feldcore/run/tunnel.token` |
| Caddyfile and certificates | `/var/lib/feldcore/caddy/` |
| Hostnames and routes | `state.json`, with no secrets |
