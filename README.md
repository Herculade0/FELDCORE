# FELDCORE

A modern, self-hostable server management UI — the parts of the Unraid
experience that are useful on *any* Linux box, minus the array/parity storage
model. Drop one binary onto a Debian (or any systemd) machine and you get a
clean web UI to manage the system, shares, Docker, and later VMs.

> **Status: pre-alpha.** The dashboard, sign-in, Shares and Docker work. The
> Docker app store and VM management are next — see
> [docs/ROADMAP.md](docs/ROADMAP.md).

## Why

Unraid's web UI is, underneath, a friendly wrapper over standard Linux
primitives: a share is a directory exported via Samba/NFS, the "app store" is a
catalog of Docker container templates, and VMs are libvirt/KVM domains.
FELDCORE rebuilds the *good parts* of that experience as a modern app that runs
anywhere — no custom OS, no licence, no disk/parity lock-in.

## What it is

- **Single binary.** The Go daemon (`feldcored`) embeds the compiled web UI, so
  deployment is one file plus a systemd unit. No runtime to install on targets.
- **Modern UI.** React + TypeScript + Tailwind, live metrics over WebSocket.
- **Thin over the OS.** Features are implemented by generating standard service
  configs (Samba, NFS) and driving `systemd` and the Docker/libvirt APIs — not
  by reinventing them.

## Features

| Area | Status |
|------|--------|
| **Dashboard** — live CPU, memory, load, uptime, per-mount storage, network throughput, per-core | ✅ working |
| **Sign-in** — administrator account, sessions, everything behind a login | ✅ working |
| **Shares** — create a folder, expose it over SMB (public, read-only or private with users) and NFS; installs Samba/NFS for you | ✅ working |
| **Docker** — run and manage containers (state, live stats, logs, lifecycle), create from any image; installs Docker for you | ✅ working |
| **Docker app store** — one-click installs from the Unraid Community Applications catalogue | 🔜 next |
| **Settings** — users/auth, network, services, appearance | 🔜 planned |
| **VMs** — KVM/libvirt management | 🧭 later |

## Architecture

```
┌──────────────────────────────────────────────┐
│ feldcored (single Go binary)                   │
│                                                │
│  ┌───────────────┐   ┌──────────────────────┐  │
│  │ web UI (React) │◄──│ REST + WS API /api/v1 │  │
│  │ embedded       │   └──────────┬───────────┘  │
│  └───────────────┘              │              │
│                     ┌───────────┴───────────┐  │
│                     │ system / shares /       │  │
│                     │ docker / vm managers    │  │
│                     └───────────┬───────────┘  │
└─────────────────────────────────┼──────────────┘
                                  ▼
          /proc · systemd · smbd/nfsd · docker.sock · libvirt
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for detail.

## Development

Requires Go 1.24+ and Node 20+.

```bash
# Terminal 1 — back-end (serves API on :8787)
go run ./cmd/feldcored -data-dir ./.data

# Terminal 2 — front-end dev server with hot reload (proxies /api to :8787)
npm --prefix web install
npm --prefix web run dev
```

Without root the daemon runs and the UI works, but it cannot change file
sharing on the machine; the Shares page says so. To exercise shares for real,
run the built binary as root (`sudo ./bin/feldcored`).

Build the production single binary (UI embedded):

```bash
make build        # builds web, embeds it, compiles ./bin/feldcored
```

### Tests

```bash
go test ./...                              # unit tests, safe anywhere
sudo scripts/integration-shares.sh         # end-to-end against real Samba/NFS
sudo scripts/integration-docker.sh         # end-to-end against real Docker
```

The integration script installs packages and edits Samba configuration. Read
the warning at the top of the file and run it only on a development machine
or a disposable VM.

## What FELDCORE changes on your system

- State lives in `/var/lib/feldcore/state.json` (root-only).
- Share folders are created under `/srv/feldcore` (`-shares-root` to change).
- Samba: one line, `include = /etc/samba/feldcore-shares.conf`, is appended to
  `/etc/samba/smb.conf` the first time a share needs it. The original is saved
  as `smb.conf.feldcore-backup`. FELDCORE only ever rewrites its own included
  file, and validates with `testparm` before reloading.
- NFS: exports are written to `/etc/exports.d/feldcore.exports`.
- Share users are unix accounts with no home and no login shell. FELDCORE only
  modifies or removes accounts it created.
- Deleting a share never deletes files; only an empty folder is removed.
- Docker is used over its unix socket (`/var/run/docker.sock`). The one-click
  install adds your distribution's `docker.io` package. Containers FELDCORE
  creates are tagged with a `feldcore.managed=true` label; it never alters
  containers you started yourself.

## Layout

```
cmd/feldcored      daemon entrypoint
internal/system    live host metrics (gopsutil)
internal/api       HTTP/WebSocket surface + SPA serving
internal/auth      administrator account + sessions
internal/store     JSON state file (atomic writes)
internal/shares    SMB/NFS share management
internal/docker    Docker Engine API client + container management
scripts/           integration tests
web/               React front-end (Vite + Tailwind), embedded via go:embed
deploy/            install.sh + systemd unit
docs/              architecture & roadmap
```

## Licence

MIT — see [LICENSE](LICENSE).
