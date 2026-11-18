---
id: snapshots
kind: reference
title: Snapshots
summary: Point-in-time copies of a container's template, image, and mapped folders.
icon: camera
order: 300
popular: true
related:
  - run-docker
references:
  - docker
---

A container snapshot keeps three things: the FELDCORE template (image, ports, paths, variables, and labels), the image id retagged as `feldcore-snap/<name>:<timestamp>` so `docker image prune` does not delete it, and each mapped folder on the host.

Docker named volumes are not copied. They stay attached on restore, and the snapshot says so. VM snapshots are a different feature. See [[help:vms|Virtual machines]].

## Take one

Open the container and choose **Snapshots**. **Take snapshot** stops the container, copies its folders, tags the image, and starts it again. Stopping is what makes the copy consistent. Tick **Keep the container running** for a live copy. That snapshot is marked live, because a file may have been copied mid-write.

Add a note if you want to remember why ("before the mod update").

FELDCORE also snapshots automatically before a container is updated, recreated, or saved from the template editor. The note is "Automatic snapshot before the container was changed." If that snapshot fails, the edit does not go ahead. FELDCORE's own tunnel and proxy containers are skipped.

## Where the files go

The default folder is the **system** share: `/mnt/feldcore/data/system/snapshots/<container>/<id>/`, or under whatever shares root a wizard picked. Each snapshot is a directory with `snapshot.json` (mode `0600`, because the template can contain app passwords) and either archives or filesystem snapshots.

[[route:settings.snapshots|Open snapshot settings]]

The panel and that card show how much space the copies use and how much is free on that disk.

## btrfs, zfs, and tar.zst

For each host folder, FELDCORE asks what filesystem it is on.

- **btrfs.** If the folder is a subvolume, it takes a read-only subvolume snapshot. Restore makes a writable snapshot and swaps it into place. Otherwise it falls back to an archive.
- **zfs.** If the folder is the mountpoint of its own dataset, it runs `zfs snapshot`. Restore runs `zfs rollback -r`, which discards newer snapshots of that dataset. A folder that only lives inside a larger dataset is archived, so one app cannot roll back another app's files.
- **Anything else**, including ext4, is a `tar.zst` archive written by FELDCORE itself. No `zstd` program is required. Restore moves the current folder aside, extracts, and puts the old folder back if the extract fails.

The list shows which method was used.

## Restore, delete, retention

**Restore** asks you to confirm. It stops the container, puts the mapped folders back, and recreates the container from the saved template with the image set to the `feldcore-snap/…` tag. If the container was running when the snapshot was taken, it is started again. If it had been removed, it is created.

**Delete** removes the snapshot directory, the btrfs subvolume or zfs snapshot, and the pinned image tag. If a container is still using that image, the files are removed and the image is left installed. The page says so.

**Keep this many** and **Keep for this many days** apply per container. After each snapshot, copies older than the age limit are removed, then the oldest are removed until only N remain. Zero on either setting turns that limit off. The defaults are 5 copies and 30 days. Automatic and manual snapshots count the same. A copy older than the age limit is removed even if you have fewer than N. The newest copy is kept when it is inside both limits.
