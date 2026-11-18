---
id: shares
kind: reference
title: Shares and folders
summary: SMB and NFS exports, share users, and the folder picker containers use.
icon: folder
order: 210
popular: true
page: shares
related:
  - share-files
references: []
---

A share is a directory FELDCORE created under the shares root, plus the Samba and NFS config it writes for that directory. The default root is `/mnt/feldcore/data`. Config is written as a drop-in, next to anything you edited by hand, and the services are reloaded through systemd.

The task-shaped version of this is [[help:share-files|Share files]].

## SMB

| Mode | Samba behaviour |
| --- | --- |
| Public | Guest can read and write. |
| Read-only | Guest can read. Writes over SMB are refused. |
| Private | `valid users` is the list you picked. No guest. |

Share users are created from the **Share users** card. Changing a password there updates that Samba account. Deleting the user removes the account. It does not delete shares.

## NFS

The export is written under `/etc/exports.d/` and applied with `exportfs`. You set the client list and whether the export is read-only. NFS does not use the SMB user list.

## The folder picker

Containers do not get a separate file manager. **Add configuration → Path** suggests shares when the field is focused and empty. Once the value contains `/`, it asks the server for real directories under what you have typed. That list is a suggestion. You can still type a path. The server refuses to treat a non-directory as a share folder.

## What this page does not do

It does not show who is connected, or how full each share is. It does not rename a share. It does not export a folder that already lives somewhere outside the shares root. Deleting a share unpublishes it and removes the directory only when the directory is empty.

Default folders a wizard may create, when that wizard runs, are `appdata` (container data), `system`, `domains` (VM disks), and `isos`. You can create the same names yourself.

[[route:shares|Open Shares]]
