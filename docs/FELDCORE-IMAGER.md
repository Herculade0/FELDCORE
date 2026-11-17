# FELDCORE Imager

Scope only. This file is the design. No Imager binary, web page, or first-boot hook exists yet.

FELDCORE Imager is a downloader and writer. It takes a released FELDCORE OS disk image, optionally drops one answer file onto that image's EFI System Partition, and either saves the result or writes it to a USB stick the operator named. The stick or the saved image is the appliance disk. First boot reads the answer file and applies it through the setup writers that the first-start wizard already uses.

The answer file is `feldcore-install.json`. Nothing in the tree reads that name today. `feldcore-install` partitions a target disk. It does not apply hostname, network, or account settings. The Imager must not grow a second configuration language beside `internal/setup.Request`.

## Goal and user stories

The operator is preparing a machine that has no FELDCORE account yet. They want a bootable image on disk, or on a USB stick, with the first-boot choices already filled in so the box can come up headless.

Two outputs:

1. **Save.** A customised `feldcore-os.raw` or `feldcore-os.raw.zst` on the operator's HDD. They can archive it or write it later.
2. **Write.** The same bytes written to one whole USB disk, chosen explicitly, in this session.

Headless first boot, when the answer file is present and every section it names succeeds:

- The host name, time zone, and locale are set.
- Wired networking stays on DHCP, or one interface gets the static address from the file.
- The FELDCORE administrator exists, or a setup code the operator already knows is in place so they can claim the server without reading a random code off the console.
- Cloudflare Tunnel or WireGuard, and a Proton Drive backup, are stored the same way the wizard stores them.
- The browser wizard does not block the dashboard on steps the file already finished.
- WiFi and SSH are part of the same file. They do nothing until the image actually contains a WiFi supplicant and `sshd`. Those packages are not in the image today. The stories are real; the first milestone must refuse the fields rather than pretend they were applied.

Someone who leaves the file empty gets the image `os/build.sh` already builds: DHCP on interfaces named `e*`, hostname `feldcore`, root password locked, no `sshd`, and a setup code printed to the console on first start.

## What already exists

| Piece | Where | What it does |
| --- | --- | --- |
| Appliance disk | `os/build.sh` `assemble_appliance` | GPT disk `feldcore-os.raw.zst`. ESP, slot A, slot B, state. |
| Installer disk | `os/build.sh` `assemble_installer` | `feldcore-installer.raw.zst`. Installer root, plus a `feldcore-payload` partition. This is a second image, not the OS the Imager customises. |
| ESP writer | `fill_esp` in `os/build.sh` | Writes the FAT ESP with `mcopy`. The build host does not have to mount FAT. |
| Boot | `loader/loader.conf`, type-1 entries | `root=LABEL=feldcore-a`. Secure Boot is off. UEFI only. systemd-boot. |
| Install onto another disk | `os/image/usr/sbin/feldcore-install` | `feldcore-install --yes DISK`, or `feldcore.install=DISK` on the kernel command line via `feldcore-install.service`. Requires `--yes` and a whole disk. Refuses the booted disk and any disk with a mounted filesystem. |
| First-start wizard | `internal/setup` | Steps in `steps.go`: welcome, host, network, admin, storage, remote, notify, backup, summary. |
| Host writer | `applyHost` | `hostnamectl`, `timedatectl`, locale, NTP. On the appliance, `chrony` is refused. NTP is `systemd-timesyncd`. |
| Network writer | `SaveNetwork`, `applyNet`, `netFile`, `applyDHCP` | DHCP or static. systemd-networkd file `/etc/systemd/network/10-feldcore.network`, or ifupdown when that is the backend. The image ships `20-wired.network` matching `e*` with DHCP. |
| Admin and claim | `applyAdmin`, `EnsureCode`, `CheckCode`, `ConsumeCode` | One administrator in the FELDCORE state file. One-time setup code in `/var/lib/feldcore/setup-code`. |
| Remote access | `applyRemote` | `none`, Cloudflare (`token` or `api`), or one WireGuard config. Tokens and the WireGuard private key go to the secrets box. |
| Proton Drive | `applyBackup` | Experimental rclone backup. Password goes to the secrets box. |
| Notifications | `applyNotify` | SMTP or ntfy. The wizard can do this. A bad test send fails the step. |
| Skip wizard | `-skip-wizard` / `FELDCORE_SKIP_WIZARD` | Marks the wizard finished and applies nothing. The setup code is still required. This is not the answer-file path. |
| OS update | `internal/osupdate`, Updates page | Signed manifest of the root image, kernel, and initrd. Writes the inactive A/B slot of the **running** appliance. It does not build a USB image. |
| Release | `.github/workflows/os-image.yml` | On a `v*` tag, builds the image and publishes the assets on that tag's GitHub Release when `FELDCORE_UPDATE_SIGNING_KEY` is set. `release.yml` publishes `feldcored` binaries for the same tag. The Imager downloads the OS image assets, not those binaries. |
| Disk identity | `internal/storage` `applyIdentity` | Stable id is `serial:<serial>` when a serial exists, otherwise `byid:<basename>`. `path:` is the leftover. The OS disk is any disk that holds `/`, `/boot`, `/boot/efi`, or `/usr`. Formatting that disk is refused. |

