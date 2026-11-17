---
id: secure-your-box
kind: guide
title: Secure your box
summary: Claim the account, turn on the firewall, and keep the admin page off the internet.
icon: lock
order: 80
popular: false
related:
  - expose-site
  - ai-agents
references:
  - settings
  - publish
  - api-tokens
  - audit
---

FELDCORE runs as root because it edits system config. The website in front of that is one administrator account. There are no extra roles.

## The account

Sign-in is required for every page except health and the login flow itself. Passwords are stored as bcrypt. Failed sign-ins are rate limited per address.

Change the password, and how long a session lasts, on the Security card. The timeout applies to new sign-ins. 168 hours is one week.

[[route:settings.security|Open Security]]

## The firewall

The firewall switch uses `ufw`. SSH's port stays allowed, and FELDCORE does not edit or stop sshd. FELDCORE's own port (8787 unless you changed it) is allowed too.

[[route:settings.security|Open the firewall switch]]

> [!WARNING]
> A static address that is wrong drops this page, and it is not put back on its own. Prefer DHCP unless you know the address is free.

[[route:settings.network|Open Network]]

## Do not publish the admin UI by accident

Remote access and per-app Publish are off until you turn them on. Publishing a container is fine. Publishing **this** page means anyone who can open the hostname can try the login, unless Cloudflare Access is in front of it.

[[route:settings.remote|Open Remote access]]

Protection (rate limit, bots, optional country list) sits in front of published apps. It is not a substitute for keeping the admin UI on your LAN.

[[route:settings.protection|Open Protection]]

## Agents

A token is not your session. Give it the smallest scope that works, set an expiry, and revoke it if it leaks. The host terminal and consoles stay on the signed-in browser.

[[route:settings.agent|Open Agent access]]

[[help:ai-agents|AI Agents]] walks through scopes, MCP, and the audit log.
