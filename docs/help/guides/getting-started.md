---
id: getting-started
kind: guide
title: Get started
summary: Install the binary, claim the server with the setup code, and sign in.
icon: rocket
order: 10
popular: true
related:
  - dashboard
  - settings
references:
  - dashboard
  - settings
  - first-run-wizard
---

FELDCORE is one program, `feldcored`, with this website packed inside it. It listens on port **8787**. You need an account before any of the other pages will load.

## Install

On a Debian machine, the installer puts the binary at `/usr/local/bin/feldcored` and starts it with systemd.

```bash
curl -fsSL https://raw.githubusercontent.com/Herculade0/FELDCORE/main/deploy/get.sh | sudo sh
```

The installer prints a one-time setup code. The same code is in the journal and in a root-only file:

```bash
journalctl -u feldcore -b
sudo cat /var/lib/feldcore/setup-code
```

> [!TIP]
> Building it yourself is `make build`, then `sudo ./bin/feldcored`. The code is printed in that terminal.

## Claim the server

1. Open `http://<this-server>:8787`.
2. Enter the setup code. It works once, so another machine on your LAN cannot create the account first.
3. Choose a username and a password of at least 8 characters.

After that, the same address shows the sign-in page. The session cookie is what makes you the administrator. An agent token, covered in [[help:ai-agents|AI Agents]], is a different credential and is not an admin session.

[[route:dashboard|Open the dashboard]]

## What you can set next

The first-start wizard may open after the account exists and walk through the host, network, where shares live, which packages to install, and the firewall. That flow is still being finished, so the step-by-step is marked coming soon: [[help:first-run-wizard|First-run wizard]].

Anything the wizard sets can be changed later.

[[route:settings|Open Settings]]

> [!WARNING]
> Do the setup promptly. Until an account exists, whoever can open port 8787 can claim the server.