The image is Debian 13, amd64, root read-only. `/etc` is an overlay whose upper directory is on the state partition, so `hostnamectl` and network files survive a reboot and a later slot update. `/home` is tmpfs. `/var/log`, `/var/tmp`, `/var/cache`, and `/var/lib/systemd` are tmpfs. The state partition is mounted at `/var/lib/feldcore` and is 8 GiB in the default build (`FELDCORE_STATE_MIB`). `apt` on the image is a stub that exits 1. Packages cannot be installed on first boot.

`sshd` is not installed. The root password is locked. There is no WiFi supplicant in `os/packages.txt` or `os/mkosi/mkosi.conf`. Wired DHCP is the only network configuration in the image.

The FELDCORE administrator is not a Unix account. Share users (`internal/shares/users.go`) are locked Unix accounts with no home and no shell, created for Samba. An SSH login user is a third kind of account and has no writer yet.

`os/build.sh` generates one `/etc/machine-id` for the root filesystem. Every clone of that image shares it. systemd has already read it by the time a first-boot unit runs. The Imager must leave slot A byte-for-byte identical to the signed root image, so it also must not rewrite `machine-id` inside each stick.

Default raw disk size from `os/build.sh`, before compression:

`16 + 256 + 2 × (FELDCORE_ROOT_MIB + 128) + FELDCORE_STATE_MIB` MiB.

With the defaults (`FELDCORE_ROOT_MIB=6144`, `FELDCORE_STATE_MIB=8192`) that is 21008 MiB. The published file is zstd. GitHub release assets have a 2 GiB limit. The compressed file is what the Imager downloads. Writing needs the decompressed size, and the target disk has to be at least that large. The state partition stays 8 GiB. The Imager does not grow partitions to fill the stick.

## Form factor

Build the first-boot applicator and a Linux CLI first. The CLI is `cmd/feldcore-imager`. It is a separate program from `feldcored`. The daemon must not gain a disk writer.

| Form | What it is | Trade-off |
| --- | --- | --- |
| (a) Page in the FELDCORE web UI | A form in the existing React UI. | The running appliance cannot assemble the image. The state partition is 8 GiB, the root is read-only, and `/tmp` is tmpfs. The decompressed disk is about 21 GiB. A page that streams a customised image needs a workstation-sized disk and becomes a second writer. The page is a good place to edit the answer file and download that JSON. It is a bad place to open `/dev/sdX`, especially on a multi-drive machine. |
| (b) Desktop app | A cross-platform flasher in the style of Raspberry Pi Imager. | Raspberry Pi Imager is a Qt application. This repository is Go and React. A Qt app would be a second implementation of the disk writer. There is no desktop shell here yet. If a GUI is built later, use Wails so the existing React form and the Go writer are one program. Do not start with Electron, and do not start the project with the GUI. |
| (c) CLI | `feldcore-imager` on Linux. | It can download, verify, inject with `mtools`, save a file, and write a device using sysfs serials. Linux is the host that has the disks. macOS and Windows can wait. A port that cannot see a stable serial must be allowed to save a file and must refuse to write a device. It must not fall back to "the only removable disk" or "the largest disk". |

The web page that only downloads `feldcore-install.json` can follow the CLI. It does not assemble an image and it does not write a block device. The Updates page stays the A/B updater for the machine that is already running (`web/src/components/UpdatesPage.tsx`, `/api/v1/os`). The Imager is not a mode of that page.

The desktop app is not scheduled. Build it only after the owner asks, and only as a front end on the same Go writer the CLI uses.

## Image generation

### Base image

Default source is the GitHub Release published by `.github/workflows/os-image.yml` for the tag the operator names, or `latest` when they do not.

| Asset | Role for the Imager |
| --- | --- |
| `feldcore-os.raw.zst` | The appliance disk. This is the file that is customised and saved or written. |
| `feldcore-os.json` | Signed manifest. |
| `feldcore-os.json.minisig` | minisign signature of that manifest. |
| `update.pub` | Public key. The same key is installed in the image at `/usr/share/feldcore/update.pub`. |
| `feldcore-root.img.zst`, `vmlinuz`, `initrd.img` | The artifacts the manifest hashes. Used to check the disk. |
| `feldcore-installer.raw.zst` | Not the default output. A later milestone copies the answer file onto a disk that `feldcore-install` writes. |

The manifest URL the daemon already uses is `https://github.com/Herculade0/FELDCORE/releases/latest/download/feldcore-os.json`. The signature is that URL plus `.minisig`. The disk asset for a known version is `https://github.com/Herculade0/FELDCORE/releases/download/v<version>/feldcore-os.raw.zst`.

`internal/osupdate.ParseManifest` uses `DisallowUnknownFields`, and `validate` allows only the file kinds `root`, `kernel`, and `initrd`. The signed manifest does not hash `feldcore-os.raw.zst`. Adding a disk hash to the manifest is a parser change and is out of this scope. Verify the disk by its contents:

