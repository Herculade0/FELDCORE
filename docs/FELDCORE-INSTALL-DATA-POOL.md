# Install-disk data pool

Scope for using the free space on the FELDCORE OS install disk as a storage pool. This document is the specification. It does not change the installer, the image build, or the daemon.

The published appliance image and `feldcore-install` write four partitions and stop. On a disk larger than that layout the rest of the disk stays unallocated. A 512 GiB target keeps the fixed layout (about 20.5 GiB of partitions) and leaves roughly 491 GiB unused. The owner wants the Proxmox-style result: that remainder becomes usable data space on the same disk, and any other disk in the machine is left alone.

## Recommendation

Keep the ESP, slot A, slot B, and `feldcore-state` at their current sizes, start sectors, GPT types, and labels. After those four partitions, add one GPT partition that fills the rest of the install disk, format it as a single-disk btrfs filesystem, and register that filesystem with the existing pool service under the name `data`.

Do the partition and `mkfs` in `feldcore-install` when the operator is installing, and again from a first-boot unit so a raw copy of `feldcore-os.raw` onto a larger disk gets the same partition. Register, mount, and point appdata and the shares root at the pool from `feldcored` on first start. Do not grow `feldcore-state`. Do not put a data partition into the image `os/build.sh` assembles. Do not run `CreatePool` against the install disk.

The rest of this document is the trade-off against the other shapes, the exact constraints from the current tree, and the work split.

## What the tree does today

### Fixed layout

`os/build.sh` defaults:

| Variable | Default | Role |
| --- | --- | --- |
| `esp_mib` | 256 | ESP, hardcoded |
| `FELDCORE_ROOT_MIB` / `root_mib` | 6144 | ext4 root image |
| `slot_slack` | 128 | added to each slot |
| `FELDCORE_STATE_MIB` / `state_mib` | 8192 | state partition |
| image trailer | 16 | `total` padding so the backup GPT fits in the image file |

`assemble_appliance` sets `slot_mib = root_mib + slot_slack` (6272) and `total = 16 + esp_mib + slot_mib + slot_mib + state_mib` (21008 MiB, 20.515625 GiB). The image file is truncated to that size. `sgdisk` then writes:

| # | GPT name (`-c`) | Type | Start / size | Filesystem label |
| --- | --- | --- | --- | --- |
| 1 | `FC-ESP` | `EF00` | `1M`, `+256M` | `FC-ESP` (FAT volume label) |
| 2 | `feldcore-a` | `8300` | next, `+6272M` | `feldcore-a` (the root image itself) |
| 3 | `feldcore-b` | `8300` | next, `+6272M` | `feldcore-b` (`mkfs.ext4` of an empty slot) |
| 4 | `feldcore-state` | `8300` | next, `+8192M` | `feldcore-state` |

`8300` is the generic Linux filesystem GUID (`0FC63DAF-8483-4772-8E79-3D69D8477DE4`). `docs/OS-IMAGE.md` records why: the discoverable root type makes gpt-auto pick one of two roots at random. The kernel command line names the slot instead.

`feldcore-install` repeats that layout on the target disk. ESP is hardcoded `ESP_MIB=256`. Slot size is `root-mib` from the payload plus a hardcoded 128. State size is `state-mib` from the payload. The sequence is `sgdisk --zap-all`, then the four `-n` lines, `mkfs.vfat -n FC-ESP` on partition 1, `mkfs.ext4 -L feldcore-b` on partition 3, `mkfs.ext4 -L feldcore-state` on partition 4, and `zstd -d` of `feldcore-root.img.zst` onto partition 2. The root image was created with `mkfs.ext4 -L feldcore-a`, so slot A's filesystem label arrives inside the image. Nothing in the installer reads the target disk's size.

On a 512 GiB disk the four partitions plus the 1 MiB primary GPT occupy 20993 MiB. The 16 MiB figure is only padding inside `feldcore-os.raw`. It is not a fifth region the installer reserves. The free space is the target disk minus those partitions and the backup GPT at the end of the physical disk.

### What state is for

`os/image/etc/fstab` mounts `LABEL=FC-ESP` at `/efi` and `LABEL=feldcore-state` at `/var/lib/feldcore`. The root line is absent on purpose. The kernel mounts the slot read-only (`root=LABEL=feldcore-a ro` or `feldcore-b`).

`feldcore.service` sets `FELDCORE_DATA_DIR=/var/lib/feldcore`. These live on the state partition, not in the slot image:

- `state.json`, `storage.json`, secrets, the setup code
- `updates/incoming` (kernel and initrd before they are copied to the ESP)
- sysexts (`/var/lib/feldcore/sysexts`, linked from `/var/lib/extensions`)
- the `/etc` overlay upper dir (`prepare-state`)
- Docker's data-root, `/var/lib/feldcore/docker` (`os/image/etc/docker/daemon.json`)
- containerd's root, `/var/lib/feldcore/containerd` (rewritten in `configure_containerd`)
- libvirt, via the symlink `/var/lib/libvirt` → `/var/lib/feldcore/libvirt` in `os/build.sh`

