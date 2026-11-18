---
id: share-files
kind: guide
title: Share files
summary: Create a folder and export it over SMB, NFS, or both.
icon: folder
order: 70
popular: false
related:
  - secure-your-box
  - run-docker
references:
  - shares
---

A share is a folder on this server that other computers can open. On a stock install the folders live under `/mnt/feldcore/data`. A wizard that picked another shares root is honoured.

## Create one

1. If Windows file sharing or NFS is not installed, use the button on that card. On a Debian or Ubuntu host FELDCORE installs the package with apt and starts it. On FELDCORE OS both are already in the image, and the button only starts them.
2. Press **New share**. The name becomes the folder name. Letters, numbers, hyphens, and underscores. You cannot rename it later.
3. Turn on SMB, NFS, or both, and save.

[[route:shares|Open Shares]]

## Who can open it

SMB has three modes:

| Mode | Who |
| --- | --- |
| Public | Anyone on the network can read and write. No password. |
| Read-only | Anyone on the network can read. Nobody can change files over SMB. |
| Private | Only the share users you tick, each with a password. |

Add those users on the **Share users** card. They are Samba accounts, not extra FELDCORE logins. There is still one administrator for this website.

NFS takes a list of clients (a network such as `192.168.0.0/24`, or hostnames) and an optional read-only flag.

> [!WARNING]
> Public and SMB read-only are guest access. Use them on a network you trust. Private is the one that asks for a password. FELDCORE has to be running as root or the page will let you look, and shares will not go live.

## Where the files are

There is no full file manager in the app. Other computers browse the share with Finder, Explorer, or an NFS mount. When a container needs a folder, the path field on the container form suggests your shares first, and typing `/` lists real directories as you go.

Deleting a share removes the folder only when it is empty. If you have files in it, the share is unpublished and the folder stays.