1. Verify `feldcore-os.json.minisig` with `update.pub` (`internal/osupdate.Verify`).
2. Read the GPT. Partition 1 is type EF00, name `FC-ESP`, starting at 1 MiB, 256 MiB. Partition 2 is name `feldcore-a`. Refuse a disk that does not match the layout `assemble_appliance` writes.
3. Hash the first `size` bytes of partition 2 and compare to the manifest's `root` SHA-256. The partition is the root image plus 128 MiB of slack, so hashing the whole partition will not match. `size` and `sha256` in the manifest are the decoded root image, the bytes `dd` wrote.
4. Hash `/feldcore/a/vmlinuz` and `/feldcore/a/initrd.img` on the ESP and compare to the manifest's `kernel` and `initrd`.
5. Stop if any hash differs. Do not inject.

Download from the GitHub Release. A failed `os-image` workflow still uploads an Actions artifact, and that artifact may have been signed with the ephemeral key `os/build.sh` generates when `FELDCORE_UPDATE_SIGNING_KEY` is missing. The job then fails on purpose so that artifact is not published. The Imager does not download Actions artifacts.

A local file from `./os/build.sh` (`dist/os/feldcore-os.raw.zst`, or `FELDCORE_OS_OUT`) is the other source. The CLI requires `--allow-unsigned` for that path and prints that the release signature was not checked. The ephemeral key under `FELDCORE_OS_WORK` is not a trust anchor. Slot A is still left unchanged.

The image is amd64. The OS workflow does not build an arm64 disk. `release.yml` building `feldcored-linux-arm64` does not make an arm64 OS image.

### Where customisation is injected

Inject the answer file onto the ESP. Do not edit slot A, slot B, the state filesystem, the kernel command line, or `loader.conf`.

The ESP is FAT. `mcopy` can write it without mounting, which is how `fill_esp` already works. For a file image the offset is the GPT start of partition 1, 1048576 bytes on the layout above:

`mcopy -o -i <raw>@@1048576 feldcore-install.json ::/feldcore/feldcore-install.json`

On the booted system the same file is `/efi/feldcore/feldcore-install.json`. `fstab` mounts that FAT with `umask=0077`, so only root can read it after boot. The raw image and the stick still hold the bytes in the clear for anyone who mounts the ESP elsewhere.

The state partition is the wrong injection point for the CLI. It is ext4, so writing it means mounting or `debugfs`, and that wants root on the operator's machine. The ESP path matches the build.

After a successful apply, delete `/efi/feldcore/feldcore-install.json`. Also delete `/var/lib/feldcore/feldcore-install.json` if a copy was placed there by hand. Write `/var/lib/feldcore/feldcore-install.applied` mode `0600` with the file's `version` and the section names that succeeded. No secret values. A later boot with the marker present and the JSON gone does nothing.

FAT does not scrub deleted clusters. Deleting the file stops the next boot from reading it. It does not erase a password from the stick. The CLI warning has to say that.

### How first boot applies it

`feldcore-state.service` runs `prepare-state` before `feldcore.service`. The `/etc` overlay is up by the time `feldcored` starts. The ESP is mounted at `/efi`. The applicator runs inside `feldcored`, after the secrets box, accounts, tunnel, VPN, mail, and Proton Drive services are wired, and before `ListenAndServe`. A shell unit cannot call `applyHost` or `applyAdmin`. Those methods need the Go services. Putting the logic in a shell script would be a second mechanism.

`cmd/feldcored/main.go` today calls `EnsureCode` early and prints `FELDCORE setup code: XXXX-XXXX` to stdout, which `feldcore.service` sends to the journal and the console. The HTTP server starts later. The claim window is open from that print until `ConsumeCode`. The applicator has to run before the server listens, so a host on the LAN cannot claim the box while the file is also creating the administrator.

Order:

1. Read the answer file. Log the path and the section names. Do not log the body.
2. If the file names `wifi`, `ssh`, or `user` and this binary has no writer for that section, stop before changing the system. Leave the file. Log the section name. The console setup code still works.
3. If the file will create the administrator, do not print the setup code.
4. If the file sets `setupCode` and does not create an administrator, write `/var/lib/feldcore/setup-code` mode `0600` before `EnsureCode`. `EnsureCode` keeps a valid code that is already there.
5. Call the writers below. Record wizard fields the same way `Service.apply` does. The writers do not update `store.Wizard` themselves.
6. On full success, delete the JSON, write the marker, and if an administrator was created, set `Wizard.Done` and call `shares.EnsureDefaults` for `appdata`, `system`, `domains`, and `isos`. `main` decides whether to create those shares from a snapshot taken before the applicator runs, so the applicator has to call `EnsureDefaults` itself when it finishes setup.
7. On failure, leave the JSON in place, record which sections finished, and log the step name plus the error text the writer already returns. Do not log secrets. A retry must be safe: hostname and network overwrite their own files, `applyAdmin` stops when an administrator already exists (`ErrSetupDone`), `ConsumeCode` has already removed the code.

Do not set `FELDCORE_SKIP_WIZARD`. That flag marks the wizard done and copies only hostname, time zone, locale, and network mode. It drops the remote and backup fields and applies nothing by itself.

Do not drive this through `Service.Apply`. That path is the interactive wizard: it checks the setup code, refuses a step that is not the current one, and can issue a browser session. The applicator calls the writers directly.

