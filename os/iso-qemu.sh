#!/bin/bash
# QEMU helpers for the installer ISO tests. Sourced, not executed.
# repo is set by the test that sources this file.
# shellcheck disable=SC2154
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
	echo "os/iso-qemu.sh is sourced by the ISO tests" >&2
	exit 1
fi

iso_qemu_pid=""
iso_key_pid=""
# When set, the boot waits for the Ventoy mode menu and sends keys.
# Ventoy 1.1.18 opens that menu after VTOY_MENU_TIMEOUT and then waits.
# The first line is "Boot in normal mode". "grub2" moves to the next line
# first. An empty mode sends Enter, which keeps the first line.
iso_key_sock=""
iso_key_mode=""

iso_die() {
	printf 'iso-test: %s\n' "$*" >&2
	exit 1
}

iso_qemu_init() {
	code=${FELDCORE_OVMF_CODE:-/usr/share/OVMF/OVMF_CODE_4M.fd}
	vars_src=${FELDCORE_OVMF_VARS:-/usr/share/OVMF/OVMF_VARS_4M.fd}
	[[ -f ${code} && -f ${vars_src} ]] || iso_die "OVMF firmware was not found (${code})"
	case ${code} in
	*.ms.fd|*.secboot.fd) iso_die "refusing Secure Boot firmware ${code}" ;;
	esac
	accel=kvm
	cpu=host
	if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
		accel=tcg
		cpu=qemu64
	fi
}

iso_stop_qemu() {
	if [[ -n ${iso_key_pid} ]] && kill -0 "${iso_key_pid}" 2>/dev/null; then
		kill "${iso_key_pid}" 2>/dev/null || true
		wait "${iso_key_pid}" 2>/dev/null || true
	fi
	iso_key_pid=""
	if [[ -n ${iso_qemu_pid} ]] && kill -0 "${iso_qemu_pid}" 2>/dev/null; then
		kill "${iso_qemu_pid}" 2>/dev/null || true
		wait "${iso_qemu_pid}" 2>/dev/null || true
	fi
	iso_qemu_pid=""
}

iso_send_keys() {
	local sock=$1
	shift
	python3 - "$sock" "$@" <<'PY'
import socket, sys, time
path = sys.argv[1]
keys = sys.argv[2:]
s = socket.socket(socket.AF_UNIX)
s.settimeout(2)
s.connect(path)
try:
    s.recv(4096)
except OSError:
    pass
for key in keys:
    s.sendall(f"sendkey {key}\n".encode())
    time.sleep(0.2)
    try:
        s.recv(4096)
    except OSError:
        pass
s.close()
PY
}

# Ventoy's mode menu is up well before a minute on this firmware. A second
# Enter covers a slow menu without typing into the installer shell: the
# motd check stops the guest as soon as the shell banner appears.
iso_start_enter_keys() {
	local sock=$1
	local mode=${iso_key_mode}
	(
		sleep 25
		if [[ ${mode} == grub2 ]]; then
			iso_send_keys "${sock}" down ret || true
		else
			iso_send_keys "${sock}" ret || true
		fi
		sleep 20
		iso_send_keys "${sock}" ret || true
	) &
	iso_key_pid=$!
}

iso_start_qemu() {
	local vars=$1
	shift
	cp -f "${vars_src}" "${vars}"
	local -a mon=()
	if [[ -n ${iso_key_sock} ]]; then
		rm -f "${iso_key_sock}"
		mon+=(-monitor "unix:${iso_key_sock},server,nowait")
	fi
	qemu-system-x86_64 \
		-machine "q35,accel=${accel}" \
		-cpu "${cpu}" \
		-m 2048 -smp 2 \
		-drive "if=pflash,format=raw,readonly=on,file=${code}" \
		-drive "if=pflash,format=raw,file=${vars}" \
		"${mon[@]}" \
		"$@" &
	iso_qemu_pid=$!
	if [[ -n ${iso_key_sock} ]]; then
		iso_start_enter_keys "${iso_key_sock}"
	fi
}

# Boot until the serial log contains the installer motd, then stop the guest.
iso_boot_motd() {
	local log=$1 timeout_s=$2
	shift 2
	local vars deadline
	vars=$(mktemp)
	: >"${log}"
	iso_start_qemu "${vars}" "$@" -serial "file:${log}" -display none -no-reboot
	deadline=$((SECONDS + timeout_s))
	while ((SECONDS < deadline)); do
		if grep -q 'FELDCORE installer\.' "${log}" 2>/dev/null; then
			iso_stop_qemu
			rm -f "${vars}"
			return 0
		fi
		if ! kill -0 "${iso_qemu_pid}" 2>/dev/null; then
			iso_qemu_pid=""
			rm -f "${vars}"
			iso_die "qemu exited before the installer motd. serial log:"$'\n'"$(tail -n 80 "${log}" 2>/dev/null || true)"
		fi
		sleep 2
	done
	iso_stop_qemu
	rm -f "${vars}"
	iso_die "timed out waiting for the installer motd. serial log:"$'\n'"$(tail -n 120 "${log}" 2>/dev/null || true)"
}

# Type one command after the motd. The command must print FELDCORE_INSTALL_DONE.
iso_boot_command() {
	local log=$1 timeout_s=$2 command=$3
	shift 3
	local vars sock deadline py
	vars=$(mktemp)
	sock=$(mktemp -u)
	: >"${log}"
	iso_start_qemu "${vars}" "$@" \
		-chardev "socket,id=ser,path=${sock},server=on,wait=off" \
		-serial chardev:ser \
		-display none -monitor none -no-reboot
	set +e
	PYTHONUNBUFFERED=1 python3 "${repo}/os/iso-serial.py" "${sock}" "${timeout_s}" "FELDCORE installer." "${command}" | tee "${log}"
	py=$?
	set -e
	deadline=$((SECONDS + 45))
	while ((SECONDS < deadline)); do
		if ! kill -0 "${iso_qemu_pid}" 2>/dev/null; then
			iso_qemu_pid=""
			break
		fi
		sleep 1
	done
	iso_stop_qemu
	rm -f "${vars}" "${sock}"
	if [[ ${py} -ne 0 ]]; then
		iso_die "the installer command did not finish. serial log:"$'\n'"$(tail -n 120 "${log}" 2>/dev/null || true)"
	fi
}
