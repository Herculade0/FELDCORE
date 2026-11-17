---
id: backups
kind: reference
title: Backups
summary: Restic destinations, what each copy includes, and how a restore is confirmed.
icon: hard-drive
order: 345
popular: true
page: backups
related:
  - backup-data
references:
  - proton
  - snapshots
  - compose-stacks
---

Backups is the restic page. The walk-through is [[help:backup-data|Back up your data]].

[[route:backups|Open Backups]]

The Settings → Network card **Proton Drive backups** still uploads one `feldcore-<time>.tar.gz`. That card is unchanged and is described in [[help:proton|Proton Drive and Mail]]. It is not this page. This page is one restic repository per destination.

A destination can be a local folder or share, SMB, NFS, SFTP, or Proton Drive. The repository password is created on the first backup, stored as a secret, and shown only when you press **Repository password**. Removing a destination leaves the copies already stored where they are.

Retention is daily, weekly, and monthly counts. The scheduler looks about once a minute. A failed slot is recorded when the job finishes, so it is not started again every minute.

Restore either writes under the data directory's `restores` folder, or, after you type `replace`, writes the files back at their original paths.

An agent token uses `backups:read` to look and `backups:write` to change destinations, run jobs, and restore. `backups:write` includes `backups:read`. `admin` includes both. The old tar.gz card stays on `admin`.

Saving a destination, deleting one, starting or finishing a job, testing a destination, and revealing the repository password are written to the audit log.

[[help:snapshots|Snapshots]] are per container or per virtual machine. They are not a restic copy of the server.
