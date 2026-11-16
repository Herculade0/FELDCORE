# FELDCORE

A modern, self-hostable server management UI — the parts of the Unraid
experience that are useful on *any* Linux box, minus the array/parity storage
model. Drop one binary onto a Debian (or any systemd) machine and you get a
clean web UI to manage the system, shares, Docker, and later VMs.

> **Status: pre-alpha.** The dashboard, sign-in, Shares, Docker and the App
> Store work. VM management is next — see
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
| **Docker** — run, edit and manage containers (state, live stats, logs, lifecycle); Unraid-style paths/ports/variables/labels/devices, a folder picker, GPU sharing and an allocations table; installs Docker for you | ✅ working |
| **App Store** — browse and search the Unraid Community Applications catalogue; choosing an app opens the container setup prefilled from its template | ✅ working |
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
- On startup as root, FELDCORE creates Unraid's four default shares when
  they are missing: `appdata`, `system`, `domains` and `isos`. SMB and NFS
  are left off, so nothing is exported until you turn a protocol on. A share
  or folder that already has one of those names is kept as it is; an existing
  folder is adopted as the share and its files are not changed. `media` and
  `data` are not created. Pass `-default-shares=false` or set
  `FELDCORE_DEFAULT_SHARES=0` to skip this. Without root, the defaults are
  not created.
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
  creates are tagged with a `feldcore.managed=true` label. It only changes a
  container when you edit, start, stop or remove it yourself, and refuses to
  edit ones that belong to a Docker Compose project.
- Editing a container replaces it, because Docker cannot change ports, paths
  or variables in place. The original is kept until the replacement has
  started, and is restored if it does not. Files in mapped folders survive;
  data stored only inside the container does not.
- The App Store keeps a copy of the Unraid Community Applications catalogue in
  the data directory (`ca-feed.json`) and only downloads it when you refresh,
  or the first time, if nothing is saved. Picking an app fills in the
  container form; it does not install it.
- Template paths under `/mnt/user/appdata` are rewritten to the `appdata`
  share (created with the other default shares, above), or to whatever
  folder you set under Settings.
- GPU sharing passes `/dev/dri` into the container and adds the groups that
  own it. Nothing is installed for this.

## Layout

```
cmd/feldcored      daemon entrypoint
internal/system    live host metrics (gopsutil)
internal/api       HTTP/WebSocket surface + SPA serving
internal/auth      administrator account + sessions
internal/store     JSON state file (atomic writes)
internal/shares    SMB/NFS share management
internal/docker    Docker Engine API client + container management
internal/apps      App Store: cached Community Applications catalogue
scripts/           integration tests
web/               React front-end (Vite + Tailwind), embedded via go:embed
deploy/            install.sh + systemd unit
docs/              architecture & roadmap
```

## Licence

MIT — see [LICENSE](LICENSE).