Storage formatting stays out of the file. The wizard's storage step can erase a disk. The data disk's serial is not known when the image is built. A headless file that names a device would be guessing on a multi-drive machine. Shares stay on the default root. The operator picks a data disk later, in the wizard or on the Storage page, which already refuses the OS disk.

## Answer file

`version` is `1`. A different version is refused. Unknown fields are refused, so a typo does not silently drop a password. The CLI validates with the same rules as the writers before it injects.

```json
{
  "version": 1,
  "hostname": "feldbox",
  "timezone": "Europe/London",
  "locale": "en_GB.UTF-8",
  "ntp": "systemd-timesyncd",
  "ntpServers": "",
  "network": { "mode": "dhcp", "iface": "", "address": "", "gateway": "", "dns": "" },
  "wifi": { "ssid": "", "psk": "", "country": "" },
  "ssh": { "enable": false, "permitRoot": false, "authorizedKeys": [] },
  "user": { "name": "", "password": "", "sudo": false, "authorizedKeys": [] },
  "admin": { "username": "", "password": "", "skipTotp": true },
  "setupCode": "",
  "remote": { "mode": "none", "cfMode": "token", "cfToken": "", "cfApi": "", "wgConf": "" },
  "notify": { "kind": "none" },
  "backup": { "kind": "none", "user": "", "password": "", "schedule": "" },
  "firewall": false
}
```

Empty strings and omitted objects mean "do not change that section". `wifi`, `ssh`, and `user` are omitted until the image can apply them. The CLI refuses to inject a file that contains them while those writers are missing.

| JSON | `Request` field | Writer | First boot |
| --- | --- | --- | --- |
| `hostname` | `Hostname` | `applyHost` | `hostnamectl set-hostname`. Must match `HostPlan` (`hostRe`). The image default in `os/image/etc/hostname` is `feldcore`. Persists through the `/etc` overlay. |
| `timezone` | `Timezone` | `applyHost` | `timedatectl set-timezone`. A zone like `Europe/London` (`tzRe`). |
| `locale` | `Locale` | `applyHost` | `localectl set-locale LANG=…`, or `/etc/default/locale` if `localectl` fails. Empty leaves the image locale. Pattern is `localeRe` in `plan.go`. |
| `ntp` | `NTP` | `applyHost` | `systemd-timesyncd` enables `systemd-timesyncd` and `timedatectl set-ntp true`. `chrony` returns an error on the appliance because chrony is not installed and apt is a stub. The CLI rejects `chrony` for this image. |
| `ntpServers` | none | `writeNTP` via `SaveSystem` | Optional space-separated list written to `/etc/systemd/timesyncd.conf.d/feldcore.conf`. Empty leaves the compiled-in defaults. This is the Settings NTP list, not the wizard's chrony-versus-timesyncd switch. |
| `network.mode` | `NetMode` | `SaveNetwork` | `dhcp` or empty: do not write `10-feldcore.network`. `20-wired.network` already matches `e*` with `DHCP=yes`. Record `Wizard.NetMode=dhcp`. `static`: call `applyNet`. |
| `network.iface` | `Iface` | `SaveNetwork` | Static only. `ifaceRe`. Empty means the applicator uses the interface `fillNet` would show: the default-route interface, and only when that interface has an IPv4 address. `fillNet` does not fall back to another NIC. If it leaves the name empty, wait until `network-online.target` has been reached, try once more, then fail the section. Do not default to `eth0`. |
| `network.address` | `Address` | `netFile` | IPv4 CIDR, for example `192.168.1.20/24`. `ValidateNet` rejects a gateway off that network. |
| `network.gateway` | `Gateway` | `netFile` | IPv4 on that network. |
| `network.dns` | `DNS` | `netFile` | IPv4 addresses, spaces or commas. Empty omits the `DNS=` line. |
| `wifi.ssid`, `wifi.psk`, `wifi.country` | none | none yet | See below. Refused until a writer and a package exist. |
| `ssh.enable`, `ssh.permitRoot`, `ssh.authorizedKeys` | none | none yet | See below. |
| `user.name`, `user.password`, `user.sudo`, `user.authorizedKeys` | none | none yet | See below. Not `applyAdmin`, and not the Samba user helper. |
| `admin.username` | `Username` | `applyAdmin` | Must match `usernameRe`: a letter or digit, then letters, digits, `.`, `_`, `-`, up to 32 characters. |
| `admin.password` | `Password` and `Password2` | `applyAdmin` | One copy in the file. The applicator sets both `Request` fields to it. `applyAdmin` calls `ValidateStrong`: at most 72 bytes, at least 12 characters, and either a 16-character passphrase with some variety or upper, lower, digit, and symbol. The HTTP claim path (`handleAuthSetup` → `SetupAndLogin`) only requires 8 characters. The answer file uses the wizard rule because it calls `applyAdmin`. |
| `admin.skipTotp` | `SkipTOTP` | `applyAdmin` | Must be true. An authenticator needs a live code from `beginTOTP`. Unattended setup cannot enrol TOTP. Two-factor stays off until the owner turns it on in Settings. |
| `setupCode` | none | write `setup-code`, then `EnsureCode` | Eight characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, the alphabet in `claim.go`. Stored at `/var/lib/feldcore/setup-code` mode `0600`. Mutually exclusive with `admin.password`. `applyAdmin` calls `ConsumeCode`, so a code baked next to a password is deleted before anyone can type it. |
| `remote.mode` | `RemoteMode` | `applyRemote` | `none`, `cloudflare`, or `wireguard`. Empty means skip the section. `none` turns a stored tunnel off. |
| `remote.cfMode` | `CFMode` | `applyRemote` | `token` (default) or `api`. |
| `remote.cfToken` | `CFToken` | `remote.Save` | Tunnel token. The tunnel is named `feldcore` because `applyRemote` hardcodes that name. The token is encrypted under `/var/lib/feldcore/secrets/`. It is not written to `state.json`. |
| `remote.cfApi` | `CFAPI` | `remote.Save` | API token when `cfMode` is `api`. Same storage. `applyRemote` does not set `ExposeAdmin` or `AdminHostname`. Publishing the FELDCORE UI stays off. |
| `remote.wgConf` | `WGConf` | `vpn.Save` then `SetEnabled(true)` | The whole WireGuard config. `applyRemote` saves one tunnel named `Home`, preset `auto`, and enables it. The private key goes to the secrets box. |
| `notify.kind` | `NotifyKind` | `applyNotify` | `none`, `email`, or `ntfy`. Optional. Omitted in the first CLI form. `applyNotify` sends a test message and returns an error when the server cannot be reached. A notify failure must not roll back an administrator that was just created. Record it and continue. Email maps to `MailHost`, `MailPort` (default 587), `MailUser`, `MailPass`, `MailFrom`, `MailTo`, `MailTLS` (default `starttls`). ntfy maps to `NtfyURL` (default `https://ntfy.sh`), `NtfyTopic`, `NtfyToken`. |
| `backup.kind` | `BackupKind` | `applyBackup` | `none` or `proton`. |
| `backup.user` | `BackupUser` | `applyBackup` | Proton account email. |
| `backup.password` | `BackupPass` | `applyBackup` | Encrypted. `applyBackup` sets `Enabled`, remote path `feldcore`, retention 7, and `IncludeAppdata`. It does not set `Mailbox` or `TwoFA` on `backup.Settings`. Those stay on the Settings page after boot. |
| `backup.schedule` | `BackupSchedule` | `applyBackup` | Empty means manual until a time is set in Settings. |
| `firewall` | `Firewall` | `SaveFirewall` | Default false. `true` enables ufw with the existing SSH allow rules (`UFWCommands`). `sshd` is not running, so the rule opens port 22 anyway (`sshPortsFrom` falls back to 22). The field does not install or edit `sshd`. |