`docs/ROADMAP.md` treats that partition as the config copy. Data, appdata, and VM disks are supposed to stay off it. An update does not pack state into the slot image.

`/srv` is on the read-only root and is not a shares location. The default shares root is `/mnt/feldcore/data` (`internal/config/config.go`, `-shares-root` / `FELDCORE_SHARES_ROOT`). `/mnt/feldcore` is a bind of `/var/lib/feldcore/mnt`, so the mount point can be created while the root is read-only. The data disk is mounted at that folder, and the files live on the data disk, not on the 8 GiB state partition. The four default share names are `appdata`, `system`, `domains`, and `isos`.

### How slots and state are found

`internal/osupdate/layout.go` `slotDevice` uses `/dev/disk/by-label/feldcore-a` and `feldcore-b` unless a test overrides `SlotA` / `SlotB`. Those udev links are filesystem labels, not GPT partition names. `slotFromCmdline` reads `feldcore.slot=` and `root=LABEL=feldcore-a|feldcore-b`. fstab finds state and the ESP by filesystem label. Partition numbers are not part of boot or update.

`Stage` (`internal/osupdate/service.go`) stats the inactive slot device, refuses a root artifact larger than that device, and also refuses a root over 8 GiB (`8<<30`), a kernel over 128 MiB, or an initrd over 256 MiB. It writes the signed root bytes onto that device, then copies the kernel and initrd onto the ESP and publishes `feldcore-<slot>+3.conf`. The running slot is not opened. State is not opened. The GPT is not read or written.

Boot counting is the entry file name on the ESP (`internal/osupdate/boot.go`). `feldcore-bless.service` renames a successful entry to the stable name. Rollback writes `loader.conf`. The loader file shipped by the image sets `auto-entries no` and `auto-firmware no`.

The signed manifest hashes the decoded root, kernel, and initrd. It does not hash the partition table. `cmd/osmanifest` and the verifier have no record of partition 4's size or of any later partition.

One existing detail the data partition must not make worse: `make_root_image` labels every root image `feldcore-a`, and `Stage` writes those bytes onto whichever slot is inactive. After the first update, slot B's filesystem label is `feldcore-a` until something retitles it, while the boot entry for that slot still says `root=LABEL=feldcore-b` and the GPT name is still `feldcore-b`. Slot discovery for the update code uses the filesystem label. This scope does not retitle slots. It only requires that the new filesystem not use `feldcore-a`, `feldcore-b`, `feldcore-state`, or `FC-ESP`.

### What the pool engine will and will not accept

`internal/storage` is the pool implementation the Storage page and the wizard already call.

- Filesystems: `btrfs`, `zfs`, `xfs`. There is no ext4 pool. ext4 exists only on the wizard's one-disk format path (`mkfs.ext4 -L feldcore`, then an fstab line).
- Layouts in `layoutSpecs`: btrfs `single` (1 or more), `raid1` (2 or more), `raid10` (4 or more, even); ZFS `mirror` (even, 2 or more), `raidz1` (3 or more), `raidz2` (4 or more); XFS `single` (exactly one). There is no ZFS single-disk layout.
- `CreatePool` runs `mkfs.btrfs`, `mkfs.xfs`, or `zpool create` on the whole-disk paths from `Inventory`. It always formats. A confirm string must match each disk's serial, or its by-id name when the serial is empty. A mismatch returns before `mkfs`.
- `probeHost` keeps `TYPE=disk` and backed loop devices. Partitions are counted and their mount points are rolled onto the parent. They are not pool members.
- A disk is `OS: true` when it, or a child, is mounted at `/`, `/boot`, `/boot/efi`, or `/usr`, and `markOS` also marks the parent of those sources. `annotate` then sets `Eligible: false` and `Blocked` to "This disk holds the operating system." `CreatePool`, `AddDisk`, and `ReplaceDisk` all stop on an ineligible disk.
- `guard.check` refuses `mkfs.*`, `wipefs`, `sgdisk`, `sfdisk`, `fdisk`, `parted`, `dd`, `zpool create|add|attach|replace|destroy`, and `btrfs device add|delete|remove` when an argument is the OS disk, the root partition, or the root UUID. `scanOS` records the mount source and `parentDisk` of `/`. It does not automatically record every sibling partition. A command that names `/dev/sda5` while the root is `/dev/sda2` is not caught today. The implementation has to close that hole for format commands.
- Members are matched by serial, by-id, id, or whole-disk path (`sameDisk`). `mountRecorded` resolves a member to a whole disk and then calls `requireDevice`. If the member carries the install disk's serial, that resolution hits the OS disk and the mount is refused.
- Pools mount by filesystem UUID at `MountRoot`/`<name>`, default `/mnt/feldcore/<name>`. `writeUnit` installs a systemd `.mount` unit whose `What=` is `/dev/disk/by-uuid/<uuid>`. `/etc/fstab` is not rewritten for pools. On the appliance, `/etc` is an overlay whose upper directory is on the state partition, so a unit written there survives a slot switch.
- `SetCache` creates `<mount>/appdata` and stores that path as `state.json`'s `appdata`. It does not move files. One pool holds the flag. The pool is not named `cache` (that word is reserved for ZFS).
- `BindShare` creates `<mount>/<share>` and, when the shares root is writable and the share path is missing, symlinks it from the shares root. A real directory at the shares root is left in place.
- `AddDisk` on btrfs is `btrfs device add`. On ZFS it is `zpool attach` to an existing member. A bare `zpool add` of one disk is refused in `AddArgs` because it would drop redundancy. XFS add is refused. `DestroyPool` unmounts and forgets the record. It does not wipe.
- The image packages `btrfs-progs`, `xfsprogs`, and `gdisk` (`os/packages.txt`). It does not package ZFS. `haveTool` offers ZFS only when `zpool list` succeeds.

