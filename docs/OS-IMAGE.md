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

The root is Debian 13 (trixie). The package list is `os/packages.txt` (and the same names in `os/mkosi/mkosi.conf`). It includes systemd, the amd64 kernel, systemd-boot, Docker Engine, containerd, the Compose v2 plugin (`docker-compose`), git, Samba, NFS, restic, rclone, cifs-utils, wireguard-tools, smartmontools, mdadm, btrfs-progs, xfsprogs, hdparm, parted, ufw, rtl-sdr, systemd-timesyncd, wpasupplicant (the `wpa_supplicant` daemon), openssh-server, pciutils, curl, and GPU firmware from non-free-firmware. Recommended packages are not installed. ZFS is not included. libvirt, QEMU, OVMF, swtpm, virtiofsd, and the NVIDIA Container Toolkit are sysexts, not base packages. A pinned `cloudflared` binary is installed at `/usr/bin/cloudflared`. `feldcored` runs that program as the tunnel connector and does not start a `feldcore-cloudflared` container on the appliance. A pinned Caddy 2.11.9 static binary is installed at `/usr/bin/caddy`, built from that release with the Cloudflare DNS module. `feldcored` supervises it and does not start a `feldcore-caddy` container. Its config and certificates are under `/var/lib/feldcore/caddy`. `feldcored` is `/usr/bin/feldcored` and is started by `feldcore.service` with `FELDCORE_DATA_DIR=/var/lib/feldcore`. The package unit in `deploy/feldcore.service` is unchanged and still uses `/usr/local/bin/feldcored`.

Docker, containerd, and the image smoke unit are enabled. The smoke unit runs only when the kernel command line contains `feldcore.smoke`. `os/smoke.sh --root` checks the root filesystem after the image is applied, and `os/build.sh` runs it before the disk is packed.

`/etc/os-release` and `/etc/feldcore-os-release` say `ID=feldcore` and `SYSEXT_LEVEL=1`. The daemon treats the machine as an appliance only when that id is set. A copy of the daemon installed on a normal Debian system can check a signed release, and it refuses to write a slot.

`/usr/bin/apt`, `apt-get`, `apt-cache`, and `apt-mark` are replaced with a script that prints that FELDCORE OS has no apt and exits 1. `sources.list` is empty. dpkg is left in place because systemd scripts may call it. There is no compiler and no DKMS. The root password is locked (`passwd -l root`). openssh-server is installed and `ssh.service` is not enabled. `/etc/ssh/sshd_config.d/00-feldcore.conf` sets `PermitRootLogin prohibit-password` and `PasswordAuthentication no`, so SSH is public-key only and root cannot log in with a password. `PermitRootLogin yes` is not used. An install answer file is what enables sshd. The serial console is enabled so a boot log is visible. The appliance image does not autologin.

DNS is a static `/etc/resolv.conf` (1.1.1.1 and 8.8.8.8). Wired interfaces named `e*` use DHCP through systemd-networkd. Wireless interfaces (`Type=wlan`) use DHCP through `25-wifi.network` once `wpa_supplicant` is running. `feldcore-wifi.service` is not enabled. It starts only when `/etc/wpa_supplicant/feldcore.conf` exists and an answer file enables the unit. `iwd` and NetworkManager are not installed.

Free firmware is in the image. Proprietary NVIDIA firmware and the NVIDIA driver are not. Those belong in a sysext, and this build does not download them.

`/etc/machine-id` is shipped empty. systemd writes a unique id on first boot when that file exists and is empty, so clones of one image do not share an id. `/var/lib/dbus/machine-id` is a symlink to `/etc/machine-id`. `os/build.sh` truncates the file after the packages are installed, because debootstrap may have written an id. The slot, ESP, and state labels and sizes are unchanged.

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
| `feldcore-installer.raw.zst` | Bootable raw installer disk. |
| `feldcore-installer.iso` | Hybrid UEFI installer. Published with `feldcore-installer.iso.minisig`. If the ISO is at or over 2 GiB, the release has `feldcore-installer.iso.zst` and its `.minisig` instead, and both sizes are printed if neither fits. The ISO is not a field in `feldcore-os.json`. |
| `feldcore-root.img.zst` | The slot image an update writes. |
| `vmlinuz`, `initrd.img` | The kernel and initrd an update copies to the ESP. |
| `feldcore-os.json` | The signed manifest. |
| `feldcore-os.json.minisig` | Its minisign signature. |
| `update.pub` | The matching public key. |