`Unattended` in `Request` is Debian's `unattended-upgrades`. `applySecurity` refuses it on the appliance. The answer file has no such field. OS updates stay on the Updates page.

Wizard record after a full apply that created the administrator: `Done=true`, `Step=summary`, plus the fields each writer would have stored (`Hostname`, `Timezone`, `Locale`, `NTP`, `NetMode`, `AdminUser`, `RemoteMode`, `BackupKind`, `NotifyKind`). Optional sections the file omitted are appended to `Skipped`. `TOTP` stays false. The browser then opens the dashboard. `handleAuthSetup` is not used, because the administrator already exists and `Setup` returns `ErrSetupDone`.

When the file only presets `setupCode` and host settings, leave `Done` false. The operator claims through the browser with the known code. `handleAuthSetup` calls `Begin(false)`, which keeps an existing wizard record. The baked hostname and network mode are what the wizard shows.

## Bake-in options

### Hostname, time zone, locale, NTP

Exists today as the host step. First boot calls `applyHost`. A wrong time zone fails that section and leaves the file in place. The serial console is already enabled in the image, so the operator can see the error without SSH.

### Wired network

Exists today. DHCP is the image default and needs no file. Static writes `10-feldcore.network`, which sorts before `20-wired.network`, and reconfigures that interface with `networkctl reconfigure`. A wrong address drops the browser. Recovery is the serial console. SSH recovery works only when SSH was enabled and the address is reachable. The wizard text already says a wrong static address is fixed from the console. The Imager prints the same warning before it injects a static address.

### WiFi

New. No `wpa_supplicant`, `iwd`, or NetworkManager in the package lists. `firmware-misc-nonfree` is in the image; a firmware package is not a supplicant.

Until `os/packages.txt` and `os/mkosi/mkosi.conf` contain a supplicant, and a writer exists:

- The CLI refuses a file that contains `wifi`.
- The applicator, if it sees `wifi`, fails closed before any other section is applied.

When that writer exists, first boot joins `ssid` with `psk`. The country code is the regulatory domain. The PSK is stored in a root-only file under `/etc` so the overlay keeps it. It is removed from the ESP file when apply succeeds. Interface names (`wlan0`, `wlx…`) are not known at image-build time, so the match is the SSID, not a kernel name baked into the JSON. If `network.mode` is `static`, the address fields apply to the wireless interface once it is up; otherwise the wireless interface uses DHCP. `apt-get install` is not a fallback.

Which daemon is an open question. The recommendation is `wpa_supplicant`, because the image already uses systemd-networkd. `iwd` is the other candidate. Pick one in the image PR. Do not install both.

### SSH and a Unix user

