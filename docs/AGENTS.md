# Using FELDCORE from an agent

Phase 1 lets the owner's own agents read this server, and call the HTTP API
within the scopes on their token. The MCP server is read-only. Nothing in this
phase can open a shell. Write tools, and an approval queue for destructive
actions, are described in [ROADMAP.md](ROADMAP.md).

The daemon listens on port 8787. Replace `HOST` below with the server's
address.

## Create a token

1. Sign in to FELDCORE in a browser.
2. Open **Settings**, then **Agent access**.
3. Name the token (for example `Claude Code`), pick scopes, and an optional expiry.
4. Create it and copy the secret. It is shown once. FELDCORE stores only a SHA-256 hash, the same way it stores session tokens.

**Revoke** removes one token. **Revoke all agent access** disables every token at once. A revoked token fails the next request.

## Scopes

| Scope | What it allows |
| --- | --- |
| `read` | Look at the host, shares, the App Store, containers, container logs, VMs, Compose stacks (including stack logs and stack job progress), the storage inventory, health status and history, and the notification inbox. Required for MCP. |
| `containers` | Create, start, stop, restart and delete containers over the HTTP API. |
| `vms` | Create, edit, start, stop and delete virtual machines over the HTTP API. Listing VMs is `read`. |
| `files:read` | List, preview and download files in a share. |
| `files:write` | Upload, rename, move, copy and delete files in a share. Includes `files:read`. |
| `stacks` | Save, deploy, update, stop and remove Compose stacks. Listing stacks and reading logs is `read`. |
| `storage` | Create and change pools, mount them by UUID, add or replace a disk, and run scrub, balance and trim. Looking at disks is `read`. |
| `backups:read` | See backup destinations, jobs, copies, and the files inside a copy. |
| `backups:write` | Change destinations and run backups, checks and restores. Includes `backups:read`. |
| `mail` | Read the saved Proton Bridge username, app password, and IMAP and SMTP endpoints. Grant `read` as well for the `proton_bridge` MCP tool. |
| `admin` | Settings, shares, tokens, health thresholds, notification channels, and every other API call. Includes the scopes above. |

`admin` does not include a shell. The host terminal, container consoles and a VM's VNC console stay on the signed-in browser session. An agent token that calls them is refused and the refusal is written to the audit log.

Grant `read` together with `containers` when an agent should both look and change containers. Grant `read` with `stacks` as well: deploy returns a job immediately, and following it is a read. A token with only `containers` or only `stacks` cannot call MCP and cannot read system info. Stack files, the security check and the on-disk path are described in [COMPOSE.md](COMPOSE.md).

## Call the HTTP API

Send the secret as a bearer token. A browser session cookie is separate and is not required.

```bash
curl -sS -H "Authorization: Bearer fc_..." http://HOST:8787/api/v1/system/info
```

A missing or unknown token is `401`. A token that lacks the scope for that route is `403`. The signed-in UI is unchanged: a session cookie is still the administrator.

## Connect Claude Code

The MCP endpoint is `http://HOST:8787/mcp`. It speaks the streamable HTTP transport and requires the same bearer token on every request, including the session stream. A session cookie is not accepted.

```bash
claude mcp add --transport http feldcore http://HOST:8787/mcp --header "Authorization: Bearer fc_..."
```

The token needs the `read` scope (`admin` includes it).

## Other MCP clients

Any client that speaks streamable HTTP can use the same URL. Send this header on every request:

```
Authorization: Bearer fc_...
```

A typical client entry:

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

That shape is what Cursor and other streamable-HTTP clients expect. The header name and the URL are the part that matters: if a client uses a different key for headers, the request that reaches FELDCORE still has to carry `Authorization`.

## Tools in phase 1

All of them are read-only. There is no shell, exec, or terminal tool.

| Tool | What it returns |
| --- | --- |
| `system_info` | Hostname, OS, kernel, CPU, boot time |
| `system_stats` | Latest CPU, memory, load, disks and network |
| `list_shares` | Shares, with SMB and NFS state |
| `list_containers` | Containers, with image, state, status and ports |
| `container_logs` | The last lines of one container's logs (`id`, optional `tail`, capped at 500) |
| `list_apps` | App Store apps, or a search (`query`, optional `limit`) |
| `list_vms` | Virtual machines when virsh is installed. Otherwise it says the VM layer is not installed. Listing uses `virsh -r`, which cannot start or stop a domain. |
| `health_report` | Latest health checks: SMART, temperatures, disk space, load, containers, VMs, failed units, certificates, and tunnels. |
| `proton_bridge` | Saved Proton Bridge username, app password, and IMAP and SMTP endpoints. The token needs `mail` as well as `read`. The tool does not sign in and does not stop the container. |

`list_apps` reads the catalogue already cached on the server. It does not download or install anything. Sign-in for Proton Bridge stays in the container console. See [PROTON.md](PROTON.md).

## Audit

**Audit** in the sidebar lists every call authenticated with a token, every package install started from the UI, health checks started by hand, health-threshold and notification setting changes, every published request the protection layer blocked, and Citizen ID sign-ins, failures, and access denials. A row shows the name (or `admin` for an install or a browser setting change, `protection` for a block), action, target, time and result (`ok`, `denied` or `error`). An install writes `install started`, then `install finished` or `install failed`. A block writes `publish.block`. Anonymous sign-in noise is aggregated so it cannot push those entries out of the log. The log keeps the newest 1,000 entries, and drops anything older than 90 days.

## If a token leaks

Revoke it, or use **Revoke all agent access**, and create a new one. The old secret cannot be looked up from the server, because it was never stored.
