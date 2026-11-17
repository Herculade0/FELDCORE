# FELDCORE installer ISO

This is a design for the installer image. It does not change the build. The implementation that follows it is a separate change, and that change is what updates `README.md`, `docs/OS-IMAGE.md`, and `docs/FELDCORE-INSTALL-ANSWER.md`.

Ship a hybrid UEFI ISO, `feldcore-installer.iso`, built by `xorriso` at the end of `os/build.sh`. The ISO is El Torito bootable and a valid GPT disk with an EFI System Partition, so the same file is a Ventoy drop-in, an Etcher flash, and a QEMU `-cdrom`. The installed appliance stays a systemd-boot GPT disk. GRUB exists only on the installer medium.

The answer file stays `feldcore-install.json` version 1, the file `internal/setup/answers.go` already applies. The ISO is read-only and its EFI partition is not a place a Windows user can write, so the installer looks for that file on any other attached FAT or exFAT volume (`/feldcore/feldcore-install.json`, or the volume root), copies it onto the new appliance ESP, and the existing first-boot reader applies it. A missing file is still a normal boot: the appliance prints a setup code and the browser wizard runs.

Keep publishing `feldcore-os.raw.zst` and `feldcore-installer.raw.zst`. The raw appliance image is what `os/qemu.sh` and `os/boot-test.sh` boot. The raw installer stays through the first ISO release so a machine can still be installed with the image that already boots. It goes away only after the Mac mini checklist below has been done on a published ISO.

## What the raw installer actually is

`os/build.sh` `assemble_installer` builds `feldcore-installer.raw` and compresses it to `feldcore-installer.raw.zst`. The disk is a GPT image of a fixed size:

| Partition | `sgdisk` name | Size | Filesystem |
| --- | --- | --- | --- |
| 1, type EF00 | `FC-ESP` | 256 MiB (`esp_mib`) | FAT32, label `FC-ESP` |
| 2, type 8300 | `feldcore-a` | `FELDCORE_ROOT_MIB` (default 6144) + 128 MiB | ext4, the installer root |
| 3, type 8300 | `feldcore-payload` | 2048 MiB | ext4, label `feldcore-payload` |

`fill_esp` writes the ESP with mtools: `EFI/BOOT/BOOTX64.EFI` and `EFI/systemd/systemd-bootx64.efi` (both copies of `systemd-bootx64.efi`), `loader/loader.conf`, `loader/entries/feldcore-a+3.conf`, and `feldcore/a/{vmlinuz,initrd.img}`. The loader line is the same one the appliance uses:

```
root=LABEL=feldcore-a ro console=tty0 console=ttyS0,115200 feldcore.slot=a feldcore.version=VERSION
```

`installer_overlay` copies the appliance root and adds a serial autologin. The payload partition holds `feldcore-root.img.zst`, `vmlinuz`, `initrd.img`, `systemd-bootx64.efi`, and the text files `version`, `root-mib`, and `state-mib`. `os/image/usr/sbin/feldcore-install` mounts `LABEL=feldcore-payload` when `/payload` does not already contain those files, then writes a new appliance disk (ESP, slot A, slot B, state) with `sgdisk` on the device it was given.

`assemble_appliance` is a different image, `feldcore-os.raw.zst`: ESP, slot A, slot B, and an 8 GiB `feldcore-state`. `os/qemu.sh` and `os/boot-test.sh` boot that file. The updater does not. `internal/osupdate/manifest.go` `BuildManifest` hashes `feldcore-root.img.zst`, `vmlinuz`, `initrd.img`, and `sysexts/*.raw` only.

`.github/workflows/os-image.yml` publishes both compressed raw disks, the root image, the kernel, the initrd, `feldcore-os.json`, `feldcore-os.json.minisig`, and `update.pub`. `docs/OS-IMAGE.md` records the GitHub Release asset limit of 2 GiB, which is why the disks are zstd.

### The GPT backup header

`sgdisk -o` writes a primary GPT at LBA 1 and a backup GPT at the last LBA of the image file. `truncate` sets that size from the partition sizes, about 256 + 6272 + 2048 MiB plus the 16 MiB of padding in `assemble_installer`. Etcher and `dd` write those bytes at the start of a larger stick and leave the stick's tail untouched. The backup header then sits in the middle of the stick. Its recorded alternate LBA is the end of the image, not the end of the media.

UEFI firmware boots from the primary GPT, which is why the stick still boots. Windows checks that the backup header is at the last LBA. When it is not, Windows ignores the GPT and shows the protective MBR: one partition, type `0xEE`, which Explorer does not mount. That matches what we saw. Two further facts make "drop the file on the ESP from Windows" a bad requirement even after the header is repaired:

- Windows does not assign a drive letter to an EFI System Partition (type `0xEF` / EF00). `diskpart` can assign one only when it can see the GPT. WSL `--mount` refuses a removable USB.
- Etcher writes the image byte for byte. It does not run `sgdisk -e` to move the backup header to the end of the stick.

A hybrid ISO flashed the same way has the same backup-header gap. Firmware still boots. Windows still does not mount the installer ESP. The ISO is not a way to make the installer ESP writable from Explorer. The answer file has to live on a volume Windows already mounts.

Ventoy boots ISO files that it finds on its own FAT or exFAT data partition. It does not boot `feldcore-installer.raw.zst`. The data partition is the volume Explorer gives a drive letter.

## The ISO

One file, volume id `FELDCORE`, produced by `xorriso -as mkisofs`. It is both an ISO 9660 / Joliet filesystem and a GPT disk. El Torito and the GPT ESP point at the same FAT image, so a CD boot and a hard-disk boot load the same `EFI/BOOT/BOOTX64.EFI`.