New. `openssh-server` is not in the image, apt cannot install it later, and `/home` is tmpfs. An `authorized_keys` file in `/home` would disappear on the next boot.

When the image PR adds `openssh-server` and the writer exists, first boot does the following. Until then the CLI and the applicator refuse the `ssh` and `user` sections the same way they refuse `wifi`.

- The package is installed at image build. The `ssh` unit stays disabled. `ssh.enable: false` leaves it disabled.
- `ssh.enable: true` runs `systemctl enable --now ssh`. Host keys are generated by that first start. They are not baked into the image and they are not fields in the JSON. Baking them would make every stick share a host key.
- Public keys match the pattern `sshKeyRe` in `internal/vm/spec.go` (OpenSSH public-key lines). A block that contains `PRIVATE KEY` is rejected at inject time.
- Root's keys go to `/etc/ssh/authorized_keys/root` mode `0600`, with `AuthorizedKeysFile /etc/ssh/authorized_keys/%u` in a drop-in under `/etc/ssh/sshd_config.d`. `/etc` persists. `/home` does not.
- `permitRoot: true` sets `PermitRootLogin prohibit-password`. `permitRoot: false` sets `PermitRootLogin no`. `PermitRootLogin yes` is not a value the writer can emit. The root password stays locked. There is no JSON field for a root password.
- `user.name` creates a Unix account whose home is `/var/lib/feldcore/home/<name>`, so the directory is on the state partition rather than the tmpfs `/home`. Shell is `/bin/bash`. This path does not call `shares.Manager`. Those accounts are Samba identities with no shell.
- `user.password` empty locks the password. Login is by public key.
- `user.password` set stores a Unix password. The CLI warns. Minimum length is 12 characters, the same floor as `ValidateStrong`, so the OS login is not weaker than the FELDCORE administrator.
- `user.sudo` defaults to false. `sudo` is not in `os/packages.txt`. `true` is refused until the image contains `sudo`. The FELDCORE administrator is the admin of the web UI and does not need a sudo Unix account.

### Setup code and the administrator

Two ways to claim, and the file may use one of them.

**Bake the administrator.** `admin.username` and `admin.password` with `skipTotp: true`. First boot calls `applyAdmin`, which hashes the password, calls `ConsumeCode`, and does not enrol an authenticator. The setup code is not printed. Anyone with the stick has the plaintext password until first boot deletes the file.

**Bake the setup code.** `setupCode` is written to `/var/lib/feldcore/setup-code` before `EnsureCode`. The operator types it on the claim page. `CheckCode` does not consume the code on a wrong guess. Eight failures in five minutes for one client return `ErrRateLimited`. `ConsumeCode` runs only after the account exists. The code is still printed to the console, because `EnsureCode` returns it. The stick and the console both show a secret that can claim the server.

**Bake neither.** Today's behaviour. `EnsureCode` generates a code and prints it. The operator reads the console or `journalctl -u feldcore -b`.

The code file is mode `0600` and the data directory is mode `0700`. The HTTP claim path and `applyAdmin` are the only consumers.

### Cloudflare Tunnel, WireGuard, Proton Drive

These writers exist. First boot calls `applyRemote` or `applyBackup` with the fields in the table. Secrets land in the secrets box. `state.json` keeps the mode and the Proton username, which is what the wizard already stores.

A tunnel token or a WireGuard config in the answer file is enough to reach the box from outside once the network is up. The CLI lists those field names in the warning before inject. `applyRemote` does not publish the admin UI (`ExposeAdmin` stays at its default).

Proton Drive's rclone backend is experimental. The existing wizard text says so. The Imager repeats that when `backup.kind` is `proton`. Mailbox password and Proton 2FA are not fields `applyBackup` accepts.

### Firewall and notifications

`firewall: true` is `SaveFirewall(true)`. Default is off, matching the current wizard, which no longer applies the old security step. Turning it on does not start SSH.

Notifications are in the schema because `applyNotify` exists. They are not in the first form. A test send that fails must not prevent the administrator from existing.

## Writing to USB

The owner runs GORK, a machine with more than one drive. The repository has no disk inventory for that machine. The writer therefore has no special case and no default device, on GORK or on any other host.

These rules apply to the CLI and to any later web or desktop writer. They share one Go function. A front end sends a stable id and a confirm token. It does not send a bare `/dev/sdX` and it does not rank disks.