`CreatePool` cannot create this pool. The free space is a partition on the disk `annotate` has already marked as the OS disk, and `CreatePool` would format a whole disk.

### Install and first-boot paths that exist

| Path | What runs |
| --- | --- |
| `feldcore-install --yes DISK` | Interactive installer. Refuses a partition, the booted disk, and any disk with a mounted filesystem. Then zap and the four partitions. |
| `feldcore.install=DISK` on the installer kernel command line | `feldcore-install.service` runs `feldcore-install --from-cmdline`. Absent that argument, the service exits 0. |
| `dd` of `feldcore-os.raw` (or the zstd) onto a disk | The image's GPT, including the backup header at the end of the 21008 MiB image. No installer runs. The physical disk's tail is invisible until the backup header is moved. |
| `-skip-wizard` / `FELDCORE_SKIP_WIZARD=1` | Daemon headless path. The setup code is still required. Default shares are created when `-default-shares` is on (the default). |
| First-run wizard, Storage step | `keep` a folder, `disk` (use a mount, or ext4 after `ERASE <name>`), or `pool` (mount a pool the controller already has). A new pool name is only recorded. `UseVolume` wires `*storage.Service` in. Assembling disks stays on `CreatePool`. |

There is no Imager and no answer file in this repository. GitHub code search on `Herculade0/FELDCORE` for an imager or an answer file returns nothing. The answer file below is the contract an Imager would write. The installer is the reader. The disk to wipe is still the `--yes` argument or `feldcore.install=`.

`cmd/feldcored/main.go` starts `EnsureDefaults` in a goroutine before `storage.Open`. `Restore` runs later and only mounts pools already in `storage.json`. An adopt step has to finish, and the shares root has to move, before `EnsureDefaults` runs. Otherwise the headless path creates `appdata` at the default `/mnt/feldcore/data/appdata` before the data disk is mounted. That directory is on the state bind, and the later mount hides it. `EnsureDefaults` does not retry.

## 1. Options

### (a) Fifth partition, registered as a pool

Add GPT partition 5, type `8300`, name `feldcore-data`, on the free space after partition 4. Format it btrfs, label `feldcore-data`, profile single data and dup metadata (the same profile `FormatArgs` uses for one btrfs device). Record it in `storage.json` as pool `data`, mount it at `/mnt/feldcore/data` by UUID, set it as the cache pool, and point the shares root at that mount when the operator has not chosen one.

Consequences:

- Slot and state labels, sizes, and start sectors stay put. Update, boot counting, and rollback keep using the ESP and the slot labels.
- The Storage page shows a real pool: mount, usage, scrub, trim, cache flag, share placement. Those code paths already exist.
- The OS disk stays ineligible. A second disk joins this pool only through `AddDisk`, which still demands that disk's serial or by-id name.
- `CreatePool` cannot be the registration call. A new adopt path records an existing filesystem and does not format.
- The member must not carry the parent disk's serial or by-id, or `sameDisk` treats the whole OS disk as the pool member and `mountRecorded` refuses to mount it.
- ext4 is not a pool filesystem in this tree. Choosing ext4 would skip cache, scrub, UUID mount units, and `AddDisk`, and would go through the wizard format path, which also refuses the OS disk.

### (b) Grow `feldcore-state` to the end of the disk

Create partition 4 at `state-mib` from the payload is replaced by "the rest of the disk". The filesystem stays ext4 with label `feldcore-state`, mounted at `/var/lib/feldcore`.

Consequences:

