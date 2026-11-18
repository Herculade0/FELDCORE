# FELDCORE

FELDCORE is provided as is. Downloading, installing or running it is at your own risk.

A modern, self-hostable server management UI — the parts of the Unraid
experience that are useful on *any* Linux box, minus the array/parity storage
model. It ships two ways: one binary you drop onto a Debian (or any systemd)
machine, and a bootable OS image. Either way you get a web UI for the
system, shares, Docker and virtual machines.

> **Status: pre-alpha.** The dashboard, sign-in, first-start setup, Shares,
> Docker, the App Store, virtual machines, and the Updates page work. See
> [docs/VMS.md](docs/VMS.md) for the VM manager. The bootable image is
> [below](#feldcore-os-bootable-image).

## Why

Unraid's web UI is, underneath, a friendly wrapper over standard Linux
primitives: a share is a directory exported via Samba/NFS, the "app store" is a
catalog of Docker container templates, and VMs are libvirt/KVM domains.
FELDCORE rebuilds the *good parts* of that experience as a modern app. There
is no licence and no disk/parity lock-in. The same daemon runs on a Debian or
systemd machine you already have, and it is also the system on the bootable
FELDCORE OS image.

## What it is

- **Single binary.** The Go daemon (`feldcored`) embeds the compiled web UI, so
  deployment onto an existing machine is one file plus a systemd unit. No
  runtime to install on targets.
- **Bootable OS image.** FELDCORE OS is a read-only Debian disk. UEFI firmware,
  systemd-boot, two root slots. The image runs the same `feldcored` daemon.
- **Modern UI.** React + TypeScript + Tailwind, live metrics over WebSocket.
- **Thin over the OS.** Features are implemented by generating standard service
  configs (Samba, NFS) and driving `systemd` and the Docker/libvirt APIs — not
  by reinventing them.

## Features

| Area | Status |
|------|--------|
| **Dashboard** — live CPU, memory, load, uptime, per-mount storage, network throughput, per-core | ✅ working |
| **Sign-in** — administrator account, sessions, everything behind a login | ✅ working |
| **First-start setup** — one-time claim code, then host, network, storage, packages, default shares, firewall and update checks | ✅ working |
| **Shares** — create a folder, expose it over SMB (public, read-only or private with users) and NFS; installs Samba/NFS for you | ✅ working |
| **Docker** — run, edit and manage containers (state, live stats, logs, lifecycle); Unraid-style paths/ports/variables/labels/devices, a folder picker, GPU sharing and an allocations table; installs Docker for you | ✅ working |
| **Stacks** — paste, upload or fetch a Compose file, check it, and deploy, update, stop or remove it; each stack is on its own network unless you say otherwise | ✅ working |
| **App Store** — browse and search the Unraid Community Applications catalogue; choosing an app opens the container setup prefilled from its template | ✅ working |
| **Settings** — appearance, host, network, services, firewall, updates, App Store feed, agent tokens | ✅ working |
| **Agent access** — scoped bearer tokens, a read-only MCP server, and an audit log | ✅ working |
| **Terminal** — host shell, and a console in each running container. Agent tokens cannot open it | ✅ working |
| **VMs** — KVM/libvirt: create and edit, CPU pinning, UEFI/TPM, disks, ISO and VirtIO drivers, VNC in the browser, passthrough, snapshots. Agent tokens cannot open the console | ✅ working |
| **Updates** — FELDCORE OS image: check a minisign-signed release, write it to the inactive A/B slot, and boot the previous image after three failed tries. On a Debian install the page can check the release; staging runs on the OS image | ✅ working |

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

## Install

Two paths. The single binary is copied onto a machine that already runs
Debian or another systemd distribution. The OS image is a disk you boot.

### On a Debian or systemd machine

A new Debian machine does not need Go or Node installed. Once a GitHub
release exists:

```bash
curl -fsSL https://raw.githubusercontent.com/Herculade0/FELDCORE/main/deploy/get.sh | sudo sh
```

The installer prints a one-time setup code. The same code is written to the
journal (`journalctl -u feldcore -b`) and stored in
`/var/lib/feldcore/setup-code`, readable only by root. Open
`http://<this-server>:8787`. The guided setup asks for that code on every
step until the administrator account exists. The code stops working after
that, so another machine on the LAN cannot claim the server first.

Closing the page, or rebooting, continues at the same step. Optional steps
can be skipped. Each step that changes the machine shows what it will do
before it does it:

1. Welcome, including the setup code.
2. Host name, time zone, locale and NTP (`systemd-timesyncd`, or chrony).
3. The current interface and address. DHCP stays as it is unless you choose a
   static address. DNS can be checked without rewriting the DHCP configuration.
   A static address shows the new URL before applying it. A wrong address
   drops the page; fix it from the console, or start setup over.
4. The administrator account, with a strong password. An authenticator app
   (TOTP) is optional and encouraged. Recovery codes are shown once. The
   secret is not stored in `state.json`.
5. Where share folders live. You can keep a folder, use one disk, or record a
   pool name. Formatting one empty disk is possible only after you type the
   confirmation phrase, and the operating-system disk cannot be formatted.
   Assembling several disks into one pool is not done in this step.
6. Remote access: none, Cloudflare Tunnel, or WireGuard.
7. A first notification target: email or ntfy. A test is sent before the step
   continues. Skipping does not turn an existing notification off.
8. A Proton Drive backup of app data, or skip. Skipping does not turn an
   existing backup off.
9. A summary. Finishing creates `appdata`, `system`, `domains` and `isos` if
   they are missing. SMB and NFS stay off.

Package install, the firewall and update checks stay in Settings. From
Settings → General you can run the non-destructive steps again (host, network,
sign-in, remote access, notifications, backups). That replay does not format
a disk and does not create another administrator.

For a scripted install, start the daemon with `-skip-wizard` (or
`FELDCORE_SKIP_WIZARD=1`). The setup code is still required to create the
account. With the wizard skipped, `-default-shares` (the default) creates
`appdata`, `system`, `domains` and `isos` on startup.

## FELDCORE OS (bootable image)

FELDCORE OS is an immutable amd64 disk image. You boot it on bare metal.
The firmware is UEFI and the bootloader is systemd-boot. Secure Boot is off.
The root is Debian 13 (trixie), an ext4 filesystem mounted read-only. `apt`,
`apt-get`, and the other apt commands are replaced with a script that exits,
so packages are not installed onto the running system. State is a separate
partition labeled `feldcore-state`, mounted at `/var/lib/feldcore`, including
an overlay for `/etc`. An image swap leaves that partition in place. The
daemon is `/usr/bin/feldcored` and listens on port 8787, the same UI as the
single-binary install.

The GPT disk has an EFI system partition (`FC-ESP`), slot A (`feldcore-a`),
slot B (`feldcore-b`), and the state partition. The first image is written
to slot A. Slot B is empty until an update is staged.

`os/build.sh` builds the appliance disk, a raw installer disk, a hybrid
installer ISO, and a signed manifest. The default root is debootstrap.
`FELDCORE_IMAGE_BUILDER=mkosi` builds the same package list from
`os/mkosi/mkosi.conf`. It does not build the ISO. Output is `dist/os` unless
`FELDCORE_OS_OUT` is set:

| File | What it is |
|------|------------|
| `feldcore-os.raw.zst` | Bootable appliance disk (zstd-compressed raw GPT image) |
| `feldcore-installer.raw.zst` | Bootable raw installer disk |
| `feldcore-installer.iso` and `feldcore-installer.iso.minisig` | Hybrid UEFI installer. When the ISO is at or over 2 GiB the release has `feldcore-installer.iso.zst` and its `.minisig` instead. The ISO is not listed in `feldcore-os.json`. |
| `feldcore-root.img.zst`, `vmlinuz`, `initrd.img` | The slot image, kernel, and initrd an update writes |
| `feldcore-os.json`, `feldcore-os.json.minisig` | Manifest and its minisign signature |
| `update.pub` | Public key for that signature and for the ISO signature |

The installer does not install itself onto the disk it booted from.
`feldcore-install --yes /dev/disk` writes the appliance layout onto another
whole disk. It refuses a partition, the installer medium, and any disk that
has a mounted filesystem.

### Installing it (USB) and unattended setup

Flash `feldcore-installer.iso` with
[balenaEtcher](https://etcher.balena.io/), or copy the `.iso` onto a Ventoy
stick and boot that file. If the release asset is `feldcore-installer.iso.zst`,
check the signature and decompress it first (`minisign -V -p update.pub -m
feldcore-installer.iso.zst`, then `zstd -d`). Ventoy does not decompress.

Put `feldcore-install.json` on the Ventoy data partition, or on a second
FAT or exFAT stick, at `feldcore/feldcore-install.json` or at the volume
root. Boot the installer. On the console (the HDMI tty on a Mac, or the
serial console under QEMU):

```
feldcore-install --yes /dev/disk
```

Name the other whole disk. The command copies the answer file onto the new
system's EFI partition. With no answer file, the appliance prints a setup
code and the browser wizard runs after it boots. The copy on the Ventoy
partition or the second stick is left in place.

On its first boot — before it starts listening — `feldcored` reads the file
once and applies each section it finds: hostname, network (including WiFi),
the administrator account or setup code, SSH, and the data pool. WiFi
(`wpasupplicant`) and SSH (`openssh-server`) are in the image; `sshd` stays
off until the answer file turns it on, and it is public-key only (root
password locked). A missing section is skipped.

> The administrator password, the setup code and the WiFi password sit in
> **clear text** on the USB. Prefer an SSH key, and delete the file (or wipe the
> stick) after the first boot.

Build the answer file in your browser with the install form at
[herculade0.github.io/FELDCORE/install](https://herculade0.github.io/FELDCORE/install/).
It runs entirely client-side — nothing is uploaded — and downloads a
`feldcore-install.json` you put on the Ventoy partition or the second stick. The schema is in
[docs/FELDCORE-INSTALL-ANSWER.md](docs/FELDCORE-INSTALL-ANSWER.md).

A release is a git tag matching `v*`. Pushing the tag runs
[`.github/workflows/os-image.yml`](.github/workflows/os-image.yml) and
[`.github/workflows/release.yml`](.github/workflows/release.yml). Neither
workflow creates the tag. The OS workflow builds the image, sets the version
from the tag (the leading `v` is removed), and minisign-signs
`feldcore-os.json` with `cmd/osmanifest`. It publishes the files above on
that tag's GitHub Release. The binary workflow publishes `feldcored` for
linux/amd64 and linux/arm64 on the same release. The OS image is amd64.
Publishing the image on the release requires the signing key configured for
the OS workflow. If that key is not available, the image is uploaded as a
workflow artifact and the OS job fails before it attaches those files to
the release.

`os/build.sh` installs the public half of the signing key at
`/usr/share/feldcore/update.pub`. It uses `FELDCORE_UPDATE_KEY` when that
path is set, and otherwise generates a key under the work directory
(`/var/tmp/feldcore-os` unless `FELDCORE_OS_WORK` is set). The private half
stays out of the image and out of `dist/os`. The Updates page (nav key
`updates`) downloads
`https://github.com/Herculade0/FELDCORE/releases/latest/download/feldcore-os.json`
and verifies `feldcore-os.json.minisig` with the public key baked into the
image. A file at `/var/lib/feldcore/update.pub` is used first when it is
present. `FELDCORE_UPDATE_PUBKEY`, when set, is the only key that is
accepted. `FELDCORE_UPDATE_URL`, when set, is the manifest address and the
page cannot change it; otherwise a saved address is used, then the URL
above. The boot entry is written only after the decoded root, kernel, and
initrd match the SHA-256 and the size in the signed manifest.

**Update now** streams the new root onto the inactive slot and adds a
systemd-boot entry with three tries (`feldcore-b+3.conf` when slot B is
inactive). The running slot is left as it is. A boot that reaches userspace
runs `feldcore-bless.service`, which drops the counter and pins that slot.
systemd-boot counts the tries down. After three failed boots the entry
sorts last and the previous image is selected. **Roll back** selects the
earlier image for the next start. Reboot is a separate button, shown when
the next boot is another slot. A schedule is one time. It is cleared when
the update starts.

On a machine that is not this image, including the Debian single-binary
install, Updates can check a signed release. Staging, rollback, restart,
and extensions stay disabled.

Extensions on that page are signed systemd-sysext images stored on the
state partition. libvirt and the NVIDIA container toolkit are offered when
a release includes them.

The layout, the manifest, and the update jobs are in
[docs/OS-IMAGE.md](docs/OS-IMAGE.md).

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

On the single-binary install, the daemon changes the host as listed below.
On FELDCORE OS the root is read-only and apt is not available; the OS
changes when an image is swapped. State is still
`/var/lib/feldcore/state.json`.

- State lives in `/var/lib/feldcore/state.json` (root-only).
- Share folders are created under `/mnt/feldcore/data` (`-shares-root` to change).
- On a fresh install the setup wizard creates Unraid's four default shares
  when you finish: `appdata`, `system`, `domains` and `isos`. SMB and NFS
  are left off, so nothing is exported until you turn a protocol on.
  A share or folder that already has one of those names is kept as it is; an
  existing folder is adopted and its files are not changed. `media` and
  `data` are not created. The wizard is skipped with `-skip-wizard`; then
  `-default-shares` (on unless you set `-default-shares=false` or
  `FELDCORE_DEFAULT_SHARES=0`) creates the same four shares at startup.
  Without root, the defaults are not created.
- The one-time setup code is deleted after the administrator account is
  created. A static network change writes a FELDCORE drop-in for
  systemd-networkd or ifupdown and reloads that interface. It does not
  switch the address back if the new one is unreachable.
  Choosing the firewall in Settings installs ufw and adds allow rules only;
  it does not change sshd. Choosing automatic security updates in Settings
  installs `unattended-upgrades` and turns on the Debian security-update
  schedule. Formatting a data disk in the wizard runs `mkfs.ext4` only after
  the confirmation phrase matches, and refuses the operating-system disk.
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
  edit ones that belong to a Docker Compose project. Change those from
  **Stacks**. The files are kept in `/var/lib/feldcore/compose` (or
  `$FELDCORE_DATA_DIR/compose`) and backups include that folder. See
  [docs/COMPOSE.md](docs/COMPOSE.md).
- Editing a container replaces it, because Docker cannot change ports, paths
  or variables in place. The original is kept until the replacement has
  started, and is restored if it does not. Files in mapped folders survive;
  data stored only inside the container does not.
- The App Store keeps a copy of the Unraid Community Applications catalogue in
  the data directory (`ca-feed.json`) and only downloads it when you refresh,
  or the first time, if nothing is saved. Picking an app fills in the
  container form; it does not install it.
- Template paths under `/mnt/user/appdata` are rewritten to the `appdata`
  share (created with the other default shares, above).
- GPU sharing passes `/dev/dri` into the container and adds the groups that
  own it. Nothing is installed for this.
- The host terminal and each container console are a shell for the signed-in
  administrator only. Agent tokens cannot open them. The host shell is closed
  when the window closes, or after 15 minutes with no input. A container
  console is `docker exec` into that container (bash, or sh if bash is not
  there) and is only offered while the container is running.
- Agent tokens are created under Settings → Agent access. The secret is shown
  once and only a SHA-256 hash is stored. The same bearer token calls the
  HTTP API (scopes are enforced) and the read-only MCP server at `/mcp`.
  How to connect Claude Code and other clients is in
  [docs/AGENTS.md](docs/AGENTS.md). Every token call is listed on the Audit
  page.

## Layout

```
cmd/feldcored      daemon entrypoint
cmd/osmanifest     build and minisign-sign the OS update manifest
internal/system    live host metrics (gopsutil)
internal/api       HTTP/WebSocket surface, MCP at /mcp, SPA serving
internal/auth      administrator account, sessions and agent tokens
internal/vm        read-only libvirt domain list, when virsh is installed
internal/store     JSON state file (atomic writes)
internal/shares    SMB/NFS share management
internal/docker    Docker Engine API client + container management
internal/compose   Compose stacks: files, checks, and the Compose v2 CLI
internal/apps      App Store: cached Community Applications catalogue
internal/setup     guided setup wizard and the one-time claim code
internal/storage   disk inventory the wizard calls (single-disk format only)
internal/osupdate  signed A/B OS image updates
os/                FELDCORE OS image build (`build.sh`, mkosi config, installer)
scripts/           integration tests
web/               React front-end (Vite + Tailwind), embedded via go:embed
deploy/            install.sh, get.sh and the systemd unit
.github/workflows  binary release and OS image (tag v*; neither runs until you push a tag)
docs/              architecture, the OS image, roadmap, and agent access
```

## Licence

MIT — see [LICENSE](LICENSE).
