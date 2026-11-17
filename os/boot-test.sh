#!/bin/bash
# Boot a built appliance disk, with feldcore.smoke=1, and wait until the
# guest has started nginx and the UI.
set -euo pipefail

work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}
src=${1:-${work}/feldcore-os.raw}
disk=${work}/feldcore-os-smoke.raw
code=${FELDCORE_OVMF_CODE:-/usr/share/OVMF/OVMF_CODE_4M.fd}
vars_src=${FELDCORE_OVMF_VARS:-/usr/share/OVMF/OVMF_VARS_4M.fd}
vars=${work}/ovmf-vars-smoke.fd
serial=${work}/serial-smoke.log
offset=1048576

[[ -f ${src} ]] || { echo "boot-test: missing ${src}" >&2; exit 1; }
[[ -f ${code} && -f ${vars_src} ]] || { echo "boot-test: OVMF firmware was not found" >&2; exit 1; }

rm -f "${disk}"
cp --sparse=always "${src}" "${disk}"
tmp=$(mktemp)
mcopy -n -i "${disk}@@${offset}" ::/loader/entries/feldcore-a+3.conf "${tmp}"
if ! grep -q 'feldcore.smoke=1' "${tmp}"; then
	sed -i 's/^options /options feldcore.smoke=1 /' "${tmp}"
fi
mcopy -o -i "${disk}@@${offset}" "${tmp}" ::/loader/entries/feldcore-a+3.conf
rm -f "${tmp}"

cp -f "${vars_src}" "${vars}"
: >"${serial}"

accel=kvm
cpu=host
if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
	accel=tcg
	cpu=qemu64
fi

qemu-system-x86_64 \
	-machine "q35,accel=${accel}" \
	-cpu "${cpu}" \
	-m 3072 -smp 2 \
	-drive "if=pflash,format=raw,readonly=on,file=${code}" \
	-drive "if=pflash,format=raw,file=${vars}" \
	-drive "if=none,id=disk,format=raw,file=${disk}" \
	-device virtio-blk-pci,drive=disk \
	-nic user,model=virtio-net-pci,hostfwd=tcp::8787-:8787,hostfwd=tcp::8088-:8088 \
	-serial "file:${serial}" \
	-display none \
	-no-reboot &
qpid=$!

cleanup() {
	if kill -0 "${qpid}" 2>/dev/null; then
		kill "${qpid}" 2>/dev/null || true
		wait "${qpid}" 2>/dev/null || true
	fi
}
trap cleanup EXIT

deadline=$((SECONDS + 900))
while ((SECONDS < deadline)); do
	if grep -q 'FELDCORE_SMOKE_OK' "${serial}" 2>/dev/null; then
		echo "boot-test: guest smoke passed"
		# nginx is published on the guest's loopback, so the host checks the UI.
		# QEMU's user network sometimes resets the first connection.
		ok=0
		for _ in $(seq 1 20); do
			if curl -fsS --max-time 3 http://127.0.0.1:8787/ >/dev/null; then
				ok=1
				break
			fi
			sleep 1
		done
		[[ ${ok} == 1 ]] || { echo "boot-test: host could not reach :8787" >&2; exit 1; }
		echo "boot-test: host reached :8787"
		exit 0
	fi
	if ! kill -0 "${qpid}" 2>/dev/null; then
		echo "boot-test: qemu exited before the smoke marker" >&2
		tail -n 80 "${serial}" >&2 || true
		exit 1
	fi
	sleep 5
done
echo "boot-test: timed out" >&2
tail -n 120 "${serial}" >&2 || true
exit 1