- fstab and `feldcore-state.service` keep working, because they key on the label and the mount point. An update still does not open the partition.
- The Storage page does not gain a pool. `SetCache`, scrub, trim, and `AddDisk` do not apply. The default shares folder is still `/mnt/feldcore/data`. Without a separate data disk, a folder kept there sits on the state partition, because `/mnt/feldcore` is a bind of `/var/lib/feldcore/mnt`.
- Docker images, containerd content, sysexts, the `/etc` overlay, incoming update files, and the operator's bulk data share one filesystem. Filling the data area stops config writes, extension installs, and update staging. `docs/OS-IMAGE.md` already says the state partition is too small to cache a root image; making it large does not make it the right cache, because a full state partition takes the appliance down with it.
- A copy of the config partition, which the roadmap describes as the thing you restore to get settings back, becomes a copy of the array.
- `FELDCORE_STATE_MIB` stops meaning the partition size. Any tool that sizes state from `state-mib` (the installer does) has to grow a special case.
- A later disk cannot be added to an ext4 filesystem.

### (c) Leave the four partitions fixed, and put the pool on the remainder

This is (a), with one extra constraint: `assemble_appliance` keeps building a 21008 MiB image. The remainder is claimed on the target disk at install or first boot. The image artifact does not contain a data partition, because the image does not know the target disk's size. Baking a data partition into `feldcore-os.raw` would either stay tiny or grow the published disk to whatever size the build host picked.

(c) without a partition, using the raw free space as a btrfs or ZFS member, does not work. Free space is not a device node. `CreatePool` and `mkfs` take device paths. LVM physical volumes are not a layout this tree supports.

### Choice

(a) implemented under the constraint in (c). The image build stays fixed. Partition 5 is additive, on the install disk only, and is the only new GPT entry. (b) is rejected because it merges the config partition with the array and never becomes a pool.

## 2. Where the remainder is claimed

Two moments, one script.

`os/image/usr/lib/feldcore/claim-data` creates partition 5 and formats it. It does not mount, and it does not write `storage.json`.

`feldcore-install` calls it after partitions 1–4 exist and after `partx` / `udevadm settle`, when the operator asked for a data pool. The installer has already checked that the target is a whole disk, is not the booted disk, and has no mounted filesystem.

`feldcore-data-pool.service` calls the same script on the appliance before `feldcore.service`. This covers `dd` of `feldcore-os.raw`, and an installer image that predates the script. The unit orders `After=feldcore-state.service` and `Before=feldcore.service`, so the declined marker on state is visible and Docker has not yet started. The script no-ops unless the booted disk contains GPT names `FC-ESP`, `feldcore-a`, `feldcore-b`, and `feldcore-state`. The installer disk has `feldcore-payload` in place of slot B and state, so the unit does nothing there. The booted disk comes from `findmnt -n -o SOURCE /` and `lsblk -no PKNAME`, the same way `booted_disk` in `feldcore-install` works. The unit does not take a disk from the kernel command line.

`feldcored` then adopts the filesystem into `storage.json`. That has to be synchronous, after `storage.Open`, and before `EnsureDefaults`. `Restore` mounts pools that are already recorded; adopt inserts the record and mounts once, and later boots take the normal `Restore` path plus the systemd unit `writeUnit` installed.

Claim rules, in order:

1. Resolve the disk. In `--install` mode the installer passes the target. In `--boot` mode the disk is the parent of `/`. Any other disk is not opened.
2. Read the GPT. Partitions 1–4 must still be `FC-ESP`, `feldcore-a`, `feldcore-b`, `feldcore-state`, type `EF00` then three `8300`, and partition 5 if present must start at or after the end of partition 4. A mismatch exits non-zero and formats nothing.
3. If `/var/lib/feldcore/data-pool.json` contains `{"declined": true}`, exit 0.
4. If this disk already has GPT name `feldcore-data` and `blkid` shows a filesystem, exit 0. Do not `mkfs`.
5. If the name exists and `blkid` shows no filesystem, format that partition and exit 0. This is a rerun after `mkfs` failed.
6. If the unallocated space after partition 4 is below 1024 MiB, exit 0. Do not move the backup GPT. The smoke image in `os/boot-test.sh` is a copy of the 21008 MiB raw file and must hit this branch.
7. Otherwise run `sgdisk -e` on this disk (move the backup header to the end of the physical disk; no change when it is already there), then `sgdisk -n 5:0:0 -t 5:8300 -c 5:feldcore-data`. `0:0` is the largest free block, which after this layout is the tail.
8. Re-read the table. Confirm partition 1 still starts at sector 2048, partitions 1–4 kept their sizes, and partition 5 starts at or after partition 4. Any failure exits non-zero before `mkfs`.
9. `mkfs.btrfs -f -L feldcore-data -d single -m dup` on partition 5 only.

The filesystem label and the GPT name are both `feldcore-data`. The pool's display name is `data`. Discovery uses the GPT name plus "parent is the booted install disk", not the short name, so a filesystem a person labeled `data` on the other disk is ignored.

`sgdisk -e` rewrites the backup header. It does not move partitions 1–4. The installer path, which ran `sgdisk` on the full target, already has the backup header at the end; `-e` is a no-op there and step 7 still adds partition 5 only when it is missing.