GitHub release assets have a 2 GiB limit. The raw disks are zstd. The installer ISO is published as itself when it is under 2 GiB, and as `feldcore-installer.iso.zst` when only the compressed file fits.

## Signed manifest

`cmd/osmanifest` writes and signs the manifest.

```
go run ./cmd/osmanifest genkey -pub update.pub -key update.key
go run ./cmd/osmanifest manifest -dir dist/os -version 0.1.0 -notes-file NOTES -kernel 6.12.0 -out dist/os/feldcore-os.json
go run ./cmd/osmanifest sign -key update.key -in dist/os/feldcore-os.json
go run ./cmd/osmanifest sign-file -key update.key -in dist/os/feldcore-installer.iso
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

`feldcore-installer.iso` is the installer to flash or to drop on Ventoy. It is ISO 9660, with a real GPT whose EFI system partition is the same FAT image El Torito boots. The ESP label is `FC-ISOEFI`, never `FC-ESP`. GRUB on that ESP loads a small live system (`boot=feldcore`) and the live initrd mounts `live/filesystem.squashfs`. The appliance payload is `/payload` on that ISO: the slot image, the appliance kernel and initrd, `systemd-bootx64.efi`, and the text files the installer reads for version and partition size.

`os/build.sh` writes it with xorriso after the raw installer image. The documented xorriso command is tried first. xorriso 1.5.6 rejects `-appended_gpt_with_gaps` and needs `-apm-block-size 2048` plus a bless type before it will combine HFS+ with that GPT. The build then tries those flags, and if that is also rejected it ships the GPT and El Torito image without an HFS+ volume. The HFS+ file names (`System/Library/CoreServices/boot.efi`, `SystemVersion.plist`, an empty `mach_kernel`) are in the ISO tree either way. The layout that was written is `feldcore-installer.iso.layout` in the work directory.

The live image autologins root on tty1 and on ttyS0. The appliance image does not gain a tty1 autologin. On the console:

```
feldcore-install --yes /dev/disk
```

The command requires `--yes` and a whole disk. It refuses the installer medium and any disk that has a mounted partition. Before it erases the target it looks for `feldcore-install.json` on another FAT or exFAT volume: `feldcore/feldcore-install.json`, then the same name at the volume root. A volume counts when its disk is removable, its bus is USB, or its filesystem label is `FC-ANSWERS`. FAT stores 11 characters and exFAT stores 15, so the longer label in the design cannot be written with `mkfs.vfat` or `mkfs.exfat`. A Ventoy data partition and a second stick both count. The bytes are copied to `feldcore/feldcore-install.json` on the new ESP. No file is a normal install: the appliance prints a setup code and the browser wizard runs. The source file is left on the stick.

`feldcore-installer.raw.zst` is still built and published. It is the same payload and the same `feldcore-install` script on a systemd-boot disk, with a serial autologin and a `feldcore-payload` partition. `feldcore.install=/dev/disk` on its kernel command line runs the install once at boot. It stays on the release until the Mac mini checklist below has been done on a published ISO.

Check the ISO with the same public key as the manifest:

```
minisign -V -p update.pub -m feldcore-installer.iso
```

When the asset is the zstd wrapper, `-m` names that `.iso.zst` file. Decompress only after the signature verifies.

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
- The daemon on a non-appliance install shows the Updates page and will not write a slot.

## Verification

`sudo ./os/boot-test.sh` boots a sparse copy of `feldcore-os.raw` with KVM and `OVMF_CODE_4M.fd`, and adds `feldcore.smoke=1` to the slot A loader entry. The guest smoke unit waits for dockerd, runs `nginx:alpine` on `127.0.0.1:8088`, and requests `:8787`. It prints `FELDCORE_SMOKE_OK` on the serial console. The host then requests `http://127.0.0.1:8787/`.

On the image built from this tree, that guest marker appeared and the host received the UI. `os/smoke.sh --root` had already passed during `os/build.sh`. The manifest lists the `libvirt` and `nvidia-container-toolkit` sysexts, with no kernel pin.

These parts were not run end to end on that boot:

- The bless unit was not checked afterwards. The guest was stopped when the smoke marker appeared, so the ESP was not inspected for the stable `feldcore-a.conf` rename.
- The raw installer disk was assembled. The ISO boot, the answer-file copy, and Ventoy are covered by `os/iso-test.sh` and `os/ventoy-test.sh` when those scripts have been run. The result of the run that added the ISO is recorded below.
- The booted root was built with debootstrap. `mkosi` was not used for that boot.
- A libvirt guest was not started. The sysext was built and is in the manifest; it was not merged in the QEMU guest.
- The NVIDIA toolkit sysext was built from NVIDIA's apt repository. The proprietary driver was not downloaded, and no GPU was present in the guest.
- Secure Boot stayed off.