### Tree

```
/feldcore/installer.marker          one line, the image version
/boot/vmlinuz                       live kernel (slim installer root)
/boot/initrd.img                    live initrd
/boot/grub/grub.cfg
/boot/grub/early.cfg                also embedded in the EFI binary
/EFI/BOOT/BOOTX64.EFI               GRUB x86_64-efi, inside the ISO and inside efiboot.img
/live/filesystem.squashfs           slim installer userspace
/payload/feldcore-root.img.zst      the appliance slot image, already built
/payload/vmlinuz
/payload/initrd.img
/payload/systemd-bootx64.efi
/payload/version
/payload/root-mib
/payload/state-mib
/System/Library/CoreServices/boot.efi          same bytes as BOOTX64.EFI
/System/Library/CoreServices/SystemVersion.plist
/mach_kernel                        empty file
```

`efiboot.img` is a 64 MiB FAT32 (label `FC-ISOEFI`, never `FC-ESP`) containing only `\EFI\BOOT\BOOTX64.EFI` and a copy of `early.cfg`. The kernel and the squashfs stay on the ISO 9660 tree. A 2012 Apple EFI loads `\EFI\BOOT\BOOTX64.EFI` from the ESP; it does not read ISO 9660 itself. GRUB does, once `early.cfg` has searched for the marker file.

The label must not be `FC-ESP`. The appliance ESP is created with `mkfs.vfat -n FC-ESP`, and `readAnswerFile` mounts `/dev/disk/by-label/FC-ESP`. Two volumes with that label make the symlink ambiguous while the installer stick is still plugged into a machine that already has an appliance disk.

### xorriso

BIOS El Torito is out of scope. `docs/OS-IMAGE.md` says the firmware is UEFI only, and the 2012 Mac mini boots the installer through Apple EFI. There is no `-b isolinux.bin`, no `--grub2-mbr`, and no i386-pc GRUB.

```
xorriso -as mkisofs \
  -iso-level 3 \
  -r -J -joliet-long \
  -V FELDCORE \
  -o "${work}/feldcore-installer.iso" \
  -partition_offset 16 \
  -append_partition 2 0xef "${work}/efiboot.img" \
  -appended_part_as_gpt \
  -appended_gpt_with_gaps \
  -e --interval:appended_partition_2:all:: \
  -no-emul-boot \
  -c /boot.catalog \
  -hfsplus \
  -hfsplus-file-creator-type chrp tbxj \
      /System/Library/CoreServices/boot.efi \
  -hfs-bless-by /System/Library/CoreServices/boot.efi \
  "${work}/iso"
```

What each flag is doing:

| Flag | Result |
| --- | --- |
| `-V FELDCORE` | ISO 9660 primary volume descriptor, 32 bytes at offset 40 of sector 16. The live initrd matches this, space-padded. |
| `-partition_offset 16` | First ISO partition starts at 2 KiB block 16 (512-byte LBA 64), not at LBA 0. The image is still mountable as ISO 9660 from byte 0. |
| `-append_partition 2 0xef` | Appends `efiboot.img`. In the GPT, type `0xef` becomes the EFI System Partition GUID `C12A7328-F81F-11D2-BA4B-00A0C93EC93B`. |
| `-appended_part_as_gpt` | Writes a real GPT and a protective MBR (one type `0xEE` entry, signature `0x55AA` at offset 510). xorriso's man page calls the GPT from `-isohybrid-gpt-basdat` "actually invalid". This flag is the one that writes a GPT `sgdisk -p` accepts. |
| `-appended_gpt_with_gaps` | With `-partition_offset 16`, the ISO 9660 range itself becomes a GPT partition instead of an unclaimed gap. |
| `-e --interval:appended_partition_2:all::` | El Torito no-emulation boot image is the appended ESP, not a second copy of the FAT image. |
| `-hfsplus` and `-hfs-bless-by` | Apple Partition Map / HFS+ view of the same files, with `boot.efi` blessed. Creator `chrp`, type `tbxj`, which is the pair GRUB 2.12's `grub-mkrescue` passes to xorriso. |

The first implementation run has to confirm that this xorriso accepts `-hfsplus` together with `-appended_part_as_gpt`. The combinations xorriso documents as impossible are `-hfsplus` with `-isohybrid-gpt-basdat` and `-hfsplus` with `-isohybrid-apm-hfsplus`, which this command does not use. If this command is rejected, drop the three `-hfs*` flags and ship the GPT plus El Torito image. The 2012 boot picker shows a FAT ESP named `EFI/BOOT/BOOTX64.EFI` as "EFI Boot" without HFS+. The HFS+ bless is what can make the same stick show a volume named from `SystemVersion.plist`. It is not a substitute for the ESP.

`SystemVersion.plist` is a small XML plist, `ProductName` `FELDCORE`, `ProductVersion` set to `FELDCORE_OS_VERSION`. `boot.efi` is the GRUB binary, not `systemd-bootx64.efi`. `/mach_kernel` is an empty file at the volume root. Apple firmware and the old `mactel-boot` layout look for that name on an HFS+ volume. Debian's `mactel-boot` package supplies `hfs-bless` and a sample plist for blessing an installed HFS+ volume from Linux. The ISO build does not depend on that package. `hfs-bless` matters only if the Mac mini checkpoint below shows that the installed internal disk is invisible to the boot picker.

