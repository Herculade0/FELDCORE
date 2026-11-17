---
id: backup-data
kind: guide
title: Back up your data
summary: Keep versioned, encrypted copies on a folder, a share, SFTP, or Proton Drive, and restore one file or the whole copy.
icon: hard-drive
order: 110
popular: true
related:
  - get-alerts
  - share-files
references:
  - backups
  - proton
  - snapshots
---

The Backups page keeps restic repositories: encrypted, deduplicated, and kept as daily, weekly, and monthly versions. The older **Proton Drive backups** card under Settings → Network is a separate one-file upload. Use this page for the copies you can browse and restore.

[[route:backups|Open Backups]]

Container snapshots are a different thing. They put one container back to an earlier point. [[help:snapshots|Snapshots]] covers them.

## Install restic

If restic is missing, the page says so and offers **Install restic**. FELDCORE OS already includes restic and rclone. On another host the button tries apt, then a pinned official binary. A destination cannot run until restic is on the machine. Proton Drive also needs rclone.

## What gets copied

A new destination starts with every set turned on. At least one has to stay on.

| Set | What is copied |
| --- | --- |
| FELDCORE | The server state and the encrypted secrets, including the key that reads them. |
| Appdata | The container appdata folder. |
| Compose | Compose and stack files, plus a working directory Docker reports for a Compose project. |
| Virtual machines | Definitions and disks. A running guest is snapshotted when libvirt can do it. If that fails, the guest is paused for the copy and resumed afterwards. |
| Internal Affairs | The case cabinet, when that folder exists. `.obsidian` is left out. |
| Container snapshots | Snapshot archives, when that folder exists. |

The whole data directory is not copied. Mounts, staging, the restic cache, and restore folders are left out.

## Pick a destination

[[route:backups.destinations|Open destinations]]

1. Choose **New destination** and name it.
2. Pick where it goes.
3. Choose a schedule: manual, daily at a time, or weekly on a weekday and a time. Times are 24-hour, such as `03:00`.
4. Set how many daily, weekly, and monthly versions to keep. The form starts at 7, 4, and 6. Zero in every box keeps every version.
5. Leave the sets you want, and **Save destination**.

| Where | What you fill in |
| --- | --- |
| Local folder or share | An absolute path, or a share name with the path left blank. The repository is a `feldcore-restic` folder inside that path, not the share root itself. |
| SMB | Server, share name, and a user. A blank user is guest. FELDCORE mounts the share for the job and unmounts it after. |
| NFS | Server and export. |
| SFTP | Server, folder, and an unencrypted private key. A key with a passphrase is refused. |
| Proton Drive | A folder under your Drive. **Use saved Proton account** reuses the account on the Settings card, or you can type another. |

Each repository gets its own password on the first backup. It is stored as a secret and is not inside the archive, so the copy cannot open itself. **Repository password** shows it once you ask, and that request is written to the audit log. Copy it and keep it with the backup.

## Run, check, restore

**Back up now** starts a copy. **Check** runs `restic check`. Jobs show progress on the same page: queued, running, succeeded, or failed. One job runs per destination at a time.

**Browse** lists one directory of a copy.

- Restore into a folder under this server's data directory when you want to look before you replace anything.
- **Replace the copies on this server** writes them back at their original paths. The form asks you to type `replace`.

[[route:backups|Open Backups]]

A failed backup, check, or restore can raise an alert. [[help:get-alerts|Get alerts]] is where those messages are routed.

> [!WARNING]
> Replacing the copies on this server overwrites the files at their original paths. Browse and restore into a folder first when you are not sure.
