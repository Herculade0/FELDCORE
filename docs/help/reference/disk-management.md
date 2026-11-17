---
id: disk-management
kind: reference
title: Storage
summary: Disks, pools, spin-down, and where a share sits. Formatting waits until you type the serial.
icon: disc
order: 205
popular: true
page: storage
related:
  - share-files
  - backup-data
references:
  - dashboard
  - shares
---

Storage lists the disks FELDCORE can see and the pools you have created. The dashboard still shows mounted filesystems. This page is where a disk becomes a pool.

[[route:storage|Open Storage]]

If FELDCORE is not running as root, the page can list disks and then stops. It will not format, mount, or spin a disk down. The line under the title names the tools it found: btrfs, XFS, ZFS, and SMART. A layout is offered only when its tools are already installed. FELDCORE does not install them for you.

Pools mount under the path shown there, `/mnt/feldcore/<name>` unless this server was given another root.

## Create a pool

[[route:storage.pools|Open pools]]

1. Choose **New pool**.
2. Name it with a lower-case letter, then letters, numbers, or hyphens. At most 32 characters.
3. Pick a filesystem and layout. What you can pick depends on how many disks you select, and on which tools are installed.
4. Tick the disks. Type each disk's serial, or its by-id name when it has no serial.
5. **Erase and create** formats those disks. The files on them are destroyed.

| Filesystem | Layout | Disks |
| --- | --- | --- |
| btrfs | single | 1 or more |
| btrfs | raid1 | 2 or more |
| btrfs | raid10 | 4 or more, and an even number |
| ZFS | mirror | 2 or more, and an even number |
| ZFS | raidz1 | 3 or more |
| ZFS | raidz2 | 4 or more |
| XFS | single | exactly one |

Nothing is formatted until the text you type matches that disk. The operating-system disk, and the filesystems at `/`, `/boot`, `/boot/efi`, and `/usr`, are refused. A disk with neither a serial nor a by-id path is refused. A disk that already belongs to a pool is refused.

**Forget** asks you to type the pool name. It unmounts the pool and forgets it. It does not wipe the filesystem. For ZFS that is an export, not `zpool destroy`.

## After the pool exists

On a mounted pool you can queue **Scrub**, **Balance** (btrfs only), and **Trim**. **Schedule** runs one of those every so many hours, from 1 to 8760. **Add disk** and **Replace disk** ask for the new disk's serial the same way creating a pool does. XFS is one disk, so those two stay off.

**Use for appdata** points the appdata folder at that pool. Existing files are not moved. Clearing the cache pool forgets the pointer and leaves the files where they are.

**Spin-down** is for hard disks, and only when `hdparm` is installed. The choices are off, 5, 10, 20, 30, 60, and 120 minutes. Solid-state, NVMe, USB flash, and virtual disks are refused.

## Shares on a pool

The Shares card places a share onto a pool. That creates a folder on the pool and, when the share does not already have a real directory, a link from the shares root. A share that already has a directory is refused, so files are not moved or deleted. Removing the placement puts an empty folder back. The files stay on the pool.

[[route:shares|Open Shares]]

> [!WARNING]
> **Erase and create** destroys the files on the disks you selected. Copy anything you still want before you type the serial.