Headless install is the same script. `feldcore.install=DISK` reaches `install_disk`. The answer file and the `feldcore.data=` argument in section 6 decide whether the installer writes the declined marker instead of calling `claim-data`. After reboot into the new slot, `-skip-wizard` still skips the browser wizard. Adopt has already pointed the shares root at the pool, so `EnsureDefaults` creates `appdata`, `system`, `domains`, and `isos` on it.

## 3. A/B update, verification, boot counting, rollback

Partition 5 is additive. The implementation must not renumber, resize, or relabel partitions 1–4, and must not change their start sectors.

| Mechanism | What it touches | Effect of partition 5 |
| --- | --- | --- |
| Slot discovery | `/dev/disk/by-label/feldcore-a` and `feldcore-b` | None, as long as the data filesystem label is `feldcore-data`. |
| State and ESP | `LABEL=feldcore-state`, `LABEL=FC-ESP` | None, same reason. |
| `Stage` | Inactive slot device, then ESP kernel, initrd, and `feldcore-<slot>+3.conf` | The slot device size is unchanged, so the "larger than the inactive slot" check is unchanged. The 8 GiB / 128 MiB / 256 MiB caps are unchanged. |
| Signature | SHA-256 of the decoded root, kernel, and initrd against the minisign manifest | The GPT is outside the signed bytes. Partition 5 is not verified and does not invalidate the manifest. |
| Boot counting | Entry file names on the ESP | Unchanged. `auto-entries no` stays, so systemd-boot does not invent an entry for the data partition. |
| Bless and rollback | Rename the entry, or set `default` in `loader.conf` | Unchanged. |
| State contents | `storage.json`, the mount unit on the `/etc` overlay, files on partition 5 | Not in the slot image. A slot switch leaves them mounted. |

GPT type stays `8300`. The x86-64 root GUID (`4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709`) would put a second discoverable root on the disk. The discoverable `/srv` GUID (`3B8F8425-20E0-4F3B-907F-1A25A76F98E8`) would make `systemd-gpt-auto-generator` mount the partition at `/srv` and race the UUID mount. Neither is used.

`guard.check` gains one rule: a format-class command (`mkfs.*`, `wipefs`, `sgdisk`, `sfdisk`, `fdisk`, `parted`, `dd`, `shred`, `zpool` create/destroy/labelclear, `btrfs device` add/delete/remove) whose device argument is the OS parent or any partition of that parent is refused. Mount-by-UUID does not name the device, which is how pools mount today. `btrfs device add` of a later data disk names that other disk, not partition 5.

`ReplaceDisk` refuses a member whose path is a partition of the OS disk, before it builds a command. Replacing that member would be a `btrfs replace` of the install-disk partition. That stays out of this work.

Out of scope, and unchanged: retitling a staged slot so its filesystem label matches `feldcore-b`. The data pool does not depend on that fix. A fix can land separately.

## 4. Filesystem, registration, appdata, a later disk

### Filesystem

btrfs, layout `single`, one device, `-d single -m dup`.

| Candidate | Why it is or is not the auto pool |
| --- | --- |
| btrfs single | Tools are in the image. One device is a legal layout. UUID mount, scrub, and trim already exist. `AddDisk` can add a later whole disk. |
| XFS single | Tools are in the image. `AddDisk` is refused. A later disk would have to be its own pool. |
| ZFS | Not installed. No single-disk layout. `AddArgs` will not `zpool add` one disk onto a pool. A mirror has to be created with two devices. |
| ext4 | Not a pool filesystem. The wizard format path is a different feature and refuses the OS disk. |

The auto pool does not offer a filesystem picker in v1. The answer file accepts `btrfs` only. Any other `dataFS` value makes `feldcore-install` exit before `sgdisk --zap-all`.

### Registration

New function on `*storage.Service`, called once from `cmd/feldcored/main.go`. Name it `AdoptInstallPool`. It is not on the HTTP `Controller` surface and it is not the wizard's `Apply`. It does not call `FormatArgs`.

Behavior:

1. No-op when `/etc/feldcore-os-release` does not say `ID=feldcore`, or when `FELDCORE_OS_ROOT` is set (the update tests point the daemon at a fixture). No-op when the booted disk fails the partition-name check in section 2.
2. No-op when `data-pool.json` says declined, or when no `feldcore-data` partition exists on that disk.
3. Read the filesystem UUID with `blkid`. No-op when `storage.json` lists that UUID under `dismissed`.
4. If a pool with that UUID is already recorded, leave it. `Restore` mounts it.
5. If a pool named `data` exists with a different UUID, log and stop. Do not format, rename, or mount over it.
6. Otherwise append a `poolRec`: name `data`, fs `btrfs`, layout `single`, UUID from `blkid`, mount `/mnt/feldcore/data`, `BootMount` from `writeUnit`. The single member is `ID: "partuuid:<PARTUUID>"`, `Serial: ""`, `ByID: ""`, `Path: "/dev/disk/by-partuuid/<PARTUUID>"`, `SizeBytes` of the partition. Empty serial and by-id are required so `sameDisk` does not match the parent disk in `probeHost`.
7. Mount with the existing `MountArgs` (`mount -t btrfs -o noatime -U <uuid> /mnt/feldcore/data`). If the UUID mount does not come back from `findmnt -M`, unmount and do not save the record. That is the same check `CreatePool` already uses.
8. When `CachePool` is empty and `state.json` `appdata` is empty, call the existing `SetCache("data")`. That creates `/mnt/feldcore/data/appdata`.
9. When the shares root was not forced (`SharesRootForced` is false) and `state.json` `sharesRoot` is empty, set it to `/mnt/feldcore/data`. Do not call `BindShare`. The shares root is the pool mount, which is the same outcome as the wizard's existing "use this pool" branch (`applyExistingPool` returns `rec.Mount`, and `setSharesRoot` stores it). `BindShare` would try to symlink a directory onto itself.
10. `EnsureDefaults` then creates `appdata`, `system`, `domains`, and `isos` as real directories on the pool.

