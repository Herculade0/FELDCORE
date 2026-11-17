---
id: ai-agents
kind: guide
title: AI Agents
summary: Give an agent a scoped token, connect it over MCP or the API, and see what it did.
icon: bot
order: 90
popular: true
related:
  - secure-your-box
  - troubleshooting
references:
  - api-tokens
  - audit
  - internal-affairs
---

An agent is a program you trust with a token: Claude Code, Cursor, or anything else that can send HTTP. It is not signed in as you. The browser session is still the administrator. The token can only do what its scopes allow, and it cannot open a shell.

## Create a scoped token

1. Open Agent access. Name the token after the agent, for example `Claude Code`.
2. Tick the scopes it needs. Leave the others off.
3. Pick an expiry. The form offers never, 7 days, 30 days, 90 days, or a year.
4. Create it and copy the secret. It is shown once. FELDCORE stores a SHA-256 hash, the same way it stores session tokens.

[[route:settings.agent|Open Agent access]]

**Revoke** removes one token. **Revoke all agent access** disables every token at once. A revoked token fails on the next request. You cannot look the old secret up, because it was never stored.

> [!WARNING]
> Up to 25 active tokens can exist at once. A leaked token works until you revoke it or it expires. If one leaks, revoke it and create another.

## What each scope allows

| Scope | Allows |
| --- | --- |
| `read` | Look at the host, shares, the App Store, containers, container logs, VMs, Compose stacks, health, and the notification inbox. Required for MCP. |
| `containers` | Create, start, stop, restart, and delete containers over the HTTP API, and take or restore container snapshots. |
| `vms` | Create, edit, start, stop, and delete virtual machines over the HTTP API. Listing VMs is `read`. |
| `files:read` | List, preview, and download files in a share. |
| `files:write` | Upload, rename, move, copy, and delete files in a share. Includes `files:read`. |
| `stacks` | Save, deploy, update, stop, and remove Compose stacks. Grant `read` as well so the agent can list them and follow a job. |
| `storage` | Create and change pools, mount them, and run scrub, balance, and trim. Looking at disks is `read`. |
| `backups:read` | See destinations, jobs, copies, and the files inside a copy. |
| `backups:write` | Run backups, checks, and restores, and change destinations. Includes `backups:read`. |
| `admin` | Settings, shares, tokens, publishing, and every other API call. Includes the scopes above. |

`admin` does not include a shell. The host terminal, a container console, and a VM's VNC console check the signed-in browser. A token that calls them is refused, and the refusal is written to the audit log.

Grant `read` together with `containers` when an agent should both look and change containers. A token with only `containers` cannot call MCP and cannot read system info.

Publishing a hostname, and the other settings routes, need `admin`. `containers` is not enough for those.

Two more scopes are designed for Internal Affairs, `ia:file` and `ia:read`. They are not on the token form. `admin` will not include them when they ship. [[help:internal-affairs|Internal Affairs]] is coming soon.

> [!TIP]
> Phase 1 of MCP is read-only on purpose. The HTTP API is where a `containers` or `vms` token is allowed to change those things. Write tools on MCP, and an approval queue for destructive actions, are not built yet.

## Connect with MCP

The endpoint is `http://HOST:8787/mcp`. Replace `HOST` with this server. It speaks the streamable HTTP transport. Send the bearer token on every request, including the session stream. A session cookie is not accepted.

The token needs `read`. `admin` includes `read`.

```bash
claude mcp add --transport http feldcore http://HOST:8787/mcp --header "Authorization: Bearer fc_..."
```

Any other client that speaks streamable HTTP uses the same URL and header:

```json
{
  "mcpServers": {
    "feldcore": {
      "url": "http://HOST:8787/mcp",
      "headers": {
        "Authorization": "Bearer fc_..."
      }
    }
  }
}
```

The tools in this phase are all read-only. There is no shell, exec, or terminal tool.

| Tool | Returns |
| --- | --- |
| `system_info` | Hostname, OS, kernel, CPU, boot time |
| `system_stats` | Latest CPU, memory, load, disks, and network |
| `list_shares` | Shares, with SMB and NFS state |
| `list_containers` | Containers, with image, state, status, and ports |
| `container_logs` | The last lines of one container's logs (`id`, optional `tail`, capped at 500) |
| `list_apps` | App Store apps, or a search (`query`, optional `limit`) |
| `list_vms` | Virtual machines when virsh is installed. Otherwise it says the VM layer is not installed. |

`list_apps` reads the catalogue already cached on the server. It does not download or install anything. `list_vms` uses `virsh -r`, which cannot start or stop a domain.

## Connect with the HTTP API

Send the same secret as a bearer token. A browser session cookie is not required.

```bash
curl -sS -H "Authorization: Bearer fc_..." http://HOST:8787/api/v1/system/info
```

A missing or unknown token is `401`. A token that lacks the scope for that route is `403`. The paths and the scope each one needs are in [[help:api-tokens|API tokens and scopes]].

## What shows up in the audit log

Every call authenticated with a token is listed: token name, action, target, time, and result (`ok`, `denied`, or `error`). Signing in from the browser is not listed. The log keeps the newest 1,000 entries and drops anything older than 90 days.

Blocks from the publish protection layer are in the same log, with the name `protection` and the action `publish.block`.

[[route:audit|Open the audit log]]

## Internal Affairs

Internal Affairs is the cabinet where an agent would file a written report: a change it made, an error it hit, or a note about something it looked into. That is separate from the audit log. The log is the automatic list of calls. Internal Affairs would be the write-up.

It is not in the app. There is no Internal Affairs page, no filing tool, and no `ia:file` or `ia:read` tick on the token form. The button below opens the design note, which is marked coming soon.

[[route:internal-affairs|Open Internal Affairs]]
