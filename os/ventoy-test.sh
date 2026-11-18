#!/bin/bash
# Boot feldcore-installer.iso from a Ventoy disk, in normal mode and GRUB2 mode.
# Ventoy 1.1.18 is pinned. Secure Boot support in Ventoy is turned off (-S)
# because this test uses the same non-Secure-Boot OVMF file as os/qemu.sh.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}
iso=${FELDCORE_INSTALLER_ISO:-${work}/feldcore-installer.iso}
ventoy_version=1.1.18
ventoy_sha256=d86ff9de63d94c8b8f1f6b14d23c55af4b88e8207bf2893890892a6a97b1ad54
ventoy_url="https://github.com/ventoy/Ventoy/releases/download/v${ventoy_version}/ventoy-${ventoy_version}-linux.tar.gz"
# shellcheck source=os/iso-qemu.sh
source "${repo}/os/iso-qemu.sh"
iso_die() {
	printf 'ventoy-test: %s\n' "$*" >&2
	exit 1
}
trap iso_stop_qemu EXIT

loop=""
mnt=""
partx_pid=""

cleanup_disk() {
	iso_stop_qemu
	if [[ -n ${partx_pid} ]]; then
		kill "${partx_pid}" 2>/dev/null || true
		wait "${partx_pid}" 2>/dev/null || true
		partx_pid=""
	fi
	if [[ -n ${mnt} && -d ${mnt} ]]; then
		sudo umount "${mnt}" 2>/dev/null || true
	fi
	if [[ -n ${loop} ]]; then
		sudo losetup -d "${loop}" 2>/dev/null || true
	fi
}
trap cleanup_disk EXIT

[[ -f ${iso} ]] || iso_die "missing ${iso}"
command -v qemu-system-x86_64 >/dev/null || iso_die "missing qemu"
iso_qemu_init
mkdir -p "${work}"

tarball=${work}/ventoy-${ventoy_version}-linux.tar.gz
if [[ ! -f ${tarball} ]]; then
	curl -fL --retry 3 -o "${tarball}.partial" "${ventoy_url}"
	mv "${tarball}.partial" "${tarball}"
fi
echo "${ventoy_sha256}  ${tarball}" | sha256sum -c -
extract=${work}/ventoy-src
rm -rf "${extract}"
mkdir -p "${extract}"
tar -xzf "${tarball}" -C "${extract}"
src=${extract}/ventoy-${ventoy_version}
[[ -f ${src}/Ventoy2Disk.sh ]] || iso_die "Ventoy archive did not contain Ventoy2Disk.sh"

iso_bytes=$(stat -c %s "${iso}")
disk_bytes=$((iso_bytes + 1536 * 1024 * 1024))
disk=${work}/ventoy-disk.raw
rm -f "${disk}"
truncate -s "${disk_bytes}" "${disk}"
loop=$(sudo losetup -fP --show "${disk}")
# This kernel leaves loop partition nodes unpublished until partx runs, and
# max_part is 0 so Ventoy cannot mknod them from sysfs. Publish them while
# Ventoy is waiting for /dev/loopNp1.
partx_pid=""
(
	for _ in $(seq 1 240); do
		sudo partx -u "${loop}" >/dev/null 2>&1 || true
		sleep 0.5
	done
) &
partx_pid=$!
echo "ventoy-test: installing Ventoy ${ventoy_version} on ${loop}"
(
	cd "${src}"
	printf 'y\ny\n' | sudo timeout 180 sh ./Ventoy2Disk.sh -I -g -S "${loop}"
)
kill "${partx_pid}" 2>/dev/null || true
wait "${partx_pid}" 2>/dev/null || true
partx_pid=""
sudo partx -u "${loop}" 2>/dev/null || sudo partprobe "${loop}" 2>/dev/null || true
sleep 1
data=""
for part in "${loop}"p*; do
	[[ -b ${part} ]] || continue
	if sudo blkid -o export "${part}" | grep -q '^TYPE=exfat$'; then
		data=${part}
		break
	fi
