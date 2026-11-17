# FELDCORE OS image

FELDCORE OS is an immutable appliance. It boots from a disk partition, not from a USB stick. The running system is a read-only Debian root. Packages are not installed onto it. An update replaces the inactive A/B slot and the next boot tries that image. Config and state stay on a separate partition mounted at `/var/lib/feldcore`.

Secure Boot is off. The firmware is UEFI only, and the bootloader is systemd-boot.

## Disk layout

The appliance image is a GPT disk:

| Partition | Label | Size | Mount |
| --- | --- | --- | --- |
| ESP (type EF00) | `FC-ESP` | 256 MiB, FAT32 | `/efi` |
| Slot A (type 8300) | `feldcore-a` | root image plus 128 MiB | `/` when that slot is selected |
| Slot B (type 8300) | `feldcore-b` | same size as slot A | `/` when that slot is selected |
| State (type 8300) | `feldcore-state` | 8 GiB, ext4 | `/var/lib/feldcore` |

The type is the generic Linux type, not the discoverable root type. Two partitions of the discoverable root type make gpt-auto pick one of them at random. The kernel command line names the slot instead:

```
root=LABEL=feldcore-SLOT ro console=tty0 console=ttyS0,115200 feldcore.slot=SLOT feldcore.version=VERSION
```

