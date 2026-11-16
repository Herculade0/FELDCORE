# Proton VPN, Proton Drive and Proton Mail

These three are off until you turn each one on. None of them is an official
Proton integration. Credentials are encrypted in `/var/lib/feldcore/secrets`
and are not written to the log or to `state.json`.

## Proton VPN

**Settings → Network**, in the VPN card, preset **Proton VPN** (or leave it on automatic). Paste a
WireGuard configuration from
[account.proton.me](https://account.proton.me/u/0/vpn/WireGuard).

FELDCORE labels it Proton when the file looks like one of theirs: a server
comment such as `NL-FREE#85`, DNS `10.2.0.1`, or a `protonvpn` endpoint. The
page shows the server and the country. Choosing **Standard WireGuard** keeps
the same file as a generic tunnel even if it looks like Proton.

### NAT-PMP

Generate the configuration with **NAT-PMP (Port Forwarding)** if you want a
forwarded port. FELDCORE reads that comment. While the tunnel is up it sends
a NAT-PMP map request (RFC 6886) to Proton's gateway, which for a
`10.2.0.2/32` address is `10.2.0.1`, from inside the gateway's network
namespace. The public port is shown on the tunnel. It is refreshed about
every 45 seconds, because Proton's mapping expires.

This only works on servers that actually forward ports, and only when the
key in the file was created with NAT-PMP enabled. A free server, or a file
generated without that option, will not return a port. The tunnel still
connects.

Gluetun's own Proton port-forwarding flag is not set. That flag expects
Gluetun to pick the server itself and refuses a pasted custom config.

Routing a container through the Proton tunnel is the same as any other
tunnel: **Route through VPN** in the container form, with the kill switch
described in [VPN.md](VPN.md).

## Proton Drive backups

**Settings → Network**, Proton Drive backups. This uses rclone's `protondrive` backend.
That backend is unofficial. Proton does not support it, and it can break when
Proton changes their API. The page says so.

What is copied:

- `state.json`
- the secrets directory, still encrypted, **including the key that decrypts
  it**, so a restore can read it
- snapshot archives under the data directory's `snapshots` folder, when you
  leave that option on and the folder exists (the same place container
  snapshots use when that feature is installed)
- appdata, only if you turn that option on and the folder exists

It does not copy the rest of the disk.

Set a Proton email, the account password, and if the account has one, the
mailbox password. A one-time 2FA code works for the next sign-in. An OTP
secret (the base32 key from your authenticator) is what a schedule needs,
because a six-digit code expires. rclone is given an obscured password in a
short-lived config file mode `0600`, which is deleted after the run. The
plaintext is not logged.

**Test connection** lists the Drive folder. **Back up now** uploads
`feldcore-<time>.tar.gz`. A daily time (`03:00`) runs while the feature is
on. Retention deletes older files in that folder whose names match
`feldcore-*.tar.gz` and leaves anything else. Zero keeps every archive.

If `rclone` is not installed, FELDCORE runs `rclone/rclone` through Docker
instead. One of those two has to be available.

Versioned backups, including a Proton Drive destination that reuses this
account, are on the Backups page. See [BACKUPS.md](BACKUPS.md). This card
still uploads one `tar.gz`.

## Proton Mail

**Settings → Network**, Notifications. Mail is sent when:

- a container that was running stops, and FELDCORE did not stop, restart or
  edit it
- GitHub has a newer FELDCORE release, and update checks are turned on in
  Settings

Those two events, and later ones such as `backup.failed`, are published on
the in-process bus described in [EVENTS.md](EVENTS.md). Proton delivery is
unchanged: the same SMTP, Bridge, and SMTP-token paths send the mail.

Three ways to send:

| Mode | What you enter |
|------|----------------|
| Generic SMTP | Host, port, username, password, STARTTLS / TLS / none |
| Proton Mail Bridge | A Bridge you already run (host and port, usually `127.0.0.1:1025`), or **Run Bridge as a container** |
| Proton SMTP token | The token username and token from a custom-domain plan. Defaults to `smtp.protonmail.ch:587` with STARTTLS |

The Bridge container is `shenxn/protonmail-bridge`, published only on
`127.0.0.1:1025`. The first login is interactive, in that container's
console; FELDCORE then uses the Bridge SMTP password you paste. Bridge
presents its own certificate, so certificate checks are skipped for Bridge
and only for Bridge.

**Send test email** delivers one message so you can confirm the settings. The
password or token is stored encrypted and is not logged.

Turning notifications off stops delivery. It does not delete the stored password.
