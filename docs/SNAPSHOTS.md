# Container snapshots

A snapshot is a point you can put a container back to. It keeps three things:

1. The container's FELDCORE template (image, ports, paths, variables, and the labels FELDCORE stores).
2. The image id, retagged as `feldcore-snap/<name>:<timestamp>` so `docker image prune` does not delete it.
3. Each mapped folder on the host.

Docker named volumes are not copied. They stay attached when you restore, and the snapshot says so.

Open a container and choose **Snapshots**.

## Take a snapshot

**Take snapshot** stops the container, copies its folders, tags the image, and starts the container again. Stopping is what makes the copy consistent: a database is not half-written. Tick **Keep the container running** for a live copy when you cannot stop it. The snapshot is marked live, because a file may have been copied mid-write.

Add a note if you want to remember why you took it ("before the mod update").

FELDCORE also takes a snapshot automatically before a container is updated, recreated, or saved from the template editor. The note is "Automatic snapshot before the container was changed". If that snapshot fails, the edit does not go ahead. FELDCORE's own tunnel and proxy containers are skipped.

## Where the files go

The default folder is the **system** share: `/mnt/feldcore/data/system/snapshots/<container>/<id>/`. A wizard that picked a different shares root is honoured. Each snapshot is a directory with `snapshot.json` (mode `0600`, because the template can contain app passwords) and either archives or filesystem snapshots.

The panel and **Settings → General → Snapshots** show how much space the copies use and how much is free on that disk.

## btrfs, zfs, and tar.zst

For each host folder FELDCORE asks what filesystem it is on.

- **btrfs.** If the folder is a subvolume (`btrfs subvolume show` succeeds), FELDCORE takes a read-only subvolume snapshot. Restore makes a writable snapshot of that and swaps it into place. If the command fails, or the folder is just a directory on btrfs, it falls back to an archive.
- **zfs.** If the folder is the mountpoint of its own dataset, FELDCORE runs `zfs snapshot`. Restore runs `zfs rollback -r`, which discards newer snapshots of that dataset. A folder that only lives *inside* a larger dataset is archived instead, so one app cannot roll back another app's files.
- **Anything else**, including ext4, is a `tar.zst` archive written by FELDCORE itself (no `zstd` program required). Restore moves the current folder aside, extracts the archive, and puts the old folder back if the extract fails.

The snapshot list shows which method was used.

## Restore and delete

**Restore** asks you to confirm. It stops the container, puts the mapped folders back, and recreates the container from the saved template with the image set to the `feldcore-snap/…` tag. If the container was running when the snapshot was taken, it is started again. If it had been removed, it is created.

**Delete** removes the snapshot directory, the btrfs subvolume or zfs snapshot, and the pinned image tag. If a container is still using that image, the files are removed and the image is left installed; the page says so.

## Retention

**Keep this many** and **Keep for this many days** apply per container. After each snapshot, copies older than the age limit are removed, and then the oldest are removed until only N remain. Zero on either setting turns that limit off. The defaults are 5 copies and 30 days. Automatic and manual snapshots count the same.

The newest copy is kept when it is inside both limits. A copy older than the age limit is removed even if you have fewer than N.