done
[[ -n ${data} ]] || iso_die "Ventoy did not create an exFAT data partition"
mnt=${work}/ventoy-mnt
sudo mkdir -p "${mnt}"
if ! sudo mount -t exfat "${data}" "${mnt}" 2>/dev/null; then
	sudo mount.exfat-fuse "${data}" "${mnt}"
fi

write_menu() {
	local image_name=$1
	sudo mkdir -p "${mnt}/ventoy"
	sudo tee "${mnt}/ventoy/ventoy.json" >/dev/null <<EOF
{
  "control": [
    { "VTOY_DEFAULT_IMAGE": "/${image_name}" },
    { "VTOY_MENU_TIMEOUT": "5" }
  ]
}
EOF
}

copy_iso() {
	local image_name=$1
	sudo find "${mnt}" -maxdepth 1 -name 'feldcore-installer*.iso' -delete
	sudo cp "${iso}" "${mnt}/${image_name}"
	write_menu "${image_name}"
	sudo sync
}

boot_disk() {
	local label=$1
	sudo umount "${mnt}"
	sudo losetup -d "${loop}"
	loop=""
	echo "ventoy-test: ${label}"
	iso_key_sock=${work}/ventoy-monitor.sock
	iso_key_mode=""
	if [[ ${label} == grub2 ]]; then
		iso_key_mode=grub2
	fi
	iso_boot_motd "${work}/ventoy-${label}.log" 600 \
		-drive "if=none,id=disk,format=raw,file=${disk}" \
		-device virtio-blk-pci,drive=disk,bootindex=0
	iso_key_sock=""
	iso_key_mode=""
	# Ventoy 1.1.18 injects rdinit=/vtoy/vtoy and then configfiles the ISO's
	# own grub.cfg in both modes that reach the installer. Normal mode first
	# tries to chainload its EFI helper; that helper rejects this hybrid ISO
	# ("cannot load image") and Ventoy falls through to the same configfile
	# path. GRUB2 mode skips the chain. The two logs have to show that split,
	# or the timeout menu was still on "Boot in normal mode" for both boots.
	# quiet loglevel=3 drops the kernel command line on every console, so
	# rdinit= is not in this capture even when Ventoy injected it. The
	# installer initrd writes "feldcore: live root mounted" to /dev/console
	# after the squashfs mounts. ttyS0 is the last console=, so that line
	# is in the serial file, and it is absent when boot never reaches the
	# live root.
	if [[ ${label} == normal ]]; then
		grep -q 'cannot load image' "${work}/ventoy-${label}.log" \
			|| iso_die "normal mode did not attempt the Ventoy EFI chain"
	elif grep -q 'cannot load image' "${work}/ventoy-${label}.log"; then
		iso_die "grub2 mode took the normal-mode chain path"
	fi
	grep -q 'feldcore: live root mounted' "${work}/ventoy-${label}.log" \
		|| iso_die "${label} mode did not reach the installer. serial log:"$'\n'"$(tail -n 80 "${work}/ventoy-${label}.log" 2>/dev/null || true)"
	echo "ventoy-test: ${label} reached the installer"
	loop=$(sudo losetup -fP --show "${disk}")
	sudo partx -u "${loop}" 2>/dev/null || true
	sleep 0.5
	data=""
	for part in "${loop}"p*; do
		[[ -b ${part} ]] || continue
		if sudo blkid -o export "${part}" | grep -q '^TYPE=exfat$'; then
			data=${part}
			break
		fi
	done
	[[ -n ${data} ]] || iso_die "exFAT partition disappeared"
	if ! sudo mount -t exfat "${data}" "${mnt}" 2>/dev/null; then
		sudo mount.exfat-fuse "${data}" "${mnt}"
	fi
}

copy_iso feldcore-installer.iso
boot_disk normal
copy_iso feldcore-installer_VTGRUB2.iso
boot_disk grub2
sudo umount "${mnt}"
sudo losetup -d "${loop}"
loop=""
mnt=""
echo "ventoy-test: passed"
