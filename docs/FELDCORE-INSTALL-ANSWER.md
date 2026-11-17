# First-boot install answer file

`feldcore-install.json` is read once by `feldcored` on first boot. It is not the installer file `answers.json` described in `docs/FELDCORE-INSTALL-DATA-POOL.md`. The installer file decides whether `feldcore-install` creates a data partition. This file sets hostname, network, accounts, and the other sections below after the appliance has booted.

The file is plain JSON. It is not encrypted. A passphrase would stop a headless boot. The administrator password, the setup code, the WiFi PSK, the Unix password, and remote tokens are stored in the clear on the ESP until the first successful apply deletes the file. FAT does not scrub deleted clusters, so the stick can still hold those bytes. Prefer an SSH public key to a Unix password. Do not leave the stick attached after boot.

## Where it is read

`feldcored` looks for the file after the setup writers are wired and before it listens for HTTP.

1. `/efi/feldcore/feldcore-install.json` (the ESP, label `FC-ESP`). A hand copy at `/var/lib/feldcore/feldcore-install.json` is also accepted. The ESP copy wins when both exist.
2. If neither path has the file and `/dev/disk/by-label/FC-ESP` exists, the daemon mounts that device read-only on `/efi` when it is not already mounted, then reads the ESP path.
3. The file is copied to `/var/lib/feldcore/feldcore-install.json` mode `0600` and then applied.

A missing file is not an error. `EnsureCode` then prints a random setup code, which is the behaviour without an answer file.

On full success the daemon deletes both copies and writes `/var/lib/feldcore/feldcore-install.applied` mode `0600`. The marker records the file version, the sections that were applied, sections that were skipped, warnings, and notes. It does not record passwords, keys, tokens, or the setup code. A later boot that finds the marker does not apply the file again. It still deletes a leftover JSON copy.

A section that fails for a reason other than a missing package leaves the JSON in place and writes `feldcore-install.partial` with the sections that finished and the step that failed. The error text is redacted. The next boot tries the file again. Hostname, network, SSH, and WiFi overwrite their own files. `applyAdmin` with `skipTotp: true` succeeds when the administrator already exists, so a retry does not roll the account back and does not require the password again.

`notify` is the exception. A failed test send is recorded as a warning. It does not roll back an administrator that was just created, and the file is still consumed when every other section succeeded.

A field whose program is not installed is skipped and logged. It is not treated as applied. `wifi` needs `wpa_supplicant`. `ssh` needs `sshd`. `user.sudo: true` needs `sudo`, which this image does not install. The Unix account is still created, and the note says sudo was skipped. If the program is installed and `systemctl enable` fails, that section fails hard and the JSON stays.

`chrony` is refused before any change. FELDCORE OS keeps time with systemd-timesyncd, and apt cannot install packages later.

## Schema

`version` must be `1`. Unknown fields are refused. The body is one JSON object. Empty strings and omitted objects mean that section is left alone. `ssh` is applied when the object is present, including when `enable` is false.

```json
{
  "version": 1,
  "hostname": "feldbox",
  "timezone": "Europe/London",
  "locale": "en_GB.UTF-8",
  "ntp": "systemd-timesyncd",
  "ntpServers": "time.cloudflare.com",
  "network": { "mode": "dhcp", "iface": "", "address": "", "gateway": "", "dns": "" },
  "wifi": { "ssid": "", "psk": "", "country": "" },
  "ssh": { "enable": false, "permitRoot": false, "authorizedKeys": [] },
  "user": { "name": "", "password": "", "sudo": false, "authorizedKeys": [] },
  "admin": { "username": "", "password": "", "skipTotp": true },
  "setupCode": "",
  "remote": { "mode": "none", "cfMode": "token", "cfToken": "", "cfApi": "", "wgConf": "" },
  "notify": {
    "kind": "none",
    "mailHost": "",
    "mailPort": 587,
    "mailUser": "",
    "mailPass": "",
    "mailFrom": "",
    "mailTo": "",
    "mailTls": "starttls",
    "ntfyUrl": "",
    "ntfyTopic": "",
    "ntfyToken": ""
  },
  "backup": { "kind": "none", "user": "", "password": "", "schedule": "" },
  "firewall": false,
  "dataPool": true
}
```

`admin` and `setupCode` are mutually exclusive. Omit both to keep the random console code.

