# FELDCORE

A modern, self-hostable server management UI — the parts of the Unraid
experience that are useful on *any* Linux box, minus the array/parity storage
model. Drop one binary onto a Debian (or any systemd) machine and you get a
clean web UI to manage the system, shares, Docker, and later VMs.

> **Status: pre-alpha.** The dashboard is real and live. Shares, Docker, and VM
> management are in progress — see [docs/ROADMAP.md](docs/ROADMAP.md).

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
| **Shares** — create a managed directory, toggle SMB/NFS per share | 🔜 next |
| **Docker** — manage containers + an app store backed by Unraid Community Applications templates | 🔜 planned |
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
go run ./cmd/feldcored

# Terminal 2 — front-end dev server with hot reload (proxies /api to :8787)
npm --prefix web install
npm --prefix web run dev
```

Build the production single binary (UI embedded):

```bash
make build        # builds web, embeds it, compiles ./bin/feldcored
```

## Layout

```
cmd/feldcored      daemon entrypoint
internal/system    live host metrics (gopsutil)
internal/api       HTTP/WebSocket surface + SPA serving
internal/shares    SMB/NFS share management        (wip)
internal/dockermgr container + app-store management (wip)
web/               React front-end (Vite + Tailwind), embedded via go:embed
deploy/            install.sh + systemd unit
docs/              architecture & roadmap
```

## Licence

MIT — see [LICENSE](LICENSE).
