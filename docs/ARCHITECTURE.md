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
  stream; all other paths serve the embedded SPA with an index.html fallback.
- **`internal/shares`** *(wip)* — model a share as a managed directory; render
  Samba and NFS config fragments; reload services.
- **`internal/dockermgr`** *(wip)* — talk to the Docker Engine API over its unix
  socket; ingest Unraid Community Applications templates into the app store.

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

### Docker + app store
- Container lifecycle via the Docker Engine API (list/create/start/stop/logs).
- The "store" ingests the Unraid **Community Applications** template feed
  (XML: image, ports, volumes, env, icon, categories) and converts a chosen
  template into a container create request.

### VMs (later)
- libvirt/KVM domains managed through the libvirt API; VNC/SPICE console in the
  browser.

## Security model

The daemon runs as root because its job is to edit system configuration,
control services and create accounts. Everything it exposes is therefore
behind authentication:

- **One administrator account**, created on first run. Until it exists the API
  only offers the setup endpoint; afterwards setup is refused. A fresh install
  should be set up promptly, since whoever reaches it first claims it.
- **Passwords** are hashed with bcrypt. Failed sign-ins are rate limited per
  client address.
- **Sessions** are random 256-bit tokens. Only a SHA-256 hash is stored, so a
  leaked state file does not yield usable sessions. The cookie is HttpOnly and
  SameSite=Strict, and Secure when served over TLS.
- **Cross-site requests**: state-changing requests whose Origin does not match
  the Host are refused, and the WebSocket handshake enforces the same.
- **Every `/api/v1` route except health and the login flow requires a
  session**, including read-only metrics.

Input that ends up in system configuration is validated against strict
patterns (share names, user names, NFS clients) and free text is sanitised, so
a request cannot inject extra Samba or exports directives. Commands are run
directly with argument lists, never through a shell, and passwords are passed
on stdin rather than the command line.

Not yet done: TLS termination (run behind a reverse proxy, or on a trusted
LAN, for now), multiple users and roles, and an audit log.