1. List whole disks only. A name that has `/sys/class/block/<name>/partition` is a partition. `feldcore-install` already refuses those. The Imager does too.
2. Identity is the same order as `applyIdentity`: `serial:<serial>` when the serial is non-empty, otherwise `byid:<basename of the /dev/disk/by-id link>`. An id that would be `path:` is listed and cannot be written. `/dev/sdX` and `/dev/nvmeXnY` are display columns. They move when a USB stick is inserted.
3. There is no default. Omitting `--device` prints the inventory and exits non-zero. The inventory columns are model, size in bytes, transport, serial, by-id, path, removable bit, mount points, filesystem labels, and whether the disk is the OS disk. Removable is a column. It is not a selector. Size is a column and a fit check. The code must not choose the largest disk, the only USB disk, or the first disk.
4. `--device` must match exactly one disk. Zero matches stops. Two disks with the same serial stop. Cheap sticks share serial strings. In that case the operator passes `--device byid:<basename>` for one link, and the confirm text says the serial is not unique.
5. Refuse the OS disk with no override flag. `internal/storage` already treats a disk as the OS disk when it holds `/`, `/boot`, `/boot/efi`, or `/usr`. The writer uses that set and also treats `/efi` and `/var/lib/feldcore` as the OS disk, because those are the appliance ESP and the state partition. It also refuses the disk `feldcore-install`'s `booted_disk` resolves from `findmnt /` and `lsblk PKNAME`. `--force` does not exist for this check.
6. Refuse a disk that has any mounted filesystem, including a mounted child partition. `feldcore-install` already does this. Unmount first. There is no flag that writes a mounted disk.
7. `--yes` is not sufficient. The operator passes `--confirm` equal to the serial, or to the by-id basename when the serial is empty. The token is not `sdb` and not the `ERASE sdb` phrase the Storage page uses. That phrase names the unstable kernel device.
8. Immediately before `open`, resolve the id again and compare sysfs serial (and the by-id basename) with the values that were confirmed. Open the block device with `O_EXCL`. If the path moved, follow the id. If the serial changed, write nothing.
9. Refuse when the device is smaller than the decompressed image. Do not pick a bigger disk to make it fit. Do not shrink the image.
10. If another disk on the host already has a filesystem label `FC-ESP`, `feldcore-a`, `feldcore-b`, `feldcore-state`, or `feldcore-payload`, print that disk's model, serial, and labels in the confirm text. Still require the explicit id. Re-flashing a stick is allowed. Overwriting a data disk that happens to be large is not something the tool should do by default.

`write` uses a temporary decompressed image, writes it, and deletes the temporary file. Secrets then exist on the stick until first boot removes the JSON. `save` keeps the file the operator named and prints that the file contains those secrets.

### Duplicate partition labels

The appliance filesystems are labeled `FC-ESP`, `feldcore-a`, `feldcore-b`, and `feldcore-state`. The installer disk uses `FC-ESP`, `feldcore-a`, and `feldcore-payload`. Slot A is also labeled `feldcore-a` inside the root image (`mkfs.ext4 -L feldcore-a`). The kernel command line is `root=LABEL=feldcore-a`. `fstab` mounts `LABEL=FC-ESP` and `LABEL=feldcore-state`.

`docs/OS-IMAGE.md` avoids the discoverable root GPT type because two such partitions make gpt-auto pick one at random. The label on the command line has the same class of collision once two disks carry `feldcore-a`. A host that boots with more than one of these disks attached can mount the wrong ESP or the wrong state partition. The usual ways to get there:

- The installer USB is still plugged in when the disk it just wrote boots.
- Two appliance images are attached (a stick plus an internal FELDCORE disk, or two sticks).
- GORK is booted while a freshly written stick is still inserted and an existing FELDCORE disk is also present.

The Imager does not relabel partitions. The command line and `fstab` are inside the signed root and the ESP entries, and changing them would fork the image. The mitigation is operational and is printed after every write: boot the target with only one disk that carries those labels. Eject an installer USB before the installed disk's first boot. The first-boot applicator does not try to repair a duplicate label.

`feldcore-install` refuses the disk it booted from. It does not clear labels on the installer USB. The later milestone that copies the answer file onto the target ESP keeps that refusal.

## Security and privacy

Secure Boot is off. Firmware will run the systemd-boot binary on the ESP. The minisign signature covers the release manifest (root, kernel, initrd). It does not cover `feldcore-install.json`, and it does not cover the raw disk as a single file. Injection is limited to that one new file so the root, kernel, and initrd hashes still match after the edit. Anyone who can write the stick can also replace the bootloader. That is the threat model `docs/OS-IMAGE.md` already states.

Secrets the file can hold in plaintext: WiFi PSK, Unix password, administrator password, setup code, Cloudflare tokens, the WireGuard config (including the private key), Proton password, SMTP password, ntfy token. The ESP mount is root-only. The raw file on the HDD and the stick are not.

Before inject, the CLI prints the names of the secret fields that are set. It does not print the values. The operator can omit any of them and finish that step on the console or in the wizard.

Preferences, in order:

1. Omit the secret. Claim from the console code, or type the WiFi password later.
2. Prefer an SSH public key over a Unix password, and do not enable root SSH unless `permitRoot` is set. Root SSH is key-only.
3. For `write`, do not keep a customised image on disk afterwards. The temporary raw is deleted. The stick still holds the file until first boot.
4. For `save`, the output file keeps the secrets. The CLI says so and names the path.

Encrypting the answer file with a passphrase is not in the first milestones. The passphrase would have to be typed on the serial console before apply, which blocks the headless boot this tool is for. Whether to add that later is an open question.

Do not log the JSON body, passwords, PSKs, tokens, the WireGuard config, or the setup code. Audit lines follow `auditTarget`: hostname, `dhcp` or interface plus address, administrator username, remote mode, backup kind. Those are not secrets. The redacting log handler only scrubs values already stored in the secrets box, and only when they are at least 8 characters. The answer file is not in the box yet when it is parsed. The applicator must not pass those strings to `slog`. `feldcore-install` logs to the journal and the console. It must not `cat` the JSON when the copy milestone is built.