A later boot that finds the UUID already recorded does not call `SetCache` again. Clearing the cache sticks.

`DestroyPool` already refuses while `CachePool` names that pool ("Clear the cache first.") and while a `storage.json` share binding points at it. v1 does not use `BindShare`, so the binding check does not fire for the default shares. Add one guard next to those: if the configured shares root is this pool's mount, refuse and say to choose another folder first. Forget then unmounts, removes the systemd unit (it already deletes the unit file and runs `systemctl disable`), drops the record, and appends the filesystem UUID to `dismissed` in `storage.json`. It still does not wipe. The next boot's adopt sees `dismissed` and does not recreate the pool, and `claim-data` does not format the existing filesystem. Clearing `dismissed` is a later UI action; until then removing the key from `storage.json` is the manual undo. The partition and the files stay.

The Storage page already lists pools from `Inventory` and prints each member path. Pool `data` shows up with no UI change. Add disk and Replace disk build their menus from `disks.filter(d => d.eligible)`. The install disk is not eligible, so it is not offered there. The member line shows the by-partuuid path because the member is not a row in the whole-disk list.

`usedFor` checks `OS` before `Pool`, so the install disk's row stays "Operating system".

### What stays on the 8 GiB state partition

v1 does not retarget Docker, containerd, or libvirt.

- Docker data-root remains `/var/lib/feldcore/docker`.
- containerd root remains `/var/lib/feldcore/containerd`.
- libvirt remains `/var/lib/feldcore/libvirt`.
- The `appdata` share and the cache pointer live on the pool. The `system` share is a directory for the operator. It is not Docker's data-root.

