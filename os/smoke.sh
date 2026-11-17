#!/bin/bash
# Check that a built root (or a running appliance) contains the tools the
# daemon expects. --root does not start dockerd. --start does, on a live system.
set -euo pipefail

mode=root
root=
while [[ $# -gt 0 ]]; do
	case $1 in
	--root)
		mode=root
		root=${2:-}
		shift 2
		;;
	--start)
		mode=start
		shift
		;;
	*)
		echo "usage: smoke.sh --root ROOTFS | --start" >&2
		exit 2
		;;
	esac
done

bins=(
	dockerd docker containerd
	git
	smbd smbpasswd testparm
	exportfs
	restic rclone mount.cifs
	wg
	smartctl mdadm
	mkfs.btrfs btrfs mkfs.xfs xfs_scrub hdparm
	parted sgdisk wipefs fstrim lsblk blkid findmnt mkfs.ext4
	ufw
	cloudflared curl bash
	systemctl timedatectl hostnamectl
	lspci
	rtl_eeprom
)

run() {
	if [[ ${mode} == root ]]; then
		sudo chroot "${root}" "$@"
	else
		"$@"
	fi
}

if [[ ${mode} == root ]]; then
	[[ -n ${root} && -d ${root} ]] || { echo "smoke: --root needs a rootfs" >&2; exit 2; }
fi

fail=0
for b in "${bins[@]}"; do
	if ! run sh -c "command -v ${b} >/dev/null"; then
		echo "smoke: missing ${b}" >&2
		fail=1
	fi
done

if [[ ${mode} == root ]]; then
	run docker --version || fail=1
	run docker compose version || fail=1
	run dockerd --version || fail=1
	if ! grep -q '/var/lib/feldcore/docker' "${root}/etc/docker/daemon.json"; then
		echo "smoke: daemon.json does not set the state data-root" >&2
		fail=1
	fi
	if ! grep -q '/var/lib/feldcore/containerd' "${root}/etc/containerd/config.toml"; then
		echo "smoke: containerd root is not on the state partition" >&2
		fail=1
	fi
	for unit in docker.service containerd.service docker.socket feldcore-smoke.service; do
		if [[ ! -L ${root}/etc/systemd/system/multi-user.target.wants/${unit} ]]; then
			echo "smoke: ${unit} is not enabled" >&2
			fail=1
		fi
	done
	if run sh -c 'command -v zpool >/dev/null'; then
		echo "smoke: zpool is in the image; ZFS was left out on purpose" >&2
		fail=1
	fi
else
	run docker --version || fail=1
	run docker compose version || fail=1
	if ! systemctl is-active --quiet docker; then
		systemctl enable --now docker
	fi
	run docker info >/dev/null
fi

if [[ ${fail} != 0 ]]; then
	echo "smoke: failed" >&2
	exit 1
fi
echo "smoke: ok"
