---
id: proton
kind: reference
title: Proton Drive and Mail
summary: The Proton VPN preset, the experimental Drive backup, notification mail, and the App Store Bridge for other apps.
icon: mail
order: 280
popular: false
related:
  - connect-vpn
  - backup-data
  - get-alerts
references:
  - vpn
  - backups
  - notifications-health
---

These three are separate switches. None of them is an official Proton integration. Credentials are encrypted in `/var/lib/feldcore/secrets` and are not written to the log or to `state.json`. Versioned copies of the whole server are [[help:backups|Backups]]. Health checks and the other alert channels are [[help:notifications-health|Health]]. This page is the three Settings cards.

## Proton VPN

On the VPN card, paste a WireGuard config from your Proton account. FELDCORE labels it Proton when it sees a server comment such as `NL-FREE#85`, DNS `10.2.0.1`, or a `protonvpn` endpoint. The card shows the server and the country. **Standard WireGuard** keeps the same file as a generic tunnel.

[[route:settings.vpn|Open VPN]]

Generate the config with **NAT-PMP (Port Forwarding)** if you want a forwarded port. While the tunnel is up, FELDCORE sends a NAT-PMP map request to Proton's gateway (`10.2.0.1` when the address is `10.2.0.2/32`) from inside the gateway. The public port is shown on the tunnel and refreshed about every 45 seconds, because Proton's mapping expires. A free server, or a file generated without NAT-PMP, connects and returns no port.

Gluetun's own Proton port-forwarding flag is not set. That flag expects Gluetun to choose the server, and it refuses a pasted config.

Routing a container is the same as any other tunnel. See [[help:vpn|VPN]].

## Proton Drive

[[route:settings.drive|Open Proton Drive backups]]

The card says experimental. It uses rclone's `protondrive` backend, which Proton does not support, and it can break when Proton changes their API.

What a run copies:

- `state.json`
- the secrets directory, still encrypted, including the key that decrypts it, so a restore can read it
- snapshot archives under the data directory's `snapshots` folder, when that option is on and the folder exists
- appdata, only if you turn that option on and the folder exists

It does not copy the rest of the disk.

You set a Proton email, the account password, and, if the account has one, the mailbox password. A one-time 2FA code works for the next sign-in. A schedule needs an OTP secret (the base32 key from your authenticator), because a six-digit code expires. rclone receives an obscured password in a short-lived config file, mode `0600`, which is deleted after the run.

**Test connection** lists the Drive folder. **Back up now** uploads `feldcore-<time>.tar.gz`. A daily time such as `03:00` runs while the feature is on. Retention deletes older files in that folder whose names match `feldcore-*.tar.gz` and leaves anything else. Zero keeps every archive.

If `rclone` is not installed, FELDCORE runs `rclone/rclone` through Docker. One of the two has to be available.

> [!WARNING]
> The archive includes the key that decrypts your secrets. Treat the Drive folder like a copy of the server's keys, not like a photo album.

## Proton Mail

[[route:settings.mail|Open Notifications]]

Mail is sent when a container stops unexpectedly (you did not stop, restart, or edit it), and when update checks are on and GitHub has a newer FELDCORE release. Those are the only two events.

| Mode | What you enter |
| --- | --- |
| Generic SMTP | Host, port, username, password, and STARTTLS, TLS, or none |
| Proton Mail Bridge | A Bridge you already run, usually `127.0.0.1:1025`, or **Run Bridge as a container** |
| Proton SMTP token | The token username and token from a custom-domain plan. Defaults to `smtp.protonmail.ch:587` with STARTTLS |

The Bridge container is `shenxn/protonmail-bridge`, published only on `127.0.0.1:1025`. The first login is interactive, in that container's console. FELDCORE then uses the Bridge SMTP password you paste. Bridge presents its own certificate, so certificate checks are skipped for Bridge and only for Bridge.

**Send test email** delivers one message. The password or token is stored encrypted. Turning notifications off stops delivery and keeps the password.

## Proton Bridge for other apps

[[route:apps|Open the App Store]]

The App Store lists Proton Bridge. Its purpose is AI integration: other FELDCORE apps and bots read the mailbox over IMAP and send through SMTP. This is a different container from the notification Bridge above. The notification one publishes SMTP only on `127.0.0.1:1025`. The App Store one publishes IMAP and SMTP on the LAN, and you can set each bind address to `127.0.0.1` to keep them on this server.

Bridge needs a paid Proton plan: Mail Plus, Unlimited, Family, Duo, or Visionary. Proton Free cannot use Bridge.

The template stores the keychain in a data folder mounted at `/root`. That folder is what keeps the login across restarts. The container restarts unless you stop it.

Sign-in cannot be automated. Proton asks for the account password and a 2FA code.

1. Create the container from the App Store, then open **Mailbox access** on its Docker row.
2. Start it and open **Console**. That is a shell in the container, the same as `docker exec`.
3. Bridge is already running, and only one copy can use the keychain. Run:

```
pkill -x protonmail-bridge || true
pkill -x socat || true
/protonmail/entrypoint.sh init
```

On a new data folder, init creates the GPG key and the pass keychain, then opens the Bridge CLI. If init says the key already exists, run `protonmail-bridge --cli` instead.

4. At the `>>>` prompt, type `login`, then the Proton username, the account password, and the 2FA code. An account with a mailbox password gets one more prompt.
5. Type `info`. The username and password in that printout are the Bridge credentials. Type `exit`.
6. Restart the container. Then choose **Read from Bridge**, or paste the username and password from `info`.

Read from Bridge stops the container, runs `info` against the data folder, stores the Bridge password encrypted, and starts the container again. It does not log in. The panel then shows the host and port other apps should use. `info` prints `127.0.0.1` with ports 1143 and 1025, which are inside the container. Clients use the published host ports. Bridge presents its own certificate, so clients skip certificate checks for these ports.

A bot needs the `read` scope to open MCP and the `mail` scope to call `proton_bridge` or `GET /api/v1/integrations/proton-bridge`. `admin` includes `mail`. The password is not written to `state.json` or the log.

When Proton forces a new login, leave the data folder in place. Open the console, stop Bridge with the same `pkill` commands, and run `protonmail-bridge --cli`. If the account is already listed, type `logout`, then `login`, and complete the password and 2FA prompts again. Type `info` and `exit`, restart the container, and read the credentials again whenever `info` shows a new password. Do not delete the data folder to re-authenticate.
