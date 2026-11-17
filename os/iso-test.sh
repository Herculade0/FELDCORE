#!/bin/bash
# Boot the installer ISO under OVMF and check the answer-file rules.
# The firmware is OVMF_CODE_4M.fd, not the Secure Boot builds.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}
iso=${FELDCORE_INSTALLER_ISO:-${work}/feldcore-installer.iso}
# shellcheck source=os/iso-qemu.sh
source "${repo}/os/iso-qemu.sh"
trap iso_stop_qemu EXIT

answer_self_test() {
	local script=${repo}/os/image/usr/sbin/feldcore-install
	bash -c '
		set -euo pipefail
		source "$1"
		tmp=$(mktemp -d)
		trap "rm -rf ${tmp}" EXIT
		printf "%s\n" "{\"version\":1,\"hostname\":\"a\"}" > "${tmp}/ok.json"
		printf "%s\n" "{\"version\":1.0}" > "${tmp}/float.json"
		printf "%s\n" "{\"version\":2}" > "${tmp}/bad.json"
		printf "%s\n" "[1]" > "${tmp}/arr.json"
		printf "%s\n" "{\"version\":1} true" > "${tmp}/trail.json"
		printf "%s\n" "{\"version\":01}" > "${tmp}/zero.json"
		answer_acceptable "${tmp}/ok.json"
		answer_acceptable "${tmp}/float.json"
		if answer_acceptable "${tmp}/bad.json"; then exit 1; fi
		if answer_acceptable "${tmp}/arr.json"; then exit 1; fi
		answer_acceptable_awk "${tmp}/ok.json"
		answer_acceptable_awk "${tmp}/float.json"
		if answer_acceptable_awk "${tmp}/bad.json"; then exit 1; fi
		if answer_acceptable_awk "${tmp}/arr.json"; then exit 1; fi
		if answer_acceptable_awk "${tmp}/trail.json"; then exit 1; fi
		if answer_acceptable_awk "${tmp}/zero.json"; then exit 1; fi
	' bash "${script}"
}

require_tree() {
	local listing p
	listing=$(xorriso -indev "${iso}" -find / 2>/dev/null || true)
	for p in \
		/feldcore/installer.marker \
		/boot/grub/grub.cfg \
		/boot/grub/early.cfg \
		/EFI/BOOT/BOOTX64.EFI \
		/live/filesystem.squashfs \
		/payload/feldcore-root.img.zst \
		/System/Library/CoreServices/boot.efi \
		/System/Library/CoreServices/SystemVersion.plist \
		/mach_kernel
	do
		printf '%s\n' "${listing}" | grep -F -q "${p}" || iso_die "ISO tree is missing ${p}"$'\n'"${listing}"
	done
	local tmp
	tmp=$(mktemp -d)
	xorriso -osirrox on -indev "${iso}" -extract /boot/grub/grub.cfg "${tmp}/grub.cfg" >/dev/null
	xorriso -osirrox on -indev "${iso}" -extract /feldcore/installer.marker "${tmp}/marker" >/dev/null
	if grep -q 'feldcore.slot=' "${tmp}/grub.cfg" || grep -q 'root=LABEL=' "${tmp}/grub.cfg"; then
		iso_die "grub.cfg still has an appliance root"
	fi
	grep -q 'boot=feldcore' "${tmp}/grub.cfg" || iso_die "grub.cfg does not set boot=feldcore"
	[[ $(wc -l <"${tmp}/marker") -eq 1 ]] || iso_die "installer.marker is not one line"
	rm -rf "${tmp}"
}

byte_checks() {
	local sig hdr got want gpt report count
	sig=$(dd if="${iso}" bs=1 skip=510 count=2 status=none | od -An -tx1 | tr -d ' \n')
	[[ ${sig} == 55aa ]] || iso_die "MBR signature is ${sig}"
	hdr=$(dd if="${iso}" bs=1 skip=512 count=8 status=none)
	[[ ${hdr} == "EFI PART" ]] || iso_die "GPT header is not EFI PART"
	got=$(dd if="${iso}" bs=1 skip=$((32768 + 40)) count=32 status=none)
	want=$(printf '%-32s' FELDCORE)
	[[ ${got} == "${want}" ]] || iso_die "volume id is not FELDCORE"
	gpt=$(sgdisk -p "${iso}" 2>&1 || true)
	printf '%s\n' "${gpt}" | grep -q EF00 || iso_die "GPT has no EF00 partition"
	if printf '%s\n' "${gpt}" | grep -q 'FC-ESP'; then
		iso_die "ISO GPT contains FC-ESP"
	fi
	report=$(xorriso -indev "${iso}" -report_el_torito plain 2>&1 || true)
	count=$(printf '%s\n' "${report}" | grep -c 'UEFI.*none' || true)
	[[ ${count} -eq 1 ]] || iso_die "expected one UEFI no-emulation El Torito image, saw ${count}"$'\n'"${report}"
	if [[ -f ${work}/efiboot.img ]]; then
		local label
		label=$(mlabel -i "${work}/efiboot.img" -s 2>&1 || true)
		printf '%s\n' "${label}" | grep -q 'FC-ISOEFI' || iso_die "efiboot label is not FC-ISOEFI (${label})"
		if printf '%s\n' "${label}" | grep -q 'FC-ESP'; then
			iso_die "efiboot label must not be FC-ESP"
		fi
	fi
}

