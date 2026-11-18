---
id: api-tokens
kind: reference
title: API tokens and scopes
summary: Bearer tokens, the scopes on the Agent access card, and which routes each one may call.
icon: key
order: 330
popular: true
related:
  - ai-agents
references:
  - audit
  - internal-affairs
---

Tokens live on Settings → General → **Agent access**. The guide for connecting an agent is [[help:ai-agents|AI Agents]].

[[route:settings.agent|Open Agent access]]

A token is a random secret shown once, stored as a SHA-256 hash. You send it as `Authorization: Bearer`. The cookie from the browser is a different credential. The cookie is the administrator and is not limited by scopes. The token is.

## Scopes

| Scope | HTTP |
| --- | --- |
| `read` | GET on system info, stats, history, the stats stream, shares, the folder suggester, the App Store, Docker, VMs, stacks, health, the notification inbox, remote, proxy, and snapshot usage. |
| `containers` | Create, replace, start, stop, restart, and delete a container. Create, restore, and delete that container's snapshots. |
| `vms` | Create, edit, start, stop, pause, resume, autostart, and delete a VM, and its libvirt snapshots. |
| `files:read` | List, preview, and download files in a share. |
| `files:write` | Upload, rename, move, copy, and delete files in a share. Includes `files:read`. |
| `stacks` | Save, deploy, update, stop, and remove Compose stacks. |
| `storage` | Create and change pools, mount them, and run scrub, balance, and trim. |
| `backups:read` | See destinations, jobs, copies, and the files inside a copy. |
| `backups:write` | Run backups, checks, and restores, and change destinations. Includes `backups:read`. The older Proton Drive tar.gz card stays on `admin`. |
| `mail` | Read the Proton Bridge username, app password, and IMAP and SMTP endpoints (`GET` and `PUT /api/v1/integrations/proton-bridge`, and `POST .../read`). It does not open a console. |
| `admin` | Everything else, including settings, shares, tokens, publish, VPN, and notification mail. Includes the scopes above. |

`admin` does not include a shell. `/terminal`, a container's `/terminal`, and a VM's `/console` are classified as `shell`, which no token can be granted. The handlers also require the administrator's session cookie.

A new route that nobody has classified requires `admin`, so a narrower token cannot call it by accident.

Publishing a hostname (`PUT` on a container's address or publish route) is `admin`, not `containers`.

MCP at `/mcp` requires a bearer token with `read`. A session cookie is not accepted there. The tools are listed in [[help:ai-agents|AI Agents]]. They are read-only.

## Results

| Status | Meaning |
| --- | --- |
| 401 | Missing or unknown token. |
| 403 | The token is valid and does not have the scope. Shell attempts use this too. |

Both are audited. See [[help:audit|Audit log]].

Expiry is optional. The form offers never, 7, 30, 90, or 365 days. The server accepts up to 730. At most 25 tokens can be active. Revoked tokens are kept for a while so the list can still show them, then dropped. **Revoke all agent access** revokes every one.

Internal Affairs scopes are not implemented. [[help:internal-affairs|Internal Affairs]].
