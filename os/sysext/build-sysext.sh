#!/bin/bash
# Build one systemd-sysext image against a FELDCORE OS root.
# NVIDIA is not downloaded. Pass a local package directory with
# FELDCORE_NVIDIA_DIR, and set FELDCORE_NVIDIA_SYSEXT=1, to pack a driver
# tree you already have. libvirt and nvidia-container-toolkit can be built
# from Debian packages. The default image build does not run this script.
set -euo pipefail

id=${1:-}
root=${2:-}
out=${3:-}
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}/sysext-${id}

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
	echo "Unknown extension ${id}. This script builds libvirt, nvidia-container-toolkit, or a local nvidia tree." >&2
	exit 2
	;;
esac

[[ -f ${root}/etc/os-release ]] || { echo "ROOTFS has no os-release" >&2; exit 1; }
kver=$(find "${root}/lib/modules" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' -quit || true)

rm -rf "${work}"
mkdir -p "${work}/tree/usr/lib/extension-release.d" "${work}/tree/usr"
if [[ ${id} == nvidia ]]; then
	mkdir -p "${work}/tree/usr"
	cp -a "${FELDCORE_NVIDIA_DIR}/." "${work}/tree/usr/"
else
	sudo mkdir -p "${work}/apt/etc/apt" "${work}/apt/var/lib/dpkg" "${work}/tree"
	sudo cp "${root}/etc/apt/sources.list" "${work}/apt/etc/apt/sources.list" 2>/dev/null || true
	printf 'deb http://deb.debian.org/debian trixie main\n' | sudo tee "${work}/apt/etc/apt/sources.list" >/dev/null
	pkgs=$(grep -v '^[[:space:]]*#' "${repo}/os/sysext/${id}.packages" | tr '\n' ' ')
	sudo apt-get -o "Dir=${work}/apt" update
	sudo apt-get -o "Dir=${work}/apt" --download-only install -y ${pkgs}
	# The packages are downloaded for inspection and packed by dpkg into the tree.
	sudo mkdir -p "${work}/apt/var/cache/apt/archives"
	sudo dpkg-deb -x "${work}/apt/var/cache/apt/archives/"*.deb "${work}/tree" || {
		echo "No packages were downloaded for ${id}." >&2
		exit 1
	}
fi

# extension-release must match the image's ID and SYSEXT_LEVEL.
# A kernel pin is required when the extension ships modules.
{
	echo 'ID=feldcore'
	echo 'SYSEXT_LEVEL=1'
	if [[ ${id} == nvidia && -n ${kver} ]]; then
		echo "VERSION_ID=$(. "${root}/etc/os-release" && echo "${VERSION_ID}")"
	fi
} >"${work}/tree/usr/lib/extension-release.d/extension-release.${id}"

mksquashfs "${work}/tree" "${out}" -noappend -comp zstd
echo "wrote ${out}"