Etcher's "does not appear to contain a partition table" warning is the inspector finding neither an MBR signature at offset 510 nor a GPT `"EFI PART"` header at LBA 1. A non-hybrid ISO (El Torito only, zeros in the system area) hits that warning, and Etcher then writes bytes that UEFI will not boot from a stick. The command above fills both. CI checks the three bytes and `sgdisk -p` on the ISO file. That is the Etcher condition. Etcher itself is a GUI and is not part of CI. The Mac mini session flashes once with Etcher.

Flashing this ISO to a larger stick still leaves the backup GPT at the last LBA of the image. Boot works. Explorer still does not mount the ESP. Nobody should document the flashed installer ESP as the place to put `feldcore-install.json`.

### GRUB, only on the installer medium

`systemd-boot` reads `loader/loader.conf` from the FAT volume it was loaded from. It does not read ISO 9660. Putting the 6 GiB root and the payload inside the 64 MiB El Torito image is not a layout. The installed disk keeps systemd-boot: `feldcore-install` already copies `payload/systemd-bootx64.efi` to `EFI/BOOT/BOOTX64.EFI` and `EFI/systemd/systemd-bootx64.efi` on the target.

The installer EFI binary is built on the runner, from `grub-efi-amd64-bin`, and is not installed into the appliance root:

```
grub-mkimage -O x86_64-efi \
  -o "${work}/BOOTX64.EFI" \
  -p /boot/grub \
  -c "${work}/iso/boot/grub/early.cfg" \
  normal search search_fs_file search_label \
  part_gpt part_msdos fat iso9660 loopback \
  linux configfile echo reboot
```

Upstream GRUB's command is `linux`, not `linuxefi` (`linuxefi` is a Fedora patch and is not in Debian's GRUB). The Debian amd64 kernel is an EFI PE stub, which `linux` loads.

`early.cfg` is embedded because Apple EFI's device path for a loaded `BOOTX64.EFI` is often only the FAT ESP, where `/boot/vmlinuz` does not exist:

```
search --no-floppy --file --set=root /feldcore/installer.marker
set prefix=($root)/boot/grub
configfile ($root)/boot/grub/grub.cfg
```

`grub.cfg`:

```
set timeout=3
set default=0
menuentry "FELDCORE installer" {
    linux /boot/vmlinuz boot=feldcore ro console=tty0 console=ttyS0,115200 feldcore.live=1 feldcore.version=VERSION
    initrd /boot/initrd.img
}
```

`/boot/grub/grub.cfg` is one of the paths Ventoy's GRUB2 mode searches (`/boot/grub/grub.cfg`, `/EFI/BOOT/grub.cfg`, and the other paths in Ventoy's GRUB2-mode doc). Ventoy's normal mode does not read that file. It builds an EFI Block I/O for the ISO from the El Torito catalog and starts `EFI/BOOT/BOOTX64.EFI`. Both modes have to work. The cmdline deliberately has no `feldcore.slot=` and no `root=LABEL=feldcore-a`. `feldcore-bless.service` exits immediately when `feldcore.slot` is absent (`os/image/usr/lib/feldcore/bless-boot`). `root=LABEL=feldcore-a` is only true on the raw installer disk's second partition. On a CD, a Ventoy loop, or a dd'd ISO, that label is not a block device the stock initramfs can wait for.

### Live userspace

The live root is a squashfs of a small Debian trixie, not a second copy of the appliance ext4. `installer_overlay` today `cp`s the full `feldcore-root.img` (default 6144 MiB, with Docker, the web UI, Samba, NFS, and the rest of `os/packages.txt`) and the payload partition holds that same root again as `feldcore-root.img.zst`. An ISO cannot be sparse. Shipping both copies puts the release asset against the 2 GiB GitHub limit on the first build. The live system only has to partition a disk, unpack the payload, and copy one JSON file.

Slim root, `debootstrap --variant=minbase trixie`, then:

```
systemd systemd-sysv udev linux-image-amd64
bash coreutils util-linux mount kmod
e2fsprogs dosfstools gdisk parted zstd jq
ca-certificates pciutils
firmware-linux-free firmware-misc-nonfree
initramfs-tools
```

`linux-image-amd64` is the same package the appliance installs, so the installer kernel can see the Mac mini's USB stack and its SATA disk (AHCI) and `tg3` ethernet (BCM57766 on the Late 2012 mini). `firmware-misc-nonfree` is what that ethernet firmware ships in. The payload root keeps the full `os/packages.txt` set, including `firmware-amd-graphics` and `firmware-intel-graphics`. Those two stay out of the live squashfs. The live root does not install `feldcored`, Docker, or `os/image`'s appliance units.

`os/live/` in the repository holds the files copied into this root before `update-initramfs -u`:

| Path in the slim root | Role |
| --- | --- |
| `/usr/share/initramfs-tools/scripts/feldcore` | `mountroot` for `boot=feldcore` |
| `/usr/share/initramfs-tools/hooks/feldcore` | `manual_add_modules squashfs isofs vfat exfat nls_cp437 nls_ascii nls_utf8 usb_storage uas loop` |
| `/usr/sbin/feldcore-install` | the existing script, plus the payload and answer-file changes below |
| `/usr/lib/systemd/system/feldcore-install.service` | unchanged unit, `feldcore-install --from-cmdline` |
| `/etc/systemd/system/getty@tty1.service.d/autologin.conf` | HDMI console on the Mac |
| `/etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf` | the drop-in `installer_overlay` writes today |
| `/etc/motd` | the text `installer_overlay` writes today |

The appliance initrd is left as `install_packages` built it. `apply_image` runs after that initrd exists and `kernel_files` copies it out. Nothing in this design runs `update-initramfs` in the appliance root, and `os/boot-test.sh` keeps patching `loader/entries/feldcore-a+3.conf` at byte offset 1048576 (the appliance ESP starts at 1 MiB).