`applyAdmin` consumes the setup code. If apply fails before that, and the code was printed, the console has the claim secret. Skip the print when the file is going to create the administrator. If apply then fails, print the code so the console claim still works.

A baked setup code on the stick can claim the server until the administrator exists. That is the same property as the code printed on the console today. A baked administrator password on the stick is the account itself. Prefer the password only when the stick stays with the owner, and prefer the console code when the image will be copied.

After a successful apply, Cloudflare, WireGuard, and Proton secrets are in `/var/lib/feldcore/secrets/` (mode `0600`, master key beside them). They are not copied into `store.Wizard`. The wizard struct has no password fields. Keep it that way.

## Milestones

Each milestone is its own PR against `main`. This document is the first PR. It changes no Go, no image, and no workflow.

| PR | Ships | Stays out |
| --- | --- | --- |
| 1. Applicator | Parse `version: 1`. Hook `feldcored` before `ListenAndServe`. Call the existing writers for host, network, admin, setup code, remote, backup, firewall. Tests with the setup package's fake commander and a temp directory standing in for `/efi`. Fail closed when `wifi`, `ssh`, or `user` is present. | CLI. Image packages. USB write. |
| 2. CLI save | `cmd/feldcore-imager`: download a release, verify GPT and the three manifest hashes, inject with `mtools`, write a `.raw` or `.raw.zst` to a path. `--image` plus `--allow-unsigned` for a local `os/build.sh` output. Validate the JSON with the same rules as the writers. | Opening a block device. |
| 3. CLI write | `devices`, `write`, the ten rules above. Linux only. Tests feed fixture `lsblk` JSON and a fake sysfs, including a case where the largest disk is not selected, the OS disk is refused, a duplicate serial is refused, a `path:` id is refused, a bad confirm token is refused, and a serial that changes before open writes nothing. CI does not write a real disk. | Web UI. macOS and Windows device writes. |
| 4. SSH | Add `openssh-server` to `os/packages.txt` and `os/mkosi/mkosi.conf`. Unit disabled by default. Writer for `ssh` and `user` as specified above. Home directory on the state partition. | WiFi. `sudo` unless it is added in this PR on purpose. |
| 5. WiFi | One supplicant, chosen before the PR, added to both package lists. Writer stores the PSK under `/etc` and joins the SSID. | A second supplicant. |
| 6. Web form | A settings form that downloads `feldcore-install.json`. It does not download the OS image, does not decompress it, and has no API that opens a block device. | The Updates page. |
| 7. Installer copy | `feldcore-install` copies `/efi/feldcore/feldcore-install.json` from the installer onto the target ESP after it writes the layout. It still requires `--yes` and an explicit whole disk, and it still refuses the booted disk. | Unattended choice of the target disk. |
| 8. Desktop | Only if the owner asks. Wails front end on the PR 3 writer. | A Qt port. A second disk-selection implementation. |

PRs 4 and 5 change the OS image, so a stick built from an older release cannot honour those fields. The CLI refuses them unless it is injecting into an image whose package set contains the daemon. The practical check is a flag in `/etc/feldcore-os-release` or a file under `/usr/share/feldcore/` added in those image PRs, read off the root image while verifying. Until that flag is present, refuse.

## Open questions

1. **Appliance image or installer image as the thing the operator writes.** Recommendation: the appliance image (`feldcore-os.raw.zst`) is the save/write output. The installer copy is PR 7, for a USB that installs onto a different disk. Confirm that the stick is meant to be the boot disk, not only an installer.
2. **Unix login account.** The web administrator and the Samba users do not provide an SSH shell. Confirm a real Unix user is wanted, with its home on the state partition because `/home` is tmpfs.
3. **Root SSH.** Recommendation: public key only, and only when `permitRoot` is set. The root password stays locked. Confirm that a root password must not be added.
4. **WiFi daemon.** Recommendation: `wpa_supplicant` next to systemd-networkd. Say if `iwd` is preferred instead.
5. **Claim style.** Bake the administrator password, bake a known setup code, or leave the console code. The file allows the first or the second, not both. The default CLI form should make the operator pick one, with "print a code on the console" as the default when they pick neither.
6. **Grow the state partition to fill the stick.** Recommendation: no. The layout stays what `os/build.sh` wrote, and slot A stays hash-identical to the signed root. A USB has to be at least the decompressed size (about 21 GiB with the default MiB settings). Growing the state filesystem is a separate project.
7. **Desktop app.** Recommendation: do not build it until the CLI has been used. Confirm that a JSON form in the web UI plus the CLI is enough.
8. **Machine id.** Clones share `/etc/machine-id`. Fixing that belongs in the OS image (ship an empty machine-id so systemd generates one on first boot), not in a per-stick edit of slot A. Confirm whether that image change should be its own PR beside this work.
9. **Encrypted answer file.** Recommendation: not in the first milestones, because a passphrase blocks headless boot. Say if a serial-console passphrase mode is required anyway.
10. **Proton mailbox and 2FA, and publishing the admin hostname.** `applyBackup` and `applyRemote` do not take those fields. Recommendation: leave them for Settings after boot. Say if the answer file must set them, which means extending those writers in a later PR.
