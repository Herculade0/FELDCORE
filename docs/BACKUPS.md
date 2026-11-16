# Backups

The Backups page keeps versioned copies of this server. The older Settings
card, **Proton Drive backups**, still uploads one `feldcore-<time>.tar.gz`.
That path is unchanged. The page at **Backups** is a separate restic
repository per destination.

## Why restic

Copies are restic repositories: encrypted, deduplicated, and kept as daily,
weekly and monthly versions. Restic is used instead of Borg because it
reports progress as JSON, it has `--keep-daily`, `--keep-weekly` and
`--keep-monthly`, and it can write through rclone. That is the same rclone
plumbing the Proton Drive card already uses.

The pinned download, used when apt has no restic package, is restic 0.19.1.
The checksums are in `internal/backup/install_bin.go`.

## What a copy includes

Each destination has its own set. A new destination starts with all of these
on. At least one has to stay on.

| Set | What is copied |
| --- | --- |
| FELDCORE | `state.json`, `backup-catalog.json`, and `secrets/`, still encrypted, including the key that reads them. |
| Appdata | The container appdata folder. |
| Compose | `compose` and `stacks` folders, plus any working directory Docker reports for a Compose project. |
| Virtual machines | Definitions, and disks. A running guest is disk-snapshotted when libvirt can do it, then the snapshot is merged back. If the snapshot fails, the guest is paused for the copy and resumed afterwards. Without libvirt, the domains folder is copied as it sits. |
| Internal Affairs | The case cabinet, including `ledger.jsonl`. `.obsidian` is left out. |
| Container snapshots | Snapshot archives, when that folder exists. |

The whole data directory is not copied. Mounts, staging, the restic cache,
downloaded binaries and restore folders are left out.

A successful backup that included Internal Affairs records the ledger head
(the `line` field of the last `ledger.jsonl` row) in `backup-catalog.json`.
Reports are not deleted. How old a report must be before it can be pruned is
not decided, so this page does not prune them. The recorded head is what a
later prune can require.

## Destinations

- **Local folder or share.** The repository is a `feldcore-restic` directory
  inside the path, or inside the share. The share root itself is not filled
  with restic files.
- **SMB and NFS.** FELDCORE mounts the share for the job, with a credentials
  file mode `0600` for SMB, then unmounts it. Guest SMB is used when the user
  name is blank.
- **SFTP.** An unencrypted private key. The connection is `BatchMode`, so a
  passphrase key is refused. The key is written mode `0600` for the job and
  removed afterwards.
- **Proton Drive.** rclone's `protondrive` backend, the same unofficial
  backend as the Settings card. A destination can reuse the account saved in
  Settings, or keep its own. The upload needs `restic` and `rclone` on this
  machine and a live Proton account. FELDCORE does not report success when
  those are missing.

Each repository has its own password, created on the first backup and stored
as a secret (`bkr-` plus the destination id). It is not inside the archive:
the encrypted secrets in the copy cannot open the repository. **Repository
password** shows it once you ask, and that request is written to the audit
log. Copy the password and keep it with the backup.

## Schedule, retention, check

A destination runs manually, daily at a time, or weekly on a weekday and a
time. The scheduler looks about once a minute. A failed slot is recorded
when the job finishes, so it is not started again every minute.

Retention is how many daily, weekly and monthly versions to keep. Zero in
every box keeps every version. The defaults on the form are 7, 4 and 6.

**Check** runs `restic check`. A check can also read a subset of the data.

## Jobs

Backup, check and restore jobs follow the install-job shape: queued, running,
succeeded or failed, with a step, a percent, bytes, a log tail, and polling.
They are a separate runner. Install jobs stay on the admin scope and do not
carry percent or bytes. One job runs per destination at a time. Asking for
the same kind again returns the job that is already running.

## Restore

**Browse** lists one directory of a copy. Restore one file, one folder, or
the whole copy.

- **A restore folder** writes under the data directory's `restores/<job>`
  folder. Snapshot paths are nested under that folder, which is how restic
  restores an absolute path.
- **Replace the copies on this server** writes them back at their original
  paths. The form has to send the confirmation string `replace`.

## API

Browser sessions can call all of these. An agent token needs `backups:read`
for GET and `backups:write` for everything else. `backups:write` includes
`backups:read`. `admin` includes both. `/api/v1/backups` (plural) is the old
tar.gz card and stays on `admin`.

| Method | Path |
| --- | --- |
| GET | `/api/v1/backup` |
| POST | `/api/v1/backup/destinations` |
| PUT, DELETE | `/api/v1/backup/destinations/{id}` |
| POST | `/api/v1/backup/destinations/{id}/test` |
| POST | `/api/v1/backup/destinations/{id}/run` |
| POST | `/api/v1/backup/destinations/{id}/check` |
| POST | `/api/v1/backup/destinations/{id}/password` |
| GET | `/api/v1/backup/destinations/{id}/snapshots` |
| GET | `/api/v1/backup/destinations/{id}/snapshots/{snap}/files?path=` |
| POST | `/api/v1/backup/restore` |
| GET | `/api/v1/backup/jobs` and `/api/v1/backup/jobs/{id}` |

Saving a destination, deleting one, starting a job, finishing a job, failing
a job, testing a destination, and revealing the repository password are
written to the audit log.

Failure events are described in [EVENTS.md](EVENTS.md).

## What needs a machine this environment does not have

The code that builds the rclone config, the restic repository URL, the mount
command, and the libvirt snapshot or pause is real and covered by tests. A
live Proton account, a live SMB, NFS or SFTP server, and a running libvirt
guest are not available here, so those uploads and the quiesce path are not
executed against a remote system. When the tool is missing, the job says so
and, for virtual machines, copies the domains folder without a snapshot.
