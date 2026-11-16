# FELDCORE architecture

## Principle: a thin UI over standard Linux

FELDCORE does not invent its own storage, networking, or container runtime. It
is a management layer that:

1. **Reads** system state from standard sources (`/proc`, `/sys`, the Docker and
   libvirt sockets).
2. **Writes** standard service configuration (Samba, NFS exports, container
   definitions).
3. **Controls** the relevant daemons through `systemd` and native APIs.

This is the same trick Unraid uses. Its payoff: everything FELDCORE manages is
inspectable and recoverable with ordinary Linux tools, and it runs on any
distro instead of a bespoke OS.

## Components

### `feldcored` (Go, single binary)
The whole product ships as one executable with the web UI embedded via
`go:embed`. One file + a systemd unit is the entire install.

- **`internal/config`** — flags/env resolution.
- **`internal/system`** — live metrics via gopsutil; keeps minimal state
  (previous network counters) to derive per-second rates.
- **`internal/api`** — chi router. `/api/v1/*` JSON endpoints plus a WebSocket
  stream, and a read-only MCP server at `/mcp`. All other paths serve the
  embedded SPA with an index.html fallback.
- **`internal/shares`** *(wip)* — model a share as a managed directory; render
  Samba and NFS config fragments; reload services.
- **`internal/docker`** — talk to the Docker Engine API over its unix socket.
- **`internal/apps`** — the App Store. It caches the public Community
  Applications feed (the same JSON Unraid's CA plugin downloads, which already
  contains each template) and turns a chosen template into a container spec.

### Web UI (React + TypeScript + Vite + Tailwind)
A single-page app. Live data arrives over the `/api/v1/system/stream`
WebSocket; the client auto-reconnects with backoff. Built to static assets and
embedded in the binary, so there is no separate web server to run.

## Design decisions

- **Go single binary** — the deployment story is "copy one file." No runtime
  dependency on the target machine.
- **Config generation, not config ownership** — FELDCORE writes to drop-in
  locations (e.g. an included Samba conf, `/etc/exports.d/`) so its output sits
  beside, and never clobbers, hand-written config.
- **systemd as the control plane** — start/stop/reload of smbd, nfs, docker,
  libvirt all go through systemd, which every target already runs.

## Planned subsystems

### Shares
- A share = a directory under a configured root, with metadata (name, comment,
  per-protocol toggles, access rules).
- Enabling SMB renders a stanza into a FELDCORE-managed include file and runs
  `systemctl reload smbd`. Enabling NFS writes an `/etc/exports.d/feldcore.exports`
  entry and runs `exportfs -ra`.

### Docker + App Store
- Container lifecycle via the Docker Engine API (list/create/start/stop/logs).
- The App Store reads the public Community Applications JSON feed, caches it,
  and converts a chosen template into the container setup form. The user
  reviews it and applies; nothing is installed automatically. Unraid paths
  such as `/mnt/user/appdata` are rewritten onto this server's app-data folder.

### VMs
- libvirt/KVM domains. FELDCORE writes the domain XML and applies it with
  `virsh` (no cgo). The browser console is noVNC over an authenticated
  WebSocket to a VNC server bound to localhost. See [VMS.md](VMS.md).

## Security model

The daemon runs as root because its job is to edit system configuration,
control services and create accounts. Everything it exposes is therefore
behind authentication:

- **One administrator account**, created on first run. Until it exists, sign-in
  is closed and the setup wizard's host and network steps are gated by the
  one-time setup code; afterwards `/auth/setup` is refused. A fresh install
  should be set up promptly, since whoever has the setup code can claim it.
- **Passwords** are hashed with bcrypt. The setup wizard asks for a strong
  password. An optional authenticator (TOTP) can be turned on for the
  administrator sign-in; the secret and recovery-code hashes live in the
  secrets box, not in the state file. Failed sign-ins are rate limited per
  client address.
- **Sessions** are random 256-bit tokens. Only a SHA-256 hash is stored, so a
  leaked state file does not yield usable sessions. The cookie is HttpOnly and
  SameSite=Strict, and Secure when served over TLS.
- **Agent tokens** are a second credential, for the owner's own agents. Each
  is named, scoped (`read`, `containers`, `vms`, `admin`), and optional
  expiry. The secret is shown once. Only a SHA-256 hash is stored. Callers
  send `Authorization: Bearer`. A session cookie is still the administrator
  and is not limited by scopes. A token is. Admin does not include a shell:
  the host terminal and container consoles check the session cookie, and the
  scope check refuses them first.
- **MCP** is served at `/mcp` on the streamable HTTP transport. It requires a
  bearer token with the `read` scope (admin includes read). A session cookie
  is not accepted. Phase 1 tools are read-only. There is no shell or exec tool.
- **Audit**: every call authenticated with a token is recorded (token name,
  action, target, time, result). The newest 1,000 entries are kept, and
  anything older than 90 days is dropped.
- **Cross-site requests**: state-changing requests whose Origin does not match
  the Host are refused, and the WebSocket handshake enforces the same.
  Non-browser clients send no Origin and are unaffected.
- **Every `/api/v1` route except health and the login flow requires a session
  or a bearer token**, including read-only metrics.

Input that ends up in system configuration is validated against strict
patterns (share names, user names, NFS clients) and free text is sanitised, so
a request cannot inject extra Samba or exports directives. Commands are run
directly with argument lists, never through a shell, and passwords are passed
on stdin rather than the command line.

Not yet done: TLS termination (run behind a reverse proxy, or on a trusted
LAN, for now), multiple users and roles, and an audit log of the changes
FELDCORE itself makes to the system. Agent-token calls are already logged.