### Installer ISO

`os/iso-test.sh` and `os/ventoy-test.sh` were run on a tiny payload (`root-mib` 64, `state-mib` 32) built by the same `assemble_iso` path as a release. QEMU 8.2.2 used `OVMF_CODE_4M.fd`. This environment has no usable `/dev/kvm`, so the guest CPU was TCG `qemu64`. `os/boot-test.sh` was left unchanged and was not run again. The full appliance image was not rebuilt here. CI builds that image and then runs these two scripts; the answer-file target disk in `os/iso-test.sh` follows the payload's partition sizes, which is about 21 GB for the default 6144 MiB root and 8192 MiB state.

xorriso 1.5.6 wrote the layout word `hfs`. It rejects `-appended_gpt_with_gaps`. The HFS+ attempt, with `-apm-block-size 2048` and `-hfs-bless-by i`, is the image that booted. The GPT still has an EFI system partition labeled `FC-ISOEFI`, El Torito is one UEFI no-emulation image, and the volume id is `FELDCORE`.

`os/iso-test.sh` passed:

- The answer-file checker accepts version `1` and `1.0` and rejects a second value, an array, and version 2.
- CD boot (`-cdrom`) reached the motd `FELDCORE installer.` and a root shell on ttyS0.
- The ISO written onto a disk 1 GiB larger booted, and `sgdisk -v` reported the backup header short of the end of that disk.
- A second disk labeled `FC-ANSWERS` supplied `feldcore/feldcore-install.json`. The installer copied it to `feldcore/feldcore-install.json` on the new ESP and the bytes matched. The live root did not apply the file.
- Two accepted answer files made `feldcore-install --yes` exit 1 with `more than one install answer file`. The target disk's sha256 was unchanged and no `FC-ESP` or `feldcore-a` partition was created.

`os/ventoy-test.sh` passed with Ventoy 1.1.18. `VTOY_MENU_TIMEOUT` opens a mode menu and waits, so the test sends Enter. Normal mode is the first line. The GRUB2 boot sends Down, then Enter, and the image name is `feldcore-installer_VTGRUB2.iso`.

Ventoy 1.1.18 sets `rdinit=/vtoy/vtoy` and then runs this ISO's `/boot/grub/grub.cfg` from its own GRUB 2.04. In normal mode it first tries to chainload its EFI helper. That helper prints `cannot load image` and `you need to load the kernel first` for this hybrid ISO, and Ventoy falls through to the configfile path. GRUB2 mode skips that chain. Both paths mounted the live root and reached the same motd. The initrd treats Ventoy's extra iso9660 nodes (the device-mapper image, the partition alias, and `/dev/mapper/ventoy`) as one image when the marker line and the squashfs length match.

The ISO was not signed in this run. `cmd/osmanifest sign-file` streams the file through `SignStream`, and `TestSignStream` covers that path. A release signs whichever of `feldcore-installer.iso` and `feldcore-installer.iso.zst` is under 2 GiB.

These four steps still have to be done by a person on a Late 2012 Mac mini (Macmini6,1). They are not done. The FAT ESP is not a substitute for the HFS+ bless, and `feldcore-installer.raw.zst` stays on the release, until that result is written into `docs/FELDCORE-INSTALLER-ISO.md`:

1. Etcher-flash the published asset. Option-boot the Mac mini. The stick is listed as EFI Boot, or under the name from `SystemVersion.plist`, and it reaches the tty1 motd and a root shell. If the stick never appears, retry through a USB 2 hub before changing the image.
2. Copy the `.iso` onto a Ventoy stick (decompress first when the asset is `.iso.zst`) and write `feldcore/feldcore-install.json` on that same partition. Option-boot, choose the ISO, and reach the installer without mounting the installer ESP.
3. Run `feldcore-install --yes` on the internal whole disk. Power off, remove the USB, Option-boot. The internal disk is listed and the appliance answers on port 8787. If the internal disk is absent, stop and do the HFS+ ESP follow-up. Do not bless the disk by hand and call the FAT layout done.
4. The hostname or SSH access matches the JSON from step 2, and `/var/lib/feldcore/feldcore-install.applied` exists. The source file is still on the Ventoy partition unless someone deleted it.

The image ships an empty `/etc/machine-id`. That boot was not used to check that systemd filled the file in.