make_answer_disk() {
	local disk=$1 json=$2
	rm -f "${disk}"
	truncate -s 32M "${disk}"
	# FC-ANSWERS is 10 characters. FAT rejects the longer design label.
	mkfs.vfat -F 32 -n FC-ANSWERS "${disk}" >/dev/null
	mmd -i "${disk}" ::/feldcore
	mcopy -i "${disk}" "${json}" ::/feldcore/feldcore-install.json
}

answer_self_test
echo "iso-test: answer-file checker passed"
[[ -f ${iso} ]] || iso_die "missing ${iso}"
command -v xorriso >/dev/null || iso_die "missing xorriso"
command -v sgdisk >/dev/null || iso_die "missing sgdisk"
command -v qemu-system-x86_64 >/dev/null || iso_die "missing qemu"
iso_qemu_init

echo "iso-test: image layout"
byte_checks
require_tree
if [[ -f ${work}/feldcore-installer.iso.layout ]]; then
	echo "iso-test: xorriso layout $(cat "${work}/feldcore-installer.iso.layout")"
fi

echo "iso-test: CD boot"
iso_boot_motd "${work}/iso-test-cd.log" 600 -cdrom "${iso}"
echo "iso-test: CD boot reached the motd"

echo "iso-test: dd onto a larger disk"
usb=${work}/iso-test-usb.raw
bytes=$(stat -c %s "${iso}")
rm -f "${usb}"
truncate -s $((bytes + 1024 * 1024 * 1024)) "${usb}"
dd if="${iso}" of="${usb}" conv=notrunc bs=4M status=none
iso_boot_motd "${work}/iso-test-usb.log" 600 \
	-drive "if=none,id=disk,format=raw,file=${usb}" \
	-device virtio-blk-pci,drive=disk,bootindex=0
verify=${work}/iso-test-sgdisk.txt
sgdisk -v "${usb}" >"${verify}" 2>&1 || true
if ! grep -E -q "doesn't reside|does not reside|end of the disk" "${verify}"; then
	iso_die "sgdisk -v did not report a backup header away from the end of the disk"$'\n'"$(cat "${verify}")"
fi
echo "iso-test: larger disk booted and the backup header stays in the middle"

tmp=$(mktemp -d)
xorriso -osirrox on -indev "${iso}" \
	-extract /payload/root-mib "${tmp}/root-mib" \
	-extract /payload/state-mib "${tmp}/state-mib" >/dev/null
root_mib=$(tr -d ' \n' <"${tmp}/root-mib")
state_mib=$(tr -d ' \n' <"${tmp}/state-mib")
part_mib=$((root_mib + 128))
target_mib=$((16 + 256 + part_mib + part_mib + state_mib + 32))
printf '%s\n' '{"version":1,"hostname":"iso-test","timezone":"UTC"}' >"${tmp}/answer.json"
make_answer_disk "${work}/iso-test-answers.raw" "${tmp}/answer.json"
target=${work}/iso-test-target.raw
rm -f "${target}"
truncate -s "${target_mib}M" "${target}"

# The guest shell expands this line. The host must not. The marker is
# assembled in the guest so the echoed command is not mistaken for the result.
# shellcheck disable=SC2016
install_cmd='d=$(lsblk -dno PATH,SERIAL | while read -r path serial; do if [ "$serial" = target ]; then echo "$path"; break; fi; done); [ -n "$d" ] || d=$(readlink -f /dev/disk/by-id/virtio-target 2>/dev/null || true); tag=FELDCORE_; if [ -z "$d" ]; then echo "${tag}NO_TARGET"; lsblk -dno PATH,SERIAL,LABEL; echo "${tag}INSTALL_DONE"; poweroff -f; exit 1; fi; findmnt -n /run/feldcore/medium >/dev/null || echo "${tag}MEDIUM_MISSING"; feldcore-install --yes "$d"; rc=$?; echo "${tag}INSTALL_EXIT:${rc}"; if [ ! -e /var/lib/feldcore/feldcore-install.applied ]; then echo "${tag}ANSWER_APPLIED_ABSENT"; fi; sync; echo "${tag}INSTALL_DONE"; sleep 1; poweroff -f'