Debian's initramfs-tools sources `/scripts/${BOOT}` and calls `mountroot`. `BOOT` comes from the kernel argument `boot=`, and the default is `local`. The ISO passes `boot=feldcore`, so the appliance cmdline (no `boot=`) still uses `scripts/local` even if a later rebuild of the appliance initrd picked the script up. The script:

1. Load the modules from the hook. Wait for udev.
2. Mount, read-only, the first block device that is ISO 9660 and contains `/feldcore/installer.marker`. Try `/dev/sr*`, then whole disks (`sd*`, `vd*`, `nvme*n*`, `mmcblk*`). Mounting the whole disk as ISO 9660 is the Etcher/dd case: the filesystem starts at byte 32768 regardless of the partition table.
3. Otherwise walk partitions whose `blkid` type is `vfat` or `exfat`. For each file matching `/feldcore-installer*.iso`, read 2048 bytes at offset 32768. Accept the file when bytes 1–5 are `CD001` and bytes 40–71 are `FELDCORE` padded with spaces. `losetup` that file and mount it. This is the Ventoy case. Ventoy's EFI Block I/O does not survive `ExitBootServices`, so the Linux initrd has to find the file on the real stick. NTFS is not scanned. Ventoy's default data filesystem is exFAT, and the Windows instructions say to format a side stick as FAT32 or exFAT.
4. Two matching ISOs is a hard stop, with both paths printed on the console. One match continues.
5. Mount `/live/filesystem.squashfs` from that medium onto `${rootmnt}`.
6. `mount --move` the medium onto `${rootmnt}/run/feldcore/medium` so `feldcore-install` can see `/run/feldcore/medium/payload` after `switch_root`.

`/payload` in the live root is a directory. `feldcore-install` grows a step in front of `mount LABEL=feldcore-payload`:

```
if [[ ! -f ${PAYLOAD}/feldcore-root.img.zst && -f /run/feldcore/medium/payload/feldcore-root.img.zst ]]; then
    mount --bind /run/feldcore/medium/payload "${PAYLOAD}"
fi
```

The raw installer has no `/run/feldcore/medium` and keeps the label mount. One script serves both images.

### Which disk is the installer

`booted_disk` in `feldcore-install` takes `findmnt /` and `lsblk PKNAME`. On the live ISO, `/` is a squashfs loop. PKNAME of that loop is empty, so the function would report `/dev/loop0` and would not refuse the USB stick the ISO was loaded from.

`install_disk` has to refuse a second device, the live medium:

- The mount source of `/run/feldcore/medium`, resolved to a whole disk.
- If that source is a loop, the disk that holds `backing_file` from `/sys/block/loopN/loop/backing_file` (the Ventoy stick).
- The disk `booted_disk` already returns, when it is a real disk.

The existing "refuses a disk with a mounted filesystem" check still applies to the target. The Ventoy partition and the answer-file stick are mounted during the search, so naming either of them as the target already fails that check. The live-medium check has to remain after those scan mounts are gone, because the payload bind is the one mount that must stay.

### Console on the Mac mini

`installer_overlay` autologins root on `ttyS0` only. The Late 2012 Mac mini has no serial port in normal use. The HDMI console is `tty1`. The appliance locks the root password (`passwd -l root` in `apply_image`). A live image that only autologins on `ttyS0` boots to a password prompt nobody can pass.

The slim image enables `getty@tty1` with `--autologin root` and keeps the `ttyS0` drop-in so `os/iso-test.sh` can read the motd from the QEMU serial log. The appliance image does not gain a tty1 autologin.

## 2012 Mac mini

The machine this has to boot is a Mac mini (Late 2012), Macmini6,1, 64-bit Apple EFI. The boot picker is the Option key held at power on. It is not PC Secure Boot, and this design does not add shim. Secure Boot stays off, as in `docs/OS-IMAGE.md`.

What the picker requires in order to list the stick as **EFI Boot**:

- A GPT partition of the EFI System type.
- `\EFI\BOOT\BOOTX64.EFI` on that FAT filesystem. The 8.3 name is exactly those characters. `mcopy` into a fresh `mkfs.vfat` stores that as the short name. Apple's firmware looks that path up; a binary that exists only as `\EFI\systemd\systemd-bootx64.efi` is not enough, which is why `fill_esp` and `feldcore-install` already write both names.
- The partition early enough for that firmware. The appended ESP sits after the ISO payload. Ubuntu's dd'd amd64 ISOs use the same "ESP at the end" layout and show up as EFI Boot on this generation of Mac. The Mac checkpoint is what confirms it for this image. If the stick is invisible, the fallback inside this same work is a small ESP image placed so its GPT entry starts under 4 GiB, with the ISO 9660 payload after it, still referenced by El Torito via `--interval`. That is a flag change, not a second bootloader.