| JSON | Writer | What first boot does |
| --- | --- | --- |
| `hostname`, `timezone`, `locale`, `ntp` | `applyHost` | Both hostname and timezone are required when any of these four is set. Hostname matches `hostRe`. Time zone matches `tzRe` (for example `Europe/London`). Locale matches `localeRe`. `ntp` is `systemd-timesyncd` when omitted. `chrony` is refused. |
| `ntpServers` | `writeNTP` | Space-separated list in `/etc/systemd/timesyncd.conf.d/feldcore.conf`, and `Prefs.NTPServers`. This is separate from the `ntp` switch. |
| `network.mode` | `SaveNetwork` for static; no file for DHCP | `dhcp` records `Wizard.NetMode=dhcp` and does not write `10-feldcore.network`. `20-wired.network` already matches `e*` with `DHCP=yes`. |
| `network.iface`, `address`, `gateway`, `dns` | `SaveNetwork` / `netFile` | Static only. `address` is an IPv4 CIDR. The gateway must be on that network. DNS is IPv4 addresses separated by spaces or commas. An empty `iface` uses the default-route interface after `network-online.target`, then fails if it is still empty. `eth0` is not assumed. |
| `wifi.ssid`, `wifi.psk`, `wifi.country` | wpa_supplicant config | SSID is required, at most 32 bytes. PSK is 8–63 characters or 64 hex digits. Empty PSK is an open network. Country is a two-letter code. The config is `/etc/wpa_supplicant/feldcore.conf` mode `0600`. `feldcore-wifi.service` is enabled. If `network.mode` is `static` and `iface` is empty, the address is written to `10-feldcore-wifi.network` (`Type=wlan`) instead of a wired file. A static `iface` that is not a wireless name is refused when WiFi is set. DHCP WiFi writes `25-wifi.network`. |
| `ssh.enable` | `systemctl` | `true` runs `systemctl enable --now ssh` (and `sshd` if the unit is named that). `false` disables both units. Host keys are generated on first start. They are not fields in the file. |
| `ssh.permitRoot` | sshd drop-in | `true` sets `PermitRootLogin prohibit-password`. `false` sets `PermitRootLogin no`. The writer never emits `PermitRootLogin yes`. `PasswordAuthentication` stays `no`. The root password stays locked. There is no root-password field. |
| `ssh.authorizedKeys` | `/etc/ssh/authorized_keys/root` | OpenSSH public-key lines, the same shape as `sshKeyRe` in `internal/vm/spec.go`. A line containing `PRIVATE KEY` is refused. Mode `0600`. These keys are for root. |
| `user.name` | `useradd` | `^[a-z_][a-z0-9_-]{0,31}$`. `root` is refused. Home is `/var/lib/feldcore/home/<name>` because `/home` is tmpfs. Shell is `/bin/bash`. This is not the FELDCORE web administrator and not a Samba share user. |
| `user.password` | `chpasswd` on stdin | Empty locks the password (`passwd -l`). A set password must pass `ValidateStrong` (at least 12 characters). It is not placed on a command line. A Unix password is for the console. SSH stays key-only. |
| `user.authorizedKeys` | the user's `authorized_keys` | Same public-key rules. Written under the home directory and under `/etc/ssh/authorized_keys/<name>`. |
| `user.sudo` | `/etc/sudoers.d/feldcore-<name>` | `false` writes nothing. `true` writes `<name> ALL=(ALL) ALL` mode `0440` when `sudo` is installed. This image does not include `sudo`, so `true` is skipped and the account is still created. |
| `admin.username`, `admin.password` | `applyAdmin` | Both required. `Password` and `Password2` are set to the same value. The password must pass `ValidateStrong`. |
| `admin.skipTotp` | `SkipTOTP` | Must be `true`. Unattended setup cannot enrol an authenticator. |
| `setupCode` | `setup-code`, then `EnsureCode` | Eight characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`. Dashes and spaces are ignored (`ABCD-2345`). Stored at `/var/lib/feldcore/setup-code` mode `0600`. The console still prints it. Mutually exclusive with `admin`. |
| `remote.mode` | `applyRemote` | `cloudflare` or `wireguard`. `none` turns a stored tunnel off. Empty skips the section. |
| `remote.cfMode`, `cfToken`, `cfApi` | `remote.Save` | `cfMode` is `token` (default) or `api`. The tunnel name is `feldcore`. Tokens go to the secrets box, not `state.json`. The admin UI is not published. |
| `remote.wgConf` | `vpn.Save` | One tunnel named `Home`, then enabled. |
| `notify.*` | `applyNotify` | `email` or `ntfy`. `none` and empty skip the section. A failed test is a warning, not a rollback. |
| `backup.kind`, `user`, `password`, `schedule` | `applyBackup` | `proton` or skip. Password goes to the secrets box. Empty schedule means manual. |
| `firewall` | `SaveFirewall(true)` | Only `true` enables ufw. `false` and omitted do not disable it. sshd is not edited. |
| `dataPool` | marker only | See below. |

Sections run in this order: host, ntpServers, network, admin or setupCode, user, ssh, wifi, remote, backup, firewall, dataPool, notify.

When the apply creates the administrator, the wizard record is `Done=true`, `Step=summary`, `TOTP=false`, `AdminUser` set, and the host, network, remote, backup, and notify fields that were applied. Omitted remote, notify, and backup sections are listed in `Skipped`, and so is storage. Default shares are created when the shares service is wired. When the file only sets a setup code and host settings, `Done` stays false so the browser claim page uses the known code.

## Data pool

`dataPool` reserves the choice from `docs/FELDCORE-INSTALL-DATA-POOL.md`. This reader does not partition a disk, does not call `sgdisk` or `mkfs`, and does not call `CreatePool`.

| Value | Effect |
| --- | --- |
| omitted | Nothing. A later claim may still create the partition. |
| `true` | Nothing is formatted. A `data-pool.json` that says declined is removed so a later claim can proceed. The marker notes that the partition was not created. |
| `false` | Writes `/var/lib/feldcore/data-pool.json` as `{"declined": true}`. A later `claim-data` treats that as "do not create the partition". |

## Examples

Administrator, DHCP, and SSH key login. No setup code.

```json
{
  "version": 1,
  "hostname": "feldbox",
  "timezone": "Europe/London",
  "network": { "mode": "dhcp" },
  "admin": {
    "username": "admin",
    "password": "a long passphrase that is not this sentence",
    "skipTotp": true
  },
  "ssh": {
    "enable": true,
    "permitRoot": true,
    "authorizedKeys": [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExamplePublicKeyOnly comment"
    ]
  },
  "dataPool": true
}
```

Known setup code, no administrator. The operator claims the web UI in a browser.

```json
{
  "version": 1,
  "hostname": "feldbox",
  "timezone": "UTC",
  "network": { "mode": "dhcp" },
  "setupCode": "ABCD-2345",
  "dataPool": false
}
```
