# Storage

FELDCORE's disk and pool control lives in `internal/storage`. The Storage page
and the first-run wizard both call `*storage.Service`. A pool is formatted only
by `CreatePool`, `AddDisk` or `ReplaceDisk`, and each of those refuses until
the typed confirmation matches the disk. The wizard can also format one empty
disk as ext4, and only after the phrase `ERASE <name>` is typed.

## What was already there

The dashboard reports usage of filesystems that are already mounted: mount
point, device, filesystem type and bytes. Before this package, nothing read
SMART, serial, temperature or a by-id path, and nothing created a btrfs, ZFS
or XFS layout, mounted a pool by UUID, scrubbed, balanced, trimmed, spun a
disk down, or replaced a disk. Shares were directories under one shares root.
Appdata was a path string. The job runner existed for package installs.

The setup wizard lists disks and can keep a folder, use a disk that is already
mounted, or format one empty disk as ext4 after `ERASE <name>`. Naming a pool
used to only record a folder. It still does that for a name that is not a pool
yet. A name that already is a pool is mounted and used.

## Wizard interface

`storage.Controller` is the pool surface. `storage.Volume` is the wizard
step's surface (`Disks`, `Plan`, `Apply`). `*storage.Service` implements both.
The daemon opens one service with `storage.Open` and passes it to the wizard
with `UseVolume`. It is the only caller that sets `PermitHostDisks`.

`Disks` is `Inventory`, mapped into the list the setup page already shows:
model, size, mount, serial, by-id path, and what the disk is used for. `Plan`
and `Apply` keep the wizard's three choices.

- **keep** stores shares in a folder and formats nothing.
- **disk** uses a filesystem that is already mounted, or formats that one disk
  as ext4 after `ERASE <name>`. The command runs on the controller's executor,
  which still refuses the operating-system disk. A disk that already belongs
  to a pool is refused. When the daemon may change the system, fstab records
  the filesystem by UUID.
- **pool** mounts a pool the controller already has (`Mount`) and uses that
  pool's folder as the shares root. A new name is only recorded. Assembling
  disks stays on `CreatePool`, because that asks for every serial.

`*storage.Linux` still implements `Volume` for tests that read a fake sysfs
tree. The daemon does not use it.

```go
type Controller interface {
    Inventory(ctx context.Context) (View, error)
    DiskHealth(ctx context.Context) ([]DiskHealth, error)
    CreatePool(ctx context.Context, in CreatePool) (Pool, error)
    DestroyPool(ctx context.Context, name, confirm string) error
    Mount(ctx context.Context, name string) error
    Unmount(ctx context.Context, name string) error
    AddDisk(ctx context.Context, in AddDisk) (AddResult, error)
    ReplaceDisk(ctx context.Context, in ReplaceDisk) (install.Job, error)
    SetCache(ctx context.Context, pool string) (CacheResult, error)
    CachePath() string
    BindShare(ctx context.Context, share, pool string) (Binding, error)
    UnbindShare(ctx context.Context, share string) error
    SharePath(share string) (string, bool)
    SetSpinDown(ctx context.Context, id string, minutes int) error
    SpinDownNow(ctx context.Context, id string) error
    SetSchedule(ctx context.Context, in Schedule) (Schedule, error)
    RunJob(ctx context.Context, pool, kind string) (install.Job, error)
}
```

`CreatePool.Disks` are disk ids from `Inventory`. `CreatePool.Confirm` is the
typed serial of each disk, in the same order, or the by-id name when the disk
has no hardware serial. A mismatch returns before `mkfs` or `zpool create`
runs. `DestroyPool` asks for the pool name. It unmounts and forgets the pool.
It does not wipe the filesystem.

`Restore` mounts pools that were saved and reapplies spin-down. It never
formats. The daemon calls it, then `Loop`, which starts due schedules. Neither
method is on `Controller`.

State is `storage.json` inside the data directory, separate from the main
state file. Live usage and health are not stored.

## HTTP

All of these sit under `/api/v1`. A browser session is the administrator. An
agent token needs `read` to look and `storage` to change. `admin` includes
both.

| Method | Path | Scope |
| --- | --- | --- |
| GET | `/storage` | `read` |
| GET | `/storage/disks` | `read` |
| POST | `/storage/pools` | `storage` |
| POST | `/storage/pools/{name}/mount` | `storage` |
| POST | `/storage/pools/{name}/unmount` | `storage` |
| POST | `/storage/pools/{name}/destroy` | `storage` |
| POST | `/storage/pools/{name}/disks` | `storage` |
| POST | `/storage/pools/{name}/replace` | `storage` |
| POST | `/storage/pools/{name}/jobs` | `storage` |
| PUT | `/storage/pools/{name}/schedule` | `storage` |
| PUT | `/storage/spindown` | `storage` |
| POST | `/storage/spindown` | `storage` |
| POST | `/storage/cache` | `storage` |
| PUT | `/storage/shares/{share}` | `storage` |

`GET /storage/disks` is the disk list for notifications. Each item has the
model, serial, path, by-id path, temperature, SMART health, reallocated,
pending and media-error counts, and what the disk is used for. Storage records
that data and does not send mail. Notifications owns SMART alerting.

