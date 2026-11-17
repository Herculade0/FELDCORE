#!/bin/bash
# Boot a built FELDCORE OS disk in QEMU with UEFI firmware and no Secure Boot.
# Secure Boot firmware (OVMF_CODE_4M.ms.fd / .secboot.fd) is intentionally not used.
set -euo pipefail

work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}
disk=${1:-${work}/feldcore-os.raw}
code=${FELDCORE_OVMF_CODE:-/usr/share/OVMF/OVMF_CODE_4M.fd}
vars_src=${FELDCORE_OVMF_VARS:-/usr/share/OVMF/OVMF_VARS_4M.fd}
vars=${work}/ovmf-vars.fd
serial=${work}/serial.log
mkdir -p "${work}"
cp -f "${vars_src}" "${vars}"
: >"${serial}"

accel=kvm
if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
	accel=tcg
fi

exec qemu-system-x86_64 \
	-machine "q35,accel=${accel}" \
	-cpu host \
	-m 2048 -smp 2 \
	-drive "if=pflash,format=raw,readonly=on,file=${code}" \
	-drive "if=pflash,format=raw,file=${vars}" \
	-drive "if=none,id=disk,format=raw,file=${disk}" \
	-device virtio-blk-pci,drive=disk \
	-nic user,model=virtio-net-pci,hostfwd=tcp::8787-:8787 \
	-serial "file:${serial}" \
	-display none \
	-no-reboot
