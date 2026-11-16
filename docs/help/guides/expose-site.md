---
id: expose-site
kind: guide
title: How to expose your site
summary: Give an app a name and HTTPS, through a Cloudflare Tunnel or on your own network.
icon: globe
order: 20
popular: true
related:
  - connect-vpn
  - secure-your-box
references:
  - remote-access
  - publish
  - networks
---

Nothing is published on a fresh install. You choose a container, then pick how people reach its web page. Game and UDP ports are not carried by the tunnel or by Caddy.

## Cloudflare Tunnel

The tunnel dials out to Cloudflare, so you do not open a port on the router.

1. Create a tunnel token, or an API token that can edit the tunnel and DNS. The exact permissions are in [[help:remote-access|Cloudflare Tunnel and HTTPS]].
2. Paste it here and save.

[[route:settings.remote|Open Remote access]]

3. On the container's row, open **Web address** and turn on **Expose publicly**. Set a hostname on a domain Cloudflare already hosts, such as `plex.example.com`, and the port the app listens on inside the container.

[[route:docker|Open Docker]]

FELDCORE attaches the container to the `feldcore` Docker network (unless it uses the host network), writes an ingress rule, and creates a proxied DNS name. Turning the switch off removes that rule and the DNS record FELDCORE created.

> [!WARNING]
> **Expose the FELDCORE page** publishes this admin UI. Only do that together with Cloudflare Access, so a stranger cannot try passwords against the sign-in form. Names ending in `.home.arpa`, `.local`, `.lan`, or `.internal` cannot be published.

## The Publish switch

Editing a container shows one **Publish** switch. Turning it on publishes the HTTPS name, the certificate, and, when Remote access is already on, the tunnel hostname. Pick the subdomain and the port inside the container, then press **Publish**.

The **Citizen ID** menu on that form is stored. It is not enforced until Citizen ID is connected. Protection still runs. See [[help:citizen-id|Citizen ID]].

[[route:settings.publishing|Open published apps]]

## A name on your home network

1. Set the home domain. `home.arpa` stays on your network.

[[route:settings.web|Open Web addresses]]

2. On the container, open **Web address**, turn on **Name on your home network**, and choose **HTTPS with the local certificate**.
3. Download FELDCORE's root certificate from the Web addresses card after Caddy has started, and install it on each device. Until you do, the browser warns.

**Plain HTTP** skips certificates. Use it only on a network you trust.

## Let's Encrypt on a public name

In **Web address**, choose **HTTPS with Let's Encrypt** and a hostname on a zone Cloudflare hosts. Caddy solves DNS-01 with the same API token as Remote access, so port 80 does not have to be open to the internet for the certificate.

Visitors still need to reach port 443 on this server, unless you use the tunnel instead. The tunnel terminates TLS at Cloudflare, so a tunnel hostname does not need this certificate.

Caddy publishes ports 80 and 443. If something else holds them, the page names it and does not start the proxy.

## Protection in front of every published app

Rate limits, known-bot blocking, and optional country lists apply to published requests. Country checks stay off until you turn them on.

[[route:settings.protection|Open Protection]]

A blocked request is written to the audit log as `publish.block`.

[[route:audit|Open the audit log]]

## Forwarding a port yourself

FELDCORE does not log in to your router. To publish the old way, map a host port on the container (bridge mode), then forward that port on the router to this machine.

You then have an open port, and you arrange HTTPS yourself. The tunnel and the Publish switch exist so you do not have to do that for a web page.
