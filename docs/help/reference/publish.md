---
id: publish
kind: reference
title: Publish and protection
summary: The Publish switch, per-app access, and the checks in front of every published name.
icon: globe
order: 250
popular: false
related:
  - expose-site
  - secure-your-box
references:
  - remote-access
  - citizen-id
---

Publishing is off until you turn it on for one container. The list of what is currently published is on Docker and on Settings → Publishing.

[[route:settings.publishing|Open published apps]]

## The switch

On the container edit form, **Publish** turns on three things together: the HTTPS name, the certificate, and the Cloudflare hostname when Remote access is already connected. You set the subdomain and the port the app listens on inside the container. Saving with the switch off removes them again.

The Web address button on the container row is the other door to the same idea: **Name on your home network** and **Expose publicly** are separate toggles there. Use whichever you are looking at. They write the same kind of route.

Home names such as `plex.home.arpa` use the local certificate and skip the tunnel. A public name uses the tunnel when Remote access is on. If Remote access is off, the page says the name is served from this server only, and that no router ports were opened.

## Certificate

A home name uses the local certificate.

A public name has a choice:

- **Let's Encrypt (directly public)** is the default. Caddy asks Let's Encrypt with the Cloudflare API token from Remote access. Use this when the name itself must present a public certificate.
- **Local certificate (tunnel only)** uses FELDCORE's internal CA. Cloudflare terminates the public HTTPS, and the tunnel dials this server on port 8791. A certificate does not need the API token. Use this when the name is only opened through the tunnel.

Remote access still publishes the tunnel hostname for either choice when the name is public and Remote access is on. The sign-in page is served on that same port, so a browser coming through the tunnel can sign in.

## Citizen ID on the form

The menu is `public`, `signed-in`, `groups`, or `admins`. The value is stored with the app and enforced. Protection still runs first. Group names are comma-separated. The same choice, with friendlier labels, is on Settings → Citizen ID.

[[help:citizen-id|Citizen ID]]

[[route:settings.citizen|Open Citizen ID]]

## Protection

Protection sits in front of every published app, before the Citizen ID step and before the proxy.

[[route:settings.protection|Open Protection]]

- **Requests per second** and **Burst** are per client IP, per app.
- **Block known bots and empty user agents** refuses scanners such as sqlmap, nikto, and nmap, and requests with no user agent. This starts on.
- **Block common scanner paths** is optional. It refuses paths such as `/wp-admin`, `/.env`, and `/.git`.
- **Country list** does nothing until you enable the GeoLite2 database and save. The licence key is stored with the other secrets and is not logged. Private addresses have no country, so an allow list blocks them.

A refusal is an audit event: token name `protection`, action `publish.block`, result `denied`. The reason is one of rate, country, geo-unavailable, bot, empty-ua, or scanner.

[[route:audit|Open the audit log]]

The base domain controls names like `plex.<domain>`. Leave it blank to use the web-address domain, which starts as `home.arpa`.
