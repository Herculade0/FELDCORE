#!/bin/bash
# Build one systemd-sysext image against a FELDCORE OS root.
# NVIDIA's kernel driver is not downloaded. Pass a local package directory
# with FELDCORE_NVIDIA_DIR, and set FELDCORE_NVIDIA_SYSEXT=1, to pack a
# driver tree you already have.
set -euo pipefail

id=${1:-}
root=${2:-}
out=${3:-}
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}/sysext-${id}
mirror=${FELDCORE_DEBIAN_MIRROR:-http://deb.debian.org/debian}

if [[ -z ${id} || -z ${root} || -z ${out} ]]; then
	echo "Usage: build-sysext.sh ID ROOTFS OUTPUT.raw" >&2
	exit 2
fi
case ${id} in
nvidia)
	[[ ${FELDCORE_NVIDIA_SYSEXT:-0} == 1 && -n ${FELDCORE_NVIDIA_DIR:-} ]] || {
		echo "Refusing to fetch the NVIDIA driver. Set FELDCORE_NVIDIA_SYSEXT=1 and FELDCORE_NVIDIA_DIR." >&2
		exit 1
	}
	;;
libvirt | nvidia-container-toolkit) ;;
*)
	echo "Unknown extension ${id}." >&2
	exit 2
	;;
esac

[[ -f ${root}/etc/os-release ]] || { echo "ROOTFS has no os-release" >&2; exit 1; }
command -v mksquashfs >/dev/null || { echo "missing mksquashfs" >&2; exit 1; }

sudo rm -rf "${work}"
mkdir -p "${work}/tree/usr/lib/extension-release.d"

if [[ ${id} == nvidia ]]; then
	cp -a "${FELDCORE_NVIDIA_DIR}/." "${work}/tree/usr/"
else
	sudo rm -rf "${work}/dl"
	sudo mkdir -p \
		"${work}/dl/etc/apt/apt.conf.d" \
		"${work}/dl/etc/apt/sources.list.d" \
		"${work}/dl/etc/apt/keyrings" \
		"${work}/dl/var/lib/apt/lists/partial" \
		"${work}/dl/var/cache/apt/archives/partial" \
		"${work}/dl/var/lib/dpkg" \
		"${work}/tree"
	printf 'APT::Install-Recommends "false";\nAPT::Install-Suggests "false";\n' |
		sudo tee "${work}/dl/etc/apt/apt.conf.d/00no-recommends" >/dev/null
	keyring=${root}/usr/share/keyrings/debian-archive-keyring.gpg
	[[ -f ${keyring} ]] || keyring=/usr/share/keyrings/debian-archive-keyring.gpg
	[[ -f ${keyring} ]] || { echo "debian-archive-keyring.gpg was not found" >&2; exit 1; }
	sudo cp "${keyring}" "${work}/dl/etc/apt/keyrings/debian-archive-keyring.gpg"
	sudo tee "${work}/dl/etc/apt/sources.list" >/dev/null <<EOF
deb [signed-by=${work}/dl/etc/apt/keyrings/debian-archive-keyring.gpg] ${mirror} trixie main non-free-firmware
deb [signed-by=${work}/dl/etc/apt/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian-security trixie-security main non-free-firmware
deb [signed-by=${work}/dl/etc/apt/keyrings/debian-archive-keyring.gpg] ${mirror} trixie-updates main non-free-firmware
EOF
	if [[ ${id} == nvidia-container-toolkit ]]; then
		curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey |
			sudo gpg --dearmor -o "${work}/dl/etc/apt/keyrings/nvidia-container-toolkit.gpg"
		sudo tee "${work}/dl/etc/apt/sources.list.d/nvidia-container-toolkit.list" >/dev/null <<EOF
deb [signed-by=${work}/dl/etc/apt/keyrings/nvidia-container-toolkit.gpg] https://nvidia.github.io/libnvidia-container/stable/deb/amd64 /
EOF
	fi
	pkgs=$(grep -v '^[[:space:]]*#' "${repo}/os/sysext/${id}.packages" | grep -v '^[[:space:]]*$' | tr '\n' ' ')
	sudo apt-get -o "Dir=${work}/dl" update
	sudo apt-get -o "Dir=${work}/dl" install --download-only -y --no-install-recommends ${pkgs}
	shopt -s nullglob
	debs=("${work}/dl/var/cache/apt/archives/"*.deb)
	((${#debs[@]})) || { echo "No packages were downloaded for ${id}." >&2; exit 1; }
	for deb in "${debs[@]}"; do
		sudo dpkg-deb -x "${deb}" "${work}/tree"
	done
fi

{
	echo 'ID=feldcore'
	echo 'SYSEXT_LEVEL=1'
	if [[ ${id} == nvidia ]]; then
		# A driver tree ships modules, so it is pinned to the image kernel.
		kver=$(find "${root}/lib/modules" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' -quit || true)
		[[ -n ${kver} ]] || { echo "the rootfs has no kernel modules" >&2; exit 1; }
		echo "KERNEL=${kver}"
	fi
} | {
	# dpkg-deb -x extracts package trees as root, so usr/lib may now be
	# root-owned. Create the dir and write the release file with the same
	# privilege so the redirect cannot hit EACCES.
	sudo mkdir -p "${work}/tree/usr/lib/extension-release.d"
	sudo tee "${work}/tree/usr/lib/extension-release.d/extension-release.${id}" >/dev/null
}

mkdir -p "$(dirname "${out}")"

# dpkg-deb -x (and cp -a from a root-owned driver dir) populate the tree as
# root, so it is root-owned. mksquashfs must run with the same privilege;
# otherwise it cannot stat tree/usr, tree/lib and tree/etc, prints
# "Permission denied, ignoring" for each, and silently writes an empty ~4 KiB
# image. First assert the payload actually landed, so an empty tree fails the
# build loudly instead of shipping a hollow extension.
payload=$(sudo find "${work}/tree" -mindepth 1 \
	-path "${work}/tree/usr/lib/extension-release.d" -prune -o \
	-type f -print -quit 2>/dev/null || true)
[[ -n ${payload} ]] || {
	echo "sysext ${id}: no payload files under ${work}/tree; refusing to pack an empty image." >&2
	exit 1
}

sudo mksquashfs "${work}/tree" "${out}" -noappend -comp zstd
sudo chown "$(id -u):$(id -g)" "${out}"

# Guard against a silently empty image whatever the cause. A real extension is
# tens of KiB or larger; an empty squashfs is about 4 KiB. This floor matches
# os/check-sysexts.sh.
bytes=$(stat -c %s "${out}")
((bytes >= 65536)) || {
	echo "sysext ${id}: ${out} is ${bytes} bytes, too small to be a real extension (empty squashfs?)." >&2
	exit 1
}

echo "wrote ${out} (${bytes} bytes)"