Every state-changing call is written to the audit log (`storage.pool.create`,
`storage.pool.destroy`, `storage.pool.mount`, `storage.pool.unmount`,
`storage.pool.add`, `storage.pool.replace`, `storage.scrub`, `storage.balance`,
`storage.trim`, `storage.schedule`, `storage.cache.set`, `storage.cache.clear`,
`storage.share.bind`, `storage.share.unbind`, `storage.spindown`,
`storage.spindown.now`). A failed attempt is recorded too.

## Layouts

| Filesystem | Layout | Disks | Command |
| --- | --- | --- | --- |
| btrfs | single | 1 or more | `mkfs.btrfs -d single -m dup` for one disk, `-m raid1` for more |
| btrfs | raid1 | 2 or more | data and metadata `raid1` |
| btrfs | raid10 | 4 or more, even | data and metadata `raid10` |
| ZFS | mirror | 2 or more, even | `zpool create` with `mirror` pairs, attached with `zpool attach` |
| ZFS | raidz1 | 3 or more | `raidz1` |
| ZFS | raidz2 | 4 or more | `raidz2` |
| XFS | single | exactly one | `mkfs.xfs` |

A layout is offered only when its tools are installed. FELDCORE does not
install btrfs-progs, xfsprogs or ZFS. ZFS is offered when `zpool list` can
talk to the kernel module. A single `zpool add` of one disk is refused,
because it would drop redundancy. RAIDZ expansion uses `zpool attach` and
returns the `zpool` error when the installed OpenZFS cannot expand.

Pool names match `^[a-z][a-z0-9-]{0,31}$`. ZFS will not accept `mirror`,
`raidz`, `raidz1`, `raidz2`, `raidz3`, `cache`, `log`, `spare`, `disk` or
`file` as a pool name. The cache pool is a flag on a pool, not a pool named
`cache`.

## Mounting

btrfs and XFS are mounted by UUID:

```
mount -t <fs> -o noatime -U <uuid> <mount>
```

`blkid` has to report that UUID on a member first. A multi-device btrfs pool
is registered with `btrfs device scan` before the mount. That scan does not
format. The mount point is `/mnt/feldcore/<name>` unless the daemon was given
another root.

When the daemon is root it also writes a systemd `.mount` unit whose `What=`
is `/dev/disk/by-uuid/<uuid>`, and enables it. The unit is copied under the
data directory either way. `/etc/fstab` is not rewritten. ZFS is created with
`-o cachefile=<datadir>/zpool.cache` and imported with `zpool import -c` that
file. Forgetting a ZFS pool exports it. It does not run `zpool destroy`.

Usage comes from the mounted filesystem only when `findmnt -M` says that path
is the pool's mount. A directory that is not a mount point is reported as
unmounted, so the parent disk is not shown as the pool.

## Jobs

Scrub, balance, trim and disk replace are registered on the install job runner
from issue 11, under the names `storage-<kind>-<pool>`. Balance exists only on
btrfs. Trim is `fstrim` for btrfs and XFS, and `zpool trim` for ZFS. A btrfs
RAID pool starts a balance after a disk is added.

A schedule is `scrub`, `balance` or `trim`, every 1 to 8760 hours. The storage
loop checks every 30 seconds and starts a due job on the same runner. The
schedule does not format.

## Spin-down, cache and shares

Spin-down applies to disks whose type is `hdd`. The idle times are off, 5, 10,
20, 30, 60 and 120 minutes, mapped to a fixed `hdparm -S` value. Solid-state,
NVMe, USB flash and virtual disks are refused. `hdparm` is not installed for you.

The cache pool is one pool that holds `appdata`. Setting it creates that
directory and points FELDCORE's appdata path at it. Clearing it forgets the
pointer. Files already in appdata are not moved.

Placing a share on a pool creates `<pool>/<share>` and, when the share folder
is missing, symlinks it from the shares root. A share that already has a real
directory is refused, so files are not moved or deleted. Removing the
placement deletes that symlink and puts an empty folder back. The files stay
on the pool.

## What this will not do

- It will not format, mount or change the operating-system disk, or the
  filesystems mounted at `/`, `/boot`, `/boot/efi` or `/usr`. If that disk
  cannot be identified, the change is refused.
- It will not format a disk that has neither a serial nor a by-id path.
- It will not wipe a disk when a pool is forgotten.
- It will not move existing share or appdata files onto a pool.
- It will not send SMART mail. Read `GET /api/v1/storage/disks` for that.
- It will not install missing tools.
- Tests format loop devices only. The daemon is the only process that sets
  `PermitHostDisks`. A test that is allowed to format passes `Allow` with the
  loop paths it created.

Live ZFS is implemented as real `zpool` commands. It does not run where the
kernel module is missing, which includes the environment these tests ran in.
The same is true of btrfs when the kernel has no btrfs filesystem: the
commands are covered by unit tests, and the loop-device test mounts XFS and
runs btrfs only when `/proc/filesystems` lists it. `fstrim` on a loop device
without discard support fails, and that failure is stored on the job.