Those three paths are in the read-only image (`daemon.json`, containerd's `config.toml`, the libvirt symlink). Moving them means an image change plus a mount that exists before `docker.service` and `containerd.service`. `feldcored` is too late to perform that mount on first boot: those units order on `feldcore-state.service`, not on the daemon. A follow-up would have `claim-data` or a generator install the UUID mount unit `Before=docker.service containerd.service`, and would bind-mount pool directories over the three state paths only when the pool mounted. v1 does not do that. Images, container writable layers, and libvirt's default pool can still fill the 8 GiB state partition. Section 8 asks the owner whether that follow-up is in the same effort.

### A later disk that should extend or mirror this pool

Adding capacity is the existing Add disk action: `AddDisk` runs `btrfs device add -f <new-disk> /mnt/feldcore/data` after the serial or by-id confirm. The data profile stays `single`. The new member is a normal whole-disk member with a serial. The partition member is unchanged. Free space grows. Redundancy does not.

Mirroring is a balance convert (`btrfs balance start -dconvert=raid1 -mconvert=raid1`). The balance job in `MaintArgs` is an unfiltered `btrfs balance start`, not a convert. v1 does not start a convert from Add disk and does not add a convert button. A convert is a separate change with its own confirm, because it rewrites every block and a second disk is required before it can finish.

XFS cannot take this path. ZFS cannot take it with the layouts and packages in the image.

## 5. A machine with another disk

The owner's machine is the 512 GB install disk plus a 4 TB disk. Auto-pool claims the install disk's remainder only.

`claim-data --boot` opens the parent of `/` and no other node. `claim-data --install` opens the disk named by `--yes` or `feldcore.install=`, which already cannot be the installer's own disk. Neither mode scans `/dev/disk/by-id` for the largest disk.

The 4 TB disk is created from the Storage page with the current `CreatePool` flow: pick the disk, type its serial (or the by-id name when the serial is empty), and format. That path already refuses:

- the OS disk (`annotate`, `requireDevice`, `guard.check`)
- a disk with a mount, or a child mount
- a disk that is already a pool member
- a disk with neither a serial nor a by-id path
- a confirm string that does not match
- a process that is not allowed to format host disks

The 4 TB disk is its own pool. It is not added to pool `data` unless the operator later uses Add disk and types that disk's serial. Add disk is capacity for `data`. It is still not the default for a second disk found at boot.

No code path in this work calls `sgdisk`, `mkfs`, `wipefs`, or `dd` on a disk that does not hold the booted slot (or, during install, the single named target).

## 6. Declining

Default is to create the pool. An operator who wants the tail left unallocated says so at install time. The choice is stored on the state partition so the first-boot unit does not undo it.

`feldcore-install` reads the choice before `sgdisk --zap-all`.

Answer file, first hit wins:

1. `--answers PATH`
2. `feldcore.answers=PATH` on the installer kernel command line
3. `/payload/answers.json` when the payload partition is mounted (`LABEL=feldcore-payload`, the directory `FELDCORE_PAYLOAD` or `/payload`)

Unknown JSON fields are an error, except `disk`. If `disk` is present it must be the same node as the `--yes` / `feldcore.install=` target after symlink resolution. The file cannot select a disk by itself. A missing file is the defaults below. A file that is present and not valid JSON, or that fails the `disk` check, aborts the install before the zap.

```json
{
  "dataPool": true,
  "dataFS": "btrfs",
  "wipeData": false
}
```

| Field | Default | Effect |
| --- | --- | --- |
| `dataPool` | `true` | `false` skips `claim-data` and writes `/var/lib/feldcore/data-pool.json` with `{"declined": true}` on the new state filesystem. |
| `dataFS` | `btrfs` | Only `btrfs` is accepted. Anything else aborts before the zap. |
| `wipeData` | `false` | See section 7. |
| `disk` | absent | Optional cross-check against the target argument. |

Kernel arguments override the file when they are set. They exist so the current cmdline path does not need a file:

| Argument | Effect |
| --- | --- |
| `feldcore.data=off` | `dataPool: false` |
| `feldcore.data=btrfs` | `dataPool: true` |
| `feldcore.wipe-data=1` | `wipeData: true` |

`--no-data` is the flag form of `feldcore.data=off` for the serial console. `--wipe-data` is the flag form of `feldcore.wipe-data=1`.

There is no Imager in this repo. An Imager that appears later drops `answers.json` onto the payload (or passes `--answers`) and still passes the target disk on the command line or as `feldcore.install=`. The daemon does not read the answer file. It reads `data-pool.json` on state.

Deleting `data-pool.json` and rebooting lets `claim-data --boot` create the partition. That is the undo for a decline, until a Storage-page action exists.

The wizard, when pool `data` is already recorded and the storage step has no saved mode, opens on mode `pool` / name `data` instead of `keep`. Applying that path mounts the pool and sets the shares root, which adopt may already have done. An operator can still pick another pool or a folder. Picking `/srv/feldcore` is refused: that folder is on the read-only operating-system image, and the plan tells the operator to choose a folder under `/mnt/feldcore`. The pool remains on the Storage page either way.

`-skip-wizard` never shows the step. Adopt's shares-root write is what the headless path uses, and only when the shares root was not forced and `state.json` does not already have one.

## 7. Idempotency, re-image, update, rollback

### Claim and adopt

Running `claim-data` twice does not format a filesystem that `blkid` already sees on `feldcore-data`. Adopt does not insert a second record for the same UUID. `SetCache` and the shares-root write run only when those fields are empty. A daemon restart calls `Restore`, which mounts and does not format.

A `mkfs` that was interrupted leaves a GPT name and no filesystem. The next claim formats that partition. It does not move or resize partitions 1–4.

### Re-running the installer

`feldcore-install` today runs `sgdisk --zap-all` and destroys every partition on the named disk. That stays the behavior when the target has no `feldcore-data` partition. An install from before this feature (four partitions, no data pool) is still replaced by `--yes`, and the new layout then gains partition 5 unless the operator declined.

When the target already has GPT name `feldcore-data` or filesystem label `feldcore-data`, and `wipeData` is false, `feldcore-install` exits non-zero before `sgdisk --zap-all`. The disk is unchanged. The message names the partition and the two ways to proceed: install to a different disk, or pass `--wipe-data` / `feldcore.wipe-data=1` / `"wipeData": true`.

`wipeData: true` zaps the whole target, including the data pool, then writes the four OS partitions and, unless `dataPool` is false, a new empty partition 5. There is no v1 path that rewrites partitions 1–4 while keeping partition 5's bytes. Preserving the tail across a zap means recreating the same start and end sectors and not formatting them. That is easy to get wrong, and it is not part of this work.

A target that has foreign partitions and no `feldcore-data` label is still wiped by `--yes`. That is the installer's current contract. This scope does not add a "refuse any non-empty disk" check.

### A/B image update and rollback

`Stage`, bless, and rollback do not open partition 5, do not read `data-pool.json`, and do not rewrite `storage.json`. The pool's mount unit lives on the `/etc` overlay, whose upper directory is on state. State is not inside the slot image.

A successful update boots the other slot with the same state partition and the same partition 5. A failed update falls back by boot counting. The fallback slot mounts the same labels. The data pool is still there in both cases.

Rollback does not reboot by itself. After the reboot, `Restore` mounts pool `data` by the UUID already stored. It does not look at which slot is active.

A new OS image that ships this feature, installed as an update onto a machine whose disk was filled by an older installer, has no partition 5. The first boot of that image runs `claim-data --boot` and creates one, unless `data-pool.json` says declined. Updating the OS is not a re-image: it does not zap the disk. Creating partition 5 on that boot is the `dd` / old-installer case, and the four existing partitions stay where they are.

## 8. Milestones

Each milestone is a PR against `main`. Docs that describe current behavior (`docs/OS-IMAGE.md`, `docs/STORAGE.md`, `docs/help/reference/disk-management.md`, the installer paragraph in `README.md`) change in the PR that makes the behavior true, not in this one.

### M1. Claim the tail

- Add `os/image/usr/lib/feldcore/claim-data` and the rules in section 2.
- Call it from `feldcore-install` after partitions 1–4, including the `--from-cmdline` path.
- Add `--no-data`, `--wipe-data`, `--answers`, `feldcore.data=`, `feldcore.wipe-data=`, `feldcore.answers=`, and the payload `answers.json` reader.
- Refuse to zap when `feldcore-data` is already on the target and `wipeData` is false.
- Write `data-pool.json` when declining.
- `assemble_appliance` sizes stay `16 + esp + 2*slot + state`. No data partition in `feldcore-os.raw`.
- Shell test on a loop device with small sizes passed into the same function the installer uses: partitions 1–4 keep start and size, partition 5 starts after partition 4, a second run does not call `mkfs`, a declined marker creates no partition 5, a disk with no free space past partition 4 does not call `sgdisk -e`.

### M2. First boot for a raw image

- `feldcore-data-pool.service` runs `claim-data --boot` and no-ops on the installer disk (no `feldcore-b` and `feldcore-state` on the booted disk).
- Same script as M1. A disk that already has `feldcore-data` is left unformatted.
- The `os/boot-test.sh` image stays four partitions.

### M3. Adopt into the pool service

- `AdoptInstallPool` as specified in section 4, called from `main.go` after `storage.Open` and before `EnsureDefaults`.
- `dismissed` UUIDs in `storage.json`. `DestroyPool` records the UUID when the pool's member is the install-disk partition, and it refuses while the shares root is that pool's mount.
- `guard.check` refuses format-class commands on every partition of the OS parent.
- `ReplaceDisk` refuses that member.
- `SetCache("data")` and the shares-root write under the empty/unforced rules.
- Unit tests with a fake executor: adopt writes the record and the mount argv is `mount -t btrfs -U`, no `mkfs` argv appears, a member with a serial equal to the OS disk is rejected by the test fixture's `sameDisk` check, a second adopt does not duplicate the pool, a dismissed UUID is skipped.
- The existing `osupdate` tests stay green. Slot devices in those tests remain by-label paths.

### M4. Wizard and the written docs

- Wizard storage step preselects pool `data` when that pool is recorded and the step has no saved mode.
- The keep-a-folder plan says the appliance root is read-only when the chosen path is on it.
- Update `docs/OS-IMAGE.md`, `docs/STORAGE.md`, the disk-management help page, and the README installer paragraph to describe partition 5, the decline switch, and the wipe refusal.

Not in these four PRs: a btrfs raid1 convert, moving Docker or libvirt onto the pool, resizing partition 5 when a virtual disk grows, a Storage-page button that clears `dismissed`, ext4 or ZFS as the auto filesystem, and any change to slot labels.

## 9. Open questions

1. Pool name `data`, mounted at `/mnt/feldcore/data`. Say if it should be `array` instead. `array` matches the pool-name rule. `cache` does not, and would be illegal if this were ever a ZFS pool.
2. Free-space floor of 1024 MiB. Below that the tail stays unallocated. Say if a small install disk should never grow a data pool (a higher floor, or a floor of "at least as big as the state partition").
3. Docker's data-root, containerd's root, and libvirt's directory stay on the 8 GiB state partition in v1. The `appdata` share moves. Confirm that split, or say those three should move in the same effort. Moving them is a separate ordering problem: the UUID mount has to exist before `docker.service`, which means the claim unit writes it, not the daemon.
4. Re-install onto a disk that already has `feldcore-data` is refused unless `wipeData` is set. Confirm that. The alternative, recreating partitions 1–4 in place and keeping partition 5's bytes, is not specified here.
5. A second disk found at boot becomes its own pool only after someone uses the Storage page. Add disk onto `data` is available and does not mirror. Confirm that a 4 TB data disk should not be attached to `data` automatically, and that a raid1 convert can wait.
6. The answer-file shape in section 6 is the Imager contract. There is no Imager in this repo. Confirm the field names before an Imager is written against them.
7. After Forget, the pool stays gone across reboot because the UUID is in `dismissed`, and the filesystem is not wiped. Forget is refused while this pool is the cache or the shares root, which is the same order the Storage page already uses for any other pool (clear the cache, move the shares, then forget). Confirm that Forget should keep working that way for this pool.