HFS+ blessing (`System/Library/CoreServices/boot.efi`, `SystemVersion.plist`, empty `/mach_kernel`, `-hfs-bless-by`) is the extra that can show a named volume instead of the generic EFI Boot row. The installed-system writeups from this era (Ubuntu's UEFI-on-Mac notes, the `mactel-boot` / `hfs-bless` flow) are about the **internal disk after install**, and several of them say a FAT ESP alone was not the default startup disk until it was reformatted HFS+ and blessed. Those writeups also say the Option key still listed "EFI Boot" for a FAT ESP. v1 of `feldcore-install` keeps writing a FAT ESP with `EFI/BOOT/BOOTX64.EFI`, and also writes, on that same FAT:

- `/System/Library/CoreServices/boot.efi` — a copy of `systemd-bootx64.efi`
- `/System/Library/CoreServices/SystemVersion.plist`
- `/mach_kernel` — empty

Those files do not bless a FAT volume. `hfs-bless` only works on HFS+. Shipping the files means the Mac checkpoint can see whether the picker wanted them, and a follow-up can reformat the target ESP as HFS+ without inventing the file layout. Reformatting the appliance ESP as HFS+ is not part of v1: systemd-boot and `bless-boot` rewrite `loader/loader.conf` on a FAT filesystem mounted at `/efi`, and `fstab` names `LABEL=FC-ESP` with type implied by the FAT label mount. An HFS+ ESP would be a change to the appliance boot path, done only if the checkpoint says the internal disk does not appear.

USB 3 sticks sometimes never enumerate in this firmware's USB stack. The checkpoint retries a missing stick through a USB 2 port or a USB 2 hub before treating the image as bad. The image does not grow a USB 2 quirk.

## Answer file

Two JSON files are easy to mix up. This design changes only the first.

| File | Who reads it | What it decides |
| --- | --- | --- |
| `feldcore-install.json` | `(*setup.Service).ApplyInstallFile` in `internal/setup/answers.go`, called from `cmd/feldcored/main.go` after the setup writers are wired and before HTTP listens | Hostname, network, accounts, SSH, and the other sections in `docs/FELDCORE-INSTALL-ANSWER.md` |
| `answers.json` | Specified in `docs/FELDCORE-INSTALL-DATA-POOL.md`. `feldcore-install` does not read it today | Whether a future data-pool step formats a tail partition. The disk to wipe is still the `--yes` argument or `feldcore.install=` |

`answerName` is `feldcore-install.json`. `readAnswerFile` does, in order:

1. `{esp}/feldcore/feldcore-install.json`, where `esp` is `/efi` unless a test set `efiRoot`.
2. `{dataDir}/feldcore-install.json`, normally `/var/lib/feldcore/feldcore-install.json`. The ESP copy wins when both exist, because the state path is only tried when the ESP path is absent.
3. If `efiRoot` is empty and `/dev/disk/by-label/FC-ESP` exists, mount that device read-only on `/efi` and try step 1 again. A missing label is not an error.
4. A missing file returns `nil, nil, nil`. `main.go` then runs `EnsureCode`, which prints a random setup code. That is the current interactive first boot. The browser wizard is the UI. There is no installer TUI.

On success the daemon copies the bytes to the state directory mode `0600`, applies the sections, writes `feldcore-install.applied`, and deletes both copies. `removeAnswerPath` remounts `/efi` read-write when the ESP delete fails on a read-only mount. FAT does not scrub the clusters. The cleartext warning in `docs/FELDCORE-INSTALL-ANSWER.md` stays.

`parseAnswer` uses `DisallowUnknownFields`. `version` must be 1. A file that fails to parse is returned as an error and is not copied onto state, so a bad file left on the ESP is still there on the next boot.

`feldcore.service` has `RequiresMountsFor=/var/lib/feldcore`. The raw installer disk has no `feldcore-state` partition, so the daemon does not start on that image and cannot consume an answer file that was meant for the machine being installed. The slim ISO root does not ship `feldcored` at all. The live session must not apply the file. It copies it.

The `dataPool` field inside `feldcore-install.json` is a marker the daemon writes or removes on state. It does not call `sgdisk`. It is not `answers.json`.

### Options

**(a) A file on another FAT or exFAT volume.** This is the unattended path. The user writes `feldcore/feldcore-install.json` on a volume Explorer or macOS already mounts: the Ventoy data partition, next to the ISO, or a second small stick. The installer copies that file to the new ESP at `feldcore/feldcore-install.json`, which is the path step 1 above already reads. After the copy, no daemon change is required for the ISO install. The daemon still grows a fallback scan so a raw `feldcore-os.raw.zst` flash, which has the same Windows GPT problem and no installer copy step, can see a second stick.

**(b) An interactive installer when no file is present.** The installed appliance already does this: missing file, `EnsureCode`, setup code on the console, browser wizard. The ISO does not add a TUI or a second web wizard. The live image autologins on tty1 so the operator can type `feldcore-install --yes DISK`. Choosing the target disk stays that command, or `feldcore.install=` on the kernel command line for the raw installer and for a grub line someone builds on purpose. The data-pool scope already says the answer file must not select the disk. A 2012 Mac mini's internal SATA disk is not a stable `/dev/sdX` name once a USB stick is also present. `install_disk` keeps refusing the live medium.

**(c) The file body on the kernel command line.** The command line is world-readable in `/proc/cmdline` and would be stored in `grub.cfg` on the medium. Passwords, the WiFi PSK, and the setup code do not go there. An optional path override is reasonable and is a different argument from the one the data-pool scope reserved: `feldcore.install-json=/some/path` names a file. `feldcore.answers=` remains the name for a future `answers.json`. The override is not required for the Windows or Ventoy path.

**(a)** is the one to build. **(b)** is the behavior when the scan finds nothing. **(c)** is an optional path argument, never a document body.

### Where the installer looks

`feldcore-install` resolves the file before `sgdisk --zap-all` and stores the bytes under `/run/feldcore/feldcore-install.json` (tmpfs). Scan mounts can then be dropped. The zap of the wrong disk cannot be undone, so ambiguity fails before `sgdisk`.

Resolution order:

1. `--install-json PATH` if that flag is present. This is not `--answers`. `--answers` is the data-pool document's name for `answers.json`.
2. `feldcore.install-json=PATH` from `/proc/cmdline`, a path, when the flag was not passed.
3. A volume scan.

A candidate volume is `vfat` or `exfat`, is not the target disk, and is not the live medium from the section above. It must also be one of: sysfs `removable` is `1` for the parent disk, `ID_BUS=usb`, or the filesystem label is `FELDCORE-ANSWERS`. The label is what the QEMU test uses for a virtio disk, which reports `removable=0`. A real USB stick does not need the label.

On each candidate, in order, the relative paths are `feldcore/feldcore-install.json` and then `feldcore-install.json`. The first of those that exists is the candidate for that volume. ISO 9660 is not a candidate. The installer image does not contain an answer file, and a read-only tree is not a place the user can drop one.

`jq -e 'type == "object" and .version == 1'` accepts the candidate. `jq` is on the slim root for this check. The full schema stays in `parseAnswer` on first boot of the appliance. A file that exists and fails the `jq` check is an error, and the scan does not skip ahead to another volume. Two volumes with an accepted file are an error, and the message names both devices. Zero files is a normal install.

After the target ESP is mounted and the boot files are in place, and before `umount`, the script `mkdir -p feldcore` and writes the tmpfs copy to `feldcore/feldcore-install.json` on that ESP. That sits beside `feldcore/a/`, which is the slot directory `fill_esp` and `feldcore-install` already create. The daemon's path is `/efi/feldcore/feldcore-install.json` once that ESP is mounted at `/efi`.

The source file on the Ventoy partition or the second stick is left in place. Deleting it before the appliance has applied it throws away the only copy if the first boot fails validation. `removeAnswerCopies` on the appliance deletes the ESP copy it applied. The stick copy is the operator's to remove, which is the same cleartext warning as today.

### Daemon fallback

`readAnswerFile` grows a fourth step, after the FC-ESP mount attempt, using the same volume rules and the same two relative paths. The first accepted file is read and then takes the existing path: validate, write mode `0600` under the data directory, apply, delete.

Rules that keep the current tests honest:

- When `efiRoot != ""`, `readAnswerFile` returns after the two static paths, as it does now (`answers_test.go` sets `efiRoot` and would otherwise try to mount the host). The scan is behind that return.
- The scan is a function on `Service` with a default that walks sysfs. Tests replace it with a list of directories.
- A second accepted file is an error, same as the installer.
- `removeAnswerCopies` learns the source path it actually read. For a path under `/efi` it keeps the remount-rw behavior. For a side volume it tries to delete and logs a warning on failure. A failed delete does not fail the apply.

The ESP copy still wins, because steps 1–3 run first. An ISO install that copied the file onto the new ESP never needs the fallback. The fallback is the raw-appliance flash plus a second stick, and the case where the installer copy did not happen and the stick is still inserted at first boot.

`feldcored` on the live ISO is not a reader. The slim root has no daemon. The raw installer disk still has the daemon unit enabled (`apply_image` links `feldcore.service`) and still has `RequiresMountsFor=/var/lib/feldcore`, which is not mounted there.

## Build, CI, and the signature

`assemble_iso` runs after `assemble_installer` and before `build_sysexts`. It consumes files the script already produced: `feldcore-root.img.zst`, `vmlinuz`, `initrd.img`, `systemd-bootx64.efi`, and the three payload text files. It does its own `debootstrap` for the slim root, its own `update-initramfs`, `mksquashfs`, `grub-mkimage`, and the `xorriso` command. `FELDCORE_IMAGE_BUILDER=mkosi` keeps meaning "build the appliance rootfs with mkosi". It does not build the ISO. `os/mkosi/mkosi.conf` is `Format=directory` and `Bootable=no`.

`publish` copies `feldcore-installer.iso` into `FELDCORE_OS_OUT` (default `dist/os`) and signs it there.

Runner packages added to the "Install image tools" step of `.github/workflows/os-image.yml`, beside the existing `debootstrap gdisk dosfstools e2fsprogs mtools zstd qemu-utils rsync squashfs-tools`:

```
xorriso grub-efi-amd64-bin grub-common
```

Those three are build-host packages. They are not added to `os/packages.txt` or `os/mkosi/mkosi.conf`. The appliance does not contain GRUB or xorriso.

`require` in `os/build.sh` gains `xorriso` and `grub-mkimage`.

The upload-artifact step and the `softprops/action-gh-release` file list gain the ISO and its `.minisig`. The raw files stay on both lists.

### Tools that do not build this ISO

`xorriso` is the builder because the output has to be a real ISO 9660 tree (Ventoy and `-cdrom`), a valid GPT (Etcher, Apple EFI on a stick), and an HFS+ bless (the Mac picker). The script already shells out to `sgdisk`, `mcopy`, and `zstd`. One more command fits that shape.

mkosi v27 (August 2026) can stamp an El Torito catalog onto a `Format=disk` image through systemd-repart (`ElTorito=`). That catalog sits on a sized GPT disk. A sized GPT disk is the image that failed on Windows tonight, and it is not a file Ventoy treats as an ISO drop-in. The repository's mkosi config does not describe the ESP, the slots, or the payload; `os/build.sh` does. mkosi stays the optional rootfs builder.

Debian `live-build` would be a second package list and a second squashfs pipeline next to `debootstrap` and `assemble_installer`. The payload files, the minisign key, and the slot image would still have to be produced by `os/build.sh`. The live userspace here is small enough that a short `debootstrap` in `assemble_iso` is the whole of it.

`grub-mkrescue` wraps xorriso and adds an i386-pc BIOS El Torito image. This installer is UEFI only. Calling `grub-mkimage` and `xorriso` keeps the BIOS image out while using the same HFS+ creator/type pair `grub-mkrescue` uses.

### Signature

`feldcore-os.json` is the file `osmanifest sign` signs, and it is the file old appliances download. `ParseManifest` uses `DisallowUnknownFields`. `validate` accepts `files[].kind` of `root`, `kernel`, and `initrd` only and rejects anything else with "The release lists an unknown file." A new kind, or a new top-level field, makes an already-shipped daemon refuse the manifest and stop updating. The ISO is not added to `feldcore-os.json`.

The raw disks are not in the manifest today either. They are release assets gated by the same `FELDCORE_UPDATE_SIGNING_KEY` check as the manifest. The ISO gets its own detached signature so a user can check the file they flash, which the raw disk does not currently offer.

`internal/osupdate/minisign.go` `Sign` already writes a minisign `ED` signature (BLAKE2b-512, then ed25519) that `minisign -V` accepts. It takes the whole message as a byte slice. A multi-gigabyte ISO must not be loaded into one slice. `cmd/osmanifest` grows `sign-file`:

```
go run ./cmd/osmanifest sign-file -key "$FELDCORE_UPDATE_KEY" -in dist/os/feldcore-installer.iso
```

That streams the file through BLAKE2b and writes `feldcore-installer.iso.minisig` beside it, trusted comment naming the base name, same key as the manifest. `update.pub` verifies both. `publish` calls it next to the existing `osmanifest sign` of the manifest. The workflow step "Refuse a private key in the output" already fails the job if a `*.key` or `*.sec` lands in `dist/os`. The missing-secret path is unchanged: the artifact can still be uploaded, and the job fails before `action-gh-release` runs.

Verification for a user:

```
minisign -V -p update.pub -m feldcore-installer.iso
```

If the published asset is the zstd wrapper (next section), the signed bytes are the bytes of the asset they download. The `.minisig` name matches that asset. Decompress only after `minisign -V` succeeds.

### 2 GiB

GitHub rejects a release asset at or above 2 GiB. `feldcore-root.img.zst` is already a release asset, so the payload alone fits. The ISO is that payload plus the live squashfs, GRUB, and the 64 MiB ESP image. Squashfs and zstd do not shrink each other: wrapping the ISO in zstd will not save a payload that is already zstd plus a squashfs that is already compressed.

`assemble_iso` prints the ISO size. `publish` chooses the asset:

| Size of `feldcore-installer.iso` | What is published |
| --- | --- |
| Under 2 GiB | `feldcore-installer.iso` and `feldcore-installer.iso.minisig` |
| ISO at or over 2 GiB, and `feldcore-installer.iso.zst` under 2 GiB | the zstd asset and its `.minisig` |
| Both at or over 2 GiB | `publish` fails with both sizes. The job does not attach a truncated file |

Etcher already flashes `feldcore-installer.raw.zst` in the workflow we have, so a `.iso.zst` asset is flashable the same way. Ventoy does not decompress. The release text says to `zstd -d` and then copy the `.iso` onto the Ventoy partition.

Levers if the gate fails, in order, still inside this work: drop `firmware-misc-nonfree` down to the `tg3` firmware file only, raise the squashfs compression, and confirm the live initrd is not also storing a second uncompressed copy of modules that are in the squashfs. Removing the payload from the ISO and fetching it at install time is not a lever. The Mac mini install is offline.

## Raw images

`feldcore-os.raw.zst` stays. It is the appliance disk, the QEMU disk, and the direct `dd` image. The updater ignores it and keeps using the manifest's root, kernel, and initrd.

`feldcore-installer.raw.zst` stays on the release that introduces the ISO. `assemble_installer` keeps running so the ISO payload and the raw installer cannot drift into two different slot images. A later release deletes the raw installer from the workflow file list after the Mac mini checklist in this document has been filled in against a published ISO. Until that note exists, the raw installer is the fallback that already boots.

`README.md` currently says to flash `feldcore-os.raw.zst` and write `feldcore-install.json` onto the ESP from Windows, macOS, or Linux. That sentence is the workflow that failed. The implementation change rewrites it to: flash or Ventoy the ISO, put the JSON on the Ventoy partition or a second FAT/exFAT stick, boot, run `feldcore-install --yes` against the other whole disk. This design file is not that rewrite.

## Verification

### Every image build

`os/iso-test.sh`, run after `os/build.sh` in CI once the QEMU firmware is installed (the same `OVMF_CODE_4M.fd` as `os/qemu.sh`, not `OVMF_CODE_4M.ms.fd` and not the `.secboot` file). `os/boot-test.sh` still has to pass on `feldcore-os.raw`. The ISO work does not change that script's disk.

1. `xorriso -indev feldcore-installer.iso -report_el_torito` shows one EFI no-emulation image.
2. Bytes 510–511 of the ISO are `55 AA`. Bytes 512–519 are `EFI PART`. `sgdisk -p` on the ISO file shows a type EF00 partition.
3. Sector 16's volume id is `FELDCORE`.
4. The tree contains `/feldcore/installer.marker`, `/boot/grub/grub.cfg`, `/EFI/BOOT/BOOTX64.EFI`, `/live/filesystem.squashfs`, and `/payload/feldcore-root.img.zst`.
5. QEMU `-cdrom feldcore-installer.iso`, display none, serial file, exits when the serial log contains the installer motd (`FELDCORE installer.`). Timeout in the same spirit as `os/boot-test.sh` (that script uses 900 seconds for a full appliance smoke boot; the slim root should be well under that).
6. Copy the ISO onto a raw disk that is at least 1 GiB larger (`dd conv=notrunc`), boot that disk as a virtio hard disk, and require the same motd. Then `sgdisk -v` on the larger disk must report that the backup header is not at the last LBA. That is the Windows failure, preserved on purpose, and the boot has to succeed anyway.
7. Answer-file guest: the ISO as CD, an empty virtio disk as the target, a third virtio disk with a FAT32 filesystem labeled `FELDCORE-ANSWERS` and `feldcore/feldcore-install.json` on it. The serial console (or a small expect script) runs `feldcore-install --yes` on the empty disk. The test mounts the target's first partition and compares `feldcore/feldcore-install.json` to the source. The live root must not contain `/var/lib/feldcore/feldcore-install.applied`.
8. A second answer-file case with two FAT disks, each with a valid file, must exit non-zero before the target's GPT changes (`sgdisk -p` on the target stays empty).

`os/ventoy-test.sh` is the Ventoy leg. Pin a Ventoy release tarball in the script (version and sha256). Create a raw disk with Ventoy's Linux installer, non-interactive. Copy `feldcore-installer.iso` to the data partition and write `/ventoy/ventoy.json` so the menu does not wait for a key:

```json
{
  "control": [
    { "VTOY_DEFAULT_IMAGE": "/feldcore-installer.iso" },
    { "VTOY_MENU_TIMEOUT": "5" }
  ]
}
```

Boot that disk under the same OVMF firmware and require the installer motd. Leave `VTOY_DEFAULT_SEARCH_ROOT` unset unless the ISO is placed under that directory: Ventoy ignores `VTOY_DEFAULT_IMAGE` when the path sits outside the search root. Repeat with the file named `feldcore-installer_VTGRUB2.iso` (Ventoy treats the `_VTGRUB2` suffix as GRUB2 mode, which sources `/boot/grub/grub.cfg`) and the JSON `VTOY_DEFAULT_IMAGE` updated to that name. Both modes have to reach the motd. This test downloads Ventoy, so it is allowed to be a separate workflow step from the image build, and a failure there fails the job.

### On the real Mac mini, once per published ISO

CI cannot see the Option-key picker or a USB 3 firmware bug. These four are the gate for dropping `feldcore-installer.raw.zst` later. They are not required to merge the implementation, and a tag may publish the ISO before they are done. Record the result in this file's checklist when they are done, the same way `docs/OS-IMAGE.md` records the QEMU smoke boot.

1. Etcher-flash the published asset to a USB stick. Option-boot the Mac mini. The stick is listed as EFI Boot, or under the `SystemVersion.plist` product name. It reaches the tty1 motd and a root shell. If the stick never appears, repeat on a USB 2 hub before changing the image. Note the stick.
2. From Windows or macOS, copy the `.iso` onto a Ventoy stick (decompress first when the asset is `.iso.zst`) and write `feldcore/feldcore-install.json` on that same partition. Option-boot, choose the ISO, reach the installer. This is the check that the answer file did not require mounting the installer ESP.
3. Run `feldcore-install --yes` on the internal whole disk. Power off, remove the USB, Option-boot. The internal disk is listed and the appliance answers on port 8787. If the internal disk is absent from the picker, stop and do the HFS+ ESP follow-up. Do not bless the disk by hand and call the FAT layout done.
4. With the JSON from step 2, the booted appliance's hostname (or SSH, when the file enabled it) matches the file, and `/var/lib/feldcore/feldcore-install.applied` exists. The source file is still on the Ventoy partition unless someone deleted it.

## Milestones

One implementation sequence, three milestones. The appliance partition table, the signed manifest's `files` array, and `os/boot-test.sh` stay put.

**M1. The ISO boots in QEMU.** `os/live/`, `assemble_iso`, the xorriso command, `grub-mkimage`, the slim debootstrap, tty1 autologin, `osmanifest sign-file` streaming, the workflow package list and artifact list, and `os/iso-test.sh` checks 1–6. `assemble_installer` still publishes the raw installer. `os/boot-test.sh` passes.

**M2. The answer file survives a read-only medium.** The pre-zap resolver and the ESP copy in `feldcore-install`, the live-medium refuse list, the `/run/feldcore/medium/payload` bind, the fourth step in `readAnswerFile` with tests that do not mount the host, and `os/iso-test.sh` checks 7 and 8. `docs/FELDCORE-INSTALL-ANSWER.md`, the installer section of `docs/OS-IMAGE.md`, and the USB section of `README.md` change in this milestone so they describe the scan.

**M3. Ventoy and the Mac mini.** `os/ventoy-test.sh` for normal mode and `_VTGRUB2`. A person with the Late 2012 Mac mini runs the four-step checkpoint and writes the result into this file. Dropping `feldcore-installer.raw.zst` is a later release, after that write-up, not part of M3's code.

M1 is a new function beside `assemble_installer` and a small initramfs script. M2 is the two readers (`feldcore-install` and `readAnswerFile`) plus tests. M3 is a QEMU harness that downloads a pinned Ventoy, and one session at the Mac. The risky checks are the xorriso flag combination, the 2 GiB asset gate, and whether the Mac's boot picker lists the internal disk after install.

## What this does not do

- Secure Boot, shim, and a signed kernel.
- A BIOS CSM El Torito image.
- Putting the ISO into `feldcore-os.json`, or any other manifest field an old daemon would reject.
- Implementing `answers.json`, `claim-data`, or the data-pool partition from `docs/FELDCORE-INSTALL-DATA-POOL.md`.
- An installer TUI or a second wizard. No answer file means the setup code and the existing browser wizard, after the operator has named a target disk.
- Reformatting the appliance ESP as HFS+, unless the Mac checkpoint says the internal disk does not appear in the picker.
- Deleting `feldcore-installer.raw.zst` in the same release that first ships the ISO.