`ro` mounts the root read-only. `/etc/fstab` has no root line, so systemd does not remount it read-write. `/tmp`, `/var/tmp`, `/var/log`, `/var/cache`, `/var/lib/systemd`, and `/home` are tmpfs. The ESP and the state partition are the writable disks. After the state partition is mounted, `feldcore-state.service` overlays `/etc` with an upper directory on that partition, so host configuration (Samba, NFS, ufw, hostname, Docker's daemon.json) survives a reboot without making the OS image writable. `/var/lib/samba` and `/var/lib/nfs` are bind-mounted onto the state partition. Docker's data-root is `/var/lib/feldcore/docker` and containerd's root is `/var/lib/feldcore/containerd`.

The first image ships a boot entry for slot A only. Slot B is an empty ext4 filesystem until an update is staged. The state partition is not inside the root image, so an update does not wipe it.

`/var/lib/extensions` points at `/var/lib/feldcore/sysexts`. systemd-sysext merges images from there at boot.

## Boot counting and rollback

Boot entries are type-1 loader files, not UKIs. systemd-boot 255 reads a counter in the file name. A newly staged image is `feldcore-b+3.conf`. Each failed boot renames it to `feldcore-b+2-1.conf`, then `+1-2`, then `+0-3`. An entry with zero tries left sorts after a good entry, so the previous image is selected. The counter is not skipped when it is the only entry.

The menu does not pin `default` to the counted name. Both entries use `sort-key feldcore`, and the higher version sorts first. Staging a new image clears `default`, so version order selects the new image while it still has tries left. After three failed boots the `+0` entry sorts last and the previous good entry boots.

A boot that reaches userspace runs `feldcore-bless.service`. That renames `feldcore-SLOT+*.conf` to `feldcore-SLOT.conf` and sets `default` to that stable name. A rollback pin is a `default` line that names the other slot's stable file. An exact `default` match wins even when the other slot has a higher version.

The Updates page rollback does not reboot by itself. It writes the stable entry for the earlier image and sets `default` to it. The next restart uses that image. There is nothing to roll back to until a second slot has a boot entry.

## What is in the image

The root is Debian 13 (trixie). The package list is `os/packages.txt` (and the same names in `os/mkosi/mkosi.conf`). It includes systemd, the amd64 kernel, systemd-boot, Docker Engine, containerd, the Compose v2 plugin (`docker-compose`), git, Samba, NFS, restic, rclone, cifs-utils, wireguard-tools, smartmontools, mdadm, btrfs-progs, xfsprogs, hdparm, parted, ufw, rtl-sdr, systemd-timesyncd, pciutils, curl, and GPU firmware from non-free-firmware. Recommended packages are not installed. ZFS is not included. libvirt, QEMU, OVMF, swtpm, virtiofsd, and the NVIDIA Container Toolkit are sysexts, not base packages. A pinned `cloudflared` binary is installed at `/usr/bin/cloudflared`. `feldcored` runs that program as the tunnel connector and does not start a `feldcore-cloudflared` container on the appliance. A pinned Caddy 2.11.9 static binary is installed at `/usr/bin/caddy`, built from that release with the Cloudflare DNS module. `feldcored` supervises it and does not start a `feldcore-caddy` container. Its config and certificates are under `/var/lib/feldcore/caddy`. `feldcored` is `/usr/bin/feldcored` and is started by `feldcore.service` with `FELDCORE_DATA_DIR=/var/lib/feldcore`. The package unit in `deploy/feldcore.service` is unchanged and still uses `/usr/local/bin/feldcored`.

Docker, containerd, and the image smoke unit are enabled. The smoke unit runs only when the kernel command line contains `feldcore.smoke`. `os/smoke.sh --root` checks the root filesystem after the image is applied, and `os/build.sh` runs it before the disk is packed.

`/etc/os-release` and `/etc/feldcore-os-release` say `ID=feldcore` and `SYSEXT_LEVEL=1`. The daemon treats the machine as an appliance only when that id is set. A copy of the daemon installed on a normal Debian system can check a signed release, and it refuses to write a slot.

`/usr/bin/apt`, `apt-get`, `apt-cache`, and `apt-mark` are replaced with a script that prints that FELDCORE OS has no apt and exits 1. `sources.list` is empty. dpkg is left in place because systemd scripts may call it. There is no compiler and no DKMS. The root password is locked. sshd is not installed. The serial console is enabled so a boot log is visible. The appliance image does not autologin.

DNS is a static `/etc/resolv.conf` (1.1.1.1 and 8.8.8.8). Wired interfaces named `e*` use DHCP through systemd-networkd.

Free firmware is in the image. Proprietary NVIDIA firmware and the NVIDIA driver are not. Those belong in a sysext, and this build does not download them.

A cloned image keeps the machine-id that was generated at build time. systemd has already read it by the time a unit could replace it. Machines cloned from one image share that id until the image is rebuilt.

## Building

`os/build.sh` produces the disk image, the installer image, and a signed manifest.

```
./os/build.sh
```

Useful environment variables:

| Variable | Meaning |
| --- | --- |
| `FELDCORE_OS_VERSION` | Image version. Default `0.1.0`. |
| `FELDCORE_OS_WORK` | Work directory. Default `/var/tmp/feldcore-os`. The signing key lives here. |
| `FELDCORE_OS_OUT` | Output directory. Default `dist/os`. |
| `FELDCORE_IMAGE_BUILDER` | `debootstrap` (default) or `mkosi`. |
| `FELDCORE_UPDATE_KEY` | Private key file. When unset, the script generates an ephemeral key under the work directory. |
| `FELDCORE_ROOT_MIB` | Root filesystem size in MiB. Default `6144`. An update refuses a root larger than 8 GiB. |
| `FELDCORE_STATE_MIB` | State partition size in MiB. Default `8192`. The installer reads this from the payload file `state-mib`. |
| `FELDCORE_BUILD_SYSEXTS` | Space-separated sysext ids to build into `dist/os/sysexts`. Default `libvirt nvidia-container-toolkit`. Empty skips them. |

The default builder reuses a finished debootstrap tree in the work directory. `mkosi summary` accepts `os/mkosi/mkosi.conf` (Debian trixie, `Format=directory`, `Bootable=no`, the same package list). `FELDCORE_IMAGE_BUILDER=mkosi` builds the root that way and the script then applies the same image files and disk layout. The image booted in this repository's verification was built with debootstrap.

The script builds `feldcored` with `CGO_ENABLED=0` and the embedded web UI, copies it into the root, then packs an ext4 image. The ESP is written with mtools, so the build machine does not have to mount FAT. It deletes `*.key` and `*.sec` from the output directory and refuses to finish if one is still there.

`dist/` is gitignored. Do not commit a private key. `os/keys/README.md` describes how to generate one. The public key is installed at `/usr/share/feldcore/update.pub` and is also copied to the output as `update.pub`.

### Output files

| File | Role |
| --- | --- |
| `feldcore-os.raw.zst` | Bootable appliance disk. |
| `feldcore-installer.raw.zst` | Bootable installer disk. |
| `feldcore-root.img.zst` | The slot image an update writes. |
| `vmlinuz`, `initrd.img` | The kernel and initrd an update copies to the ESP. |
| `feldcore-os.json` | The signed manifest. |
| `feldcore-os.json.minisig` | Its minisign signature. |
| `update.pub` | The matching public key. |

GitHub release assets have a 2 GiB limit. The published files are zstd, not the raw disks. The raw disks are sparse and mostly empty, so the compressed files are the size of the root filesystem.

## Signed manifest

`cmd/osmanifest` writes and signs the manifest.

```
go run ./cmd/osmanifest genkey -pub update.pub -key update.key
go run ./cmd/osmanifest manifest -dir dist/os -version 0.1.0 -notes-file NOTES -kernel 6.12.0 -out dist/os/feldcore-os.json
go run ./cmd/osmanifest sign -key update.key -in dist/os/feldcore-os.json
go run ./cmd/osmanifest pub -key update.key -pub update.pub
```

The private key file is not a minisign encrypted secret. It is one untrusted-comment line and a base64 blob of `Ed`, an 8-byte key id, and the 64-byte ed25519 seed. Mode `0600`. It is never written into an image.

The signature is minisign's hashed form (`ED`): BLAKE2b-512 of the file, then an ed25519 signature over that hash and a second signature over the trusted comment. `minisign -V` accepts it. The verifier also accepts a legacy `Ed` signature produced by `minisign -S`.

`sha256` and `size` in the manifest are the decoded bytes that are written to the slot or the ESP. `encoding` is empty or `zstd`. The manifest names `root`, `kernel`, and `initrd` once each. Optional `sysexts` name extension images. Relative file names are resolved against the manifest URL by replacing the last path segment.

The default manifest URL is `https://github.com/Herculade0/FELDCORE/releases/latest/download/feldcore-os.json`. The signature is that URL plus `.minisig`.

The address the daemon uses is, in order: `FELDCORE_UPDATE_URL` (the Updates page cannot change it), the address saved in the state file, then the default. The public key is, in order: `FELDCORE_UPDATE_PUBKEY` when that path is set (it must exist; there is no fallback), `/var/lib/feldcore/update.pub`, then `/usr/share/feldcore/update.pub`.

Downloads require HTTPS. Hostnames `localhost` and the cloud metadata names are refused. Loopback, unspecified, link-local, and multicast addresses are refused, including `169.254.169.254`. The dialer checks every resolved address and does not use an HTTP proxy. Private LAN addresses are allowed so a local mirror works. `FELDCORE_UPDATE_ALLOW_LOCAL=1` permits `http` and loopback for tests. The image does not set it.

## Update, schedule, and rollback

The daemon streams the root image onto the inactive slot while it hashes the decoded bytes. The state partition is too small to hold the image, so it is not used as a cache. The boot entry is written only after the hash and the size match. A mismatch can leave the inactive slot dirty. It does not point the boot menu at it.

The kernel and initrd are downloaded under the state directory and then copied onto the ESP. A same-version update is refused. So is an image larger than the slot, a root over 8 GiB, a kernel over 128 MiB, or an initrd over 256 MiB.

A schedule is one RFC3339 time in the state file. The daemon checks it about every 20 seconds and once at startup. When the time is due it clears the schedule and starts the update job, so a failed clock cannot reboot in a loop. Restarting into the new slot is a separate action.

These jobs use the install runner from the package-install work: `os-update`, `os-rollback`, `os-sysext-<id>`, and `os-sysext-remove-<id>`. They do not take the apt lock. A mutex in the update service refuses a second slot write, rollback, or extension change while one is running.

Reboot is `POST /api/v1/os/reboot`. It runs `systemctl reboot` and only on an appliance.

### API

All of these require the administrator. A read token is refused. `GET /api/v1/os` is not a read-scoped path.

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/v1/os` | Current slot, next boot, last verified release, extensions |
| POST | `/api/v1/os/check` | Download and verify the manifest |
| POST | `/api/v1/os/update` | 202 and the `os-update` job |
| POST | `/api/v1/os/rollback` | 202 and the `os-rollback` job |
| PUT | `/api/v1/os/schedule` | `{ "at": "<RFC3339>" }` or `{ "at": "" }` |
| PUT | `/api/v1/os/source` | `{ "url": "https://..." }` or empty to use the default |
| POST | `/api/v1/os/sysexts/{id}` | 202 and an install job |
| DELETE | `/api/v1/os/sysexts/{id}` | 202 and a remove job |
| POST | `/api/v1/os/reboot` | Reboot the appliance |

Audit actions, recorded as the signed-in user or token: `os.update.check`, `os.update.stage`, `os.update.staged`, `os.update.schedule`, `os.update.source`, `os.update.rollback`, `os.update.reboot`, `os.sysext.install`, `os.sysext.remove`. The job runner also records `install started` and `install finished` as `admin`.

`FELDCORE_OS_ROOT` points the daemon at a fixture tree instead of the real machine. It is for tests and screenshots. While that root is set, installing an extension does not run the host's `systemd-sysext`, because that command merges the extensions of the machine it is running on. On the appliance the root is unset and a successful install runs `systemd-sysext refresh`.

## Updates page

The nav item is Updates. It shows the running version and slot, the next boot and its remaining tries, the signed release and its notes, and the two slots. Check for updates, Update now, a one-shot schedule, and Roll back to the earlier image use the job runner. The progress panel says Staged or Ready rather than Installed. Restart into the next slot is offered when the next boot is not the running slot.

The same page lists extensions that are installed or named by the cached manifest. An extension whose kernel does not match `uname -r` cannot be installed. An extension shipped inside the image cannot be removed. One installed on the state partition can.

The Settings page still has its own check for a newer `feldcored` binary. That is not this OS update.

## Extensions

A sysext is a squashfs (or another image systemd-sysext accepts) with `/usr/lib/extension-release.d/extension-release.<id>` containing `ID=feldcore` and `SYSEXT_LEVEL=1`. An empty kernel field means the extension is userspace-only. A non-empty kernel must equal the running kernel, because a module's vermagic has to match.

`os/sysext/build-sysext.sh` packs `libvirt` or `nvidia-container-toolkit` from packages, or a local NVIDIA tree when `FELDCORE_NVIDIA_SYSEXT=1` and `FELDCORE_NVIDIA_DIR` point at files you already have. It does not download the proprietary driver. The image build runs it for the ids in `FELDCORE_BUILD_SYSEXTS` and the manifest advertises the images that were produced. The toolkit packages come from NVIDIA's apt repository at build time. The driver sysext stays a local tree.

NVIDIA, the container toolkit, and libvirt are the examples the manager is meant to show once a release includes them. The GPU notes in `docs/GPU-SELECTION.md` still apply: no DKMS on the running system, and the driver sysext has to match the image kernel.

## Installer

`feldcore-installer.raw.zst` is a second disk, not a USB-stick OS. It uses the same systemd-boot setup. Slot A is the installer system. A `feldcore-payload` partition holds the zstd root image, the kernel, the initrd, and `systemd-bootx64.efi`.

The installer root unlocks a serial autologin so the command can be typed. The appliance image does not. On the serial console:

```
feldcore-install --yes /dev/vdb
```

The command requires `--yes` and a whole disk. It refuses the disk the installer booted from and any disk that has a mounted partition. It writes only the named device: ESP, slot A, slot B, and the state partition. Slot A receives the payload image. The boot entry is `feldcore-a+3.conf`.

`feldcore.install=/dev/vdb` on the kernel command line runs the same command once at boot. Without that argument the service exits and the installer waits.

## GitHub Actions

`.github/workflows/os-image.yml` runs when a tag matching `v*` is pushed. It does not create the tag. It builds the image, uploads the output as a workflow artifact, and publishes those files onto the tag's GitHub Release.

Publishing requires the `FELDCORE_UPDATE_SIGNING_KEY` secret, which is the unencrypted key file. The workflow writes it under `RUNNER_TEMP` and does not print it. If the secret is missing, the artifact is still uploaded and the job then fails, so a release is not signed by a key that existed only on that runner. The binary workflow in `release.yml` is unchanged and still publishes `feldcored` for the same tag.

## Booting it here

`os/qemu.sh` boots `feldcore-os.raw` with QEMU, KVM, and the OVMF firmware that does not enforce Secure Boot (`OVMF_CODE_4M.fd`, not the `.ms` or `.secboot` files). Serial output goes to the work directory. Port 8787 on the host is forwarded to the guest.

`os/qemu.sh` was run on this image. The result is in the verification section below.

## What this does not do

- Secure Boot, shim, and kernel signing are not implemented.
- The booted image in the verification below is the debootstrap root. The mkosi config is checked with `mkosi summary` and is not what that boot used, unless `FELDCORE_IMAGE_BUILDER=mkosi` was set.
- The NVIDIA kernel driver is not downloaded. The container toolkit and libvirt are userspace sysexts in the default manifest. A driver tree is still a local `FELDCORE_NVIDIA_SYSEXT=1` build.
- The installer command is in the image. A full install onto a second QEMU disk is not part of the boot check below unless the verification section says it was run.
- Machine-ids are not unique per clone.
- The daemon on a non-appliance install shows the Updates page and will not write a slot.

## Verification

`sudo ./os/boot-test.sh` boots a sparse copy of `feldcore-os.raw` with KVM and `OVMF_CODE_4M.fd`, and adds `feldcore.smoke=1` to the slot A loader entry. The guest smoke unit waits for dockerd, runs `nginx:alpine` on `127.0.0.1:8088`, and requests `:8787`. It prints `FELDCORE_SMOKE_OK` on the serial console. The host then requests `http://127.0.0.1:8787/`.

On the image built from this tree, that guest marker appeared and the host received the UI. `os/smoke.sh --root` had already passed during `os/build.sh`. The manifest lists the `libvirt` and `nvidia-container-toolkit` sysexts, with no kernel pin.

These parts were not run end to end on that boot:

- The bless unit was not checked afterwards. The guest was stopped when the smoke marker appeared, so the ESP was not inspected for the stable `feldcore-a.conf` rename.
- The installer disk was assembled and was not booted, and `feldcore-install` was not pointed at a second disk.
- The booted root was built with debootstrap. `mkosi` was not used for that boot.
- A libvirt guest was not started. The sysext was built and is in the manifest; it was not merged in the QEMU guest.
- The NVIDIA toolkit sysext was built from NVIDIA's apt repository. The proprietary driver was not downloaded, and no GPU was present in the guest.
- Secure Boot stayed off.
- Every clone of this image shares the machine-id generated at build time.