echo "iso-test: answer file copied onto the new ESP"
iso_boot_command "${work}/iso-test-answer.log" 900 "${install_cmd}" \
	-drive "if=none,id=cdrom,media=cdrom,readonly=on,file=${iso}" \
	-device virtio-scsi-pci,id=scsi \
	-device scsi-cd,drive=cdrom,bootindex=0 \
	-drive "if=none,id=target,format=raw,file=${target}" \
	-device virtio-blk-pci,drive=target,serial=target \
	-drive "if=none,id=ans,format=raw,file=${work}/iso-test-answers.raw" \
	-device virtio-blk-pci,drive=ans,serial=answers
grep -q 'FELDCORE_INSTALL_EXIT:0' "${work}/iso-test-answer.log" || iso_die "install did not exit 0"
grep -q 'FELDCORE_ANSWER_APPLIED_ABSENT' "${work}/iso-test-answer.log" || iso_die "the live root applied the answer file"
if grep -q 'FELDCORE_MEDIUM_MISSING' "${work}/iso-test-answer.log"; then
	iso_die "the live medium was not mounted at /run/feldcore/medium"
fi
if grep -q 'FELDCORE_NO_TARGET' "${work}/iso-test-answer.log"; then
	iso_die "the guest could not see the target disk"
fi
copied=${tmp}/copied.json
mcopy -n -i "${target}@@1048576" ::/feldcore/feldcore-install.json "${copied}"
cmp -s "${tmp}/answer.json" "${copied}" || iso_die "ESP answer file does not match the source"
echo "iso-test: answer file on the new ESP matches"

echo "iso-test: two answer files are refused"
printf '%s\n' '{"version":1,"hostname":"one","timezone":"UTC"}' >"${tmp}/one.json"
printf '%s\n' '{"version":1,"hostname":"two","timezone":"UTC"}' >"${tmp}/two.json"
make_answer_disk "${work}/iso-test-answers-a.raw" "${tmp}/one.json"
make_answer_disk "${work}/iso-test-answers-b.raw" "${tmp}/two.json"
blank=${work}/iso-test-blank.raw
rm -f "${blank}"
truncate -s 64M "${blank}"
before=$(sha256sum "${blank}" | awk '{print $1}')
# shellcheck disable=SC2016
refuse_cmd='d=$(lsblk -dno PATH,SERIAL | while read -r path serial; do if [ "$serial" = target ]; then echo "$path"; break; fi; done); [ -n "$d" ] || d=$(readlink -f /dev/disk/by-id/virtio-target 2>/dev/null || true); tag=FELDCORE_; if [ -z "$d" ]; then echo "${tag}NO_TARGET"; lsblk -dno PATH,SERIAL,LABEL; echo "${tag}INSTALL_DONE"; poweroff -f; exit 1; fi; feldcore-install --yes "$d"; rc=$?; echo "${tag}INSTALL_EXIT:${rc}"; sync; echo "${tag}INSTALL_DONE"; sleep 1; poweroff -f'
iso_boot_command "${work}/iso-test-two.log" 900 "${refuse_cmd}" \
	-drive "if=none,id=cdrom,media=cdrom,readonly=on,file=${iso}" \
	-device virtio-scsi-pci,id=scsi \
	-device scsi-cd,drive=cdrom,bootindex=0 \
	-drive "if=none,id=target,format=raw,file=${blank}" \
	-device virtio-blk-pci,drive=target,serial=target \
	-drive "if=none,id=a,format=raw,file=${work}/iso-test-answers-a.raw" \
	-device virtio-blk-pci,drive=a,serial=answersa \
	-drive "if=none,id=b,format=raw,file=${work}/iso-test-answers-b.raw" \
	-device virtio-blk-pci,drive=b,serial=answersb
if grep -q 'FELDCORE_INSTALL_EXIT:0' "${work}/iso-test-two.log"; then
	iso_die "two answer files were accepted"
fi
grep -q 'more than one install answer file' "${work}/iso-test-two.log" || iso_die "the two-file error was not reported"
after=$(sha256sum "${blank}" | awk '{print $1}')
[[ ${before} == "${after}" ]] || iso_die "the target disk changed after a refused install"
gpt=$(sgdisk -p "${blank}" 2>&1 || true)
if printf '%s\n' "${gpt}" | grep -E -q 'FC-ESP|feldcore-a'; then
	iso_die "the refused install wrote a partition table"
fi
rm -rf "${tmp}"
echo "iso-test: passed"
